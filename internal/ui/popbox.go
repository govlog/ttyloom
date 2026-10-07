package ui

import (
	"strings"
	"unicode"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/emoji"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// Completion box: typing @… or :… in a window bound to a chat opens a small
// box above the input. @ offers the members whose @username matches (the same
// cache as the F3 box, partsCache, 5 min); : offers the emojis whose shortcode
// matches, the custom emojis of the room first. ↑/↓ then Tab/Enter insert the
// choice; Esc mutes the box for that word.

const popRows = 6 // shown candidates, at most

// popRow : one candidate of the box.
type popRow struct {
	icon   string // emoji drawn before the label; "" for a member or a custom emoji
	label  string
	insert string // what the pick puts in place of the word, before its space
	exact  bool   // a Unicode emoji whose shortcode is the whole word: a ":" converts it
}

type popBox struct {
	start  int // rune index of the @ or : in the editor buffer
	rows   []popRow
	cur    int
	emoji  bool     // a :word box
	q      string   // what is typed between the : and the cursor
	moved  bool     // an arrow or the wheel moved the choice
	recent []string // the Ctrl+T recents, read when the :word box opens
}

// sigilWord gives the word the cursor is in when it starts with sigil: start
// = index of the sigil, q = what is typed between it and the cursor. ok=false
// when the cursor is not right inside such a word.
func sigilWord(buf []rune, cur int, sigil rune) (start int, q string, ok bool) {
	i := cur
	for i > 0 && !sep(buf[i-1]) {
		i--
	}
	if i >= len(buf) || buf[i] != sigil || cur <= i {
		return 0, "", false
	}
	return i, string(buf[i+1 : cur]), true
}

// emojiQuery tells whether q can start a shortcode: letters, digits, _ + -
// only, one letter or digit at least — ":)", ":-(" and ": " open nothing.
func emojiQuery(q string) bool {
	word := false
	for _, r := range q {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word = true
		case r != '_' && r != '+' && r != '-':
			return false
		}
	}
	return word
}

// emojiRows : the custom emojis of the room whose name or a word of it starts
// with q (folded), then the Unicode ones by emoji.Lookup. The custom names
// come from the network: one that is not a ":name:" is left out.
func emojiRows(customs []string, q string, recent []string) []popRow {
	var rows []popRow
	for _, c := range customs {
		name := strings.ToLower(strings.Trim(c, ":"))
		if reCustomName.MatchString(c) && (strings.HasPrefix(name, q) || strings.Contains(name, "_"+q)) {
			rows = append(rows, popRow{label: c, insert: c})
		}
	}
	for _, h := range emoji.Lookup(q, recent) {
		rows = append(rows, popRow{icon: h.Char, label: ":" + h.Code + ":", insert: h.Char, exact: h.Exact})
	}
	return rows
}

// mentionRows : the members as rows of the box.
func mentionRows(ps []model.Participant) []popRow {
	rows := make([]popRow, len(ps))
	for i, p := range ps {
		rows[i] = popRow{label: mentionLabel(p), insert: mentionInsert(p)}
	}
	return rows
}

// popScan opens, refreshes or closes the box from the editor content.
// Called after every key and when the members arrive (participants).
func (u *UI) popScan() {
	if u.inputAsks() || u.spellFix != nil {
		u.pop = nil // the input carries a question, not a message
		return
	}
	buf := u.ed.buf // read in place: after every key
	start, q, ok := sigilWord(buf, u.ed.Cursor(), '@')
	emo := false
	if !ok {
		start, q, ok = sigilWord(buf, u.ed.Cursor(), ':')
		ok, emo = ok && emojiQuery(q), true
	}
	// A command completes with Tab; its ":" can be IRC syntax (/quote … :text).
	if !ok || (len(buf) > 0 && buf[0] == '/') {
		u.pop, u.popMute = nil, 0
		return
	}
	if u.popMute == start+1 { // Esc on this word: stay away until it is left
		u.pop = nil
		return
	}
	c := u.inputChat(u.view())
	if c == nil {
		u.pop = nil
		return
	}
	old := u.pop
	if old != nil && (old.start != start || old.emoji != emo) {
		old = nil
	}
	p := &popBox{start: start, emoji: emo, q: q}
	if emo {
		p.recent = emoji.Recent(config.RecentPath())
		if old != nil {
			p.recent = old.recent
		}
		p.rows = emojiRows(c.Customs, render.Fold(q), p.recent)
	} else {
		p.rows = mentionRows(mentionFilter(u.mentionAll(c), q))
	}
	if len(p.rows) == 0 {
		u.pop = nil
		return
	}
	if old != nil && old.cur < len(p.rows) {
		p.cur, p.moved = old.cur, old.moved // refine: the arrow choice survives the typing
	}
	u.pop = p
}

