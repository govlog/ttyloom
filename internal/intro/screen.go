// Package intro plays the demo at start: ASCII effects and the TTYLOOM logo
// over the chat, which shows through. Nothing here touches the terminal: the
// UI gives the frame text it drew, gets back the frame to send.
package intro

import (
	"strconv"
	"strings"

	"github.com/clipperhouse/uax29/v2/graphemes"

	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/theme"
)

// Cell : one cell of the screen.
type Cell struct {
	Text  string      // grapheme cluster; "" on the right half of a wide one
	Style theme.Style // URL and Masked always empty
}

// Screen : what the terminal shows, row by row.
type Screen struct {
	Cols, Rows int
	Cells      []Cell // Rows*Cols, row by row
}

// At gives the cell at col, row; nil outside the screen.
func (s *Screen) At(col, row int) *Cell {
	if col < 0 || row < 0 || col >= s.Cols || row >= s.Rows {
		return nil
	}
	return &s.Cells[row*s.Cols+col]
}

// Parse models the screen a frame of draw() leaves on a blank terminal: cursor
// moves, the SGR of theme.Style, the erase to the end of the row, clusters and
// their width. Any other sequence is skipped (OSC 8 links, kitty, sixel). The
// text of a frame comes from our own code: this is not a general terminal.
func Parse(text string, cols, rows int) *Screen {
	s := &Screen{Cols: max(cols, 0), Rows: max(rows, 0)}
	s.Cells = make([]Cell, s.Cols*s.Rows)
	for i := range s.Cells {
		s.Cells[i].Text = " "
	}
	var st theme.Style
	x, y := 0, 0
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == 0x1b && i+1 < len(text) && text[i+1] == '[':
			j := i + 2
			for j < len(text) && (text[j] < 0x40 || text[j] > 0x7e) {
				j++
			}
			if j >= len(text) {
				return s
			}
			params := text[i+2 : j]
			switch text[j] {
			case 'H':
				r, col, _ := strings.Cut(params, ";")
				y, x = num(r)-1, num(col)-1
			case 'm':
				st = sgr(st, params)
			case 'K':
				if params == "" || params == "0" {
					s.erase(x, y, st.BG)
				}
			}
			i = j + 1
		case c == 0x1b && i+1 < len(text) && strings.IndexByte("]_P^X", text[i+1]) >= 0:
			// A string (OSC, APC, DCS…): up to ST or BEL.
			j := i + 2
			for j < len(text) {
				if text[j] == 0x07 {
					j++
					break
				}
				if text[j] == 0x1b && j+1 < len(text) && text[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			i = j
		case c == 0x1b:
			i += 2
		case c < 0x20 || c == 0x7f:
			i++
		case c < 0x80:
			s.put(x, y, text[i:i+1], 1, st)
			x++
			i++
		default:
			j := i
			for j < len(text) && text[j] != 0x1b && (text[j] >= 0x20 && text[j] != 0x7f) {
				j++
			}
			g := graphemes.FromString(text[i:j])
			for g.Next() {
				cl := g.Value()
				w := render.Width(cl)
				if w == 0 {
					continue
				}
				s.put(x, y, cl, w, st)
				x += w
			}
			i = j
		}
	}
	return s
}

