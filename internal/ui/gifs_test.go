package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

func (f *fakeBackend) SearchGifs(_ context.Context, _ *model.Chat, q string) {
	f.gifQueries = append(f.gifQueries, q)
}
func (f *fakeBackend) SendGif(_ context.Context, _ *model.Chat, g model.Gif, _ int64) {
	f.gifsSent = append(f.gifsSent, g)
}
func (f *fakeBackend) Download(_ context.Context, _ *model.Media, path string) {
	f.downloads = append(f.downloads, path)
}

// gifUI : a kitty terminal, one Telegram chat in window 1 (window 0 is shown).
func gifUI() (*UI, *fakeBackend) {
	b := &fakeBackend{caps: model.AllCaps()}
	c := &model.Chat{Net: netTelegram, ID: 1, Kind: model.ChatUser, Title: "Alice"}
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, th: theme.Terminal(), cfg: &config.Config{},
		t: &term.Term{Cols: 100, Rows: 30, CellW: 10, CellH: 20, Kitty: true}, images: "kitty",
		nets: map[string]model.Backend{netTelegram: b}, chats: map[model.ChatKey]*model.Chat{c.Key(): c},
		self: map[string]selfInfo{netTelegram: {ID: 9, Name: "me"}}, ctx: context.Background(),
		chatList: []*model.Chat{c}}
	w := u.ws.New(true)
	w.Chat, w.Loaded = c, true
	return u, b
}

// The box asks gridDownloads previews at a time, in the order of the grid,
// and a landing lets the next one go. While some are on their way, the tick
// draws again: their cells move.
func TestGridDownloadQueue(t *testing.T) {
	u, b := gifUI()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // the decoding of the test file ends with the test
	u.ctx = ctx
	u.ws.Cur = 1
	u.openGifs("cat")
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvGifs{Query: "cat", Gifs: gifList(12)}})
	g := u.gifs.gifs
	box := func() string {
		var s strings.Builder
		for _, l := range u.gifLines(u.gifRect()) {
			s.WriteString(render.LineText(l) + "\n")
		}
		return s.String()
	}
	box()
	if len(b.downloads) != gridDownloads || !strings.HasSuffix(b.downloads[0], thumbName(g[0].Preview)) ||
		!strings.HasSuffix(b.downloads[gridDownloads-1], thumbName(g[gridDownloads-1].Preview)) {
		t.Fatalf("first drawing: downloads %v, want the first %d cells", b.downloads, gridDownloads)
	}
	if !u.tick() {
		t.Fatal("no repaint while the downloads run")
	}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvDownloaded{Media: g[0].Preview, Path: b.downloads[0]}})
	box()
	if len(b.downloads) != gridDownloads+1 || !strings.HasSuffix(b.downloads[gridDownloads], thumbName(g[gridDownloads].Preview)) {
		t.Fatalf("after a landing: downloads %v, want the next cell", b.downloads)
	}
}

// A cell with no picture yet draws its state in grey: waiting, on its middle
// line; a download of known size fills it with bars to its share, to the
// eighth of a column; of unknown size, a band of bars sweeps it; decoding, it
// stays full with the word cut into its middle line.
func TestGridCellStates(t *testing.T) {
	u, _ := gifUI()
	cell := func(md *model.Media, ln int) string {
		return render.LineText(render.Line{Spans: u.thumbCell(md, ln, 16, 7, theme.Style{})})
	}
	md := &model.Media{Kind: model.MediaGIF, Loc: 1}
	if got := cell(md, 3); strings.TrimSpace(got) != i18n.T("grid_waiting") || strings.TrimSpace(cell(md, 0)) != "" {
		t.Fatalf("waiting: %q", got)
	}
	md.State, md.DLDone, md.DLTotal = model.MediaLoading, 50, 100
	for _, ln := range []int{0, 3, 6} {
		if got := cell(md, ln); got != strings.Repeat("█", 8)+strings.Repeat(" ", 8) {
			t.Fatalf("half downloaded, line %d: %q", ln, got)
		}
	}
	md.DLDone = 53 // 16 cells x 8 eighths x 53 % = 67.8: 8 cells and 3 eighths
	if got := cell(md, 0); got != strings.Repeat("█", 8)+"▍"+strings.Repeat(" ", 7) {
		t.Fatalf("53 %%: %q", got)
	}
	md.DLTotal = 0
	now := time.Now()
	a, b := sweepBar(now, 16), sweepBar(now.Add(100*time.Millisecond), 16)
	if a == b || !strings.Contains(a, "████") || strings.Count(a, "█") != 4 || render.Width(a) != 16 || cell(md, 0) == strings.Repeat(" ", 16) {
		t.Fatalf("unknown size: %q then %q, cell %q", a, b, cell(md, 0))
	}
	u.decodes = map[*model.Media]context.CancelFunc{md: func() {}}
	if got := cell(md, 0); got != strings.Repeat("█", 16) || !strings.Contains(cell(md, 3), i18n.T("grid_decoding")) {
		t.Fatalf("decoding: %q / %q", got, cell(md, 3))
	}
}

