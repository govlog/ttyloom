package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/clipperhouse/uax29/v2/graphemes"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// vt : the text side of a terminal, enough for what draw writes — cursor
// moves, erase to the end of the line, colours, text. Escape strings
// (kitty, sixel, OSC 8, Terminology) leave no text and are skipped.
type vt struct {
	cols, rows int
	cell       [][]string // text and colour of each cell
	row, col   int
	sgr        string
}

func newVT(cols, rows int) *vt {
	v := &vt{cols: cols, rows: rows, cell: make([][]string, rows)}
	for r := range v.cell {
		v.cell[r] = make([]string, cols)
	}
	return v
}

func (v *vt) clone() *vt {
	c := *v
	c.cell = make([][]string, v.rows)
	for r := range v.cell {
		c.cell[r] = append([]string(nil), v.cell[r]...)
	}
	return &c
}

func (v *vt) put(s string) {
	if v.row >= 0 && v.row < v.rows && v.col >= 0 && v.col < v.cols {
		v.cell[v.row][v.col] = v.sgr + s
	}
}

func (v *vt) feed(b []byte) {
	s := string(b)
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			g := graphemes.FromString(s[i:])
			g.Next()
			cl := g.Value()
			if j := strings.IndexByte(cl, 0x1b); j > 0 {
				cl = cl[:j]
			}
			v.put(cl)
			for k := 1; k < render.Width(cl); k++ {
				v.col++
				v.put("") // second half of a wide cluster
			}
			v.col++
			i += len(cl)
			continue
		}
		if i+1 >= len(s) {
			return
		}
		switch s[i+1] {
		case '[':
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j >= len(s) {
				return
			}
			v.csi(s[i+2:j], s[j])
			i = j + 1
		case ']', '_', 'P': // OSC, APC, DCS: up to ST
			j := strings.Index(s[i+2:], "\x1b\\")
			if j < 0 {
				return
			}
			i += 2 + j + 2
		case '}': // Terminology: up to NUL
			j := strings.IndexByte(s[i:], 0)
			if j < 0 {
				return
			}
			i += j + 1
		default:
			i += 2
		}
	}
}

func (v *vt) csi(p string, final byte) {
	switch final {
	case 'H':
		r, c, _ := strings.Cut(p, ";")
		v.row, _ = strconv.Atoi(r)
		v.col, _ = strconv.Atoi(c)
		v.row, v.col = v.row-1, v.col-1
	case 'K':
		for c := max(v.col, 0); c < v.cols && v.row >= 0 && v.row < v.rows; c++ {
			v.cell[v.row][c] = v.sgr + " "
		}
	case 'm':
		v.sgr = "\x1b[" + p + "m"
	}
}

// diff : the first cell where two screens differ, "" when they are the same.
func (v *vt) diff(w *vt) string {
	for r := range v.rows {
		for c := range v.cols {
			if v.cell[r][c] != w.cell[r][c] {
				return fmt.Sprintf("row %d col %d: %q, want %q", r, c, v.cell[r][c], w.cell[r][c])
			}
		}
	}
	return ""
}

