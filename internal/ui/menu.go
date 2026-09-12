package ui

import (
	"slices"
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// Context menu: right click on a chat line of the sidebar (F2), on a member
// of the member box (F3) or on a message, a small box on top anchored at the
// click. While it is open it takes the keyboard and the mouse like the emoji
// picker, and the line aimed at stays highlighted.

type menuEntry struct {
	label, key string
	danger     bool // drawn in the error colour (delete)
}

type ctxMenu struct {
	chat    *model.Chat // target: line of the sidebar…
	member  string      // …or member of the member box (@name or id)…
	item    *Item       // …or a message; win is its window
	win     *Window
	reacts  []string // message: quick reactions on the first row, clickable
	entries []menuEntry
	x, y    int // anchor: the click
	cur     int
}

// menuReacts : quick reactions of the message menu, at most.
const menuReacts = 7

// msgEntries : entries of the menu of a message — its actions (SelActs), the
// edit and the delete moved last, the delete in the error colour.
func msgEntries(acts []render.Act) []menuEntry {
	var es, own []menuEntry
	for _, a := range acts {
		e := menuEntry{label: a.Label, key: string(a.Key), danger: a.Key == 'd'}
		if a.Key == 'e' || a.Key == 'd' {
			own = append(own, e)
		} else {
			es = append(es, e)
		}
	}
	return append(es, own...)
}

// reactRow : the quick reactions drawn on one row, one space between.
func reactRow(reacts []string) string {
	out := make([]string, len(reacts))
	for i, r := range reacts {
		out[i] = render.Truncate(render.CleanLine(r), 2, "")
	}
	return strings.Join(out, " ")
}

// reactAt : the reaction under column x of the row (0 = first cell inside
// the border), "" between two or past the end.
func reactAt(reacts []string, x int) string {
	col := 0
	for _, r := range reacts {
		w := render.Width(render.Truncate(render.CleanLine(r), 2, ""))
		if x >= col && x < col+w {
			return r
		}
		col += w + 1
	}
	return ""
}

// openMsgMenu : right click on a message — its actions, and the quick
// reactions of its chat. The message is selected: the entries are its keys.
func (u *UI) openMsgMenu(it *Item, x, y int) {
	w := u.view()
	m := it.Msg
	if !render.Actionable(m) {
		return
	}
	u.setSel(w, it)
	c := u.chatOf(m)
	caps := u.capsOf(m)
	var reacts []string
	if caps.Reactions {
		reacts = u.allowed(c)
		reacts = reacts[:min(len(reacts), menuReacts)]
	}
	es := msgEntries(render.SelActs(m, u.own(m), w.Search != "", caps))
	if len(es) == 0 && len(reacts) == 0 {
		return
	}
	u.menu = &ctxMenu{item: it, win: w, reacts: reacts, entries: es, x: x, y: y}
}

// menuHover : the pointer over an entry makes it the current one (follow-mouse).
// true when the current entry changed.
func (u *UI) menuHover(x, y int) bool {
	m := u.menu
	r := u.menuBox()
	i := y - r.row - m.head()
	if !r.hits(y, x, 1, 1) || i < 0 || i >= len(m.entries) || i == m.cur {
		return false
	}
	m.cur = i
	return true
}

// head : lines between the top border and the first entry (the reaction row).
func (m *ctxMenu) head() int {
	if len(m.reacts) > 0 {
		return 2
	}
	return 1
}

// menuEntries gives the entries of the target. A member has neither a room
// nor a history of their own; a private chat is not left — only its window closes.
//
// caps is what the network of the target can do: an entry it would refuse is
// not offered at all, rather than asking for a confirmation and then doing
// nothing. Closing a private chat is the exception — it only shuts the window,
// no network is asked anything, so Caps.Leave does not gate it.
func menuEntries(kind model.ChatKind, member bool, caps model.Caps) []menuEntry {
	if member {
		es := []menuEntry{
			{label: i18n.T("menu_private_message"), key: "query"},
			{label: i18n.T("menu_info"), key: "info"},
		}
		if caps.Block {
			es = append(es, menuEntry{label: i18n.T("menu_report_block"), key: "block"})
		}
		return es
	}
	es := make([]menuEntry, 0, 5)
	switch {
	case kind == model.ChatUser:
		es = append(es, menuEntry{label: i18n.T("menu_close_chat"), key: "leave"})
	case caps.Leave:
		es = append(es, menuEntry{label: i18n.T("menu_leave_room"), key: "leave"})
	}
	if caps.Block {
		es = append(es, menuEntry{label: i18n.T("menu_report_block"), key: "block"})
	}
	return append(es,
		menuEntry{label: i18n.T("menu_delete_chat"), key: "delete"},
		menuEntry{label: i18n.T("menu_info"), key: "info"},
		menuEntry{label: i18n.T("menu_search"), key: "search"})
}

// menuRect gives the box of the entries (borders included, width = longest
// label + 2), anchored at the click (x, y) and only moved sideways or up to
// stay on the screen — cols columns, rows lines, status and input left out.
func menuRect(es []menuEntry, reacts []string, x, y, cols, rows int) rect {
	w := render.Width(reactRow(reacts))
	for _, e := range es {
		w = max(w, render.Width(e.label))
	}
	w = min(w+2, max(2, cols)) // floors at 2: the box keeps its borders
	h := min(len(es)+2+min(len(reacts), 1), max(2, rows))
	return rect{row: max(0, min(y, rows-h)), col: max(0, min(x, cols-w)), h: h, w: w}
}

// menuBox gives the open menu, in screen coordinates.
func (u *UI) menuBox() rect {
	return menuRect(u.menu.entries, u.menu.reacts, u.menu.x, u.menu.y, u.t.Cols, u.viewRows())
}

// Lines gives the whole box, one render.Line per screen line; the current
// entry is inverted.
func (m *ctxMenu) Lines(th theme.Theme, w, h int) []render.Line {
	edge := th.Style(theme.Dim)
	on := theme.Style{FG: th.FG, Reverse: true}
	inner := max(0, w-2)
	side := render.Span{Text: "│", Style: edge}
	out := []render.Line{{Spans: []render.Span{{Text: "┌" + strings.Repeat("─", inner) + "┐", Style: edge}}}}
	if len(m.reacts) > 0 {
		out = append(out, render.Line{Spans: []render.Span{side, {Text: padTo(reactRow(m.reacts), inner)}, side}})
	}
	for i, e := range m.entries {
		st := theme.Style{}
		if e.danger {
			st.FG = th.Color(theme.Error)
		}
		if i == m.cur {
			st = on
		}
		out = append(out, render.Line{Spans: []render.Span{side, {Text: padTo(e.label, inner), Style: st}, side}})
	}
	out = append(out, render.Line{Spans: []render.Span{{Text: "└" + strings.Repeat("─", inner) + "┘", Style: edge}}})
	return out[:min(len(out), h)] // never more lines than the rect
}

func (m *ctxMenu) move(d int) { m.cur = max(0, min(m.cur+d, len(m.entries)-1)) }

// openMenu : right click at (x, y) in the sidebar. A line with no chat (window
// 0, empty line) opens nothing.
func (u *UI) openMenu(x, y int) {
	c := u.sideChatAt(y)
	if c == nil {
		return
	}
	u.menu, sideMenuChat = &ctxMenu{chat: c, entries: menuEntries(c.Kind, false, u.caps(c)), x: x, y: y}, c
}

// openMemberMenu : right click on the line line of the member box, q being the
// token of the member (@name or id). The chat of the box is kept with it: an
// event can move the shown window (EvAuthPrompt, EvStopped both goTo(0)) without
// closing the menu, and the token would then be sent to the wrong network.
func (u *UI) openMemberMenu(q string, line, x, y int) {
	c := u.view().Chat
	if c == nil {
		return // no chat left to pin: the token would name no network
	}
	u.parts.mark = line
	u.menu = &ctxMenu{chat: c, member: q, entries: menuEntries(model.ChatUser, true, u.caps(c)), x: x, y: y}
}

// closeMenu closes the menu and gives its line back its normal style.
func (u *UI) closeMenu() {
	u.menu, sideMenuChat = nil, nil
	if u.parts != nil {
		u.parts.mark = -1
	}
}

// menuList : the context menu seen as a list overlay. A click runs the entry
// under the pointer at once, which is why click does not go through cur.
func (u *UI) menuList() listOverlay {
	m := u.menu
	run := func(i int) {
		u.closeMenu()
		u.menuDo(m, m.entries[i].key)
	}
	return listOverlay{r: u.menuBox(), head: m.head(), rows: len(m.entries), n: len(m.entries),
		move: m.move, click: run, enter: func() { run(m.cur) }, close: u.closeMenu}
}

// menuKey : ↑/↓ move, Enter runs, any other key closes. It does not go through
// listOverlay.key: PgUp, Home and the rest must close, not move.
func (u *UI) menuKey(k term.Key) {
	l := u.menuList()
	switch {
	case k.Code == term.Up:
		l.move(-1)
	case k.Code == term.Down:
		l.move(1)
	case k.Code == term.Enter:
		l.enter()
	default:
		l.close()
	}
}

// menuMouse : left click on an entry = action, wheel = move, click outside the
// box = close.
func (u *UI) menuMouse(e term.MouseEvent) {
	m := u.menu
	if r := u.menuBox(); len(m.reacts) > 0 && e.Button == 0 && e.Y == r.row+1 && r.hits(e.Y, e.X, 1, 1) {
		if pick := reactAt(m.reacts, e.X-r.col-1); pick != "" { // reaction row: the emoji under the click
			u.closeMenu()
			u.react(m.item, pick)
		}
		return
	}
	u.menuList().mouse(e)
}

// menuDo runs the entry chosen on the target of the menu, never on the current
// window.
func (u *UI) menuDo(m *ctxMenu, key string) {
	if m.member != "" {
		u.menuMember(m, key)
		return
	}
	if m.item != nil { // message: the entry is the key of the selection
		u.setSel(m.win, m.item)
		u.selKey(m.win, []rune(key)[0])
		return
	}
	c := m.chat
	switch key {
	case "leave":
		if c.Kind == model.ChatUser { // nothing to leave: only the bound window closes
			if i := u.ws.ForChat(c.Key()); i >= 0 {
				u.freeImages(u.ws.List[i])
				u.ws.CloseAt(i) // no goTo: neither read marked nor history loaded again
				u.loadParts()   // the F3 box follows the shown window
			}
			return
		}
		// The network is looked up before the question, not inside the answer:
		// nothing is asked for a chat nobody can act on any more.
		if b := u.net(c); b != nil {
			u.confirm(i18n.T("confirm_leave", render.CleanLine(u.title(c))), func() { b.Leave(u.ctx, c) })
		}
	case "block":
		if b := u.net(c); b != nil {
			u.confirm(i18n.T("confirm_block", render.CleanLine(u.title(c))), func() { b.Block(u.ctx, c) })
		}
	case "delete":
		if b := u.net(c); b != nil {
			u.confirm(i18n.T("confirm_delete", render.CleanLine(u.title(c))), func() { b.DeleteChat(u.ctx, c) })
		}
	case "info":
		u.openChat(c)
		if c.Kind == model.ChatUser {
			u.command("whois", nil, "")
			return
		}
		u.partsOn = true
		u.loadParts()
	case "search":
		if !u.caps(c).Search { // said now rather than at the Enter of a refused /search
			u.netUnsupported(c.Net)
			return
		}
		u.openChat(c)
		u.ed.Set("/search ") // cursor at the end: Enter starts the search
	}
}

// menuMember gives the menu entries of a member of the member box. The token
// is enough for the backend: the peer of a member is already known to the session.
//
// Both calls take a bare token and no chat, so neither fits the chat-bound
// rule; they are not broadcast either — the token comes from the member box
// of m.chat and means nothing anywhere else. They go to that one network,
// the one pinned when the menu opened rather than the one shown now.
func (u *UI) menuMember(m *ctxMenu, key string) {
	q := m.member
	if key == "query" {
		u.openMember(m.chat.Net, q)
		return
	}
	b := u.net(m.chat)
	if b == nil {
		return
	}
	switch key {
	case "info":
		if !u.caps(m.chat).Whois {
			u.netUnsupported(m.chat.Net)
			return
		}
		b.WhoisMember(u.ctx, q)
	case "block":
		u.confirm(i18n.T("confirm_block", render.CleanLine(q)), func() { b.BlockMember(u.ctx, q) })
	}
}

// openChat gives the window of the chat, opened when it does not exist yet.
func (u *UI) openChat(c *model.Chat) {
	if i := u.ws.ForChat(c.Key()); i >= 0 {
		u.goTo(i)
		return
	}
	u.attach(u.ws.New(false), c)
}

// chatGone : chat left, blocked or gone from the account. Bound windows
// closed, entry dropped from the sidebar, cached history erased, and one line
// to say so.
func (u *UI) chatGone(k model.ChatKey) {
	c := u.chats[k]
	if !u.dropChat(k) {
		// Nothing known under that key, and so nothing to say: a member
		// blocked with no private chat open with them, a channel of a kind the
		// sidebar never lists, or one of a guild left that the account could
		// not read. Only the block of a member used to answer here, and it
		// cannot be told apart from the three others.
		return
	}
	u.saveDialogs() // otherwise the cache would bring it back at the next start
	u.goTo(u.ws.Cur)
	u.sys(i18n.T("chat_removed", render.CleanLine(u.title(c))))
}

// dropChat wipes every trace of the chat, without a word: windows closed,
// entry dropped from the sidebar, cached history erased. false when the key is
// unknown — nothing was dropped. The list is NOT written to the disk here: a
// caller dropping several chats writes it once.
func (u *UI) dropChat(k model.ChatKey) bool {
	if u.chats[k] == nil {
		return false
	}
	u.dropTargets(func(c *model.Chat) bool { return c.Key() == k })
	for i := len(u.ws.List) - 1; i > 0; i-- {
		w := u.ws.List[i]
		if w.Chat == nil || w.Chat.Key() != k {
			continue
		}
		u.freeImages(w)
		u.ws.List = slices.Delete(u.ws.List, i, i+1)
		switch {
		case u.ws.Cur == i:
			u.ws.Cur = 0
		case u.ws.Cur > i:
			u.ws.Cur--
		}
	}
	u.chatList = slices.DeleteFunc(u.chatList, func(x *model.Chat) bool { return x.Key() == k })
	// The sync queue keeps pointers: a step on a chat that was left would
	// bring a CHANNEL_PRIVATE into window 0.
	u.syncQueue = slices.DeleteFunc(u.syncQueue, func(x *model.Chat) bool { return x.Key() == k })
	delete(u.chats, k)
	delete(u.dirty, k)
	delete(u.partsCache, k)
	delete(u.typing, k)
	// presence and avatars are keyed by peer: in a private chat the peer wears
	// the id of the chat, which is the only entry this chat owns.
	delete(u.presence, k)
	delete(u.avatars, k)
	delete(u.cached, k)
	u.dropHistory(k)
	return true
}

// dropHistory drops the history file of the chat from the disk cache of its
// network.
func (u *UI) dropHistory(k model.ChatKey) {
	u.bgWait.Wait()
	delete(u.dirty, k)
	if cc := u.cacheFor(k.Net); cc != nil {
		_ = cc.RemoveHistory(k.ID) // nothing to show: the file may not exist
	}
}
