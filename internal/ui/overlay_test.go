package ui

import (
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// Characterization of the four list overlays — theme picker, new chat, global
// search and context menu. Keyboard, wheel and click, as they answer today:
// these tests pin the behaviour, they do not ask for a better one.

// listUI : the frame the four boxes are read in — 80×24, no chat, no network.
// Same hand-built UI as TestOverlayMouseClose (hit_test.go).
func listUI() *UI {
	return &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{},
		t: &term.Term{Cols: 80, Rows: 24}, th: theme.Terminal()}
}

// TestOverlayThemePicker : the six navigation keys, the wheel and a click on a
// name all land on the same current line and each one applies the theme shown.
// Enter keeps it, Esc gives the first one back.
func TestOverlayThemePicker(t *testing.T) {
	u := listUI()
	cfg, err := config.LoadFrom(t.TempDir()) // themeKeep writes the configuration
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	u.cfg = cfg
	open := func() *themePicker {
		u.themePick = newThemePicker(themeNames, "terminal", u.th, u.t.Cols, u.t.Rows)
		u.themePick.load = fakeLoad
		return u.themePick
	}
	p := open()
	for _, c := range []struct {
		k    term.Key
		want int
	}{
		{term.Key{Code: term.Down}, 1},
		{term.Key{Code: term.End}, len(themeNames) - 1},
		{term.Key{Code: term.Up}, len(themeNames) - 2},
		{term.Key{Code: term.PgUp}, 0},
		{term.Key{Code: term.PgDn}, min(p.rows, len(themeNames)-1)},
		{term.Key{Code: term.Home}, 0},
	} {
		u.themeKey(c.k)
		if p.cur != c.want {
			t.Fatalf("%v: cur = %d, want %d", c.k.Code, p.cur, c.want)
		}
	}
	if u.th.Name != themeNames[0] {
		t.Fatalf("move applies the theme: %q", u.th.Name)
	}
	// Wheel over the box: one name per notch, the theme follows.
	r := u.themeRect()
	wheel := func(b int) { u.themeMouse(term.MouseEvent{X: r.col + 1, Y: r.row + 2, Button: b, Press: true}) }
	wheel(65)
	if p.cur != 1 || u.th.Name != themeNames[1] {
		t.Fatalf("wheel down: cur = %d, theme %q", p.cur, u.th.Name)
	}
	wheel(64)
	if p.cur != 0 || u.th.Name != themeNames[0] {
		t.Fatalf("wheel up: cur = %d, theme %q", p.cur, u.th.Name)
	}
	// Click on the third row of the list: it becomes the current one, nothing
	// is kept yet.
	u.themeMouse(term.MouseEvent{X: r.col + 1, Y: r.row + 2 + 2, Button: 0, Press: true})
	if p.cur != p.top()+2 || u.themePick == nil {
		t.Fatalf("click: cur = %d, open = %v", p.cur, u.themePick != nil)
	}
	// Click on the top border, the header and the foot: nothing moves, nothing closes.
	cur := p.cur
	for _, y := range []int{r.row, r.row + 1, r.row + p.height() - 2, r.row + p.height() - 1} {
		u.themeMouse(term.MouseEvent{X: r.col + 1, Y: y, Button: 0, Press: true})
		if u.themePick == nil || p.cur != cur {
			t.Fatalf("click on y=%d: cur = %d, open = %v", y, p.cur, u.themePick != nil)
		}
	}
	// Enter keeps the theme shown, and writes it to the configuration.
	name := u.th.Name
	u.themeKey(term.Key{Code: term.Enter})
	if u.themePick != nil || u.cfg.Theme != name {
		t.Fatalf("enter: open = %v, cfg %q, want %q", u.themePick != nil, u.cfg.Theme, name)
	}
	// Esc gives the theme of the opening back, and saves nothing.
	open()
	u.themeKey(term.Key{Code: term.End})
	u.themeKey(term.Key{Code: term.Esc})
	if u.themePick != nil || u.th.Name != name || u.cfg.Theme != name {
		t.Fatalf("esc: open = %v, theme %q, cfg %q", u.themePick != nil, u.th.Name, u.cfg.Theme)
	}
}

