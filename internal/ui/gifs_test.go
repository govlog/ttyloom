package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
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

// The answer fills the grid, the previews on the screen (and only those)
// start their download at the first drawing, Enter sends the current one as
// a pending line of the conversation and closes the box.
func TestGifSend(t *testing.T) {
	u, b := gifUI()
	u.ws.Cur = 1
	u.openGifs("cat")
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvGifs{Query: "cat", Gifs: gifList(12)}})
	g := u.gifs
	if len(g.gifs) != 12 || g.g.perRow != gridCols || g.g.rows != gridRows {
		t.Fatalf("grid: %d gifs, %dx%d", len(g.gifs), g.g.perRow, g.g.rows)
	}
	u.overlay() // a frame: the visible cells ask for their file
	if want := gridCols * gridRows; len(b.downloads) != want {
		t.Fatalf("downloads: %d, want the %d cells on the screen", len(b.downloads), want)
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

// However large the screen, 3 x 2 previews at most, with a scrollbar when the
// list is longer: a click at the foot of the bar shows the last rows, the
// wheel moves a row, and the previews far from the view let their frames go.
func TestGifGridScroll(t *testing.T) {
	u, _ := gifUI()
	u.t.Cols, u.t.Rows = 200, 60
	u.ws.Cur = 1
	u.openGifs("cat")
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvGifs{Query: "cat", Gifs: gifList(30)}})
	g := &u.gifs.g
	r := u.gifRect()
	if g.perRow != 3 || g.rows != 2 {
		t.Fatalf("grid %dx%d on a 200x60 screen, want 3x2", g.perRow, g.rows)
	}
	var bar strings.Builder
	for _, l := range u.gifLines(r) {
		bar.WriteString(render.LineText(l))
	}
	if !strings.Contains(bar.String(), "┃") {
		t.Fatal("no scrollbar thumb for 30 previews")
	}
	first := u.gifs.gifs[0].Preview
	first.State, first.Frames = model.MediaReady, [][]byte{{1}}
	u.gifMouse(term.MouseEvent{Press: true, X: r.col + 1 + g.width() - 1, Y: r.row + 2 + g.height() - 1})
	if g.top != 8 || g.cur < 24 {
		t.Fatalf("click at the foot of the bar: top %d, cur %d; want top 8 (rows 9 and 10), cur on the screen", g.top, g.cur)
	}
	u.overlay()
	if first.Frames != nil {
		t.Error("the frames of a preview 4 pages away are kept")
	}
	u.gifMouse(term.MouseEvent{Button: 64, Press: true, X: r.col + 2, Y: r.row + 3})
	if g.top != 7 {
		t.Errorf("wheel up: top %d, want 7", g.top)
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
