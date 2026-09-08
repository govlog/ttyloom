package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// GIF box (Ctrl+G, /gif): a search line, a grid of animated previews, Enter
// or a click sends the one chosen to the conversation of the window. The
// network answers (@gif on Telegram, Discord's GIF provider); the box only
// shows and picks. Same mechanics as the new chat overlay for the typing: the
// query goes out once it has settled, one request in flight, a stale answer
// dropped. An empty query shows the trending ones.

const (
	gifFrames = 40      // frames decoded per preview: memory, not fidelity
	gifPID    = 1 << 21 // kitty placements of the box: gifPID | rank of the cell
)

type gifBox struct {
	chat     *model.Chat // conversation the GIF goes to, pinned at opening
	query    []rune
	gifs     []model.Gif
	cur      int
	top      int // first grid row shown
	inflight string
	sent     string
	asked    bool // a query went out at least once (the trending one is "")
	typed    time.Time
	err      string
	perRow   int // previews per grid row, from the box width
	rows     int // grid rows shown, from the box height
	cellCols int // size of one preview, in cells
	cellRows int
}

// gifCell : size of one preview in cells — a 16:9 clip on kitty, wider and
// taller in half blocks, where a cell is one pixel by two.
func (u *UI) gifCell() (cols, rows int) {
	if u.images == "halfblock" {
		return 24, 10
	}
	return 16, 7
}

// visible : rank of the first cell on the screen and of the one past the last.
func (g *gifBox) visible() (lo, hi int) {
	lo = g.top * g.perRow
	return lo, min(len(g.gifs), lo+g.perRow*g.rows)
}

// owns tells whether md is one of the previews of the box.
func (g *gifBox) owns(md *model.Media) bool {
	for _, x := range g.gifs {
		if x.Preview == md {
			return true
		}
	}
	return false
}

// move : bounded move by d cells; the rows shown follow the current one.
func (g *gifBox) move(d int) {
	if len(g.gifs) == 0 {
		return
	}
	g.cur = min(max(g.cur+d, 0), len(g.gifs)-1)
	row := g.cur / g.perRow
	if row < g.top {
		g.top = row
	}
	if row >= g.top+g.rows {
		g.top = row - g.rows + 1
	}
}

// scroll : the wheel moves the rows shown by d, the current cell stays inside.
func (g *gifBox) scroll(d int) {
	if len(g.gifs) == 0 {
		return
	}
	last := (len(g.gifs) - 1) / g.perRow
	g.top = min(max(g.top+d, 0), last)
	lo, hi := g.visible()
	g.cur = min(max(g.cur, lo), hi-1)
}

// gifLayout sizes the grid on the screen: three previews wide when the width
// allows, as many rows as the height takes.
func (u *UI) gifLayout() {
	g := u.gifs
	g.cellCols, g.cellRows = u.gifCell()
	inner := max(min(u.t.Cols-4, 3*(g.cellCols+1)-1), g.cellCols)
	g.perRow = max(1, (inner+1)/(g.cellCols+1))
	g.rows = max(1, (u.t.Rows-6)/(g.cellRows+1))
	g.move(0) // a resize: the current cell comes back on the screen
}

// gifRect : the box, centred. Borders, a header, the rows each followed by
// its marker line, a foot.
func (u *UI) gifRect() rect {
	u.gifLayout()
	g := u.gifs
	return centerRect(u.t.Cols, u.t.Rows, g.perRow*(g.cellCols+1)+1, g.rows*(g.cellRows+1)+4)
}

// openGifs : Ctrl+G, /gif. The conversation is the one the input goes to.
func (u *UI) openGifs(q string) {
	w := u.sendWin()
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
	u.gifs = &gifBox{chat: c, query: []rune(render.CleanLine(q))}
	u.gifLayout()
	u.gifQuery()
}

// gifQuery starts the search when the query changed. One at a time — the next
// one goes out when the one in flight comes back (gifsResult).
func (u *UI) gifQuery() {
	g := u.gifs
	q := strings.TrimSpace(string(g.query))
	if g.inflight != "" || (g.asked && q == g.sent) {
		return
	}
	b := u.net(g.chat)
	if b == nil {
		g.err = i18n.T("net_unsupported", g.chat.Net)
		return
	}
	g.inflight, g.err, g.asked = q, "", true
	b.SearchGifs(u.ctx, g.chat, q)
}

// gifsResult : answer of the network. A query given up is dropped; a query
// that moved during the round trip goes out again.
func (u *UI) gifsResult(e model.EvGifs) {
	g := u.gifs
	if g == nil || e.Query != g.inflight || u.dispatchNet != g.chat.Net {
		return
	}
	g.inflight = ""
	if strings.TrimSpace(string(g.query)) != e.Query {
		u.gifQuery()
		return
	}
	u.gifFree() // the previews of the list before: frames and images go
	g.err, g.sent, g.gifs, g.cur, g.top = render.CleanLine(e.Err), e.Query, e.Gifs, 0, 0
	if e.Err != "" {
		g.asked = false // the same query can go out again
	}
}

