package media

import (
	"context"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
)

// noise : a w x h picture that does not compress to nothing.
func noise(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(i*7 + i/w)
	}
	img.Set(0, 0, color.White)
	return img
}

// BenchmarkLoadGIFFitted : a 480x270 GIF of 60 frames at 100 ms, fitted to
// 240x135 — every frame scaled.
func BenchmarkLoadGIFFitted(b *testing.B) {
	g := &gif.GIF{Config: image.Config{Width: 480, Height: 270}}
	for i := range 60 {
		fr := image.NewPaletted(image.Rect(0, 0, 480, 270), palette.Plan9)
		for k := range fr.Pix {
			fr.Pix[k] = uint8(k*3 + i)
		}
		g.Image, g.Delay = append(g.Image, fr), append(g.Delay, 10)
	}
	p := filepath.Join(b.TempDir(), "a.gif")
	f, err := os.Create(p)
	if err != nil {
		b.Fatal(err)
	}
	if err := gif.EncodeAll(f, g); err != nil {
		b.Fatal(err)
	}
	f.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if fr, err := Load(context.Background(), p, "image/gif", 240, 135, 100); err != nil || len(fr.PNG) != 60 {
			b.Fatal(err)
		}
	}
}

// BenchmarkKittyTransmit : one 22 KB frame of a GIF-box preview sent again
// at each animation tick.
func BenchmarkKittyTransmit(b *testing.B) {
	data := make([]byte, 22<<10)
	b.ReportAllocs()
	for range b.N {
		KittyTransmit(1, data)
	}
}

// BenchmarkEncodeFrame : one 480x270 frame of a played video.
func BenchmarkEncodeFrame(b *testing.B) {
	img := noise(480, 270)
	b.ReportAllocs()
	for range b.N {
		if _, err := encode(img); err != nil {
			b.Fatal(err)
		}
	}
}
