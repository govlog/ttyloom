package intro

import (
	"math"

	"github.com/govlog/ttyloom/internal/theme"
)

// The logo, TTYLOOM_, in letters drawn by hand ('#' = lit). Two sizes: the
// big one wants 90 columns and 20 rows, the small one fits 40 columns.
var (
	bigFont = map[rune][]string{
		'T': {
			"#########",
			"#########",
			"   ###   ",
			"   ###   ",
			"   ###   ",
			"   ###   ",
			"   ###   ",
		},
		'Y': {
			"###   ###",
			" ### ### ",
			"  #####  ",
			"   ###   ",
			"   ###   ",
			"   ###   ",
			"   ###   ",
		},
		'L': {
			"###      ",
			"###      ",
			"###      ",
			"###      ",
			"###      ",
			"#########",
			"#########",
		},
		'O': {
			" ####### ",
			"###   ###",
			"###   ###",
			"###   ###",
			"###   ###",
			"###   ###",
			" ####### ",
		},
		'M': {
			"###   ###",
			"#### ####",
			"#########",
			"### # ###",
			"###   ###",
			"###   ###",
			"###   ###",
		},
		'A': {
			"  #####  ",
			" ### ### ",
			"###   ###",
			"#########",
			"#########",
			"###   ###",
			"###   ###",
		},
		'E': {
			"#########",
			"#########",
			"###      ",
			"#######  ",
			"###      ",
			"#########",
			"#########",
		},
		'I': {
			"#####",
			" ### ",
			" ### ",
			" ### ",
			" ### ",
			" ### ",
			"#####",
		},
		'K': {
			"###   ###",
			"###  ### ",
			"### ###  ",
			"######   ",
			"### ###  ",
			"###  ### ",
			"###   ###",
		},
		'N': {
			"###   ###",
			"####  ###",
			"##### ###",
			"### #####",
			"###  ####",
			"###   ###",
			"###   ###",
		},
		'R': {
			"######## ",
			"###   ###",
			"###   ###",
			"######## ",
			"### ###  ",
			"###  ### ",
			"###   ###",
		},
		'S': {
			" ########",
			"###      ",
			"###      ",
			" ####### ",
			"      ###",
			"      ###",
			"######## ",
		},
		'W': {
			"###   ###",
			"###   ###",
			"###   ###",
			"### # ###",
			"#########",
			"#### ####",
			"###   ###",
		},
		' ': {"   ", "   ", "   ", "   ", "   ", "   ", "   "},
		'_': {
			"       ",
			"       ",
			"       ",
			"       ",
			"       ",
			"       ",
			"#######",
		},
	}
	smallFont = map[rune][]string{
		'T': {
			"####",
			" ## ",
			" ## ",
			" ## ",
			" ## ",
		},
		'Y': {
			"#  #",
			"#  #",
			" ## ",
			" ## ",
			" ## ",
		},
		'L': {
			"#   ",
			"#   ",
			"#   ",
			"#   ",
			"####",
		},
		'O': {
			" ## ",
			"#  #",
			"#  #",
			"#  #",
			" ## ",
		},
		'M': {
			"#   #",
			"## ##",
			"# # #",
			"#   #",
			"#   #",
		},
		'A': {
			" ## ",
			"#  #",
			"####",
			"#  #",
			"#  #",
		},
		'E': {
			"####",
			"#   ",
			"### ",
			"#   ",
			"####",
		},
		'I': {
			"###",
			" # ",
			" # ",
			" # ",
			"###",
		},
		'K': {
			"#  #",
			"# # ",
			"##  ",
			"# # ",
			"#  #",
		},
		'N': {
			"#  #",
			"## #",
			"# ##",
			"#  #",
			"#  #",
		},
		'R': {
			"### ",
			"#  #",
			"### ",
			"# # ",
			"#  #",
		},
		'S': {
			" ###",
			"#   ",
			" ## ",
			"   #",
			"### ",
		},
		'W': {
			"#   #",
			"#   #",
			"# # #",
			"## ##",
			"#   #",
		},
		'.': {"  ", "  ", "  ", "  ", "# "},
		'*': {"   ", "# #", " # ", "# #", "   "},
		'-': {"   ", "   ", "###", "   ", "   "},
		'0': {" ## ", "# ##", "## #", "#  #", " ## "},
		'1': {" # ", "## ", " # ", " # ", "###"},
		'2': {"### ", "   #", " ## ", "#   ", "####"},
		'3': {"### ", "   #", " ## ", "   #", "### "},
		'4': {"#  #", "#  #", "####", "   #", "   #"},
		'5': {"####", "#   ", "### ", "   #", "### "},
		'6': {" ## ", "#   ", "### ", "#  #", " ## "},
		'7': {"####", "   #", "  # ", " #  ", " #  "},
		'8': {" ## ", "#  #", " ## ", "#  #", " ## "},
		'9': {" ## ", "#  #", " ###", "   #", " ## "},
		'B': {"### ", "#  #", "### ", "#  #", "### "},
		'V': {"#   #", "#   #", " # # ", " # # ", "  #  "},
		' ': {"  ", "  ", "  ", "  ", "  "},
		'_': {
			"   ",
			"   ",
			"   ",
			"   ",
			"###",
		},
	}
)

