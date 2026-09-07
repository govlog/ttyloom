package ui

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/theme"
)

// defaultMaxItems : items kept per window in memory when nothing else is
// asked. setMaxItems lines the limit up with cache_messages when the config
// keeps more (otherwise the window would cut again what the cache kept).
const defaultMaxItems = 2000

// Item : a message, a system line, or lines already drawn (/chats…).
type Item struct {
	Msg *model.Msg
	Sys string
	// At : arrival of a system line, shown before it in the status windows
	// (window 0, aggregate, log) when timestamps are on. Zero on the lines
	// that are not events (cache mark, history gap): no time on those.
	At time.Time
	// Info : information line of a message (key "i"), dropped when the
	// message is deselected. ponytail: a mark on the item, no list to keep.
	Info bool
	// gap : hole marker of the history — the two ids the hole lies between
	// (see markGap). Zero on every other item.
	gap   [2]int
	Text  []render.Line
	w     int
	lines []render.Line
}

type Window struct {
	Chat   *model.Chat
	Items  []*Item
	Sel    *Item // selected message (never a sys item)
	Scroll int   // physical lines from the bottom
	Act    int   // messages received while the window was not the current one
	// MarkID : last message read, frozen at each (re)entry into the window
	// (attach, goTo, focus back) — position of the redline (/set redline).
	MarkID int
	// scrolledToMark : Scroll already set on the redline (once per opening).
	// MarkID stays: the line goes on showing through the EvHistory that follow,
	// without moving while the window stays shown.
	scrolledToMark bool
	ReadSent       int // last id reported read to the server
	Full           bool
	Loading        bool
	Loaded         bool   // recent history loaded at least once
	Draft          string // input in progress, kept for the time of leaving the window
	Log            bool   // /log: added messages logged to disk
	// Search : result of /search on Chat, not a history. The window is invisible
	// to ForChat (the live messages go to the window of the chat) and to the
	// cache, and its *model.Msg are shared with the window of the chat.
	Search string
	// max : items kept in memory, 0 = defaultMaxItems. Taken from Windows.max
	// at creation and lined up live by setMaxItems (/set cache_messages).
	max int
	// Filter : messages left out of the drawing (/net on the aggregate). nil =
	// everything. The items stay in memory: the filter moves with no reload.
	Filter func(*model.Msg) bool
	nLines int // lines of the last drawing: capacity hint of the next one
}

func (w *Window) Name() string {
	if w.Search != "" {
		return "?" + w.Search
	}
	if w.Chat == nil {
		return "(none)"
	}
	return w.Chat.Title
}

// Update replaces the message with the same ID/TmpID. false when it is missing:
// the aggregated view thus follows the edits without getting a history message.
func (w *Window) Update(m *model.Msg) bool {
	for i := len(w.Items) - 1; i >= 0; i-- {
		it := w.Items[i]
		if it.Msg == nil || it.Msg.Key() != m.Key() {
			continue // message ids are per chat, and the aggregate mixes chats and networks
		}
		if (m.ID != 0 && it.Msg.ID == m.ID) || (m.TmpID != 0 && it.Msg.TmpID == m.TmpID) {
			// TmpID != 0 : local send, whose media is only a placeholder — the
			// real one comes with the echo and must take its place.
			if it.Msg.Media != nil && m.Media != nil && m.Media.State == model.MediaNone && it.Msg.TmpID == 0 &&
				identOf(it.Msg.Media) == identOf(m.Media) {
				m.Media = it.Msg.Media // keeps the media already loaded
			}
			it.Msg, it.lines = m, nil
			return true
		}
	}
	return false
}

// mediaIdent : what tells two media of the same message apart without
// reading the opaque handle — a Telegram file reference rotates between two
// reads of the same photo, so Loc itself is no identity. An edit that swaps
// the photo changes at least the size.
type mediaIdent struct {
	kind      model.MediaKind
	size      int64
	w, h      int
	name, url string
}

func identOf(md *model.Media) mediaIdent {
	return mediaIdent{md.Kind, md.Size, md.W, md.H, md.Name, md.URL}
}

