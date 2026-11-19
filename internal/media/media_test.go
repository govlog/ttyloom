package media

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/govlog/ttyloom/internal/model"
)

func TestFileName(t *testing.T) {
	d := time.Date(2026, 8, 29, 12, 3, 1, 0, time.UTC)
	key := model.ChatKey{Net: "telegram", ID: 7}
	name := FileName(key, "Antonio Gómez", 12, d, ".jpg")
	if name != "20260829-120301_telegram_7_Antonio_G_mez_12.jpg" {
		t.Fatal(name)
	}
	key.ID++
	if got := FileName(key, "Antonio Gómez", 12, d, ".jpg"); got == name {
		t.Fatal(got)
	}
}

func TestKittyChunks(t *testing.T) {
	// The data goes as base64 in chunks of 4096 characters at most, the keys
	// on the first, m=1 on every chunk but the last; joined, they give it back.
	for _, n := range []int{1, 2, 3, 3071, 3072, 3073, 6144, 6145, 22 << 10} {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(i * 7)
		}
		s := KittyDisplay(7, 3, data, 10, 5, 0)
		parts := strings.Split(strings.TrimSuffix(s, "\x1b\\"), "\x1b\\")
		var enc strings.Builder
		for i, p := range parts {
			head, payload, ok := strings.Cut(strings.TrimPrefix(p, "\x1b_G"), ";")
			want := "m=1"
			if i == len(parts)-1 {
				want = "m=0"
			}
			if i == 0 {
				want = "a=T,f=100,i=7,p=3,c=10,r=5,q=2," + want
			}
			if !ok || head != want || len(payload) > chunk {
				t.Fatalf("%d bytes, chunk %d: %q, %d characters", n, i, head, len(payload))
			}
			enc.WriteString(payload)
		}
		if got, err := base64.StdEncoding.DecodeString(enc.String()); err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%d bytes: the chunks do not give the data back (%v)", n, err)
		}
	}
	if got := KittyPlace(7, 3, 10, 5, 0); got != "\x1b_Ga=p,i=7,p=3,c=10,r=5,q=2\x1b\\" {
		t.Fatal(got)
	}
	// Crop: source sub-rectangle in pixels of the image sent.
	crop := KittyPlace(7, 3, 10, 5, 0, image.Rect(20, 10, 120, 60))
	if crop != "\x1b_Ga=p,i=7,p=3,c=10,r=5,x=20,y=10,w=100,h=50,q=2\x1b\\" {
		t.Fatal(crop)
	}
	if got := KittyPlace(7, 3, 10, 5, 0, image.Rectangle{}); got != "\x1b_Ga=p,i=7,p=3,c=10,r=5,q=2\x1b\\" {
		t.Fatal(got) // empty rectangle: no crop key
	}
	if got := KittyPlace(7, 3, 10, 5, -1); got != "\x1b_Ga=p,i=7,p=3,c=10,r=5,z=-1,q=2\x1b\\" {
		t.Fatal(got) // under the text
	}
}

func TestKittyDeletePlacement(t *testing.T) {
	if got := KittyDeletePlacement(7, 3); got != "\x1b_Ga=d,d=i,i=7,p=3,q=2\x1b\\" {
		t.Fatalf("%q", got)
	}
	if got := KittyDeletePlacement(7, 0); got != "\x1b_Ga=d,d=i,i=7,q=2\x1b\\" { // every placement
		t.Fatalf("%q", got)
	}
}

func TestLoadPNGFit(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for x := 0; x < 400; x++ {
		img.Set(x, 0, color.RGBA{255, 0, 0, 255})
	}
	p := filepath.Join(t.TempDir(), "a.png")
	f, _ := os.Create(p)
	png.Encode(f, img)
	f.Close()
	fr, err := Load(context.Background(), p, "image/png", 100, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.PNG) != 1 || fr.W != 100 || fr.H != 50 {
		t.Fatalf("%d %dx%d", len(fr.PNG), fr.W, fr.H)
	}
	if w, h := Fit(50, 20, 100, 100); w != 50 || h != 20 {
		t.Fatalf("no upscale: %d %d", w, h)
	}
}

func TestLoadGIFFrames(t *testing.T) {
	f1 := image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{color.RGBA{255, 0, 0, 255}})
	f2 := image.NewPaletted(image.Rect(2, 2, 4, 4), color.Palette{color.RGBA{0, 0, 255, 255}})
	g := &gif.GIF{
		Image:  []*image.Paletted{f1, f2},
		Delay:  []int{10, 10},
		Config: image.Config{Width: 4, Height: 4},
	}
	p := filepath.Join(t.TempDir(), "a.gif")
	f, _ := os.Create(p)
	if err := gif.EncodeAll(f, g); err != nil {
		t.Fatal(err)
	}
	f.Close()

	fr, err := Load(context.Background(), p, "image/gif", 100, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.PNG) != 2 || fr.W != 4 || fr.H != 4 || fr.Delay != 100*time.Millisecond {
		t.Fatalf("%d %dx%d %s", len(fr.PNG), fr.W, fr.H, fr.Delay)
	}
	img, err := png.Decode(bytes.NewReader(fr.PNG[1]))
	if err != nil {
		t.Fatal(err)
	}
	red := color.RGBAModel.Convert(img.At(0, 0)).(color.RGBA)
	blue := color.RGBAModel.Convert(img.At(3, 3)).(color.RGBA)
	if red != (color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("(0,0) = %v, want red", red)
	}
	if blue != (color.RGBA{0, 0, 255, 255}) {
		t.Fatalf("(3,3) = %v, want blue", blue)
	}

	fr, err = Load(context.Background(), p, "image/gif", 100, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.PNG) != 1 {
		t.Fatalf("static path: %d frames", len(fr.PNG))
	}
}

