// Package dsc wraps ningen (arikawa); it talks to the UI only through
// model.Event, like tgc. No arikawa type ever leaves this package.
package dsc

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/utils/httputil"
	"github.com/diamondburned/ningen/v3"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// Config : Token as given by token_cmd or the token file; empty, Run logs in
// by QR and writes TokenFile. TokenFile is empty when the token is the
// user's (token_cmd): Logout then leaves it alone.
type Config struct{ Token, TokenFile string }

type Client struct {
	model.Poster
	cfg Config
	// st : the state, built with the token — again after a QR login, hence
	// the atomic: the UI goroutine reads it meanwhile (a chat replayed from
	// the cache calls c.state() before Run has connected).
	st atomic.Pointer[ningen.State]
	// dlSem : three transfers at a time, downloads and uploads together —
	// same cap as tgc, so two backends at work stay civil with the network.
	dlSem chan struct{}
	// ulSem : uploads apart from the downloads — a send must never wait for
	// three files being fetched.
	ulSem chan struct{}
	// self : id of the account, known only from READY. The gateway handlers
	// read it to tell our own messages apart, hence the atomic.
	self atomic.Int64
}

// New builds the state at once (ningen.New opens nothing): a chat replayed
// from the disk cache exists before Run has run, and every method reads
// c.state() — on the UI goroutine for DeleteChat, with no guard in front.
func New(cfg Config, events chan<- model.Event) *Client {
	// DefaultIdentifier: the identify properties belong to ningen, they are
	// never touched here.
	c := &Client{Poster: model.Poster{Events: events}, cfg: cfg,
		dlSem: make(chan struct{}, 3), ulSem: make(chan struct{}, 2)}
	c.st.Store(ningen.New(cfg.Token))
	return c
}

// state : the ningen state of the moment (see Client.st).
func (c *Client) state() *ningen.State { return c.st.Load() }

// Caps : Discord takes reactions, edits, the GIF box and the search — inside
// a chat and global (search.go). No read receipt, no whois, no address book,
// and no name resolution: a pseudo that no dialog carries cannot become a chat
// (/query, /join).
func (c *Client) Caps() model.Caps {
	return model.Caps{Reactions: true, AnyReaction: true, Edit: true, Gifs: true, Search: true, GlobalSearch: true}
}

// Run blocks: QR login when there is no token yet, gateway connection, then
// until ctx ends.
func (c *Client) Run(ctx context.Context) error {
	if c.cfg.Token == "" {
		tok, err := c.qrLogin(ctx)
		if err != nil {
			return fmt.Errorf("discord: %w", err)
		}
		if err := config.WriteAtomic(c.cfg.TokenFile, []byte(tok+"\n"), 0o600); err != nil {
			return fmt.Errorf("discord: %w", err)
		}
		c.cfg.Token = tok
		c.st.Store(ningen.New(tok))
	}
	st := c.state()
	c.wire() // before Open: the events of the connection itself are not lost
	// The network is named in every error: main turns it into an EvStopped, and
	// with two backends the bare arikawa message says nothing about which one died.
	if err := st.Open(ctx); err != nil { // gives the hand back on READY
		return fmt.Errorf("discord: %w", err)
	}
	defer st.Close()
	me, err := st.Me()
	if err != nil {
		return fmt.Errorf("discord: %w", err)
	}
	// c.self is not read here: the READY handler stores it, and msgOf only
	// ever runs after that handler in the same chain of synchronous calls.
	// Open comes back on initd, which is signalled before those handlers run,
	// so nothing may be assumed of c.self at this point.
	// Bot stays false: the token is that of an account, and the bot gates of
	// the UI (/chats, global search…) are a Telegram matter.
	c.Post(model.EvReady{SelfID: int64(me.ID), SelfName: cmp.Or(me.DisplayName, me.Username), Bot: false})
	// EvConnected is posted by the ConnectedEvent handler, on this connection
	// and on every one that follows it.
	<-ctx.Done()
	return nil
}

// qrLogin : the remote-auth QR (remoteauth.go) shown by the UI with a prompt
// beside it. A reply on the prompt (Enter) gives the login up, /discord logout
// too (ctx); the account seen on the phone replaces the question while the
// user confirms. The prompt and the QR close with EvQRDone either way.
func (c *Client) qrLogin(parent context.Context) (string, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	reply := make(chan string, 1)
	c.Post(model.EvAuthPrompt{Question: i18n.T("qr_prompt_discord"), Reply: reply})
	defer c.Post(model.EvQRDone{})
	go func() {
		select {
		case <-reply:
			cancel()
		case <-ctx.Done():
		}
	}()
	ra := remoteAuth{gateway: remoteAuthGateway, login: remoteAuthLogin,
		show: func(url string, exp time.Time) { c.Post(model.EvQR{URL: url, Expires: exp}) },
		scanned: func(user string) {
			c.Post(model.EvAuthPrompt{Question: i18n.T("qr_scanned_discord", user), Reply: reply})
		},
		trace: func(s string) { c.PostNB(model.EvLog{Level: "DEBUG", Msg: s}) }}
	tok, err := ra.run(ctx)
	if err != nil && ctx.Err() != nil && parent.Err() == nil { // Enter, not a logout or a quit
		return "", errors.New(i18n.T("qr_cancelled_discord"))
	}
	return tok, err
}

