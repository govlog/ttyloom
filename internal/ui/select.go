package ui

import (
	"encoding/base64"
	"slices"
	"strings"

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
	// Same conditions as the help line: a key that is not on it is not kept.
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
	case r == 'd' && act && u.own(m):
		u.confirm(i18n.T("confirm_delete_message", m.ID), func() {
			c := u.chatOf(m)
			if b := u.net(c); b != nil {
				b.Delete(u.ctx, c, m.ID)
			}
		})
	case r == 'p' && act:
		u.reply = it
	case r == 'c' && act:
		u.copySel(it, it) // same path as the drag: OSC 52 + status
	case r == 'i' && act:
		c := u.chatOf(m)
		if b := u.net(c); b != nil {
			b.Info(u.ctx, c, m.ID, c.ReadOutboxMaxID, c.ReadInboxMaxID)
		}
	case r == 'r' && act && u.capsOf(m).Reactions:
		u.openReactPicker(it)
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
	case a.Key == render.KeyEsc:
		u.cancelMode()
		u.setSel(w, nil)
	case it != nil:
		u.setSel(w, it) // the palette acts on the message clicked
		u.selKey(w, a.Key)
	}
}

// --- selection drag: copy of messages to the clipboard ---

// selCancel : end of a drag with no copy (new press, lost release).
func (u *UI) selCancel() {
	u.drag = dragNone
	if u.selAnchor != nil {
		u.selInvalidate()
		u.selAnchor, u.selEnd = nil, nil
	}
}

// selDragTo : the message under the cursor becomes the end of the range. As
// long as we stay on the line of the press and on its message, it is still a
// click (selEnd nil). true: the range changed, the screen must be drawn again.
func (u *UI) selDragTo(m term.MouseEvent) bool {
	x0, _ := u.layout()
	it := hitAt(u.hits, m.X-x0, m.Y).item
	if m.Y == u.selY && it == u.selAnchor {
		return false // neither line nor message: a shaky click stays a click
	}
	end := u.selEnd
	if it != nil && selectable(it) {
		end = it
	}
	if end == nil {
		end = u.selAnchor // outside the messages: the range stops at the anchor
	}
	if end == u.selEnd {
		return false // range unchanged: no drawing to make again
	}
	u.selInvalidate() // old range
	u.selEnd = end
	u.selInvalidate() // new one
	return true
}

// selRelease : release of a selection drag. With no move it is a click
// (selection, or toggle on a second click); otherwise the range is copied.
func (u *UI) selRelease() {
	u.drag = dragNone
	anchor, end := u.selAnchor, u.selEnd
	u.selInvalidate()
	u.selAnchor, u.selEnd = nil, nil
	if end == nil {
		w := u.view()
		if anchor != nil && anchor.Msg != nil && anchor.Msg.Deleted { // deleted: the click shows its content, the next one hides it
			anchor.Reveal, anchor.lines = !anchor.Reveal, nil
		}
		if w.Sel == anchor {
			anchor = nil // second click on the selected message: toggle
		}
		u.setSel(w, anchor)
		return
	}
	u.copySel(anchor, end)
}

// selInvalidate : the background of the range is not part of the cache key,
// so the drawings it touches must be made again.
func (u *UI) selInvalidate() {
	for _, it := range rangeItems(u.view().Items, u.selAnchor, u.selEnd) {
		it.Invalidate()
	}
}

// copySel copies the range [a..b] to the clipboard of the terminal (OSC 52),
// with a note in the status bar.
func (u *UI) copySel(a, b *Item) {
	blocks := selBlocks(u.view().Items, a, b)
	if len(blocks) == 0 {
		return // nothing to copy in the range
	}
	text := strings.Join(blocks, "\n")
	msg := i18n.T("copied_one", len(blocks))
	if len(blocks) > 1 {
		msg = i18n.T("copied_many", len(blocks))
	}
	if len(text) > maxOSC52 {
		msg += i18n.T("copied_truncated")
	}
	u.t.WriteString(osc52(text)) // the buffer goes out with the frame that follows
	u.flash(msg)
}

// rangeItems gives the items between a and b included, bounds taken in either
// order. nil when one of the two is missing (window changed, message dropped).
func rangeItems(items []*Item, a, b *Item) []*Item {
	i, j := slices.Index(items, a), slices.Index(items, b)
	if i < 0 || j < 0 {
		return nil
	}
	if i > j {
		i, j = j, i
	}
	return items[i : j+1]
}