// Upsert adds the message or replaces the one with the same ID/TmpID. true when added.
func (w *Window) Upsert(m *model.Msg) bool {
	if w.Update(m) {
		return false
	}
	w.Items = append(w.Items, &Item{Msg: m})
	w.trim()
	return true
}

// Touch invalidates the drawing of the message m (compared by pointer): the
// views share the *model.Msg, but each has its own line cache.
func (w *Window) Touch(m *model.Msg) {
	for i := len(w.Items) - 1; i >= 0; i-- {
		if w.Items[i].Msg == m {
			w.Items[i].lines = nil
			return
		}
	}
}

// Sent applies the send receipt to the pending item. false when it is gone.
// The backend handles the update of our own send during the RPC itself:
// in a channel or supergroup the server copy thus comes before the receipt.
// When it is already there, we drop the pending item instead of doubling the message.
func (w *Window) Sent(tmpID int64, id int, errText string) bool {
	k := -1
	for i, it := range w.Items {
		if it.Msg != nil && it.Msg.TmpID == tmpID {
			k = i
			break
		}
	}
	if k < 0 {
		return false
	}
	pend := w.Items[k]
	if id != 0 {
		for _, it := range w.Items {
			// The duplicate is looked for in the chat of the send only: in the
			// aggregate, the same id elsewhere is another message.
			if it.Msg != nil && it.Msg.ID == id && it.Msg.Key() == pend.Msg.Key() && it.Msg.TmpID != tmpID {
				if w.Sel == pend {
					w.Sel = nil
				}
				w.Items = slices.Delete(w.Items, k, k+1)
				return true
			}
		}
	}
	pend.Msg.ID, pend.Msg.Pending, pend.Msg.Err, pend.lines = id, false, errText, nil
	return true
}

// Merge inserts messages with no duplicate ID: the older ones at the head,
// the ones newer than the last message of the window at the tail. The second
// case is the cached history filled by the network — without it, the new
// messages would end up above the old ones.
func (w *Window) Merge(ms []*model.Msg) {
	have := map[int]bool{}
	for _, it := range w.Items {
		if it.Msg != nil {
			have[it.Msg.ID] = true
		}
	}
	last := w.LastID()
	var older, newer []*Item
	for _, m := range ms {
		switch {
		case have[m.ID]:
		case m.ID > last: // ms comes from the oldest to the newest: the order holds
			newer = append(newer, &Item{Msg: m})
		default:
			older = append(older, &Item{Msg: m})
		}
	}
	w.Items = append(append(older, w.Items...), newer...)
	w.dropFilledGaps()
	w.trim()
}

// idRange gives the smallest and the largest message id of the window; 0, 0
// when it holds no message.
func (w *Window) idRange() (lo, hi int) {
	for _, it := range w.Items {
		if it.Msg == nil || it.Msg.ID <= 0 {
			continue
		}
		if lo == 0 || it.Msg.ID < lo {
			lo = it.Msg.ID
		}
		if it.Msg.ID > hi {
			hi = it.Msg.ID
		}
	}
	return lo, hi
}

// markGap puts the hole marker between the messages loID and hiID, which the
// window now holds one after the other with everything between them missing.
// Nothing is inserted twice for the same hole.
func (w *Window) markGap(loID, hiID int) {
	for i, it := range w.Items {
		if it.gap == [2]int{loID, hiID} {
			return
		}
		if it.Msg != nil && it.Msg.ID == hiID {
			w.Items = slices.Insert(w.Items, i, &Item{Sys: i18n.T("history_gap"), gap: [2]int{loID, hiID}})
			return
		}
	}
}

// dropFilledGaps drops the markers whose hole a later page has filled: a
// message now stands between the two boundary ids.
// ponytail: a page that fills only part of a hole drops the marker all the
// same — no code path loads a bounded range today. Split the marker in two if
// one ever does.
func (w *Window) dropFilledGaps() {
	for i := 0; i < len(w.Items); i++ {
		g := w.Items[i].gap
		if g == [2]int{} || !w.hasBetween(g[0], g[1]) {
			continue
		}
		w.Items = slices.Delete(w.Items, i, i+1)
		i--
	}
}