// drawnGIF : a decoded GIF frame of the size of the fixture of the screenshots
// (96 x 54, shown as 480 x 270), drawn here.
func drawnGIF(t *testing.T) *model.Media {
	img := image.NewRGBA(image.Rect(0, 0, 96, 54))
	for y := range 54 {
		for x := range 96 {
			img.Set(x, y, color.RGBA{uint8(x * 2), uint8(y * 4), 128, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return &model.Media{Kind: model.MediaGIF, Label: "[gif 480x270]", State: model.MediaReady, Path: "x",
		Frames: [][]byte{b.Bytes()}, FrameW: 96, FrameH: 54, W: 480, H: 270, Loc: 1, Ext: ".gif", Mime: "image/gif"}
}

// Sending only the rows that changed leaves the screen exactly as sending the
// whole frame would, through typing, scrolling, overlays opening and closing,
// a window change and the sidebar going away.
func TestRowDiffMatchesFullFrame(t *testing.T) {
	for _, mode := range []string{"halfblock", "kitty"} {
		var out bytes.Buffer
		u := shotUI(t, theme.Terminal(), &out, drawnGIF(t))
		u.images = mode
		screen := newVT(shotCols, shotRows)
		steps := []struct {
			name string
			do   func()
		}{
			{"first frame", func() {}},
			{"typing", func() { u.ed.Set(u.ed.String() + "x") }},
			{"scroll up", func() { u.key(term.Key{Code: term.PgUp}) }},
			{"scroll down", func() { u.key(term.Key{Code: term.PgDn}) }},
			{"members and menu", func() { shotMembers(t, u) }},
			{"overlays closed", func() { u.menu, u.parts, u.partsOn = nil, nil, false }},
			{"picker", func() { shotPicker(t, u) }},
			{"picker closed", func() { u.picker = nil }},
			{"search", func() { shotSearch(t, u) }},
			{"search closed", func() { u.search, u.gsearch = nil, nil }},
			{"other window", func() { u.ws.Cur = 2 }},
			{"back", func() { u.ws.Cur = 1 }},
			{"no sidebar", func() { u.side = sideHidden }},
		}
		for _, st := range steps {
			st.do()
			before := screen.clone()
			out.Reset()
			u.draw()
			screen.feed(out.Bytes())
			u.repaint() // the same state, sent whole
			out.Reset()
			u.draw()
			want := before.clone()
			want.feed(out.Bytes())
			if d := screen.diff(want); d != "" {
				t.Fatalf("%s, %s: %s", mode, st.name, d)
			}
		}
	}
}

// In sixel, a key typed sends the input line, not the pictures again; a
// picture that moves (the sidebar hidden) goes again.
func TestSixelSentOnlyWhenItsRowsChange(t *testing.T) {
	var out bytes.Buffer
	u := shotUI(t, theme.Terminal(), &out, drawnGIF(t))
	u.images, u.t.Sixel = "sixel", true
	u.draw()
	if !strings.Contains(out.String(), "\x1bP") {
		t.Fatal("first frame: no sixel for the GIF on the screen")
	}
	out.Reset()
	u.ed.Set(u.ed.String() + "x") // a key typed
	u.draw()
	if strings.Contains(out.String(), "\x1bP") {
		t.Fatal("a key typed sent the pictures again")
	}
	out.Reset()
	u.side = sideHidden
	u.draw()
	if !strings.Contains(out.String(), "\x1bP") {
		t.Fatal("moved: the picture was not drawn again")
	}
}

// Terminology draws a file and never makes it bigger: zoomed in the viewer,
// the picture it gets is the zoomed region itself, at the size of the box.
func TestTerminologyZoomGivesTheZoomedBitmap(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	src := image.NewRGBA(image.Rect(0, 0, 100, 40)) // left half red, right half blue
	for y := range 40 {
		for x := range 100 {
			c := color.RGBA{255, 0, 0, 255}
			if x >= 50 {
				c = color.RGBA{0, 0, 255, 255}
			}
			src.Set(x, y, c)
		}
	}
	var frame bytes.Buffer
	if err := png.Encode(&frame, src); err != nil {
		t.Fatal(err)
	}
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{}, th: theme.Terminal(),
		t: term.NewOffscreen(&bytes.Buffer{}, 80, 24), images: "terminology"}
	u.t.CellW, u.t.CellH = 10, 20
	defer u.dropPics(true)
	md := &model.Media{Frames: [][]byte{frame.Bytes()}, FrameW: 100, FrameH: 40}
	// The left half, zoomed into 20 x 4 cells (200 x 80 px).
	u.placed = []placed{{row: 2, img: &render.Img{Media: md, Cols: 20, Rows: 4}, crop: image.Rect(0, 0, 50, 40)}}
	pics := u.cellPics(0)
	if len(pics) != 1 {
		t.Fatalf("%d pictures", len(pics))
	}
	_, rest, _ := strings.Cut(pics[0].seq, "\x1b}ic#20;4;")
	path, _, ok := strings.Cut(rest, "\x00")
	if !ok {
		t.Fatalf("no Terminology picture: %q", pics[0].seq)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 80 {
		t.Fatalf("bitmap %v, want the 50 x 40 region scaled to 100 x 80", b)
	}
	if r, _, bl, _ := img.At(99, 79).RGBA(); r>>8 < 200 || bl>>8 > 50 {
		t.Fatalf("right bottom pixel %v: the blue half came in", img.At(99, 79))
	}
}
