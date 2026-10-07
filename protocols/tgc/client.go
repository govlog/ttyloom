// Package tgc wraps gotd; it talks to the UI only through model.Event.
package tgc

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/contrib/middleware/floodwait"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/telegram/updates/hook"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/i18n"
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
	// me : the account, set by Run once logged in; read by the requests,
	// which the UI can start before that.
	me atomic.Pointer[tg.User]
	// loggedIn : tg.UpdateLoginToken signal, armed from New (the QR is offered
	// before the gap handler runs).
	loggedIn qrlogin.LoggedIn

	mu sync.Mutex // guards seen, floodUntil, gifUser and fills
	// ponytail: session cache with no eviction, a few hundred bytes per peer
	// seen; move to an LRU if an account ever sees tens of thousands.
	seen       map[int64]peers.Peer // peers already seen, by TDLib id: names with no network
	floodUntil time.Time            // FLOOD_WAIT breaker
	gifUser    tg.InputUserClass    // @gif once resolved (gifs.go)
	// fills : the quote reads in flight (fillLater), by message {chat, id},
	// each with its number; a newer version of the message drops the older read.
	fills   map[[2]int64]uint64
	fillSem chan struct{} // quote reads running (fillLater)
	fillSeq uint64
}

func New(cfg Config, events chan<- model.Event) *Client {
	c := &Client{Poster: model.Poster{Events: events}, cfg: cfg, dl: downloader.NewDownloader(),
		dlSem: make(chan struct{}, 3), ulSem: make(chan struct{}, 2), seen: map[int64]peers.Peer{}, fills: map[[2]int64]uint64{},
		fillSem: make(chan struct{}, 2)}
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

// Resolve takes an @username, a t.me/username link, a phone number, or the id
// of a peer already seen (a member with no @username clicked in the F3 box).
// join=true joins a channel that was left.
func (c *Client) Resolve(ctx context.Context, q string, join bool, request uint64) {
	go func() {
		defer c.Guard("Resolve", func(err string) { c.Post(model.EvChat{Request: request, Query: q, Err: err}) })
		p, err := c.memberPeer(ctx, q) // a TDLib id already seen: no network
		if ch, ok := p.(peers.Channel); err == nil && ok && join && ch.Left() {
			_, err = c.api.ChannelsJoinChannel(ctx, ch.InputChannel())
		}
		if err != nil {
			c.Post(model.EvChat{Request: request, Query: q, Err: err.Error()})
			return
		}
		c.Post(model.EvChat{Request: request, Query: q, Chat: c.chatOf(p)})
	}()
}

// MarkRead, ReadReactions, ReadMentions and Typing send nothing while the FLOOD_WAIT
// breaker is open: nothing waits for them.
func (c *Client) MarkRead(ctx context.Context, chat *model.Chat, maxID int) {
	if !c.floodOK() {
		return
	}
	go func() {
		defer c.Guard("MarkRead", nil)
		if ch, ok := c.peer(chat).(*tg.InputPeerChannel); ok {
			c.api.ChannelsReadHistory(ctx, &tg.ChannelsReadHistoryRequest{Channel: inputChannel(ch), MaxID: maxID})
			return
		}
		c.api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{Peer: c.peer(chat), MaxID: maxID})
	}()
}

// ReadReactions marks the reactions to my messages in chat as read on the
// server — the badge of the official clients goes. Fire and forget like MarkRead.
func (c *Client) ReadReactions(ctx context.Context, chat *model.Chat) {
	if !c.floodOK() {
		return
	}
	go func() {
		defer c.Guard("ReadReactions", nil)
		c.api.MessagesReadReactions(ctx, &tg.MessagesReadReactionsRequest{Peer: c.peer(chat)})
	}()
}

// ReadMentions marks the mentions of me in chat as read on the server —
// readHistory leaves them on, and the @ badge of the official clients with
// them. Fire and forget like MarkRead.
func (c *Client) ReadMentions(ctx context.Context, chat *model.Chat) {
	if !c.floodOK() {
		return
	}
	go func() {
		defer c.Guard("ReadMentions", nil)
		c.api.MessagesReadMentions(ctx, &tg.MessagesReadMentionsRequest{Peer: c.peer(chat)})
	}()
}

// Typing tells "typing" in chat. cancel (a send) is ignored, as on Discord:
// the other clients drop the line of a peer when its message comes, so the
// cancel would double the requests of every send for nothing. Fire and forget
// like MarkRead: no error comes up, a missed typing hint does not deserve an
// error path.
func (c *Client) Typing(ctx context.Context, chat *model.Chat, cancel bool) {
	if cancel || !c.floodOK() {
		return
	}
	go func() {
		defer c.Guard("Typing", nil)
		c.api.MessagesSetTyping(ctx, &tg.MessagesSetTypingRequest{Peer: c.peer(chat), Action: &tg.SendMessageTypingAction{}})
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
		lines = append(lines, i18n.T("info_id", u.ID)) // the value of from in hooks.toml
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
