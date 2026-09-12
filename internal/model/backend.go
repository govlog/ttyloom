package model

import (
	"context"
	"fmt"

	"github.com/govlog/ttyloom/internal/i18n"
)

// Backend : one chat network. Every method other than Run and Caps is
// non-blocking (goroutine + event); Run blocks until the connection ends.
type Backend interface {
	Run(ctx context.Context) error
	Caps() Caps
	LoadDialogs(ctx context.Context)
	LoadHistory(ctx context.Context, chat *Chat, beforeID, limit int)
	LoadHistoryAround(ctx context.Context, chat *Chat, id, limit int)
	LoadHistorySince(ctx context.Context, chat *Chat, minID, limit int)
	Send(ctx context.Context, chat *Chat, text string, tmpID int64)
	SendReply(ctx context.Context, chat *Chat, text string, replyTo int, tmpID int64)
	SendStyled(ctx context.Context, chat *Chat, segs []Seg, tmpID int64)
	SendPre(ctx context.Context, chat *Chat, text string, tmpID int64)
	SendPhoto(ctx context.Context, chat *Chat, path, caption string, removeAfter bool, tmpID int64)
	SendFile(ctx context.Context, chat *Chat, path, caption string, removeAfter bool, tmpID int64)
	Edit(ctx context.Context, chat *Chat, id int, text string)
	EditStyled(ctx context.Context, chat *Chat, id int, segs []Seg)
	Delete(ctx context.Context, chat *Chat, id int)
	React(ctx context.Context, chat *Chat, id int, e string)
	Typing(ctx context.Context, chat *Chat, cancel bool)
	MarkRead(ctx context.Context, chat *Chat, maxID int)
	Search(ctx context.Context, chat *Chat, q string, limit int)
	SearchGlobal(ctx context.Context, q string, limit int)
	SearchContacts(ctx context.Context, q string, limit int)
	Contacts(ctx context.Context)
	Resolve(ctx context.Context, q string, join bool)
	Participants(ctx context.Context, chat *Chat)
	Whois(ctx context.Context, chat *Chat)
	WhoisMember(ctx context.Context, token string)
	WhoRead(ctx context.Context, chat *Chat, id, readMax int)
	WhoReacted(ctx context.Context, chat *Chat, id int, rs []Reaction)
	Info(ctx context.Context, chat *Chat, id, readMax, readInboxMax int)
	Block(ctx context.Context, chat *Chat)
	BlockMember(ctx context.Context, token string)
	Leave(ctx context.Context, chat *Chat)
	DeleteChat(ctx context.Context, chat *Chat)
	Download(ctx context.Context, m *Media, path string)
	DownloadMap(ctx context.Context, m *Media, path string)
	// SearchGifs : the GIF box (Ctrl+G) — q empty asks for the trending ones.
	// chat is the conversation the box was opened from (the inline peer on
	// Telegram). SendGif posts a result of it, EvSent back like a text send.
	SearchGifs(ctx context.Context, chat *Chat, q string)
	SendGif(ctx context.Context, chat *Chat, g Gif, tmpID int64)
}

// Launcher builds the backend of one configured network and runs it: the
// backend comes back at once, Run goes on under ctx, and an EvStopped reaches
// the UI when it ends. An error (a token command that fails) means nothing
// was started. Called at start for each network, then by /<net> login.
type Launcher func(ctx context.Context, net string) (Backend, error)

// Logouter : a backend that can end the account session on the server
// (Telegram: auth.logOut). Optional — /<net> logout only disconnects a
// network without it.
type Logouter interface {
	Logout(ctx context.Context) error
}

// Caps : what the network can do; the UI hides what is false. The zero value
// allows nothing — a backend that is gone gates everything off rather than
// offering an action that would go nowhere.
type Caps struct {
	ReadReceipts bool // ✓✓ ticks + "who read" popup
	Reactions    bool
	// AnyReaction : every Unicode emoji is a reaction (Discord), no list of
	// the account: the picker is the whole table, with the search. A custom
	// emoji of the room goes as ":name:".
	AnyReaction  bool
	Edit         bool
	Whois        bool
	Search       bool // search inside a chat (/search)
	GlobalSearch bool
	Contacts     bool
	Resolve      bool // an @name or a link turned into a chat (/query, /join)
	Sync         bool // startup sweep of the whole chat list (see UI.syncStart)
	Leave        bool // leaving a room (a private chat only closes its window)
	Block        bool // blocking or reporting a chat or a member of a room
	Gifs         bool // GIF search and send (Ctrl+G)
}

// AllCaps : every capability on — Telegram, and the tests that draw a message
// with nothing gated off.
func AllCaps() Caps {
	return Caps{ReadReceipts: true, Reactions: true, Edit: true, Whois: true, Search: true,
		GlobalSearch: true, Contacts: true, Resolve: true, Sync: true, Leave: true, Block: true, Gifs: true}
}

// Poster : how a backend talks to the UI — the event channel, a blocking and
// a non-blocking send, and the panic guard of its goroutines. Embedded by
// every backend: one copy of the three, not one per network.
type Poster struct{ Events chan<- Event }

func (p Poster) Post(e Event) { p.Events <- e }

// PostNB : non-blocking send for the callbacks that must not block.
func (p Poster) PostNB(e Event) {
	select {
	case p.Events <- e:
	default:
	}
}

// Guard, deferred in a goroutine: a panic would kill the terminal and leave
// the UI waiting for a terminal event that never comes. onPanic gets its text.
func (p Poster) Guard(name string, onPanic func(err string)) {
	r := recover()
	if r == nil {
		return
	}
	p.PostNB(EvLog{Level: "ERROR", Msg: i18n.T("panic_in", name, r)})
	if onPanic != nil {
		onPanic(fmt.Sprint(r))
	}
}