func (w *Window) hasBetween(loID, hiID int) bool {
	for _, it := range w.Items {
		if it.Msg != nil && it.Msg.ID > loID && it.Msg.ID < hiID {
			return true
		}
	}
	return false
}

// MergeAround inserts a page centred on a message: each new id takes its rank
// among the ones already there, instead of going as a block to the head (such a
// page falls in the middle of the window, not at its edges). ms is sorted from
// the oldest to the newest; the items with no message (system lines) stay put.
func (w *Window) MergeAround(ms []*model.Msg) {
	have := map[int]bool{}
	for _, it := range w.Items {
		if it.Msg != nil {
			have[it.Msg.ID] = true
		}
	}
	var add []*Item
	for _, m := range ms {
		if !have[m.ID] {
			have[m.ID] = true
			add = append(add, &Item{Msg: m})
		}
	}
	if len(add) == 0 {
		return
	}
	// Hole: the page and what the window holds do not overlap and do not touch
	// — a jump to a far away message. Nothing fills it (a scroll up loads above
	// the oldest message shown, thus above the hole), so the seam is marked.
	//
	// The range is that of the page ms, not that of add: add drops the messages
	// already there, so a page that overlaps the window would come out of it
	// disjoint and get a marker that nothing could ever drop.
	// ponytail: ids are read as a dense line — a message deleted right under lo
	// still leaves a marker for a page that stopped one id short. The exact
	// answer is a bounded server query; the marker only misleads by being there.
	lo, hi := w.idRange()
	plo, phi := ms[0].ID, ms[len(ms)-1].ID
	gapLo, gapHi := 0, 0
	switch {
	case lo == 0 || plo <= 0:
	case plo > hi:
		gapLo, gapHi = hi, plo
	case phi < lo:
		gapLo, gapHi = phi, lo
	}
	out := make([]*Item, 0, len(w.Items)+len(add))
	k := 0
	for _, it := range w.Items {
		for k < len(add) && it.Msg != nil && it.Msg.ID > 0 && add[k].Msg.ID < it.Msg.ID {
			out = append(out, add[k])
			k++
		}
		out = append(out, it)
	}
	w.Items = append(out, add[k:]...)
	if gapLo != 0 {
		w.markGap(gapLo, gapHi)
	}
	w.dropFilledGaps()
	// ponytail: no trim here — it cuts from the head and would drop exactly the
	// page just inserted (window at its cap, jump to an old message). The next
	// Merge/Upsert will bring the window back to its limit.
}

// Msgs gives a copy of the messages of the window for the cache. The sends
// never confirmed (zero ID) are left aside. The copy shares nothing with the
// item any more: the Media is copied without its frames, because the goroutine
// that writes the cache reads it while the UI goes on changing it (animation,
// download).
func (w *Window) Msgs() []model.Msg {
	out := make([]model.Msg, 0, len(w.Items))
	for _, it := range w.Items {
		if it.Msg == nil || it.Msg.ID == 0 {
			continue
		}
		m := *it.Msg
		if m.Media != nil {
			md := *m.Media
			md.Frames = nil
			m.Media = &md
		}
		out = append(out, m)
	}
	return out
}

func (w *Window) AddSys(s string)           { w.Items = append(w.Items, &Item{Sys: s, At: time.Now()}); w.trim() }
func (w *Window) AddLines(ls []render.Line) { w.Items = append(w.Items, &Item{Text: ls}); w.trim() }

// Invalidate of an item: its drawing will be made again at the next draw.
func (it *Item) Invalidate() {
	if it != nil {
		it.lines = nil
	}
}

func (w *Window) Invalidate() {
	for _, it := range w.Items {
		it.lines = nil
	}
}

func (w *Window) InvalidateMedia(md *model.Media) {
	for _, it := range w.Items {
		if it.Msg != nil && it.Msg.Media == md {
			it.lines = nil
		}
	}
}

