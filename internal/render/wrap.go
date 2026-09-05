package render

import (
	"strings"

	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/mattn/go-runewidth"

	"github.com/govlog/ttyloom/internal/theme"
)

// vs16 : emoji variation selector (U+FE0F). go-runewidth counts it as 0
// cells, but Ghostty/kitty draw a rune of width 1 followed by vs16
// (❤️ = U+2764 U+FE0F, say) on 2 cells.
const vs16 = '\uFE0F'

// clusterWidth : width of a grapheme cluster (ZWJ emoji, skin tone, flag…):
// the sum of the rune widths, capped at 2 like go-runewidth v0.0.28, with the
// vs16 fix.
func clusterWidth(cl string) int {
	sum := 0
	for _, r := range cl {
		sum += runewidth.RuneWidth(r)
	}
	if sum == 1 && strings.ContainsRune(cl, vs16) {
		sum = 2
	}
	if sum > 2 {
		sum = 2
	}
	return sum
}

// Width gives the display width of s, cluster by cluster. Printable ASCII is
// one cell per byte and needs no segmentation: it is most of what is drawn.
func Width(s string) int {
	ascii := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c >= 0x7f {
			ascii = false
			break
		}
	}
	if ascii {
		return len(s)
	}
	w := 0
	g := graphemes.FromString(s)
	for g.Next() {
		w += clusterWidth(g.Value())
	}
	return w
}

// Truncate gives s cut to w cells (it cuts on grapheme boundaries), with tail
// added when it did cut.
func Truncate(s string, w int, tail string) string {
	if Width(s) <= w {
		return s
	}
	w -= Width(tail)
	width, pos := 0, 0
	g := graphemes.FromString(s)
	for g.Next() {
		cw := clusterWidth(g.Value())
		if width+cw > w {
			break
		}
		width += cw
		pos = g.End()
	}
	return s[:pos] + tail
}

// Wrap cuts spans into lines of width ≤ width: whole words, hard cut when a
// word is longer, '\n' forces a cut. Trailing spaces are dropped and a line
// never starts with a space. The text is flattened into runes tagged with the
// index of their span (two bytes a rune, not a whole Style), so a word can
// cross a style change without being cut.
func Wrap(spans []Span, width int) []Line {
	if width < 1 {
		width = 1
	}
	n := 0
	for _, sp := range spans {
		n += len(sp.Text) // bytes: an upper bound of the runes, one allocation each
	}
	runes := make([]rune, 0, n)
	si := make([]uint16, 0, n) // span of each rune: a message has runs of style, never 65k spans
	for k, sp := range spans {
		for _, r := range sp.Text {
			runes = append(runes, r)
			si = append(si, uint16(k))
		}
	}
	style := func(i int) theme.Style { return spans[si[i]].Style }

	var lines []Line
	var cur []Span
	curW := 0

	// add groups rs (same styles) into next-to-next spans of one style.
	add := func(lo, hi int) {
		start := lo
		for i := lo + 1; i <= hi; i++ {
			if i == hi || (si[i] != si[start] && style(i) != style(start)) {
				text := string(runes[start:i])
				if n := len(cur); n > 0 && cur[n-1].Style == style(start) {
					cur[n-1].Text += text
				} else {
					cur = append(cur, Span{text, style(start)})
				}
				start = i
			}
		}
		curW += runesWidth(runes[lo:hi])
	}
	flush := func() {
		for len(cur) > 0 {
			last := &cur[len(cur)-1]
			last.Text = strings.TrimRight(last.Text, " ")
			if last.Text != "" {
				break
			}
			cur = cur[:len(cur)-1]
		}
		lines = append(lines, Line{Spans: cur})
		cur, curW = nil, 0
	}

	i := 0
	for i < len(runes) {
		switch runes[i] {
		case '\n':
			flush()
			i++
		case ' ':
			if curW == 0 {
				i++ // a line never starts with a space
				continue
			}
			if curW+1 <= width {
				add(i, i+1)
			} else {
				flush()
			}
			i++
		default:
			j := i
			for j < len(runes) && runes[j] != ' ' && runes[j] != '\n' {
				j++
			}
			w := runesWidth(runes[i:j])
			if curW+w <= width {
				add(i, j)
				i = j
				continue
			}
			if curW > 0 {
				flush()
			}
			for w > width {
				n, _ := hardCut(runes[i:j], width)
				add(i, i+n)
				flush()
				i += n
				w = runesWidth(runes[i:j])
			}
			if j > i {
				add(i, j)
			}
			i = j
		}
	}
	if len(cur) > 0 || len(lines) == 0 {
		flush()
	}
	return lines
}

// runesWidth : Width of a run of runes. Printable ASCII is one cell a rune
// and needs neither the string nor the segmentation.
func runesWidth(rs []rune) int {
	for _, r := range rs {
		if r < 0x20 || r >= 0x7f {
			return Width(string(rs))
		}
	}
	return len(rs)
}

// hardCut gives the number of runes of tok that fit in width columns and
// their total width. It always takes at least one grapheme cluster, even when
// that one is wider than width (a wide rune in a single column): no empty
// cut, no endless loop, no cluster cut in two (a ZWJ/vs16 emoji stays whole).
func hardCut(tok []rune, width int) (n, w int) {
	g := graphemes.FromString(string(tok))
	for g.Next() {
		cl := g.Value()
		cw := clusterWidth(cl)
		if n > 0 && w+cw > width {
			break
		}
		w += cw
		for range cl {
			n++
		}
	}
	return n, w
}
