package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// Picture grid of the GIF box (Ctrl+G) and of the media browser (Ctrl+M):
// gridCols x gridRows cells at most on the screen out of a longer list, a
// marker line under each row (the current cell underlined) and a scrollbar
// at their right, thicker under the pointer, that a drag moves. Only the
// live cell plays — the one under the pointer, or the one the arrows just
// reached; the others hold their frame. One picture moves at a time: little
// to send to the terminal, nothing that blinks.

const (
	gridCols = 6
	gridRows = 4
	gridPID  = 1 << 21 // kitty placements of the cells: gridPID | rank of the cell
	gridKeep = 2       // pages of cells kept decoded on each side of the view
)

type grid struct {
	n                  int  // cells in the list
	cur, top           int  // current cell, first row shown
	live               int  // cell that plays, -1: none
	perRow, rows       int  // cells per row, rows shown
	cellCols, cellRows int  // size of one cell
	barHot, held       bool // pointer on the scrollbar, thumb grabbed: drawn thicker
}

// thumbs : a box drawn on the grid. thumb gives the picture of cell i, nil
// for a cell with none (a file).
type thumbs interface {
	grid() *grid
	thumb(i int) *model.Media
}

// thumbBox : the picture box open, nil with none.
func (u *UI) thumbBox() thumbs {
	switch {
	case u.gifs != nil:
		return u.gifs
	case u.mbox != nil:
		return u.mbox
	}
	return nil
}

// ownsThumb tells whether md is the picture of a cell of t.
func ownsThumb(t thumbs, md *model.Media) bool {
	for i := range t.grid().n {
		if t.thumb(i) == md {
			return true
		}
	}
	return false
}

// gridStill : md is a picture of the box open (ok), which holds its frame
// unless it is the live cell.
func (u *UI) gridStill(md *model.Media) (still, ok bool) {
	t := u.thumbBox()
	if t == nil || !ownsThumb(t, md) {
		return false, false
	}
	g := t.grid()
	return g.live < 0 || g.live >= g.n || t.thumb(g.live) != md, true
}

// gridRect : a box around g, centred — borders, a header, the grid, a foot —
// with g fitted to the screen first.
func (u *UI) gridRect(g *grid) rect {
	cc, cr := u.gridCell()
	g.fit(u.t.Cols, u.t.Rows, cc, cr)
	return centerRect(u.t.Cols, u.t.Rows, g.width()+2, g.height()+4)
}

// gridOrigin : screen position of the top left cell of the box open.
func (u *UI) gridOrigin() (row, col int) {
	r := u.gridRect(u.thumbBox().grid())
	return r.row + 2, r.col + 1
}

// gridHover : the pointer moved to (x, y) of the screen; true when the cell
// that plays changed.
func (u *UI) gridHover(x, y int) bool {
	t := u.thumbBox()
	if t == nil || u.viewer != nil { // the preview covers the box
		return false
	}
	row, col := u.gridOrigin()
	return t.grid().hover(x-col, y-row)
}

// thumbPlacements : the kitty placements of the box open, if any.
func (u *UI) thumbPlacements(x0 int) []placed {
	t := u.thumbBox()
	if t == nil {
		return nil
	}
	row, col := u.gridOrigin()
	return u.gridPlacements(t, row, col, x0)
}

// gridCell : size of one cell — a 16:9 clip on kitty, wider and taller in
// half blocks, where a cell is one pixel by two.
func (u *UI) gridCell() (cols, rows int) {
	if u.images == "halfblock" {
		return 24, 10
	}
	return 16, 7
}

// fit sizes the grid for a screen of cols x rows: gridCols x gridRows cells
// of cellCols x cellRows. On a smaller screen the cells shrink first, down to
// half their size, then the grid loses columns or rows. The box around takes
// two borders, a header, a foot, the scrollbar and a margin.
func (g *grid) fit(cols, rows, cellCols, cellRows int) {
	g.perRow, g.cellCols = fitAxis(cols-5, gridCols, cellCols)
	g.rows, g.cellRows = fitAxis(rows-6, gridRows, cellRows)
	g.move(0) // a resize: the current cell comes back on the screen
}

// fitAxis : n cells of size want at most, each followed by a gap, in room:
// the size shrinks to fit, down to half of want; past that, cells go.
func fitAxis(room, n, want int) (count, size int) {
	least := max(1, want/2)
	count = max(1, min(n, room/(least+1)))
	return count, max(1, min(want, room/count-1))
}

