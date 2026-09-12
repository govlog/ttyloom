package ui

import (
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// "New chat" overlay: last line of the sidebar, /new or Ctrl+N. The list shows
// the known chats and the contacts of the account, filtered live by the
// typing; an "on Telegram" section fills it with contacts.search — same
// mechanics as the global search (typing settled, one request in flight, a
// stale answer dropped).

const (
	ncW     = 50                     // width of the box, borders included
	ncLimit = 10                     // server results at most
	ncDelay = 300 * time.Millisecond // typing settled before the request
	ncMinQ  = 3                      // without an @, nothing goes below that
)

// ncRow : one line of the list — a chat, or the header of the server section.
type ncRow struct {
	chat *model.Chat
	sep  bool
}

type newChatBox struct {
	query    []rune
	rows     []ncRow
	cur      int
	found    []*model.Chat // server results shown
	inflight string        // query of the call running; "" = none (only one at a time)
	sent     string        // query of the results shown
	typed    time.Time     // last keystroke; zero = nothing to start again
	err      string
}

// chat gives the chat of the current line, nil when the list is empty.
func (n *newChatBox) chat() *model.Chat {
	if n.cur < 0 || n.cur >= len(n.rows) {
		return nil
	}
	return n.rows[n.cur].chat
}

// move : bounded move; the section header is never selectable (it is stepped
// over in the direction of the move).
func (n *newChatBox) move(d int) {
	step := 1
	if d < 0 {
		step = -1
	}
	i := min(max(n.cur+d, 0), len(n.rows)-1)
	for i >= 0 && i < len(n.rows) && n.rows[i].sep {
		i += step
	}
	if i < 0 || i >= len(n.rows) {
		return // end of the list: the selection does not move
	}
	n.cur = i
}

// top gives the first line shown, the current one at mid height — same rule as
// the theme picker: no scroll to keep.
func (n *newChatBox) top(rows int) int {
	return min(max(0, n.cur-rows/2), max(0, len(n.rows)-rows))
}

// ncMatch : folded query (already without @) held in the shown name, the
// Telegram title or the user name.
func ncMatch(c *model.Chat, q string, title func(*model.Chat) string) bool {
	if q == "" {
		return true
	}
	return strings.Contains(render.Fold(chatTitle(c, title)), q) ||
		strings.Contains(render.Fold(c.Title), q) ||
		strings.Contains(render.Fold(c.Username), q)
}

// newChatFilter gives the lines of the overlay — first the local ones that
// match q (chats of the sidebar then contacts of the account), then the new
// server results under a section header. Duplicates go by id, the local one
// wins: a contact already open does not show twice. The server results are not
// filtered again — the server is the one that answered the query.
func newChatFilter(local, found []*model.Chat, q string, title func(*model.Chat) string) []ncRow {
	f := render.Fold(strings.TrimPrefix(strings.TrimSpace(q), "@"))
	seen := make(map[model.ChatKey]bool, len(local))
	var out, srv []ncRow
	for _, c := range local {
		if seen[c.Key()] || !ncMatch(c, f, title) {
			continue
		}
		seen[c.Key()] = true
		out = append(out, ncRow{chat: c})
	}
	for _, c := range found {
		if seen[c.Key()] {
			continue
		}
		seen[c.Key()] = true
		srv = append(srv, ncRow{chat: c})
	}
	if len(srv) == 0 {
		return out
	}
	return append(append(out, ncRow{sep: true}), srv...)
}

// Lines gives the whole box, one render.Line per screen line; w and h are its
// size, borders included. title gives the local name of a chat (/rename),
// online tells whether a peer is connected (u.presence).
func (n *newChatBox) Lines(th theme.Theme, w, h int, title func(*model.Chat) string, online func(*model.Chat) bool) []render.Line {
	box, edge, sel := boxStyles(th)
	dim, acc := box, box
	dim.FG, acc.FG = th.Color(theme.Dim), th.Color(theme.Accent)
	inner, rows := max(1, w-2), gsRows(h) // same geometry as the global search
	b := boxDraw{edge: edge, fill: box, inner: inner}
	head := i18n.T("new_chat_head", render.CleanLine(string(n.query)))
	switch {
	case n.err != "":
		head += "  (" + n.err + ")"
	case n.inflight != "":
		head += "  …"
	}
	out := make([]render.Line, 0, rows+4)
	out = append(out, b.bar("┌", "┐"), b.text(head, box))
	for i, top := 0, n.top(rows); i < rows; i++ {
		j := top + i
		switch {
		case j >= len(n.rows):
			s := ""
			if i == 0 && len(n.rows) == 0 {
				s = i18n.T("no_result")
			}
			out = append(out, b.text(s, dim))
		case n.rows[j].sep:
			s := i18n.T("on_telegram_sep")
			if k := inner - render.Width(s); k > 0 {
				s += strings.Repeat("─", k)
			}
			out = append(out, b.text(s, dim))
		default:
			c := n.rows[j].chat
			st, hi := box, acc
			if j == n.cur { // current line: inverted, presence mark included
				st, hi = sel, sel
			}
			label := kindPrefix(c.Kind) + " " + bareTitle(c, render.CleanLine(chatTitle(c, title)))
			if c.Username != "" {
				label += " (@" + render.CleanLine(c.Username) + ")"
			}
			on := ""
			if c.Kind == model.ChatUser && online(c) {
				on = i18n.T("online_suffix")
			}
			out = append(out, b.row(
				render.Span{Text: padTo(label, max(0, inner-render.Width(on))), Style: st},
				render.Span{Text: on, Style: hi},
			))
		}
	}
	return append(out, b.text(i18n.T("box_open_close"), edge), b.bar("└", "┘"))
}

// ncRect : box of the overlay, centred.
func (u *UI) ncRect() rect {
	w := max(24, min(u.t.Cols-4, ncW))
	h := max(6, min(u.t.Rows-6, 20))
	return centerRect(u.t.Cols, u.t.Rows, w, h)
}

// openNewChat : "+ new message" line of the sidebar, /new or Ctrl+N. The
// contacts of the account are asked only once per session, at the first
// opening: it is the only network call the overlay makes with no typing.
func (u *UI) openNewChat() {
	if u.botOnly() {
		u.sys(i18n.T("new_chat_bot_unavailable"))
		return
	}
	u.newChat = &newChatBox{}
	u.ncFilter()
	if !u.gotContacts {
		u.gotContacts = true
		// Silent prefetch: a network with no address book is simply left out —
		// the local chats fill the overlay, and ncSend says it when a typed
		// query has nowhere to go.
		u.eachNetCap(func(c model.Caps) bool { return c.Contacts },
			func(b model.Backend) { b.Contacts(u.ctx) })
	}
}

// ncLocal : local source of the overlay — chats of the sidebar (in the current
// sort order) then contacts of the account; newChatFilter drops the duplicates.
func (u *UI) ncLocal() []*model.Chat { return append(u.sortedChats(), u.contacts...) }

// online tells whether a peer is connected from the last presence update
// (u.presence, exactly the online text, see formatStatus).
// ponytail: text compare; relang re-translates the stored presences at a
// /set lang, so the comparison holds across a language change.
func (u *UI) online(c *model.Chat) bool { return u.presence[c.Key()] == i18n.T("presence_online") }

// ncFilter : list computed again after a keystroke or an answer; the selected
// chat is kept when it survives.
func (u *UI) ncFilter() {
	n := u.newChat
	sel := n.chat()
	n.rows = newChatFilter(u.ncLocal(), n.found, string(n.query), u.title)
	n.cur = ncFirst(n.rows, sel)
}

// ncFirst gives the index of sel in rows, else the first selectable line.
func ncFirst(rows []ncRow, sel *model.Chat) int {
	if sel != nil {
		for i, r := range rows {
			if r.chat == sel {
				return i
			}
		}
	}
	for i, r := range rows {
		if !r.sep {
			return i
		}
	}
	return 0
}

// ncWorth tells whether a query is worth a network round trip — an @name, or
// three characters. Nothing goes out on a shorter one.
func ncWorth(q string) bool {
	return (strings.HasPrefix(q, "@") && len(q) > 1) || len([]rune(q)) >= ncMinQ
}

// ncSend starts the server search when the query changed and deserves it.
// Only one at a time — the next one goes out when the one in flight comes back.
func (u *UI) ncSend() {
	n := u.newChat
	q := strings.TrimSpace(string(n.query))
	if !ncWorth(q) { // typing too short: no server section at all any more
		if n.found != nil || n.err != "" {
			n.found, n.sent, n.err = nil, "", ""
			u.ncFilter()
		}
		return
	}
	if n.inflight != "" || q == n.sent {
		return
	}
	n.inflight, n.err = q, ""
	// ponytail: one inflight string for every network — the first answer frees
	// the slot and the query can go out again while the others are still on
	// their way. Fine with one backend; count the answers when there are two.
	if !u.eachNetCap(func(c model.Caps) bool { return c.Contacts },
		func(b model.Backend) { b.SearchContacts(u.ctx, q, ncLimit) }) {
		// Like the global search: the query is marked sent so that the next key
		// does not ask again.
		n.inflight, n.sent = "", q
		n.err = i18n.T("net_unsupported", strings.Join(u.netNames(), ", "))
	}
}

// contactsList : answer of contacts.getContacts, merged into u.contacts (no
// duplicate id). gsChat rather than remember: the answer does not carry the
// read counters, it would wipe the ones of the sidebar.
func (u *UI) contactsList(e model.EvContacts) {
	if e.Err != "" {
		u.gotContacts = false // try again at the next opening
		u.status0(i18n.T("contacts_error", render.CleanLine(e.Err)))
		return
	}
	have := make(map[model.ChatKey]bool, len(u.contacts))
	for _, c := range u.contacts {
		have[c.Key()] = true
	}
	for _, c := range e.Peers {
		if have[c.Key()] {
			continue
		}
		have[c.Key()] = true
		u.contacts = append(u.contacts, u.gsChat(c))
	}
	if u.newChat != nil {
		u.ncFilter()
	}
}

// contactsFound : answer of contacts.search.
func (u *UI) contactsFound(e model.EvContactsFound) {
	n := u.newChat
	if n == nil || e.Query != n.inflight {
		return // overlay closed, or answer of a query given up
	}
	n.inflight = ""
	if strings.TrimSpace(string(n.query)) != e.Query {
		u.ncSend() // the query moved during the round trip
		return
	}
	n.err, n.sent, n.found = render.CleanLine(e.Err), e.Query, nil
	if e.Err != "" {
		n.sent = "" // the same query can go out again after an error (like the global search)
	}
	for _, c := range e.Peers {
		n.found = append(n.found, u.gsChat(c))
	}
	u.ncFilter()
}

// ncOpen : Enter or click — the chat of the current line, in its window when
// it has one, in a new one otherwise. The chat already carries its peer
// (entities of the answer): nothing to resolve.
func (u *UI) ncOpen() {
	c := u.newChat.chat()
	if c == nil {
		return
	}
	u.newChat = nil
	u.openChat(c)
}

// ncList : the new chat overlay seen as a list overlay. A separator line
// carries no chat: a click on it does nothing.
func (u *UI) ncList() listOverlay {
	n, r := u.newChat, u.ncRect()
	rows := gsRows(r.h)
	return listOverlay{r: r, head: 2, rows: rows, top: n.top(rows), n: len(n.rows),
		move: n.move, enter: u.ncOpen, close: func() { u.newChat = nil },
		click: func(i int) {
			if n.rows[i].sep {
				return // separator: no chat to open
			}
			n.cur = i
			u.ncOpen()
		}}
}

// ncKey : the shared navigation, then the typing that filters.
func (u *UI) ncKey(k term.Key) {
	if u.ncList().key(k) {
		return
	}
	n := u.newChat
	switch {
	case k.Code == term.Backspace:
		if len(n.query) > 0 {
			n.query, n.typed = n.query[:len(n.query)-1], time.Now()
			u.ncFilter()
		}
	case k.Code == term.None && k.Rune != 0 && !k.Alt:
		n.query, n.typed = append(n.query, k.Rune), time.Now()
		u.ncFilter()
	}
}

// ncMouse : wheel and click in the list; a click outside the box closes.
func (u *UI) ncMouse(e term.MouseEvent) { u.ncList().mouse(e) }
