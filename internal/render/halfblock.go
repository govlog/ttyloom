package render

import (
	"image"

	"github.com/govlog/ttyloom/internal/theme"
)

// Halfblocks turns an image into lines of ▀ (fg = top pixel, bg = bottom
// pixel), nearest sampling to cols x 2·rows pixels. Transparent = default colour.
func Halfblocks(img image.Image, cols, rows int) []Line {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil
	}
	lines := make([]Line, rows)
	for y := 0; y < rows; y++ {
		var spans []Span
		for x := 0; x < cols; x++ {
			px := b.Min.X + x*w/cols
			top, tok := pix(img, px, b.Min.Y+(2*y)*h/(2*rows))
			bot, bok := pix(img, px, b.Min.Y+(2*y+1)*h/(2*rows))
			var sp Span
			switch {
			case tok && bok:
				sp = Span{"▀", theme.Style{FG: top, BG: bot}}
			case tok:
				sp = Span{"▀", theme.Style{FG: top}}
			case bok:
				sp = Span{"▄", theme.Style{FG: bot}}
			default:
				sp = Span{" ", theme.Style{}}
			}
			if n := len(spans); n > 0 && spans[n-1].Style == sp.Style {
				spans[n-1].Text += sp.Text
			} else {
				spans = append(spans, sp)
			}
		}
		lines[y].Spans = spans
	}
	return lines
}

func pix(img image.Image, x, y int) (theme.Color, bool) {
	r, g, b, a := img.At(x, y).RGBA()
	if a < 0x8000 {
		return theme.Color{}, false
	}
	return theme.Color{Kind: 2, RGB: theme.RGB{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8)}}, true
}
