package ui

import (
	"strings"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/spell"
	"github.com/govlog/ttyloom/internal/theme"
)

// Ctrl+B, Ctrl+I and Ctrl+U leave IRC toggles in the draft (\x02, \x1d,
// \x1f, the codes ircii uses): what follows a marker takes the style until
// the same marker comes again, and the styles stack. Backspace on a marker
// takes the style back. The send turns the runs into segments; the input
// line shows the styles and hides the markers.
const (
	markBold      = '\x02'
	markItalic    = '\x1d'
	markUnderline = '\x1f'
	marks         = "\x02\x1d\x1f"
)

// toggle flips the style of a marker; false when r is not one.
func toggle(s *model.Seg, r rune) bool {
	switch r {
	case markBold:
		s.Bold = !s.Bold
	case markItalic:
		s.Italic = !s.Italic
	case markUnderline:
		s.Underline = !s.Underline
	default:
		return false
	}
	return true
}

// parseStyle splits a draft with markers into styled runs; nil with no
// marker (the text goes out raw). Empty runs are dropped.
func parseStyle(s string) []model.Seg {
	if !strings.ContainsAny(s, marks) {
		return nil
	}
	var segs []model.Seg
	var cur model.Seg
	var text []rune
	flush := func() {
		if len(text) > 0 {
			cur.Text = string(text)
			segs = append(segs, cur)
			text = nil
		}
	}
	for _, r := range s {
		if toggle(&cur, r) { // cur already flipped: flush the run before it with the old style
			cur2 := cur
			toggle(&cur, r)
			flush()
			cur = cur2
			continue
		}
		text = append(text, r)
	}
	flush()
	return segs
}

// styleLabel : the styles active after rs, "bold+italic+underline" style,
// "" when none (status bar).
func styleLabel(rs []rune) string {
	var st model.Seg
	for _, r := range rs {
		toggle(&st, r)
	}
	var parts []string
	for _, p := range []struct {
		on   bool
		name string
	}{{st.Bold, "bold"}, {st.Italic, "italic"}, {st.Underline, "underline"}} {
		if p.on {
			parts = append(parts, p.name)
		}
	}
	return strings.Join(parts, "+")
}

// inputSpans styles the runes shown of the input: the runs between markers
// take their style, the markers show nothing, and the misspelt ranges (bad,
// offsets in the draft) keep their undercurl inside each run. before = the
// runes of the draft in front of shown (styles already open), lo = the rune
// offset of shown in the draft.
func (u *UI) inputSpans(before, shown []rune, lo int, bad []spell.Range) []render.Span {
	var st model.Seg
	for _, r := range before {
		toggle(&st, r)
	}
	var out []render.Span
	i := 0
	flush := func(j int) {
		if j <= i {
			return
		}
		base := theme.Style{Bold: st.Bold, Italic: st.Italic, Underline: st.Underline}
		badSt := u.spellStyle()
		badSt.Bold, badSt.Italic = base.Bold, base.Italic
		out = append(out, styleRanges(shown[i:j], lo+i, bad, base, badSt)...)
	}
	for j, r := range shown {
		if strings.ContainsRune(marks, r) {
			flush(j)
			toggle(&st, r)
			i = j + 1
		}
	}
	flush(len(shown))
	return out
}