// Logout ends the session on the server and forgets the token file: the next
// login shows the QR again. A token that comes from token_cmd is the user's
// and is left alone — /discord logout only disconnects then.
func (c *Client) Logout(ctx context.Context) error {
	if c.cfg.TokenFile == "" {
		return nil
	}
	// The body of the official client; the server invalidates the token.
	err := c.state().Client.WithContext(ctx).FastRequest(http.MethodPost, api.EndpointAuth+"logout",
		httputil.WithJSONBody(map[string]any{"provider": nil, "voip_provider": nil}))
	if rm := os.Remove(c.cfg.TokenFile); rm != nil && !errors.Is(rm, os.ErrNotExist) {
		return rm
	}
	return err
}

// textChannels : the channel types listed in the sidebar. Voice, categories,
// forums and threads are out of the v1.
var textChannels = []discord.ChannelType{discord.GuildText, discord.GuildAnnouncement}

// LoadDialogs lists the channels of the account: DMs, group DMs, then the
// text channels of every guild. No message is loaded here — a login must not
// look like a scrape.
func (c *Client) LoadDialogs(context.Context) {
	go func() {
		defer c.Guard("LoadDialogs", func(err string) { c.Post(model.EvDialogs{Err: err}) })
		dms, err := c.state().PrivateChannels()
		if err != nil {
			c.Post(model.EvDialogs{Err: err.Error()})
			return
		}
		guilds, err := c.state().Guilds()
		if err != nil {
			c.Post(model.EvDialogs{Err: err.Error()})
			return
		}
		// complete : the list is the whole account, so the UI may drop every
		// discord chat missing from it. One guild that could not be read makes
		// it false — its channels would look gone, and the prune erases their
		// cached history.
		complete := true
		chats := make([]*model.Chat, 0, len(dms))
		for i := range dms {
			chats = append(chats, c.dialog(&dms[i], ""))
		}
		for _, g := range guilds {
			// ningen filters by type and drops the channels the account has no
			// right to read: a sidebar entry that only gives a 403 is worse
			// than no entry.
			chs, err := c.state().Channels(g.ID, textChannels)
			if err != nil {
				// One guild missing must not cost the whole list. Plain
				// text like the other logs of this package: the user-facing
				// texts of Discord are a matter for the last task of the phase.
				c.Post(model.EvLog{Level: "WARN", Msg: fmt.Sprintf("discord: channels of %q: %s", g.Name, err)})
				complete = false
				continue
			}
			for i := range chs {
				chats = append(chats, c.dialog(&chs[i], g.Name))
			}
		}
		// An empty list never prunes: that is what a state not filled yet
		// gives back, and it would wipe every cached chat of the account.
		complete = complete && len(chats) > 0 && !readyIncomplete(c.state().Ready())
		c.Post(model.EvDialogs{Chats: chats, Complete: complete})
		c.presences(dms) // after the list: the chat has to exist first
	}()
}

// presences posts the presence the state already holds for every DM. The
// gateway only sends a PRESENCE_UPDATE when a status changes, so without this
// pass a correspondent already online at the start stays unmarked until they
// move. The cache alone is read, never the network — the rule of the handlers.
func (c *Client) presences(dms []discord.Channel) {
	for i := range dms {
		ch := &dms[i]
		if ch.Type != discord.DirectMessage || len(ch.DMRecipients) == 0 {
			continue
		}
		p, err := c.state().Cabinet.Presence(0, ch.DMRecipients[0].ID)
		if err != nil {
			continue // nothing known of them: the sidebar says nothing either
		}
		// Guild 0: the presence of a user is the same everywhere, and a DM
		// belongs to no guild. Offline and invisible give no line at all, so
		// they post nothing rather than an empty status.
		if s := presenceOf(p.Status); s != "" {
			// Keyed by the id of the DM channel, like the PresenceUpdate
			// handler: the UI knows a presence by the chat it belongs to.
			c.Post(model.EvPresence{UserID: int64(ch.ID), Status: s})
		}
	}
}

// readyIncomplete : the READY carries a guild Discord could not send, an
// outage on its side. arikawa never stores such a guild (storeGuildCreate
// gives up on Unavailable) and Guilds() then gives the others back with no
// error at all: the list would look whole while its channels are missing, and
// the UI would drop them along with their cached history.
func readyIncomplete(r gateway.ReadyEvent) bool {
	for _, g := range r.Guilds {
		if g.Unavailable {
			return true
		}
	}
	return false
}

// dialog : one line of the sidebar, read marks included.
func (c *Client) dialog(ch *discord.Channel, guild string) *model.Chat {
	chat := chatOf(ch, guild)
	chat.Customs, chat.CustomLocs = c.customsOf(ch.GuildID)
	c.readOf(chat, ch)
	return chat
}
