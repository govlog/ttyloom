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
	masked     bool // the text shown is not url: a click asks first
}

type hit struct {
	item   *Item
	img    *render.Img
	url    string
	masked bool
	act    *render.Action
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
			return hit{item: r.item, url: s.url, masked: s.masked}
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
	dragGrid          // scrollbar of a picture box (GIF box, media browser) grabbed
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
	// The tabs hold the columns of the message area only: on the status line
	// the sidebar keeps its own clicks.
	if m.Press && m.Button == 0 && u.tabsOn() && m.Y == u.tabRow() && m.X >= x0 {
		if net, ok := u.tabAt(m.X); ok {
			u.tabTo(net)
		}
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
	if u.mention != nil { // the @… box too: its click is never one on the message under it
		if r := u.mentionRect(); r.hits(m.Y, m.X, 1, 1) {
			u.mentionMouse(m, r)
			return
		}
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
		case h.url != "" && h.masked: // the text hides where the link goes: show it first
			u.openHidden(h.url)
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
	// The hover background is baked into the cached lines of both messages:
	// they are drawn again, as on a selection change. No height changes.
	u.hover.Invalidate()
	it.Invalidate()
	u.hover = it
	return true
}

// overlayLead tells whether an overlay has the lead on the mouse: key() routes
// every event to it, so nothing under it is hovered nor zoned. The viewer and
// the questions of the input line on top of the lead overlays.
func (u *UI) overlayLead() bool {
	return u.viewer != nil || u.lead() != nil || u.pager != nil || u.pasteAsk != "" || u.ask != nil || u.sendAsk != nil
}

// leadOverlay : an overlay that takes every event until it closes — the mouse
// to its mouse handler, the rest to its keyboard one.
type leadOverlay struct {
	open  func(*UI) bool
	key   func(*UI, term.Key)
	mouse func(*UI, term.MouseEvent)
	close func(*UI)            // from outside (login prompt, account change), by its own way out
	box   func(*UI) overlayBox // what overlay() draws of it
}

// leads : the lead overlays, the one on top first — the one list every reader
// goes by: key() gives an event to the first one open, overlay() draws them
// from the last to the first, so the one with the keys is the one on top. The
// hub comes last: it comes back on its own (hubBack) and must not cover a box
// the user opened meanwhile; its page (a form) right above it. Filled by init:
// the handlers reach back to the list (authStart closes the overlays).
var leads []leadOverlay

func init() {
	leads = []leadOverlay{
		{func(u *UI) bool { return u.picker != nil }, (*UI).pickerKey, (*UI).pickerMouse, func(u *UI) { u.picker = nil },
			func(u *UI) overlayBox {
				u.customLoad() // the images of the custom emojis on the screen
				return overlayBox{rect: u.pickerRect(), lines: u.picker.Lines(u.th)}
			}},
		{func(u *UI) bool { return u.themePick != nil }, (*UI).themeKey, (*UI).themeMouse, (*UI).themeCancel, // the theme in use comes back
			func(u *UI) overlayBox { return overlayBox{rect: u.themeRect(), lines: u.themePick.Lines(u.th)} }},
		{func(u *UI) bool { return u.newChat != nil }, (*UI).ncKey, (*UI).ncMouse, func(u *UI) { u.newChat = nil },
			func(u *UI) overlayBox {
				r := u.ncRect()
				return overlayBox{rect: r, lines: u.newChat.Lines(u.th, r.w, r.h, u.title, u.online)}
			}},
		{func(u *UI) bool { return u.gifs != nil }, (*UI).gifKey, (*UI).gifMouse, (*UI).gifClose,
			func(u *UI) overlayBox { r := u.gifRect(); return overlayBox{rect: r, lines: u.gifLines(r)} }},
		{func(u *UI) bool { return u.mbox != nil }, (*UI).mboxKey, (*UI).mboxMouse, (*UI).mboxClose,
			func(u *UI) overlayBox { r := u.gridRect(&u.mbox.g); return overlayBox{rect: r, lines: u.mboxLines(r)} }},
		{func(u *UI) bool { return u.gsearch != nil }, (*UI).gsKey, (*UI).gsMouse, func(u *UI) { u.gsearch = nil },
			func(u *UI) overlayBox {
				r := u.gsRect()
				return overlayBox{rect: r, lines: u.gsearch.Lines(u.th, r.w, r.h, u.title)}
			}},
		{func(u *UI) bool { return u.menu != nil }, (*UI).menuKey, (*UI).menuMouse, (*UI).closeMenu, // its line loses its highlight
			func(u *UI) overlayBox {
				r := u.menuBox()
				return overlayBox{rect: r, lines: u.menu.Lines(u.th, r.w, r.h)}
			}},
		{func(u *UI) bool { return u.form != nil }, (*UI).formKey, (*UI).formMouse, func(u *UI) { u.form = nil },
			func(u *UI) overlayBox { r := u.formRect(); return overlayBox{rect: r, lines: u.form.Lines(u.th, r.w)} }},
		{func(u *UI) bool { return u.hub != nil }, (*UI).hubKey, (*UI).hubMouse, func(u *UI) { u.hub = nil },
			func(u *UI) overlayBox {
				if u.form != nil {
					return overlayBox{} // its page takes its place
				}
				u.hubRefresh()
				r := u.hubRect()
				return overlayBox{rect: r, lines: u.hub.Lines(u.th, r.w)}
			}},
	}
}

// lead : the lead overlay on top, nil when none is open.
func (u *UI) lead() *leadOverlay {
	for i := range leads {
		if leads[i].open(u) {
			return &leads[i]
		}
	}
	return nil
}

// closeOverlays closes every lead overlay, each by its own way out.
func (u *UI) closeOverlays() {
	for _, o := range leads {
		if o.open(u) {
			o.close(u)
		}
	}
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

// followTop : the first entry shown of a window of rows over a list, moved as
// little as possible so that cur stays in view.
func followTop(top, cur, rows int) int { return min(max(top, cur-rows+1), cur) }

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

// zoneAt stores the zone under the pointer. true when the change shows — the
// only case where a mouse move is worth a repaint, exactly like hoverAt: the
// sidebar entered or left, or a scrollbar on the screen to colour. The
// keyboard follows: into the sidebar when the pointer enters it, back to the
// input line when it leaves; Shift+Tab can move it in between.
func (u *UI) zoneAt(x, y int) bool {
	z, old := u.zoneOf(x, y), u.zone
	if old == z {
		return false
	}
	u.zone = z
	u.find.keys = z == zoneSide
	return old == zoneSide || z == zoneSide || u.barShown
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
	if u.mention != nil && u.mentionRect().hits(y, x, 1, 1) {
		return nil // nor under the @… box
	}
	if h := hitAt(u.hits, x-x0, y); h.item != nil && selectable(h.item) {
		return h.item
	}
	return nil
}

// pickerKey : the picker keeps the keys until Enter or Esc; Ctrl+C closes it
// with no choice (Ctrl+C does not quit while it is open).
func (u *UI) pickerKey(k term.Key) {
	if (k.Code == term.Ctrl && k.Rune == 'c') || u.picker.Key(k) {
		u.picker = nil
	}
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
