package ui

import (
	"image"
	"math"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
)

func TestViewerBox(t *testing.T) {
	cases := []struct {
		cols, rows, imgCols, imgRows int
		x, y                         int
	}{
		{80, 24, 20, 10, 30, 5}, // smaller than the box: centred
		{80, 24, 78, 21, 1, 0},  // exactly the box: on the margins
		{3, 4, 1, 1, 1, 0},      // smallest box 1x1
		{10, 6, 20, 20, 1, 0},   // larger than the box: never a negative position
		{81, 25, 20, 11, 30, 5}, // odd width and height
	}
	for _, c := range cases {
		x, y := viewerBox(c.cols, c.rows, c.imgCols, c.imgRows)
		if x != c.x || y != c.y {
			t.Fatalf("viewerBox(%d,%d,%d,%d) = %d,%d — want %d,%d",
				c.cols, c.rows, c.imgCols, c.imgRows, x, y, c.x, c.y)
		}
	}
}

// v on a message with no media opens no preview and leaves the key to the input.
func TestViewKeyNoMedia(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, images: "kitty"}
	w := u.ws.Current()
	w.Upsert(&model.Msg{ID: 1, Date: time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC), From: "alice", Text: "coucou"})
	u.setSel(w, w.Items[0])
	if u.selKey(w, 'v') {
		t.Fatal("v key consumed with no media")
	}
	if u.viewer != nil {
		t.Fatal("preview opened with no media")
	}
}

// A box unchanged does not start a decoding again; a new box moves the
// generation on, which makes the result in flight stale.
func TestViewLoadSameBox(t *testing.T) {
	v := &viewer{}
	if !v.need(780, 420) || v.gen != 1 {
		t.Fatalf("1st decode: gen=%d", v.gen)
	}
	if v.need(780, 420) || v.gen != 1 {
		t.Fatalf("same box: new decode (gen=%d)", v.gen)
	}
	if !v.need(600, 420) || v.gen != 2 { // width only
		t.Fatalf("narrower box: gen=%d", v.gen)
	}
	if !v.need(600, 840) || v.gen != 3 { // height only (or zoom: pixels, not cells)
		t.Fatalf("taller box: gen=%d", v.gen)
	}
}

// Zoom steps: x1.25 both ways, bounded to [0.25, 8].
func TestViewerZoomSteps(t *testing.T) {
	cases := []struct {
		z    float64
		in   bool
		want float64
	}{
		{1, true, 1.25},
		{1.25, true, 1.5625},
		{1.25, false, 1},
		{1, false, 0.8},
		{7, true, 8},       // upper bound
		{8, true, 8},       // already at the bound
		{0.3, false, 0.25}, // lower bound
		{0.25, false, 0.25},
		{0, true, 1.25}, // zoom missing: taken as 1
	}
	for _, c := range cases {
		if got := zoomStep(c.z, c.in); math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("zoomStep(%g, %v) = %g — want %g", c.z, c.in, got, c.want)
		}
	}
}

// Visible sub-rectangle: centred, bounded, never outside the image.
func TestViewRect(t *testing.T) {
	cases := []struct {
		imgW, imgH, boxW, boxH int
		cx, cy                 float64
		x, y, w, h             int
	}{
		{1000, 800, 1000, 800, .5, .5, 0, 0, 1000, 800}, // it fits: everything
		{100, 100, 500, 500, .5, .5, 0, 0, 100, 100},    // smaller: everything
		{1000, 800, 500, 400, .5, .5, 250, 200, 500, 400},
		{1000, 800, 500, 400, 0, 0, 0, 0, 500, 400},     // top left corner
		{1000, 800, 500, 400, 1, 1, 500, 400, 500, 400}, // bottom right corner: moved back
		{1000, 800, 500, 800, .1, .5, 0, 0, 500, 800},   // left edge, whole height
	}
	for _, c := range cases {
		x, y, w, h := viewRect(c.imgW, c.imgH, c.boxW, c.boxH, c.cx, c.cy)
		if x != c.x || y != c.y || w != c.w || h != c.h {
			t.Fatalf("viewRect(%d,%d,%d,%d,%g,%g) = %d,%d,%d,%d — want %d,%d,%d,%d",
				c.imgW, c.imgH, c.boxW, c.boxH, c.cx, c.cy, x, y, w, h, c.x, c.y, c.w, c.h)
		}
	}
}

// geometry : at zoom 1 the image is fitted with no crop; at zoom 2 it fills
// the box and only a sub-rectangle of the frame is shown.
func TestViewerGeometry(t *testing.T) {
	v := &viewer{md: &model.Media{FrameW: 2000, FrameH: 1000}, zoom: 1, cx: .5, cy: .5}
	if c, r := v.geometry(82, 27, 10, 20); c != 80 || r != 20 || !v.crop.Empty() {
		t.Fatalf("zoom 1: %d×%d cells, crop %v", c, r, v.crop)
	}
	v.zoom = 2
	c, r := v.geometry(82, 27, 10, 20)
	if want := image.Rect(500, 200, 1500, 800); c != 80 || r != 24 || v.crop != want {
		t.Fatalf("zoom 2: %d×%d cells, crop %v — want 80×24, %v", c, r, v.crop, want)
	}
	v.cx = 1 // right edge: the view moves back onto the image
	if v.geometry(82, 27, 10, 20); v.crop != image.Rect(1000, 200, 2000, 800) {
		t.Fatalf("right edge: crop %v", v.crop)
	}
}

// A drag with the left button pans the zoomed image; a click with no move
// still closes the preview.
func TestViewerDragPans(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, t: &term.Term{Cols: 80, Rows: 24}, images: "halfblock"}
	md := &model.Media{Kind: model.MediaPhoto, Path: "x", FrameW: 800, FrameH: 600, Frames: [][]byte{nil}}
	u.viewer = &viewer{src: md, md: md, zoom: 4, cx: 0.5, cy: 0.5, cols: 40, rows: 20, crop: image.Rect(300, 225, 500, 375)}
	mouse := func(x, y int, press, motion bool) {
		u.key(term.Key{Code: term.Mouse, Mouse: term.MouseEvent{Button: 0, X: x, Y: y, Press: press, Motion: motion}})
	}
	mouse(10, 10, true, false)
	mouse(20, 10, true, true)
	if u.viewer == nil {
		t.Fatal("drag closed the preview")
	}
	if u.viewer.cx >= 0.5 {
		t.Fatalf("drag to the right: cx = %v, want the view moved left", u.viewer.cx)
	}
	mouse(20, 10, false, false)
	if u.viewer == nil {
		t.Fatal("release after a drag closed the preview")
	}
	mouse(10, 10, true, false)
	mouse(10, 10, false, false)
	if u.viewer != nil {
		t.Fatal("click with no move: preview still open")
	}
}
