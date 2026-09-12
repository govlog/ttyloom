package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/cache"
	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/protocols/tgc"
)

func TestMergeDialogs(t *testing.T) {
	cached := []*model.Chat{
		{ID: 1, Title: "vieux titre"},
		{ID: 2, Title: "absent du réseau"},
	}
	fresh := []*model.Chat{
		{ID: 3, Title: "nouveau"},
		{ID: 1, Title: "titre à jour"},
	}
	got := mergeDialogs(cached, fresh)
	want := []model.Chat{
		{ID: 3, Title: "nouveau"},
		{ID: 1, Title: "titre à jour"},
		{ID: 2, Title: "absent du réseau"},
	}
	if len(got) != len(want) {
		t.Fatalf("%d chats, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].ID != w.ID || got[i].Title != w.Title {
			t.Fatalf("chat %d: %d %q, want %d %q", i, got[i].ID, got[i].Title, w.ID, w.Title)
		}
	}
}

func TestWindowMsgs(t *testing.T) {
	w := &Window{}
	w.AddSys("ligne système")
	m := &model.Msg{ID: 7, Text: "salut", Media: &model.Media{
		Label: "[photo]", Frames: [][]byte{{1, 2}}, State: model.MediaReady}}
	w.Upsert(m)
	w.Upsert(&model.Msg{TmpID: 1, Text: "en attente"}) // never confirmed: outside the cache

	msgs := w.Msgs()
	if len(msgs) != 1 || msgs[0].ID != 7 {
		t.Fatalf("msgs: %+v", msgs)
	}
	if msgs[0].Media == m.Media {
		t.Fatal("Media shared with the item")
	}
	if msgs[0].Media.Frames != nil {
		t.Fatal("frames copied into the cache")
	}
	msgs[0].Text = "modifié"
	msgs[0].Media.Label = "modifié"
	if m.Text != "salut" || m.Media.Label != "[photo]" || len(m.Media.Frames) != 1 {
		t.Fatalf("the item followed the copy: %+v %+v", m, m.Media)
	}
}

func TestFinalCacheFlushWaitsForOlderWrite(t *testing.T) {
	c := cache.New(t.TempDir(), 20)
	u := newCacheUI(t, c)
	w := u.ws.New(true)
	w.Chat = &model.Chat{Net: model.NetTelegram, ID: 1}
	w.Upsert(&model.Msg{ID: 7, ChatID: 1, Text: "latest"})
	u.dirty[w.Chat.Key()] = true
	release := make(chan struct{})
	u.bg(func() {
		<-release
		c.SaveHistory(1, []model.Msg{{ID: 7, ChatID: 1, Text: "older"}})
	})
	time.AfterFunc(20*time.Millisecond, func() { close(release) })
	u.flushCache(true)
	u.bgWait.Wait()
	msgs, err := c.LoadHistory(1)
	if err != nil || len(msgs) != 1 || msgs[0].Text != "latest" {
		t.Fatalf("final cache: %+v, %v", msgs, err)
	}
}

func msgRange(from, to int) []*model.Msg {
	var out []*model.Msg
	for id := from; id <= to; id++ {
		out = append(out, &model.Msg{ID: id, ChatID: 1})
	}
	return out
}

func TestMergeAppendsNewer(t *testing.T) {
	w := &Window{}
	w.Merge(msgRange(800, 1000)) // cached history
	w.Merge(msgRange(950, 1010)) // network answer: overlap then new messages
	if len(w.Items) != 211 {
		t.Fatalf("%d items, want 211 (800..1010 no duplicate)", len(w.Items))
	}
	for i, it := range w.Items {
		if it.Msg == nil || it.Msg.ID != 800+i {
			t.Fatalf("item %d: %+v, want ID %d", i, it.Msg, 800+i)
		}
	}
}

// TestMergeOlderAfterCache : lazy loading — a window built from the cache (ids
// 100..300) loads an older page from the network (1..99, PgUp at the top of
// the window), then gets a recent page (301..310, plain network).
func TestMergeOlderAfterCache(t *testing.T) {
	w := &Window{}
	w.Merge(msgRange(100, 300)) // cached slice
	w.Merge(msgRange(1, 99))    // older page, loaded on the way up
	w.Merge(msgRange(301, 310)) // recent network page
	if len(w.Items) != 310 {
		t.Fatalf("%d items, want 310 (1..310 no duplicate)", len(w.Items))
	}
	for i, it := range w.Items {
		if it.Msg == nil || it.Msg.ID != 1+i {
			t.Fatalf("item %d: %+v, want ID %d", i, it.Msg, 1+i)
		}
	}
	if got := w.OldestID(); got != 1 {
		t.Fatalf("OldestID = %d, want 1", got)
	}
}

