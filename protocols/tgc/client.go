// Package tgc wraps gotd; it talks to the UI only through model.Event.
package tgc

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/contrib/middleware/floodwait"
	"github.com/gotd/log"
	"github.com/gotd/td/constant"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/message/unpack"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/telegram/updates/hook"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/govlog/ttyloom/internal/emoji"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
)

type Config struct {
	AppID       int
	AppHash     string
	BotToken    string
	SessionPath string
}

type Client struct {
	model.Poster
	cfg    Config
	client *telegram.Client
	api    *tg.Client
	peers  *peers.Manager
	gaps   *updates.Manager
	hook   telegram.UpdateHandler
	sender *message.Sender
	dl     *downloader.Downloader
	dlSem  chan struct{}
	// ulSem : uploads apart from the downloads — a send must never wait for
	// three files being fetched.
	ulSem chan struct{}
	me    *tg.User
	// loggedIn : tg.UpdateLoginToken signal, armed from New (the QR is offered
	// before the gap handler runs).
	loggedIn qrlogin.LoggedIn

	mu sync.Mutex // guards seen, floodUntil and gifUser
	// ponytail: session cache with no eviction, a few hundred bytes per peer
	// seen; move to an LRU if an account ever sees tens of thousands.
	seen       map[int64]peers.Peer // peers already seen, by TDLib id: names with no network
	floodUntil time.Time            // FLOOD_WAIT breaker
	gifUser    tg.InputUserClass    // @gif once resolved (gifs.go)
}

func New(cfg Config, events chan<- model.Event) *Client {
	c := &Client{Poster: model.Poster{Events: events}, cfg: cfg, dl: downloader.NewDownloader(),
		dlSem: make(chan struct{}, 3), ulSem: make(chan struct{}, 2), seen: map[int64]peers.Peer{}}
	lg := logger{c}
	// The gap handler is built before the client (it is its UpdateHandler); it
	// hands over to c.hook, wired after the peers are made.
	c.gaps = updates.New(updates.Config{
		Handler: telegram.UpdateHandlerFunc(func(ctx context.Context, u tg.UpdatesClass) error { return c.hook.Handle(ctx, u) }),
		Logger:  lg,
	})
	c.client = telegram.NewClient(cfg.AppID, cfg.AppHash, telegram.Options{
		SessionStorage: &sessionFile{FileStorage: session.FileStorage{Path: cfg.SessionPath}},
		UpdateHandler:  c.gaps,
		Logger:         lg,
		Middlewares: []telegram.Middleware{
			// The hook first: the updates given back by our own RPCs (send, read, join)
			// feed the gap handler instead of firing a getDifference.
			hook.UpdateHook(c.gaps.Handle),
			// pts given back by messages.affected* (readHistory at each window change).
			hook.AffectedHook(c.gaps),
			floodwait.NewSimpleWaiter().WithMaxWait(15 * time.Second),
		},
		OnConnectionState: func(s telegram.ConnectionState) {
			switch s {
			case telegram.ConnectionStateReady:
				c.PostNB(model.EvConnected{})
			case telegram.ConnectionStateDisconnected:
				c.PostNB(model.EvDisconnected{})
			}
		},
	})
	c.api = c.client.API()
	c.peers = peers.Options{Logger: lg}.Build(c.api)
	c.sender = message.NewSender(c.api)
	d := tg.NewUpdateDispatcher()
	c.loggedIn = qrlogin.OnLoginToken(d)
	c.registerHandlers(d)
	c.hook = c.peers.UpdateHook(d)
	return c
}

// Caps : Telegram does everything the UI knows how to show.
func (c *Client) Caps() model.Caps { return model.AllCaps() }

// Run blocks: connection, authentication, then the update loop.
func (c *Client) Run(ctx context.Context) error {
	return c.client.Run(ctx, func(ctx context.Context) error {
		if c.cfg.BotToken == "" {
			st, err := c.client.Auth().Status(ctx)
			if err != nil {
				return fmt.Errorf(i18n.T("auth_error"), err)
			}
			if !st.Authorized {
				phone, err := c.qrLogin(ctx)
				if err != nil {
					return err
				}
				// QR taken: IfNecessary sees the authorisation and asks for nothing.
				flow := auth.NewFlow(authenticator{c: c, phone: phone}, auth.SendCodeOptions{})
				if err := c.client.Auth().IfNecessary(ctx, flow); err != nil {
					return fmt.Errorf(i18n.T("auth_error"), err)
				}
			}
		} else {
			st, err := c.client.Auth().Status(ctx)
			if err != nil {
				return fmt.Errorf(i18n.T("auth_bot_error"), err)
			}
			if !st.Authorized {
				if _, err := c.client.Auth().Bot(ctx, c.cfg.BotToken); err != nil {
					return fmt.Errorf(i18n.T("auth_bot_error"), err)
				}
			}
		}
		me, err := c.client.Self(ctx)
		if err != nil {
			return err
		}
		c.me = me
		return c.gaps.Run(ctx, c.api, me.ID, updates.AuthOptions{IsBot: me.Bot, OnStart: func(ctx context.Context) {
			c.Post(model.EvReady{SelfID: me.ID, SelfName: nick(c.peers.User(me)), Bot: me.Bot})
			go c.loadReactions(ctx)
		}})
	})
}

// --- authentication ---

// Logout ends the account session on the server (auth.logOut): the next Run
// goes through the QR flow again. The session file stays — its auth key is
// still good for a new login.
func (c *Client) Logout(ctx context.Context) error {
	_, err := c.api.AuthLogOut(ctx)
	return err
}

// qrLogin offers the QR login before the phone flow: the QR goes to the UI
// while the prompt waits for Enter, and the first of the two ways that works
// closes the other. It gives back the text already typed (a phone number,
// maybe) when the user prefers the phone, "" when the QR worked.
func (c *Client) qrLogin(ctx context.Context) (string, error) {
	qctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		err := errors.New(i18n.T("qr_interrupted")) // kept if Auth panics
		defer func() { done <- err }()
		defer c.Guard("qrLogin", nil)
		// show is called again at each token renewal: the UI replaces the overlay.
		// The URL is worth an open session, so it is never logged.
		_, err = c.client.QR().Auth(qctx, c.loggedIn, func(_ context.Context, t qrlogin.Token) error {
			c.Post(model.EvQR{URL: t.URL(), Expires: t.Expires()})
			return nil
		})
	}()
	reply := make(chan string, 1)
	c.Post(model.EvAuthPrompt{Question: i18n.T("qr_prompt"), Reply: reply})
	select {
	case line := <-reply:
		line = strings.TrimSpace(line)
		cancel()
		<-done // no EvQR after this point
		c.Post(model.EvQRDone{})
		return line, nil
	case err := <-done:
		c.Post(model.EvQRDone{}) // closes the overlay and the prompt left with nobody to answer
		switch {
		case err == nil:
			return "", nil
		case ctx.Err() != nil:
			return "", err
		case tgerr.Is(err, "SESSION_PASSWORD_NEEDED"):
			// QR taken by the phone, but the account has a 2FA password.
			pwd, err := authenticator{c: c}.Password(ctx)
			if err != nil {
				return "", err
			}
			if _, err := c.client.Auth().Password(ctx, pwd); err != nil {
				return "", fmt.Errorf(i18n.T("twofa_error"), err)
			}
			return "", nil
		}
		// The QR alone failed: the phone flow takes over.
		c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("qr_login_error", err)})
		return "", nil
	}
}

type authenticator struct {
	c *Client
	// phone : text already typed in front of the QR — given back as it is at the
	// first ask, otherwise the user would have to type their number again.
	phone string
}

