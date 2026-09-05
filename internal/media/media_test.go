package media

import (
	"bytes"
	"context"
	"encoding/binary"
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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/model"
)

func TestFileName(t *testing.T) {
	d := time.Date(2026, 8, 29, 12, 3, 1, 0, time.UTC)
	key := model.ChatKey{Net: model.NetTelegram, ID: 7}
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
	data := bytes.Repeat([]byte{1}, 5000) // base64 → 6668 bytes → 2 chunks
	s := KittyDisplay(7, 3, data, 10, 5)
	if strings.Count(s, "\x1b_G") != 2 || !strings.HasPrefix(s, "\x1b_Ga=T,f=100,i=7,p=3,c=10,r=5,q=2,m=1;") || !strings.Contains(s, "\x1b_Gm=0;") {
		t.Fatalf("%.60q", s)
	}
	if got := KittyPlace(7, 3, 10, 5); got != "\x1b_Ga=p,i=7,p=3,c=10,r=5,q=2\x1b\\" {
		t.Fatal(got)
	}
	// Crop: source sub-rectangle in pixels of the image sent.
	crop := KittyPlace(7, 3, 10, 5, image.Rect(20, 10, 120, 60))
	if crop != "\x1b_Ga=p,i=7,p=3,c=10,r=5,x=20,y=10,w=100,h=50,q=2\x1b\\" {
		t.Fatal(crop)
	}
	if got := KittyPlace(7, 3, 10, 5, image.Rectangle{}); got != "\x1b_Ga=p,i=7,p=3,c=10,r=5,q=2\x1b\\" {
		t.Fatal(got) // empty rectangle: no crop key
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
		Delay:  []int{5, 5},
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
	if len(fr.PNG) != 2 || fr.W != 4 || fr.H != 4 || fr.Delay != 50*time.Millisecond {
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

func TestGIFStopsReadingAtFrameLimit(t *testing.T) {
	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	g := &gif.GIF{Config: image.Config{Width: 2, Height: 2}}
	for range maxFrames {
		g.Image = append(g.Image, frame)
		g.Delay = append(g.Delay, 5)
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
// DecodeConfig, which is what the guard reads.
func pngHeader(w, h uint32) []byte {
	var ihdr bytes.Buffer
	ihdr.WriteString("IHDR")
	binary.Write(&ihdr, binary.BigEndian, w)
	binary.Write(&ihdr, binary.BigEndian, h)
	ihdr.Write([]byte{8, 0, 0, 0, 0}) // 8 bits, greyscale, no interlace
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	binary.Write(&out, binary.BigEndian, uint32(ihdr.Len()-4))
	out.Write(ihdr.Bytes())
	binary.Write(&out, binary.BigEndian, crc32.ChecksumIEEE(ihdr.Bytes()))
	return out.Bytes()
}

// TestLoadRefusesPixelBomb : a 4 KB header claiming 65535x65535 (4 GB once
// decoded) is refused on the announced size, before any allocation.
func TestLoadRefusesPixelBomb(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bomb.png")
	if err := os.WriteFile(p, pngHeader(65535, 65535), 0o600); err != nil {
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