// writeGIF : a GIF of n 8x8 frames, delay in hundredths of a second each.
func writeGIF(t *testing.T, n, delay int) string {
	t.Helper()
	g := &gif.GIF{Config: image.Config{Width: 8, Height: 8}}
	for i := range n {
		g.Image = append(g.Image, image.NewPaletted(image.Rect(0, 0, 8, 8), color.Palette{color.Gray{Y: uint8(i)}}))
		g.Delay = append(g.Delay, delay)
	}
	p := filepath.Join(t.TempDir(), "a.gif")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := gif.EncodeAll(f, g); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestLoadGIFFrameCount : a GIF keeps the frames asked, no more — the GIF
// box asks 40 per preview, and a 60-frame GIF used to keep all of them.
func TestLoadGIFFrameCount(t *testing.T) {
	fr, err := Load(context.Background(), writeGIF(t, 60, 10), "image/gif", 100, 100, 40)
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.PNG) != 40 {
		t.Fatalf("%d frames, want the 40 asked", len(fr.PNG))
	}
}

// TestLoadGIFRealSpeed : the animation moves one frame per 100 ms tick at
// most, so a GIF made of 30 ms frames used to play 3.3 times too slowly. It
// keeps a frame per 100 ms of its timeline now: the loop lasts what it
// lasts in a browser.
func TestLoadGIFRealSpeed(t *testing.T) {
	fr, err := Load(context.Background(), writeGIF(t, 30, 3), "image/gif", 100, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	loop := time.Duration(len(fr.PNG)) * max(fr.Delay, 100*time.Millisecond)
	if loop < 800*time.Millisecond || loop > 1200*time.Millisecond {
		t.Fatalf("%d frames at %v: the 0.9 s loop plays in %v", len(fr.PNG), fr.Delay, loop)
	}
}

func TestGIFStopsReadingAtFrameLimit(t *testing.T) {
	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	g := &gif.GIF{Config: image.Config{Width: 2, Height: 2}}
	for range maxFrames {
		g.Image = append(g.Image, frame)
		g.Delay = append(g.Delay, 10)
	}
	var data bytes.Buffer
	if err := gif.EncodeAll(&data, g); err != nil {
		t.Fatal(err)
	}
	// Data after the preview limit must not reach the full GIF decoder.
	body := append(data.Bytes()[:data.Len()-1], 0x2c, 0xff)
	path := filepath.Join(t.TempDir(), "long.gif")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	frames, err := Load(context.Background(), path, "image/gif", 10, 10, maxFrames)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames.PNG) != maxFrames {
		t.Fatalf("frames: %d", len(frames.PNG))
	}
}

// The video decoding stops at the limit asked for and reports its progress.
func TestLoadVideoFramesCap(t *testing.T) {
	if !HaveFFmpeg {
		t.Skip("ffmpeg not available")
	}
	p := filepath.Join(t.TempDir(), "t.mp4")
	// 4 s at 10 fps = 40 frames: enough to see the limit bite (15) and a partial
	// report go through (at 30).
	gen := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi",
		"-i", "testsrc=duration=4:size=64x64:rate=10", p)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("generation: %v %s", err, out)
	}
	fr, err := Load(context.Background(), p, "video/mp4", 64, 64, 15)
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.PNG) != 15 || fr.W != 64 || fr.H != 64 {
		t.Fatalf("%d frames %dx%d", len(fr.PNG), fr.W, fr.H)
	}
	if fr.Delay != 100*time.Millisecond { // fps=10: the real interval
		t.Fatalf("delay %v", fr.Delay)
	}
	var seen []int
	fr, err = Load(context.Background(), p, "video/mp4", 64, 64, 40, func(f *Frames) { seen = append(seen, len(f.PNG)) })
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.PNG) != 40 {
		t.Fatalf("%d frames", len(fr.PNG))
	}
	if len(seen) != 1 || seen[0] != 30 { // only one report: 30, not the last one
		t.Fatalf("progress: %v", seen)
	}
}

// pngHeader builds a PNG with an IHDR only (no pixel data): enough for
// DecodeConfig, which is what the guard reads. depth: bits per channel,
// kind: PNG colour type (0 greyscale, 6 RGBA).
func pngHeader(w, h uint32, depth, kind byte) []byte {
	var ihdr bytes.Buffer
	ihdr.WriteString("IHDR")
	binary.Write(&ihdr, binary.BigEndian, w)
	binary.Write(&ihdr, binary.BigEndian, h)
	ihdr.Write([]byte{depth, kind, 0, 0, 0}) // no interlace
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	binary.Write(&out, binary.BigEndian, uint32(ihdr.Len()-4))
	out.Write(ihdr.Bytes())
	binary.Write(&out, binary.BigEndian, crc32.ChecksumIEEE(ihdr.Bytes()))
	return out.Bytes()
}