// TestOverlayNewChat : navigation, wheel and click of the new chat overlay.
// The section header carries no chat: it is stepped over by a move and a click
// on it does nothing at all. Enter (or a click on a name) opens the chat.
func TestOverlayNewChat(t *testing.T) {
	u := listUI()
	c1, c2 := &model.Chat{ID: 1, Title: "un"}, &model.Chat{ID: 2, Title: "deux"}
	w := u.ws.New(true) // c2 already has its window: opening it switches there
	w.Chat = c2
	fill := func() *newChatBox {
		u.newChat = &newChatBox{rows: []ncRow{{chat: c1}, {sep: true}, {chat: c2}}}
		return u.newChat
	}
	n := fill()
	r := u.ncRect()
	for _, c := range []struct {
		k    term.Key
		want int
	}{
		{term.Key{Code: term.End}, 2},
		{term.Key{Code: term.Home}, 0},
		{term.Key{Code: term.Down}, 2}, // the header is stepped over
		{term.Key{Code: term.Up}, 0},
		{term.Key{Code: term.PgDn}, 2}, // bounded by the last row
		{term.Key{Code: term.PgUp}, 0},
	} {
		u.ncKey(c.k)
		if n.cur != c.want {
			t.Fatalf("%v: cur = %d, want %d", c.k.Code, n.cur, c.want)
		}
	}
	u.ncMouse(term.MouseEvent{X: r.col + 1, Y: r.row + 1, Button: 65, Press: true})
	if n.cur != 2 {
		t.Fatalf("wheel down: %d", n.cur)
	}
	u.ncMouse(term.MouseEvent{X: r.col + 1, Y: r.row + 1, Button: 64, Press: true})
	if n.cur != 0 {
		t.Fatalf("wheel up: %d", n.cur)
	}
	// Click on the header row, then past the last row: nothing chosen, box open.
	row := func(i int) int { return r.row + 2 + i - n.top(gsRows(r.h)) }
	for _, i := range []int{1, len(n.rows)} {
		u.ncMouse(term.MouseEvent{X: r.col + 1, Y: row(i), Button: 0, Press: true})
		if u.newChat == nil || n.cur != 0 {
			t.Fatalf("click on row %d: open = %v, cur = %d", i, u.newChat != nil, n.cur)
		}
	}
	u.ncKey(term.Key{Code: term.Esc})
	if u.newChat != nil || u.ws.Cur != 0 {
		t.Fatalf("esc: open = %v, window %d", u.newChat != nil, u.ws.Cur)
	}
	// Click on the last name: it is chosen, the box closes and its chat is shown.
	n = fill()
	u.ncMouse(term.MouseEvent{X: r.col + 1, Y: row(2), Button: 0, Press: true})
	if u.newChat != nil || u.ws.Cur != 1 {
		t.Fatalf("click on a name: open = %v, window %d", u.newChat != nil, u.ws.Cur)
	}
	// Enter on the current line: same thing, from the keyboard.
	u.ws.Cur = 0
	n = fill()
	u.ncKey(term.Key{Code: term.End})
	u.ncKey(term.Key{Code: term.Enter})
	if u.newChat != nil || u.ws.Cur != 1 {
		t.Fatalf("enter: open = %v, window %d", u.newChat != nil, u.ws.Cur)
	}
}

// TestOverlayGlobalSearch : navigation (the scroll follows the current line),
// wheel and click of the global search. Enter settles on the message of the
// result; the 3rd Ctrl+F closes the overlay while keeping the local search,
// Esc closes both.
func TestOverlayGlobalSearch(t *testing.T) {
	u := listUI()
	c := &model.Chat{ID: 1, Title: "salon"}
	w := u.ws.New(true)
	w.Chat = c
	it := &Item{Msg: &model.Msg{ID: 42, ChatID: 1}}
	w.Items = []*Item{it}
	hits := make([]model.SearchHit, 30)
	for i := range hits {
		hits[i] = model.SearchHit{Chat: c, MsgID: 42}
	}
	fill := func(n int) *globalSearch {
		u.gsearch, u.search = &globalSearch{hits: hits[:n]}, &searchState{}
		return u.gsearch
	}
	g := fill(len(hits))
	r := u.gsRect()
	rows := gsRows(r.h)
	for _, c := range []struct {
		k         term.Key
		cur, scrl int
	}{
		{term.Key{Code: term.End}, 29, 30 - rows},
		{term.Key{Code: term.PgUp}, 29 - rows, 29 - rows},
		{term.Key{Code: term.Up}, 28 - rows, 28 - rows},
		{term.Key{Code: term.Home}, 0, 0},
		{term.Key{Code: term.Down}, 1, 0},
		{term.Key{Code: term.PgDn}, 1 + rows, 2},
	} {
		u.gsKey(c.k)
		if g.cur != c.cur || g.scroll != c.scrl {
			t.Fatalf("%v: cur = %d (want %d), scroll = %d (want %d)", c.k.Code, g.cur, c.cur, g.scroll, c.scrl)
		}
	}
	u.gsMouse(term.MouseEvent{X: r.col + 1, Y: r.row + 1, Button: 65, Press: true})
	if g.cur != 2+rows || g.scroll != 3 {
		t.Fatalf("wheel down: cur = %d, scroll = %d", g.cur, g.scroll)
	}
	u.gsMouse(term.MouseEvent{X: r.col + 1, Y: r.row + 1, Button: 64, Press: true})
	if g.cur != 1+rows || g.scroll != 3 {
		t.Fatalf("wheel up: cur = %d, scroll = %d", g.cur, g.scroll)
	}
	// Click past the last result: nothing is opened, the box stays.
	fill(2)
	u.gsMouse(term.MouseEvent{X: r.col + 1, Y: r.row + 2 + 2, Button: 0, Press: true})
	if u.gsearch == nil || u.search == nil {
		t.Fatalf("click on an empty row: gsearch = %v, search = %v", u.gsearch != nil, u.search != nil)
	}
	// 3rd Ctrl+F: the overlay goes, the local search stays.
	u.gsKey(term.Key{Code: term.Ctrl, Rune: 'f'})
	if u.gsearch != nil || u.search == nil {
		t.Fatalf("ctrl+f: gsearch = %v, search = %v", u.gsearch != nil, u.search != nil)
	}
	// Esc: both go.
	fill(2)
	u.gsKey(term.Key{Code: term.Esc})
	if u.gsearch != nil || u.search != nil {
		t.Fatalf("esc: gsearch = %v, search = %v", u.gsearch != nil, u.search != nil)
	}
	// Click on the second result: the box closes, the local search too, and the
	// window of the chat is shown on the message.
	fill(2)
	u.gsMouse(term.MouseEvent{X: r.col + 1, Y: r.row + 2 + 1, Button: 0, Press: true})
	if u.gsearch != nil || u.search != nil || u.ws.Cur != 1 || w.Sel != it {
		t.Fatalf("click on a result: gsearch = %v, window %d, sel %v", u.gsearch != nil, u.ws.Cur, w.Sel)
	}
	// Enter on the current line: same thing, from the keyboard.
	u.ws.Cur, w.Sel = 0, nil
	fill(2)
	u.gsKey(term.Key{Code: term.Enter})
	if u.gsearch != nil || u.ws.Cur != 1 || w.Sel != it {
		t.Fatalf("enter: gsearch = %v, window %d, sel %v", u.gsearch != nil, u.ws.Cur, w.Sel)
	}
}

