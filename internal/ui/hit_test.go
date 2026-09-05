package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

func TestHitAt(t *testing.T) {
	it := &Item{}
	rows := []rowHit{
		{item: it, urls: []urlSpan{{col0: 6, col1: 17, url: "https://a.example"}}},
		{item: it},
		{},
	}
	if h := hitAt(rows, 10, 0); h.item != it || h.url != "https://a.example" {
		t.Fatalf("url: %+v", h)
	}
	if h := hitAt(rows, 17, 0); h.item != it || h.url != "" { // bound left out
		t.Fatalf("edge: %+v", h)
	}
	if h := hitAt(rows, 3, 1); h.item != it || h.url != "" {
		t.Fatalf("item only: %+v", h)
	}
	if h := hitAt(rows, 0, 2); h.item != nil {
		t.Fatalf("empty row: %+v", h)
	}
	if h := hitAt(rows, 0, 9); h.item != nil {
		t.Fatalf("out of range: %+v", h)
	}
}

// isDoubleClick : same item, not nil, and less than 400 ms.
func TestDoubleClick(t *testing.T) {
	it1, it2 := &Item{Msg: &model.Msg{ID: 1}}, &Item{Msg: &model.Msg{ID: 2}}
	t0 := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	if isDoubleClick(nil, it1, t0, t0.Add(100*time.Millisecond)) {
		t.Fatal("no earlier press")
	}
	if isDoubleClick(it1, it2, t0, t0.Add(100*time.Millisecond)) {
		t.Fatal("different item")
	}
	if isDoubleClick(it1, it1, t0, t0.Add(400*time.Millisecond)) {
		t.Fatal("400 ms exactly: too late")
	}
	if !isDoubleClick(it1, it1, t0, t0.Add(399*time.Millisecond)) {
		t.Fatal("same item, under 400 ms")
	}
}

// hoverAt : item under the pointer, and repaint only on a change.
func TestHoverAt(t *testing.T) {
	it1, it2 := &Item{Msg: &model.Msg{ID: 1}}, &Item{Msg: &model.Msg{ID: 2}}
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{Hover: config.HoverMenu},
		t: &term.Term{Cols: 80, Rows: 6}} // 4 message lines, column 79 = the bar
	u.hits = []rowHit{{item: it1}, {item: it2}, {}}
	if !u.hoverAt(5, 0) || u.hover != it1 {
		t.Fatalf("it1: %v", u.hover)
	}
	if u.hoverAt(9, 0) {
		t.Fatal("same item: no repaint")
	}
	if !u.hoverAt(5, 1) || u.hover != it2 {
		t.Fatalf("it2: %v", u.hover)
	}
	if !u.hoverAt(79, 1) || u.hover != nil {
		t.Fatalf("scroll bar: %v", u.hover)
	}
	u.hoverAt(5, 1)
	if !u.hoverAt(5, 4) || u.hover != nil {
		t.Fatalf("status bar: %v", u.hover)
	}
	u.cfg.Hover = config.HoverOff
	if u.hoverAt(5, 0) || u.hover != nil {
		t.Fatalf("hover off: %v", u.hover)
	}
}

