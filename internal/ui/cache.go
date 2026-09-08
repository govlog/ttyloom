package ui

import (
	"fmt"
	"slices"
	"time"

	"github.com/govlog/ttyloom/internal/cache"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// Disk cache: read at start, write debounced. The UI goroutine touches the
// disk only at start and at exit; the periodic writes go to a goroutine that
// only ever gets copies.

// cacheMark : line put under the history that comes from the disk, dropped as
// soon as the network has answered for that chat.
//
// ponytail: the mark is compared by its translated text; relang rewrites the
// lines in place at a /set lang, which is what keeps the comparison true. Move
// it to a field of the item if that ever gets in the way.
func cacheMark() string { return i18n.T("cache_pending") }

// flushEvery : a changed chat is written at most once every 5 s.
const flushEvery = 5 * time.Second

// cacheFor gives the disk cache of a network, nil when it has none (cache off,
// network with no cache). Every call site takes nil as "nothing to read nor
// write".
func (u *UI) cacheFor(net string) *cache.Cache { return u.caches[net] }

// loadCache fills chats, chatList and the windows from the disk, one cache per
// network. Called before the loop, on the UI goroutine: the state is its own.
func (u *UI) loadCache() {
	u.cached, u.cacheSelf = map[model.ChatKey]bool{}, map[string]int64{}
	loaded := 0
	for net, cc := range u.caches {
		chats, selfID, err := cc.LoadDialogs()
		if err != nil {
			u.status0(i18n.T("cache_error", err))
			continue
		}
		if len(chats) == 0 {
			// With no readable dialogs the identity of the cache cannot be checked: a
			// history left by another account (or in an earlier format) must not come
			// back through bindChat.
			_ = cc.Wipe()
			continue
		}
		u.cacheSelf[net] = selfID
		for i := range chats {
			// The gob was written before the networks had a name: without this the
			// chat would reach u.net() un-stamped and its calls would go nowhere.
			// Stamped before remember: on an already known chat, the first
			// insertion keeps its Net.
			chats[i].Net = net
			c := u.remember(&chats[i])
			u.cached[c.Key()] = true
			u.listChat(c)
		}
		loaded += len(chats)
	}
	if loaded == 0 {
		return
	}
	// Only the chats that the automatic opening (C3) would show get their
	// window at once; the other histories are read at binding time.
	windows := 0
	for _, c := range autoOpenChats(u.chatList, time.Now(), u.cfg.AutoOpenDays) {
		w := u.ws.New(true)
		u.bindChat(w, c)
		if len(w.Items) > 0 {
			windows++
		}
	}
	u.status0(i18n.T("cache_loaded", loaded, windows))
}

// bindChat binds the window to the chat and, when it is still empty, plays
// the cached history in it. Every binding point goes through here: a window
// opened by a single incoming message thus starts again from its
// cache_messages cached messages, instead of wiping them at the next flush.
// The window stays "not loaded": the network keeps the lead on the recent
// history.
func (u *UI) bindChat(w *Window, c *model.Chat) {
	w.Chat = c
	if len(w.Items) > 0 || w.Search != "" {
		return
	}
	cc := u.cacheFor(c.Net)
	if cc == nil {
		return
	}
	msgs, err := cc.LoadHistory(c.ID)
	if err != nil || len(msgs) == 0 {
		return
	}
	ptrs := make([]*model.Msg, len(msgs))
	for k := range msgs {
		msgs[k].Net = c.Net // same as loadCache: the gob carries no network
		ptrs[k] = &msgs[k]
	}
	w.Merge(ptrs)
	// Loaded and Full stay false: the first visit still loads the network
	// history, which fills the gap between the cache and now, and scrolling up
	// loads the older messages.
	w.AddSys(cacheMark())
}

// dropCache : the account connected on net is not the one that wrote its
// cache. Everything that came from the disk for THAT network is dropped
// (windows, chats, files); the other networks keep theirs, and window 0 stays.
func (u *UI) dropCache(net string) {
	u.bgWait.Wait() // Finish older writes before removing this account’s files.
	u.cancelMode()  // edit, reply, confirmation, search: nothing valid left
	u.setSel(u.view(), nil)
	stale := func(k model.ChatKey) bool { return k.Net == net && u.cached[k] }
	u.dropTargets(func(c *model.Chat) bool { return stale(c.Key()) })
	for i := len(u.ws.List) - 1; i > 0; i-- {
		if w := u.ws.List[i]; w.Chat == nil || !stale(w.Chat.Key()) {
			continue
		}
		u.ws.List = slices.Delete(u.ws.List, i, i+1)
		switch {
		case u.ws.Cur == i:
			u.ws.Cur = 0
		case u.ws.Cur > i:
			u.ws.Cur--
		}
	}
	u.chatList = slices.DeleteFunc(u.chatList, func(c *model.Chat) bool { return stale(c.Key()) })
	for k := range u.cached {
		if k.Net == net {
			delete(u.chats, k)
			delete(u.cached, k)
		}
	}
	for k := range u.dirty {
		if k.Net == net { // a pending write would put the dropped history back
			delete(u.dirty, k)
		}
	}
	delete(u.cacheSelf, net)
	if cc := u.cacheFor(net); cc != nil {
		_ = cc.Wipe()
	}
	u.status0(i18n.T("cache_other_account"))
}

// mergeDialogs : the network list comes first and its fields win; the chats
// known only from the cache are kept at the tail. Keyed by chat and not by
// bare id: the list holds every network at once.
func mergeDialogs(cached, fresh []*model.Chat) []*model.Chat {
	seen := make(map[model.ChatKey]bool, len(fresh))
	out := make([]*model.Chat, 0, len(cached)+len(fresh))
	for _, c := range fresh {
		seen[c.Key()] = true
		out = append(out, c)
	}
	for _, c := range cached {
		if !seen[c.Key()] {
			out = append(out, c)
		}
	}
	return out
}

// pruneChats drops the chats of net that its dialog list no longer holds.
// Only ever called on a list that is the whole account (EvDialogs.Complete):
// a Telegram list is paged, and a chat missing from one page is not a chat
// that is gone. mergeDialogs alone would keep it, from the cache, for ever —
// a Discord server one leaves used to stay in the sidebar, live and at the
// next start.
func (u *UI) pruneChats(net string, fresh []*model.Chat) {
	seen := make(map[model.ChatKey]bool, len(fresh))
	for _, c := range fresh {
		seen[c.Key()] = true
	}
	n := 0
	for _, c := range slices.Clone(u.chatList) { // dropChat edits u.chatList
		if c.Net == net && !seen[c.Key()] && u.dropChat(c.Key()) {
			n++
		}
	}
	if n == 0 {
		return
	}
	// No saveDialogs here: the EvDialogs case writes the list a few lines
	// below, so the whole prune costs one write.
	u.goTo(u.ws.Cur) // windows closed: the current one has moved
	u.sys(i18n.T("chats_pruned", n))
}

// dropCacheMark drops the "cache" line of the window.
func (w *Window) dropCacheMark() {
	for i, it := range w.Items {
		if it.Sys == cacheMark() {
			w.Items = slices.Delete(w.Items, i, i+1)
			return
		}
	}
}

// markDirty : the chat will be written at the next flush.
func (u *UI) markDirty(k model.ChatKey) {
	if u.cacheFor(k.Net) != nil && k.ID != 0 {
		u.dirty[k] = true
	}
}

// saveDialogs writes the list of the chats, split by network: one file per
// cache. The goroutine only gets copies: the UI goes on changing its
// *model.Chat (remember).
func (u *UI) saveDialogs() {
	if len(u.caches) == 0 {
		return
	}
	byNet := map[string][]model.Chat{}
	for _, c := range u.chatList {
		cp := *c
		cp.Reactions = nil // read again at each F3: a stale restriction does not survive
		byNet[c.Net] = append(byNet[c.Net], cp)
	}
	// Every cache known to us writes, even with nothing for its network: a list
	// that became empty (last chat left) must not come back from the file at
	// the next start.
	for net, cc := range u.caches {
		if _, ok := u.self[net]; !ok {
			// No EvReady yet: writing 0 as the self id would make the file look
			// like nobody's, and the "other account" drop would never fire at the
			// next start. Its file is the authority until the network says who we
			// are.
			continue
		}
		// me: our id on THAT network, the one read back at the next start
		// (cacheSelf) to tell whether the cache is still ours.
		chats, me := byNet[net], u.selfOf(net).ID
		u.bg(func() { cc.SaveDialogs(chats, me) }) // error ignored: nothing to show from there
	}
}

// flushCache writes the changed chats. sync: write in place (exit); else one
// goroutine per chat, which only gets copies.
func (u *UI) flushCache(sync bool) {
	if sync {
		// Older writes must finish before the final snapshot reaches disk.
		u.bgWait.Wait()
	}
	u.flushed = time.Now()
	for k := range u.dirty {
		delete(u.dirty, k)
		cc := u.cacheFor(k.Net)
		if cc == nil {
			continue
		}
		i := u.ws.ForChat(k)
		if i < 0 {
			continue
		}
		msgs := u.ws.List[i].Msgs()
		if len(msgs) == 0 {
			continue
		}
		id := k.ID // the cache of the network is keyed by the bare id
		if sync {
			_ = cc.SaveHistory(id, msgs)
			continue
		}
		// ponytail: error ignored (no access to the UI from the goroutine) and last
		// rename wins when two writes of the same chat cross: at worst the cache
		// loses the 5 s of the newest one, never a half written file (temporary
		// file + rename). Serialise per chat if the write ever becomes slower than
		// the debounce.
		u.bg(func() { cc.SaveHistory(id, msgs) })
	}
}

// --- sequential sync ---

// syncLimit : messages brought back at most per chat at the first sync. Fixed,
// apart from cache_messages (the depth kept in the cache): the sync only fills
// the gap since the last visit, it does not fill the cache in one go;
// scrolling up in a window loads the rest from the network.
const syncLimit = 200

// syncPause : dead time between two chats. The sync goes over the whole chat
// list: without a pause, Telegram answers with a FLOOD_WAIT.
const syncPause = 150 * time.Millisecond

// evSyncTick : move to the next chat (time.AfterFunc → UI loop).
type evSyncTick struct{}

// syncOrder gives the sync order, from the most recently active chat to the
// oldest. The chats with no known date end at the tail (a zero date is the
// oldest there is) and one chat is kept only once: the list can hold the same
// id twice (pinned dialogs).
func syncOrder(chats []*model.Chat) []*model.Chat {
	seen := make(map[model.ChatKey]bool, len(chats))
	out := make([]*model.Chat, 0, len(chats))
	for _, c := range chats {
		if !seen[c.Key()] {
			seen[c.Key()] = true
			out = append(out, c)
		}
	}
	slices.SortStableFunc(out, func(a, b *model.Chat) int { return b.LastDate.Compare(a.LastDate) })
	return out
}

// syncStart queues the chats of net at its first chat list. With no cache
// there is nothing to fill, and a bot has no history. A queue already being
// drained (another network) simply gets the new chats at its tail.
//
// A network without the Sync capability is left alone: on Discord one page of
// history is two REST reads, so sweeping the whole chat list at every login
// would look like a self-bot for nothing — opening a window loads its page
// anyway.
func (u *UI) syncStart(net string) {
	if b := u.netOf(net); b == nil || !b.Caps().Sync {
		return
	}
	if u.selfOf(net).Bot || len(u.caches) == 0 {
		return
	}
	q := syncOrder(u.netChats(net))
	if len(q) == 0 {
		return
	}
	u.syncQueue = append(u.syncQueue, q...)
	u.syncTotal += len(q)
	u.status0(i18n.T("sync_start", u.syncTotal))
	u.syncNext() // already in flight: syncNext gives the lead back on syncCur
}

// syncNext starts the next step. Only one request in flight: syncCur goes
// back to nil only when its result comes.
func (u *UI) syncNext() {
	if u.syncCur != nil || len(u.syncQueue) == 0 || u.ctx.Err() != nil {
		return
	}
	// A chat whose network is gone is skipped rather than left in flight:
	// syncCur would never come back to nil and the queue would stall.
	for len(u.syncQueue) > 0 {
		c := u.syncQueue[0]
		u.syncQueue = u.syncQueue[1:]
		b := u.net(c)
		if b == nil {
			continue
		}
		u.syncCur = c
		b.LoadHistorySince(u.ctx, c, u.syncMinID(c), syncLimit)
		return
	}
}

// syncMinID : last known id of the chat — the window when it exists, else the
// history file. 0: nothing local, the sync brings the recent history.
//
// ponytail: disk read on the UI goroutine, once per chat and per sync, spaced
// by syncPause: a file of 200 messages decodes in less than a millisecond.
// Move to an index of the last ids if the sync ever makes the display
// stutter.
func (u *UI) syncMinID(c *model.Chat) int {
	if i := u.ws.ForChat(c.Key()); i >= 0 {
		return u.ws.List[i].LastID()
	}
	cc := u.cacheFor(c.Net)
	if cc == nil {
		return 0
	}
	msgs, err := cc.LoadHistory(c.ID)
	if err != nil {
		return 0
	}
	last := 0
	for _, m := range msgs {
		last = max(last, m.ID)
	}
	return last
}

// syncHistory : result of one step. The window of the chat, when it exists,
// gets the messages like a plain network history; otherwise only the file is
// updated — one window per chat would make Ctrl+X useless.
func (u *UI) syncHistory(e model.EvHistory) {
	c := u.syncCur
	if c == nil || c.Key() != u.evKey(e.ChatID) {
		return // result outside the queue: never move the queue on twice
	}
	u.syncCur = nil
	switch {
	case e.Err != "":
		u.status0(i18n.T("sync_error", u.title(c), e.Err))
	case u.ws.ForChat(u.evKey(e.ChatID)) >= 0:
		u.history(e) // Merge (the newest at the tail), cache mark, dirty, media
		u.syncNew += len(e.Msgs)
	default:
		u.syncToCache(c, e.Msgs)
		u.syncNew += len(e.Msgs)
	}
	if done := u.syncTotal - len(u.syncQueue); done%10 == 0 && done < u.syncTotal {
		u.status0(i18n.T("sync_progress", done, u.syncTotal))
	}
	if len(u.syncQueue) == 0 {
		u.status0(i18n.T("sync_done", u.syncNew))
		return
	}
	time.AfterFunc(syncPause, func() {
		select {
		case u.events <- evSyncTick{}:
		case <-u.ctx.Done(): // /quit: nobody reads the events any more
		}
	})
}

// syncToCache : chat with no window. The new messages are merged with the
// history file of its network and written straight away, without going through
// a Window.
func (u *UI) syncToCache(c *model.Chat, msgs []model.Msg) {
	cc := u.cacheFor(c.Net)
	if cc == nil || len(msgs) == 0 {
		return
	}
	chatID := c.ID
	old, err := cc.LoadHistory(chatID)
	if err != nil {
		return
	}
	// msgs comes from the event and no window points at it: the writing
	// goroutine owns it alone. SaveHistory sorts and cuts at cache_messages.
	u.bg(func() { cc.SaveHistory(chatID, mergeHistory(old, msgs)) })
}

// mergeHistory gives the cached history filled with the new messages, with no
// duplicate ID. Like Window.Merge, the version already cached wins.
func mergeHistory(old, fresh []model.Msg) []model.Msg {
	have := make(map[int]bool, len(old))
	for _, m := range old {
		have[m.ID] = true
	}
	out := old
	for _, m := range fresh {
		if !have[m.ID] {
			out = append(out, m)
		}
	}
	return out
}

// bg runs a cache write in the background. A panic there would take the whole
// process down without the deferred Close of main — terminal left in raw
// mode — and the goroutines of the backend all have their defer c.guard. Nothing to
// show from there: the interface is not reachable, and a cache that fails
// costs nothing but a slower start.
func (u *UI) bg(f func()) {
	u.bgWait.Add(1)
	go func() {
		defer u.bgWait.Done()
		defer func() {
			if r := recover(); r != nil {
				// Visible in /debug and in window 0, like the backend guard.
				select {
				case u.events <- model.EvLog{Level: "ERROR", Msg: fmt.Sprintf("cache: panic: %v", r)}:
				default: // The UI can already be waiting for the final save.
				}
			}
		}()
		f()
	}()
}