// ponytail: trim does not free the kitty images of the dropped items; the terminal clears them.
func (w *Window) trim() {
	if n := len(w.Items) - cmp.Or(w.max, defaultMaxItems); n > 0 {
		w.Items = w.Items[n:]
		if w.Sel != nil && !slices.Contains(w.Items, w.Sel) {
			w.Sel = nil
		}
	}
}

// selectable : a shown message, neither a sys item nor a service message.
func selectable(it *Item) bool { return it.Msg != nil && it.Msg.Service == "" }

// shown : the item is drawn — Filter (/net on the aggregate) keeps it. The
// walks over w.Items (selection, last message of mine) go through it: a
// message the user cannot see is no target for an edit or a reaction.
func (w *Window) shown(it *Item) bool {
	return it == nil || w.Filter == nil || it.Msg == nil || w.Filter(it.Msg)
}

// SelectNext moves Sel one message up (dir < 0) or down (dir > 0). From
// nothing, only up selects: the last message shown, failing that the last
// message at all. Past the last message, down deselects. true when the
// selection changed.
func (w *Window) SelectNext(dir int, visible func(*Item) bool) bool {
	i := slices.Index(w.Items, w.Sel) // -1: nothing, or item gone
	if i < 0 {
		old := w.Sel
		w.Sel = nil
		if dir > 0 {
			return old != nil
		}
		for _, pick := range []func(*Item) bool{visible, nil} {
			for k := len(w.Items) - 1; k >= 0; k-- {
				if it := w.Items[k]; selectable(it) && w.shown(it) && (pick == nil || pick(it)) {
					w.Sel = it
					return true
				}
			}
		}
		return old != nil
	}
	for k := i + dir; k >= 0 && k < len(w.Items); k += dir {
		if selectable(w.Items[k]) && w.shown(w.Items[k]) {
			w.Sel = w.Items[k]
			return true
		}
	}
	if dir > 0 { // past the last message
		w.Sel = nil
		return true
	}
	return false
}

func (w *Window) OldestID() int {
	for _, it := range w.Items {
		if it.Msg != nil && it.Msg.ID > 0 {
			return it.Msg.ID
		}
	}
	return 0
}

func (w *Window) LastID() int {
	for i := len(w.Items) - 1; i >= 0; i-- {
		if it := w.Items[i]; it.Msg != nil && it.Msg.ID > 0 {
			return it.Msg.ID
		}
	}
	return 0
}

// Lines draws the whole window (cache per item and width), with day
// separators and the redline (last read, /set redline).
func (w *Window) Lines(o render.Opts) []render.Line {
	lines, _, _ := w.LineItems(o)
	return lines
}

// LineItems draws like Lines and also gives back the item of each line (nil
// for the separators), and the rank of the redline (-1 when it is missing or
// off, o.Redline): map of the clicks + first read position.
func (w *Window) LineItems(o render.Opts) ([]render.Line, []*Item, int) {
	// Sized on the frame before: the drawing rarely changes height, and the
	// doubling growth cost more than the lines themselves.
	out := make([]render.Line, 0, w.nLines)
	items := make([]*Item, 0, w.nLines)
	var lastDay [3]int // year, month, day: no time.Format per message per frame
	marked := false
	markIdx := -1
	sep := func(l render.Line) { out, items = append(out, l), append(items, nil) }
	for _, it := range w.Items {
		if !w.shown(it) {
			continue
		}
		if it.Msg != nil {
			y, m, d := it.Msg.Date.Date()
			if day := [3]int{y, int(m), d}; day != lastDay {
				sep(render.Separator(render.LongDate(it.Msg.Date), o))
				lastDay = day
			}
			if o.Redline && w.MarkID > 0 && !marked && it.Msg.ID > w.MarkID && !it.Msg.Out {
				markIdx = len(out)
				sep(render.Redline(o))
				marked = true
			}
		}
		if it.lines == nil || it.w != o.Width {
			switch {
			case it.Msg != nil:
				it.lines = render.Message(it.Msg, o)
			case it.Text != nil:
				it.lines = it.Text
			default:
				s := "*** " + it.Sys
				if o.Timestamps && !it.At.IsZero() && w.Chat == nil && w.Search == "" {
					s = o.Stamp(it.At) + s
				}
				it.lines = render.Plain(s, o.Theme.Style(theme.System), o.Width)
			}
			it.w = o.Width
		}
		out = append(out, it.lines...)
		for range it.lines {
			items = append(items, it)
		}
	}
	w.nLines = len(out)
	return out, items, markIdx
}