func (a authenticator) ask(ctx context.Context, q string, secret bool) (string, error) {
	reply := make(chan string, 1)
	a.c.Post(model.EvAuthPrompt{Question: q, Secret: secret, Reply: reply})
	select {
	case s := <-reply:
		return strings.TrimSpace(s), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (a authenticator) Phone(ctx context.Context) (string, error) {
	if a.phone != "" {
		return a.phone, nil
	}
	return a.ask(ctx, i18n.T("prompt_phone"), false)
}
func (a authenticator) Password(ctx context.Context) (string, error) {
	return a.ask(ctx, i18n.T("prompt_2fa"), true)
}
func (a authenticator) Code(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
	return a.ask(ctx, i18n.T("prompt_code"), false)
}
func (a authenticator) AcceptTermsOfService(context.Context, tg.HelpTermsOfService) error { return nil }
func (a authenticator) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New(i18n.T("no_telegram_account"))
}

// --- gotd logs → debug window (ERROR also in window 0) ---

type logger struct{ c *Client }

func (l logger) Enabled(_ context.Context, lvl log.Level) bool { return lvl >= log.LevelWarn }
func (l logger) Log(ctx context.Context, lvl log.Level, msg string, attrs ...log.Attr) {
	if !l.Enabled(ctx, lvl) { // log.Helper calls Log without filtering
		return
	}
	var b strings.Builder
	b.WriteString(msg)
	for _, a := range attrs {
		fmt.Fprintf(&b, " %s=%s", a.Key, a.Value.String())
	}
	l.c.PostNB(model.EvLog{Level: lvl.String(), Msg: b.String()})
}

// --- updates ---

// safe : a panic in an update handler (remote data) becomes an EvLog, never a crash.
func (c *Client) safe(name string, f func() error) (err error) {
	defer c.Guard(name, nil)
	return f()
}

func (c *Client) registerHandlers(d tg.UpdateDispatcher) {
	d.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		return c.safe("OnNewMessage", func() error { return c.onMessage(ctx, u.Message, peer.EntitiesFromUpdate(e), false) })
	})
	d.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewChannelMessage) error {
		return c.safe("OnNewChannelMessage", func() error { return c.onMessage(ctx, u.Message, peer.EntitiesFromUpdate(e), false) })
	})
	d.OnEditMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateEditMessage) error {
		return c.safe("OnEditMessage", func() error { return c.onMessage(ctx, u.Message, peer.EntitiesFromUpdate(e), true) })
	})
	d.OnEditChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateEditChannelMessage) error {
		return c.safe("OnEditChannelMessage", func() error { return c.onMessage(ctx, u.Message, peer.EntitiesFromUpdate(e), true) })
	})
	d.OnDeleteMessages(func(_ context.Context, _ tg.Entities, u *tg.UpdateDeleteMessages) error {
		return c.safe("OnDeleteMessages", func() error {
			c.Post(model.EvDeleted{IDs: u.Messages})
			return nil
		})
	})
	d.OnDeleteChannelMessages(func(_ context.Context, _ tg.Entities, u *tg.UpdateDeleteChannelMessages) error {
		return c.safe("OnDeleteChannelMessages", func() error {
			var id constant.TDLibPeerID
			id.Channel(u.ChannelID)
			c.Post(model.EvDeleted{ChatID: int64(id), IDs: u.Messages})
			return nil
		})
	})
	d.OnUserTyping(func(_ context.Context, e tg.Entities, u *tg.UpdateUserTyping) error {
		return c.safe("OnUserTyping", func() error {
			var id constant.TDLibPeerID
			id.User(u.UserID)
			c.Post(model.EvTyping{ChatID: int64(id), Who: c.nameOf(peer.EntitiesFromUpdate(e), &tg.PeerUser{UserID: u.UserID})})
			return nil
		})
	})
	d.OnChatUserTyping(func(_ context.Context, e tg.Entities, u *tg.UpdateChatUserTyping) error {
		return c.safe("OnChatUserTyping", func() error {
			var id constant.TDLibPeerID
			id.Chat(u.ChatID)
			c.Post(model.EvTyping{ChatID: int64(id), Who: c.nameOf(peer.EntitiesFromUpdate(e), u.FromID)})
			return nil
		})
	})
	d.OnChannelUserTyping(func(_ context.Context, e tg.Entities, u *tg.UpdateChannelUserTyping) error {
		return c.safe("OnChannelUserTyping", func() error {
			var id constant.TDLibPeerID
			id.Channel(u.ChannelID)
			c.Post(model.EvTyping{ChatID: int64(id), Who: c.nameOf(peer.EntitiesFromUpdate(e), u.FromID)})
			return nil
		})
	})
	d.OnUserStatus(func(_ context.Context, _ tg.Entities, u *tg.UpdateUserStatus) error {
		return c.safe("OnUserStatus", func() error {
			c.Post(model.EvPresence{UserID: u.UserID, Status: formatStatus(u.Status, time.Now())})
			return nil
		})
	})
	d.OnReadHistoryOutbox(func(_ context.Context, _ tg.Entities, u *tg.UpdateReadHistoryOutbox) error {
		return c.safe("OnReadHistoryOutbox", func() error {
			c.Post(model.EvReadOutbox{ChatID: tdlibID(u.Peer), MaxID: u.MaxID})
			return nil
		})
	})
	d.OnReadChannelOutbox(func(_ context.Context, _ tg.Entities, u *tg.UpdateReadChannelOutbox) error {
		return c.safe("OnReadChannelOutbox", func() error {
			var id constant.TDLibPeerID
			id.Channel(u.ChannelID)
			c.Post(model.EvReadOutbox{ChatID: int64(id), MaxID: u.MaxID})
			return nil
		})
	})
	d.OnReadHistoryInbox(func(_ context.Context, _ tg.Entities, u *tg.UpdateReadHistoryInbox) error {
		return c.safe("OnReadHistoryInbox", func() error {
			c.Post(model.EvReadInbox{ChatID: tdlibID(u.Peer), MaxID: u.MaxID,
				Unread: u.StillUnreadCount, HasUnread: true})
			return nil
		})
	})
	d.OnReadChannelInbox(func(_ context.Context, _ tg.Entities, u *tg.UpdateReadChannelInbox) error {
		return c.safe("OnReadChannelInbox", func() error {
			var id constant.TDLibPeerID
			id.Channel(u.ChannelID)
			c.Post(model.EvReadInbox{ChatID: int64(id), MaxID: u.MaxID,
				Unread: u.StillUnreadCount, HasUnread: true})
			return nil
		})
	})
	d.OnMessageReactions(func(_ context.Context, _ tg.Entities, u *tg.UpdateMessageReactions) error {
		return c.safe("OnMessageReactions", func() error {
			// The TDLib id is computed without resolving the peer; when the chat is
			// not open, the UI simply ignores the event.
			c.Post(model.EvReactions{ChatID: tdlibID(u.Peer), ID: u.MsgID, Reactions: reactionsOf(u.Reactions.Results)})
			return nil
		})
	})
}

func (c *Client) onMessage(ctx context.Context, mc tg.MessageClass, ent peer.Entities, edit bool) error {
	c.rememberAll(ent)
	m, chat, ok := c.convert(ctx, mc, ent, nil)
	if !ok {
		return nil
	}
	c.fillReplies(ctx, chat, []*model.Msg{&m})
	if edit {
		c.Post(model.EvEditMessage{Msg: m})
	} else {
		c.Post(model.EvNewMessage{Msg: m, Chat: chat})
	}
	return nil
}

func nick(p peers.Peer) string {
	if u, ok := p.Username(); ok && u != "" {
		return u
	}
	return p.VisibleName()
}

// tdlibID gives the TDLib id of a raw peer, with no lookup at all.
func tdlibID(p tg.PeerClass) int64 {
	var id constant.TDLibPeerID
	switch v := p.(type) {
	case *tg.PeerUser:
		id.User(v.UserID)
	case *tg.PeerChat:
		id.Chat(v.ChatID)
	case *tg.PeerChannel:
		id.Channel(v.ChannelID)
	}
	return int64(id)
}

// isMin : "min" entity — seen from afar (searchGlobal, updates), its
// access_hash is worth nothing for an RPC. See https://core.telegram.org/api/min
func isMin(p peers.Peer) bool {
	switch v := p.(type) {
	case peers.User:
		return v.Raw().Min
	case peers.Channel:
		return v.Raw().Min
	}
	return false // peers.Chat (small group): no access_hash, so no min
}

// remember keeps a peer seen for the messages that follow. A "min" entity
// never replaces a peer already known: it would break the RPCs that use it
// (history, send) while we had something better.
func (c *Client) remember(p peers.Peer) peers.Peer {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = map[int64]peers.Peer{}
	}
	id := int64(p.TDLibPeerID())
	if old, ok := c.seen[id]; ok && isMin(p) {
		return old
	}
	c.seen[id] = p
	return p
}

