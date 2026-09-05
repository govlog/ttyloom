// Package media decodes files into PNG frames, names them, and talks kitty.
package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"image"
	"image/draw"
	"image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

type Frames struct {
	PNG   [][]byte
	W, H  int
	Delay time.Duration
}

const (
	maxFrames = 100 // GIF
	// maxVideoFrames : hard limit on the number of frames whatever
	// video_inline_frames says. A PNG frame weighs 50 to 280 KB inline, up to
	// 2.8 MB full screen: the count alone does not bound the memory, hence
	// videoBudget.
	maxVideoFrames = 1000
	// ponytail: budget per decoding, not global; two videos played at the same
	// time thus hold twice that. A shared counter if it ever hurts.
	videoBudget  = 200 << 20 // bytes of decoded PNG at most
	progressStep = 30        // frames between two partial reports
	// maxPixels : pixel budget of one decoded image (40 Mpx ≈ 160 MB in
	// RGBA). The header announces the size before the decoder allocates for
	// it, so it is read first — a 4 MB PNG can claim 65535x65535. Same guard
	// as fetchTile (osm.go) on the OSM side.
	maxPixels = 40 << 20
	// Limit the canvas and the input frame count before gif.DecodeAll.
	// Paletted frames use one byte per pixel.
	maxGIFPixels = maxPixels * 4 / maxFrames
)

// checkPixels refuses the dimensions announced by a header before the
// decoding allocates for them.
func checkPixels(w, h, maxPx int) error {
	if w <= 0 || h <= 0 || w > maxPx || h > maxPx || w > maxPx/h {
		return fmt.Errorf(i18n.T("image_too_big"), w, h)
	}
	return nil
}

var HaveFFmpeg = func() bool {
	_, e1 := exec.LookPath("ffmpeg")
	_, e2 := exec.LookPath("ffprobe")
	return e1 == nil && e2 == nil
}()