// TestOverlayMouseClose : a click outside the box closes each overlay; the
// wheel inside leaves it open. Only the routing is exercised — no terminal.
func TestOverlayMouseClose(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // newPicker reads the recent emojis
	th := theme.Terminal()
	newUI := func() *UI {
		return &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{},
			t: &term.Term{Cols: 80, Rows: 24}, th: th}
	}
	out := term.MouseEvent{X: 0, Y: 0, Button: 0, Press: true} // top left corner: outside every centred box

	u := newUI()
	u.picker = newPicker(60, 14, nil)
	r := u.pickerRect()
	if r.row == 0 || r.col == 0 {
		t.Fatalf("picker box at the corner: %+v", r) // the click below would fall inside
	}
	u.pickerMouse(term.MouseEvent{X: r.col + 1, Y: r.row + 2, Button: 65, Press: true})
	if u.picker == nil {
		t.Fatal("picker: wheel inside the box must not close")
	}
	u.pickerMouse(out)
	if u.picker != nil {
		t.Fatal("picker: click outside box")
	}

	u = newUI()
	u.themePick = newThemePicker(themeNames, "terminal", th, u.t.Cols, u.t.Rows)
	u.themePick.load = fakeLoad
	tr := u.themeRect()
	u.themeMouse(term.MouseEvent{X: tr.col + 1, Y: tr.row + 1, Button: 64, Press: true})
	if u.themePick == nil {
		t.Fatal("themepick: wheel inside the box must not close")
	}
	u.themeMouse(out)
	if u.themePick != nil {
		t.Fatal("themepick: click outside box")
	}

	u = newUI()
	u.newChat = &newChatBox{}
	nr := u.ncRect()
	u.ncMouse(term.MouseEvent{X: nr.col + 1, Y: nr.row + 1, Button: 64, Press: true})
	if u.newChat == nil {
		t.Fatal("newchat: wheel inside the box must not close")
	}
	u.ncMouse(out)
	if u.newChat != nil {
		t.Fatal("newchat: click outside box")
	}

	u = newUI()
	u.gsearch = &globalSearch{}
	gr := u.gsRect()
	u.gsMouse(term.MouseEvent{X: gr.col + 1, Y: gr.row + 1, Button: 64, Press: true})
	if u.gsearch == nil {
		t.Fatal("gsearch: wheel inside the box must not close")
	}
	u.gsMouse(out)
	if u.gsearch != nil {
		t.Fatal("gsearch: click outside box")
	}

	u = newUI()
	u.menu = &ctxMenu{entries: menuEntries(model.ChatGroup, false, model.AllCaps()), x: 10, y: 5, cur: 1}
	mr := u.menuBox()
	u.menuMouse(term.MouseEvent{X: mr.col + 1, Y: mr.row + 1, Button: 64, Press: true})
	if u.menu == nil || u.menu.cur != 0 {
		t.Fatalf("menu: wheel inside the box, cur = %v", u.menu)
	}
	u.menuMouse(out)
	if u.menu != nil || sideMenuChat != nil {
		t.Fatal("menu: click outside box")
	}
}

// TestImageClickOpensViewer : a left click on an inline image opens the full
// screen preview, like "v" — the desktop viewer (xdg-open) stays on "o" and
// /open.
func TestImageClickOpensViewer(t *testing.T) {
	md := &model.Media{Kind: model.MediaPhoto, Label: "[photo]", Mime: "image/png",
		Path: "/nonexistent.png", State: model.MediaReady, Frames: [][]byte{{1}},
		Loc: &tg.InputPhotoFileLocation{}}
	it := &Item{Msg: &model.Msg{ID: 1, Media: md}}
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{}, th: theme.Terminal(),
		images: "halfblock", events: make(chan model.Event, 4), ctx: context.Background(),
		t: &term.Term{Cols: 80, Rows: 10}}
	u.hits = []rowHit{{item: it, img: &render.Img{Media: md, Cols: 4, Rows: 1}}}

	u.mouse(term.MouseEvent{Button: 0, X: 2, Y: 0, Press: true})

	if u.viewer == nil || u.viewer.src != md {
		t.Fatalf("click on the image: viewer %+v", u.viewer)
	}
}

// hoverUI : a panel, one chat and a message area with a scrollbar — the frame
// the follow-mouse tests read.
func hoverUI() *UI {
	c := &model.Chat{ID: 1, Kind: model.ChatUser, Title: "Alice", LastDate: time.Now()}
	return &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, th: theme.Terminal(),
		cfg: &config.Config{SidebarSort: "recent", Hover: config.HoverMenu},
		t:   &term.Term{Cols: 80, Rows: 10}, side: sideChats, sideW: testSideW, chatList: []*model.Chat{c}}
}

// skin : the two cells the highlight plays on — the │ bar of the panel, with
// its style, and the whole scrollbar column.
func skin(u *UI) string {
	var b strings.Builder
	lines, _ := u.sideBlock(-1)
	for _, l := range lines {
		sp := l.Spans[len(l.Spans)-1]
		b.WriteString(sp.Style.SGR() + sp.Text)
	}
	u.drawScrollbar(&b, u.t.Cols-1, u.viewRows(), 100, 0)
	return b.String()
}

