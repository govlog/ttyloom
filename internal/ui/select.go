package ui

import (
	"cmp"
	"encoding/base64"
	"slices"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/emoji"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

// Selection of a message: keyboard move, key palette.

// setSel changes the selection of w and invalidates the two drawings it
// touches (the background and the marker are not part of the cache key).
func (u *UI) setSel(w *Window, it *Item) {
	u.clearInfo(w)
	if w.Sel == it {
		return
	}
	w.Sel.Invalidate()
	it.Invalidate()
	w.Sel = it
	u.selShow = u.selShow || it != nil
}

// selMove : Alt+↑ / Alt+↓.
func (u *UI) selMove(w *Window, dir int) {
	u.clearInfo(w)
	old := w.Sel
	if w.SelectNext(dir, u.onScreen) {
		old.Invalidate()
		w.Sel.Invalidate()
		u.selShow = true
	}
}

// onScreen tells whether the item is drawn in the view as it was at the last draw.
func (u *UI) onScreen(it *Item) bool {
	for _, r := range u.hits {
		if r.item == it {
			return true
		}
	}
	return false
}

// selKey handles a printable key while a message is selected. true: key taken
// (kept for the palette).
func (u *UI) selKey(w *Window, r rune) bool {
	it := w.Sel
	if it == nil || it.Msg == nil || u.edit != nil || u.reply != nil || u.sendAsk != nil {
		return false // while editing, replying or captioning, everything goes to the input
	}
	m := it.Msg
	// Same conditions as the entries of its menu (render.SelActs): a key that
	// is not one of them is not kept.
	act := render.Actionable(m)
	switch {
	case r == 'o' && act && render.Openable(m.Media):
		u.openItemMedia(w, it)
	case r == 'v' && act && m.Media != nil:
		u.viewMsg(m)
	case r == 'l' && act && render.Playable(m):
		u.playPause(m.Media, 0, 0, 0) // box of the message
	case r == 's' && act && render.Stoppable(m.Media):
		u.stopVideo(m.Media)
	case r == 'e' && act && u.own(m):
		u.startEdit(it)
	case r == 'd' && act && u.own(m) && u.capsOf(m).Delete:
		u.confirm(i18n.T("confirm_delete_message", m.ID), func() {
			c := u.chatOf(m)
			if b := u.net(c); b != nil {
				b.Delete(u.backendContext(b), c, m.ID)
			}
		})
	case r == 'p' && act:
		u.reply = it
	case r == 'f' && act && u.capsOf(m).Forward:
		u.openForward(m)
	case r == 'c' && act:
		u.copyText(msgText(m)) // same path as the drag: OSC 52 + status
	case r == 'i' && act:
		c := u.chatOf(m)
		if b := u.net(c); b != nil {
			b.Info(u.backendContext(b), c, m.ID, c.ReadOutboxMaxID, c.ReadInboxMaxID)
		}
	case r == 'r' && act && u.capsOf(m).Reactions:
		u.openReactPicker(it)
	case r == 'n' && act && u.capsOf(m).Pin:
		c := u.chatOf(m)
		if b := u.net(c); b != nil {
			b.Pin(u.backendContext(b), c, m.ID, !m.Pinned)
		}
	case r == 'g' && w.Search != "" && m.ID != 0:
		u.jumpTo(u.chatOf(m), m.ID) // search result: join the chat
	case r == 'g' && m.Reply != nil && m.Reply.ID != 0:
		u.jumpTo(u.chatOf(m), m.Reply.ID)
	default:
		u.setSel(w, nil) // any other key: the input takes the lead back
		return false
	}
	return true
}

// actClick : left click on an action — palette of the selected message or
// reaction. Same path as the matching key.
func (u *UI) actClick(w *Window, it *Item, a render.Action) {
	switch {
	case a.Key == render.KeyReact:
		if it != nil && it.Msg != nil {
			u.react(it, a.Emoji)
		}
	case a.Key == render.KeyJump:
		if it != nil && it.Msg != nil {
			u.jumpTo(u.chatOf(it.Msg), a.ID)
		}
	case a.Key == render.KeyView: // label of a media: the preview, whatever the mode
		if it != nil && it.Msg != nil {
			u.viewMsg(it.Msg)
		}
	case it != nil:
		u.setSel(w, it) // the palette acts on the message clicked
		u.selKey(w, a.Key)
	}
}

// --- selection drag: copy of text to the clipboard ---

// selPos : a cell of the message area, held by the line of an item and not by
// the screen: a scroll or a message that comes during the drag leaves the
// range on its text.
type selPos struct {
	item *Item
	row  int // rank of the line in the drawing of the item
	col  int // column in the message area
}

// selAt : a cell in the lines of the window (Visible) of the frame.
type selAt struct{ line, col int }

// at : the cell in the lines of the frame; false when the item is no longer
// drawn (trim, /clear).
func (p selPos) at(items []*Item) (selAt, bool) {
	first, last := blockOf(items, p.item)
	if first < 0 {
		return selAt{}, false
	}
	return selAt{min(first+p.row, last), p.col}, true
}

// selCancel : end of a drag with no copy (new press, lost release).
func (u *UI) selCancel() {
	u.drag, u.selA, u.selB = dragNone, selPos{}, selPos{}
}

// selDragTo : the cell under the pointer becomes the end of the range. As long
// as the pointer stays on the cell of the press, it is still a click (selB
// unset). Under the message area, the last row; on a row with no item (day
// separator, empty rows), the end stays where it was. true: the range
// changed, the screen must be drawn again.
func (u *UI) selDragTo(m term.MouseEvent) bool {
	u.selX, u.selY = m.X, m.Y
	x0, _ := u.layout()
	y := min(max(m.Y, 0), len(u.hits)-1)
	if y < 0 || u.hits[y].item == nil {
		return false
	}
	p := selPos{u.hits[y].item, u.hits[y].sub, max(m.X-x0, 0)}
	if p == u.selB || (u.selB.item == nil && p == u.selA) {
		return false
	}
	u.selB = p
	return true
}

// selEdge : while a selection drag holds the pointer on the first or the last
// row of the message area (or under it), each tick scrolls the view one line
// that way as long as there is buffer on that side — at the top, scroll()
// loads the older history like PgUp — and the end of the range follows the
// line that comes under the pointer. true: the view moved.
func (u *UI) selEdge() bool {
	view := u.viewRows()
	d := 0
	switch {
	case u.drag != dragText || u.selB.item == nil || view < 1:
		return false // no drag, or still a click
	case u.selY <= 0:
		d = 1
	case u.selY >= view-1:
		d = -1
	default:
		return false
	}
	w := u.view()
	old := w.Scroll
	u.scroll(w, d)
	if w.Scroll == old {
		return false // nothing more on that side
	}
	// The line the next frame draws under the pointer: draw() cuts the same way.
	lines, items := w.Visible(u.opts())
	start, end := viewSlice(lines, w.Scroll, view)
	if i := start + min(max(u.selY, 0), view-1); i < end && items[i] != nil { // a day separator: the end waits for the next line
		x0, _ := u.layout()
		u.selB = selPos{items[i], lineRank(items, i), max(u.selX-x0, 0)}
	}
	return true
}

// lineRank : rank of line i in the drawing of its item.
func lineRank(items []*Item, i int) int {
	k := i
	for k > 0 && items[k-1] == items[i] {
		k--
	}
	return i - k
}

// selRelease : release of a selection drag. With no move it is a click on a
// message (selection, or toggle on a second click); otherwise the text of the
// range goes to the clipboard.
func (u *UI) selRelease() {
	anchor, moved := u.selA.item, u.selB.item != nil
	if moved {
		u.copyText(u.selText())
	}
	u.selCancel()
	if moved || anchor == nil || !selectable(anchor) {
		return
	}
	w := u.view()
	if anchor.Msg.Deleted { // deleted: the click shows its content, the next one hides it
		anchor.Reveal, anchor.lines = !anchor.Reveal, nil
	}
	if w.Sel == anchor {
		anchor = nil // second click on the selected message: toggle
	}
	u.setSel(w, anchor)
}

// selBounds : the two ends of the drag in the lines of the window, in reading
// order. ok false: no drag past a click, or an end no longer drawn.
func (u *UI) selBounds(items []*Item) (lo, hi selAt, ok bool) {
	if u.selB.item == nil {
		return lo, hi, false
	}
	lo, okA := u.selA.at(items)
	hi, okB := u.selB.at(items)
	if !okA || !okB {
		return lo, hi, false
	}
	if hi.line < lo.line || (hi.line == lo.line && hi.col < lo.col) {
		lo, hi = hi, lo
	}
	return lo, hi, true
}

// selCols : the cells of line i that the range [lo, hi] covers, [c0, c1), on
// the text of the line alone: never the prefix (time, nick) nor the indent.
// ok false: the line is out of the range or holds no text (image, reactions,
// separator). c0 >= c1 on a text line the range only crosses (empty line,
// pointer in the indent).
func selCols(l render.Line, i int, lo, hi selAt) (c0, c1 int, ok bool) {
	if !l.Text || i < lo.line || i > hi.line {
		return 0, 0, false
	}
	c0, c1 = l.TextCol, l.TextCol+l.TextW
	if i == lo.line {
		c0 = max(c0, lo.col)
	}
	if i == hi.line {
		c1 = min(c1, hi.col+1)
	}
	return c0, c1, true
}

// selText : the text of the range as drawn. A break between two items or two
// lines of a message stays; the lines of one paragraph cut by the wrap join
// back with the space they lost (none inside a cut word).
func (u *UI) selText() string {
	lines, items := u.view().Visible(u.opts())
	lo, hi, ok := u.selBounds(items)
	if !ok {
		return ""
	}
	var b strings.Builder
	var prev *Item
	for i := lo.line; i <= hi.line; i++ {
		c0, c1, ok := selCols(lines[i], i, lo, hi)
		if !ok {
			continue
		}
		switch {
		case prev == nil: // first line
		case items[i] != prev:
			b.WriteByte('\n')
		default:
			b.WriteString(lines[i].Join)
		}
		prev = items[i]
		if c0 < c1 {
			_, mid, _ := render.Cut(lines[i], c0, c1)
			for _, sp := range mid {
				b.WriteString(sp.Text)
			}
		}
	}
	return b.String()
}

// msgText : what "c" copies — the text of the message, or the label of its
// media when it has no text.
func msgText(m *model.Msg) string {
	if m.Text == "" && m.Media != nil {
		return m.Media.Label
	}
	return m.Text
}

// copyText copies s to the clipboard of the terminal (OSC 52), with a note in
// the status bar. Nothing when s is empty.
func (u *UI) copyText(s string) {
	if s == "" {
		return
	}
	// base64 protects the terminal, not the clipboard: without Clean the
	// control characters of a remote message land there as they are, and a
	// later paste in a shell with no bracketed paste runs the line. The breaks
	// stay, hence Clean and not CleanLine.
	s = render.Clean(s)
	msg := i18n.T("copied")
	if len(s) > maxOSC52 {
		msg += i18n.T("copied_truncated")
	}
	u.t.WriteString(osc52(s)) // the buffer goes out with the frame that follows
	u.flash(msg)
}

// maxOSC52 : cap of the copied text. base64 grows it by a third: the sequence
// sent to the terminal stays under a megabyte.
const maxOSC52 = (1 << 20) * 3 / 4

// osc52 : clipboard copy sequence of the terminal, in one single block.
func osc52(s string) string {
	if len(s) > maxOSC52 {
		s = strings.ToValidUTF8(s[:maxOSC52], "") // never a rune cut in two
	}
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(s)) + "\a"
}

