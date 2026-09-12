package ui

import (
	"slices"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

// rowHit : what is drawn on one screen line of the message area.
type rowHit struct {
	item *Item
	img  *render.Img     // line of a kitty image block
	urls []urlSpan       // column ranges of the links
	acts []render.Action // palette of the selected message, reactions
}

type urlSpan struct {
	col0, col1 int // [col0, col1)
	url        string
}

type hit struct {
	item *Item
	img  *render.Img
	url  string
	act  *render.Action
}

// hitAt : screen line y (0 = first message line), column x.
func hitAt(rows []rowHit, x, y int) hit {
	if y < 0 || y >= len(rows) {
		return hit{}
	}
	r := rows[y]
	for i, a := range r.acts {
		if x >= a.Col0 && x < a.Col1 {
			return hit{item: r.item, act: &r.acts[i]}
		}
	}
	for _, s := range r.urls {
		if x >= s.col0 && x < s.col1 {
			return hit{item: r.item, url: s.url}
		}
	}
	return hit{item: r.item, img: r.img}
}

// dragMode : kind of drag running; it tells what the moves and the release
// that follow are worth.
type dragMode int

const (
	dragNone dragMode = iota
	dragBar           // scrollbar cursor grabbed
	dragText          // selection of messages to copy
	dragSide          // │ bar of the sidebar: resizing
	dragView          // left button held in the preview: the image pans
)

// mouse : wheel = scroll, left click = link, image, selection or scrollbar
// (last column of the message area).
func (u *UI) mouse(m term.MouseEvent) {
	// Press and release never go through the move path of Run(): the zone is
	// read again here, so that a click after a clear() and the release of a
	// drag paint the right one instead of a frame with nothing lit.
	u.zoneAt(m.X, m.Y)
	x0, cols := u.layout()
	bar, view := x0+cols, u.viewRows()
	// Drag running: the move follows the mouse, the release ends it. A new
	// click goes to the normal path, which opens a drag again when it is on
	// the bar: without that, a lost release would block the clicks.
	if u.drag != dragNone && ((m.Motion && m.Button == 0) || !m.Press) { // 1003: motion with no button (3) = lost drag
		switch {
		case !m.Press && u.drag == dragText:
			u.selRelease() // copy, or click when the mouse did not move
		case !m.Press && u.drag == dragSide:
			u.sideDragEnd() // width saved
		case !m.Press:
			u.drag = dragNone
		case u.drag == dragBar:
			u.dragTo(m.Y, view)
		case u.drag == dragSide:
			u.sideDragTo(m.X)
		} // the move in dragText is handled by Run(): never here
		return
	}
	// Here: a press outside a drag (key() filters the rest); a lost release
	// leaves neither a stuck drag nor a stuck highlight.
	u.selCancel()
	u.who = nil // wheel or click: the anchor of the popup no longer holds
	if u.spellFix != nil && u.spellFixMouse(m) {
		return
	}
	if m.Press && m.Button == 2 && m.Y >= u.t.Rows-u.inputRows() {
		u.spellClick(m.X, m.Y) // right click on a misspelled word of the input
		return
	}
	if m.Button == 0 && m.X == bar && m.Y < view {
		if _, _, ok := scrollbar(len(u.view().Lines(u.opts())), view, 0); !ok {
			return // no bar: nothing to grab
		}
		u.drag = dragBar
		u.dragTo(m.Y, view)
		return
	}
	if x0 > 0 && m.Button == 0 && m.X == x0-1 { // bar of the sidebar: grabbed to resize
		u.drag = dragSide
		return
	}
	if x0 > 0 && m.X < x0 { // sidebar
		u.sideMouse(m)
		return
	}
	// The member box covers the messages: it takes the click and the wheel
	// before them, which keeps it out of u.hits.
	if r, ok := u.partsRect(); ok && r.hits(m.Y, m.X, 1, 1) {
		u.partsMouse(m, r)
		return
	}
	if r, ok := u.jumpRect(); ok && u.jumpShown && m.Press && m.Button == 0 && r.hits(m.Y, m.X, 1, 1) {
		u.view().Scroll = 0 // pill "↓ last message"
		return
	}
	if m.Press && m.Button == 2 && m.Y < u.viewRows() { // right click: the menu of the message
		if h := hitAt(u.hits, m.X-x0, m.Y); h.item != nil && selectable(h.item) {
			u.openMsgMenu(h.item, m.X, m.Y)
		}
		return
	}
	switch {
	case m.Button == 64: // wheel up
		u.scroll(u.view(), 3)
	case m.Button == 65: // wheel down
		u.scroll(u.view(), -3)
	case m.Button == 0 && m.Y < u.viewRows():
		h := hitAt(u.hits, m.X-x0, m.Y)
		switch {
		case h.act != nil:
			u.actClick(u.view(), h.item, *h.act)
		case h.url != "":
			u.open(h.url) // xdg-open opens a URL as well as a path
		case h.img != nil && h.item != nil && h.item.Msg != nil:
			// Click on the image: full screen preview, the same as "v". "o"
			// and /open keep the desktop viewer (xdg-open).
			u.viewMsg(h.item.Msg)
		case h.item != nil && selectable(h.item):
			now := time.Now()
			// lastClick.item may have left the window since (trim, /clear,
			// chatGone): a pointer found by chance on an item outside the view
			// does not count as a second click.
			if isDoubleClick(u.lastClick.item, h.item, u.lastClick.at, now) && slices.Contains(u.view().Items, u.lastClick.item) {
				u.lastClick.item, u.lastClick.at = nil, time.Time{} // a triple click does not toggle back
				if w := u.view(); w.Sel != h.item {
					u.setSel(w, h.item) // the message stays selected as after the first click
				}
				u.reactDouble(h.item)
				return
			}
			u.lastClick.item, u.lastClick.at = h.item, now
			// Anchor of a selection drag: nothing is selected nor copied before
			// the release, which tells a click from a copy.
			u.drag, u.selAnchor, u.selEnd, u.selY = dragText, h.item, nil, m.Y
		}
	}
}

// isDoubleClick : press on the same message as the press before, less than
// 400 ms apart.
func isDoubleClick(prev, cur *Item, prevAt, now time.Time) bool {
	return prev != nil && prev == cur && now.Sub(prevAt) < 400*time.Millisecond
}

// dragTo brings the top of the bar cursor to line y. It goes through
// scroll() so that the top of a partial window loads the history, like PgUp.
func (u *UI) dragTo(y, view int) {
	w := u.view()
	total := len(w.Lines(u.opts()))
	u.scroll(w, scrollFromY(min(max(y, 0), view-1), view, total)-w.Scroll)
}

// hoverAt updates the message under the pointer. true when the hover changed
// (the only case where a mouse move is worth a repaint).
func (u *UI) hoverAt(x, y int) bool {
	it := u.hoverItem(x, y)
	if u.hover == it {
		return false
	}
	// The help line changes the height of both messages: their drawings must
	// be made again, as on a selection change.
	u.hover.Invalidate()
	it.Invalidate()
	u.hover = it
	return true
}

// overlayLead tells whether an overlay has the lead on the mouse: key() routes
// every event to it, so nothing under it is hovered nor zoned.
func (u *UI) overlayLead() bool {
	return u.viewer != nil || u.picker != nil || u.themePick != nil || u.gsearch != nil || u.newChat != nil || u.form != nil ||
		u.gifs != nil || u.menu != nil || u.pager != nil || u.pasteAsk != "" || u.ask != nil || u.sendAsk != nil
}

// leadOverlay : an overlay that takes every event until it closes — the mouse
// to its mouse handler, the rest to its keyboard one. It is not the same list
// as overlayLead(): that one counts six overlays more: the picker and the
// viewer keep their own block in key(), the other four have no mouse handler.
type leadOverlay struct {
	open  bool
	key   func(term.Key)
	mouse func(term.MouseEvent)
}

// listOverlay : the shape the list overlays share (theme picker, new chat,
// global search, context menu) — a box with a header, a window of rows over n
// entries, and the four things an event does to them. It is built at each
// event from the open overlay and holds no state of its own: cur, scroll and
// query stay in the box.
type listOverlay struct {
	r     rect        // box on screen, borders included
	head  int         // lines between the top border and the first entry
	rows  int         // entries the box shows
	top   int         // entry drawn on the first row
	n     int         // entries in all
	move  func(d int) // current entry moved by d
	click func(i int) // left click on entry i, which is on screen
	enter func()      // Enter on the current entry
	close func()      // Esc, Ctrl+C, or a click outside the box
}

// key routes the keys every list overlay answers the same way: the two exits,
// Enter, and the six moves. false when the key is none of them — the caller keeps its
// own branches (typing, Ctrl+F).
func (l listOverlay) key(k term.Key) bool {
	switch {
	case k.Code == term.Esc, k.Code == term.Ctrl && k.Rune == 'c':
		l.close()
	case k.Code == term.Enter:
		l.enter()
	case k.Code == term.Up:
		l.move(-1)
	case k.Code == term.Down:
		l.move(1)
	case k.Code == term.PgUp:
		l.move(-l.rows)
	case k.Code == term.PgDn:
		l.move(l.rows)
	case k.Code == term.Home:
		l.move(-l.n)
	case k.Code == term.End:
		l.move(l.n)
	default:
		return false
	}
	return true
}

// mouse routes a mouse event: outside the box closes, the wheel moves by one
// entry, a left click goes to the entry under the pointer. A click on a
// border, on the header or on an empty row does nothing.
func (l listOverlay) mouse(e term.MouseEvent) {
	switch {
	case !l.r.hits(e.Y, e.X, 1, 1):
		l.close()
	case e.Button == 64:
		l.move(-1)
	case e.Button == 65:
		l.move(1)
	case e.Button == 0:
		y := e.Y - l.r.row - l.head
		if i := l.top + y; y >= 0 && y < l.rows && i < l.n {
			l.click(i)
		}
	}
}

// zone : area of the screen the pointer sits in — what the wheel acts on and
// what the highlight follows (follow-mouse).
type zone int

const (
	zoneNone zone = iota // status bar, input, or nothing to follow (hover off, overlay)
	zoneSide             // sidebar (F2), its │ bar included
	zoneMsgs             // message area
	zoneBar              // scrollbar column, last one of the message area
)

// zoneOf gives the zone of the cell (x, y). Same gating as hoverItem: with
// hover off or an overlay on top, no zone is followed.
func (u *UI) zoneOf(x, y int) zone {
	if u.cfg.Hover == config.HoverOff || u.overlayLead() {
		return zoneNone
	}
	x0, cols := u.layout()
	if x0 > 0 && x < x0 { // the panel and its bar make one zone: the wheel of both is its own
		return zoneSide
	}
	if r, ok := u.partsRect(); ok && r.hits(y, x, 1, 1) {
		return zoneNone // the member box takes the wheel: nothing to follow under it
	}
	switch {
	case y >= u.viewRows(): // separator line, status bar, input
		return zoneNone
	case x >= x0+cols: // column kept for the bar, whether one shows or not
		return zoneBar
	default:
		return zoneMsgs
	}
}

// zoneAt stores the zone under the pointer. true when it changed — the only
// case where a mouse move is worth a repaint, exactly like hoverAt.
func (u *UI) zoneAt(x, y int) bool {
	z := u.zoneOf(x, y)
	if u.zone == z {
		return false
	}
	u.zone = z
	return true
}

// hoverItem gives the message hovered at (x, y), nil outside the message area
// or when an overlay has the lead.
func (u *UI) hoverItem(x, y int) *Item {
	if u.cfg.Hover == config.HoverOff || u.overlayLead() {
		return nil
	}
	x0, cols := u.layout()
	if x < x0 || x >= x0+cols || y >= u.viewRows() { // sidebar, bar, separator, status, input
		return nil
	}
	if r, ok := u.partsRect(); ok && r.hits(y, x, 1, 1) {
		return nil // under the member box: no message is hovered
	}
	if h := hitAt(u.hits, x-x0, y); h.item != nil && selectable(h.item) {
		return h.item
	}
	return nil
}

// pickerMouse : left click on an emoji = choice, wheel = next or previous
// line, click outside the box = close with no choice. key() lets only presses
// through: neither release nor move.
func (u *UI) pickerMouse(m term.MouseEvent) {
	ov := u.pickerRect()
	x, y := m.X-ov.col, m.Y-ov.row
	switch {
	case x < 0 || x >= ov.w || y < 0 || y >= ov.h:
		u.picker = nil
	case m.Button == 64:
		u.picker.move(-u.picker.cols)
	case m.Button == 65:
		u.picker.move(u.picker.cols)
	case m.Button == 0 && u.picker.Click(x, y):
		u.picker = nil
	}
}