// selBlocks gives one block per copyable message of the range, in display
// order, prefixed with "<nick>" as soon as there are several. The messages
// with no text are skipped — unless the range holds media only: their label is
// better than an empty copy.
func selBlocks(items []*Item, a, b *Item) []string {
	var msgs []*model.Msg
	labels := true
	for _, it := range rangeItems(items, a, b) {
		if !selectable(it) { // system line, info, service message
			continue
		}
		msgs = append(msgs, it.Msg)
		labels = labels && it.Msg.Text == ""
	}
	var out, from []string
	for _, m := range msgs {
		t := m.Text
		if t == "" && labels && m.Media != nil {
			t = m.Media.Label
		}
		if t == "" {
			continue
		}
		// base64 protects the terminal, not the clipboard: without this the
		// control characters of a remote message land there as they are, and
		// a later paste in a shell with no bracketed paste runs the line.
		// The breaks between blocks stay, hence Clean and not CleanLine.
		out, from = append(out, render.Clean(t)), append(from, render.CleanLine(m.From))
	}
	if len(out) > 1 { // only one message: its text as it is, with no prefix
		for i := range out {
			out[i] = "<" + from[i] + "> " + out[i]
		}
	}
	return out
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
// in every view. The chat is resolved by the caller: the event names it by a
// bare id, which alone tells the networks apart no longer.
func (u *UI) readOutbox(c *model.Chat, maxID int) {
	if c == nil || maxID <= c.ReadOutboxMaxID {
		return
	}
	c.ReadOutboxMaxID = maxID
	for _, w := range u.views() {
		for _, it := range w.Items {
			if it.Msg != nil && it.Msg.Key() == c.Key() && it.Msg.Out {
				it.lines = nil
			}
		}
	}
}

// readInbox : my reads up to maxID; the ticks of the received messages
// (someone else) must be made again in every view. Unread follows the unread
// count as soon as hasUnread is true, even with maxID unchanged — another
// client may have read without moving MaxID on our side. Like readOutbox, the
// chat comes resolved from the caller.
func (u *UI) readInbox(c *model.Chat, maxID, unread int, hasUnread bool) {
	if c == nil || maxID < c.ReadInboxMaxID {
		return
	}
	if maxID > c.ReadInboxMaxID {
		c.ReadInboxMaxID = maxID
		for _, w := range u.views() {
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
func (u *UI) cancelMode() {
	if u.edit != nil || u.reply != nil {
		u.ed.Set("")
	}
	u.edit, u.reply, u.ask, u.search = nil, nil, nil, nil
	u.multi = false
	u.selCancel()
	u.cancelSend()
}

// confirm : closed question asked in the input line; do runs on "y".
type confirm struct {
	q  string
	do func()
}

func (u *UI) confirm(q string, do func()) { u.ask = &confirm{q: q, do: do} }

// askKey : answer to a confirmation. It swallows every key, like pasteKey: y
// (or Y) confirms, everything else cancels — the prompt never stays stuck.
func (u *UI) askKey(k term.Key) bool {
	if k.Code == term.Mouse {
		return true // click, wheel: swallowed without touching the confirmation
	}
	do := u.ask.do
	u.ask = nil
	if k.Code == term.None && (k.Rune == 'y' || k.Rune == 'Y') {
		do()
	}
	return true
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
		b.EditStyled(u.ctx, c, m.ID, segs)
		return
	}
	b.Edit(u.ctx, c, m.ID, text)
}

// edited : edit receipt. Failure → "[failed: …]" under the message and the
// input takes the text back to fix it.
func (u *UI) edited(e model.EvEdited) {
	// Every view, not only the window of the chat: a /search result older than
	// the loaded history has its own copy of the message, which would stay
	// "pending" for ever.
	view := u.view()
	key := u.evKey(e.ChatID)
	var cur *Item // copy shown, to take the edit up again on a failure
	found := false
	for _, w := range u.views() {
		for _, it := range w.Items {
			if it.Msg == nil || it.Msg.ID != e.ID || it.Msg.Key() != key {
				continue
			}
			it.Msg.Pending, it.Msg.Err, it.lines = false, e.Err, nil
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
	list := u.allowed(u.chatOf(it.Msg))
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
	if next != "" && !slices.Contains(u.allowed(c), emoji.Base(next)) {
		u.reactFailed(c.Key(), model.EvReactionFailed{ChatID: c.ID, ID: it.Msg.ID,
			Emoji: next, Reason: i18n.T("reaction_not_available")})
		return
	}
	// After the local refusal: a reaction outside the list is told in the
	// window whether a backend is there or not.
	if b := u.net(c); b != nil {
		b.React(u.ctx, c, it.Msg.ID, next)
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
	found := false
	for _, w := range u.views() { // same as edited: each copy of the message
		for _, it := range w.Items {
			if it.Msg != nil && it.Msg.ID == e.ID && it.Msg.Key() == key {
				// A re-read does not empty a list already filled: only an update
				// (or a re-read that really sees zero reaction) has the say to
				// wipe it.
				if e.Refetch && len(e.Reactions) == 0 && len(it.Msg.Reactions) > 0 {
					continue
				}
				it.Msg.Reactions, it.lines = e.Reactions, nil
				found = true
			}
		}
	}
	if found {
		u.markDirty(key)
	}
}

// lastOwn gives my last message that can still be changed. own is handed over
// rather than an id: a window of the aggregate mixes networks, and my id is
// not the same on each of them.
func lastOwn(w *Window, own func(*model.Msg) bool) *Item {
	for i := len(w.Items) - 1; i >= 0; i-- {
		it := w.Items[i]
		if selectable(it) && w.shown(it) && render.Actionable(it.Msg) && own(it.Msg) {
			return it
		}
	}
	return nil
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