// TestOverlayMenu : ↑/↓ move, Enter runs the current entry, any other key
// closes — a click runs the entry under the pointer, whatever the current one.
func TestOverlayMenu(t *testing.T) {
	u := listUI()
	chat := &model.Chat{Net: model.NetTelegram, ID: 1, Title: "salon", Kind: model.ChatGroup}
	entries := menuEntries(model.ChatGroup, false, model.AllCaps())
	fill := func(cur int) *ctxMenu {
		u.menu, sideMenuChat = &ctxMenu{chat: chat, entries: entries, x: 10, y: 5, cur: cur}, chat
		return u.menu
	}
	// "search" is the last entry: with no network behind the chat it only says
	// so — an action that needs neither backend nor confirmation.
	last := len(entries) - 1
	said := func() bool {
		its := u.view().Items
		return len(its) > 0 && strings.Contains(its[len(its)-1].Sys, i18n.T("net_unsupported", model.NetTelegram))
	}
	m := fill(0)
	u.menuKey(term.Key{Code: term.Down})
	u.menuKey(term.Key{Code: term.Down})
	if m.cur != 2 {
		t.Fatalf("down: %d", m.cur)
	}
	u.menuKey(term.Key{Code: term.Up})
	if m.cur != 1 {
		t.Fatalf("up: %d", m.cur)
	}
	// PgUp is not a move here: it closes, like any key that is not ↑/↓/Enter.
	u.menuKey(term.Key{Code: term.PgUp})
	if u.menu != nil || sideMenuChat != nil || said() {
		t.Fatalf("pgup must close the menu with no action: menu = %v", u.menu != nil)
	}
	// Enter runs the current entry and closes.
	fill(last)
	u.menuKey(term.Key{Code: term.Enter})
	if u.menu != nil || sideMenuChat != nil || !said() {
		t.Fatalf("enter: menu = %v, action run = %v", u.menu != nil, said())
	}
	// Click on the top border: nothing runs, the menu stays.
	m = fill(0)
	r := u.menuBox()
	u.menuMouse(term.MouseEvent{X: r.col + 1, Y: r.row, Button: 0, Press: true})
	if u.menu == nil || m.cur != 0 {
		t.Fatalf("click on the border: menu = %v, cur = %d", u.menu != nil, m.cur)
	}
	// Wheel: one entry per notch, bounded.
	u.menuMouse(term.MouseEvent{X: r.col + 1, Y: r.row + 1, Button: 65, Press: true})
	if m.cur != 1 {
		t.Fatalf("wheel down: %d", m.cur)
	}
	u.menuMouse(term.MouseEvent{X: r.col + 1, Y: r.row + 1, Button: 64, Press: true})
	if m.cur != 0 {
		t.Fatalf("wheel up: %d", m.cur)
	}
	// Click on the last entry: it runs, whatever the current one is, and the
	// menu closes.
	u.view().Items = nil
	u.menuMouse(term.MouseEvent{X: r.col + 1, Y: r.row + 1 + last, Button: 0, Press: true})
	if u.menu != nil || sideMenuChat != nil || !said() {
		t.Fatalf("click on an entry: menu = %v, action run = %v", u.menu != nil, said())
	}
}