// info : information lines put under the message (key "i").
func (u *UI) info(e model.EvInfo) {
	w := u.view()
	u.clearInfo(w)
	key := u.evKey(e.ChatID)
	for i, it := range w.Items {
		if it.Msg == nil || it.Msg.ID != e.ID || it.Msg.Key() != key {
			continue
		}
		items := make([]*Item, 0, len(e.Lines))
		for _, l := range e.Lines {
			items = append(items, &Item{Sys: l, Info: true})
		}
		w.Items = slices.Insert(w.Items, i+1, items...)
		return
	}
}

// clearInfo : the information lines survive neither Esc nor the next
// selection. Never in the cache: it keeps only the messages.
func (u *UI) clearInfo(w *Window) {
	w.Items = slices.DeleteFunc(w.Items, func(it *Item) bool { return it.Info })
}

// readOutbox : my messages up to maxID are read; the ticks must be made again
// in every view of the chat. The chat is resolved by the caller: the event
// names it by a bare id, which alone tells the networks apart no longer.
func (u *UI) readOutbox(c *model.Chat, maxID int) {
	if c == nil || maxID <= c.ReadOutboxMaxID {
		return
	}
	c.ReadOutboxMaxID = maxID
	for _, w := range u.viewsOf(c.Key()) {
		for _, it := range w.Items {
			if it.Msg != nil && it.Msg.Key() == c.Key() && it.Msg.Out {
				it.lines = nil
			}
		}
	}
}