// width : inner columns of the grid, the scrollbar included; height : its
// lines, a marker line under each row of cells.
func (g *grid) width() int  { return g.perRow*(g.cellCols+1) + 1 }
func (g *grid) height() int { return g.rows * (g.cellRows + 1) }

// visible : rank of the first cell on the screen and of the one past the last.
func (g *grid) visible() (lo, hi int) {
	lo = g.top * g.perRow
	return lo, min(g.n, lo+g.perRow*g.rows)
}

func (g *grid) lastTop() int { return max(0, (g.n+g.perRow-1)/g.perRow-g.rows) }

// move : bounded move of the current cell by d; the rows shown follow it.
func (g *grid) move(d int) {
	if g.n == 0 {
		return
	}
	g.cur = min(max(g.cur+d, 0), g.n-1)
	row := g.cur / g.perRow
	g.top = min(max(g.top, row-g.rows+1), row)
}

// scrollTo shows the rows from top on; the current cell stays inside.
func (g *grid) scrollTo(top int) {
	g.top = min(max(top, 0), g.lastTop())
	if lo, hi := g.visible(); hi > lo {
		g.cur = min(max(g.cur, lo), hi-1)
	}
}

// key : the moves of the grid — arrows, pages, Home, End. The cell reached
// plays. false: not a move.
func (g *grid) key(k term.Key) bool {
	page := g.perRow * g.rows
	switch k.Code {
	case term.Left:
		g.move(-1)
	case term.Right:
		g.move(1)
	case term.Up:
		g.move(-g.perRow)
	case term.Down:
		g.move(g.perRow)
	case term.PgUp:
		g.move(-page)
	case term.PgDn:
		g.move(page)
	case term.Home:
		g.move(-g.n)
	case term.End:
		g.move(g.n)
	default:
		return false
	}
	g.live = g.cur
	return true
}

// at gives the cell under (x, y), counted from the top left cell; a gap, a
// marker line, the scrollbar or an empty cell gives -1.
func (g *grid) at(x, y int) int {
	if x < 0 || y < 0 {
		return -1
	}
	row, ln := y/(g.cellRows+1), y%(g.cellRows+1)
	col, cx := x/(g.cellCols+1), x%(g.cellCols+1)
	if row >= g.rows || ln >= g.cellRows || col >= g.perRow || cx >= g.cellCols {
		return -1
	}
	if i := (g.top+row)*g.perRow + col; i < g.n {
		return i
	}
	return -1
}

// bar : the scrollbar geometry, in lines from the top of the grid.
func (g *grid) bar() (top, length int, ok bool) {
	step := g.cellRows + 1
	total := (g.lastTop() + g.rows) * step
	return scrollbar(total, g.height(), total-g.height()-g.top*step)
}

// onBar tells whether (x, y) is on the scrollbar.
func (g *grid) onBar(x, y int) bool { return x == g.width()-1 && y >= 0 && y < g.height() }

// barTo brings the rows where line y of the scrollbar points.
func (g *grid) barTo(y int) {
	step := g.cellRows + 1
	total := (g.lastTop() + g.rows) * step
	above := total - g.height() - scrollFromY(y, g.height(), total)
	g.scrollTo((above + step/2) / step)
}

// mouse : the wheel scrolls a row, a press on the scrollbar brings the rows
// there and grabs it (grab: a drag follows, see drag), the pointer makes the
// cell under it play. x, y count from the top left cell; click gives the
// cell clicked, -1 for none.
func (g *grid) mouse(e term.MouseEvent, x, y int) (click int, grab bool) {
	click = -1
	switch {
	case e.Button == 64:
		g.scrollTo(g.top - 1)
	case e.Button == 65:
		g.scrollTo(g.top + 1)
	case e.Button == 0 && e.Press && !e.Motion && g.onBar(x, y):
		g.barTo(y)
		g.held, grab = true, true
	case e.Button == 0 && e.Press && !e.Motion:
		if click = g.at(x, y); click >= 0 {
			g.cur = click
		}
	}
	g.live, g.barHot = g.at(x, y), g.onBar(x, y) // after a scroll, another cell is under the pointer
	return click, grab
}

