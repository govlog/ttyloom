package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// Full screen preview (key v, /view): exclusive state, the screen carries
// only the image and a status line.

type viewer struct {
	src    *model.Media    // media of the message: never changed
	md     *model.Media    // full screen copy (frames and kitty id apart)
	w, h   int             // box of the last decoding started, in pixels
	gen    int             // generation: an older result is dropped
	zoom   float64         // 1 = fitted to the screen
	cx, cy float64         // centre of the view, as a 0..1 fraction of the image
	crop   image.Rectangle // sub-rectangle of the frame shown, in pixels (empty = all)
	// cols, rows : placement of the last drawing, in cells — what a cell of
	// mouse drag is worth in image.
	cols, rows int
	// dragX, dragY : cell of the last drag event; moved: the press has moved,
	// so its release is not a click.
	dragX, dragY int
	moved        bool
}

const (
	zoomMin, zoomMax = 0.25, 8
	zoomFactor       = 1.25
)

// zoomStep gives the next step (in) or the one before, bounded to [zoomMin, zoomMax].
func zoomStep(z float64, in bool) float64 {
	if z <= 0 {
		z = 1
	}
	if in {
		z *= zoomFactor
	} else {
		z /= zoomFactor
	}
	return min(max(z, zoomMin), zoomMax)
}

// viewRect gives the sub-rectangle of the imgW x imgH px image visible in a
// box of boxW x boxH px, centred on (cx, cy) — a 0..1 fraction of the image —
// and moved so as never to go past the edge. Image smaller than the box: taken whole.
func viewRect(imgW, imgH, boxW, boxH int, cx, cy float64) (x, y, w, h int) {
	w, h = max(min(imgW, boxW), 1), max(min(imgH, boxH), 1)
	x = max(0, min(int(cx*float64(imgW))-w/2, imgW-w))
	y = max(0, min(int(cy*float64(imgH))-h/2, imgH-h))
	return x, y, w, h
}

// pan moves the centre of the view by dx, dy times 10 % of the part shown.
// The centre stays in the range that keeps the view inside the image: without
// that it would drift out of frame and the next zoom would jump.
func (v *viewer) pan(dx, dy float64) {
	md := v.md
	if v.crop.Empty() || md.FrameW <= 0 || md.FrameH <= 0 {
		return // nothing cropped: nothing to move
	}
	fx := float64(v.crop.Dx()) / float64(md.FrameW)
	fy := float64(v.crop.Dy()) / float64(md.FrameH)
	v.cx = min(max(v.cx+dx*0.1*fx, fx/2), 1-fx/2)
	v.cy = min(max(v.cy+dy*0.1*fy, fy/2), 1-fy/2)
}

// dragTo pans with the mouse: the image follows the pointer, one cell of
// drag being one cell of the placement. Nothing cropped: nothing to move.
func (v *viewer) dragTo(x, y int) {
	dx, dy := x-v.dragX, y-v.dragY
	v.dragX, v.dragY = x, y
	if dx != 0 || dy != 0 {
		v.moved = true
	}
	md := v.md
	if v.crop.Empty() || md.FrameW <= 0 || md.FrameH <= 0 || v.cols <= 0 || v.rows <= 0 {
		return
	}
	fx := float64(v.crop.Dx()) / float64(md.FrameW)
	fy := float64(v.crop.Dy()) / float64(md.FrameH)
	v.cx = min(max(v.cx-float64(dx)*fx/float64(v.cols), fx/2), 1-fx/2)
	v.cy = min(max(v.cy-float64(dy)*fy/float64(v.rows), fy/2), 1-fy/2)
}

// need tells whether the box asked for differs from the last decoding started
// — a new one goes out, under a new generation. Pixels and not cells: a zoom
// changes the useful resolution without changing cols/rows. A border drag goes
// through every size, but only once per size.
func (v *viewer) need(w, h int) bool {
	if w == v.w && h == v.h {
		return false
	}
	v.w, v.h, v.gen = w, h, v.gen+1
	return true
}

// viewCopy gives a copy of the media for the preview. Frames and kitty id
// start from zero: the window keeps its own, decoded for a smaller box.
func viewCopy(src *model.Media) *model.Media {
	return &model.Media{Kind: src.Kind, Label: src.Label, Name: src.Name, Mime: src.Mime,
		W: src.W, H: src.H, Path: src.Path, URL: src.URL}
}