// Load decodes a file into PNG frames fitted in maxW x maxH px (never made
// bigger). frames = 1 for a still image or the first frame of a video,
// maxFrames for a GIF, up to maxVideoFrames for a video being played.
// progress, when given, receives a video while it decodes every progressStep
// frames (inline play: the animation starts before the end).
//
// ctx ends the decoding: a video being played is cancelled between two frames
// and its ffmpeg goes with it. A still image is one call of the standard
// library, nothing to cut in two there.
func Load(ctx context.Context, path, mime string, maxW, maxH, frames int, progress ...func(*Frames)) (*Frames, error) {
	if mt := Sniff(path); mt != "" { // the type a network announces is not always the one of the bytes
		mime = mt
	}
	var p func(*Frames)
	if len(progress) > 0 {
		p = progress[0]
	}
	switch {
	case strings.HasPrefix(mime, "video/"):
		return loadVideo(ctx, path, maxW, maxH, frames, p)
	case mime == "image/webp" && animatedWebP(head(path)):
		// The Go decoder reads still WebP only; ffmpeg plays an animated one.
		return loadVideo(ctx, path, maxW, maxH, frames, p)
	case mime == "image/gif" && frames > 1:
		return loadGIF(ctx, path, maxW, maxH)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	if err := checkPixels(cfg.Width, cfg.Height, maxPixels); err != nil {
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	img = fit(img, maxW, maxH)
	b, err := encode(img)
	if err != nil {
		return nil, err
	}
	return &Frames{PNG: [][]byte{b}, W: img.Bounds().Dx(), H: img.Bounds().Dy()}, nil
}

// head gives the first 512 bytes of path, nil when it cannot be read.
func head(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var b [512]byte
	n, _ := f.Read(b[:])
	return b[:n]
}

// Sniff gives the media type of the first bytes of path, never of its name
// nor of what a network said; "" when nothing is recognised.
func Sniff(path string) string {
	mt := strings.SplitN(http.DetectContentType(head(path)), ";", 2)[0]
	if mt == "application/octet-stream" {
		return ""
	}
	return mt
}

// animatedWebP tells whether the bytes open an animated WebP: the extended
// header (VP8X) with its animation flag, the second bit of its flags byte.
func animatedWebP(b []byte) bool {
	return len(b) > 20 && string(b[:4]) == "RIFF" && string(b[8:16]) == "WEBPVP8X" && b[20]&0x02 != 0
}

// Fit gives the size reduced to fit in maxW x maxH (0 = no limit), aspect kept.
func Fit(w, h, maxW, maxH int) (int, int) {
	if w <= 0 || h <= 0 {
		return w, h
	}
	s := 1.0
	if maxW > 0 {
		if f := float64(maxW) / float64(w); f < s {
			s = f
		}
	}
	if maxH > 0 {
		if f := float64(maxH) / float64(h); f < s {
			s = f
		}
	}
	return max(int(float64(w)*s), 1), max(int(float64(h)*s), 1)
}

func fit(img image.Image, maxW, maxH int) image.Image {
	b := img.Bounds()
	w, h := Fit(b.Dx(), b.Dy(), maxW, maxH)
	if w == b.Dx() && h == b.Dy() {
		return img
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	return dst
}

func encode(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img)
	return buf.Bytes(), err
}

// ponytail: no GIF "disposal" handling, each frame is drawn on top of the one before.
func loadGIF(ctx context.Context, path string, maxW, maxH int) (*Frames, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg, err := gif.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	if err := checkPixels(cfg.Width, cfg.Height, maxGIFPixels); err != nil {
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	r, err := gifPreview(ctx, f)
	if err != nil {
		return nil, err
	}
	g, err := gif.DecodeAll(r)
	if err != nil {
		return nil, err
	}
	if len(g.Image) == 0 {
		return nil, errors.New(i18n.T("gif_empty"))
	}
	w, h := g.Config.Width, g.Config.Height
	if w == 0 || h == 0 {
		w, h = g.Image[0].Bounds().Dx(), g.Image[0].Bounds().Dy()
	}
	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	out := &Frames{Delay: 100 * time.Millisecond}
	if len(g.Delay) > 0 && g.Delay[0] > 0 {
		out.Delay = max(time.Duration(g.Delay[0])*10*time.Millisecond, 20*time.Millisecond)
	}
	for i, fr := range g.Image {
		if i >= maxFrames || ctx.Err() != nil {
			break
		}
		draw.Draw(canvas, fr.Bounds(), fr, fr.Bounds().Min, draw.Over)
		img := fit(canvas, maxW, maxH)
		b, err := encode(img)
		if err != nil {
			return nil, err
		}
		out.PNG = append(out.PNG, b)
		out.W, out.H = img.Bounds().Dx(), img.Bounds().Dy()
	}
	if err := ctx.Err(); err != nil {
		return nil, err // cancelled: same answer as a video, never half a GIF
	}
	return out, nil
}

// gifPreview gives the decoder at most maxFrames complete image blocks.
// A trailer ends the preview before the decoder can allocate later frames.
func gifPreview(ctx context.Context, f *os.File) (io.Reader, error) {
	var header [13]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return nil, err
	}
	skip := func(n int64) error {
		_, err := io.CopyN(io.Discard, f, n)
		return err
	}
	if header[10]&0x80 != 0 {
		if err := skip(3 << (1 + (header[10] & 7))); err != nil {
			return nil, err
		}
	}
	for frames := 0; frames < maxFrames; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var tag [1]byte
		if _, err := io.ReadFull(f, tag[:]); err != nil {
			return nil, err
		}
		switch tag[0] {
		case 0x3b:
			end, err := f.Seek(0, io.SeekCurrent)
			return io.NewSectionReader(f, 0, end), err
		case 0x21:
			if err := skip(1); err != nil { // Extension label.
				return nil, err
			}
		case 0x2c:
			var descriptor [9]byte
			if _, err := io.ReadFull(f, descriptor[:]); err != nil {
				return nil, err
			}
			w, h := int(binary.LittleEndian.Uint16(descriptor[4:6])), int(binary.LittleEndian.Uint16(descriptor[6:8]))
			if err := checkPixels(w, h, maxGIFPixels); err != nil {
				return nil, err
			}
			if descriptor[8]&0x80 != 0 {
				if err := skip(3 << (1 + (descriptor[8] & 7))); err != nil {
					return nil, err
				}
			}
			if err := skip(1); err != nil { // LZW code size.
				return nil, err
			}
			frames++
		default:
			return nil, errors.New("gif: invalid block")
		}
		for {
			if _, err := io.ReadFull(f, tag[:]); err != nil {
				return nil, err
			}
			if tag[0] == 0 {
				break
			}
			if err := skip(int64(tag[0])); err != nil {
				return nil, err
			}
		}
	}
	end, err := f.Seek(0, io.SeekCurrent)
	return io.MultiReader(io.NewSectionReader(f, 0, end), strings.NewReader(";")), err
}

// loadVideo : fixed arguments, never a shell. The raw output is read as it
// comes and encoded to PNG here (no PNG encoder on the ffmpeg side to parse).
func loadVideo(ctx context.Context, path string, maxW, maxH, frames int, progress func(*Frames)) (*Frames, error) {
	if !HaveFFmpeg {
		return nil, errors.New(i18n.T("ffmpeg_absent"))
	}
	frames = min(max(frames, 1), maxVideoFrames)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,r_frame_rate", "-of", "csv=p=0", "-protocol_whitelist", "file,pipe", "-i", path).Output()
	if err != nil {
		return nil, fmt.Errorf(i18n.T("ffprobe_error"), err)
	}
	var w, h, num, den int
	n, _ := fmt.Sscanf(strings.TrimSpace(string(out)), "%d,%d,%d/%d", &w, &h, &num, &den)
	if n < 2 || w <= 0 || h <= 0 {
		return nil, fmt.Errorf(i18n.T("ffprobe_output"), out)
	}
	if err := checkPixels(w, h, maxPixels); err != nil {
		return nil, err
	}
	// Down to 10 fps at most; a slower source keeps its rate, otherwise ffmpeg
	// would repeat frames. Delay = the real interval.
	fps := 10.0
	if n == 4 && num > 0 && den > 0 && float64(num)/float64(den) < fps {
		fps = float64(num) / float64(den)
	}
	w, h = Fit(w, h, maxW, maxH)
	w, h = max(w&^1, 2), max(h&^1, 2) // even size for ffmpeg
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-nostdin", "-protocol_whitelist", "file,pipe", "-i", path,
		"-vf", fmt.Sprintf("fps=%g,scale=%d:%d", fps, w, h),
		"-frames:v", strconv.Itoa(frames),
		"-f", "rawvideo", "-pix_fmt", "rgba", "-")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		cmd.Process.Kill() // early exit (budget): ffmpeg does not stay stuck on the pipe
		cmd.Wait()
	}()
	res := &Frames{W: w, H: h, Delay: max(time.Duration(float64(time.Second)/fps), 20*time.Millisecond)}
	buf := make([]byte, w*h*4)
	used := 0
	for len(res.PNG) < frames && used < videoBudget && ctx.Err() == nil {
		if _, err := io.ReadFull(stdout, buf); err != nil {
			break
		}
		img := &image.RGBA{Pix: append([]byte(nil), buf...), Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
		b, err := encode(img)
		if err != nil {
			return nil, err
		}
		res.PNG = append(res.PNG, b)
		used += len(b)
		// Snapshot: the frames already decoded go to the screen while the rest
		// arrives (the slice grows, the PNGs never change).
		if progress != nil && len(res.PNG)%progressStep == 0 && len(res.PNG) < frames {
			progress(&Frames{PNG: slices.Clone(res.PNG), W: w, H: h, Delay: res.Delay})
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err // cancelled: what was decoded goes with the decoding
	}
	if len(res.PNG) == 0 {
		return nil, errors.New(i18n.T("ffmpeg_no_frame"))
	}
	return res, nil
}

// ProbeVideo reads the size of the first video stream and the length of a
// media file. It is the send side: a failure only means "no attribute to
// attach", the file then goes out as a plain document.
func ProbeVideo(ctx context.Context, path string) (w, h int, dur time.Duration, err error) {
	if !HaveFFmpeg {
		return 0, 0, 0, errors.New(i18n.T("ffmpeg_absent"))
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height:format=duration", "-of", "csv=p=0", "-protocol_whitelist", "file,pipe", "-i", path).Output()
	if err != nil {
		return 0, 0, 0, fmt.Errorf(i18n.T("ffprobe_error"), err)
	}
	// Two lines at most: "w,h" for the video stream — missing for a sound
	// file — then the length in seconds. The order is fixed by ffprobe but
	// each line is recognised on its own shape, not on its rank.
	for _, ln := range strings.Split(string(out), "\n") {
		ln = strings.TrimSpace(ln)
		var a, b int
		if n, _ := fmt.Sscanf(ln, "%d,%d", &a, &b); n == 2 {
			w, h = a, b
		} else if s, e := strconv.ParseFloat(ln, 64); e == nil && s > 0 {
			dur = time.Duration(s * float64(time.Second))
		}
	}
	if dur <= 0 {
		return 0, 0, 0, fmt.Errorf(i18n.T("ffprobe_output"), out)
	}
	return w, h, dur, nil
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// maxSafeName : a name that comes back as a file name must stay one. A chat
// title has no bound on the network side: without the cut a name built from
// one gives an ENAMETOOLONG instead of a file. The output holds only
// [A-Za-z0-9_-], so cutting on a byte is safe.
const maxSafeName = 64

// SafeName replaces the unstable characters of a name (a chat title…) with
// _ : it can be used as a file name as it is.
func SafeName(s string) string {
	n := unsafeChars.ReplaceAllString(s, "_")
	if len(n) > maxSafeName {
		n = n[:maxSafeName]
	}
	return n
}

// FileName includes the network and chat ID so equal titles cannot collide.
func FileName(key model.ChatKey, chat string, msgID int, date time.Time, ext string) string {
	c := SafeName(chat)
	if len(c) > 30 {
		c = c[:30]
	}
	return fmt.Sprintf("%s_%s_%d_%s_%d%s", date.Format("20060102-150405"), SafeName(key.Net), key.ID, c, msgID, ext)
}

// Extension gives a known media suffix, or .bin for an unknown MIME type.
func Extension(mime string) string {
	mime, _, _ = strings.Cut(mime, ";")
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/flac":
		return ".flac"
	case "application/pdf":
		return ".pdf"
	case "text/plain":
		return ".txt"
	}
	return ".bin"
}
