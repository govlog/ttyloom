package intro

import (
	"image"
	"image/color"
	"slices"

	"github.com/govlog/ttyloom/internal/theme"
)

// FX : what an effect puts on one cell.
type FX struct {
	Ch   byte      // glyph of the effect, 0: the chat glyph stays
	Fill byte      // glyph with no Ch, on a blank cell of the chat only: its text shows through
	FG   theme.RGB // colour of Ch, or of Fill
	Tint theme.RGB // colour the background goes toward
	A    float32   // 0..1: how far the background goes toward Tint
}

// Layer : the effect over the whole screen.
type Layer struct {
	Cols, Rows int
	Cells      []FX
}

func NewLayer(cols, rows int) *Layer {
	cols, rows = max(cols, 0), max(rows, 0)
	return &Layer{Cols: cols, Rows: rows, Cells: make([]FX, cols*rows)}
}

// At gives the cell at col, row; nil outside the layer, or with no layer.
func (l *Layer) At(col, row int) *FX {
	if l == nil || col < 0 || row < 0 || col >= l.Cols || row >= l.Rows {
		return nil
	}
	return &l.Cells[row*l.Cols+col]
}

// Resolve gives the RGB value a colour of the chat shows on the screen; false
// when it is unknown. fg tells which default a Kind 0 colour stands for.
type Resolve func(c theme.Color, fg bool) (theme.RGB, bool)

// Backdrop : the dark the chat goes toward while the demo plays, the
// background of the logo (docs/logo.svg).
var Backdrop = theme.RGB{R: 0x11, G: 0x11, B: 0x1b}

// How far dark = 1 takes a background and a text colour toward Backdrop: the
// text stays readable behind the effect.
const (
	darkBG  = 0.8
	darkFG  = 0.5
	darkPic = 0.6 // a picture of the chat: dimmed, still seen
)

// Composite lays fx over chat. dark (0..1) is how far the chat goes toward
// Backdrop. A text colour rgb cannot resolve is left as it is; an unknown
// background takes Backdrop at once. A cell nothing touches comes out exactly
// as it went in.
func Composite(chat *Screen, fx *Layer, dark float32, rgb Resolve) *Screen {
	out := &Screen{Cols: chat.Cols, Rows: chat.Rows, Cells: slices.Clone(chat.Cells)}
	for y := 0; y < out.Rows; y++ {
		for x := 0; x < out.Cols; x++ {
			var f FX
			if p := fx.At(x, y); p != nil {
				f = *p
			}
			if dark <= 0 && f.A <= 0 && f.Ch == 0 && f.Fill == 0 {
				continue
			}
			c := &out.Cells[y*out.Cols+x]
			c.Style = blend(c.Style, f, dark, rgb)
			if f.Ch == 0 && c.Text == " " {
				f.Ch = f.Fill
			}
			if f.Ch == 0 {
				continue
			}
			// The glyph of the effect takes the cell: a wide cluster it cuts
			// leaves a blank on its other half.
			if c.Text == "" {
				if l := out.At(x-1, y); l != nil {
					l.Text = " "
				}
			} else if r := out.At(x+1, y); r != nil && r.Text == "" {
				r.Text = " "
			}
			c.Text = glyphs[f.Ch&0x7f]
			if c.Style.Reverse { // its colours are unknown: it keeps the background it shows
				c.Style = theme.Style{FG: c.Style.FG, BG: theme.Color{Kind: 2, RGB: f.FG}, Reverse: true}
			} else {
				c.Style = theme.Style{FG: theme.Color{Kind: 2, RGB: f.FG}, BG: c.Style.BG}
			}
		}
	}
	return out
}

// Veil : what the demo lays on the cols x rows cells at col, row where a
// picture of the chat lies — under the text, over the backgrounds (kitty,
// z < 0), so that the dark and the tints, backgrounds, would stay under it.
// Each cell gives sx x sy pixels: the dark of a picture, then the tint of the
// cell, in one colour and one opacity to lay over the picture.
func Veil(fx *Layer, dark float32, col, row, cols, rows, sx, sy int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, max(cols, 0)*sx, max(rows, 0)*sy))
	a1 := min(max(darkPic*dark, 0), 1)
	for y := range rows {
		for x := range cols {
			var f FX
			if p := fx.At(col+x, row+y); p != nil {
				f = *p
			}
			a2 := min(max(f.A, 0), 1)
			// over a picture P: mix(mix(P, Backdrop, a1), Tint, a2) = mix(P, c, a)
			a := 1 - (1-a1)*(1-a2)
			if a <= 0 {
				continue
			}
			ch := func(b, t uint8) uint8 {
				return uint8((float32(b)*a1*(1-a2)+float32(t)*a2)/a + 0.5)
			}
			px := color.NRGBA{R: ch(Backdrop.R, f.Tint.R), G: ch(Backdrop.G, f.Tint.G), B: ch(Backdrop.B, f.Tint.B), A: uint8(a*255 + 0.5)}
			for py := y * sy; py < (y+1)*sy; py++ {
				for qx := x * sx; qx < (x+1)*sx; qx++ {
					img.SetNRGBA(qx, py, px)
				}
			}
		}
	}
	return img
}

// blend darkens st toward Backdrop and tints its background toward f.Tint.
// A reverse cell shows its FG as background: it is blended swapped and
// written without Reverse, unless one of its colours is unknown.
func blend(st theme.Style, f FX, dark float32, rgb Resolve) theme.Style {
	fgC, bgC := st.FG, st.BG
	fgOf, bgOf := true, false // which default a Kind 0 colour means
	if st.Reverse {
		fgC, bgC, fgOf, bgOf = bgC, fgC, false, true
	}
	fg, okf := rgb(fgC, fgOf)
	bg, okb := rgb(bgC, bgOf)
	if st.Reverse && (!okf || !okb) {
		return st
	}
	under := bg // what the text was read on
	if !okb {
		// No known background (no answer to OSC 11): the backdrop comes at
		// once, with no fade, so that the fills of the demo show on it.
		bg, under = Backdrop, Backdrop
		dark = max(dark, 1)
	}
	bg = mix(bg, Backdrop, darkBG*dark)
	if f.A > 0 {
		bg = mix(bg, f.Tint, f.A)
	}
	bgC = theme.Color{Kind: 2, RGB: bg}
	if okf {
		// Text lighter than its background dims with it; darker text (a light
		// theme) turns light, or it would vanish on the backdrop.
		if luma(fg) < luma(under) {
			fg = mix(fg, lightText, darkBG*dark)
		} else {
			fg = mix(fg, Backdrop, darkFG*dark)
		}
		fgC = theme.Color{Kind: 2, RGB: fg}
	}
	st.FG, st.BG, st.Reverse = fgC, bgC, false
	return st
}

// glyphs : the string of each ASCII glyph, made once: string(byte) would
// allocate at every cell of every frame.
var glyphs = func() (g [128]string) {
	for i := range g {
		g[i] = string(rune(i))
	}
	return g
}()

// lightText : what dark text turns into on the backdrop.
var lightText = theme.RGB{R: 0xba, G: 0xc2, B: 0xde}

// luma : the brightness of c, 0..255.
func luma(c theme.RGB) int { return (int(c.R)*299 + int(c.G)*587 + int(c.B)*114) / 1000 }

// mix goes from a toward b by w (0..1), channel by channel.
func mix(a, b theme.RGB, w float32) theme.RGB {
	w = min(max(w, 0), 1)
	ch := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*w + 0.5) }
	return theme.RGB{R: ch(a.R, b.R), G: ch(a.G, b.G), B: ch(a.B, b.B)}
}
