package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// Member box (F3): a box on top in the upper right corner of the message
// area, never a whole column. It does not take the keyboard (Esc stays for the
// selection): F3 alone closes it.

const partsTTL = 5 * time.Minute

type partsBox struct {
	chat   model.ChatKey
	title  string
	lines  []model.Participant
	scroll int // first content line shown
	mark   int // line highlighted while its context menu is open (-1 = none)
}

type partsEntry struct {
	lines []model.Participant
	at    time.Time
}

// partsBoxRect gives the box stuck to the upper right corner of the message
// area (cols columns, scrollbar left out; rows lines). n = content lines; the
// box adds its two borders and never goes past the edge.
func partsBoxRect(cols, rows, n int) (x, y, w, h int) {
	// Floors at 2: under that the box could not even hold its borders and
	// Lines would draw more lines than the rect keeps free.
	w = min(max(16, min(32, cols/3)), max(2, cols))
	h = min(max(3, min(n+2, rows/2)), max(2, rows))
	return max(0, cols-w), 0, w, h
}

// partsRect gives the box in screen coordinates, false when it is closed.
func (u *UI) partsRect() (rect, bool) {
	if u.parts == nil {
		return rect{}, false
	}
	x0, cols := u.layout()
	x, y, w, h := partsBoxRect(cols, u.viewRows(), len(u.parts.lines))
	return rect{row: y, col: x0 + x, h: h, w: w}, true
}

// toggleParts : F3. u.partsOn carries the intent, u.parts the box that can be
// shown: a window with no chat has nothing to show without cancelling the
// request, until a /query binds it.
func (u *UI) toggleParts() {
	u.partsOn = !u.partsOn
	if u.partsOn && u.view().Chat == nil {
		u.sys(i18n.T("parts_window_not_bound"))
	}
	u.loadParts()
}

// loadParts builds (again) the box for the chat of the shown window — at
// opening time as well as at each window or binding change.
func (u *UI) loadParts() {
	c := u.view().Chat
	if !u.partsOn || c == nil {
		u.parts = nil
		return
	}
	p := &partsBox{chat: c.Key(), title: render.CleanLine(u.title(c)), mark: -1}
	if c.Kind == model.ChatUser {
		// The peer of a private chat is already known: no network call, and the
		// presence is the one of the updates, fresher than a getFullUser.
		p.lines = privateParts(u.title(c), c, u.presence[c.Key()])
		u.parts = p
		return
	}
	e, ok := u.partsCache[c.Key()]
	if b := u.net(c); b != nil && (!ok || time.Since(e.at) > partsTTL) {
		// The "loading…" is cached: opening the box again during the request
		// does not start it again. The error, though, wipes the entry.
		e = partsEntry{lines: []model.Participant{{Text: i18n.T("loading")}}, at: time.Now()}
		u.partsCache[c.Key()] = e
		b.Participants(u.ctx, c)
	}
	p.lines = e.lines
	u.parts = p
}

// privateParts gives the name (local when renamed), @username and presence of the peer.
func privateParts(name string, c *model.Chat, presence string) []model.Participant {
	out := []model.Participant{{Text: render.CleanLine(name)}}
	if c.Username != "" {
		out = append(out, model.Participant{Text: "@" + render.CleanLine(c.Username)})
	}
	if presence != "" {
		out = append(out, model.Participant{Text: presence})
	}
	return out
}

// participants : answer of the backend. The text is remote: render.CleanLine once
// here rather than at each repaint.
func (u *UI) participants(e model.EvParticipants) {
	lines := e.Lines
	for i := range lines {
		lines[i].Text = render.CleanLine(lines[i].Text)
	}
	key := u.evKey(e.ChatID)
	if e.Err != "" {
		lines = []model.Participant{{Text: i18n.T("unavailable")}}
		delete(u.partsCache, key) // the next opening tries again
	} else {
		if len(lines) == 0 {
			lines = []model.Participant{{Text: i18n.T("no_member")}}
		}
		u.partsCache[key] = partsEntry{lines: lines, at: time.Now()}
	}
	if u.parts != nil && u.parts.chat == key {
		u.parts.lines, u.parts.scroll = lines, 0
	}
	u.mentionScan() // the members the open @… box waits for

}