// gifName : file of a preview in the cache, named by a hash of its handle —
// never by a string of the network.
func gifName(md *model.Media) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%v", md.Loc)))
	return hex.EncodeToString(h[:10]) + md.Ext
}

// gifLoad starts the download of the previews on the screen that have none
// yet. Called at each drawing: a cell that scrolls into view asks then.
func (u *UI) gifLoad() {
	g := u.gifs
	b := u.net(g.chat)
	if b == nil {
		return
	}
	lo, hi := g.visible()
	for i := lo; i < hi; i++ {
		md := g.gifs[i].Preview
		if md.State != model.MediaNone || !md.Previewable() {
			continue
		}
		md.State = model.MediaLoading
		b.Download(u.ctx, md, filepath.Join(config.CacheDir(), "gifs", g.chat.Net, gifName(md)))
	}
}

// gifDecode : a preview just downloaded — decoded to its cell, gifFrames at
// most. downloaded() routes the previews of the box here.
func (u *UI) gifDecode(md *model.Media) {
	g := u.gifs
	cw, ch, _, _ := u.cells()
	u.loadFrames(md, g.cellCols*cw, g.cellRows*ch, gifFrames, 0)
}

// gifFree drops what the previews decoded and sent to the terminal. A
// download still on its way is orphaned: dropped when it lands.
func (u *UI) gifFree() {
	for _, x := range u.gifs.gifs {
		md := x.Preview
		u.cancelDecode(md)
		if s := u.kittyFree(md); s != "" && u.t.Kitty {
			u.t.WriteString(s)
		}
		if md.State == model.MediaLoading {
			if u.gifOrphan == nil {
				u.gifOrphan = map[*model.Media]bool{}
			}
			u.gifOrphan[md] = true
		}
		md.Frames, md.State, md.Frame, md.Want = nil, model.MediaNone, 0, 0
	}
}

func (u *UI) gifClose() {
	u.gifFree()
	u.gifs = nil
}

// gifSend posts the current cell in the conversation of the box, as a
// pending line like a file send; the receipt and the echo replace it.
func (u *UI) gifSend() {
	g := u.gifs
	if g.cur >= len(g.gifs) {
		return
	}
	pick, c := g.gifs[g.cur], g.chat
	u.gifClose()
	b := u.net(c)
	if b == nil {
		return
	}
	w := u.winFor(c)
	u.tmpID++
	me := u.selfOf(c.Net)
	m := &model.Msg{Net: c.Net, ChatID: c.ID, ChatLabel: c.Title, Date: time.Now(), From: me.Name, FromID: me.ID,
		Out: true, Pending: true, TmpID: u.tmpID,
		Media: &model.Media{Kind: model.MediaGIF, State: model.MediaLoading, Label: "[gif]"}}
	u.insertPending(w, m)
	b.SendGif(u.ctx, c, pick, u.tmpID)
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
	case k.Code == term.Left:
		g.move(-1)
	case k.Code == term.Right:
		g.move(1)
	case k.Code == term.Up:
		g.move(-g.perRow)
	case k.Code == term.Down:
		g.move(g.perRow)
	case k.Code == term.PgUp:
		g.move(-g.perRow * g.rows)
	case k.Code == term.PgDn:
		g.move(g.perRow * g.rows)
	case k.Code == term.Home:
		g.move(-len(g.gifs))
	case k.Code == term.End:
		g.move(len(g.gifs))
	case k.Code == term.Backspace:
		if n := len(g.query); n > 0 {
			g.query, g.typed = g.query[:n-1], time.Now()
		}
	case k.Code == term.Paste:
		g.query, g.typed = append(g.query, []rune(render.CleanLine(k.Text))...), time.Now()
	case k.Code == term.None && k.Rune != 0 && !k.Alt:
		g.query, g.typed = append(g.query, k.Rune), time.Now()
	}
}

// gifMouse : a click outside closes, the wheel moves the rows, a click on a
// cell sends it.
func (u *UI) gifMouse(e term.MouseEvent) {
	g, r := u.gifs, u.gifRect()
	switch {
	case !r.hits(e.Y, e.X, 1, 1):
		u.gifClose()
	case e.Button == 64:
		g.scroll(-1)
	case e.Button == 65:
		g.scroll(1)
	case e.Button == 0:
		if i, ok := u.gifCellAt(e.X, e.Y, r); ok {
			g.cur = i
			u.gifSend()
		}
	}
}

// gifCellAt gives the rank of the cell under (x, y); a gap, the chrome or an
// empty cell gives false.
func (u *UI) gifCellAt(x, y int, r rect) (int, bool) {
	g := u.gifs
	x, y = x-r.col-1, y-r.row-2
	if x < 0 || y < 0 {
		return 0, false
	}
	row, ln := y/(g.cellRows+1), y%(g.cellRows+1)
	col, cx := x/(g.cellCols+1), x%(g.cellCols+1)
	if row >= g.rows || ln >= g.cellRows || col >= g.perRow || cx >= g.cellCols {
		return 0, false
	}
	i := (g.top+row)*g.perRow + col
	return i, i < len(g.gifs)
}