// viewMsg : key v or /view on a message.
func (u *UI) viewMsg(m *model.Msg) {
	switch md := m.Media; {
	case md == nil || !md.Previewable():
		u.sys(i18n.T("no_preview_for_media"))
	case u.images == "off":
		u.sys(i18n.T("images_off"))
	case md.Kind == model.MediaMap && !u.cfg.Maps:
		u.sys(i18n.T("maps_off"))
	default:
		u.openViewer(m)
	}
}

// viewNth : preview of the nth previewable media of w, from the end (/view).
func (u *UI) viewNth(w *Window, n int) {
	for i := len(w.Items) - 1; i >= 0; i-- {
		it := w.Items[i]
		if it.Msg == nil || it.Msg.Media == nil || !it.Msg.Media.Previewable() || !w.shown(it) {
			continue // shown: the nth from the end is counted on the screen, filter included
		}
		n--
		if n > 0 {
			continue
		}
		u.viewMsg(it.Msg)
		return
	}
	w.AddSys(i18n.T("no_previewable_media"))
}

// openViewer opens the preview: full screen decoding in a goroutine, or a
// download first (downloaded() carries on).
func (u *UI) openViewer(m *model.Msg) {
	md := m.Media
	u.closeViewer()
	u.picker, u.pager, u.pasteAsk = nil, nil, "" // the preview is exclusive
	u.viewer = &viewer{src: md, md: viewCopy(md), zoom: 1, cx: 0.5, cy: 0.5}
	if md.Path == "" {
		if md.State != model.MediaLoading {
			u.download(m)
		}
		return
	}
	u.viewLoad()
}

// viewLoad : full screen decoding, box (cols-2) x (rows-3) cells. The frames
// in place stay shown (the terminal stretches them) until the new ones come.
func (u *UI) viewLoad() {
	v := u.viewer
	v.md.Path = v.src.Path
	if v.md.Path == "" || u.images == "off" {
		return
	}
	cellW, cellH, _, _ := u.cells()
	boxW, boxH := max(u.t.Cols-2, 1)*cellW, max(u.t.Rows-3, 1)*cellH
	// Zooming out does not decode again: the same frame is placed on fewer
	// cells. Zooming in asks for a box bigger than the screen; Fit does not
	// make things bigger, so the frame caps at the size of the source and above
	// that kitty (or the half blocks) stretch it.
	z := max(v.zoom, 1)
	w, h := int(float64(boxW)*z), int(float64(boxH)*z)
	// Frame already smaller than the box of the last decoding: the source is
	// drawn whole, and a bigger box would not give one pixel more.
	// Direction-neutral: once the source is drawn whole, any box that still
	// covers it gives the same frame (a wheel burst must not start N decodes).
	if md := v.md; len(md.Frames) > 0 && md.FrameW < v.w && md.FrameH < v.h && w >= md.FrameW && h >= md.FrameH {
		return
	}
	if !v.need(w, h) {
		return // same box: the decoding running or done will do
	}
	u.loadFrames(v.md, v.w, v.h, u.frameCount(v.md), v.gen)
}

// closeViewer frees the image of the preview and gives the screen back to the windows.
func (u *UI) closeViewer() {
	v := u.viewer
	if v == nil {
		return
	}
	u.viewer = nil
	if u.drag == dragView {
		u.drag = dragNone
	}
	u.cancelDecode(v.md)   // full screen decoding running: nobody will look at it
	s := u.kittyFree(v.md) // ids and LRU cleaned even when the mode has changed
	if u.t.Kitty {
		u.t.WriteString(s)
	}
	v.md.Frames = nil // full screen frames given up
	u.placed = u.placed[:0]
	u.clear() // full repaint: the whole screen carried the preview
}