func newCacheUI(t *testing.T, c *cache.Cache) *UI {
	t.Helper()
	return &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{AutoOpenDays: 7},
		caches: map[string]*cache.Cache{model.NetTelegram: c},
		chats:  map[model.ChatKey]*model.Chat{}, dirty: map[model.ChatKey]bool{},
		self: map[string]selfInfo{}, dialogsSeen: map[string]bool{}, reactList: map[string][]string{}}
}

func TestLoadCache(t *testing.T) {
	c := cache.New(t.TempDir(), 2000)
	recent := model.Chat{ID: 1, Title: "récent", LastDate: time.Now().Add(-time.Hour)}
	vieux := model.Chat{ID: 2, Title: "vieux", LastDate: time.Now().AddDate(0, 0, -30)}
	if err := c.SaveDialogs([]model.Chat{recent, vieux}, 10); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2} {
		if err := c.SaveHistory(id, []model.Msg{{ID: 4, ChatID: id, Text: "bonjour"}}); err != nil {
			t.Fatal(err)
		}
	}

	u := newCacheUI(t, c)
	u.loadCache()

	if len(u.chatList) != 2 || u.chats[tgk(1)] == nil || u.cacheSelf[model.NetTelegram] != 10 {
		t.Fatalf("chatList %+v, selfID %+v", u.chatList, u.cacheSelf)
	}
	// The gob does not carry a network: with no stamp at load time, u.net
	// would give nil and every call on the chat would go nowhere.
	if u.chats[tgk(1)].Net != model.NetTelegram {
		t.Fatalf("cache chat not stamped: Net %q", u.chats[tgk(1)].Net)
	}
	i := u.ws.ForChat(tgk(1))
	if i <= 0 || u.ws.Cur != 0 {
		t.Fatalf("window %d, current %d: want a hidden window", i, u.ws.Cur)
	}
	if u.ws.ForChat(tgk(2)) >= 0 {
		t.Fatal("window open for a chat outside auto-open")
	}
	w := u.ws.List[i]
	if w.Loaded || w.Full {
		t.Fatalf("Loaded %v, Full %v: the network must keep control", w.Loaded, w.Full)
	}
	if len(w.Items) != 2 || w.Items[0].Msg == nil || w.Items[1].Sys != cacheMark() {
		t.Fatalf("items: %+v", w.Items)
	}
	w.dropCacheMark()
	if len(w.Items) != 1 || w.Items[0].Msg.ID != 4 {
		t.Fatalf("after dropCacheMark: %+v", w.Items)
	}

	// Chat outside the automatic opening window: its history comes when the
	// window is bound.
	tard := u.ws.New(true)
	u.bindChat(tard, u.chats[tgk(2)])
	if tard.Chat.ID != 2 || len(tard.Items) != 2 || tard.Loaded || tard.Full {
		t.Fatalf("late bindChat: %+v", tard.Items)
	}
	if tard.Items[0].Msg.Net != model.NetTelegram {
		t.Fatalf("cache message not stamped: Net %q", tard.Items[0].Msg.Net)
	}
}

// TestLoadCacheAutreReseau: the Net set at load time is that of the cache
// read, not a constant — a cache filed under another network comes back
// under that network, chats and messages.
func TestLoadCacheAutreReseau(t *testing.T) {
	c := cache.New(t.TempDir(), 2000)
	if err := c.SaveDialogs([]model.Chat{{ID: 1, Title: "salon", LastDate: time.Now()}}, 10); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveHistory(1, []model.Msg{{ID: 4, ChatID: 1, Text: "bonjour"}}); err != nil {
		t.Fatal(err)
	}

	u := newCacheUI(t, c)
	u.caches = map[string]*cache.Cache{"discord": c}
	u.loadCache()

	k := model.ChatKey{Net: "discord", ID: 1}
	if u.chats[k] == nil {
		t.Fatalf("cache chat not stamped under its network: %+v", u.chats)
	}
	i := u.ws.ForChat(k)
	if i <= 0 {
		t.Fatalf("window %d: want a hidden window for %+v", i, k)
	}
	w := u.ws.List[i]
	if len(w.Items) == 0 || w.Items[0].Msg == nil || w.Items[0].Msg.Net != "discord" {
		t.Fatalf("cache message not stamped: %+v", w.Items)
	}
}

