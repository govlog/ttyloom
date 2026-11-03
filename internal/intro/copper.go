package intro

import (
	"math"
	"sort"
	"strings"

	"github.com/govlog/ttyloom/internal/theme"
)

func init() { parts = append(parts, newCopper) }

// copper : copper bars swinging on sines, in front of the logo or behind it
// as they come and go, and a sine scroller at the bottom. The bars only tint
// the cells: the chat stays readable through them.
type copper struct{}

func newCopper() part { return copper{} }

func (copper) Name() string { return "copper" }

const scrollSpeed = 18.0 // columns per second

type bar struct {
	y     float64
	col   theme.RGB
	front float64 // > 0: in front of the logo
}

func (copper) Draw(l *Layer, c Canvas, t float64) {
	if c.Cols < 1 || c.Rows < 1 {
		return
	}
	bars := make([]bar, len(palette6))
	swing := math.Max(float64(c.Rows)/2-3, 1)
	for i, col := range palette6 {
		ph := t*1.4 + float64(i)*0.52
		bars[i] = bar{y: float64(c.Rows)/2 + swing*math.Sin(ph), col: col, front: math.Cos(ph)}
	}
	sort.Slice(bars, func(a, b int) bool { return bars[a].front < bars[b].front }) // far first
	occ := make([]int8, len(l.Cells))
	for i := range occ {
		occ[i] = -1
	}
	g := logoFor(c)
	logoDone := false
	for _, b := range bars {
		if b.front > 0 && !logoDone {
			occ = drawLogo(l, c, g, t, nil)
			logoDone = true
		}
		drawBar(l, c, b, occ)
	}
	if !logoDone {
		drawLogo(l, c, g, t, nil)
	}
	scroller(l, c, t)
}

// drawBar : one bar, five rows of a gradient from dark to its colour and
// back, with a light core. Over the logo it only shades the letters.
func drawBar(l *Layer, c Canvas, b bar, occ []int8) {
	for row := int(b.y) - 3; row <= int(b.y)+3; row++ {
		v := 1 - math.Abs(float64(row)-b.y)/2.6
		if v <= 0 || row < 0 || row >= c.Rows {
			continue
		}
		col := mix(Backdrop, b.col, float32(v))
		if v > 0.75 {
			col = mix(col, white, float32((v-0.75)*1.4))
		}
		for x := 0; x < c.Cols; x++ {
			i := row*c.Cols + x
			f := &l.Cells[i]
			if occ[i] >= 0 {
				f.Tint = mix(f.Tint, col, 0.6*float32(v))
				continue
			}
			f.Tint, f.A = mix(f.Tint, col, float32(v)), max(f.A, 0.85*float32(v))
		}
	}
}

// scroller : the text of the demo, scrolling right to left on a sine near
// the bottom: in the small font of the logo when there is room for it, in
// plain text otherwise.
func scroller(l *Layer, c Canvas, t float64) {
	text := "TTYLOOM  *  MANY NETWORKS. ONE TERMINAL.  *  "
	if c.Version != "" {
		text += strings.ToUpper(c.Version) + "  *  "
	}
	hueAt := func(x int) theme.RGB {
		h := math.Mod(float64(x)*0.03+t*0.4, 3)
		k := int(h)
		return mix(threadCol[k], threadCol[(k+1)%3], float32(h-float64(k)))
	}
	switch {
	case c.Rows < 16: // no room under the logo
		return
	case c.Rows < 26:
		lineScroller(l, c, t, text, hueAt)
		return
	}
	strip := layout(text, smallFont, 1, func(int) theme.RGB { return white })
	period := strip.W + 2
	shift := int(t * scrollSpeed)
	base := c.Rows - 9 // its sine stays above the hint
	for _, lc := range strip.Cells {
		// The strip repeats: every copy that falls on the screen.
		for x := lc.X - shift%period; x < c.Cols; x += period {
			if x < 0 {
				continue
			}
			y := base + lc.Y + int(math.Round(1.4*math.Sin(float64(x)*0.08+t*3)))
			edge := float32(min(float64(min(x, c.Cols-1-x))/6, 1)) // the ends fade
			col := mix(Backdrop, chrome(hueAt(x), float64(lc.Y)/float64(max(strip.H-1, 1))), edge)
			if s := l.At(x+1, y+1); s != nil && s.Ch != ' ' {
				*s = FX{Ch: ' ', Tint: shadowCol, A: 0.8 * edge}
			}
			if f := l.At(x, y); f != nil {
				*f = FX{Ch: ' ', Tint: col, A: edge}
			}
		}
	}
}

// lineScroller : the scroller in plain text, on a gentle wave.
func lineScroller(l *Layer, c Canvas, t float64, text string, hueAt func(int) theme.RGB) {
	base := c.Rows - 5
	shift := int(t * scrollSpeed)
	for x := 0; x < c.Cols; x++ {
		ch := text[(x+shift)%len(text)]
		if ch == ' ' {
			continue
		}
		f := l.At(x, base+int(math.Round(math.Sin(float64(x)*0.07+t*3))))
		if f == nil {
			continue
		}
		edge := float32(min(float64(min(x, c.Cols-1-x))/6, 1))
		f.Ch, f.FG = ch, mix(Backdrop, mix(hueAt(x), white, 0.25), edge)
		if f.A < 0.5 {
			f.Tint, f.A = Backdrop, 0.5
		}
	}
}