// rememberAll keeps every entity of a batch (history, search, update).
func (c *Client) rememberAll(ent peer.Entities) {
	for _, u := range ent.Users() {
		c.remember(c.peers.User(u))
	}
	for _, ch := range ent.Chats() {
		c.remember(c.peers.Chat(ch))
	}
	for _, ch := range ent.Channels() {
		c.remember(c.peers.Channel(ch))
	}
}

// peerOf gives the peer from the entities of the batch, else from the peers
// already seen. Never a network call: resolving each unknown sender through
// users.getUsers during the sync fires a FLOOD_WAIT (hundreds of calls).
func (c *Client) peerOf(ent peer.Entities, p tg.PeerClass) (peers.Peer, bool) {
	switch v := p.(type) {
	case *tg.PeerUser:
		if u, ok := ent.Users()[v.UserID]; ok {
			return c.remember(c.peers.User(u)), true
		}
	case *tg.PeerChat:
		if ch, ok := ent.Chats()[v.ChatID]; ok {
			return c.remember(c.peers.Chat(ch)), true
		}
	case *tg.PeerChannel:
		if ch, ok := ent.Channels()[v.ChannelID]; ok {
			return c.remember(c.peers.Channel(ch)), true
		}
	default:
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	pr, ok := c.seen[tdlibID(p)]
	return pr, ok
}

// nameOf gives the name of a peer with no network call, "?" when unknown.
func (c *Client) nameOf(ent peer.Entities, p tg.PeerClass) string {
	if p == nil {
		return "?"
	}
	if pr, ok := c.peerOf(ent, p); ok {
		return nick(pr)
	}
	return "?"
}

// errFlood : the breaker is open. Built at each read: the language can change
// during a session.
func errFlood() error { return errors.New(i18n.T("flood_wait_active")) }

// floodOK : false while the FLOOD_WAIT breaker is open.
func (c *Client) floodOK() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().After(c.floodUntil)
}

// floodTrip opens the breaker when err is a FLOOD_WAIT; one WARN per
// opening, not one per refused call.
func (c *Client) floodTrip(err error) {
	d, ok := tgerr.AsFloodWait(err)
	if !ok {
		return
	}
	now := time.Now()
	c.mu.Lock()
	first := now.After(c.floodUntil)
	if u := now.Add(d); u.After(c.floodUntil) {
		c.floodUntil = u
	}
	c.mu.Unlock()
	if first {
		c.PostNB(model.EvLog{Level: "WARN", Msg: i18n.T("flood_wait_tripped", d.Round(time.Second))})
	}
}

// resolvePeer : network lookup, refused during a FLOOD_WAIT.
func (c *Client) resolvePeer(ctx context.Context, p tg.PeerClass) (peers.Peer, error) {
	if !c.floodOK() {
		return nil, errFlood()
	}
	pr, err := c.peers.ResolvePeer(ctx, p)
	if err != nil {
		c.floodTrip(err)
		return nil, err
	}
	return c.remember(pr), nil
}

// peer types back the opaque handle of a chat. A handle from another backend
// gives nil: the gotd call it feeds fails on its own, no branch to add here.
func (c *Client) peer(chat *model.Chat) tg.InputPeerClass {
	p, _ := chat.Peer.(tg.InputPeerClass)
	return p
}

func (c *Client) chatOf(p peers.Peer) *model.Chat {
	ch := &model.Chat{ID: int64(p.TDLibPeerID()), Title: p.VisibleName(), Peer: p.InputPeer()}
	ch.Username, _ = p.Username()
	ch.PhotoLoc = peerPhoto(p)
	switch v := p.(type) {
	case peers.User:
		ch.Kind = model.ChatUser
	case peers.Chat:
		ch.Kind = model.ChatGroup
	case peers.Channel:
		ch.Kind, ch.Channel = model.ChatGroup, true
		if v.IsBroadcast() {
			ch.Kind = model.ChatChannel
		}
	}
	return ch
}

// convert turns a raw message into a model.Msg + chat. ent carries the
// entities of the batch (history, search, update); a non-nil chat = chat
// already known, no peer is resolved by network. ok=false when it stays unknown.
func (c *Client) convert(ctx context.Context, mc tg.MessageClass, ent peer.Entities, chat *model.Chat) (model.Msg, *model.Chat, bool) {
	var peerID, fromID tg.PeerClass
	var id, date int
	var out bool
	switch v := mc.(type) {
	case *tg.Message:
		peerID, fromID, id, date, out = v.PeerID, v.FromID, v.ID, v.Date, v.Out
	case *tg.MessageService:
		peerID, fromID, id, date, out = v.PeerID, v.FromID, v.ID, v.Date, v.Out
	default:
		return model.Msg{}, nil, false
	}
	if chat == nil {
		cp, ok := c.peerOf(ent, peerID)
		if !ok { // update of a chat never seen: the only network lookup left
			var err error
			if cp, err = c.resolvePeer(ctx, peerID); err != nil {
				return model.Msg{}, nil, false
			}
		}
		chat = c.chatOf(cp)
	}
	m := model.Msg{ID: id, ChatID: chat.ID, Date: unixTime(date), Out: out}
	if fromID != nil {
		if fp, ok := c.peerOf(ent, fromID); ok {
			m.From, m.FromID = nick(fp), int64(fp.TDLibPeerID())
			m.FromPhoto = peerPhoto(fp)
		}
	}
	if m.From == "" {
		switch {
		case out && c.me != nil:
			me := c.peers.User(c.me)
			m.From, m.FromID, m.FromPhoto = nick(me), c.me.ID, peerPhoto(me)
		case fromID == nil && chat.Kind == model.ChatUser:
			// Private chat: from_id missing on the MTProto side (the incoming
			// sender is the peer for sure). Avatar = the one of the chat, already resolved.
			m.From, m.FromID, m.FromPhoto = chat.Title, chat.ID, chat.PhotoLoc
		case fromID == nil:
			// ponytail: channel post, FromID stays 0 (nick colour): no avatar,
			// only the gutter. The avatar of the channel is already in the
			// sidebar.
			m.From = chat.Title
		default:
			m.From = "?" // sender missing from the entities: no network call
		}
	}
	switch v := mc.(type) {
	case *tg.MessageService:
		m.Service = m.From + " " + describeAction(v.Action)
	case *tg.Message:
		m.Text, m.Entities, m.Edited = v.Message, spansOf(v.Message, v.Entities), v.EditDate != 0
		if a, ok := v.GetPostAuthor(); ok && fromID == nil {
			m.From = a
		}
		m.Media = mediaOf(v.Media)
		if f, ok := v.GetFwdFrom(); ok {
			switch {
			case f.FromName != "":
				m.FwdFrom = f.FromName
			case f.FromID != nil:
				m.FwdFrom = c.nameOf(ent, f.FromID)
			}
		}
		if r, ok := v.GetReplyTo(); ok {
			if h, ok := r.(*tg.MessageReplyHeader); ok && h.ReplyToMsgID != 0 {
				m.Reply = &model.Quote{ID: h.ReplyToMsgID, Text: oneLine(h.QuoteText)}
			}
		}
		if rs, ok := v.GetReactions(); ok {
			m.Reactions = reactionsOf(rs.Results)
		}
	}
	return m, chat, true
}

