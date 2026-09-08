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

// Mention box: typing @… in a window bound to a chat opens a small box above
// the input with the members whose @username matches. ↑/↓ then Tab/Enter
// insert the username; Esc mutes the box for that word. The members come from
// the same cache as the F3 box (partsCache, 5 min).

const mentionRows = 6 // shown candidates, at most

type mentionBox struct {
	start int // rune index of the @ in the editor buffer
	items []model.Participant
	cur   int
}

// mentionWord gives the @word the cursor is in: start = index of the @, q =
// what is typed between the @ and the cursor. ok=false when the cursor is not
// right inside a word starting with @.
func mentionWord(buf []rune, cur int) (start int, q string, ok bool) {
	i := cur
	for i > 0 && !sep(buf[i-1]) {
		i--
	}
	if i >= len(buf) || buf[i] != '@' || cur <= i {
		return 0, "", false
	}
	return i, string(buf[i+1 : cur]), true
}

// mentionFilter keeps the members that carry an @username and whose username
// or folded name starts with q.
func mentionFilter(all []model.Participant, q string) []model.Participant {
	q = render.Fold(q)
	var out []model.Participant
	for _, p := range all {
		if !strings.HasPrefix(p.Query, "@") {
			continue // no username: a plain-text mention would notify nobody
		}
		if q == "" || strings.HasPrefix(render.Fold(p.Query[1:]), q) || strings.HasPrefix(render.Fold(p.Text), q) {
			out = append(out, p)
		}
	}
	return out
}

// mentionAll gives the raw candidates for c — the peer of a private chat, the
// cached members otherwise (the fetch is fired like loadParts does).
func (u *UI) mentionAll(c *model.Chat) []model.Participant {
	if c.Kind == model.ChatUser {
		if c.Username == "" {
			return nil
		}
		return []model.Participant{{Text: render.CleanLine(u.title(c)), Query: "@" + c.Username}}
	}
	e, ok := u.partsCache[c.Key()]
	if b := u.net(c); b != nil && (!ok || time.Since(e.at) > partsTTL) {
		e = partsEntry{lines: []model.Participant{{Text: i18n.T("loading")}}, at: time.Now()}
		u.partsCache[c.Key()] = e
		b.Participants(u.ctx, c)
	}
	return e.lines
}

// mentionScan opens, refreshes or closes the box from the editor content.
// Called after every key and when the members arrive (participants).
func (u *UI) mentionScan() {
	if u.prompt != nil || u.ask != nil || u.search != nil || u.pasteAsk != "" || u.sendAsk != nil || u.spellFix != nil {
		u.mention = nil // the input carries a question, not a message
		return
	}
	buf := []rune(u.ed.String())
	start, q, ok := mentionWord(buf, u.ed.Cursor())
	if !ok || (len(buf) > 0 && buf[0] == '/') { // a command completes with Tab
		u.mention, u.mentionMute = nil, 0
		return
	}
	if u.mentionMute == start+1 { // Esc on this word: stay away until it is left
		u.mention = nil
		return
	}
	c := u.inputChat(u.view())
	if c == nil {
		u.mention = nil
		return
	}
	items := mentionFilter(u.mentionAll(c), q)
	if len(items) == 0 {
		u.mention = nil
		return
	}
	cur := 0
	if u.mention != nil && u.mention.start == start && u.mention.cur < len(items) {
		cur = u.mention.cur // refine: the arrow choice survives the typing
	}
	u.mention = &mentionBox{start: start, items: items, cur: cur}
}

// mentionKey handles the keys while the box is open. false: the key is not
// for the box, the normal path takes it.
func (u *UI) mentionKey(k term.Key) bool {
	m := u.mention
	switch {
	case k.Code == term.Esc:
		u.mentionMute = m.start + 1
		u.mention = nil
	case k.Code == term.Up:
		m.cur = max(0, m.cur-1)
	case k.Code == term.Down:
		m.cur = min(len(m.items)-1, m.cur+1)
	case k.Code == term.Tab, k.Code == term.Enter && !k.Shift && !k.Alt:
		u.ed.Replace(m.start, u.ed.Cursor(), m.items[m.cur].Query+" ")
		u.mention, u.mentionMute = nil, 0
	default:
		return false
	}
	return true
}

// mentionRect gives the box, stuck above the status bar, at the left edge of
// the message area.
func (u *UI) mentionRect() rect {
	m := u.mention
	x0, cols := u.layout()
	h := min(len(m.items), mentionRows) + 2
	w := 2
	for _, p := range m.items {
		w = max(w, render.Width(mentionLabel(p))+2)
	}
	w = min(max(12, w), min(40, cols))
	row := max(0, u.t.Rows-u.inputRows()-1-h) // above the status line
	return rect{row: row, col: x0, h: h, w: w}
}

// mentionLabel : "@username  Name", the name left out when it repeats the
// username.
func mentionLabel(p model.Participant) string {
	if render.Fold(p.Text) == render.Fold(p.Query[1:]) {
		return p.Query
	}
	return p.Query + "  " + p.Text
}

// Lines draws the box; cur inverted, a window of mentionRows lines keeps it
// in view.
func (m *mentionBox) Lines(th theme.Theme, w, h int) []render.Line {
	body, edge, sel := boxStyles(th)
	b := boxDraw{edge: edge, fill: body, inner: max(0, w-2)}
	out := []render.Line{b.bar("┌", "┐")}
	rows := max(0, h-2)
	off := 0
	if m.cur >= rows {
		off = m.cur - rows + 1
	}
	for i := off; i < min(len(m.items), off+rows); i++ {
		st := body
		if i == m.cur {
			st = sel
		}
		out = append(out, b.text(" "+mentionLabel(m.items[i]), st))
	}
	return append(out, b.bar("└", "┘"))
}
