package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	xdraw "golang.org/x/image/draw"

	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
)

// Pictures drawn in the cells: sixel, or the markers of Terminology. They
// take the layout of kitty (blank cells reserved in the text, u.placed) and
// go out after the rows of text (paint), again whenever a row under them was
// written. Both are made from the decoded frame, cut and scaled here (zoom,
// picture cut by the view). Not animated: a GIF holds its frame (animate
// knows kitty and half blocks only), and a video plays in mpv.

// pixelMode : pictures placed over blank cells (kitty, sixel, Terminology),
// not drawn as text.
func pixelMode(mode string) bool { return mode == "kitty" || mode == "sixel" || mode == "terminology" }

// cellPics gives the pictures of u.placed for the mode; nil in kitty (its
// placements) and in half blocks (text). x0: first column of the message
// area, which placed.img.Col counts from. A picture still being made
// (picOf) is left out, unless the same media had one at the same place in
// the last frame: that one stays on the screen until the new one comes (a
// zoom step of the viewer), where a blank would flash.
func (u *UI) cellPics(x0 int) []cellPic {
	if u.images != "sixel" && u.images != "terminology" {
		return nil
	}
	var out []cellPic
	for _, p := range u.placed {
		md, row, col := p.img.Media, p.row, x0+p.img.Col
		if len(md.Frames) == 0 {
			continue
		}
		s, busy := u.picOf(md, p.crop, p.img.Cols, p.img.Rows)
		var seq string
		switch {
		case s == "":
		case u.images == "sixel":
			seq = fmt.Sprintf("\x1b[%d;%dH", row+1, col+1) + s
		default:
			seq = terminologyPic(s, row, col, p.img.Cols, p.img.Rows)
		}
		switch {
		case seq != "":
			out = append(out, cellPic{row: row, col: col, rows: p.img.Rows, md: md, seq: seq})
		case busy:
			same := func(q cellPic) bool { return q.md == md && q.row == row && q.col == col && q.rows == p.img.Rows }
			if i := slices.IndexFunc(u.prevPics, same); i >= 0 {
				out = append(out, u.prevPics[i])
			}
		}
	}
	return out
}

// picKey : one rendering of a frame for the mode. The frame is named by its
// first byte: a new decoding gives new bytes.
type picKey struct {
	mode         string
	frame        *byte
	crop         image.Rectangle
	cols, rows   int
	cellW, cellH int
}

// picSlots : pictures made at the same time. The sixel encoder holds a core
// for 15 to 40 ms a picture; a screen of them goes through this many lanes
// while the UI loop stays free for the keys.
const picSlots = 4

// evPic : a picture made outside the UI loop (makePic), back for the cache.
type evPic struct {
	key  picKey
	seq  string // sixel, or the file of Terminology; "" when it cannot be made
	size int    // bytes of that file
}

// picOf : what draws the shown frame of md, cut to crop and fitted to cols x
// rows cells — the sixel sequence, or for Terminology the path of a PNG of
// it. "" when it cannot be made, or while it is being made (busy): the
// decoding, the scaling and the encoding run outside the UI loop, and the
// event that brings the picture back draws the screen again.
// ponytail: up to 64 kept and 96 MB of files, all dropped past that; an LRU
// if a scroll through many pictures ever makes them again and again.
func (u *UI) picOf(md *model.Media, crop image.Rectangle, cols, rows int) (s string, busy bool) {
	f := md.Frames[md.Frame%len(md.Frames)]
	if len(f) == 0 {
		return "", false
	}
	cw, ch, _, _ := u.cells()
	k := picKey{u.images, &f[0], crop, cols, rows, cw, ch}
	if s, ok := u.pics[k]; ok {
		return s, false
	}
	if !u.picBusy[k] {
		u.picStart(k, f)
	}
	return "", true
}