// readInbox : my reads up to maxID; the ticks of the received messages
// (someone else) must be made again in every view of the chat. Unread follows
// the unread count as soon as hasUnread is true, even with maxID unchanged —
// another client may have read without moving MaxID on our side. Like
// readOutbox, the chat comes resolved from the caller.
func (u *UI) readInbox(c *model.Chat, maxID, unread int, hasUnread bool) {
	if c == nil || maxID < c.ReadInboxMaxID {
		return
	}
	if maxID > c.ReadInboxMaxID {
		c.ReadInboxMaxID = maxID
		for _, w := range u.viewsOf(c.Key()) {
			for _, it := range w.Items {
				if it.Msg != nil && it.Msg.Key() == c.Key() && !it.Msg.Out {
					it.lines = nil
				}
			}
		}
	}
	if hasUnread {
		c.Unread = unread
	}
}

// startEdit : the input takes the text of the message, the prompt turns to "✎".
// A network with no edit says so and takes nothing: single gate of the "e" key
// and of the ↑ on an empty input.
func (u *UI) startEdit(it *Item) {
	if !u.capsOf(it.Msg).Edit {
		u.netUnsupported(it.Msg.Net)
		return
	}
	u.edit, u.reply = it, nil
	u.ed.Set(it.Msg.Text)
	if u.multilineOn() && strings.Contains(it.Msg.Text, "\n") {
		u.multi = true // a message with line breaks comes back in the expanded editor
	}
}

