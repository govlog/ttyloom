package intro

import (
	"math"
	"strings"

	"github.com/govlog/ttyloom/internal/theme"
)

func init() { parts = append(parts, newMatrix) }

// matrix : falling glyphs. From 2 s, the ones that fall on the logo stay
// there and form it; then the logo decrypts into the motto, word after word,
// and back.
type matrix struct{}

func newMatrix() part { return matrix{} }

func (matrix) Name() string { return "matrix" }

var (
	rainCol  = theme.RGB{R: 0xa6, G: 0xe3, B: 0xa1}
	rainDark = theme.RGB{R: 0x14, G: 0x2a, B: 0x1a}
)

// Times of the part, in seconds.
const (
	lockFrom = 2.0 // the rain starts to lay the logo
	lockBy   = 5.8 // the logo is whole
)

// words : what the logo turns into, and when.
var words = []struct {
	at   float64
	text string
	col  theme.RGB
}{
	{8.5, "MANY NETWORKS", colPink},
	{11, "ONE TERMINAL", colBlue},
	{13.5, logoText, theme.RGB{}},
}

// drop : the rain of one column.
type drop struct {
	speed, period, offset float64
	trail                 float64
}

func dropOf(x, rows int) drop {
	trail := 6 + 16*float64(hash(x, 2))
	return drop{
		speed:  8 + 14*float64(hash(x, 1)),
		trail:  trail,
		period: float64(rows) + trail + 10*float64(hash(x, 3)),
		offset: 100 * float64(hash(x, 4)),
	}
}

// head : the row of the head of the drop at t.
func (d drop) head(t float64) float64 { return math.Mod(t*d.speed+d.offset, d.period) }

// passes : the first time from t0 on that the head crosses row y.
func (d drop) passes(y int, t0 float64) float64 {
	k := math.Ceil((t0*d.speed + d.offset - float64(y)) / d.period)
	return (float64(y) + k*d.period - d.offset) / d.speed
}

// rainGlyph : the glyph of a cell at t; each cell changes at its own pace.
func rainGlyph(x, y int, t float64) byte {
	n := int(t*5 + float64(hash(x, y))*7)
	return byte(33 + int(hash(x+n*131, y*977+n)*93))
}

func (matrix) Draw(l *Layer, c Canvas, t float64) {
	if c.Cols < 1 || c.Rows < 1 {
		return
	}
	for x := 0; x < c.Cols; x++ {
		if hash(x, 5) > 0.65 { // a column in three is dry: the chat shows there
			continue
		}
		d := dropOf(x, c.Rows)
		hy := d.head(t)
		for y := max(int(hy-d.trail), 0); y <= min(int(hy), c.Rows-1); y++ {
			age := hy - float64(y)
			v := float32(1 - age/d.trail)
			if v <= 0 {
				continue
			}
			f := l.At(x, y)
			f.Ch = rainGlyph(x, y, t)
			f.FG = mix(Backdrop, rainCol, v)
			if age < 1 {
				f.FG = white
			}
			f.Tint, f.A = rainDark, 0.4*v
		}
	}
	g := logoFor(c)
	if t < words[0].at {
		if t < lockFrom {
			return
		}
		x0, y0 := g.origin(c)
		lockAt := func(lc logoCell) float64 {
			x, y := x0+lc.X, y0+lc.Y
			at := dropOf(x, c.Rows).passes(y, lockFrom)
			if at > lockBy {
				at = lockFrom + (lockBy-lockFrom)*float64(hash(x, y))
			}
			return at
		}
		drawLogo(l, c, g, t, func(lc logoCell) (int, int, bool) { return 0, 0, t >= lockAt(lc) })
		for _, lc := range g.Cells { // a cell just laid flashes
			if age := t - lockAt(lc); age >= 0 && age < 0.2 {
				if f := l.At(x0+lc.X, y0+lc.Y); f != nil {
					f.Tint = mix(lc.FG, white, float32(1-age/0.2))
				}
			}
		}
		return
	}
	k := 0
	for k+1 < len(words) && t >= words[k+1].at {
		k++
	}
	prev := g
	if k > 0 {
		prev = wordLogo(words[k-1].text, words[k-1].col, c)
	}
	decrypt(l, c, prev, wordLogo(words[k].text, words[k].col, c), t-words[k].at)
}

// wordLogo lays a word out in the font the screen has room for, on several
// lines when one is too wide. A zero col gives the colours of the logo.
func wordLogo(text string, col theme.RGB, c Canvas) logo {
	colour := letterColour
	if col != (theme.RGB{}) {
		colour = func(int) theme.RGB { return col }
	}
	font, gap := fontFor(c)
	lines := []string{text}
	if one := layout(text, font, gap, colour); one.W > c.Cols-2 {
		lines = strings.Fields(text)
	}
	g := logo{Lit: map[[2]int]bool{}}
	var parts []logo
	for _, s := range lines {
		p := layout(s, font, gap, colour)
		parts = append(parts, p)
		g.W = max(g.W, p.W)
	}
	y, letters := 0, 0
	for _, p := range parts {
		dx, n := (g.W-p.W)/2, 0
		for _, lc := range p.Cells {
			n = max(n, lc.Letter+1)
			lc.X, lc.Y, lc.Letter = lc.X+dx, lc.Y+y, lc.Letter+letters
			g.Cells = append(g.Cells, lc)
			g.Lit[[2]int{lc.X, lc.Y}] = true
		}
		letters += n
		y += p.H + 1
	}
	g.H = max(y-1, 0)
	return g
}

// decrypt : the word cur comes in over tau seconds, letter after letter:
// first glyphs of the rain in its shape, then solid. The word before it
// falls apart into glyphs meanwhile.
func decrypt(l *Layer, c Canvas, prev, cur logo, tau float64) {
	if tau < 0.4 {
		px, py := prev.origin(c)
		for _, lc := range prev.Cells {
			x, y := px+lc.X, py+lc.Y
			if f := l.At(x, y); f != nil && float64(hash(x, y)) > tau/0.4 {
				*f = FX{Ch: rainGlyph(x, y, tau*3), FG: rainCol, Tint: rainDark, A: 0.8}
			}
		}
	}
	x0, y0 := cur.origin(c)
	for _, lc := range cur.Cells {
		x, y := x0+lc.X, y0+lc.Y
		appear := 0.25 + 0.04*float64(lc.Letter)
		settle := 0.6 + 0.09*float64(lc.Letter) + 0.15*float64(hash(x, y))
		f := l.At(x, y)
		if f == nil || tau < appear {
			continue
		}
		if tau < settle { // glyphs of the rain, on the shape of the letter
			*f = FX{Ch: rainGlyph(x, y, tau*4), FG: mix(rainCol, white, 0.6), Tint: mix(Backdrop, lc.FG, 0.4), A: 0.95}
			continue
		}
		if s := l.At(x+1, y+1); s != nil && !cur.Lit[[2]int{lc.X + 1, lc.Y + 1}] {
			*s = FX{Ch: ' ', Tint: shadowCol, A: 0.85}
		}
		if lc.Ch != 0 {
			*f = FX{Ch: lc.Ch, FG: lc.FG, Tint: shadowCol, A: 0.85}
		} else {
			*f = FX{Ch: ' ', Tint: lc.FG, A: 1}
		}
	}
}