const logoText = "TTYLOOM_"

// Colours of the logo (docs/logo.svg) and of its threads.
var (
	colTTY    = theme.RGB{R: 0xcd, G: 0xd6, B: 0xf4}
	colMint   = theme.RGB{R: 0x94, G: 0xe2, B: 0xd5}
	colPink   = theme.RGB{R: 0xf5, G: 0xc2, B: 0xe7}
	colBlue   = theme.RGB{R: 0x89, G: 0xb4, B: 0xfa}
	shadowCol = theme.RGB{R: 0x08, G: 0x08, B: 0x0e}
	white     = theme.RGB{R: 0xff, G: 0xff, B: 0xff}
	threadCol = [3]theme.RGB{colBlue, colMint, colPink}
)

// logoCell : one lit cell of the logo.
type logoCell struct {
	X, Y   int // in the logo box, from its top left
	Letter int // 0..6 for T T Y L O O M, 7 for the cursor
	Ch     byte
	FG     theme.RGB
}

// logo : the logo laid out in one font.
type logo struct {
	W, H  int
	Cells []logoCell
	Lit   map[[2]int]bool // the lit cells, by X, Y
}

// logoFor lays the logo out in the font the screen has room for.
func logoFor(c Canvas) logo {
	font, gap := fontFor(c)
	return layout(logoText, font, gap, letterColour)
}

// fontFor : the big font when the screen has room for it, the small one
// otherwise.
func fontFor(c Canvas) (map[rune][]string, int) {
	if c.Cols >= 90 && c.Rows >= 20 {
		return bigFont, 2
	}
	return smallFont, 1
}

// layout lays text out on one line in font; colour gives the colour of the
// i-th letter. A rune the font lacks is skipped.
func layout(text string, font map[rune][]string, gap int, colour func(i int) theme.RGB) logo {
	g := logo{Lit: map[[2]int]bool{}}
	x, i := 0, 0
	for _, r := range text {
		rows, ok := font[r]
		if !ok {
			continue
		}
		g.H = max(g.H, len(rows))
		for y, row := range rows {
			for dx := 0; dx < len(row); dx++ {
				if row[dx] == '#' {
					g.Lit[[2]int{x + dx, y}] = true
					g.Cells = append(g.Cells, logoCell{X: x + dx, Y: y, Letter: i})
				}
			}
		}
		x += len(rows[0]) + gap
		i++
	}
	g.W = max(x-gap, 0)
	for k := range g.Cells {
		lc := &g.Cells[k]
		if gap > 1 { // strokes of one cell have no corner to cut
			lc.Ch = g.glyph(lc.X, lc.Y)
		}
		lc.FG = chrome(colour(lc.Letter), float64(lc.Y)/float64(max(g.H-1, 1)))
	}
	return g
}

func letterColour(i int) theme.RGB {
	switch {
	case i < 3:
		return colTTY
	case i < 7:
		return colMint
	}
	return colPink
}

// chrome : the colour of a row at v (0 top, 1 bottom): light on top, dark at
// the bottom, the way the letters of the demos shone.
func chrome(c theme.RGB, v float64) theme.RGB {
	if v < 0.35 {
		return mix(c, white, float32(0.45*(0.35-v)/0.35))
	}
	return mix(c, Backdrop, float32(0.45*(v-0.35)/0.65))
}

// glyph : the glyph of a lit cell from its neighbours. An outer corner is
// cut by a slash, the way ASCII art rounds a block; other cells are solid
// (0: filled with the colour of the letter).
func (g logo) glyph(x, y int) byte {
	up, down := g.Lit[[2]int{x, y - 1}], g.Lit[[2]int{x, y + 1}]
	left, right := g.Lit[[2]int{x - 1, y}], g.Lit[[2]int{x + 1, y}]
	switch {
	case !up && !left && (down || right):
		return '/'
	case !up && !right && (down || left):
		return '\\'
	case !down && !left && (up || right):
		return '\\'
	case !down && !right && (up || left):
		return '/'
	}
	return 0
}

