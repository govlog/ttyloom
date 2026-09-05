package ui

import (
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/spell"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// Spell checking of the input (/set spell fr|us|fr+us|off): the wrong words
// are undercurled in red; Ctrl+R walks them (Enter fixes with the chosen
// suggestion, i ignores for the session, a adds to the personal file, Ctrl+R
// skips); a right click on a word opens the same box for it alone.

// spellChecker : what the UI needs from spell.Checker — an interface so the
// tests run without cgo nor system dictionaries.
type spellChecker interface {
	Check(string) bool
	Suggest(string) []string
	Ignore(string)
	AddPersist(string) error
}

// spellFixBox : the correction box, anchored to word. walk: opened by Ctrl+R,
// every action moves to the next mistake; a right click fixes one word only.
type spellFixBox struct {
	word spell.Range
	sugg []string
	cur  int
	walk bool
}

// spellDirty forgets the last scan (the text or the dictionary changed).
func (u *UI) spellDirty() { u.spellText = "\x00" }

// spellBad gives the wrong words of the whole input, cached until the text
// changes.
func (u *UI) spellBad() []spell.Range {
	if u.spell == nil {
		return nil
	}
	text := u.ed.String()
	if text == u.spellText {
		return u.spellCache
	}
	var bad []spell.Range
	r := []rune(text)
	for _, w := range spell.Words(text, u.cfg.SpellQuotes) {
		if !u.spell.Check(string(r[w.Start:w.End])) {
			bad = append(bad, w)
		}
	}
	u.spellText, u.spellCache = text, bad
	return bad
}

// spellBadRanges gives the ranges to underline: the word still under the
// cursor is left alone (it is being typed), except while the fix box walks.
func (u *UI) spellBadRanges() []spell.Range {
	bad := u.spellBad()
	if u.spellFix != nil {
		return bad
	}
	cur := u.ed.Cursor()
	out := bad[:0:0]
	for _, w := range bad {
		if cur >= w.Start && cur <= w.End {
			continue
		}
		out = append(out, w)
	}
	return out
}

// spellWordText gives the word of r in the input.
func (u *UI) spellWordText(r spell.Range) string {
	rs := []rune(u.ed.String())
	return string(rs[r.Start:r.End])
}

// spellFixStart : Ctrl+R — the box opens on the first mistake of the input.
func (u *UI) spellFixStart() {
	if u.spell == nil {
		u.flash(i18n.T("spell_off", u.cfg.Spell))
		return
	}
	bad := u.spellBad()
	if len(bad) == 0 {
		u.flash(i18n.T("spell_no_error"))
		return
	}
	u.spellFixOpen(bad[0], true)
}

// spellFixOpen anchors the box on w; the cursor goes to the end of the word,
// so the eye and the next insertions follow.
func (u *UI) spellFixOpen(w spell.Range, walk bool) {
	u.spellFix = &spellFixBox{word: w, sugg: u.spell.Suggest(u.spellWordText(w)), walk: walk}
	u.ed.cur = min(w.End, len([]rune(u.ed.String())))
}

// spellFixNext moves to the first mistake at or after pos, or closes the box;
// nothing left anywhere → "no mistake" flash.
func (u *UI) spellFixNext(pos int) {
	bad := u.spellBad()
	for _, w := range bad {
		if w.Start >= pos {
			u.spellFixOpen(w, true)
			return
		}
	}
	u.spellFix = nil
	if len(bad) == 0 {
		u.flash(i18n.T("spell_no_error"))
	}
}

// spellFixDone : after an action on the current word — walk mode goes on,
// the one-word box closes.
func (u *UI) spellFixDone(pos int) {
	if u.spellFix.walk {
		u.spellFixNext(pos)
		return
	}
	u.spellFix = nil
}

// spellFixKey handles the keys while the box is open. false: the key is not
// for the box — it is closed and the normal path takes the key.
func (u *UI) spellFixKey(k term.Key) bool {
	f := u.spellFix
	switch {
	case k.Code == term.Esc:
		u.spellFix = nil
	case k.Code == term.Up:
		f.cur = max(0, f.cur-1)
	case k.Code == term.Down:
		f.cur = min(len(f.sugg)-1, f.cur+1)
	case k.Code == term.Enter && !k.Shift && !k.Alt:
		if len(f.sugg) == 0 {
			return true // nothing to apply: i, a, Ctrl+R or Esc
		}
		s := f.sugg[f.cur]
		u.ed.Replace(f.word.Start, f.word.End, s)
		u.spellDirty()
		u.spellFixDone(f.word.Start + len([]rune(s)))
	case k.Code == term.None && k.Rune == 'i':
		u.spell.Ignore(u.spellWordText(f.word))
		u.spellDirty()
		u.spellFixDone(f.word.End)
	case k.Code == term.None && k.Rune == 'a':
		if err := u.spell.AddPersist(u.spellWordText(f.word)); err != nil {
			u.sys(err.Error())
		}
		u.spellDirty()
		u.spellFixDone(f.word.End)
	case k.Code == term.Ctrl && k.Rune == 'r': // skip, no action
		u.spellFixNext(f.word.End)
	default:
		u.spellFix = nil // any other key goes back to the editor
		return false
	}
	return true
}

// styleRanges cuts the shown runes (starting at rune lo of the full text)
// into spans: base, and bad over the given ranges.
func styleRanges(rs []rune, lo int, bad []spell.Range, base, badSt theme.Style) []render.Span {
	var out []render.Span
	flush := func(from, to int, st theme.Style) {
		if to > from {
			out = append(out, render.Span{Text: string(rs[from:to]), Style: st})
		}
	}
	i := 0
	for _, w := range bad {
		s, e := max(0, w.Start-lo), min(len(rs), w.End-lo)
		if e <= i {
			continue
		}
		if s > len(rs) {
			break
		}
		flush(i, max(i, s), base)
		flush(max(i, s), e, badSt)
		i = e
	}
	flush(i, len(rs), base)
	return out
}

// applySpell (re)opens the checker for mode ("off" closes it). A mode that
// does not open is told in the status window and leaves the checker in place:
// a typo in /set spell must not cost the working one.
func (u *UI) applySpell(mode string) error {
	if mode == "" || mode == "off" {
		u.spell, u.spellFix = nil, nil
		u.spellDirty()
		return nil
	}
	c, err := spellNew(mode, u.cfg.SpellPath())
	if err != nil {
		if u.spell == nil {
			u.sys(i18n.T("spell_off", err.Error())) // nothing was on: spell stays off
		} else {
			u.sys(err.Error()) // the checker in place keeps working
		}
		return err
	}
	u.spell, u.spellFix = c, nil
	u.spellDirty()
	return nil
}

// spellNew : seam of the tests (they plant a fake). spell.New returns a
// concrete *spell.Checker: a nil one must stay a nil interface.
var spellNew = func(mode, perso string) (spellChecker, error) {
	c, err := spell.New(mode, perso)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// spellActive : the input carries a message and the checking is on.
func (u *UI) spellActive() bool {
	return u.spell != nil && u.prompt == nil && u.ask == nil && u.search == nil &&
		u.pasteAsk == "" && u.sendAsk == nil
}

// spellStyle : red undercurl (straight underline without terminal support);
// the FG drives the colour of the curl (58, theme.SGR).
func (u *UI) spellStyle() theme.Style {
	return theme.Style{FG: u.th.Color(theme.Error), Underline: true, Curly: theme.Curly}
}

// inputMap : what drawInput knew at the last frame, for the mouse — screen
// cell -> rune of the draft.
type inputMap struct {
	top        int // screen row (1-based) of the first input line
	pw, avail  int // prompt width, text columns
	lo         int // first rune shown (single line)
	multi      bool
	start      int // first draft line shown
	cli, cliLo int // cursor line and its first rune shown
}

// inputRuneAt gives the rune index of the draft under the screen cell (x, y),
// both 0-based; -1 outside the text.
func (u *UI) inputRuneAt(x, y int) int {
	m := u.inMap
	x0, _ := u.layout()
	cells := x - x0 - m.pw
	if cells < 0 || cells >= m.avail {
		return -1
	}
	lines := []string{strings.ReplaceAll(u.ed.String(), "\n", "⏎")}
	li, off, lo := 0, 0, m.lo
	if m.multi {
		lines = strings.Split(u.ed.String(), "\n")
		li = m.start + y + 1 - m.top
		if li < 0 || li >= len(lines) {
			return -1
		}
		for k := 0; k < li; k++ {
			off += len([]rune(lines[k])) + 1
		}
		lo = 0
		if li == m.cli {
			lo = m.cliLo
		}
	} else if y+1 != m.top {
		return -1
	}
	rs := []rune(lines[li])
	w := 0
	for i := lo; i < len(rs); i++ {
		w += render.Width(string(rs[i]))
		if w > cells {
			return off + i
		}
	}
	return -1
}

// spellClick : right click on the input — the box opens on the wrong word
// under the pointer.
func (u *UI) spellClick(x, y int) {
	if !u.spellActive() {
		return
	}
	i := u.inputRuneAt(x, y)
	if i < 0 {
		return
	}
	for _, w := range u.spellBad() {
		if i >= w.Start && i < w.End {
			u.spellFixOpen(w, false)
			return
		}
	}
}

// spellFixRect gives the box, above the status bar, anchored at the column of
// the word when it is on screen.
func (u *UI) spellFixRect() rect {
	f := u.spellFix
	x0, cols := u.layout()
	rows := len(f.sugg) + 3 // borders + the a/i help line
	col := x0
	if !u.inMap.multi && f.word.Start >= u.inMap.lo {
		rs := []rune(strings.ReplaceAll(u.ed.String(), "\n", "⏎"))
		end := min(f.word.Start, len(rs))
		col = x0 + u.inMap.pw + render.Width(string(rs[u.inMap.lo:end]))
	}
	w := render.Width(u.spellFixHelp()) + 2
	for _, s := range f.sugg {
		w = max(w, render.Width(s)+3)
	}
	w = min(max(14, w), min(44, cols))
	col = max(0, min(col, x0+cols-w))
	row := max(0, u.t.Rows-u.inputRows()-1-rows)
	return rect{row: row, col: col, h: rows, w: w}
}

func (u *UI) spellFixHelp() string {
	return " a " + i18n.T("spell_add") + " · i " + i18n.T("spell_ignore")
}

// Lines draws the correction box: the suggestions (chosen one inverted), then
// the a/i help line.
func (f *spellFixBox) Lines(th theme.Theme, w, h int, help string) []render.Line {
	body, edge, sel := boxStyles(th)
	b := boxDraw{edge: edge, fill: body, inner: max(0, w-2)}
	out := []render.Line{b.bar("┌", "┐")}
	for i, s := range f.sugg {
		st := body
		if i == f.cur {
			st = sel
		}
		out = append(out, b.text(" "+s, st))
	}
	dim := body
	dim.FG = th.Color(theme.Dim)
	out = append(out, b.text(help, dim))
	return append(out, b.bar("└", "┘"))
}

// spellFixMouse : left click in the box picks the suggestion under the
// pointer; any other click closes the box. true: the click was used.
func (u *UI) spellFixMouse(m term.MouseEvent) bool {
	if !m.Press || m.Button != 0 {
		return false
	}
	r := u.spellFixRect()
	if !r.hits(m.Y, m.X, 1, 1) {
		u.spellFix = nil
		return false
	}
	if i := m.Y - r.row - 1; i >= 0 && i < len(u.spellFix.sugg) {
		u.spellFix.cur = i
		u.spellFixKey(term.Key{Code: term.Enter})
	}
	return true
}
