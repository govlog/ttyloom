// Package dsc wraps ningen (arikawa); it talks to the UI only through
// model.Event, like tgc. No arikawa type ever leaves this package.
package dsc

import (
	"cmp"
	"context"
	"fmt"
	"sync/atomic"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/ningen/v3"

	"github.com/govlog/ttyloom/internal/model"
)

type Config struct{ Token string }

type Client struct {
	model.Poster
	cfg Config
	st  *ningen.State
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
// c.st — on the UI goroutine for DeleteChat, with no guard in front.
func New(cfg Config, events chan<- model.Event) *Client {
	// DefaultIdentifier: the identify properties belong to ningen, they are
	// never touched here.
	return &Client{Poster: model.Poster{Events: events}, cfg: cfg, st: ningen.New(cfg.Token),
		dlSem: make(chan struct{}, 3), ulSem: make(chan struct{}, 2)}
}

// Caps : Discord takes reactions, edits, the GIF box and the search — inside
// a chat and global (search.go). No read receipt, no whois, no address book,
// and no name resolution: a pseudo that no dialog carries cannot become a chat
// (/query, /join).
func (c *Client) Caps() model.Caps {
	return model.Caps{Reactions: true, Edit: true, Gifs: true, Search: true, GlobalSearch: true}
}

// Run blocks: gateway connection, then until ctx ends.
func (c *Client) Run(ctx context.Context) error {
	c.wire() // before Open: the events of the connection itself are not lost
	// The network is named in every error: main turns it into an EvFatal, and
	// with two backends the bare arikawa message says nothing about which one died.
	if err := c.st.Open(ctx); err != nil { // gives the hand back on READY
		return fmt.Errorf("discord: %w", err)
	}
	defer c.st.Close()
	me, err := c.st.Me()
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

// textChannels : the channel types listed in the sidebar. Voice, categories,
// forums and threads are out of the v1.
var textChannels = []discord.ChannelType{discord.GuildText, discord.GuildAnnouncement}

// LoadDialogs lists the channels of the account: DMs, group DMs, then the
// text channels of every guild. No message is loaded here — a login must not
// look like a scrape.
func (c *Client) LoadDialogs(context.Context) {
	go func() {
		defer c.Guard("LoadDialogs", func(err string) { c.Post(model.EvDialogs{Err: err}) })
		dms, err := c.st.PrivateChannels()
		if err != nil {
			c.Post(model.EvDialogs{Err: err.Error()})
			return
		}
		guilds, err := c.st.Guilds()
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
			chs, err := c.st.Channels(g.ID, textChannels)
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
		complete = complete && len(chats) > 0 && !readyIncomplete(c.st.Ready())
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
		p, err := c.st.Cabinet.Presence(0, ch.DMRecipients[0].ID)
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
	c.readOf(chat, ch)
	return chat
}