// popKey handles the keys while the box is open. false: the key is not for
// the box, the normal path takes it.
func (u *UI) popKey(k term.Key) bool {
	p := u.pop
	switch {
	case k.Code == term.Esc:
		u.popMute = p.start + 1
		u.pop = nil
	case k.Code == term.Up:
		p.cur, p.moved = max(0, p.cur-1), true
	case k.Code == term.Down:
		p.cur, p.moved = min(len(p.rows)-1, p.cur+1), true
	case k.Code == term.Enter && p.emoji && !p.moved && len([]rune(p.q)) < 2:
		return false // ":D" then Enter: a smiley in text, sent as typed
	case k.Code == term.Tab, k.Code == term.Enter && !k.Shift && !k.Alt:
		u.popPick(p.rows[p.cur].insert + " ")
	case p.emoji && k.Code == term.None && k.Rune == ':' && !k.Alt && p.rows[0].exact:
		u.popPick(p.rows[0].insert) // ":fire:" typed whole becomes 🔥, as on Discord and Slack
	default:
		return false
	}
	return true
}

// popPick puts s in place of the word and closes the box; an emoji goes
// first in the Ctrl+T recents.
func (u *UI) popPick(s string) {
	if u.pop.emoji {
		noteRecent(strings.TrimSuffix(s, " "))
	}
	u.ed.Replace(u.pop.start, u.ed.Cursor(), s)
	u.pop, u.popMute = nil, 0
}

// popMouse : an event in the box — the wheel moves the current row, a left
// click picks the one under the pointer, as Tab does.
func (u *UI) popMouse(e term.MouseEvent, r rect) {
	p := u.pop
	switch e.Button {
	case 64:
		u.popKey(term.Key{Code: term.Up})
	case 65:
		u.popKey(term.Key{Code: term.Down})
	case 0:
		rows := r.h - 2
		if y := e.Y - r.row - 1; y >= 0 && y < rows && p.top(rows)+y < len(p.rows) {
			p.cur = p.top(rows) + y
			u.popKey(term.Key{Code: term.Tab})
		}
	}
}

// top : the first row shown in a box of rows lines — the current one stays in view.
func (p *popBox) top(rows int) int { return followTop(0, p.cur, rows) }

// popRect gives the box, stuck above the status bar, at the left edge of the
// message area.
func (u *UI) popRect() rect {
	p := u.pop
	x0, cols := u.layout()
	h := min(len(p.rows), popRows) + 2
	w := 2
	for _, r := range p.rows {
		lw := render.Width(r.label) + 3 // borders and the space before the label
		if r.icon != "" {
			lw += cellW // the emoji cell and its space
		}
		w = max(w, lw)
	}
	w = min(max(12, w), min(40, cols))
	row := max(0, u.t.Rows-u.inputRows()-1-h) // above the status line
	return rect{row: row, col: x0, h: h, w: w}
}

// Lines draws the box; cur inverted, a window of popRows lines keeps it in
// view.
func (p *popBox) Lines(th theme.Theme, w, h int) []render.Line {
	body, edge, sel := boxStyles(th)
	b := boxDraw{edge: edge, fill: body, inner: max(0, w-2)}
	out := []render.Line{b.bar("┌", "┐")}
	rows := max(0, h-2)
	off := p.top(rows)
	for i := off; i < min(len(p.rows), off+rows); i++ {
		st := body
		if i == p.cur {
			st = sel
		}
		text := " " + p.rows[i].label
		if p.rows[i].icon != "" {
			text = " " + emojiCell(p.rows[i].icon) + text
		}
		out = append(out, b.text(text, st))
	}
	return append(out, b.bar("└", "┘"))
}