func gifList(n int) []model.Gif {
	gifs := make([]model.Gif, n)
	for i := range gifs {
		gifs[i] = model.Gif{Preview: &model.Media{Kind: model.MediaGIF, Loc: i, Ext: ".gif", Mime: "image/gif", W: 100, H: 100, Label: "[gif]"}, Send: i}
	}
	return gifs
}

// Ctrl+G with no conversation says so and opens nothing; from a conversation
// the box opens and asks the trending GIFs of its network at once.
func TestGifOpen(t *testing.T) {
	u, b := gifUI()
	u.openGifs("")
	if u.gifs != nil || len(b.gifQueries) != 0 {
		t.Fatal("opened from window 0, which has no conversation")
	}
	u.ws.Cur = 1
	u.openGifs("")
	if u.gifs == nil || len(b.gifQueries) != 1 || b.gifQueries[0] != "" {
		t.Fatalf("open: box %v, queries %v", u.gifs != nil, b.gifQueries)
	}
}

// The answer fills the grid, Enter sends the current preview as a pending
// line of the conversation and closes the box.
func TestGifSend(t *testing.T) {
	u, b := gifUI()
	u.ws.Cur = 1
	u.openGifs("cat")
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvGifs{Query: "cat", Gifs: gifList(12)}})
	g := u.gifs
	if len(g.gifs) != 12 || g.g.perRow != gridCols || g.g.rows != gridRows {
		t.Fatalf("grid: %d gifs, %dx%d", len(g.gifs), g.g.perRow, g.g.rows)
	}
	u.gifKey(term.Key{Code: term.Right})
	u.gifKey(term.Key{Code: term.Enter})
	if u.gifs != nil {
		t.Fatal("box still open after the send")
	}
	if len(b.gifsSent) != 1 || b.gifsSent[0].Send != 1 {
		t.Fatalf("sent: %+v", b.gifsSent)
	}
	w := u.ws.List[1]
	last := w.Items[len(w.Items)-1].Msg
	if last == nil || !last.Pending || last.Media == nil || last.Media.Kind != model.MediaGIF {
		t.Fatalf("pending line: %+v", last)
	}
}

// A stale answer (query given up) is dropped; Esc closes and frees what the
// previews had decoded.
func TestGifStaleAndClose(t *testing.T) {
	u, _ := gifUI()
	u.ws.Cur = 1
	u.openGifs("cat")
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvGifs{Query: "dog", Gifs: gifList(3)}})
	if len(u.gifs.gifs) != 0 {
		t.Fatal("answer of a query given up taken")
	}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvGifs{Query: "cat", Gifs: gifList(3)}})
	md := u.gifs.gifs[0].Preview
	md.State, md.Frames = model.MediaReady, [][]byte{{1}}
	u.gifKey(term.Key{Code: term.Esc})
	if u.gifs != nil || md.Frames != nil || md.State != model.MediaNone {
		t.Fatalf("close: box %v, frames %v, state %v", u.gifs != nil, md.Frames, md.State)
	}
}