// fillReplies gets in one request the quoted messages whose text we lack.
func (c *Client) fillReplies(ctx context.Context, chat *model.Chat, msgs []*model.Msg) {
	want := map[int][]*model.Msg{}
	var ids []tg.InputMessageClass
	for _, m := range msgs {
		if m.Reply == nil || m.Reply.Text != "" {
			continue
		}
		if _, seen := want[m.Reply.ID]; !seen {
			ids = append(ids, &tg.InputMessageID{ID: m.Reply.ID})
		}
		want[m.Reply.ID] = append(want[m.Reply.ID], m)
	}
	if len(ids) == 0 || !c.floodOK() {
		return
	}
	var res tg.MessagesMessagesClass
	var err error
	if ch, ok := c.peer(chat).(*tg.InputPeerChannel); ok {
		res, err = c.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}, ID: ids})
	} else {
		res, err = c.api.MessagesGetMessages(ctx, ids)
	}
	if err != nil {
		c.floodTrip(err)
		return
	}
	mod, ok := res.AsModified()
	if !ok {
		return
	}
	c.peers.Apply(ctx, mod.GetUsers(), mod.GetChats())
	ent := peer.NewEntities(tg.UserClassArray(mod.GetUsers()).UserToMap(),
		tg.ChatClassArray(mod.GetChats()).ChatToMap(), tg.ChatClassArray(mod.GetChats()).ChannelToMap())
	c.rememberAll(ent)
	for _, mc := range mod.GetMessages() {
		q, _, ok := c.convert(ctx, mc, ent, chat)
		if !ok {
			continue
		}
		text := q.Text
		if text == "" && q.Media != nil {
			text = q.Media.Label
		}
		if q.Service != "" {
			text = q.Service
		}
		for _, m := range want[q.ID] {
			m.Reply.From, m.Reply.Text = q.From, oneLine(text)
		}
	}
}

func (c *Client) applyEntities(ctx context.Context, ent peer.Entities) {
	c.rememberAll(ent)
	var users []tg.UserClass
	for _, u := range ent.Users() {
		users = append(users, u)
	}
	var chats []tg.ChatClass
	for _, ch := range ent.Chats() {
		chats = append(chats, ch)
	}
	for _, ch := range ent.Channels() {
		chats = append(chats, ch)
	}
	c.peers.Apply(ctx, users, chats)
}

// --- asynchronous requests ---

func (c *Client) LoadDialogs(ctx context.Context) {
	go func() {
		defer c.Guard("LoadDialogs", func(err string) { c.Post(model.EvDialogs{Err: err}) })
		elems, err := query.GetDialogs(c.api).BatchSize(100).Collect(ctx)
		if err != nil {
			c.Post(model.EvDialogs{Err: err.Error()})
			return
		}
		var chats []*model.Chat
		for _, e := range elems {
			d, ok := e.Dialog.(*tg.Dialog)
			if !ok {
				continue
			}
			c.applyEntities(ctx, e.Entities)
			// From the entities of the batch: FromInputPeer would do one
			// users.getUsers per dialog (the cache of the peer manager is a no-op).
			p, ok := c.peerOf(e.Entities, d.Peer)
			if !ok {
				continue
			}
			ch := c.chatOf(p)
			ch.Unread, ch.ReadInboxMaxID, ch.TopMessage = d.UnreadCount, d.ReadInboxMaxID, d.TopMessage
			ch.ReadOutboxMaxID = d.ReadOutboxMaxID
			ch.Pinned = d.Pinned
			if e.Last != nil {
				ch.LastDate = unixTime(e.Last.GetDate())
			}
			chats = append(chats, ch)
		}
		c.Post(model.EvDialogs{Chats: chats})
	}()
}

// LoadHistory loads limit messages before beforeID (0 = the newest ones).
func (c *Client) LoadHistory(ctx context.Context, chat *model.Chat, beforeID, limit int) {
	snapshot := *chat // The UI can update chat fields while the request runs.
	chat = &snapshot
	go c.history(ctx, chat, beforeID, 0, limit, false)
}

// LoadHistoryAround loads a page of limit messages centred on id (half
// before, half after): a precise jump must not end on its target. Direct
// call, the gotd builder does not expose add_offset.
func (c *Client) LoadHistoryAround(ctx context.Context, chat *model.Chat, id, limit int) {
	snapshot := *chat // The UI can update chat fields while the request runs.
	chat = &snapshot
	go func() {
		ev := model.EvHistory{ChatID: chat.ID, Around: true, AroundID: id}
		defer c.Guard("LoadHistoryAround", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		if !c.floodOK() {
			ev.Err = errFlood().Error()
			c.Post(ev)
			return
		}
		res, err := c.api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer: c.peer(chat), OffsetID: id, AddOffset: -limit / 2, Limit: limit})
		if err != nil {
			c.floodTrip(err)
			ev.Err = err.Error()
			c.Post(ev)
			return
		}
		mod, ok := res.AsModified()
		if !ok {
			c.Post(ev) // messagesNotModified: empty page, the appointment closes
			return
		}
		c.peers.Apply(ctx, mod.GetUsers(), mod.GetChats())
		ent := peer.NewEntities(tg.UserClassArray(mod.GetUsers()).UserToMap(),
			tg.ChatClassArray(mod.GetChats()).ChatToMap(), tg.ChatClassArray(mod.GetChats()).ChannelToMap())
		c.rememberAll(ent)
		var msgs []model.Msg
		for _, mc := range mod.GetMessages() {
			if m, _, ok := c.convert(ctx, mc, ent, chat); ok {
				msgs = append(msgs, m)
			}
		}
		ptrs := make([]*model.Msg, len(msgs))
		for i := range msgs {
			ptrs[i] = &msgs[i]
		}
		c.fillReplies(ctx, chat, ptrs)
		slices.Reverse(msgs) // server: newest → oldest
		ev.Msgs = msgs
		c.Post(ev)
	}()
}

// LoadHistorySince loads up to limit messages with an id above minID (sync of
// a chat at start). minID zero: the recent history.
func (c *Client) LoadHistorySince(ctx context.Context, chat *model.Chat, minID, limit int) {
	snapshot := *chat // The UI can update chat fields while the request runs.
	chat = &snapshot
	go c.history(ctx, chat, 0, minID, limit, true)
}

