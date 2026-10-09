package media

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
)

// sixelDecode plays a sixel sequence the way a terminal does: an image of
// the raster size, alpha 255 where a pixel was painted and 0 where the
// screen stays as it was, plus the number of registers defined. A register
// never painted with, or a pixel painted outside the raster, is an error.
func sixelDecode(seq string) (*image.NRGBA, int, error) {
	body, ok := strings.CutPrefix(seq, "\x1bP")
	if !ok {
		return nil, 0, errors.New("no DCS")
	}
	body, ok = strings.CutSuffix(body, "\x1b\\")
	if !ok {
		return nil, 0, errors.New("no ST")
	}
	params, body, ok := strings.Cut(body, "q")
	if p := strings.Split(params, ";"); !ok || len(p) < 2 || p[1] != "1" {
		return nil, 0, fmt.Errorf("parameters %q: P2=1 keeps the pixels not painted", params)
	}
	var img *image.NRGBA
	regs := map[int]color.NRGBA{}
	used := map[int]bool{}
	cur, x, y := -1, 0, 0
	paint := func(ch byte, n int) error {
		if ch < '?' || ch > '~' {
			return fmt.Errorf("sixel %q", ch)
		}
		if img == nil || cur < 0 {
			return errors.New("sixel before the raster or a colour")
		}
		for range n {
			for bit := range 6 {
				if (ch-'?')>>bit&1 == 0 {
					continue
				}
				if !(image.Point{x, y + bit}.In(img.Rect)) {
					return fmt.Errorf("pixel %d,%d outside the raster %v", x, y+bit, img.Rect)
				}
				img.SetNRGBA(x, y+bit, regs[cur])
				used[cur] = true
			}
			x++
		}
		return nil
	}
	for i := 0; i < len(body); {
		ch := body[i]
		i++
		var v []int
		if ch == '"' || ch == '#' || ch == '!' {
			var n int
			v, n = sixelNums(body[i:])
			i += n
		}
		switch {
		case ch == '"':
			if len(v) != 4 || v[0] != 1 || v[1] != 1 {
				return nil, 0, fmt.Errorf("raster %v", v)
			}
			img = image.NewNRGBA(image.Rect(0, 0, v[2], v[3]))
		case ch == '#' && len(v) == 5:
			if v[1] != 2 || max(v[2], v[3], v[4]) > 100 {
				return nil, 0, fmt.Errorf("register %v", v)
			}
			pct := func(p int) uint8 { return uint8((p*255 + 50) / 100) }
			regs[v[0]], cur = color.NRGBA{pct(v[2]), pct(v[3]), pct(v[4]), 255}, v[0]
		case ch == '#' && len(v) == 1:
			if _, ok := regs[v[0]]; !ok {
				return nil, 0, fmt.Errorf("register %d not defined", v[0])
			}
			cur = v[0]
		case ch == '!' && len(v) == 1 && i < len(body):
			if err := paint(body[i], v[0]); err != nil {
				return nil, 0, err
			}
			i++
		case ch == '$':
			x = 0
		case ch == '-':
			x, y = 0, y+6
		default:
			if err := paint(ch, 1); err != nil {
				return nil, 0, err
			}
		}
	}
	for r := range regs {
		if !used[r] {
			return nil, 0, fmt.Errorf("register %d defined, never painted with", r)
		}
	}
	return img, len(regs), nil
}

// sixelNums reads the numbers separated by ';' at the start of s, and how
// many bytes they take. An empty one is 0, as for any DEC parameter.
func sixelNums(s string) ([]int, int) {
	var v []int
	for i := 0; ; {
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		n, _ := strconv.Atoi(s[i:j])
		v = append(v, n)
		if j == len(s) || s[j] != ';' {
			return v, j
		}
		i = j + 1
	}
}

// sixelClose : a channel goes through the 0..100 of a register within 1.
func sixelClose(a, b color.NRGBA) bool {
	d := func(x, y uint8) bool { return max(x, y)-min(x, y) <= 1 }
	return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B)
}

// sixelDistinct : how many colours img holds, so a test knows it goes past
// the 256 registers and through the quantiser.
func sixelDistinct(img *image.RGBA) int {
	seen := map[color.RGBA]bool{}
	for y := range img.Rect.Dy() {
		for x := range img.Rect.Dx() {
			seen[img.RGBAAt(img.Rect.Min.X+x, img.Rect.Min.Y+y)] = true
		}
	}
	return len(seen)
}

// sixelPhoto : smooth colour fields under a fine grain, the thousands of
// colours of a photo.
func sixelPhoto(w, h int) *image.RGBA {
	rng := rand.New(rand.NewPCG(1, 2))
	ch := func(v float64) uint8 { return uint8(min(max(v+rng.NormFloat64()*6, 0), 255)) }
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			fx, fy := float64(x)/float64(w), float64(y)/float64(h)
			img.SetRGBA(x, y, color.RGBA{
				ch(128 + 110*math.Sin(5*fx+2*fy)),
				ch(128 + 110*math.Sin(3*fy-4*fx+1)),
				ch(128 + 110*math.Cos(7*fx*fy+0.5)),
				255,
			})
		}
	}
	return img
}