func TestLoadCacheAutreCompte(t *testing.T) {
	c := cache.New(t.TempDir(), 2000)
	if err := c.SaveDialogs([]model.Chat{{ID: 1, Title: "récent", LastDate: time.Now()}}, 10); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveHistory(1, []model.Msg{{ID: 4, ChatID: 1}}); err != nil {
		t.Fatal(err)
	}

	u := newCacheUI(t, c)
	u.loadCache()
	if u.ws.ForChat(tgk(1)) < 0 {
		t.Fatal("cache window missing before EvReady")
	}
	// Bot: no network call; dispatched, since the drop reads the net of the envelope.
	u.dispatch(model.Envelope{Net: model.NetTelegram, Ev: model.EvReady{SelfID: 99, Bot: true}})

	if len(u.ws.List) != 1 || len(u.chatList) != 0 || len(u.chats) != 0 {
		t.Fatalf("cache not discarded: %d windows, %d chats", len(u.ws.List), len(u.chatList))
	}
	if msgs, _ := c.LoadHistory(1); msgs != nil {
		t.Fatalf("history file kept: %+v", msgs)
	}
}

func TestSyncQueueOrder(t *testing.T) {
	now := time.Now()
	sans := &model.Chat{ID: 3, Title: "sans date"}
	vieux := &model.Chat{ID: 2, Title: "vieux", LastDate: now.AddDate(0, 0, -30)}
	frais := &model.Chat{ID: 1, Title: "récent", LastDate: now}
	in := []*model.Chat{sans, vieux, frais, frais} // pinned: twice in the list

	got := syncOrder(in)

	want := []*model.Chat{frais, vieux, sans}
	if len(got) != len(want) {
		t.Fatalf("%d chats, want %d", len(got), len(want))
	}
	for i, c := range want {
		if got[i] != c {
			t.Fatalf("position %d: %q, want %q", i, got[i].Title, c.Title)
		}
	}
	if in[0] != sans {
		t.Fatal("the original list was reordered")
	}
}

// The startup sync only sweeps a network whose Caps carry Sync. On Discord a
// history page costs two REST reads per chat: sweeping the whole chat list at
// every login would be a self-bot fingerprint, and would bring nothing —
// opening a window loads its page anyway.
func TestSyncSkipsNetWithoutSync(t *testing.T) {
	u := newCacheUI(t, cache.New(t.TempDir(), 2000))
	u.ctx = context.Background()
	dc := &fakeBackend{}                      // zero Caps: no Sync
	tg := &fakeBackend{caps: model.AllCaps()} // Telegram: swept as before
	u.nets = map[string]model.Backend{model.NetDiscord: dc, model.NetTelegram: tg}
	u.chatList = []*model.Chat{
		{Net: model.NetDiscord, ID: 1, Title: "#general"},
		{Net: model.NetTelegram, ID: 2, Title: "Céline"},
	}

	u.syncStart(model.NetDiscord)
	if dc.since != 0 {
		t.Fatalf("discord: %d history reads at login, want none", dc.since)
	}

	u.syncStart(model.NetTelegram)
	if tg.since != 1 {
		t.Fatalf("telegram: %d history reads, want 1", tg.since)
	}
}

func TestSyncMergeHistory(t *testing.T) {
	old := []model.Msg{{ID: 8, Text: "vieux"}, {ID: 9, Text: "connu"}}
	got := mergeHistory(old, []model.Msg{{ID: 9, Text: "doublon"}, {ID: 10, Text: "neuf"}})
	if len(got) != 3 {
		t.Fatalf("%d messages, want 3: %+v", len(got), got)
	}
	for i, want := range []int{8, 9, 10} {
		if got[i].ID != want {
			t.Fatalf("message %d: id %d, want %d", i, got[i].ID, want)
		}
	}
	if got[1].Text != "connu" {
		t.Fatalf("duplicate: %q, want the version already cached", got[1].Text)
	}
}