// history : common body of the two loads. It runs in its own goroutine.
func (c *Client) history(ctx context.Context, chat *model.Chat, beforeID, minID, limit int, since bool) {
	ev := model.EvHistory{ChatID: chat.ID, Older: beforeID > 0, Since: since}
	defer c.Guard("history", func(err string) {
		ev.Err = err
		c.Post(ev)
	})
	// The server caps the batch at 100; above that, the gotd iterator takes the
	// short batch for the end of the history and stops (lastBatch).
	q := query.Messages(c.api).GetHistory(c.peer(chat)).BatchSize(min(limit, 100))
	if beforeID > 0 {
		q = q.OffsetID(beforeID)
	}
	it := q.Iter()
	var msgs []model.Msg
	for len(msgs) < limit && it.Next(ctx) {
		e := it.Value()
		c.applyEntities(ctx, e.Entities)
		mc, ok := e.Msg.(tg.MessageClass) // Elem.Msg: NotEmptyMessage subset
		if !ok {
			continue
		}
		// GetHistory does not expose min_id: the read goes from the newest to the
		// oldest, the first message already known stops everything.
		if mc.GetID() <= minID {
			break
		}
		if m, _, ok := c.convert(ctx, mc, e.Entities, chat); ok {
			msgs = append(msgs, m)
		}
	}
	if err := it.Err(); err != nil {
		ev.Err = err.Error()
		c.Post(ev)
		return
	}
	ptrs := make([]*model.Msg, len(msgs))
	for i := range msgs {
		ptrs[i] = &msgs[i]
	}
	c.fillReplies(ctx, chat, ptrs)
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 { // server: newest → oldest
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	ev.Msgs = msgs
	// A short sync proves nothing about the older messages: Done (w.Full) stays
	// false, otherwise scrolling up in the window would be blocked.
	ev.Done = !since && len(msgs) < limit
	c.Post(ev)
}

// Search runs messages.search in a chat, limit results at most, from the
// oldest to the newest. The default filter of the builder
// (InputMessagesFilterEmpty) takes every kind of message.
func (c *Client) Search(ctx context.Context, chat *model.Chat, q string, limit int) {
	snapshot := *chat // The UI can update chat fields while the request runs.
	chat = &snapshot
	go func() {
		ev := model.EvSearch{ChatID: chat.ID, Query: q}
		defer c.Guard("Search", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		it := query.Messages(c.api).Search(c.peer(chat)).Q(q).BatchSize(min(limit, 100)).Iter()
		var msgs []model.Msg
		for len(msgs) < limit && it.Next(ctx) {
			e := it.Value()
			c.applyEntities(ctx, e.Entities)
			mc, ok := e.Msg.(tg.MessageClass) // Elem.Msg: NotEmptyMessage subset
			if !ok {
				continue
			}
			if m, _, ok := c.convert(ctx, mc, e.Entities, chat); ok {
				msgs = append(msgs, m)
			}
		}
		if err := it.Err(); err != nil {
			ev.Err = err.Error()
			c.Post(ev)
			return
		}
		ptrs := make([]*model.Msg, len(msgs))
		for i := range msgs {
			ptrs[i] = &msgs[i]
		}
		c.fillReplies(ctx, chat, ptrs)
		slices.Reverse(msgs) // server: newest → oldest
		ev.Msgs = msgs
		c.Post(ev)
	}()
}

// SearchGlobal runs messages.searchGlobal over every chat, limit results at
// most (from the newest to the oldest, server order). The names come from the
// entities of the answer: a message whose chat is not there is dropped rather
// than resolved through the network (one result per unknown chat = as many
// calls, so a sure FLOOD_WAIT).
func (c *Client) SearchGlobal(ctx context.Context, q string, limit int) {
	go func() {
		ev := model.EvSearchGlobal{Query: q}
		defer c.Guard("SearchGlobal", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		if !c.floodOK() {
			ev.Err = errFlood().Error()
			c.Post(ev)
			return
		}
		it := query.Messages(c.api).SearchGlobal().Q(q).BatchSize(min(limit, 100)).Iter()
		// Walk cap: three batches at most. Without it, a request whose results
		// are all dropped would page over the whole account.
		for seen := 0; len(ev.Hits) < limit && seen < 3*limit && it.Next(ctx); seen++ {
			e := it.Value()
			c.rememberAll(e.Entities)
			mc, ok := e.Msg.(tg.MessageClass) // Elem.Msg: NotEmptyMessage subset
			if !ok {
				continue
			}
			if _, ok := c.peerOf(e.Entities, e.Msg.GetPeerID()); !ok {
				continue // unknown chat: convert() would go and resolve it through the network
			}
			m, chat, ok := c.convert(ctx, mc, e.Entities, nil)
			if !ok {
				continue // nothing to show and nothing to open
			}
			text := m.Text
			if text == "" { // service message, or media with no caption
				text = m.Service
				if text == "" && m.Media != nil {
					text = m.Media.Label
				}
			}
			ev.Hits = append(ev.Hits, model.SearchHit{Chat: chat, MsgID: m.ID, Date: m.Date, From: m.From, Text: text})
		}
		if err := it.Err(); err != nil {
			c.floodTrip(err)
			ev.Err = err.Error()
		}
		c.Post(ev)
	}()
}

// Contacts runs contacts.getContacts — the contacts of the account, asked
// only once per session (the "new chat" overlay). Hash 0: the whole list,
// never a delta.
func (c *Client) Contacts(ctx context.Context) {
	go func() {
		var ev model.EvContacts
		defer c.Guard("Contacts", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		if !c.floodOK() {
			ev.Err = errFlood().Error()
			c.Post(ev)
			return
		}
		res, err := c.api.ContactsGetContacts(ctx, 0)
		if err != nil {
			c.floodTrip(err)
			ev.Err = err.Error()
			c.Post(ev)
			return
		}
		full, ok := res.(*tg.ContactsContacts) // NotModified: impossible with hash 0
		if !ok {
			c.Post(ev)
			return
		}
		for _, uc := range full.Users {
			usr, ok := uc.AsNotEmpty()
			if !ok {
				continue
			}
			ev.Peers = append(ev.Peers, c.chatOf(c.remember(c.peers.User(usr))))
		}
		c.Post(ev)
	}()
}

// SearchContacts runs contacts.search — contacts, public rooms and channels
// whose name matches q, limit results at most. The peers come from the
// entities of the answer (access hash included): no network lookup after,
// neither to show the list nor to open the chat.
func (c *Client) SearchContacts(ctx context.Context, q string, limit int) {
	go func() {
		ev := model.EvContactsFound{Query: q}
		defer c.Guard("SearchContacts", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		if !c.floodOK() {
			ev.Err = errFlood().Error()
			c.Post(ev)
			return
		}
		res, err := c.api.ContactsSearch(ctx, &tg.ContactsSearchRequest{Q: q, Limit: limit})
		if err != nil {
			c.floodTrip(err)
			ev.Err = err.Error()
			c.Post(ev)
			return
		}
		ent := peer.EntitiesFromResult(res)
		c.rememberAll(ent)
		seen := map[int64]bool{}
		for _, p := range slices.Concat(res.MyResults, res.Results) { // my contacts first
			pr, ok := c.peerOf(ent, p)
			if !ok || seen[int64(pr.TDLibPeerID())] {
				continue
			}
			seen[int64(pr.TDLibPeerID())] = true
			ev.Peers = append(ev.Peers, c.chatOf(pr))
		}
		c.Post(ev)
	}()
}

func (c *Client) Send(ctx context.Context, chat *model.Chat, text string, tmpID int64) {
	go func() {
		defer c.Guard("Send", func(err string) { c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID, Err: err}) })
		id, err := unpack.MessageID(c.sender.To(c.peer(chat)).Text(ctx, text))
		ev := model.EvSent{ChatID: chat.ID, TmpID: tmpID, ID: id}
		if err != nil {
			ev.Err = err.Error()
		}
		c.Post(ev)
	}()
}

// SendStyled : like Send, with formatting on the segments (pre, italic…).
func (c *Client) SendStyled(ctx context.Context, chat *model.Chat, segs []model.Seg, tmpID int64) {
	opts := stylingOf(segs)
	go func() {
		defer c.Guard("SendStyled", func(err string) { c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID, Err: err}) })
		id, err := unpack.MessageID(c.sender.To(c.peer(chat)).StyledText(ctx, opts...))
		ev := model.EvSent{ChatID: chat.ID, TmpID: tmpID, ID: id}
		if err != nil {
			ev.Err = err.Error()
		}
		c.Post(ev)
	}()
}

// SendPre : like Send, but text goes as a code block (multiline paste).
func (c *Client) SendPre(ctx context.Context, chat *model.Chat, text string, tmpID int64) {
	c.SendStyled(ctx, chat, []model.Seg{{Text: text, Kind: model.SegPre}}, tmpID)
}

// EditStyled : like Edit, with formatting on the segments (fences of the draft).
func (c *Client) EditStyled(ctx context.Context, chat *model.Chat, id int, segs []model.Seg) {
	opts := stylingOf(segs)
	go func() {
		defer c.Guard("EditStyled", func(err string) { c.Post(model.EvEdited{ChatID: chat.ID, ID: id, Err: err}) })
		ev := model.EvEdited{ChatID: chat.ID, ID: id}
		if _, err := c.sender.To(c.peer(chat)).Edit(id).StyledText(ctx, opts...); err != nil {
			ev.Err = err.Error()
		}
		c.Post(ev)
	}()
}

// Edit replaces the text of one of my messages.
func (c *Client) Edit(ctx context.Context, chat *model.Chat, id int, text string) {
	go func() {
		defer c.Guard("Edit", func(err string) { c.Post(model.EvEdited{ChatID: chat.ID, ID: id, Err: err}) })
		ev := model.EvEdited{ChatID: chat.ID, ID: id}
		if _, err := c.sender.To(c.peer(chat)).Edit(id).Text(ctx, text); err != nil {
			ev.Err = err.Error()
		}
		c.Post(ev)
	}()
}

// Delete deletes a message for everybody. The server update comes too: the UI
// drops the duplicate on Deleted.
func (c *Client) Delete(ctx context.Context, chat *model.Chat, id int) {
	go func() {
		defer c.Guard("Delete", nil)
		var err error
		if ch, ok := c.peer(chat).(*tg.InputPeerChannel); ok {
			_, err = c.api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
				Channel: &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}, ID: []int{id}})
		} else {
			_, err = c.api.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{Revoke: true, ID: []int{id}})
		}
		if err != nil {
			c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("delete_error", err)})
			return
		}
		c.Post(model.EvDeleted{ChatID: chat.ID, IDs: []int{id}})
	}()
}