// viewerKey : the preview takes everything. o opens the file with the desktop,
// l and s play the video full screen, +/-/0, the arrows and the wheel zoom and
// move the view, the left button held pans, everything else (Esc, q, a plain
// click, Ctrl+X, F2…) closes it — predictable rather than silent.
func (u *UI) viewerKey(k term.Key) {
	v := u.viewer
	if k.Code == term.Mouse && k.Mouse.Button == 0 { // left button: a drag pans, a click closes
		m := k.Mouse
		switch {
		case m.Press && !m.Motion:
			u.drag, v.dragX, v.dragY, v.moved = dragView, m.X, m.Y, false
		case m.Motion && u.drag == dragView:
			v.dragTo(m.X, m.Y)
		case !m.Press && u.drag == dragView:
			u.drag = dragNone
			if !v.moved {
				u.closeViewer()
			}
		}
		return
	}
	if v.md.Path != "" {
		ch := k.Code == term.None // plain character
		switch {
		case ch && k.Rune == 'o':
			if render.SafeURL(v.md.URL) { // link preview: the page, not the thumbnail
				u.open(v.md.URL)
			} else {
				u.open(v.md.Path)
			}
			return
		// v.w x v.h: box of the last full screen decoding; the generation is
		// unchanged, so the result will be taken.
		case ch && k.Rune == 'l' && v.md.Kind == model.MediaVideo && v.w > 0:
			u.playPause(v.md, v.w, v.h, v.gen)
			return
		case ch && k.Rune == 's' && render.Stoppable(v.md):
			u.stopVideo(v.md)
			return
		case ch && (k.Rune == '+' || k.Rune == '='),
			k.Code == term.Mouse && k.Mouse.Button == 64: // wheel up
			u.viewZoom(zoomStep(v.zoom, true))
			return
		case ch && k.Rune == '-',
			k.Code == term.Mouse && k.Mouse.Button == 65: // wheel down
			u.viewZoom(zoomStep(v.zoom, false))
			return
		case ch && k.Rune == '0': // back to the fit, view centred again
			v.cx, v.cy = 0.5, 0.5
			u.viewZoom(1)
			return
		case k.Code == term.Left:
			v.pan(-1, 0)
			return
		case k.Code == term.Right:
			v.pan(1, 0)
			return
		case k.Code == term.Up:
			v.pan(0, -1)
			return
		case k.Code == term.Down:
			v.pan(0, 1)
			return
		}
	}
	u.closeViewer()
}

// viewZoom applies a zoom factor: above 1 the image must be decoded again at a
// finer resolution, otherwise the placement is enough.
func (u *UI) viewZoom(z float64) {
	v := u.viewer
	if z == v.zoom {
		return
	}
	v.zoom = z
	u.viewLoad()
}

// viewerBox gives the column and the line (origin 0) of an image of imgCols x
// imgRows cells centred in the box (cols-2) x (rows-3) — one margin column on
// each side, the status line at the bottom.
func viewerBox(cols, rows, imgCols, imgRows int) (x, y int) {
	return 1 + max(0, (cols-2-imgCols)/2), max(0, (rows-3-imgRows)/2)
}

// geometry gives the size of the placement in cells and the visible
// sub-rectangle of the frame (v.crop, empty when there is nothing to crop).
//
// The "zoom 1" size is the fit of the current frame to the screen: as Fit does
// not make things bigger, it is exactly what the decoding gave — no need for
// the size of the source, which is often missing. x zoom gives the display
// size; what goes past the screen is cropped at the source, in the same ratio.
func (v *viewer) geometry(cols, rows, cellW, cellH int) (imgCols, imgRows int) {
	md := v.md
	boxCols, boxRows := max(cols-2, 1), max(rows-3, 1)
	boxW, boxH := boxCols*cellW, boxRows*cellH
	v.crop = image.Rectangle{}
	if md.FrameW <= 0 || md.FrameH <= 0 || cellW <= 0 || cellH <= 0 {
		return 0, 0
	}
	z := v.zoom
	if z <= 0 {
		z = 1
	}
	fitW, fitH := media.Fit(md.FrameW, md.FrameH, boxW, boxH)
	dispW := max(int(float64(fitW)*z), 1)
	dispH := max(int(float64(fitH)*z), 1)
	// Box of the screen brought back to pixels of the frame: frame/display ratio.
	x, y, w, h := viewRect(md.FrameW, md.FrameH,
		max(boxW*md.FrameW/dispW, 1), max(boxH*md.FrameH/dispH, 1), v.cx, v.cy)
	if w < md.FrameW || h < md.FrameH {
		v.crop = image.Rect(x, y, x+w, y+h)
	}
	imgCols = min(max((min(dispW, boxW)+cellW-1)/cellW, 1), boxCols)
	imgRows = min(max((min(dispH, boxH)+cellH-1)/cellH, 1), boxRows)
	v.cols, v.rows = imgCols, imgRows
	return imgCols, imgRows
}