// swapDraft swaps the draft being typed (cur) for the one of the window
// reached, filing cur into the one of the window left. Pure, tested with no
// UI; when old and target are the same window (refresh of the current
// window), cur comes back unchanged.
func swapDraft(old, target *Window, cur string) string {
	old.Draft = cur
	next := target.Draft
	target.Draft = ""
	return next
}

// Windows : ordered list, index 0 = status window.
type Windows struct {
	List []*Window
	Cur  int
	Log  bool // current cfg.Log: the windows made later take this setting
	max  int  // items kept per window; the windows made later take it too
}

func NewWindows() *Windows           { return &Windows{List: []*Window{{}}} }
func (ws *Windows) Current() *Window { return ws.List[ws.Cur] }

func (ws *Windows) New(hide bool) *Window {
	w := &Window{Log: ws.Log, max: ws.max}
	ws.List = append(ws.List, w)
	if !hide {
		ws.Cur = len(ws.List) - 1
	}
	return w
}

// Close closes the current window (never window 0) and gives the closed window back.
func (ws *Windows) Close() *Window { return ws.CloseAt(ws.Cur) }

// CloseAt closes the window i (never window 0, never an index outside the
// list) and resets Cur: the shown window stays the same, or the one before
// when it is the one being closed. nil when nothing was closed.
func (ws *Windows) CloseAt(i int) *Window {
	if i <= 0 || i >= len(ws.List) {
		return nil
	}
	w := ws.List[i]
	ws.List = slices.Delete(ws.List, i, i+1)
	if ws.Cur >= i {
		ws.Cur--
	}
	return w
}

// setMaxItems lines the memory of every window up with cache_messages, never
// under defaultMaxItems: the window must not cut again what the cache kept.
// Read at start and at each /set cache_messages; the trim comes at the next
// Merge/Upsert.
func (u *UI) setMaxItems(n int) {
	n = max(defaultMaxItems, n)
	u.ws.max = n
	for _, w := range u.ws.List {
		w.max = n
	}
	u.agg.max, u.debug.max = n, n
}

func (ws *Windows) Next() { ws.Cur = (ws.Cur + 1) % len(ws.List) }
func (ws *Windows) Prev() { ws.Cur = (ws.Cur + len(ws.List) - 1) % len(ws.List) }

func (ws *Windows) Switch(n int) bool {
	if n < 0 || n >= len(ws.List) {
		return false
	}
	ws.Cur = n
	return true
}

// ForChat gives the window bound to the chat k, -1 when there is none. Keyed
// by (net, id): a bare id is not a chat identity any more.
func (ws *Windows) ForChat(k model.ChatKey) int {
	for i, w := range ws.List {
		if w.Chat != nil && w.Chat.Key() == k && w.Search == "" {
			return i
		}
	}
	return -1
}

// ByName : title resolves the shown name of a chat (local names of /rename);
// nil = Telegram title.
func (ws *Windows) ByName(prefix string, title func(*model.Chat) string) int {
	p := strings.ToLower(prefix)
	for i, w := range ws.List {
		if w.Chat == nil {
			continue
		}
		// winName and not Chat.Title: "/win ?text" reaches a search window, and a
		// local name is a target too.
		if strings.HasPrefix(strings.ToLower(winName(w, title)), p) || strings.HasPrefix(strings.ToLower(w.Chat.Username), p) {
			return i
		}
	}
	return -1
}
