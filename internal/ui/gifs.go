package ui

import (
	"fmt"
	"path/filepath"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

// GIF box (Ctrl+G, /gif): a search line, a grid of previews (grid.go),
// Enter or a click sends the one chosen to the conversation of the window.
// The network answers (@gif on Telegram, Discord's GIF provider); the box
// only shows and picks. Same mechanics as the new chat overlay for the
// typing: the query goes out once it has settled, one request in flight, a
// stale answer dropped. An empty query shows the trending ones.

const gifFrames = 40 // frames decoded per preview: memory, not fidelity

type gifBox struct {
	netQuery             // the trending ones answer the empty query
	chat     *model.Chat // conversation the GIF goes to, pinned at opening
	gifs     []model.Gif
	g        grid
}

func (b *gifBox) grid() *grid              { return &b.g }
func (b *gifBox) thumb(i int) *model.Media { return b.gifs[i].Preview }

func (u *UI) gifRect() rect { return u.gridRect(&u.gifs.g) }

// openGifs : Ctrl+G, /gif. The conversation is the one the input goes to.
func (u *UI) openGifs(q string) {
	w := u.sendWin()
	if w == nil {
		return
	}
	c := w.Chat
	if c == nil {
		w.AddSys(i18n.T("window_not_bound"))
		return
	}
	if u.selfOf(c.Net).Bot {
		u.sys(i18n.T("bot_unavailable"))
		return
	}
	if !u.caps(c).Gifs {
		u.netUnsupported(c.Net)
		return
	}
	if u.images == "off" { // a grid of blank cells would pick blind
		u.sys(i18n.T("images_off"))
		return
	}
	u.gifs = &gifBox{chat: c, netQuery: netQuery{query: []rune(render.CleanLine(q))}, g: grid{live: -1}}
	u.gifRect() // sized before the first answer
	u.gifQuery()
}

// gifQuery starts the search when the query changed. One at a time — the next
// one goes out when the one in flight comes back (gifsResult).
func (u *UI) gifQuery() {
	g := u.gifs
	q, ok := g.next()
	if !ok {
		return
	}
	b := u.net(g.chat)
	if b == nil {
		g.fail(i18n.T("net_unsupported", g.chat.Net))
		return
	}
	b.SearchGifs(u.backendContext(b), g.chat, q)
}

// gifsResult : answer of the network. A query given up is dropped; a query
// that moved during the round trip goes out again.
func (u *UI) gifsResult(e model.EvGifs) {
	g := u.gifs
	if g == nil || e.Query != g.inflight || u.dispatchNet != g.chat.Net {
		return
	}
	if g.back(e.Query) {
		u.gifQuery()
		return
	}
	u.gridFree(g) // the previews of the list before: frames and images go
	g.shown(e.Query, e.Err)
	g.gifs = e.Gifs
	g.g.n, g.g.cur, g.g.top, g.g.live = len(e.Gifs), 0, 0, -1
}

// gifLoad starts the download of the previews on the screen that have none
// yet, and lets the frames of the ones far from the view go. Called at each
// drawing: a cell that scrolls into view asks then.
func (u *UI) gifLoad() {
	g := u.gifs
	b := u.net(g.chat)
	if b == nil {
		return
	}
	u.gridRelease(g)
	lo, hi := g.g.visible()
	for i := lo; i < hi; i++ {
		md := g.gifs[i].Preview
		if md.State != model.MediaNone || !md.Previewable() {
			continue
		}
		md.State = model.MediaLoading
		b.Download(u.backendContext(b), md, filepath.Join(config.CacheDir(), "gifs", g.chat.Net, thumbName(md)))
	}
}

func (u *UI) gifClose() {
	u.gridFree(u.gifs)
	u.gifs = nil
}

// gifSend posts the current cell in the conversation of the box, as a
// pending line like a file send; the receipt and the echo replace it.
func (u *UI) gifSend() {
	g := u.gifs
	if g.g.cur >= len(g.gifs) {
		return
	}
	pick, c := g.gifs[g.g.cur], g.chat
	u.gifClose()
	b := u.net(c)
	if b == nil {
		return
	}
	w := u.winFor(c)
	m := u.pendingMsg(c, "")
	m.Media = &model.Media{Kind: model.MediaGIF, State: model.MediaLoading, Label: "[gif]"}
	u.insertPending(w, m)
	b.SendGif(u.backendContext(b), c, pick, m.TmpID)
	u.flash(i18n.T("sending"))
}

// gifKey : the box takes every key — moves in the grid, Enter sends, Esc
// closes, the rest types the query.
func (u *UI) gifKey(k term.Key) {
	g := u.gifs
	switch {
	case k.Code == term.Esc, k.Code == term.Ctrl && k.Rune == 'c':
		u.gifClose()
	case k.Code == term.Enter:
		u.gifSend()
	case g.g.key(k):
	default:
		g.edit(k) // the rest types the query
	}
}

// gifMouse : a click outside closes; inside, the grid takes the wheel, the
// scrollbar and the pointer, and a click on a cell sends it.
func (u *UI) gifMouse(e term.MouseEvent) {
	r := u.gifRect()
	if u.drag != dragGrid && !r.hits(e.Y, e.X, 1, 1) { // a drag of the thumb may leave the box
		if e.Press && !e.Motion {
			u.gifClose()
		} else {
			u.gifs.g.live = -1
		}
		return
	}
	if u.gridMouse(&u.gifs.g, e, r.row+2, r.col+1) >= 0 {
		u.gifSend()
	}
}

// gifLines draws the box: header with the query and the count, the grid, the
// keys. It also starts the downloads of the cells on the screen.
func (u *UI) gifLines(r rect) []render.Line {
	g := u.gifs
	u.gifLoad()
	box, edge, _ := boxStyles(u.th)
	b := boxDraw{edge: edge, fill: box, inner: r.w - 2}
	head := i18n.T("gif_head", render.CleanLine(string(g.query)))
	switch {
	case g.err != "":
		head += "  (" + g.err + ")"
	case g.inflight != "":
		head += "  …"
	case len(g.gifs) == 0:
		head += "  " + i18n.T("no_result")
	default:
		head += fmt.Sprintf("  %d/%d", g.g.cur+1, len(g.gifs))
	}
	out := []render.Line{b.bar("┌", "┐"), b.text(head, box)}
	out = append(out, g.g.lines(b, u.th, box, u.gridCells(g, box, nil))...)
	return append(out, b.text(i18n.T("gif_keys"), edge), b.bar("└", "┘"))
}