// drag : a move of the pointer with the thumb grabbed — the rows follow it;
// the button let go ends the drag (false).
func (g *grid) drag(e term.MouseEvent, y int) bool {
	if !e.Press {
		g.held = false
		return false
	}
	g.barTo(y)
	return true
}

// hover : the pointer moved to (x, y) of the grid. true when the cell that
// plays changed, or the pointer came on or left the scrollbar — worth a
// repaint.
func (g *grid) hover(x, y int) bool {
	i, hot := g.at(x, y), g.onBar(x, y)
	if i == g.live && hot == g.barHot {
		return false
	}
	g.live, g.barHot = i, hot
	return true
}

// lines draws the rows of the grid inside b: cell(i, ln) gives line ln of
// cell i, cellCols wide.
func (g *grid) lines(b boxDraw, th theme.Theme, fill theme.Style, cell func(i, ln int) []render.Span) []render.Line {
	acc, dim := fill, fill
	acc.FG, dim.FG = th.Color(theme.Accent), th.Color(theme.Dim)
	blank := render.Span{Text: strings.Repeat(" ", g.cellCols), Style: fill}
	marker := render.Span{Text: strings.Repeat("━", g.cellCols), Style: acc}
	gap := render.Span{Text: " ", Style: fill}
	barTop, barLen, bar := g.bar()
	step := g.cellRows + 1
	out := make([]render.Line, 0, g.height())
	sp := make([]render.Span, 0, 2*g.perRow+2)
	for y := range g.height() {
		row, ln := y/step, y%step
		sp = sp[:0]
		for c := range g.perRow {
			if c > 0 {
				sp = append(sp, gap)
			}
			switch i := (g.top+row)*g.perRow + c; {
			case i >= g.n:
				sp = append(sp, blank)
			case ln == g.cellRows && i == g.cur:
				sp = append(sp, marker)
			case ln == g.cellRows:
				sp = append(sp, blank)
			default:
				sp = append(sp, cell(i, ln)...)
			}
		}
		mark := render.Span{Text: " ", Style: fill}
		switch {
		case bar && y >= barTop && y < barTop+barLen && (g.barHot || g.held):
			mark = render.Span{Text: "█", Style: acc} // pointed or grabbed: thicker
		case bar && y >= barTop && y < barTop+barLen:
			mark = render.Span{Text: "┃", Style: acc}
		case bar && g.barHot:
			mark = render.Span{Text: "│", Style: acc}
		case bar:
			mark = render.Span{Text: "│", Style: dim}
		}
		out = append(out, b.row(append(sp, gap, mark)...))
	}
	return out
}

// gridCells gives the drawing of the cells of t: the picture in half blocks,
// blank under a kitty image (it covers the cell), else the state of the
// download or text(i), the lines of a cell with no picture.
func (u *UI) gridCells(t thumbs, fill theme.Style, text func(i int) []string) func(i, ln int) []render.Span {
	g := t.grid()
	dim := fill
	dim.FG = u.th.Color(theme.Dim)
	frames := map[int][]render.Line{} // each picture turned to half blocks once for the whole box
	if u.images == "halfblock" {
		lo, hi := g.visible()
		for i := lo; i < hi; i++ {
			if md := t.thumb(i); md != nil && len(md.Frames) > 0 {
				if cols, rows := render.Box(md.FrameW, md.FrameH, g.cellCols, g.cellRows, 1, 2); cols > 0 && rows > 0 {
					frames[i] = render.HalfblockFrame(md, cols, rows)
				}
			}
		}
	}
	blank := []render.Span{{Text: strings.Repeat(" ", g.cellCols), Style: fill}}
	return func(i, ln int) []render.Span {
		md := t.thumb(i)
		if fr, ok := frames[i]; ok {
			top := (g.cellRows - len(fr)) / 2
			if ln < top || ln >= top+len(fr) {
				return blank
			}
			l := fr[ln-top]
			w := render.Width(render.LineText(l))
			left := max(0, (g.cellCols-w)/2)
			sp := append([]render.Span{{Text: strings.Repeat(" ", left), Style: fill}}, l.Spans...)
			return append(sp, render.Span{Text: strings.Repeat(" ", max(0, g.cellCols-left-w)), Style: fill})
		}
		if md != nil && len(md.Frames) > 0 {
			return blank // kitty: the image covers the cell
		}
		var lines []string
		switch {
		case md == nil && text != nil:
			lines = text(i)
		case md != nil && md.State == model.MediaFailed:
			lines = []string{render.CleanLine(md.Err)}
		case md != nil:
			lines = []string{i18n.T("loading")}
		}
		if ln >= len(lines) {
			return blank
		}
		return []render.Span{{Text: padTo(render.Truncate(lines[ln], g.cellCols, "…"), g.cellCols), Style: dim}}
	}
}