// editLast : ↑ on an empty input, outside any mode and outside a selection,
// edits my last message. false: the key stays the input history.
func (u *UI) editLast(w *Window) bool {
	if u.ed.String() != "" || w.Sel != nil || u.edit != nil || u.reply != nil || u.queryPending(w) != "" {
		return false
	}
	if w.Target != nil {
		w = u.winFor(w.Target)
	}
	it := lastOwn(w, u.own)
	if it == nil || !u.capsOf(it.Msg).Edit {
		return false // no edit on that network: ↑ goes back to the input history
	}
	u.setSel(w, it)
	u.startEdit(it)
	return true
}

// cancelMode : Esc, window change — it leaves edit, reply, confirmation and
// search, and empties the input that carried the text of the mode. The search
// works on the lines of the shown window: it does not survive a view change.
// The correction box points into the input: it closes with it.
func (u *UI) cancelMode() {
	if u.edit != nil || u.reply != nil {
		u.ed.Set("")
	}
	u.edit, u.reply, u.ask, u.search, u.spellFix = nil, nil, nil, nil, nil
	u.multi = false
	u.selCancel()
	u.cancelSend()
}

// confirm : closed question asked in the input line; do runs on "y".
type confirm struct {
	q  string
	do func()
	at time.Time // when the question opened
}

func (u *UI) confirm(q string, do func()) { u.ask = &confirm{q: q, do: do, at: time.Now()} }

// askKey : answer to a confirmation. It swallows every key, like pasteKey: y
// (or Y) confirms, everything else cancels — the prompt never stays stuck. A
// y within 300 ms of the question is typing, not an answer ("dy…" over one of
// my selected messages): it cancels too.
func (u *UI) askKey(k term.Key) bool {
	if k.Code == term.Mouse {
		return true // click, wheel: swallowed without touching the confirmation
	}
	a := u.ask
	u.ask = nil
	if answeredYes(k, a.at) {
		a.do()
	}
	return true
}

// answeredYes : k confirms a question asked at at — y or Y, 300 ms or more
// after it; sooner, it is typing, not an answer.
func answeredYes(k term.Key, at time.Time) bool {
	return k.Code == term.None && (k.Rune == 'y' || k.Rune == 'Y') && time.Since(at) >= 300*time.Millisecond
}