// origin : top left of the logo box, centred on the screen.
func (g logo) origin(c Canvas) (x, y int) {
	return (c.Cols - g.W) / 2, (c.Rows - g.H) / 2
}

// drawLogo draws the logo; at gives the offset of each cell and whether it
// shows (nil: in place). A drop shadow goes under the letters. It gives, per
// cell of the layer, the letter drawn there (-1: none), for drawThreads.
func drawLogo(l *Layer, c Canvas, g logo, t float64, at func(lc logoCell) (dx, dy int, show bool)) []int8 {
	occ := make([]int8, len(l.Cells))
	for i := range occ {
		occ[i] = -1
	}
	x0, y0 := g.origin(c)
	blink := math.Mod(t, 1.06) < 0.53
	pos := func(lc logoCell) (int, int, bool) {
		if lc.Letter == 7 && !blink {
			return 0, 0, false
		}
		if at == nil {
			return x0 + lc.X, y0 + lc.Y, true
		}
		dx, dy, show := at(lc)
		return x0 + lc.X + dx, y0 + lc.Y + dy, show
	}
	for _, lc := range g.Cells {
		if x, y, ok := pos(lc); ok {
			if f := l.At(x+1, y+1); f != nil {
				*f = FX{Ch: ' ', Tint: shadowCol, A: 0.85}
			}
		}
	}
	for _, lc := range g.Cells {
		x, y, ok := pos(lc)
		f := l.At(x, y)
		if !ok || f == nil {
			continue
		}
		if lc.Ch != 0 { // a cut corner: the slash in the colour of the letter, on the dark
			*f = FX{Ch: lc.Ch, FG: lc.FG, Tint: shadowCol, A: 0.85}
		} else {
			*f = FX{Ch: ' ', Tint: lc.FG, A: 1}
		}
		occ[y*l.Cols+x] = int8(lc.Letter)
	}
	return occ
}

// drawThreads draws the three threads of the logo through its middle, a bit
// wider than it. Thread k goes over letter i when i+k is even, under it
// otherwise: the weave of a loom. Over a letter, the thread is a strip of its
// colour; elsewhere, a line of glyphs. occ is what drawLogo gave.
func drawThreads(l *Layer, c Canvas, g logo, t float64, occ []int8) {
	x0, y0 := g.origin(c)
	mid := float64(y0) + float64(g.H-1)/2
	amp := float64(g.H)/2 - 0.5
	cx := float64(x0) + float64(g.W)/2
	reach := float64(g.W)/2 + 6
	wave := 2 * math.Pi / float64(g.W) // one wave across the logo: a gentle slope, one row per column
	for k := range 3 {
		phase := float64(k) * 2 * math.Pi / 3
		ys := func(x float64) float64 { return mid + amp*math.Sin((x-cx)*wave-t*1.1+phase) }
		for x := int(cx - reach); x <= int(cx+reach); x++ {
			a := float32(min((reach-math.Abs(float64(x)-cx))/6, 1)) // the ends fade out
			if a <= 0 || x < 0 || x >= l.Cols {
				continue
			}
			fx := float64(x)
			y := ys(fx)
			row := int(math.Round(y))
			if row < 0 || row >= l.Rows {
				continue
			}
			i := row*l.Cols + x
			f := &l.Cells[i]
			switch L := occ[i]; {
			case L < 0: // a line of glyphs on a band of dark, unbroken over the chat
				f.Ch = threadGlyph(ys(fx+0.5)-ys(fx-0.5), y-float64(row))
				f.FG = mix(Backdrop, threadCol[k], a)
				if f.A < 0.6*a {
					f.Tint, f.A = Backdrop, 0.6*a
				}
			case (int(L)+k)%2 == 0: // over the letter: a darker line across its fill
				f.Ch, f.FG = '=', mix(threadCol[k], Backdrop, 0.45)
			}
		}
	}
}

// threadGlyph : the glyph of a thread by its slope (rows per column) and
// where it crosses its cell (-0.5 top .. 0.5 bottom).
func threadGlyph(slope, frac float64) byte {
	switch {
	case slope < -0.45:
		return '/'
	case slope > 0.45:
		return '\\'
	case frac > 0.2:
		return '_'
	case frac < -0.2:
		return '~'
	}
	return '-'
}