// TestBindChatAutoMedia: media replayed from the disk cache starts
// downloading when the window opens. Real bug case: the startup sync
// answered "0 new messages", so the window is already Loaded and no
// EvHistory will come to cover these ids — without autoMediaWin the photos
// stay labels until the next F4.
func TestBindChatAutoMedia(t *testing.T) {
	dir := t.TempDir()
	c := cache.New(dir, 2000)
	date := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	chat := &model.Chat{Net: model.NetTelegram, ID: 1, Title: "Céline"} // stamped the way loadCache does it
	msgs := []model.Msg{
		{ID: 4, ChatID: 1, Date: date, Media: &model.Media{Kind: model.MediaPhoto,
			Loc: &tg.InputPhotoFileLocation{ID: 4}, Ext: ".jpg", Size: 43 * 1024}},
		{ID: 5, ChatID: 1, Date: date, Media: &model.Media{Kind: model.MediaPhoto,
			Loc: &tg.InputPhotoFileLocation{ID: 5}, Ext: ".jpg", Size: 40 << 20}}, // above the threshold
	}
	if err := c.SaveHistory(1, msgs); err != nil {
		t.Fatal(err)
	}
	// File already there: tgc.Download returns control on the os.Stat, no network.
	if err := os.WriteFile(filepath.Join(dir, media.FileName(chat.Key(), chat.Title, 4, date, ".jpg")), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	u := newCacheUI(t, c)
	u.chats[chat.Key()] = chat
	u.images = "kitty"
	u.cfg.AutoMediaMaxKB = 5120
	u.cfg.DownloadDir = dir
	u.t = &term.Term{Cols: 80, Rows: 24}
	ev := make(chan model.Event, 8)
	u.nets = map[string]model.Backend{model.NetTelegram: tgc.New(tgc.Config{}, ev)}

	w := u.ws.New(true)
	u.bindChat(w, chat)
	w.Loaded = true // sync: "0 new messages"
	for _, it := range w.Items {
		if it.Msg != nil && it.Msg.Media != nil && it.Msg.Media.State != model.MediaNone {
			t.Fatalf("media %d replayed in state %v", it.Msg.ID, it.Msg.Media.State)
		}
	}

	u.goTo(u.ws.ForChat(chat.Key())) // click in the sidebar on an already open chat

	states := map[int]model.MediaState{}
	for _, it := range w.Items {
		if it.Msg != nil && it.Msg.Media != nil {
			states[it.Msg.ID] = it.Msg.Media.State
		}
	}
	if states[4] != model.MediaLoading {
		t.Fatalf("cache media: state %v, want MediaLoading", states[4])
	}
	if states[5] != model.MediaNone {
		t.Fatalf("media above the threshold: state %v, want MediaNone", states[5])
	}
	// Download is fire-and-forget: wait for its event, else the goroutine
	// re-creates the directory behind the cleanup of t.TempDir().
	select {
	case <-ev:
	case <-time.After(2 * time.Second):
		t.Fatal("no EvDownloaded")
	}
}

// TestSaveDialogsWithoutIdentity: a network that has not said who we are yet
// keeps its file untouched. Writing 0 as its self id would make the cache look
// like nobody's, and the "other account" drop would never fire again.
func TestSaveDialogsWithoutIdentity(t *testing.T) {
	a, b := cache.New(t.TempDir(), 2000), cache.New(t.TempDir(), 2000)
	if err := a.SaveDialogs([]model.Chat{{ID: 1, Title: "a"}}, 10); err != nil {
		t.Fatal(err)
	}
	if err := b.SaveDialogs([]model.Chat{{ID: 2, Title: "b"}}, 20); err != nil {
		t.Fatal(err)
	}
	u := newCacheUI(t, a)
	u.caches["discord"] = b                                     // no EvReady on discord
	u.self[model.NetTelegram] = selfInfo{ID: 10, Name: "alice"} // telegram alone knows us
	u.chatList = []*model.Chat{{Net: model.NetTelegram, ID: 1, Title: "a"}}

	u.saveDialogs()
	u.bgWait.Wait() // every write landed: what follows is a decision, not a race

	if chats, self, err := a.LoadDialogs(); err != nil || self != 10 || len(chats) != 1 {
		t.Fatalf("telegram gob not written: %+v selfID %d err %v", chats, self, err)
	}
	chats, self, err := b.LoadDialogs()
	if err != nil || self != 20 || len(chats) != 1 || chats[0].ID != 2 {
		t.Fatalf("discord gob rewritten with no identity: %+v selfID %d err %v", chats, self, err)
	}
}

// TestMergeFillsHole : a page holding messages missing from the middle of the
// window (gateway down while they came, then live messages after it) puts them
// at their place, not at the top of the window where nobody sees them.
func TestMergeFillsHole(t *testing.T) {
	w := &Window{}
	w.Merge(msgRange(1, 100))
	w.Merge(msgRange(201, 210)) // live messages after the cut
	w.Merge(msgRange(150, 210)) // recent page after the reconnection
	if len(w.Items) != 161 {
		t.Fatalf("%d items, want 161 (1..100, 150..210 no duplicate)", len(w.Items))
	}
	for i := 1; i < len(w.Items); i++ {
		if w.Items[i].Msg.ID <= w.Items[i-1].Msg.ID {
			t.Fatalf("item %d: id %d after %d", i, w.Items[i].Msg.ID, w.Items[i-1].Msg.ID)
		}
	}
}