// TestZoneOf : the pointer names one zone per area — panel (its bar included),
// messages, scrollbar column — and nothing at all under the status bar, with
// hover off or under an overlay.
func TestZoneOf(t *testing.T) {
	u := hoverUI() // x0 = 27, message area 27..78, bar 79, 8 message lines
	for _, c := range []struct {
		x, y int
		want zone
	}{
		{0, 0, zoneSide}, {26, 3, zoneSide}, // the │ bar belongs to the panel
		{27, 0, zoneMsgs}, {78, 7, zoneMsgs},
		{79, 0, zoneBar}, {79, 7, zoneBar},
		{79, 8, zoneNone}, {40, 8, zoneNone}, {40, 9, zoneNone}, // separator, status, input
	} {
		if got := u.zoneOf(c.x, c.y); got != c.want {
			t.Fatalf("(%d,%d): %d, want %d", c.x, c.y, got, c.want)
		}
	}
	u.side = sideHidden // no panel: the first column belongs to the messages
	if got := u.zoneOf(0, 0); got != zoneMsgs {
		t.Fatalf("panel hidden: %d", got)
	}
	u.side = sideChats
	u.menu = &ctxMenu{}
	if got := u.zoneOf(2, 2); got != zoneNone {
		t.Fatalf("overlay on top: %d", got)
	}
	u.menu = nil
	u.cfg.Hover = config.HoverOff
	if got := u.zoneOf(2, 2); got != zoneNone {
		t.Fatalf("hover off: %d", got)
	}
}

// TestZoneAt : repaint only on a zone change — ?1003 sends dozens of moves a
// second and each one must not draw a frame.
func TestZoneAt(t *testing.T) {
	u := hoverUI()
	if !u.zoneAt(2, 2) || u.zone != zoneSide {
		t.Fatalf("panel: %d", u.zone)
	}
	if u.zoneAt(5, 6) {
		t.Fatal("same zone: no repaint")
	}
	if !u.zoneAt(40, 2) || u.zone != zoneMsgs {
		t.Fatalf("messages: %d", u.zone)
	}
	if !u.zoneAt(79, 2) || u.zone != zoneBar {
		t.Fatalf("bar column: %d", u.zone)
	}
	if !u.zoneAt(40, 9) || u.zone != zoneNone {
		t.Fatalf("input: %d", u.zone)
	}
}

// TestZoneResyncOnClick : a press reads the zone again. Only the moves feed
// zoneAt, so after a clear() (F2, /set theme, resize) or the release of a drag
// the zone was stale and the frame was drawn with nothing lit until the
// pointer moved again.
func TestZoneResyncOnClick(t *testing.T) {
	u := hoverUI()
	u.clear() // zone dropped, pointer still over the messages
	u.mouse(term.MouseEvent{X: 40, Y: 2, Button: 0, Press: true})
	if u.zone != zoneMsgs {
		t.Fatalf("press over the messages: zone %d", u.zone)
	}
	u.drag = dragBar
	u.zone = zoneNone
	u.mouse(term.MouseEvent{X: 79, Y: 2, Button: 0}) // release ending a bar drag
	if u.zone != zoneBar {
		t.Fatalf("release on the column: zone %d", u.zone)
	}
}

// TestFollowMouseHighlight : the zone under the pointer lights up — over the
// panel its │ bar takes the very colour of a resize drag, over the messages
// the scrollbar column lights up, and on the column itself the cursor goes
// full block, like a grab.
func TestFollowMouseHighlight(t *testing.T) {
	u := hoverUI()
	idle := skin(u)

	u.zone = zoneSide
	side := skin(u)
	if side == idle {
		t.Fatal("pointer over the panel: nothing changed")
	}
	u.zone, u.drag = zoneNone, dragSide
	if skin(u) != side {
		t.Fatal("pointer over the panel: the bar must take the colour of the resize drag")
	}
	u.drag = dragNone

	u.zone = zoneMsgs
	msgs := skin(u)
	if msgs == idle || strings.Contains(msgs, "█") {
		t.Fatalf("pointer over the messages: column lit, cursor kept thin: %q", msgs)
	}

	u.zone = zoneBar
	if bar := skin(u); !strings.Contains(bar, "█") || bar == msgs {
		t.Fatalf("pointer on the column: full block cursor: %q", bar)
	}
}

// TestFollowMouseHoverOff : hover = off, the pointer changes nothing at all —
// the frame is the one of before the feature, byte for byte, wherever it goes.
func TestFollowMouseHoverOff(t *testing.T) {
	u := hoverUI()
	u.cfg.Hover = config.HoverOff
	ref := skin(u)
	for _, p := range [][2]int{{2, 3}, {26, 3}, {40, 2}, {79, 2}, {40, 9}} {
		if u.zoneAt(p[0], p[1]) {
			t.Fatalf("hover off: zone changed at %v", p)
		}
		if got := skin(u); got != ref {
			t.Fatalf("hover off: frame changed at %v", p)
		}
	}
}
