package ui

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/emoji"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// Emoji picker: centred overlay, filter by name, recents first.

// ponytail: an emoji is taken to hold 2 cells, plus 1 of separator; no fix per
// terminal. runewidth reads sequences with a variation selector too low (❤️ =
// 1): on a terminal that draws them on a single cell, the grid shifts by one
// column on that line.
const cellW = 3

type picker struct {
	query  []rune
	items  []emoji.Emoji
	recent []string // recents read at opening time, first when query is empty
	cur    int
	cols   int // columns of the grid
	rows   int // rows of the grid
	onPick func(string)
	// react : fixed list (reaction picker) — no search and no recents, the order
	// of Telegram is the right one. nil = the whole Unicode table.
	react []string
}

// newPicker : width x height = the whole box, borders included. The grid
// loses 2 border columns and 4 lines (borders, search, name).
func newPicker(width, height int, onPick func(string)) *picker {
	p := &picker{cols: max(1, (width-1)/cellW), rows: max(1, height-4), onPick: onPick,
		recent: emoji.Recent(config.RecentPath())}
	p.filter()
	return p
}

// newReactPicker : picker limited to the reactions the chat allows. Neither
// recents nor search: the table is built only here. The box fits the number of
// items (ceil / cols) instead of always taking height-4 lines; height stays
// the cap.
func newReactPicker(width, height int, list []string, onPick func(string)) *picker {
	cols := max(1, (width-1)/cellW)
	rows := max(1, min((len(list)+cols-1)/cols, height-4))
	p := &picker{cols: cols, rows: rows, onPick: onPick, react: list}
	p.filter()
	return p
}

// reactItems gives the entries of the picker for a list of emojis. The name
// comes from the Unicode table, which carries the variation selector Telegram
// leaves out; unknown there, the emoji stays with no name — never server text.
func reactItems(list []string) []emoji.Emoji {
	byChar := make(map[string]emoji.Emoji, len(emoji.All()))
	for _, e := range emoji.All() {
		byChar[emoji.Base(e.Char)] = e
	}
	out := make([]emoji.Emoji, 0, len(list))
	for _, c := range list {
		e := byChar[emoji.Base(c)]
		e.Char = c // the one sent back to the server, not the one of the table
		out = append(out, e)
	}
	return out
}

// width : width of the box, borders included.
func (p *picker) width() int { return p.cols*cellW + 1 }

// height : height of the box (borders, search, grid, name).
func (p *picker) height() int { return p.rows + 4 }

// cellAt gives the index of the emoji under (x, y), coordinates relative to
// the box. -1 outside the grid. The cell of an emoji covers its separator.
func (p *picker) cellAt(x, y int) int {
	if y < 2 || y >= p.rows+2 || x < 1 || x >= p.width()-1 { // 2 header lines
		return -1
	}
	i := (p.top()+y-2)*p.cols + (x-1)/cellW
	if i >= len(p.items) {
		return -1
	}
	return i
}

// Click : left click in the box. true when an emoji was chosen (the picker
// closes, as after Enter).
func (p *picker) Click(x, y int) bool {
	i := p.cellAt(x, y)
	if i < 0 {
		return false
	}
	p.cur = i
	p.pick(p.items[i].Char)
	return true
}

// filter computes the items again for the current search and goes back to the head.
func (p *picker) filter() {
	if p.react != nil { // fixed list: the search does not apply to it
		p.items, p.cur = reactItems(p.react), 0
		return
	}
	q := string(p.query)
	p.items, p.cur = emoji.Search(q), 0
	if q != "" || len(p.recent) == 0 {
		return
	}
	byChar := make(map[string]emoji.Emoji, len(p.items))
	for _, e := range p.items {
		byChar[e.Char] = e
	}
	head := make([]emoji.Emoji, 0, len(p.recent))
	seen := make(map[string]bool, len(p.recent))
	for _, c := range p.recent {
		e, ok := byChar[c]
		if !ok || seen[c] {
			continue // recent unknown to the table (other Unicode version) or already placed
		}
		seen[c] = true
		head = append(head, e)
	}
	out := make([]emoji.Emoji, 0, len(p.items)+len(head))
	out = append(out, head...)
	for _, e := range p.items {
		if !seen[e.Char] {
			out = append(out, e)
		}
	}
	p.items = out
}

// Key : true when the picker is done (choice or give up).
func (p *picker) Key(k term.Key) bool {
	switch {
	case k.Code == term.Esc:
		return true
	case k.Code == term.Enter:
		if p.cur < len(p.items) {
			p.pick(p.items[p.cur].Char)
		}
		return true
	case k.Code == term.Backspace:
		if n := len(p.query); n > 0 {
			p.query = p.query[:n-1]
			p.filter()
		}
	case k.Code == term.Left:
		p.move(-1)
	case k.Code == term.Right:
		p.move(1)
	case k.Code == term.Up:
		p.move(-p.cols)
	case k.Code == term.Down:
		p.move(p.cols)
	case k.Code == term.PgUp:
		p.move(-p.cols * p.rows)
	case k.Code == term.PgDn:
		p.move(p.cols * p.rows)
	case k.Code == term.None && k.Rune != 0 && !k.Alt && p.react == nil:
		p.query = append(p.query, k.Rune)
		p.filter()
	}
	return false
}