// loadReactions gets the global list of the reactions of the account, once
// per connection. The inactive ones are dropped, and the premium ones too
// outside a premium account: the server would refuse them.
func (c *Client) loadReactions(ctx context.Context) {
	defer c.Guard("loadReactions", nil)
	res, err := c.api.MessagesGetAvailableReactions(ctx, 0)
	if err != nil {
		c.floodTrip(err)
	}
	list, _ := res.(*tg.MessagesAvailableReactions)
	if err != nil || list == nil {
		c.Post(model.EvReactionsList{Emojis: model.DefaultReactions})
		return
	}
	premium := c.me != nil && c.me.Premium
	out := make([]string, 0, len(list.Reactions))
	for _, r := range list.Reactions {
		if r.Inactive || (r.Premium && !premium) {
			continue
		}
		out = append(out, r.Reaction)
	}
	if len(out) == 0 {
		out = model.DefaultReactions
	}
	c.Post(model.EvReactionsList{Emojis: out})
}

// reactionReason gives the text to show in the window for a refused reaction,
// "" for the other errors (already in the log).
func reactionReason(err error) string {
	switch {
	case tgerr.Is(err, "REACTION_INVALID"):
		return i18n.T("reaction_not_available")
	case tgerr.Is(err, "REACTIONS_TOO_MANY"):
		return i18n.T("reaction_too_many")
	case tgerr.Is(err, "PREMIUM_ACCOUNT_REQUIRED"):
		// In "Saved Messages", a reaction is a tag: Premium only.
		return i18n.T("reaction_premium")
	}
	return ""
}

// React sends my reaction on a message; emoji == "" removes my reaction. The
// state up to date also comes through OnMessageReactions (server, or echo of
// this RPC): EvReactions replaces the list, posting twice does nothing.
func (c *Client) React(ctx context.Context, chat *model.Chat, id int, e string) {
	e = emoji.Base(e) // Telegram refuses ❤️ (with a variation selector), not ❤
	go func() {
		defer c.Guard("React", nil)
		var reaction []tg.ReactionClass
		if e != "" {
			reaction = []tg.ReactionClass{&tg.ReactionEmoji{Emoticon: e}}
		}
		upd, err := c.api.MessagesSendReaction(ctx, &tg.MessagesSendReactionRequest{
			Peer: c.peer(chat), MsgID: id, Reaction: reaction})
		if err != nil {
			c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("react_error", err)})
			if r := reactionReason(err); r != "" {
				c.Post(model.EvReactionFailed{ChatID: chat.ID, ID: id, Emoji: e, Reason: r})
			}
			return
		}
		if u := reactionUpdate(upd); u != nil {
			c.Post(model.EvReactions{ChatID: chat.ID, ID: id, Reactions: reactionsOf(u.Reactions.Results)})
		}
		// The update does not always carry the state after (emoji removed,
		// reaction set from another client): the re-read of the message
		// decides.
		if m, err := c.getMessage(ctx, chat, id); err == nil {
			r, ok := m.GetReactions()
			if !ok {
				// Field missing (flag 20): the server says nothing about the
				// reactions, and above all not that there are none left. Posting
				// an empty list here would wipe the one of the update.
				c.Post(model.EvLog{Level: "DEBUG", Msg: i18n.T("react_refetch_missing", id)})
				return
			}
			c.Post(model.EvReactions{ChatID: chat.ID, ID: id, Reactions: reactionsOf(r.Results), Refetch: true})
		}
		// ponytail: no EvLog when reactionUpdate finds nothing here: upd has also
		// just gone through the hook.UpdateHook middleware (see New()), which feeds
		// it back to the same dispatcher → OnMessageReactions → EvReactions. If a
		// reaction ever stays visibly stuck, this is where the EvLog should be
		// added, not before.
	}()
}

// getMessage re-reads a message (reactions up to date, info). No peer is
// resolved: only the message counts.
func (c *Client) getMessage(ctx context.Context, chat *model.Chat, id int) (*tg.Message, error) {
	if !c.floodOK() {
		return nil, errFlood()
	}
	ids := []tg.InputMessageClass{&tg.InputMessageID{ID: id}}
	var res tg.MessagesMessagesClass
	var err error
	if ch, ok := c.peer(chat).(*tg.InputPeerChannel); ok {
		res, err = c.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}, ID: ids})
	} else {
		res, err = c.api.MessagesGetMessages(ctx, ids)
	}
	if err != nil {
		c.floodTrip(err)
		return nil, err
	}
	mod, ok := res.AsModified()
	if ok {
		for _, mc := range mod.GetMessages() {
			if m, ok := mc.(*tg.Message); ok && m.ID == id {
				return m, nil
			}
		}
	}
	return nil, fmt.Errorf(i18n.T("message_not_found"), id)
}

// reactorsLimit : cap of messages.getMessageReactionsList, with no paging.
// ponytail: above that, the line shows the first 50 people who reacted; page
// only if somebody asks for the full list.
const reactorsLimit = 50

// reactors gives "who reacted", on one line. In a private chat the authors
// come from the message itself (me or the peer), with no network; elsewhere
// one single request, the names taken from its entities.
func (c *Client) reactors(ctx context.Context, p tg.InputPeerClass, kind model.ChatKind, title string, rs []model.Reaction, id int) string {
	if kind == model.ChatUser {
		out := make([]string, 0, len(rs))
		for _, r := range rs {
			who := title
			if r.Mine {
				who = i18n.T("me")
			}
			out = append(out, r.Emoji+" "+who)
		}
		return strings.Join(out, " · ")
	}
	if !c.floodOK() {
		return i18n.T("unavailable")
	}
	res, err := c.api.MessagesGetMessageReactionsList(ctx, &tg.MessagesGetMessageReactionsListRequest{
		Peer: p, ID: id, Limit: reactorsLimit})
	if err != nil {
		c.floodTrip(err)
		return i18n.T("unavailable")
	}
	ent := c.entities(res.Users)
	var order []string
	names := map[string][]string{}
	for _, r := range res.Reactions {
		e := emojiOf(r.Reaction)
		if e == "" {
			continue
		}
		if _, seen := names[e]; !seen {
			order = append(order, e)
		}
		who := c.nameOf(ent, r.PeerID)
		if r.My {
			who = i18n.T("me")
		}
		names[e] = append(names[e], who)
	}
	if len(order) == 0 {
		return i18n.T("unavailable") // channel with anonymous reactions, or nothing to show
	}
	out := make([]string, 0, len(order))
	for _, e := range order {
		out = append(out, e+" "+strings.Join(names[e], ", "))
	}
	return strings.Join(out, " · ")
}

// Info gives the information lines of a message (key "i"). readMax is the
// ReadOutboxMaxID of the chat (my messages), readInboxMax its ReadInboxMaxID
// (received messages) — only the UI keeps them up to date, and they are never
// read again from chat in this goroutine.
func (c *Client) Info(ctx context.Context, chat *model.Chat, id, readMax, readInboxMax int) {
	// Title and Kind change under remember() (UI goroutine): read here.
	title, kind := chat.Title, chat.Kind
	go func() {
		ev := model.EvInfo{ChatID: chat.ID, ID: id}
		fail := func(err string) {
			ev.Lines = []string{i18n.T("info_error", err)}
			c.Post(ev)
		}
		defer c.Guard("Info", fail)
		m, err := c.getMessage(ctx, chat, id)
		if err != nil {
			fail(err.Error())
			return
		}
		ev.Lines = append(ev.Lines, i18n.T("info_sent_at", i18n.LocalTime(unixTime(m.Date))))
		if d, ok := m.GetEditDate(); ok && d != 0 {
			ev.Lines = append(ev.Lines, i18n.T("info_edited_at", i18n.LocalTime(unixTime(d))))
		}
		if m.Out {
			if id <= readMax {
				ev.Lines = append(ev.Lines, i18n.T("info_read"))
			} else {
				ev.Lines = append(ev.Lines, i18n.T("info_sent"))
			}
			if kind == model.ChatGroup { // neither a private chat nor a channel
				ev.Lines = append(ev.Lines, i18n.T("info_read_by", c.readers(ctx, chat, id)))
			}
		} else {
			if id <= readInboxMax {
				ev.Lines = append(ev.Lines, i18n.T("info_read_by_me"))
			} else {
				ev.Lines = append(ev.Lines, i18n.T("info_unread"))
			}
			if v, ok := m.GetViews(); ok && v > 0 {
				ev.Lines = append(ev.Lines, i18n.T("info_views", v))
			}
		}
		if r, ok := m.GetReactions(); ok {
			if rs := reactionsOf(r.Results); len(rs) > 0 {
				ev.Lines = append(ev.Lines, i18n.T("info_reactions", c.reactors(ctx, c.peer(chat), kind, title, rs, id)))
			}
		}
		ev.Lines = append(ev.Lines, i18n.T("info_id", id)) // both branches
		c.Post(ev)
	}()
}