// zeroPNG writes a black greyscale PNG of w x h px and gives its path.
func zeroPNG(t *testing.T, w, h int) string { return flatPNG(t, w, h, 8, 0) }

// flatPNG writes a black PNG of w x h px, depth bits a channel, colour type
// kind (0 grey, 2 RGB, 6 RGBA), and gives its path. The chunks are made
// here: png.Encode writes row by row and takes seconds under -race for
// millions of rows.
func flatPNG(t *testing.T, w, h int, depth, kind byte) string {
	t.Helper()
	return writePNG(t, w, h, depth, kind, nil)
}

// writePNG writes a w x h PNG and gives its path: each row is filter byte 0
// then the pixels fill gives (black when fill is nil; fill sets every byte),
// written to zlib by batches of about 1 MB.
func writePNG(t *testing.T, w, h int, depth, kind byte, fill func(row []byte, y int)) string {
	t.Helper()
	var idat bytes.Buffer
	z, _ := zlib.NewWriterLevel(&idat, zlib.BestSpeed)
	n := 1 + (w*map[byte]int{0: 1, 2: 3, 6: 4}[kind]*int(depth)+7)/8 // bytes a row
	batch := make([]byte, n*max(1, (1<<20)/n))
	for y := 0; y < h; {
		k := min(len(batch)/n, h-y)
		for i := 0; fill != nil && i < k; i++ {
			fill(batch[i*n+1:(i+1)*n], y+i)
		}
		z.Write(batch[:k*n])
		y += k
	}
	z.Close()
	b := pngHeader(uint32(w), uint32(h), depth, kind)
	for _, c := range [][]byte{append([]byte("IDAT"), idat.Bytes()...), []byte("IEND")} {
		b = binary.BigEndian.AppendUint32(b, uint32(len(c)-4))
		b = binary.BigEndian.AppendUint32(append(b, c...), crc32.ChecksumIEEE(c))
	}
	p := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestLoadRefusesThinImage : the scaler keeps 1 px of width at least and
// pays per source row and column, so a 1x8388608 PNG of 32 KB took 776 MB in
// Load. Such a shape is refused on its header; a long screenshot still loads.
func TestLoadRefusesThinImage(t *testing.T) {
	for _, d := range [][2]int{{1, 8 << 20}, {8 << 20, 1}, {2, 4 << 20}} {
		p := zeroPNG(t, d[0], d[1])
		size := fmt.Sprintf("%dx%d", d[0], d[1])
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := Load(context.Background(), p, "image/png", 600, 220, 1)
		runtime.ReadMemStats(&after)
		if mb := (after.TotalAlloc - before.TotalAlloc) >> 20; mb > 16 {
			t.Errorf("%s PNG: Load allocated %d MB, want 16 MB at most", size, mb)
		}
		if err == nil || !strings.Contains(err.Error(), size) {
			t.Errorf("%s PNG: err %v, want the size refused", size, err)
		}
	}
	fr, err := Load(context.Background(), zeroPNG(t, 1170, 12000), "image/png", 600, 220, 1)
	if err != nil || len(fr.PNG) != 1 {
		t.Fatalf("1170x12000 screenshot in a 600x220 box: %v, err %v; want one picture", fr, err)
	}
}

// TestLoadRefusesPixelBomb : a 4 KB header claiming 65535x65535 (4 GB once
// decoded) is refused on the announced size, before any allocation.
func TestLoadRefusesPixelBomb(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bomb.png")
	if err := os.WriteFile(p, pngHeader(65535, 65535, 8, 0), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), p, "image/png", 800, 600, 1); err == nil {
		t.Fatal("bomb accepted")
	} else if !strings.Contains(err.Error(), "65535") {
		t.Fatalf("unexpected error: %v", err) // not the size guard
	}
	// A real image of a sane size still goes through the same path.
	ok := filepath.Join(t.TempDir(), "ok.png")
	f, _ := os.Create(ok)
	png.Encode(f, image.NewRGBA(image.Rect(0, 0, 40, 20)))
	f.Close()
	if _, err := Load(context.Background(), ok, "image/png", 800, 600, 1); err != nil {
		t.Fatal(err)
	}
}

// TestLoadRefuses16BitBomb : the cap counts 4 bytes a pixel, and a 16-bit
// image decodes to 8. A 25 Mpx 16-bit PNG, a few hundred KB when uniform,
// took 200 MB once decoded plus the scaler temporary.
func TestLoadRefuses16BitBomb(t *testing.T) {
	p := filepath.Join(t.TempDir(), "deep.png")
	if err := os.WriteFile(p, pngHeader(5000, 5000, 16, 6), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), p, "image/png", 800, 600, 1); err == nil || !strings.Contains(err.Error(), "5000") {
		t.Fatalf("16-bit image of 25 Mpx not refused on its size: %v", err)
	}
}