// num reads a parameter of a cursor move: 1 when empty or wrong.
func num(p string) int {
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// put writes a cluster of width w at x, y with st. Off the screen, or a wide
// cluster with no room for its right half: dropped.
func (s *Screen) put(x, y int, text string, w int, st theme.Style) {
	if y < 0 || y >= s.Rows || x < 0 || x+w > s.Cols {
		return
	}
	s.unwide(x, y)
	if w == 2 {
		s.unwide(x+1, y)
	}
	st.URL, st.Masked = "", false
	i := y*s.Cols + x
	s.Cells[i] = Cell{Text: text, Style: st}
	if w == 2 {
		s.Cells[i+1] = Cell{Style: st}
	}
}

// unwide : the cell at x is about to change; when it is half of a wide
// cluster, the other half becomes a blank, as on a terminal.
func (s *Screen) unwide(x, y int) {
	c := s.At(x, y)
	if c == nil {
		return
	}
	if c.Text == "" {
		if l := s.At(x-1, y); l != nil {
			l.Text = " "
		}
		return
	}
	if r := s.At(x+1, y); r != nil && r.Text == "" {
		r.Text = " "
	}
}

// erase : EL, from x to the end of the row, in the background bg.
func (s *Screen) erase(x, y int, bg theme.Color) {
	if y < 0 || y >= s.Rows {
		return
	}
	s.unwide(max(x, 0), y)
	for col := max(x, 0); col < s.Cols; col++ {
		s.Cells[y*s.Cols+col] = Cell{Text: " ", Style: theme.Style{BG: bg}}
	}
}

// sgr applies the parameters of an SGR to st: the ones theme.Style.SGR
// writes, and their resets.
func sgr(st theme.Style, params string) theme.Style {
	p := strings.Split(params, ";")
	for i := 0; i < len(p); i++ {
		switch v := p[i]; v {
		case "", "0":
			st = theme.Style{}
		case "1":
			st.Bold = true
		case "2":
			st.Dim = true
		case "3":
			st.Italic = true
		case "4":
			st.Underline, st.Curly = true, false
		case "4:3":
			st.Underline, st.Curly = true, true
		case "4:0", "24":
			st.Underline, st.Curly = false, false
		case "7":
			st.Reverse = true
		case "9":
			st.Strike = true
		case "22":
			st.Bold, st.Dim = false, false
		case "23":
			st.Italic = false
		case "27":
			st.Reverse = false
		case "29":
			st.Strike = false
		case "39":
			st.FG = theme.Color{}
		case "49":
			st.BG = theme.Color{}
		case "38", "48", "58":
			c, n := extColor(p[i+1:])
			i += n
			switch v {
			case "38":
				st.FG = c
			case "48":
				st.BG = c
			}
		default:
			n, err := strconv.Atoi(v)
			switch {
			case err != nil:
			case n >= 30 && n <= 37:
				st.FG = theme.Ansi(uint8(n - 30))
			case n >= 90 && n <= 97:
				st.FG = theme.Ansi(uint8(n - 90 + 8))
			case n >= 40 && n <= 47:
				st.BG = theme.Ansi(uint8(n - 40))
			case n >= 100 && n <= 107:
				st.BG = theme.Ansi(uint8(n - 100 + 8))
			}
		}
	}
	return st
}

// extColor reads what follows 38, 48 or 58: 2;r;g;b or 5;n. It gives the
// colour and how many parameters it took. An index above 15 has no Color: the
// default.
func extColor(p []string) (theme.Color, int) {
	if len(p) >= 4 && p[0] == "2" {
		r, _ := strconv.Atoi(p[1])
		g, _ := strconv.Atoi(p[2])
		b, _ := strconv.Atoi(p[3])
		return theme.Color{Kind: 2, RGB: theme.RGB{R: uint8(r), G: uint8(g), B: uint8(b)}}, 4
	}
	if len(p) >= 2 && p[0] == "5" {
		if n, err := strconv.Atoi(p[1]); err == nil && n >= 0 && n < 16 {
			return theme.Ansi(uint8(n)), 2
		}
		return theme.Color{}, 2
	}
	return theme.Color{}, len(p)
}

// Emit gives a frame that draws s: each row after its own cursor move, an SGR
// at its start and wherever the style changes, so every row stands alone.
func (s *Screen) Emit() string {
	b := make([]byte, 0, s.Cols*s.Rows*24)
	for y := 0; y < s.Rows; y++ {
		b = append(b, "\x1b["...)
		b = strconv.AppendInt(b, int64(y+1), 10)
		b = append(b, ";1H"...)
		first := true
		var prev theme.Style
		for _, c := range s.Cells[y*s.Cols : (y+1)*s.Cols] {
			if c.Text == "" {
				continue
			}
			if first || c.Style != prev {
				b = appendSGR(b, c.Style)
				prev, first = c.Style, false
			}
			b = append(b, c.Text...)
		}
	}
	return string(b)
}

// appendSGR appends the SGR of st, the same as theme.Style.SGR writes (a
// reset first), without its allocations: a frame of the demo changes colour
// at almost every cell.
func appendSGR(b []byte, st theme.Style) []byte {
	b = append(b, "\x1b[0"...)
	if st.Bold {
		b = append(b, ";1"...)
	}
	if st.Dim {
		b = append(b, ";2"...)
	}
	if st.Italic {
		b = append(b, ";3"...)
	}
	if st.Underline {
		if st.Curly {
			b = append(b, ";4:3"...)
		} else {
			b = append(b, ";4"...)
		}
	}
	if st.Reverse {
		b = append(b, ";7"...)
	}
	if st.Strike {
		b = append(b, ";9"...)
	}
	b = appendColour(b, st.FG, 30, "38")
	b = appendColour(b, st.BG, 40, "48")
	if st.Underline && st.Curly && st.FG.Kind == 2 {
		b = appendColour(b, st.FG, 0, "58")
	}
	return append(b, 'm')
}

// appendColour : ;<base+n> for an ANSI colour (base 0: none), ;<ext>;2;r;g;b
// for an RGB one, nothing for the default.
func appendColour(b []byte, c theme.Color, base int, ext string) []byte {
	switch {
	case c.Kind == 1 && base > 0:
		n := base + int(c.Idx)
		if c.Idx >= 8 {
			n = base + 60 + int(c.Idx) - 8
		}
		b = append(b, ';')
		return strconv.AppendInt(b, int64(n), 10)
	case c.Kind == 2:
		b = append(b, ';')
		b = append(b, ext...)
		b = append(b, ";2;"...)
		b = strconv.AppendInt(b, int64(c.RGB.R), 10)
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(c.RGB.G), 10)
		b = append(b, ';')
		return strconv.AppendInt(b, int64(c.RGB.B), 10)
	}
	return b
}