// readers gives the members who read the message, named from the entities
// already seen (never a network lookup: an active group would list dozens).
func (c *Client) readers(ctx context.Context, chat *model.Chat, id int) string {
	if !c.floodOK() {
		return i18n.T("unavailable")
	}
	parts, err := c.api.MessagesGetMessageReadParticipants(ctx,
		&tg.MessagesGetMessageReadParticipantsRequest{Peer: c.peer(chat), MsgID: id})
	if err != nil {
		c.floodTrip(err)
		return i18n.T("unavailable")
	}
	if len(parts) == 0 {
		return i18n.T("nobody")
	}
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		names = append(names, c.nameOf(peer.Entities{}, &tg.PeerUser{UserID: p.UserID}))
	}
	return strings.Join(names, ", ")
}

// WhoRead gives the "who read" line of the hover popup of the tick: read
// date in a private chat, names of the readers in a group, plain read state
// elsewhere. readMax is the ReadOutboxMaxID kept by the UI.
func (c *Client) WhoRead(ctx context.Context, chat *model.Chat, id, readMax int) {
	kind := chat.Kind // Kind changes under remember() (UI goroutine): read here
	go func() {
		defer c.Guard("WhoRead", nil)
		ev := model.EvWho{ChatID: chat.ID, ID: id}
		switch {
		case id > readMax:
			ev.Text = i18n.T("info_unread")
		case kind == model.ChatUser:
			ev.Text = i18n.T("info_read")
			if c.floodOK() {
				res, err := c.api.MessagesGetOutboxReadDate(ctx,
					&tg.MessagesGetOutboxReadDateRequest{Peer: c.peer(chat), MsgID: id})
				if err != nil {
					c.floodTrip(err) // privacy of the peer: the plain "read" stays
				} else {
					ev.Text = i18n.T("who_read_at", i18n.LocalTime(unixTime(res.Date)))
				}
			}
		case kind == model.ChatGroup:
			ev.Text = i18n.T("info_read_by", c.readers(ctx, chat, id))
		default: // channel: no reader list
			ev.Text = i18n.T("info_read")
		}
		c.Post(ev)
	}()
}

// WhoReacted gives the "who reacted" line of the hover popup of a reaction.
func (c *Client) WhoReacted(ctx context.Context, chat *model.Chat, id int, rs []model.Reaction) {
	kind, title := chat.Kind, chat.Title
	go func() {
		defer c.Guard("WhoReacted", nil)
		c.Post(model.EvWho{ChatID: chat.ID, ID: id, React: true,
			Text: c.reactors(ctx, c.peer(chat), kind, title, rs, id)})
	}()
}

// SendReply : like Send, as a reply to the message replyTo.
func (c *Client) SendReply(ctx context.Context, chat *model.Chat, text string, replyTo int, tmpID int64) {
	go func() {
		defer c.Guard("SendReply", func(err string) { c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID, Err: err}) })
		id, err := unpack.MessageID(c.sender.To(c.peer(chat)).Reply(replyTo).Text(ctx, text))
		ev := model.EvSent{ChatID: chat.ID, TmpID: tmpID, ID: id}
		if err != nil {
			ev.Err = err.Error()
		}
		c.Post(ev)
	}()
}

// SendPhoto sends a local image as a photo, caption optional.
// SendFile does the same as a document (name and mime kept).
//
// EvSent like the text sends: the UI has already shown the send as a pending
// line, and the receipt unpends it. The echo of the sent message comes back
// besides, through the updates of the RPC (fed back by hook.UpdateHook, see
// New); whichever of the two comes first, the window keeps one line.
// removeAfter : work file (pasted image), erased once the send worked; a file
// chosen by the user never is.
func (c *Client) SendPhoto(ctx context.Context, chat *model.Chat, path, caption string, removeAfter bool, tmpID int64) {
	c.upload(ctx, chat, path, caption, true, removeAfter, tmpID)
}

func (c *Client) SendFile(ctx context.Context, chat *model.Chat, path, caption string, removeAfter bool, tmpID int64) {
	c.upload(ctx, chat, path, caption, false, removeAfter, tmpID)
}

func (c *Client) upload(ctx context.Context, chat *model.Chat, path, caption string, photo, removeAfter bool, tmpID int64) {
	go func() {
		fail := func(err string) {
			c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID,
				Err: i18n.T("upload_error", filepath.Base(path), err)})
		}
		defer c.Guard("upload", fail)
		// Own semaphore: a send never waits behind three downloads.
		c.ulSem <- struct{}{}
		defer func() { <-c.ulSem }()
		f, err := uploader.NewUploader(c.api).WithProgress(&upProgress{c: c, last: -1}).FromPath(ctx, path)
		if err != nil {
			fail(err.Error())
			return
		}
		var capt []styling.StyledTextOption
		if caption != "" {
			capt = append(capt, styling.Plain(caption))
		}
		b := c.sender.To(c.peer(chat))
		var id int
		if photo {
			id, err = unpack.MessageID(b.UploadedPhoto(ctx, f, capt...))
		} else {
			// No ForceFile: the server thus keeps the preview of a video or a gif.
			mt := mimeOf(path)
			doc := message.UploadedDocument(f, capt...).
				Filename(filepath.Base(path)).MIME(mt)
			// A video without DocumentAttributeVideo shows up as a plain file
			// at the other end. ffprobe absent or failing: plain document.
			if strings.HasPrefix(mt, "video/") || strings.HasPrefix(mt, "audio/") {
				if w, h, dur, perr := media.ProbeVideo(ctx, path); perr == nil {
					doc = doc.Attributes(sendAttrs(mt, w, h, dur)...)
				}
			}
			id, err = unpack.MessageID(b.Media(ctx, doc))
		}
		if err != nil {
			fail(err.Error()) // failure: the file stays, the send can be tried again
			return
		}
		if removeAfter {
			os.Remove(path)
		}
		// The id, like a text send: without it Window.Sent cannot tell the
		// pending line from the echo of the server, and both would stay.
		c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID, ID: id})
	}()
}

// sendAttrs gives the attributes that make a media play at the other end
// instead of hanging there as a file. w, h and dur come from ffprobe; a probe
// that gave nothing usable (dur <= 0) leaves the document as it is.
func sendAttrs(mt string, w, h int, dur time.Duration) []tg.DocumentAttributeClass {
	switch {
	case strings.HasPrefix(mt, "video/") && dur > 0 && w > 0 && h > 0:
		return []tg.DocumentAttributeClass{&tg.DocumentAttributeVideo{
			SupportsStreaming: true,
			Duration:          dur.Seconds(),
			W:                 w,
			H:                 h,
		}}
	// w > 0 on a sound file means an embedded cover: ffprobe hands back the
	// picture as a video stream. The file then stays a plain document rather
	// than going out as a track that is really a video (.webm is announced
	// audio/webm by the system table).
	case strings.HasPrefix(mt, "audio/") && dur > 0 && w <= 0:
		return []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{
			Duration: int(dur.Seconds()),
		}}
	}
	return nil
}

// mimeOf gives the type of a local file from its extension — the server uses
// it for the preview (video, gif). Unknown extension: octet-stream.
func mimeOf(path string) string {
	if m := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); m != "" {
		return strings.SplitN(m, ";", 2)[0]
	}
	return "application/octet-stream"
}

// upProgress : progress of an upload, posted at each whole percent. postNB: a
// late status bar is better than a blocked upload. The uploader keeps its
// single thread by default: Chunk is not concurrent.
type upProgress struct {
	c    *Client
	last int
}

func (p *upProgress) Chunk(_ context.Context, s uploader.ProgressState) error {
	if s.Total <= 0 {
		return nil
	}
	pct := int(s.Uploaded * 100 / s.Total)
	if pct == p.last {
		return nil
	}
	p.last = pct
	p.c.PostNB(model.EvUpload{Text: i18n.T("upload_progress", pct)})
	return nil
}