// picStart makes the picture of k from the frame f in a goroutine. The
// file of Terminology is named here, on the UI loop, which owns the counter
// and the directory.
func (u *UI) picStart(k picKey, f []byte) {
	path := ""
	if k.mode == "terminology" {
		if u.picDir == "" {
			d, err := os.MkdirTemp(os.Getenv("XDG_RUNTIME_DIR"), "ttyloom-pics-")
			if err != nil {
				u.picKeep(evPic{key: k}) // never asked again: "" is kept
				return
			}
			u.picDir = d
		}
		u.picN++
		path = filepath.Join(u.picDir, strconv.Itoa(u.picN)+".png")
	}
	if u.picBusy == nil {
		u.picBusy = map[picKey]bool{}
		u.picSem = make(chan struct{}, picSlots)
	}
	u.picBusy[k] = true
	sem, ctx := u.picSem, u.ctx
	u.picRuns.Add(1)
	go func() {
		defer u.picRuns.Done()
		ev := evPic{key: k}
		defer func() {
			if r := recover(); r != nil { // a frame the decoders choke on: no picture, the process lives
				ev = evPic{key: k}
			}
			u.post(ev) // busy ends in every case
		}()
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-ctx.Done():
			return
		}
		ev.seq, ev.size = makePic(k, f, path)
	}()
}

// picKeep files a picture made (picStart). A failure is kept too, as "": a
// frame that cannot be made is not asked again at each frame.
func (u *UI) picKeep(e evPic) {
	delete(u.picBusy, e.key)
	if len(u.pics) >= 64 || u.picBytes > 96<<20 {
		u.dropPics(false)
	}
	if u.pics == nil {
		u.pics = map[picKey]string{}
	}
	u.pics[e.key] = e.seq
	u.picBytes += e.size
}

// makePic : the picture of the frame f for k, cut and fitted — the sixel
// sequence, or the path of the PNG written for Terminology and its size.
// Terminology reads a file and can neither crop nor make it bigger: the zoom
// of the preview and a picture cut by the view need a bitmap of their own,
// written with no compression: a full screen one takes 11 ms to write
// instead of 140, and Terminology, which shows it only once loaded (a blank
// cell meanwhile: the flash of a zoom step), reads it in 4 ms instead of 27.
// It reads its arguments only: it runs outside the UI loop.
func makePic(k picKey, f []byte, path string) (string, int) {
	img, err := png.Decode(bytes.NewReader(f))
	if err != nil {
		return "", 0
	}
	if sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok && !k.crop.Empty() {
		img = sub.SubImage(k.crop)
	}
	fit := fitBox(img, k.cols*k.cellW, k.rows*k.cellH, k.cellW, k.cellH)
	if k.mode == "sixel" {
		return media.Sixel(fit), 0
	}
	// The frame is already a PNG: it goes as it is, unless cut or scaled.
	if !k.crop.Empty() || fit != img {
		var b bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&b, fit); err != nil {
			return "", 0
		}
		f = b.Bytes()
	}
	if os.WriteFile(path, f, 0o600) != nil {
		return "", 0
	}
	return path, len(f)
}

// dropPics forgets the pictures made, with their files; all: the directory
// too (end of the session).
func (u *UI) dropPics(all bool) {
	for k, s := range u.pics {
		if k.mode == "terminology" && s != "" {
			os.Remove(s)
		}
	}
	u.pics, u.picBytes = nil, 0
	if all && u.picDir != "" {
		os.RemoveAll(u.picDir)
		u.picDir = ""
	}
}

// fitBox scales img to fit w x h px, aspect kept. A sixel or the bitmap of
// Terminology is drawn at its own size, where kitty stretches the image to
// its cells: the zoom of the preview needs it, a picture of the messages
// (already decoded for its box, less than a cell short) is left as it is.
func fitBox(img image.Image, w, h, cellW, cellH int) image.Image {
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 || (b.Dx() <= w && b.Dy() <= h && (w-b.Dx() < cellW || h-b.Dy() < cellH)) {
		return img
	}
	s := min(float64(w)/float64(b.Dx()), float64(h)/float64(b.Dy()))
	dst := image.NewRGBA(image.Rect(0, 0, max(int(float64(b.Dx())*s), 1), max(int(float64(b.Dy())*s), 1)))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	return dst
}

// terminologyPic : Terminology draws the file at path itself, centred in
// cols x rows cells (ESC } i c, what tycat sends: made smaller, never bigger),
// on the cells marked between ESC } i b and ESC } i e. It reads the file, so
// only on this machine.
func terminologyPic(path string, row, col, cols, rows int) string {
	if path == "" || strings.ContainsFunc(path, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\x1b}ic#%d;%d;%s\x00", cols, rows, path)
	for k := range rows {
		fmt.Fprintf(&b, "\x1b[%d;%dH\x1b}ib\x00%s\x1b}ie\x00", row+k+1, col+1, strings.Repeat("#", cols))
	}
	return b.String()
}