// Closing the box while a preview decodes leaves nothing behind: the preview
// was taken for a download still on its way, kept as such for the session,
// and a later download of the same media was swallowed.
func TestGifCloseDuringDecode(t *testing.T) {
	u, _ := gifUI()
	u.events = make(chan model.Event, 4) // the decoding reports there
	u.ws.Cur = 1
	u.openGifs("cat")
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvGifs{Query: "cat", Gifs: gifList(1)}})
	md := u.gifs.gifs[0].Preview
	md.State = model.MediaLoading
	u.downloaded(model.EvDownloaded{Media: md, Path: "/nonexistent.gif"}) // its decoding starts
	u.gifKey(term.Key{Code: term.Esc})
	u.downloaded(model.EvDownloaded{Media: md, Path: "/again.gif"})
	if md.Path != "/again.gif" {
		t.Fatal("a download after the close was swallowed as an orphan of the box")
	}
}

// 6 x 4 previews at most; on a smaller screen the cells shrink before the
// grid loses columns. A scrollbar shows when the list is longer: a click at
// its foot shows the last rows and grabs the thumb, a drag brings it back
// to the top, the wheel moves a row, and the previews far from the view let
// their frames go.
func TestGifGridScroll(t *testing.T) {
	u, _ := gifUI()
	u.ws.Cur = 1
	u.openGifs("cat")
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvGifs{Query: "cat", Gifs: gifList(200)}})
	g := &u.gifs.g
	for _, sz := range []struct{ cols, rows, perRow, gridRows, cellCols, cellRows int }{
		{200, 60, 6, 4, 16, 7}, // room for the whole size
		{80, 24, 6, 4, 11, 3},  // the cells shrink
		{40, 15, 3, 2, 10, 3},  // then cells go
	} {
		u.t.Cols, u.t.Rows = sz.cols, sz.rows
		u.gifRect()
		if g.perRow != sz.perRow || g.rows != sz.gridRows || g.cellCols != sz.cellCols || g.cellRows != sz.cellRows {
			t.Fatalf("%dx%d screen: %dx%d cells of %dx%d, want %dx%d of %dx%d", sz.cols, sz.rows,
				g.perRow, g.rows, g.cellCols, g.cellRows, sz.perRow, sz.gridRows, sz.cellCols, sz.cellRows)
		}
	}
	u.t.Cols, u.t.Rows = 200, 60
	r := u.gifRect()
	var bar strings.Builder
	for _, l := range u.gifLines(r) {
		bar.WriteString(render.LineText(l))
	}
	if !strings.Contains(bar.String(), "┃") {
		t.Fatal("no scrollbar thumb for 200 previews")
	}
	if !u.gridHover(r.col+1+g.width()-1, r.row+2) {
		t.Fatal("the pointer on the scrollbar asks no repaint")
	}
	bar.Reset()
	for _, l := range u.gifLines(r) {
		bar.WriteString(render.LineText(l))
	}
	if !strings.Contains(bar.String(), "█") {
		t.Fatal("the thumb under the pointer is not thicker")
	}
	first := u.gifs.gifs[0].Preview
	first.State, first.Frames = model.MediaReady, [][]byte{{1}}
	barX, foot := r.col+1+g.width()-1, r.row+2+g.height()-1
	u.gifMouse(term.MouseEvent{Press: true, X: barX, Y: foot})
	if g.top != 30 || g.cur < 180 || u.drag != dragGrid {
		t.Fatalf("press at the foot of the bar: top %d, cur %d, drag %v; want top 30 (the last rows), cur on the screen, the thumb held", g.top, g.cur, u.drag)
	}
	u.overlay()
	if first.Frames != nil {
		t.Error("the frames of a preview far from the view are kept")
	}
	u.gifMouse(term.MouseEvent{Press: true, Motion: true, X: barX + 9, Y: r.row + 2}) // dragged up, off the bar
	u.gifMouse(term.MouseEvent{X: barX, Y: r.row + 2})                                // let go
	if g.top != 0 || u.drag != dragNone || u.gifs == nil {
		t.Fatalf("drag to the top: top %d, drag %v, box open %v", g.top, u.drag, u.gifs != nil)
	}
	u.gifMouse(term.MouseEvent{Button: 65, Press: true, X: r.col + 2, Y: r.row + 3})
	if g.top != 1 {
		t.Errorf("wheel down: top %d, want 1", g.top)
	}
}