// status : "name · 800x600 px · Esc close · o open".
func (v *viewer) status() string {
	md := v.md
	name := md.Name
	if name == "" {
		name = md.Label
	}
	if md.Kind == model.MediaMap {
		name = media.MapAttribution + " · " + name
	}
	s := render.CleanLine(name) // remote name: neither a raw sequence nor a line break
	switch {
	case md.State == model.MediaFailed:
		s += i18n.T("viewer_error", render.CleanLine(md.Err))
	case md.Path == "":
		s += i18n.T("viewer_downloading")
	case len(md.Frames) == 0:
		s += i18n.T("viewer_decoding")
	default:
		w, h := md.W, md.H
		if w <= 0 || h <= 0 {
			w, h = md.FrameW, md.FrameH
		}
		s += i18n.T("viewer_size", w, h)
		if md.Want > len(md.Frames) {
			s += i18n.T("viewer_decode_pct", 100*len(md.Frames)/md.Want)
		}
	}
	switch {
	case md.Kind != model.MediaVideo || md.Path == "":
	case !render.Stoppable(md):
		s += " · " + i18n.T("act_play")
	case md.Paused:
		s += " · " + i18n.T("act_resume") + " · " + i18n.T("act_stop")
	default:
		s += " · " + i18n.T("act_pause") + " · " + i18n.T("act_stop")
	}
	if pct := int(v.zoom*100 + 0.5); pct != 100 {
		s += i18n.T("viewer_zoom", pct)
	}
	return s + i18n.T("viewer_keys")
}

// drawViewer : the whole screen for the image, status on the last line.
func (u *UI) drawViewer() {
	v := u.viewer
	cols, rows := u.t.Cols, u.t.Rows
	var b strings.Builder
	bg := theme.Style{FG: u.th.FG, BG: u.th.BG}.SGR()
	b.WriteString("\x1b[?25l")
	for r := 1; r <= rows; r++ {
		fmt.Fprintf(&b, "\x1b[%d;1H%s\x1b[K", r, bg)
	}
	u.placed, u.hits = u.placed[:0], nil
	var cur map[kplace]bool // the placements of the windows go away through the diff
	md := v.md
	if len(md.Frames) > 0 { // frames in place, even during a new decoding
		cellW, cellH, _, _ := u.cells()
		imgCols, imgRows := v.geometry(cols, rows, cellW, cellH)
		x, y := viewerBox(cols, rows, imgCols, imgRows)
		frame := md.Frames[md.Frame%len(md.Frames)]
		switch {
		case imgCols < 1 || imgRows < 1: // tiny screen: nothing to place
		case u.images == "kitty":
			u.kittyLRU.cap = max(u.cfg.KittyImages, 1) // the preview counts in the LRU
			cur = map[kplace]bool{}
			fmt.Fprintf(&b, "\x1b[%d;%dH", y+1, x+1)
			// Same rule as draw() (line + 1): animate() animates the preview from
			// u.placed and must land on the same pid.
			u.placeKitty(&b, cur, uint32(y+1), md, frame, imgCols, imgRows, v.crop)
			// animate() animates the GIF from u.placed, at x0+Col+1: hence the shift.
			x0, _ := u.layout()
			u.placed = append(u.placed, placed{row: y, pid: uint32(y + 1), crop: v.crop,
				img: &render.Img{Media: md, Col: x - x0, Cols: imgCols, Rows: imgRows}})
		case u.images == "halfblock":
			// ponytail: the PNG is decoded again at each frame, so at each move; keep
			// the decoded image in the viewer if it ever drags.
			if img, err := png.Decode(bytes.NewReader(frame)); err == nil {
				if sub, ok := img.(interface {
					SubImage(image.Rectangle) image.Image
				}); ok && !v.crop.Empty() {
					img = sub.SubImage(v.crop) // crop on the client side
				}
				for i, l := range render.Halfblocks(img, imgCols, imgRows) {
					fmt.Fprintf(&b, "\x1b[%d;%dH", y+i+1, x+1)
					u.writeLine(&b, l, cols-x, nil)
				}
			}
		}
	}
	st := u.th.Style(theme.StatusBG)
	s := render.Truncate(v.status(), cols, "…")
	col := max(0, (cols-render.Width(s))/2)
	fmt.Fprintf(&b, "\x1b[%d;1H%s\x1b[K", rows, st.SGR())
	fmt.Fprintf(&b, "\x1b[%d;%dH", rows, col+1)
	u.writeLine(&b, render.Line{Spans: []render.Span{{Text: s, Style: st}}}, cols-col, nil)
	if u.t.Kitty {
		u.endFrameKitty(&b, cur)
	}
	u.t.WriteString(b.String())
	u.t.Flush()
}