// gridPlacements : the pictures of the cells on the screen as kitty
// placements of the frame, centred in their cell; row0, col0: screen
// position of the top left cell, x0: first column of the message area (Col
// is relative to it). animate() moves them like the images of the messages.
func (u *UI) gridPlacements(t thumbs, row0, col0, x0 int) []placed {
	if !pixelMode(u.images) {
		return nil
	}
	g := t.grid()
	cellW, cellH, _, _ := u.cells()
	var out []placed
	lo, hi := g.visible()
	for i := lo; i < hi; i++ {
		md := t.thumb(i)
		if md == nil || len(md.Frames) == 0 {
			continue
		}
		cols, rows := render.Box(md.FrameW, md.FrameH, g.cellCols, g.cellRows, cellW, cellH)
		if cols < 1 || rows < 1 {
			continue
		}
		k := i - lo
		row := row0 + (k/g.perRow)*(g.cellRows+1) + (g.cellRows-rows)/2
		col := col0 + (k%g.perRow)*(g.cellCols+1) + (g.cellCols-cols)/2
		out = append(out, placed{row: row, pid: gridPID | uint32(i), img: &render.Img{Media: md, Col: col - x0, Cols: cols, Rows: rows}})
	}
	return out
}

// gridDecode : a picture of the box just downloaded — decoded to the size of
// its cell; gifFrames at most for a GIF, the first frame of anything else.
func (u *UI) gridDecode(g *grid, md *model.Media) {
	cw, ch, _, _ := u.cells()
	frames := 1
	if md.Kind == model.MediaGIF {
		frames = gifFrames
	}
	u.loadFrames(md, g.cellCols*cw, g.cellRows*ch, frames, 0)
}

// gridMouse runs the mouse of a picture box on g, whose top left cell is at
// (row0, col0) of the screen: a drag of the thumb goes on wherever the
// pointer goes, a press on the scrollbar grabs it. It gives the cell
// clicked, -1 for none.
func (u *UI) gridMouse(g *grid, e term.MouseEvent, row0, col0 int) int {
	if u.drag == dragGrid {
		if !g.drag(e, e.Y-row0) {
			u.drag = dragNone
		}
		return -1
	}
	click, grab := g.mouse(e, e.X-col0, e.Y-row0)
	if grab {
		u.drag = dragGrid
	}
	return click
}

// gridFree drops what the pictures of t decoded and sent to the terminal. A
// download still on its way is orphaned: dropped when it lands. A picture
// that was decoding is loading too, but its download has landed already.
func (u *UI) gridFree(t thumbs) {
	if u.drag == dragGrid { // the box closes or changes its list under the thumb
		u.drag, t.grid().held = dragNone, false
	}
	for i := range t.grid().n {
		md := t.thumb(i)
		if md == nil {
			continue
		}
		decoding := u.cancelDecode(md)
		u.dropImage(md)
		if md.State == model.MediaLoading && !decoding {
			if u.gridOrphan == nil {
				u.gridOrphan = map[*model.Media]bool{}
			}
			u.gridOrphan[md] = true
		}
		u.framesLRU.drop(md)
		md.Frames, md.State, md.Frame, md.Want = nil, model.MediaNone, 0, 0
	}
}

// thumbName : file of a picture in the cache, named by a hash of its handle
// — never by a string of the network.
func thumbName(md *model.Media) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%v", md.Loc)))
	return hex.EncodeToString(h[:10]) + md.Ext
}

// gridRelease drops the frames of the pictures far from the view — more
// than gridKeep pages away. Their file stays: back in view, they are decoded
// again from the disk.
func (u *UI) gridRelease(t thumbs) {
	g := t.grid()
	lo, hi := g.visible()
	page := g.perRow * g.rows
	for i := range g.n {
		if md := t.thumb(i); md != nil && len(md.Frames) > 0 && (i < lo-gridKeep*page || i >= hi+gridKeep*page) {
			u.dropFrames(md)
		}
	}
}