// applyEdit sends the edit; an empty text = cancel.
func (u *UI) applyEdit(w *Window, it *Item, text string) {
	m := it.Msg
	c := u.chatOf(m)
	if text == "" || c == nil {
		return // empty text: cancel
	}
	if !render.Actionable(m) {
		w.AddSys(i18n.T("edit_message_gone"))
		return
	}
	b := u.net(c)
	if b == nil {
		return
	}
	m.Pending, m.Err, it.lines = true, "", nil
	u.touchShared(m)
	// ponytail: only one edit followed at a time; two edits in flight would give
	// the wrong text back to the input on a failure.
	// ponytail: an edit with fences that fails gives the stripped text back to
	// the input, the fences lost; rare enough to leave as is.
	u.editText = text
	if segs := parseDraft(text); segs != nil { // Ctrl+B/I/U runs, or fences
		u.editText = fenceText(segs)
		b.EditStyled(u.backendContext(b), c, m.ID, segs)
		return
	}
	b.Edit(u.backendContext(b), c, m.ID, text)
}

// edited : edit receipt. Failure → "[failed: …]" under the message and the
// input takes the text back to fix it.
func (u *UI) edited(e model.EvEdited) {
	// Every view of the chat, not only its window: a /search result older than
	// the loaded history has its own copy of the message, which would stay
	// "pending" for ever.
	view := u.view()
	key := u.evKey(e.ChatID)
	var cur *Item // copy shown, to take the edit up again on a failure
	found := false
	for _, w := range u.viewsOf(key) {
		for _, it := range w.Items {
			if it.Msg == nil || it.Msg.ID != e.ID || it.Msg.Key() != key {
				continue
			}
			it.Msg.Pending, it.Msg.Err, it.lines = false, e.Err, nil
			it.Msg.LiveAt = time.Now()
			found = true
			if w == view {
				cur = it
			}
		}
	}
	if !found {
		return
	}
	u.markDirty(key)
	if e.Err != "" && cur != nil && u.edit == nil && u.reply == nil && u.ed.String() == "" {
		u.setSel(view, cur)
		u.edit = cur
		u.ed.Set(u.editText)
	}
}

// openReactPicker : picker limited to the reactions the chat allows. it is
// captured: the choice comes later and the view may have changed meanwhile
// (chat lookup, login prompt, fatal error).
func (u *UI) openReactPicker(it *Item) {
	c := u.chatOf(it.Msg)
	if u.caps(c).AnyReaction { // the whole table, with the search
		u.openPicker(c, func(e string) { u.react(it, e) })
		return
	}
	list := u.allowed(c)
	if len(list) == 0 {
		u.flash(i18n.T("no_reaction_allowed"))
		return
	}
	u.picker = newReactPicker(min(60, u.t.Cols-4), min(14, u.t.Rows-4), list,
		func(e string) { u.react(it, e) })
}

// baseAll gives the list normalised (no variation selector), the form Telegram
// takes — every emoji comparison is made on that.
func baseAll(list []string) []string {
	if list == nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, emoji.Base(e))
	}
	return out
}

// defaultReactions : the fallback list, normalised once — what a network that
// never sent its own list offers.
var defaultReactions = baseAll(model.DefaultReactions)

// reactionsOf : reactions our account can use on net — the list the network
// gave (EvReactionsList), else the default one.
func (u *UI) reactionsOf(net string) []string {
	if l, ok := u.reactList[net]; ok {
		return l
	}
	return defaultReactions
}

// allowed : reactions usable in chat — the list of its network, cut by the
// restriction of the chat. Nil chat (window 0, aggregated view): the list of
// the only network up when there is one, else the default one; the restriction
// per chat is caught at click time by react().
func (u *UI) allowed(c *model.Chat) []string {
	if c != nil {
		return allowedReactions(u.reactionsOf(c.Net), c)
	}
	if len(u.reactList) == 1 {
		for _, l := range u.reactList {
			return l
		}
	}
	return defaultReactions
}