// A change of the cell size (font zoom) decodes a preview again for its
// cell: the frames of a preview, never the box and the 100 frames of a GIF
// of the messages.
func TestGifPreviewZoomStaysInCell(t *testing.T) {
	u, _ := gifUI()
	u.t = term.NewOffscreen(&bytes.Buffer{}, 100, 30)
	u.t.CellW, u.t.CellH, u.t.Kitty = 10, 20, true
	u.events = make(chan model.Event, 4) // the decoding reports there
	u.ws.Cur = 1
	u.openGifs("cat")
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvGifs{Query: "cat", Gifs: gifList(1)}})
	md := u.gifs.gifs[0].Preview
	md.State, md.Path, md.Frames, md.FrameW, md.FrameH = model.MediaReady, "/x.gif", [][]byte{{1}}, 160, 140
	md.CellW, md.CellH = 10, 20
	u.t.CellW, u.t.CellH = 15, 30
	u.draw()
	if md.Want != gifFrames {
		t.Fatalf("after the zoom: %d frames asked, want the %d of a preview", md.Want, gifFrames)
	}
}

// Only the preview under the pointer plays, or the one the arrows reached;
// the others hold their frame — in kitty (gifStill) and in half blocks.
func TestGifLivePreview(t *testing.T) {
	u, _ := gifUI()
	u.ws.Cur = 1
	u.openGifs("cat")
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvGifs{Query: "cat", Gifs: gifList(6)}})
	for _, x := range u.gifs.gifs {
		x.Preview.State, x.Preview.Frames, x.Preview.Delay = model.MediaReady, [][]byte{{1}, {2}}, time.Millisecond
	}
	a, b := u.gifs.gifs[0].Preview, u.gifs.gifs[1].Preview
	if !u.gifStill(a) || !u.gifStill(b) {
		t.Fatal("a preview plays before any hover")
	}
	row, col := u.gridOrigin()
	if !u.gridHover(col+1, row+1) || u.gifStill(a) || !u.gifStill(b) {
		t.Fatal("the preview under the pointer must play, alone")
	}
	u.gifKey(term.Key{Code: term.Right})
	if !u.gifStill(a) || u.gifStill(b) {
		t.Fatal("the arrows must move the play to the cell reached")
	}
	u.images = "halfblock"
	u.animate(time.Now().Add(time.Second))
	if a.Frame != 0 || b.Frame != 1 {
		t.Errorf("half blocks: frames %d and %d, want 0 (held) and 1 (live)", a.Frame, b.Frame)
	}
	if u.gridHover(0, 0); !u.gifStill(b) {
		t.Error("the pointer off the grid must stop the play")
	}
}

// The reports of a download move its count forward only — the parts of a
// parallel download post out of order — and leave a picture already decoding
// alone.
func TestDownloadProgressForwardOnly(t *testing.T) {
	u, _ := gifUI()
	md := &model.Media{State: model.MediaLoading}
	for _, done := range []int64{40, 20} {
		u.event(model.EvDownloadProgress{Media: md, Done: done, Total: 100})
	}
	if md.DLDone != 40 || md.DLTotal != 100 {
		t.Fatalf("out of order: %d/%d, want 40/100", md.DLDone, md.DLTotal)
	}
	u.decodes = map[*model.Media]context.CancelFunc{md: func() {}}
	u.event(model.EvDownloadProgress{Media: md, Done: 90, Total: 100})
	if md.DLDone != 40 {
		t.Fatalf("report during the decoding: %d", md.DLDone)
	}
}