// gifPlacements : the previews on the screen as kitty placements of the
// frame, centred in their cell — animate() moves them like the images of the
// messages. x0: first column of the message area (Col is relative to it).
func (u *UI) gifPlacements(x0 int) []placed {
	g := u.gifs
	if g == nil || u.images != "kitty" {
		return nil
	}
	r := u.gifRect()
	cellW, cellH, _, _ := u.cells()
	var out []placed
	lo, hi := g.visible()
	for i := lo; i < hi; i++ {
		md := g.gifs[i].Preview
		if len(md.Frames) == 0 {
			continue
		}
		cols, rows := render.Box(md.FrameW, md.FrameH, g.cellCols, g.cellRows, cellW, cellH)
		if cols < 1 || rows < 1 {
			continue
		}
		k := i - lo
		row := r.row + 2 + (k/g.perRow)*(g.cellRows+1) + (g.cellRows-rows)/2
		col := r.col + 1 + (k%g.perRow)*(g.cellCols+1) + (g.cellCols-cols)/2
		out = append(out, placed{row: row, pid: gifPID | uint32(i), img: &render.Img{Media: md, Col: col - x0, Cols: cols, Rows: rows}})
	}
	return out
}

// gifLines draws the box: header with the query and the count, the grid (a
// preview in half blocks, its state while nothing is decoded, blank under a
// kitty image), a marker line under the current cell, the keys. It also
// starts the downloads of the cells on the screen.
func (u *UI) gifLines(r rect) []render.Line {
	g := u.gifs
	u.gifLoad()
	box, edge, _ := boxStyles(u.th)
	dim, acc := box, box
	dim.FG, acc.FG = u.th.Color(theme.Dim), u.th.Color(theme.Accent)
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
		head += fmt.Sprintf("  %d/%d", g.cur+1, len(g.gifs))
	}
	out := []render.Line{b.bar("┌", "┐"), b.text(head, box)}
	lo, hi := g.visible()
	// Half blocks: the frame of each cell decoded once for the whole box.
	frames := map[int][]render.Line{}
	if u.images == "halfblock" {
		for i := lo; i < hi; i++ {
			if md := g.gifs[i].Preview; len(md.Frames) > 0 {
				if cols, rows := render.Box(md.FrameW, md.FrameH, g.cellCols, g.cellRows, 1, 2); cols > 0 && rows > 0 {
					frames[i] = render.HalfblockFrame(md, cols, rows)
				}
			}
		}
	}
	blank := render.Span{Text: strings.Repeat(" ", g.cellCols), Style: box}
	gap := render.Span{Text: " ", Style: box}
	cell := func(i, ln int) []render.Span {
		if i >= len(g.gifs) {
			return []render.Span{blank}
		}
		md := g.gifs[i].Preview
		if fr, ok := frames[i]; ok {
			top := (g.cellRows - len(fr)) / 2
			if ln < top || ln >= top+len(fr) {
				return []render.Span{blank}
			}
			l := fr[ln-top]
			left := (g.cellCols - render.Width(render.LineText(l))) / 2
			sp := []render.Span{{Text: strings.Repeat(" ", max(0, left)), Style: box}}
			sp = append(sp, l.Spans...)
			return append(sp, render.Span{Text: strings.Repeat(" ", max(0, g.cellCols-left-render.Width(render.LineText(l)))), Style: box})
		}
		if len(md.Frames) > 0 || ln != 0 {
			return []render.Span{blank} // kitty: the image covers the cell
		}
		text := i18n.T("loading")
		if md.State == model.MediaFailed {
			text = render.CleanLine(md.Err)
		}
		return []render.Span{{Text: padTo(render.Truncate(text, g.cellCols, "…"), g.cellCols), Style: dim}}
	}
	for row := 0; row < g.rows; row++ {
		for ln := 0; ln < g.cellRows; ln++ {
			var sp []render.Span
			for c := 0; c < g.perRow; c++ {
				if c > 0 {
					sp = append(sp, gap)
				}
				sp = append(sp, cell((g.top+row)*g.perRow+c, ln)...)
			}
			out = append(out, b.row(sp...))
		}
		var sp []render.Span // marker line: the current cell is underlined
		for c := 0; c < g.perRow; c++ {
			if c > 0 {
				sp = append(sp, gap)
			}
			if i := (g.top+row)*g.perRow + c; i == g.cur && i < len(g.gifs) {
				sp = append(sp, render.Span{Text: strings.Repeat("━", g.cellCols), Style: acc})
			} else {
				sp = append(sp, blank)
			}
		}
		out = append(out, b.row(sp...))
	}
	return append(out, b.text(i18n.T("gif_keys"), edge), b.bar("└", "┘"))
}
