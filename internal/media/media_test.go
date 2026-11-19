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
	"image/png"
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

// zeroPNG writes a black greyscale PNG of w x h px and gives its path. The
// chunks are made here: png.Encode writes row by row and takes seconds under
// -race for millions of rows.
func zeroPNG(t *testing.T, w, h int) string {
	t.Helper()
	var idat bytes.Buffer
	z, _ := zlib.NewWriterLevel(&idat, zlib.BestSpeed)
	z.Write(make([]byte, h*(1+w))) // each row: filter byte 0, then black pixels
	z.Close()
	b := pngHeader(uint32(w), uint32(h), 8, 0)
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