// allowedReactions gives the reactions usable in chat, in Telegram order.
// chat.Reactions nil = no known restriction → the global list; empty non-nil =
// none. The variation selector is ignored on both sides.
func allowedReactions(global []string, chat *model.Chat) []string {
	if chat == nil || chat.Reactions == nil {
		return global
	}
	ok := make(map[string]bool, len(chat.Reactions))
	for _, e := range chat.Reactions {
		ok[emoji.Base(e)] = true
	}
	out := make([]string, 0, len(chat.Reactions))
	for _, e := range global {
		if ok[emoji.Base(e)] {
			out = append(out, e)
		}
	}
	return out
}

// reactDouble : reaction of the double click — 👍 when allowed, else the first
// allowed one, else a status line (same message as the empty picker).
func (u *UI) reactDouble(it *Item) {
	list := u.allowed(u.chatOf(it.Msg))
	if len(list) == 0 {
		u.flash(i18n.T("no_reaction_allowed"))
		return
	}
	pick := "👍"
	if !slices.Contains(list, pick) {
		pick = list[0]
	}
	u.react(it, pick)
}

// react toggles my reaction pick on it (it is dropped when it is already mine).
func (u *UI) react(it *Item, pick string) {
	c := u.chatOf(it.Msg)
	// Single gate of the reactions: the click on a reaction, the double click
	// and the picker all end here — inert on a network that has none.
	if c == nil || !u.caps(c).Reactions {
		return
	}
	next := nextReaction(it.Msg.Reactions, pick)
	// A drop always goes through; setting a reaction outside the list (custom
	// of somebody else, restricted chat) would be refused by the server.
	if next != "" && !u.caps(c).AnyReaction && !slices.Contains(u.allowed(c), emoji.Base(next)) {
		u.reactFailed(c.Key(), model.EvReactionFailed{ChatID: c.ID, ID: it.Msg.ID,
			Emoji: next, Reason: i18n.T("reaction_not_available")})
		return
	}
	// After the local refusal: a reaction outside the list is told in the
	// window whether a backend is there or not.
	if b := u.net(c); b != nil {
		b.React(u.backendContext(b), c, it.Msg.ID, next)
	}
}

// reactFailed : refused reaction, in the window of the chat — the log alone
// makes one believe the click did nothing. The key comes from the caller: a
// local refusal knows its chat, only the event handler has to go through evKey.
func (u *UI) reactFailed(k model.ChatKey, e model.EvReactionFailed) {
	msg := e.Reason
	if e.Emoji != "" {
		msg = i18n.T("reaction_failed_emoji", e.Reason, e.Emoji)
	}
	w := u.view()
	if i := u.ws.ForChat(k); i >= 0 {
		w = u.ws.List[i]
	}
	w.AddSys(msg)
}

// nextReaction gives the emoji to send for pick, "" to drop it when it is already mine.
func nextReaction(rs []model.Reaction, pick string) string {
	for _, r := range rs {
		if r.Emoji == pick && r.Mine {
			return ""
		}
	}
	return pick
}

// reactions : EvReactions replaces the reactions of the message and invalidates its drawing.
func (u *UI) reactions(e model.EvReactions) {
	key := u.evKey(e.ChatID)
	found, mine, text := false, true, "" // a message not loaded may be mine: read to be safe
	for _, w := range u.viewsOf(key) {   // same as edited: each copy of the message
		for _, it := range w.Items {
			if it.Msg != nil && it.Msg.ID == e.ID && it.Msg.Key() == key {
				// A re-read does not empty a list already filled: only an update
				// (or a re-read that really sees zero reaction) has the say to
				// wipe it.
				if e.Refetch && len(e.Reactions) == 0 && len(it.Msg.Reactions) > 0 {
					continue
				}
				it.Msg.Reactions, it.lines = e.Reactions, nil
				it.Msg.LiveAt = time.Now()
				found, mine, text = true, it.Msg.Out, it.Msg.Summary()
			}
		}
	}
	if found {
		u.markDirty(key)
	}
	if e.New != "" {
		u.noteReaction(key, e, text)
	}
	if mine && !e.Refetch && len(e.Reactions) > 0 {
		u.reactedTo(key)
	}
}

// genericReaction : the badge of a chat whose network says it holds unread
// reactions to my messages without saying which (the dialog list).
const genericReaction = "👍"

// reactExcerpt : the length of my message quoted by the notification of a
// reaction, in cells.
const reactExcerpt = 60