// TestSixelExactColours : 256 colours, as many as the registers hold, come
// back as they were at every pixel, no dithering, though they sit so close
// that any quantisation would merge them. Alpha under 128 is never painted
// and takes no register; alpha 128 is painted, with its own colour.
func TestSixelExactColours(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 24, 18))
	for y := range 18 {
		for x := range 24 {
			c := color.NRGBA{200, 0, 0, 127}
			switch {
			case x < 16 && y < 16:
				c = color.NRGBA{uint8(100 + x), uint8(120 + y), 60, 255}
				if x == 15 && y == 15 {
					c.A = 128
				}
			case x >= 20 && y < 16:
				c = color.NRGBA{100, 120, 60, 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	got, _, err := sixelDecode(Sixel(img))
	if err != nil {
		t.Fatal(err)
	}
	for y := range 18 {
		for x := range 24 {
			want, px := img.NRGBAAt(x, y), got.NRGBAAt(x, y)
			if want.A < 128 && px.A != 0 {
				t.Fatalf("(%d,%d) painted %v, alpha %d is transparent", x, y, px, want.A)
			}
			if want.A >= 128 && (px.A != 255 || !sixelClose(px, want)) {
				t.Fatalf("(%d,%d) = %v, want %v", x, y, px, want)
			}
		}
	}
}

// TestSixelPartialBand : 13 rows make two bands of 6 and one of a single
// row; that row is painted, nothing below it. The image does not start at
// (0,0) and is premultiplied, as the scaler gives it: a soft edge shows its
// own colour, not a darker one.
func TestSixelPartialBand(t *testing.T) {
	r := image.Rect(3, 5, 10, 18)
	img := image.NewRGBA(r)
	cols := []color.RGBA{{200, 30, 30, 255}, {30, 200, 30, 255}, {30, 30, 200, 255}}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, cols[(x+2*y)%3])
		}
	}
	img.Set(6, 9, color.NRGBA{200, 30, 30, 160})
	got, _, err := sixelDecode(Sixel(img))
	if err != nil {
		t.Fatal(err)
	}
	if got.Rect != image.Rect(0, 0, 7, 13) {
		t.Fatalf("raster %v, want 7x13", got.Rect)
	}
	for y := range 13 {
		for x := range 7 {
			want := color.NRGBAModel.Convert(img.At(r.Min.X+x, r.Min.Y+y)).(color.NRGBA)
			if px := got.NRGBAAt(x, y); px.A != 255 || !sixelClose(px, want) {
				t.Fatalf("(%d,%d) = %v, want %v", x, y, px, want)
			}
		}
	}
}

// TestSixelPhoto : a round avatar of thousands of colours goes through 256
// registers at most, every pixel close to the source on average, and its
// transparent corners stay unpainted. On this image the palette made for it
// gives a mean error of 5 to 6, a fixed 6x6x6 one 17.
func TestSixelPhoto(t *testing.T) {
	const w, h = 160, 120
	img := sixelPhoto(w, h)
	for y := range h {
		for x := range w {
			if (x-w/2)*(x-w/2)+(y-h/2)*(y-h/2) > 55*55 {
				img.SetRGBA(x, y, color.RGBA{})
			}
		}
	}
	if n := sixelDistinct(img); n <= 256 {
		t.Fatalf("%d colours: the test needs more than 256", n)
	}
	got, regs, err := sixelDecode(Sixel(img))
	if err != nil {
		t.Fatal(err)
	}
	if regs > 256 {
		t.Fatalf("%d registers, 256 at most", regs)
	}
	var sum [3]int
	n := 0
	for y := range h {
		for x := range w {
			s, px := img.RGBAAt(x, y), got.NRGBAAt(x, y)
			if s.A == 0 {
				if px.A != 0 {
					t.Fatalf("(%d,%d) painted %v, it is transparent", x, y, px)
				}
				continue
			}
			if px.A != 255 {
				t.Fatalf("(%d,%d) not painted", x, y)
			}
			for c, d := range [3]int{int(px.R) - int(s.R), int(px.G) - int(s.G), int(px.B) - int(s.B)} {
				sum[c] += max(d, -d)
			}
			n++
		}
	}
	for c, s := range sum {
		if mae := float64(s) / float64(n); mae >= 12 {
			t.Fatalf("channel %d: mean error %.2f/255, want under 12", c, mae)
		}
	}
}

// TestSixelDithers : a grey ramp under a light grain holds more greys than
// the palette, which keeps about one per 8 levels. Averaged down a column,
// the error diffusion follows the source within a fraction of a level (0.39
// here); the nearest colour alone leaves bands (1.25).
func TestSixelDithers(t *testing.T) {
	const w, h = 256, 24
	rng := rand.New(rand.NewPCG(3, 4))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			ch := func() uint8 { return uint8(min(max(float64(x)+rng.NormFloat64(), 0), 255)) }
			img.SetRGBA(x, y, color.RGBA{ch(), ch(), ch(), 255})
		}
	}
	if n := sixelDistinct(img); n <= 256 {
		t.Fatalf("%d colours: the test needs more than 256", n)
	}
	got, _, err := sixelDecode(Sixel(img))
	if err != nil {
		t.Fatal(err)
	}
	var off float64
	for x := range w {
		var d [3]int
		for y := range h {
			s, px := img.RGBAAt(x, y), got.NRGBAAt(x, y)
			d[0] += int(px.R) - int(s.R)
			d[1] += int(px.G) - int(s.G)
			d[2] += int(px.B) - int(s.B)
		}
		for _, v := range d {
			off += float64(max(v, -v)) / h
		}
	}
	if mean := off / (3 * w); mean >= 0.7 {
		t.Fatalf("columns off by %.2f on average: the ramp shows bands", mean)
	}
}

// TestSixelRunLength : 4 equal sixels or more go as !<count><sixel>, so a
// flat image costs a few bytes a band whatever its width.
func TestSixelRunLength(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 600, 12))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	if s := Sixel(img); len(s) > 64 {
		t.Fatalf("%d bytes: %q", len(s), s)
	}
}

// BenchmarkSixel : an 800x600 photo, a large inline preview.
func BenchmarkSixel(b *testing.B) {
	img := sixelPhoto(800, 600)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		Sixel(img)
	}
}