func (p *picker) move(d int) { p.cur = max(0, min(p.cur+d, len(p.items)-1)) }

// pick keeps the choice (the cache directory is made on the fly) then hands
// it over.
func (p *picker) pick(char string) {
	path := config.RecentPath()
	// Reactions are not input emojis: outside the Ctrl+T recents.
	if p.react == nil && os.MkdirAll(filepath.Dir(path), 0o700) == nil {
		emoji.AddRecent(path, char)
	}
	if p.onPick != nil {
		p.onPick(char)
	}
}

// Lines : the whole box, one screen line per render.Line.
func (p *picker) Lines(th theme.Theme) []render.Line {
	box, edge, sel := boxStyles(th)
	b := boxDraw{edge: edge, fill: box, inner: p.cols*cellW - 1}
	name := ""
	if p.cur < len(p.items) {
		name = render.CleanLine(p.items[p.cur].Name) // padTo cuts to the box
	}
	out := make([]render.Line, 0, p.rows+4)
	head := "🔍 " + string(p.query)
	if p.react != nil {
		head = i18n.T("picker_reaction")
	}
	out = append(out, b.bar("┌", "┐"), b.text(head, box))
	for r := p.top(); len(out) < p.rows+2; r++ {
		out = append(out, p.grid(edge, box, sel, r, b.inner))
	}
	return append(out, b.text(name, box), b.bar("└", "┘"))
}

// top : first grid line shown, the current emoji at mid height.
func (p *picker) top() int {
	total := (len(p.items) + p.cols - 1) / p.cols
	return min(max(0, p.cur/p.cols-p.rows/2), max(0, total-p.rows))
}

// grid : one line of the grid; the current emoji has its own span, inverted.
func (p *picker) grid(edge, box, sel theme.Style, row, inner int) render.Line {
	spans := []render.Span{{Text: "│", Style: edge}}
	var buf strings.Builder
	w := 0
	flush := func() {
		if buf.Len() > 0 {
			spans = append(spans, render.Span{Text: buf.String(), Style: box})
			buf.Reset()
		}
	}
	for c := 0; c < p.cols; c++ {
		i := row*p.cols + c
		if i >= len(p.items) {
			break
		}
		if c > 0 {
			buf.WriteString(" ")
			w++
		}
		// emojiCell cleans the emoji (it can come raw from the server,
		// reaction list) and brings it to the exact width of its cell.
		char := emojiCell(p.items[i].Char)
		if i == p.cur {
			flush()
			spans = append(spans, render.Span{Text: char, Style: sel})
		} else {
			buf.WriteString(char)
		}
		w += cellW - 1
	}
	flush()
	if w < inner {
		spans = append(spans, render.Span{Text: strings.Repeat(" ", inner-w), Style: box})
	}
	return render.Line{Spans: append(spans, render.Span{Text: "│", Style: edge})}
}

// padTo gives s cut or filled to w cells.
func padTo(s string, w int) string {
	s = render.Truncate(s, w, "")
	return s + strings.Repeat(" ", max(0, w-render.Width(s)))
}

// emojiCell : grid cell for an emoji, width cellW-1 exactly by render.Width.
// An emoji with a text presentation by default (✍, ☃, ❤…) reads 1 by
// render.Width while Ghostty draws it on 2 cells (rule G1, see
// render.clusterWidth): adding the U+FE0F variation selector forces the emoji
// presentation and lines the inner measure up with the real drawing.
// emoji.Base drops that selector when sending (pick()): Char stays
// unchanged.
func emojiCell(e string) string {
	if render.Width(e) == 1 && isEmoji(e) {
		e += "️"
	}
	cell := render.Truncate(render.CleanLine(e), cellW-1, "")
	return cell + strings.Repeat(" ", max(0, cellW-1-render.Width(cell)))
}

// isEmoji tells whether e is an emoji. Almost all of them live above U+2000
// (symbols, dingbats, emoticons); the few parts under that (keyboard keys,
// "1️⃣" say) are looked for in the Unicode table.
// ponytail: linear scan over ~5000 entries, never fired in practice (every
// useful wide emoji is already ≥ U+2000) — no cache until it shows up in a
// profile.
func isEmoji(e string) bool {
	if r, _ := utf8.DecodeRuneInString(e); r >= 0x2000 {
		return true
	}
	base := emoji.Base(e)
	for _, x := range emoji.All() {
		if emoji.Base(x.Char) == base {
			return true
		}
	}
	return false
}