// noteReaction : someone else reacted to one of my messages (e.New, by e.By).
// The chat keeps the reaction until its window is looked at — sidebar,
// [Act: …], and the visit shows the message — and away from it a
// notification goes, with no bell: a group answers with many. Once per
// reaction: the server repeats the unread ones in its later updates.
func (u *UI) noteReaction(key model.ChatKey, e model.EvReactions, text string) {
	c := u.chats[key]
	if c == nil || (c.LastReaction == e.New && c.ReactedMsg == e.ID) {
		return
	}
	c.LastReaction, c.ReactedMsg = e.New, e.ID
	body := e.New
	if text != "" {
		body += " « " + render.Truncate(render.CleanLine(text), reactExcerpt, "…") + " »"
	}
	if u.alertNext == nil || u.alertNext.quiet { // a message waiting keeps its bell
		u.alertNext = &alertNote{chat: c, quiet: true, msg: model.Msg{Net: key.Net, ChatID: key.ID, From: cmp.Or(e.By, "?"), Text: body}}
	}
	u.flushAlert(time.Now())
}

// reactBadge : the reaction of c not seen yet, on two cells; "" with none. A
// custom emoji (":name:", no glyph on a terminal) shows as ⭐, as Telegram's.
func reactBadge(c *model.Chat) string {
	e := c.LastReaction
	if e == "" {
		return ""
	}
	if strings.HasPrefix(e, ":") {
		e = "⭐"
	}
	return emojiCell(e)
}

// reactedTo : a reaction to one of my messages stays unread on the server (the
// badge of the phone) until read — now when its window is the one shown and
// the terminal has the focus, at the next visit otherwise, both through markRead.
func (u *UI) reactedTo(key model.ChatKey) {
	c := u.chats[key]
	if c == nil {
		return
	}
	c.UnreadReactions = true
	if i := u.ws.ForChat(key); i >= 0 && u.ws.List[i] == u.view() {
		u.markRead(u.ws.List[i])
	}
}

// lastOwn gives my last message that can still be changed. own is handed over
// rather than an id: a window of the aggregate mixes networks, and my id is
// not the same on each of them.
func lastOwn(w *Window, own func(*model.Msg) bool) *Item {
	for i := len(w.Items) - 1; i >= 0; i-- {
		if it := w.Items[i]; editable(w, it, own) {
			return it
		}
	}
	return nil
}

// editable : a message of mine that can still be changed, and that is drawn.
func editable(w *Window, it *Item, own func(*model.Msg) bool) bool {
	return selectable(it) && w.shown(it) && render.Actionable(it.Msg) && own(it.Msg)
}

// editStep : Ctrl+↑ / Ctrl+↓ — walks my messages in edit mode. From an empty
// input, Ctrl+↑ edits the last one; each Ctrl+↑ then goes one message of mine
// up, Ctrl+↓ one down, and past the newest the edit is left, input emptied. A
// draft in the input is never replaced: Ctrl+↑ then does nothing.
func (u *UI) editStep(w *Window, d int) {
	if w.Target != nil {
		w = u.winFor(w.Target)
	}
	if u.edit == nil {
		if d < 0 && u.ed.String() == "" && u.reply == nil {
			if it := lastOwn(w, u.own); it != nil {
				u.setSel(w, it)
				u.startEdit(it)
			}
		}
		return
	}
	i := slices.Index(w.Items, u.edit)
	for j := i + d; i >= 0 && j >= 0 && j < len(w.Items); j += d {
		if it := w.Items[j]; editable(w, it, u.own) {
			u.setSel(w, it)
			u.startEdit(it)
			return
		}
	}
	if d > 0 { // past my newest message: back to a plain input
		u.cancelMode()
		u.setSel(w, nil)
	}
}

// quoteOf gives the local quote of the message being replied to, while waiting
// for the one of the server (one line, 80 runes).
func quoteOf(it *Item) model.Quote {
	if it == nil || it.Msg == nil {
		return model.Quote{}
	}
	m := it.Msg
	text := m.Text
	if text == "" && m.Media != nil {
		text = m.Media.Label
	}
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > 80 {
		text = string(r[:80]) + "…"
	}
	return model.Quote{ID: m.ID, From: m.From, Text: text}
}