// TestLoadGIFRefusesPixelBomb : the GIF canvas is bounded harder, because
// gif.DecodeAll decodes every frame before maxFrames applies.
func TestLoadGIFRefusesPixelBomb(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bomb.gif")
	// GIF87a header: canvas 30000x30000, then nothing (DecodeConfig stops there).
	hdr := append([]byte("GIF87a"), 0x30, 0x75, 0x30, 0x75, 0x00, 0x00, 0x00)
	if err := os.WriteFile(p, hdr, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), p, "image/gif", 800, 600, maxFrames); err == nil {
		t.Fatal("gif bomb accepted")
	} else if !strings.Contains(err.Error(), "30000") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestLoadBigGIFShowsFirstFrame : a GIF too big to animate (1920x1080 is over
// maxGIFPixels) but not too big for a still image shows its first frame. It
// used to fail as "image too large".
func TestLoadBigGIFShowsFirstFrame(t *testing.T) {
	pal := color.Palette{color.Black, color.White}
	g := &gif.GIF{Config: image.Config{Width: 1920, Height: 1080}}
	for range 2 {
		g.Image = append(g.Image, image.NewPaletted(image.Rect(0, 0, 1920, 1080), pal))
		g.Delay = append(g.Delay, 10)
	}
	p := filepath.Join(t.TempDir(), "big.gif")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := gif.EncodeAll(f, g); err != nil {
		t.Fatal(err)
	}
	f.Close()
	fr, err := Load(context.Background(), p, "image/gif", 400, 300, 40)
	if err != nil {
		t.Fatalf("1920x1080 animated GIF: %v, want its first frame", err)
	}
	if len(fr.PNG) != 1 || fr.W != 400 || fr.H != 225 {
		t.Fatalf("1920x1080 animated GIF: %d frames %dx%d, want 1 frame 400x225", len(fr.PNG), fr.W, fr.H)
	}
}

// writeFile puts b in a file of its own and gives its path.
func writeFile(t *testing.T, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// flatJPEG writes a mid-grey greyscale JPEG of w x h px (w x h a multiple of
// 256) and gives its path: every 8x8 block is a DC difference of 0 then an
// end of block, 6 bits with the standard tables, so four blocks are the
// three bytes 28 a2 8a. 36 Mpx take 422 KB, 36 MB once decoded.
func flatJPEG(t *testing.T, w, h int) string {
	t.Helper()
	var small bytes.Buffer
	grey := &image.Gray{Pix: bytes.Repeat([]byte{128}, 32*8), Stride: 32, Rect: image.Rect(0, 0, 32, 8)}
	if err := jpeg.Encode(&small, grey, nil); err != nil {
		t.Fatal(err)
	}
	b := small.Bytes() // ... SOS header, 28 a2 8a, ff d9
	sof := bytes.Index(b, []byte{0xff, 0xc0})
	binary.BigEndian.PutUint16(b[sof+5:], uint16(h))
	binary.BigEndian.PutUint16(b[sof+7:], uint16(w))
	return writeFile(t, "flat.jpg", slices.Concat(b[:len(b)-5], bytes.Repeat([]byte{0x28, 0xa2, 0x8a}, w*h/256), []byte{0xff, 0xd9}))
}

// photoJPEG writes a w x h colour JPEG and gives its path: one 16x16 tile
// with the shades and the grain of a photo, encoded alone, is every MCU,
// each one a restart interval of its own (DRI 1). The encoder of Go would
// take seconds for 48 Mpx under -race; this takes a copy. The bytes a pixel
// are the ones of a phone photo (about 2 bits a pixel).
func photoJPEG(t *testing.T, w, h int) string {
	t.Helper()
	tile := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := 0; i < len(tile.Pix); i += 4 {
		x, y, g := uint8(i/4%16), uint8(i/64), uint8(i*2654435761>>24)&31 // g: grain (Knuth hash)
		tile.Pix[i], tile.Pix[i+1], tile.Pix[i+2], tile.Pix[i+3] = 90+4*x+g, 120+3*y+g, 160-x-y+g, 255
	}
	var small bytes.Buffer
	if err := jpeg.Encode(&small, tile, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	b := small.Bytes()
	sof, sos := bytes.Index(b, []byte{0xff, 0xc0}), bytes.Index(b, []byte{0xff, 0xda})
	binary.BigEndian.PutUint16(b[sof+5:], uint16(h))
	binary.BigEndian.PutUint16(b[sof+7:], uint16(w))
	scan := sos + 2 + int(binary.BigEndian.Uint16(b[sos+2:]))
	mcu := b[scan : len(b)-2]
	n := (w + 15) / 16 * ((h + 15) / 16)
	out := slices.Grow(slices.Concat(b[:sos], []byte{0xff, 0xdd, 0, 4, 0, 1}, b[sos:scan]), n*(len(mcu)+2)+2)
	for i := range n {
		if i > 0 {
			out = append(out, 0xff, 0xd0+byte((i-1)%8))
		}
		out = append(out, mcu...)
	}
	return writeFile(t, "photo.jpg", append(out, 0xff, 0xd9))
}

// shotPNG writes a w x h screenshot of text on white, kind 0 grey or 2 RGB,
// and gives its path: lines lines of text from the top, 40 rows each, whose
// 16 middle rows carry glyph-like noise (black or white) over 60 % of the
// width. Its weight is near the one of a screenshot drawn with fonts: a
// page of a chat of 1170x12000 takes 2.4 MB (0.9 MB with fonts, still in
// proportion), 6 lines of an 8K terminal 838 KB (188 KB with fonts).
func shotPNG(t *testing.T, w, h int, kind byte, lines int) string {
	t.Helper()
	r := rand.New(rand.NewPCG(1, 2))
	noise := make([]byte, 1<<20) // larger than the window of deflate: no match between rows
	for i := range noise {
		noise[i] = byte(r.Uint32()&1) * 255
	}
	white := bytes.Repeat([]byte{255}, w*3)
	off := 0
	return writePNG(t, w, h, 8, kind, func(row []byte, y int) {
		text := 0
		if y/40 < lines && y%40 >= 12 && y%40 < 28 {
			text = len(row) * 6 / 10
		}
		if off+text > len(noise) {
			off = 0
		}
		copy(row, noise[off:off+text])
		copy(row[text:], white)
		off += text
	})
}

// shape tells the frames of fr, for a failure message.
func shape(fr *Frames) string {
	if fr == nil {
		return "no frame"
	}
	return fmt.Sprintf("%d frames of %dx%d", len(fr.PNG), fr.W, fr.H)
}

// allocated gives the MB do allocates.
func allocated(do func()) uint64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	do()
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) >> 20
}

// TestLoadRefusesBombs : a flat picture weighs a few hundred KB at most and
// tens of MB once decoded. With no user action it is refused from its header
// and its file size, before the decoder allocates. The cap of 40 Mpx took the
// 16-bit PNG (90 MB in Load) and the JPEG (36 MB, plus the scaler).
func TestLoadRefusesBombs(t *testing.T) {
	for _, c := range []struct{ what, size, path string }{
		{"48 Mpx grey PNG of 48 KB", "8000x6000", zeroPNG(t, 8000, 6000)},
		{"16-bit RGB PNG", "3000x3000", flatPNG(t, 3000, 3000, 16, 2)},
		{"greyscale JPEG of 422 KB", "6000x6000", flatJPEG(t, 6000, 6000)},
	} {
		var err error
		if mb := allocated(func() { _, err = Load(context.Background(), c.path, "", 600, 220, 1) }); mb > 16 {
			t.Errorf("%s: Load allocated %d MB, want 16 MB at most", c.what, mb)
		}
		if err == nil || !strings.Contains(err.Error(), c.size) {
			t.Errorf("%s: err %v, want %s refused", c.what, err, c.size)
		}
	}
}

// TestLoadGIFBombShowsFirstFrame : 100 flat frames of 820x820 weigh about
// 100 KB and took 70 MB in gif.DecodeAll, every frame at once, with no user
// action. The animation is refused for its proportion; the first frame shows.
func TestLoadGIFBombShowsFirstFrame(t *testing.T) {
	var one bytes.Buffer
	g := &gif.GIF{Image: []*image.Paletted{image.NewPaletted(image.Rect(0, 0, 820, 820), color.Palette{color.Black, color.White})}, Delay: []int{10}}
	if err := gif.EncodeAll(&one, g); err != nil {
		t.Fatal(err)
	}
	b := one.Bytes()
	at := bytes.Index(b, []byte{0x21, 0xf9, 0x04}) // the frame: control extension, descriptor, pixels
	p := writeFile(t, "bomb.gif", slices.Concat(b[:at], bytes.Repeat(b[at:len(b)-1], maxFrames), []byte{0x3b}))
	var fr *Frames
	var err error
	if mb := allocated(func() { fr, err = Load(context.Background(), p, "image/gif", 400, 300, maxFrames) }); mb > 16 {
		t.Errorf("flat GIF of %d frames: Load allocated %d MB, want 16 MB at most", maxFrames, mb)
	}
	if err != nil || len(fr.PNG) != 1 {
		t.Fatalf("flat GIF of %d frames: %s, err %v; want its first frame only", maxFrames, shape(fr), err)
	}
}

// TestLoadAskedTakesOutOfProportion : what Load refuses for its proportion
// (an 8K screenshot of a terminal: 158 MB from a file of 838 KB) shows in
// the preview, which the user asked for. maxCost still holds there.
func TestLoadAskedTakesOutOfProportion(t *testing.T) {
	p := shotPNG(t, 7680, 4320, 2, 6)
	if _, err := Load(context.Background(), p, "", 600, 220, 1); err == nil || !strings.Contains(err.Error(), "7680x4320") {
		t.Fatalf("8K terminal shot with no user action: err %v, want it refused", err)
	}
	fr, err := LoadAsked(context.Background(), p, "", 1920, 1080, 1)
	if err != nil || fr.W != 1920 || fr.H != 1080 {
		t.Fatalf("8K terminal shot in the preview: %s, err %v; want a 1920x1080 frame", shape(fr), err)
	}
	huge := writeFile(t, "huge.png", pngHeader(9000, 9000, 8, 6))
	if _, err := LoadAsked(context.Background(), huge, "", 3840, 2160, 1); err == nil || !strings.Contains(err.Error(), "9000x9000") {
		t.Fatalf("9000x9000 RGBA in the preview: err %v, want it refused (over maxCost)", err)
	}
}

// TestLoadBigPictures : a phone photo of 48 or 50 Mpx (8000x6000,
// 8192x6144), a few MB as a JPEG, and a long screenshot load with no user
// action, fitted to the box asked. The cap of 40 Mpx refused the photos.
func TestLoadBigPictures(t *testing.T) {
	if testing.Short() {
		t.Skip("decodes 112 Mpx: 2 s, 25 s under -race")
	}
	for _, c := range []struct {
		what string
		w, h int
		path string
	}{
		{"48 Mpx photo", 8000, 6000, photoJPEG(t, 8000, 6000)},
		{"50 Mpx photo", 8192, 6144, photoJPEG(t, 8192, 6144)},
		{"long screenshot", 1170, 12000, shotPNG(t, 1170, 12000, 2, 300)},
	} {
		fr, err := Load(context.Background(), c.path, "", 1920, 1080, 1)
		w, h := Fit(c.w, c.h, 1920, 1080)
		if err != nil || fr.W != w || fr.H != h {
			t.Errorf("%s: %s, err %v; want a %dx%d frame", c.what, shape(fr), err, w, h)
		}
	}
}

// TestLoadScaleMemory : CatmullRom keeps dstW x srcH x 32 bytes, so a
// 12 Mpx photo in the 4K preview took 334 MB in Load. The source is halved
// first, and the last step needs no buffer when CatmullRom would need more
// than scaleBudget.
func TestLoadScaleMemory(t *testing.T) {
	p := photoJPEG(t, 4000, 3000)
	var fr *Frames
	var err error
	mb := allocated(func() { fr, err = Load(context.Background(), p, "", 3840, 2160, 1) })
	if err != nil || fr.W != 2880 || fr.H != 2160 {
		t.Fatalf("12 Mpx photo in a 4K box: %s, err %v; want a 2880x2160 frame", shape(fr), err)
	}
	if mb > 128 {
		t.Fatalf("12 Mpx photo in a 4K box: Load allocated %d MB, want 128 MB at most", mb)
	}
}

// TestSafeNameCapped : a chat title is not bounded on the network side, and
// ui.logMsg names its log file from it.
func TestSafeNameCapped(t *testing.T) {
	got := SafeName(strings.Repeat("a", 5000))
	if len(got) != maxSafeName {
		t.Fatalf("length %d", len(got))
	}
	if got := SafeName("Salon des amis"); got != "Salon_des_amis" {
		t.Fatalf("short name altered: %q", got)
	}
}

// TestLoadSniffsFormat : the type a network announces is not always the type
// of the bytes (Discord's Tenor route said image/gif for what was not). Load
// reads the format in the file itself: a PNG announced as a GIF decodes.
func TestLoadSniffsFormat(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.gif")
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(context.Background(), p, "image/gif", 100, 100, 100)
	if err != nil {
		t.Fatalf("png announced as gif: %v", err)
	}
	if len(f.PNG) != 1 {
		t.Fatalf("png announced as gif: %d frames", len(f.PNG))
	}
}

// TestAnimatedWebP : the Go decoder reads still WebP only; an animated one
// (VP8X header with the animation flag) is told apart from its first bytes
// and goes to ffmpeg like a video, never to "webp: invalid format".
func TestAnimatedWebP(t *testing.T) {
	still := []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")
	anim := []byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x12\x00\x00\x00")
	if animatedWebP(still) || !animatedWebP(anim) || animatedWebP(anim[:10]) {
		t.Fatal("animated webp detection")
	}
	p := filepath.Join(t.TempDir(), "a.webp")
	if err := os.WriteFile(p, anim, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(context.Background(), p, "image/webp", 100, 100, 40)
	if err == nil || strings.Contains(err.Error(), "webp:") {
		t.Fatalf("animated webp handed to the still decoder: %v", err)
	}
}

func TestVideoProbeDoesNotFollowNetworkPlaylist(t *testing.T) {
	if !HaveFFmpeg {
		t.Skip("ffmpeg not available")
	}
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "video.m3u8")
	playlist := "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\n" + srv.URL + "/segment.ts\n#EXT-X-ENDLIST\n"
	if err := os.WriteFile(path, []byte(playlist), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ProbeVideo(context.Background(), path); err == nil {
		t.Fatal("network playlist accepted")
	}
	if requests.Load() != 0 {
		t.Fatalf("media probe made %d network requests", requests.Load())
	}
}

// A phone video is often stored in landscape with a tag that turns it to
// portrait. ffmpeg turns the frames before it scales them, so Load and
// ProbeVideo give the size shown, not the size stored. A side data with no
// rotation in it (stereo 3D here) changes nothing.
func TestVideoSizeFollowsRotation(t *testing.T) {
	if !HaveFFmpeg {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	p := func(name string) string { return filepath.Join(dir, name) }
	for _, args := range [][]string{
		{"-f", "lavfi", "-i", "testsrc=duration=0.3:size=64x32:rate=10", "-pix_fmt", "yuv420p", p("plain.mp4")},
		{"-display_rotation", "90", "-i", p("plain.mp4"), "-c", "copy", p("r90.mp4")},
		{"-display_rotation", "180", "-i", p("plain.mp4"), "-c", "copy", p("r180.mp4")},
		{"-display_rotation", "270", "-i", p("plain.mp4"), "-c", "copy", p("r270.mp4")},
		{"-display_rotation", "-90", "-i", p("plain.mp4"), "-c", "copy", p("r-90.mp4")},
		{"-i", p("plain.mp4"), "-c", "copy", "-metadata:s:v:0", "stereo_mode=left_right", p("stereo.mkv")},
		{"-display_rotation", "90", "-i", p("plain.mp4"), "-c", "copy", "-metadata:s:v:0", "stereo_mode=left_right", p("stereo90.mkv")},
	} {
		if out, err := exec.Command("ffmpeg", append([]string{"-v", "error"}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("generation: %v %s", err, out)
		}
	}
	for _, c := range []struct {
		file string
		w, h int
	}{
		{"plain.mp4", 64, 32}, {"r90.mp4", 32, 64}, {"r180.mp4", 64, 32}, {"r270.mp4", 32, 64},
		{"r-90.mp4", 32, 64}, {"stereo.mkv", 64, 32}, {"stereo90.mkv", 32, 64},
	} {
		fr, err := Load(context.Background(), p(c.file), "video/mp4", 400, 400, 1)
		if err != nil {
			t.Errorf("%s: Load: %v", c.file, err)
		} else if fr.W != c.w || fr.H != c.h {
			t.Errorf("%s: Load gives %dx%d, want %dx%d", c.file, fr.W, fr.H, c.w, c.h)
		}
		if w, h, _, err := ProbeVideo(context.Background(), p(c.file)); err != nil || w != c.w || h != c.h {
			t.Errorf("%s: ProbeVideo gives %dx%d (err %v), want %dx%d", c.file, w, h, err, c.w, c.h)
		}
	}
}

// The output of mpv follows the terminal: kitty graphics first (shared memory
// on a local terminal — on Ghostty from 1.4 only, the one that animates:
// black before), then sixel, then text half blocks in 24-bit colour or 256
// colours.
func TestMPVOutputFollowsTerminal(t *testing.T) {
	for _, tc := range []struct {
		s    Screen
		want string
	}{
		{Screen{Kitty: true, Shm: true}, "--vo=kitty --vo-kitty-use-shm=yes --vo-kitty-alt-screen=no"},
		{Screen{Kitty: true, Shm: true, Ghostty: true, Sixel: true}, "--vo=kitty --vo-kitty-use-shm=no --vo-kitty-alt-screen=no"},
		{Screen{Kitty: true, Shm: true, Ghostty: true, Anim: true}, "--vo=kitty --vo-kitty-use-shm=yes --vo-kitty-alt-screen=no"},
		{Screen{Kitty: true}, "--vo=kitty --vo-kitty-use-shm=no --vo-kitty-alt-screen=no"},
		{Screen{Sixel: true, TrueColor: true}, "--vo=sixel,tct --vo-sixel-alt-screen=no"},
		{Screen{TrueColor: true}, "--vo=tct"},
		{Screen{}, "--vo=tct --vo-tct-256=yes"},
	} {
		if got := strings.Join(mpvOut(tc.s), " "); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.s, got, tc.want)
		}
	}
}

// A received file keeps the name its sender gave it, as one safe name: no
// path in it, no control or bidi character, and a cut that keeps the suffix.
func TestOriginalName(t *testing.T) {
	long := strings.Repeat("é", 150) + ".pdf"
	cases := []struct{ in, want string }{
		{"Rapport annuel (v2).docx", "Rapport annuel (v2).docx"},
		{"../../.bashrc", ".._.._.bashrc"},
		{"a\\b\x1bc", "a_b_c"},
		{"gpj‮.exe", "gpj_.exe"},
		{" .. ", ""},
		{"", ""},
		// The name of a temporary download file: the clean-up at start
		// removes those.
		{".part-notes.pdf", "_part-notes.pdf"},
		{" .part-123", "_part-123"},
	}
	for _, c := range cases {
		if got := OriginalName(c.in); got != c.want {
			t.Errorf("OriginalName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := OriginalName(long); len(got) > maxOriginalName || !strings.HasSuffix(got, ".pdf") || !utf8.ValidString(got) {
		t.Errorf("long name: %d bytes %q", len(got), got)
	}
}

// Save puts the whole file under its name, and nothing else stays beside it.
func TestSaveWholeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := Save(path, func(f *os.File) error { _, err := f.WriteString("whole"); return err }); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "whole" {
		t.Fatalf("content %q (%v), want %q", got, err, "whole")
	}
	if got, _ := os.ReadDir(dir); len(got) != 1 {
		t.Fatalf("files in the directory: %v, want notes.txt alone", got)
	}
}

// A received file is private: the directory made for it is 0700, the file
// 0600.
func TestSaveModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "irc_libera", "alice")
	path := filepath.Join(dir, "notes.txt")
	if err := Save(path, func(*os.File) error { return nil }); err != nil {
		t.Fatalf("save: %v", err)
	}
	for _, c := range []struct {
		path string
		want os.FileMode
	}{{dir, 0o700}, {path, 0o600}} {
		st, err := os.Stat(c.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got != c.want {
			t.Fatalf("%s: mode %v, want %v", c.path, got, c.want)
		}
	}
}

// A save that fails leaves no file: none under the name, where a half file
// would pass for the media, and no temporary file. A panic of write still
// reaches the caller (the Guard of a backend).
func TestSaveFailureLeavesNothing(t *testing.T) {
	for name, write := range map[string]func(*os.File) error{
		"write": func(f *os.File) error { f.WriteString("half"); return errors.New("cut") },
		"close": func(f *os.File) error { f.WriteString("half"); return f.Close() }, // the close of Save then fails
		"panic": func(f *os.File) error { f.WriteString("half"); panic("cut") },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			err := func() (err error) {
				defer func() {
					if r := recover(); r != nil {
						err = fmt.Errorf("panic: %v", r)
					}
				}()
				return Save(filepath.Join(dir, "notes.txt"), write)
			}()
			if err == nil {
				t.Fatal("failed save: no error")
			}
			if got, _ := os.ReadDir(dir); len(got) != 0 {
				t.Fatalf("files left after %v: %v", err, got)
			}
		})
	}
}