// Lines gives the whole box, one render.Line per screen line. The title takes
// the top border; a member online goes to the accent colour.
func (p *partsBox) Lines(th theme.Theme, w, h int) []render.Line {
	edge := theme.Style{FG: th.Color(theme.Dim)}
	acc := theme.Style{FG: th.Color(theme.Accent)}
	on := theme.Style{FG: th.FG, Reverse: true}
	inner := max(0, w-2)
	side := render.Span{Text: "│", Style: edge}
	top := padDash(p.title, inner)
	if c0, ok := partsCloseCol(w); ok { // close cross stuck to the right
		top = padDash(p.title, c0-1) + "[x]"
	}
	out := []render.Line{{Spans: []render.Span{{Text: "┌" + top + "┐", Style: edge}}}}
	for i := 0; i < h-2; i++ {
		text, st := "", theme.Style{}
		if k := p.scroll + i; k >= 0 && k < len(p.lines) {
			text = p.lines[k].Text
			if p.lines[k].Online {
				st = acc
			}
			if k == p.mark {
				st = on
			}
		}
		out = append(out, render.Line{Spans: []render.Span{side, {Text: padTo(text, inner), Style: st}, side}})
	}
	out = append(out, render.Line{Spans: []render.Span{{Text: "└" + strings.Repeat("─", inner) + "┘", Style: edge}}})
	return out[:min(len(out), h)] // never more lines than the rect
}

// partsCloseCol gives the column (relative to the box) of the [x] cross of the
// top border, which takes 3 cells before the ┐ corner. ok=false: box too
// narrow to carry it (nothing would be left of the title).
func partsCloseCol(w int) (int, bool) {
	if w < 6 {
		return 0, false
	}
	return w - 4, true
}

// padDash gives the title cut to w cells, filled up with the top border.
func padDash(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = render.Truncate(s, w, "…")
	return s + strings.Repeat("─", max(0, w-render.Width(s)))
}

// lineAt gives the index of the content line under (x, y), -1 on a border or
// under the list. r is the screen position of the box.
func (p *partsBox) lineAt(x, y int, r rect) int {
	if y == r.row || y == r.row+r.h-1 || x == r.col || x == r.col+r.w-1 {
		return -1 // border
	}
	i := p.scroll + y - r.row - 1 // -1: top border
	if i < 0 || i >= len(p.lines) {
		return -1
	}
	return i
}

// partsMouse : wheel = scroll of the box, click on a member = /query, right
// click = context menu of the member. r is its screen position; the caller has
// checked that the click falls inside.
func (u *UI) partsMouse(m term.MouseEvent, r rect) {
	p := u.parts
	view := max(1, r.h-2)
	member := func() (int, string) {
		i := p.lineAt(m.X, m.Y, r)
		if i < 0 {
			return -1, ""
		}
		return i, p.lines[i].Query // empty: line not clickable (counter, state)
	}
	switch m.Button {
	case 64:
		p.scroll = max(0, p.scroll-1)
	case 65:
		p.scroll = max(0, min(p.scroll+1, len(p.lines)-view))
	case 0:
		if c0, ok := partsCloseCol(r.w); ok && m.Y == r.row && m.X-r.col >= c0 && m.X-r.col < c0+3 {
			u.toggleParts() // [x] cross: closes the box like F3
			return
		}
		if _, q := member(); q != "" {
			u.openMember(p.chat.Net, q)
		}
	case 2:
		if i, q := member(); q != "" {
			u.openMemberMenu(q, i, m.X, m.Y)
		}
	}
}

// openMember opens the private chat of a member without ever unbinding the
// current window — their window when they already have one, a new one
// otherwise, /query doing the binding (and the network lookup when needed).
func (u *UI) openMember(net, q string) {
	c, amb := u.memberChat(net, q)
	if amb {
		return // findChat has already listed the candidates: no orphan window
	}
	if c != nil {
		if i := u.ws.ForChat(c.Key()); i >= 0 {
			u.goTo(i)
			return
		}
	}
	// A chat nobody knows yet is bound through Resolve, which Discord has not:
	// said here rather than leaving an empty window on a "resolving…" that no
	// answer will ever end.
	if c == nil && !backendCaps(u.netOf(net)).Resolve {
		u.netUnsupported(net)
		return
	}
	u.ws.New(true) // hidden: goTo does the full switch (draft, selection)
	u.goTo(len(u.ws.List) - 1)
	u.bind(u.view(), q, false)
}

// memberChat gives the chat already known behind a member token — TDLib id (=
// user id) for a member with no @username, exact match of the @username
// otherwise. Never a title: the token comes from the network, and a title that
// looks like it is not the right contact. It also gives ambiguous, which bind
// would meet after the fact — we need to know before opening a window. net is
// the network of the box the token comes from: a bare id means nothing outside it.
func (u *UI) memberChat(net, q string) (*model.Chat, bool) {
	if id, err := strconv.ParseInt(q, 10, 64); err == nil {
		return u.chats[model.ChatKey{Net: net, ID: id}], false
	}
	name := strings.ToLower(strings.TrimPrefix(q, "@"))
	for _, c := range u.chatList {
		if c.Net == net && c.Username != "" && strings.ToLower(c.Username) == name {
			return c, false
		}
	}
	_, amb := u.findChat(q)
	return nil, amb
}