// Resolve takes an @username, a t.me/username link, a phone number, or the id
// of a peer already seen (a member with no @username clicked in the F3 box).
// join=true joins a channel that was left.
func (c *Client) Resolve(ctx context.Context, q string, join bool) {
	go func() {
		defer c.Guard("Resolve", func(err string) { c.Post(model.EvChat{Query: q, Err: err}) })
		// TDLib id (= user id for a private chat): the peer and its access
		// hash are already kept, so no network.
		if id, err := strconv.ParseInt(q, 10, 64); err == nil {
			c.mu.Lock()
			p, ok := c.seen[id]
			c.mu.Unlock()
			if ok {
				c.Post(model.EvChat{Query: q, Chat: c.chatOf(p)})
				return
			}
		}
		if !c.floodOK() {
			c.Post(model.EvChat{Query: q, Err: errFlood().Error()})
			return
		}
		p, err := c.peers.Resolve(ctx, q)
		if err != nil {
			c.floodTrip(err)
			c.Post(model.EvChat{Query: q, Err: err.Error()})
			return
		}
		c.remember(p)
		if ch, ok := p.(peers.Channel); ok && join && ch.Left() {
			if _, err := c.api.ChannelsJoinChannel(ctx, ch.InputChannel()); err != nil {
				c.Post(model.EvChat{Query: q, Err: err.Error()})
				return
			}
		}
		c.Post(model.EvChat{Query: q, Chat: c.chatOf(p)})
	}()
}

// Download downloads m.Loc to path (3 in parallel at most). A file already there = success at once.
func (c *Client) Download(ctx context.Context, m *model.Media, path string) {
	go func() {
		defer c.Guard("Download", func(err string) { c.Post(model.EvDownloaded{Media: m, Err: err}) })
		c.dlSem <- struct{}{}
		defer func() { <-c.dlSem }()
		if _, err := os.Stat(path); err == nil {
			c.Post(model.EvDownloaded{Media: m, Path: path})
			return
		}
		// 0700 like the rest of the project (config, cache, logs, maps): a
		// private media must not be readable by the other accounts of the
		// machine. os.CreateTemp gives a 0600 file with a name of its own —
		// two downloads of the same media no longer walk on each other, and
		// no pre-existing symbolic link is followed.
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			c.Post(model.EvDownloaded{Media: m, Err: err.Error()})
			return
		}
		f, err := os.CreateTemp(dir, ".part-*")
		if err != nil {
			c.Post(model.EvDownloaded{Media: m, Err: err.Error()})
			return
		}
		tmp := f.Name()
		loc, ok := m.Loc.(tg.InputFileLocationClass)
		if !ok { // handle of another backend: nothing to download here
			f.Close()
			os.Remove(tmp)
			c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("media_foreign")})
			c.Post(model.EvDownloaded{Media: m, Err: i18n.T("media_foreign")})
			return
		}
		_, derr := c.dl.Download(c.api, loc).Parallel(ctx, f)
		cerr := f.Close()
		if derr != nil || cerr != nil {
			os.Remove(tmp)
			c.Post(model.EvDownloaded{Media: m, Err: cmp.Or(derr, cerr).Error()})
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			os.Remove(tmp)
			c.Post(model.EvDownloaded{Media: m, Err: err.Error()})
			return
		}
		c.Post(model.EvDownloaded{Media: m, Path: path})
	}()
}

// DownloadMap gets the OSM map of m (Lat/Long); there is no Telegram Loc — an
// HTTP call to tile.openstreetmap.org through media.Tile, with the same
// semaphore and the same event back as Download: the UI has nothing to tell apart.
func (c *Client) DownloadMap(ctx context.Context, m *model.Media, path string) {
	go func() {
		defer c.Guard("DownloadMap", func(err string) { c.Post(model.EvDownloaded{Media: m, Err: err}) })
		c.dlSem <- struct{}{}
		defer func() { <-c.dlSem }()
		if err := media.Tile(ctx, m.Lat, m.Long, path); err != nil {
			c.Post(model.EvDownloaded{Media: m, Err: err.Error()})
			return
		}
		c.Post(model.EvDownloaded{Media: m, Path: path})
	}()
}

func (c *Client) MarkRead(ctx context.Context, chat *model.Chat, maxID int) {
	go func() {
		defer c.Guard("MarkRead", nil)
		if ch, ok := c.peer(chat).(*tg.InputPeerChannel); ok {
			c.api.ChannelsReadHistory(ctx, &tg.ChannelsReadHistoryRequest{
				Channel: &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}, MaxID: maxID})
			return
		}
		c.api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{Peer: c.peer(chat), MaxID: maxID})
	}()
}

// Typing tells "typing" in chat, or cancels it (cancel=true, when sending).
// Fire and forget like MarkRead: no error comes up, a missed typing hint does
// not deserve an error path.
func (c *Client) Typing(ctx context.Context, chat *model.Chat, cancel bool) {
	go func() {
		defer c.Guard("Typing", nil)
		var action tg.SendMessageActionClass = &tg.SendMessageTypingAction{}
		if cancel {
			action = &tg.SendMessageCancelAction{}
		}
		c.api.MessagesSetTyping(ctx, &tg.MessagesSetTypingRequest{Peer: c.peer(chat), Action: action})
	}()
}

// Whois runs users.getFullUser on a private chat → lines ready to show, and
// the presence on the way (the account may show up in no update at all).
func (c *Client) Whois(ctx context.Context, chat *model.Chat) {
	go func() {
		ev := model.EvWhois{ChatID: chat.ID}
		defer c.Guard("Whois", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		var id tg.InputUserClass
		switch p := c.peer(chat).(type) {
		case *tg.InputPeerUser:
			id = &tg.InputUser{UserID: p.UserID, AccessHash: p.AccessHash}
		case *tg.InputPeerSelf:
			id = &tg.InputUserSelf{}
		default:
			ev.Err = i18n.T("users_only")
			c.Post(ev)
			return
		}
		full, err := c.api.UsersGetFullUser(ctx, id)
		if err != nil {
			ev.Err = err.Error()
			c.Post(ev)
			return
		}
		c.peers.Apply(ctx, full.Users, full.Chats)
		var u *tg.User
		for _, uc := range full.Users {
			if x, ok := uc.(*tg.User); ok && x.ID == full.FullUser.ID {
				u = x
			}
		}
		ev.Lines = whoisLines(u, full.FullUser, time.Now())
		if u != nil {
			if st := formatStatus(u.Status, time.Now()); st != "" {
				c.Post(model.EvPresence{UserID: u.ID, Status: st})
			}
		}
		c.Post(ev)
	}()
}

// whoisLines formats the profile. u can be missing (empty users): only the
// content of UserFull is then shown.
func whoisLines(u *tg.User, f tg.UserFull, now time.Time) []string {
	var lines []string
	if u != nil {
		name := oneLine(strings.TrimSpace(u.FirstName + " " + u.LastName))
		if name == "" {
			name = i18n.T("whois_no_name")
		}
		if u.Username != "" {
			name += " (@" + u.Username + ")"
		}
		lines = append(lines, name)
		if u.Phone != "" {
			lines = append(lines, i18n.T("whois_phone", strings.TrimPrefix(u.Phone, "+")))
		}
	}
	if f.About != "" {
		lines = append(lines, i18n.T("whois_bio", oneLine(f.About)))
	}
	if u != nil {
		if st := formatStatus(u.Status, now); st != "" {
			lines = append(lines, st)
		}
	}
	if n := f.CommonChatsCount; n > 0 {
		key := "whois_common_one"
		if n > 1 {
			key = "whois_common_many"
		}
		lines = append(lines, i18n.T(key, n))
	}
	var flags []string
	if u != nil {
		for _, x := range []struct {
			on    bool
			label string
		}{{u.Bot, i18n.T("flag_bot")}, {u.Verified, i18n.T("flag_verified")}, {u.Premium, i18n.T("flag_premium")}} {
			if x.on {
				flags = append(flags, x.label)
			}
		}
	}
	if len(flags) > 0 {
		lines = append(lines, strings.Join(flags, " · "))
	}
	return lines
}