// A symbolic link at the name is replaced by the file: the file it points to
// keeps its content.
func TestSaveReplacesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "private.txt")
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, func(f *os.File) error { _, err := f.WriteString("received"); return err }); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "original" {
		t.Fatalf("link target written: %q", got)
	}
	if st, err := os.Lstat(path); err != nil || !st.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file now (%v)", path, err)
	}
}

// An animated PNG (Discord APNG stickers) plays through ffmpeg; a still PNG
// stays one picture of the standard decoder.
func TestLoadAPNGPlays(t *testing.T) {
	if !HaveFFmpeg {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	anim := filepath.Join(dir, "a.png")
	gen := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=0.5:size=32x32:rate=10",
		"-plays", "0", "-f", "apng", anim)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("generation: %v %s", err, out)
	}
	fr, err := Load(context.Background(), anim, "image/png", 32, 32, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.PNG) != 5 {
		t.Fatalf("animated PNG: %d frames, want 5", len(fr.PNG))
	}
	still := filepath.Join(dir, "s.png")
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(still, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if fr, err = Load(context.Background(), still, "image/png", 32, 32, 10); err != nil || len(fr.PNG) != 1 {
		t.Fatalf("still PNG: %v frames, err %v; want 1", fr, err)
	}
}

// A WebM with an alpha channel (Telegram video stickers) keeps it: its
// background shows through instead of black, and a half transparent grey stays
// grey (ffmpeg gives straight alpha, not premultiplied).
func TestLoadWebMKeepsAlpha(t *testing.T) {
	if !HaveFFmpeg {
		t.Skip("ffmpeg not available")
	}
	p := filepath.Join(t.TempDir(), "s.webm")
	gen := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=gray:size=32x32:rate=10:duration=0.3,format=rgba,colorchannelmixer=aa=0.5",
		"-vf", "format=yuva420p", "-c:v", "libvpx-vp9", "-auto-alt-ref", "0", p)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("generation (libvpx-vp9 encoder): %v %s", err, out)
	}
	fr, err := Load(context.Background(), p, "video/webm", 32, 32, 2)
	if err != nil || len(fr.PNG) == 0 {
		t.Fatalf("frames %v, err %v", fr, err)
	}
	img, err := png.Decode(bytes.NewReader(fr.PNG[0]))
	if err != nil {
		t.Fatal(err)
	}
	c := color.NRGBAModel.Convert(img.At(16, 16)).(color.NRGBA)
	if c.A < 0x60 || c.A > 0xa0 || c.R < 100 || c.R > 156 {
		t.Fatalf("half transparent grey: %+v, want alpha and grey near 128", c)
	}
}

// A WebM that is not VP9 (VP8 here, a Discord video say) still plays: the
// decoder that keeps the alpha is VP9 only.
func TestLoadWebMVP8Plays(t *testing.T) {
	if !HaveFFmpeg {
		t.Skip("ffmpeg not available")
	}
	p := filepath.Join(t.TempDir(), "v.webm")
	gen := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=0.3:size=32x32:rate=10",
		"-c:v", "libvpx", p)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("generation (libvpx encoder): %v %s", err, out)
	}
	if fr, err := Load(context.Background(), p, "video/webm", 32, 32, 2); err != nil || len(fr.PNG) == 0 {
		t.Fatalf("VP8 WebM: frames %v, err %v", fr, err)
	}
}

// With no ffmpeg, an animated PNG still shows: its first picture, through the
// standard decoder, as before APNG learnt to play.
func TestLoadAPNGWithoutFFmpeg(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	// An acTL chunk right after IHDR (8 + 25 bytes): an APNG to every sniffer.
	actl := []byte{0, 0, 0, 8, 'a', 'c', 'T', 'L', 0, 0, 0, 1, 0, 0, 0, 0}
	actl = binary.BigEndian.AppendUint32(actl, crc32.ChecksumIEEE(actl[4:]))
	raw := slices.Concat(b.Bytes()[:33], actl, b.Bytes()[33:])
	p := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	was := HaveFFmpeg
	HaveFFmpeg = false
	t.Cleanup(func() { HaveFFmpeg = was })
	if fr, err := Load(context.Background(), p, "image/png", 32, 32, 10); err != nil || len(fr.PNG) != 1 {
		t.Fatalf("APNG with no ffmpeg: %v, err %v; want its first picture", fr, err)
	}
}
