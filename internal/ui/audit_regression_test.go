package ui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/cache"
	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

func TestRegressionQueryNetworkIsolation(t *testing.T) {
	u, a, room, _ := queryUI()
	netB := ircNet("second")
	b := &queryBackend{}
	b.caps = model.AllCaps()
	u.nets[netB] = b
	roomB := &model.Chat{Net: netB, ID: 3, Title: "Second room", Kind: model.ChatGroup}
	u.remember(roomB)
	wa, wb := u.winFor(room), u.winFor(roomB)
	u.query(wa, "alice", false)
	u.query(wb, "alice", false)
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvChat{Request: a.requests[0], Query: "alice", Chat: &model.Chat{ID: 10, Title: "alice"}}})
	if len(a.resolves) != 1 || len(b.resolves) != 1 || (wb.Target != nil && wb.Target.Net != netB) {
		t.Fatalf("lookup crossed networks: requests A=%v B=%v; B target=%+v", a.resolves, b.resolves, wb.Target)
	}
	u.dispatch(model.Envelope{Net: netB, Ev: model.EvChat{Request: b.requests[0], Query: "alice", Chat: &model.Chat{ID: 10, Title: "alice"}}})
	if wa.Target == nil || wa.Target.Net != room.Net || wb.Target == nil || wb.Target.Net != netB {
		t.Fatal("each lookup must select its own network")
	}
}

func TestAuthQueueRestoresDraft(t *testing.T) {
	u, _, room, _ := queryUI()
	u.goTo(u.ws.ForChat(room.Key()))
	w := u.view()
	u.ed.Set("unfinished message")
	a, b := make(chan string, 1), make(chan string, 1)
	u.authStart(netTelegram, model.EvAuthPrompt{Secret: true, Reply: a})
	u.ed.Set("password")
	u.authStart(netDiscord, model.EvAuthPrompt{Reply: b})
	u.submit()
	if u.promptNet != netDiscord || u.ed.String() != "" {
		t.Fatal("queued prompt inherited the previous input")
	}
	u.ed.Set("code")
	u.submit()
	for ch, want := range map[chan string]string{a: "password", b: "code"} {
		select {
		case got := <-ch:
			if got != want {
				t.Fatalf("reply %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("authentication reply missing")
		}
	}
	if u.prompt != nil || u.view() != w || u.ed.String() != "unfinished message" {
		t.Fatal("login did not restore the original window and draft")
	}
}

func TestStoppedSessionReplyIgnored(t *testing.T) {
	u, _, room, _ := queryUI()
	old, cancelOld := context.WithCancel(u.ctx)
	defer cancelOld()
	current, cancelCurrent := context.WithCancel(u.ctx)
	defer cancelCurrent()
	u.netCtx = map[string]context.Context{room.Net: current}
	u.dispatch(model.Envelope{Net: room.Net, Session: old, Ev: model.EvNewMessage{Chat: room, Msg: model.Msg{ID: 99, ChatID: room.ID, Text: "old session"}}})
	u.dispatch(model.Envelope{Net: room.Net, Session: old, Ev: model.EvStopped{}})
	if len(u.winFor(room).Items) != 0 || u.nets[room.Net] == nil {
		t.Fatal("old session changed the new session")
	}
	u.dispatch(model.Envelope{Net: room.Net, Session: current, Ev: model.EvNewMessage{Chat: room, Msg: model.Msg{ID: 100, ChatID: room.ID, Text: "current session"}}})
	if len(u.winFor(room).Items) != 1 {
		t.Fatal("current session reply lost")
	}
}

func TestRefreshKeepsLiveEditAndMedia(t *testing.T) {
	started := time.Now()
	md := &model.Media{Kind: model.MediaPhoto, Loc: "expired", State: model.MediaReady, Path: "/cached/photo.png", Frames: [][]byte{{1}}}
	m := &model.Msg{ID: 10, Text: "cached", Media: md}
	w := &Window{Items: []*Item{{Msg: m}}}
	w.Merge([]*model.Msg{{ID: 10, Text: "server", Media: &model.Media{Kind: model.MediaPhoto, Loc: "renewed"}}}, started)
	if w.Items[0].Msg != m || m.Text != "server" || m.Media != md || md.Loc != "renewed" || md.Path == "" || len(md.Frames) != 1 {
		t.Fatal("refresh lost shared identity, server fields, or loaded media")
	}
	m.Text, m.LiveAt = "live edit", started.Add(time.Second)
	w.Merge([]*model.Msg{{ID: 10, Text: "stale response"}}, started)
	if m.Text != "live edit" {
		t.Fatal("history replaced a newer live edit")
	}
}

func TestIRCRekeyPreservesHistory(t *testing.T) {
	u, _, _, _ := queryUI()
	network := ircNet("test")
	c := &model.Chat{Net: network, ID: 20, Title: "alice["}
	u.remember(c)
	u.listChat(c)
	w := u.winFor(c)
	w.Upsert(&model.Msg{Net: network, ChatID: c.ID, ID: 100, Text: "memory"})
	cc := cache.New(t.TempDir(), 100)
	u.caches = map[string]*cache.Cache{network: cc}
	if err := cc.SaveHistory(20, []model.Msg{{ChatID: 20, ID: 90, Text: "disk"}}); err != nil {
		t.Fatal(err)
	}
	u.aliases = map[model.ChatKey]string{c.Key(): "friend"}
	u.rekeyChats(network, func(string) int64 { return 30 })
	key := model.ChatKey{Net: network, ID: 30}
	msgs, err := cc.LoadHistory(30)
	if err != nil || len(msgs) != 1 || msgs[0].ChatID != 30 || msgs[0].Text != "disk" {
		t.Fatalf("history migration: %v, %v", msgs, err)
	}
	if w.Chat.ID != 30 || w.Items[0].Msg.ChatID != 30 || u.aliases[key] != "friend" {
		t.Fatal("runtime references did not follow the new key")
	}
}

func TestRegressionFilteredAggregateRead(t *testing.T) {
	u, b, room, _ := queryUI()
	u.focused, u.aggregate = true, true
	u.ws.Cur = 0
	u.setNetFilter(netDiscord)
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvNewMessage{Chat: room, Msg: model.Msg{ID: 100, ChatID: room.ID, Text: "hidden", Date: time.Now()}}})
	if b.markRead != 0 || room.Unread != 1 {
		t.Fatalf("hidden message marked read: RPCs=%d unread=%d", b.markRead, room.Unread)
	}
}

func TestRegressionReconnectRefresh(t *testing.T) {
	w := &Window{}
	w.Upsert(&model.Msg{ID: 10, Text: "old text"})
	w.Merge([]*model.Msg{{ID: 10, Text: "corrected text", Edited: true}})
	if w.Items[0].Msg.Text != "corrected text" {
		t.Fatalf("server refresh discarded: %+v", w.Items[0].Msg)
	}
}

func TestRegressionAccountChange(t *testing.T) {
	u, b, room, _ := queryUI()
	u.self = map[string]selfInfo{}
	u.cacheSelf = map[string]int64{}
	u.conn = map[string]bool{}
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvReady{SelfID: 111, SelfName: "first account"}})
	u.winFor(room).Upsert(&model.Msg{Net: room.Net, ChatID: room.ID, ID: 10, Text: "first account private message"})
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvStopped{}})
	u.nets[room.Net] = b // /telegram login: the backend is back before its EvReady
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvReady{SelfID: 222, SelfName: "second account"}})
	if u.chats[room.Key()] != nil || u.ws.ForChat(room.Key()) >= 0 {
		t.Fatal("first account chat and history remain after second account login with no startup cache")
	}
}

func TestRegressionPromptOwnership(t *testing.T) {
	u, _, _, _ := queryUI()
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvAuthPrompt{Question: "Password", Secret: true, Reply: make(chan string, 1)}})
	u.ed.Set("fictional-secret")
	u.dispatch(model.Envelope{Net: netDiscord, Ev: model.EvAuthPrompt{Question: "Scan QR", Reply: make(chan string, 1)}})
	if u.prompt != nil && !u.prompt.Secret && u.ed.String() == "fictional-secret" {
		t.Fatal("secret input remains in editor after another network installs a visible prompt")
	}
}

func TestRegressionQRDoneOwnership(t *testing.T) {
	u, _, _, _ := queryUI()
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvAuthPrompt{Question: "Code", Reply: make(chan string, 1)}})
	u.dispatch(model.Envelope{Net: netDiscord, Ev: model.EvQRDone{}})
	if u.prompt == nil {
		t.Fatal("Discord QR completion removed the Telegram prompt")
	}
}

func TestRegressionRecentActivity(t *testing.T) {
	u, _, room, _ := queryUI()
	room.LastDate = time.Now().AddDate(0, 0, -30)
	when := time.Now()
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvNewMessage{Chat: room, Msg: model.Msg{ID: 100, ChatID: room.ID, Text: "new message", Date: when}}})
	if !room.LastDate.Equal(when) || room.TopMessage != 100 {
		t.Fatalf("activity metadata not updated: LastDate=%v TopMessage=%d", room.LastDate, room.TopMessage)
	}
}

func TestRegressionOpenActiveExtension(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "opened")
	script := "#!/bin/sh\nprintf '%s' \"$1\" > \"$TTYLOOM_AUDIT_MARKER\"\n"
	if err := os.WriteFile(filepath.Join(dir, "xdg-open"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("TTYLOOM_AUDIT_MARKER", marker)
	u, _, _, _ := queryUI()
	path := filepath.Join(dir, "received.desktop")
	u.open(path)
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		if got, err := os.ReadFile(marker); err == nil && string(got) == path {
			t.Fatal("active extension passed to desktop opener")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRegressionSecretRendering(t *testing.T) {
	u, _, _, _ := queryUI()
	var out bytes.Buffer
	u.t = term.NewOffscreen(&out, 100, 30)
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvAuthPrompt{Question: "Password", Secret: true, Reply: make(chan string, 1)}})
	u.ed.Set("fictional-secret")
	u.dispatch(model.Envelope{Net: netDiscord, Ev: model.EvAuthPrompt{Question: "Scan QR", Reply: make(chan string, 1)}})
	u.draw()
	u.t.Flush()
	if strings.Contains(out.String(), "fictional-secret") {
		t.Fatal("fictional password appeared in terminal output")
	}
}

func TestRegressionDCCResolveCollision(t *testing.T) {
	u, b, room, _ := queryUI()
	network := ircNet("test")
	irc := &queryBackend{}
	irc.caps = model.AllCaps()
	u.nets[network] = irc
	path := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(path, []byte("fictional data"), 0600); err != nil {
		t.Fatal(err)
	}
	u.resolve(u.winFor(room), "alice", false, []model.Backend{irc}, false, path)
	request := irc.requests[0]
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvChat{Request: request, Query: "alice", Chat: &model.Chat{ID: 40, Title: "alice"}}})
	if b.file != 0 || len(u.lookups) != 1 {
		t.Fatal("unrelated network consumed a pending file send")
	}
	u.dispatch(model.Envelope{Net: network, Ev: model.EvChat{Request: request, Query: "alice", Chat: &model.Chat{ID: 50, Title: "alice"}}})
	if irc.file != 1 || len(u.lookups) != 0 {
		t.Fatal("file did not reach the requested IRC recipient")
	}
}

func TestRegressionIRCAliasRoundTrip(t *testing.T) {
	want := model.ChatKey{Net: ircNet("libera"), ID: 123}
	got, ok := parseAliasKey(aliasKey(want))
	if !ok || got != want {
		t.Fatalf("IRC alias key does not round trip: encoded=%q got=%+v ok=%v", aliasKey(want), got, ok)
	}
}

func TestRegressionClosedWindowCache(t *testing.T) {
	u, _, _, _ := queryUI()
	network := ircNet("local")
	b := &queryBackend{}
	b.caps = model.AllCaps()
	u.nets[network] = b
	room := &model.Chat{Net: network, ID: 7, Title: "room"}
	u.remember(room)
	cc := cache.New(t.TempDir(), 100)
	u.caches = map[string]*cache.Cache{network: cc}
	u.dirty = map[model.ChatKey]bool{}
	u.dispatch(model.Envelope{Net: network, Ev: model.EvNewMessage{Chat: room, Msg: model.Msg{ID: 10, ChatID: room.ID, Text: "unsaved IRC message", Date: time.Now()}}})
	u.ws.Cur = u.ws.ForChat(room.Key())
	if !u.dirty[room.Key()] {
		t.Fatal("fixture did not dirty history")
	}
	u.closeWindow()
	u.flushCache(true)
	msgs, err := cc.LoadHistory(room.ID)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("closing before flush lost IRC history: messages=%d err=%v", len(msgs), err)
	}
}

func TestClosingSearchKeepsFullHistory(t *testing.T) {
	u, _, room, _ := queryUI()
	cc := cache.New(t.TempDir(), 100)
	u.caches = map[string]*cache.Cache{room.Net: cc}
	u.dirty = map[model.ChatKey]bool{}
	w := u.winFor(room)
	w.Upsert(&model.Msg{Net: room.Net, ChatID: room.ID, ID: 10, Text: "first"})
	w.Upsert(&model.Msg{Net: room.Net, ChatID: room.ID, ID: 11, Text: "second"})
	u.markDirty(room.Key())
	search := u.ws.New(false)
	search.Chat, search.Search = room, "second"
	search.Upsert(w.Items[1].Msg)
	u.closeWindow()
	u.flushCache(true)
	msgs, err := cc.LoadHistory(room.ID)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("closing search replaced chat history: %v, %v", msgs, err)
	}
}

// ircCacheUI : a UI with one IRC network (no server history) and its disk
// cache — the audit of 2026-09-22 (A01, A02, A04): on IRC the cache is the
// only copy of the history.
func ircCacheUI(net string, c *cache.Cache) *UI {
	u := listUI()
	u.ctx = context.Background()
	u.nets = map[string]model.Backend{net: &fakeBackend{caps: model.Caps{Resolve: true}}}
	u.caches = map[string]*cache.Cache{net: c}
	u.chats = map[model.ChatKey]*model.Chat{}
	u.dirty = map[model.ChatKey]bool{}
	u.self = map[string]selfInfo{net: {ID: 1001, Name: "me"}}
	u.conn = map[string]bool{}
	u.dialogsSeen = map[string]bool{}
	u.reactList = map[string][]string{}
	u.lastTyping = map[model.ChatKey]time.Time{}
	u.partsCache = map[model.ChatKey]partsEntry{}
	u.typing = map[model.ChatKey]typing{}
	u.presence = map[model.ChatKey]string{}
	u.avatars = map[model.ChatKey]*model.Media{}
	return u
}

// The nick was taken at login: ircevent registered "me_0" and EvReady carries
// another SelfID. An IRC network has no account — its cache stays.
func TestRegressionIRCNickCollisionKeepsHistory(t *testing.T) {
	const net = "irc:libera"
	c := cache.New(t.TempDir(), 2000)
	room := model.Chat{ID: 42, Title: "#go", Kind: model.ChatGroup}
	if err := c.SaveDialogs([]model.Chat{room}, 1001); err != nil { // previous session: nick "me"
		t.Fatal(err)
	}
	if err := c.SaveHistory(42, []model.Msg{{ID: 7, ChatID: 42, Text: "only copy of this IRC line", Date: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	u := ircCacheUI(net, c)
	u.loadCache()
	u.dispatch(model.Envelope{Net: net, Ev: model.EvReady{SelfID: 2002, SelfName: "me_0"}})
	msgs, _ := c.LoadHistory(42)
	if len(msgs) == 0 || u.chats[model.ChatKey{Net: net, ID: 42}] == nil {
		t.Fatalf("IRC history dropped after a nick change at login: %d cached messages left, chat known: %v",
			len(msgs), u.chats[model.ChatKey{Net: net, ID: 42}] != nil)
	}
}

// The query partner changes nick, or /part: EvChatGone closes the window and
// drops the entry; the history file stays, with the last unsaved line in it.
func TestRegressionIRCChatGoneKeepsHistory(t *testing.T) {
	const net = "irc:libera"
	c := cache.New(t.TempDir(), 2000)
	if err := c.SaveHistory(77, []model.Msg{{ID: 1, ChatID: 77, Text: "private IRC line", Date: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	u := ircCacheUI(net, c)
	alice := &model.Chat{Net: net, ID: 77, Title: "alice", Kind: model.ChatUser}
	u.remember(alice)
	u.listChat(alice)
	u.winFor(alice)
	u.dispatch(model.Envelope{Net: net, Ev: model.EvNewMessage{Chat: alice, Msg: model.Msg{ID: 2, ChatID: 77, Text: "not flushed yet", Date: time.Now()}}})
	u.dispatch(model.Envelope{Net: net, Ev: model.EvChatGone{ChatID: 77}})
	u.bgWait.Wait() // the list is written in the background: let it land before the temp dir goes
	if u.chats[alice.Key()] != nil || u.ws.ForChat(alice.Key()) >= 0 {
		t.Fatal("chat gone: its entry or its window is still there")
	}
	if msgs, _ := c.LoadHistory(77); len(msgs) != 2 {
		t.Fatalf("private IRC history on disk after the chat went: %d messages, want 2", len(msgs))
	}
}

// /clear then one line: the flush merges the window into the file instead of
// replacing it — 50 cached lines + 1.
func TestRegressionClearKeepsIRCHistory(t *testing.T) {
	const net = "irc:libera"
	c := cache.New(t.TempDir(), 2000)
	var old []model.Msg
	for i := 1; i <= 50; i++ {
		old = append(old, model.Msg{ID: i, ChatID: 5, Text: "old", Date: time.Now()})
	}
	if err := c.SaveHistory(5, old); err != nil {
		t.Fatal(err)
	}
	u := ircCacheUI(net, c)
	room := &model.Chat{Net: net, ID: 5, Title: "#go", Kind: model.ChatGroup}
	u.remember(room)
	u.listChat(room)
	u.winFor(room)
	u.ws.Cur = u.ws.ForChat(room.Key())
	input(u, "/clear")
	u.dispatch(model.Envelope{Net: net, Ev: model.EvNewMessage{Chat: room, Msg: model.Msg{ID: 51, ChatID: 5, Text: "new", Date: time.Now()}}})
	u.flushCache(true)
	if msgs, _ := c.LoadHistory(5); len(msgs) != 51 {
		t.Fatalf("/clear + one message left %d messages in the IRC history file (was 50)", len(msgs))
	}
}

// The merge of the flush: on a message both hold, the window copy (an edit, a
// deletion) wins over the file copy.
func TestRegressionFlushMergeWindowWins(t *testing.T) {
	c := cache.New(t.TempDir(), 2000)
	if err := c.SaveHistory(1, []model.Msg{{ID: 5, ChatID: 1, Text: "before the edit"}, {ID: 6, ChatID: 1, Text: "kept"}}); err != nil {
		t.Fatal(err)
	}
	u := newCacheUI(t, c)
	w := u.ws.New(true)
	w.Chat = &model.Chat{Net: netTelegram, ID: 1}
	w.Upsert(&model.Msg{ID: 5, ChatID: 1, Text: "edited"})
	u.dirty[w.Chat.Key()] = true
	u.flushCache(true)
	msgs, _ := c.LoadHistory(1)
	if len(msgs) != 2 || msgs[0].Text != "edited" || msgs[1].Text != "kept" {
		t.Fatalf("merged file: %+v", msgs)
	}
}

// No readable list of the chats at start: the history files stay where they
// are the only copy (KeepOld, IRC), and are set aside — not erased — elsewhere.
func TestRegressionLoadCacheWithoutDialogs(t *testing.T) {
	for _, keepOld := range []bool{true, false} {
		dir := filepath.Join(t.TempDir(), "net")
		c := cache.New(dir, 2000)
		c.KeepOld = keepOld
		if err := c.SaveHistory(42, []model.Msg{{ID: 7, ChatID: 42, Text: "line"}}); err != nil {
			t.Fatal(err)
		}
		u := newCacheUI(t, c)
		u.loadCache()
		msgs, _ := c.LoadHistory(42)
		aside, _ := filepath.Glob(dir + ".old-*")
		if keepOld && len(msgs) != 1 {
			t.Fatalf("KeepOld: history dropped with no dialogs file: %+v", msgs)
		}
		if !keepOld && (len(msgs) != 0 || len(aside) != 1) {
			t.Fatalf("history not set aside with no dialogs file: %+v, %v", msgs, aside)
		}
	}
}

// "/msg ends …" with no chat named "ends": a command that sends at once takes
// an exact name or a unique prefix, never a piece inside "Friends room".
func TestRegressionMsgNoSubstringTarget(t *testing.T) {
	u, b, _, peer := queryUI()
	input(u, "/msg ends private text")
	if len(b.sends) != 0 {
		t.Fatalf("/msg to an unknown name delivered to %v (%q) by a substring match", b.sends, b.text)
	}
	input(u, "/msg blo hi")
	if len(b.sends) != 1 || b.sends[0] != peer.Key() {
		t.Fatalf("/msg by unique prefix: %v", b.sends)
	}
}

// A click on a link whose text is not its target asks first and names the
// target; y then opens it. A link that shows its target opens at once.
func TestRegressionMaskedLinkAsks(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no xdg-open: each open leaves its error line
	u := hoverUI()
	u.t = term.NewOffscreen(&bytes.Buffer{}, 80, 10)
	w := u.view()
	w.Upsert(&model.Msg{ID: 1, From: "alice", FromID: 7, Date: time.Now(), Text: "bank.example example.org",
		Entities: []model.Span{{Start: 0, End: 12, Kind: model.SpanURL, URL: "https://evil.example/login"},
			{Start: 13, End: 24, Kind: model.SpanURL, URL: "https://example.org/"}}})
	u.draw()
	opens := func() int {
		n := 0
		for _, it := range w.Items {
			if strings.HasPrefix(it.Sys, "xdg-open") {
				n++
			}
		}
		return n
	}
	click := func(url string) {
		t.Helper()
		x0, cols := u.layout()
		for y := range u.hits {
			for x := 0; x < cols; x++ {
				if hitAt(u.hits, x, y).url == url {
					u.key(term.Key{Code: term.Mouse, Mouse: term.MouseEvent{X: x0 + x, Y: y, Press: true}})
					return
				}
			}
		}
		t.Fatalf("%s not drawn", url)
	}
	click("https://evil.example/login")
	if u.ask == nil || !strings.Contains(u.ask.q, "https://evil.example/login") || opens() != 0 {
		t.Fatalf("masked link: question %+v, %d opens", u.ask, opens())
	}
	u.ask.at = time.Now().Add(-time.Second)
	u.key(term.Key{Rune: 'y'})
	if opens() != 1 {
		t.Fatalf("masked link confirmed: %d opens", opens())
	}
	click("https://example.org/")
	if u.ask != nil || opens() != 2 {
		t.Fatalf("plain link: question %+v, %d opens", u.ask, opens())
	}
}

// A mailto: with header fields never reaches OSC 8 nor xdg-open: attach= made
// mail clients attach a local file.
func TestRegressionMailtoAttach(t *testing.T) {
	const s = "mailto:a@example.org?attach=/home/user/.ssh/id_ed25519"
	if render.SafeURL(s) || openable(s) {
		t.Fatal("mailto with attach= accepted for xdg-open")
	}
}

// The body of a desktop notification is markup for the daemon: escaped, and
// only &, < and > (an apostrophe stays one).
func TestRegressionNotifyMarkup(t *testing.T) {
	_, body := desktopArgs("t", `<b>URGENT</b> <a href="https://evil.example">bank</a> & l'appli`)
	if want := `&lt;b&gt;URGENT&lt;/b&gt; &lt;a href="https://evil.example"&gt;bank&lt;/a&gt; &amp; l'appli`; body != want {
		t.Fatalf("notify-send body %q, want %q", body, want)
	}
}

type deleteBackend struct {
	queryBackend
	deleted []int
}

func (b *deleteBackend) Delete(_ context.Context, _ *model.Chat, id int) {
	b.deleted = append(b.deleted, id)
}

// "dy…" typed over one of my selected messages: the y comes within 300 ms of
// the question and cancels it; a y after that deletes.
func TestRegressionTypedYCancelsDelete(t *testing.T) {
	u, _, room, _ := queryUI()
	b := &deleteBackend{}
	b.caps = model.AllCaps()
	u.nets[room.Net] = b
	w := u.winFor(room)
	u.goTo(u.ws.ForChat(room.Key()))
	w.Upsert(&model.Msg{Net: room.Net, ChatID: room.ID, ID: 5, Out: true, Text: "mine", Date: time.Now()})
	u.setSel(w, w.Items[len(w.Items)-1])
	u.key(term.Key{Rune: 'd'})
	u.key(term.Key{Rune: 'y'})
	if len(b.deleted) != 0 || u.ask != nil {
		t.Fatalf("typed y: deleted %v, question %+v", b.deleted, u.ask)
	}
	u.key(term.Key{Rune: 'd'})
	u.ask.at = time.Now().Add(-300 * time.Millisecond)
	u.key(term.Key{Rune: 'y'})
	if len(b.deleted) != 1 || b.deleted[0] != 5 {
		t.Fatalf("y after 300 ms: deleted %v", b.deleted)
	}
}

// The question of a login prompt can carry a remote name (Discord QR: the
// account that scanned it): drawn cleaned, never as a terminal sequence.
func TestRegressionPromptQuestionCleaned(t *testing.T) {
	u, _, _, _ := queryUI()
	var out bytes.Buffer
	u.t = term.NewOffscreen(&out, 100, 30)
	u.dispatch(model.Envelope{Net: netDiscord, Ev: model.EvAuthPrompt{Question: "Scanned by \x1b]0;pwned\x07", Reply: make(chan string, 1)}})
	u.draw()
	u.t.Flush()
	if strings.Contains(out.String(), "\x1b]0;pwned") {
		t.Fatal("prompt question written raw to the terminal")
	}
}

// An admin with no @username (★ and the (me) mark in the box): the mention
// offers, inserts and sends the name alone.
func TestRegressionMentionWithoutBoxMarks(t *testing.T) {
	g := &model.Chat{ID: 7, Kind: model.ChatGroup, Title: "grp"}
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{}, t: &term.Term{Cols: 80, Rows: 24},
		partsCache: map[model.ChatKey]partsEntry{g.Key(): {at: time.Now(),
			lines: []model.Participant{{Text: "★ Bob (me)", Name: "Bob", Query: "42"}}}}}
	u.ws.List = append(u.ws.List, &Window{Chat: g})
	u.ws.Cur = 1
	u.ed.Set("yo @b")
	u.mentionScan()
	if u.mention == nil || !u.mentionKey(term.Key{Code: term.Enter}) || u.ed.String() != "yo @Bob " {
		t.Fatalf("pick: %q", u.ed.String())
	}
	segs, _ := u.mentionSegs(g, []model.Seg{{Text: "yo @Bob"}})
	if want := []model.Seg{{Text: "yo "}, {Text: "Bob", Kind: model.SegMention, UserID: 42}}; !slices.Equal(segs, want) {
		t.Fatalf("segs: %+v", segs)
	}
}

// viewText : the drawing of v, as the screen reads it.
func viewText(u *UI, v *Window) string {
	var sb strings.Builder
	for _, l := range v.Lines(u.optsFor(v)) {
		sb.WriteString(render.LineText(l) + "\n")
	}
	return sb.String()
}

// A read receipt sweeps only the views of its chat: its window, the
// aggregate and a /search result (with a copy of its own) still get the
// new ticks.
func TestRegressionReadReceiptRedrawsChatViews(t *testing.T) {
	u, _, room, _ := queryUI()
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvNewMessage{Chat: room,
		Msg: model.Msg{ChatID: room.ID, ID: 5, Out: true, Text: "hello", Date: time.Now()}}})
	found := u.ws.New(true)
	found.Search, found.Chat = "hello", room
	found.Upsert(&model.Msg{Net: room.Net, ChatID: room.ID, ID: 5, Out: true, Text: "hello", Date: time.Now()})
	views := []*Window{u.winFor(room), u.agg, found}
	for _, v := range views {
		if got := viewText(u, v); !strings.Contains(got, "✓") || strings.Contains(got, "✓✓") {
			t.Fatalf("%s before the receipt: %q", v.Name(), got)
		}
	}
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvReadOutbox{ChatID: room.ID, MaxID: 5}})
	for _, v := range views {
		if got := viewText(u, v); !strings.Contains(got, "✓✓") {
			t.Fatalf("%s after the receipt: %q", v.Name(), got)
		}
	}
}

// A history page no longer repaints every window: the first page of a window
// with unread messages still settles the view on the redline, 3rd row, with
// the day separators drawn.
func TestRegressionHistoryScrollsToRedline(t *testing.T) {
	u, _, room, _ := queryUI()
	u.cfg.Redline = true
	w := u.winFor(room)
	w.MarkID = 40 // read up to 40 when the window opened
	start := time.Date(2026, 9, 1, 0, 30, 0, 0, time.UTC)
	var page []model.Msg
	for i := 1; i <= 100; i++ { // one message per hour: 5 days
		page = append(page, model.Msg{ChatID: room.ID, ID: i, From: "alice", Text: fmt.Sprintf("m%d", i),
			Date: start.Add(time.Duration(i) * time.Hour)})
	}
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvHistory{ChatID: room.ID, Msgs: page}})
	lines, items, idx := w.LineItems(u.optsFor(w))
	if idx < 0 || items[idx+1] == nil || items[idx+1].Msg.ID != 41 {
		t.Fatalf("redline at line %d, not right before message 41", idx)
	}
	if top := len(lines) - w.Scroll - u.viewRows(); idx-top != 2 {
		t.Fatalf("redline on row %d of the view, want 2", idx-top)
	}
	seps := 0
	for _, it := range items {
		if it == nil {
			seps++
		}
	}
	if seps != 6 {
		t.Fatalf("%d separator lines, want 5 days and the redline", seps)
	}
}

// A history page that refreshes a message the aggregate shows too (an edit
// made while offline) redraws it there, with no repaint of every window.
func TestRegressionHistoryRedrawsSharedMessage(t *testing.T) {
	u, _, room, _ := queryUI()
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvNewMessage{Chat: room,
		Msg: model.Msg{ChatID: room.ID, ID: 7, Text: "typo", Date: time.Now()}}})
	if got := viewText(u, u.agg); !strings.Contains(got, "typo") {
		t.Fatalf("aggregate: %q", got)
	}
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvHistory{ChatID: room.ID,
		Msgs: []model.Msg{{ChatID: room.ID, ID: 7, Text: "fixed", Date: time.Now()}}}})
	if got := viewText(u, u.agg); !strings.Contains(got, "fixed") {
		t.Fatalf("aggregate after the page: %q", got)
	}
}

// The day separators are kept from one frame to the next, and still follow
// the width and the language of the frame.
func TestRegressionDaySeparatorFollowsWidthAndLang(t *testing.T) {
	w := &Window{}
	w.Upsert(&model.Msg{ID: 1, Date: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), From: "a", Text: "x"})
	o := render.Opts{Width: 40, Theme: theme.Terminal(), Images: "off"}
	sep := func() string { return render.LineText(w.Lines(o)[0]) }
	if got := sep(); !strings.Contains(got, "1 septembre 2026") || render.Width(got) != 40 {
		t.Fatalf("separator: %q", got)
	}
	o.Width = 60
	if got := sep(); render.Width(got) != 60 {
		t.Fatalf("separator after a resize: %q", got)
	}
	i18n.Set("en")
	t.Cleanup(func() { i18n.Set("fr") }) // TestMain sets fr for the package
	if got := sep(); !strings.Contains(got, "September 1, 2026") {
		t.Fatalf("separator after /set lang en: %q", got)
	}
}

// The line of a chat stays highlighted while its context menu is open, and
// only then: a menu dropped by a login prompt left the highlight behind.
func TestRegressionSideMenuHighlight(t *testing.T) {
	u, _, room, _ := queryUI()
	u.side, u.sideW = sideChats, 26
	lit := func() bool {
		lines, _ := u.sideBlock(-1)
		for _, l := range lines {
			if strings.Contains(render.LineText(l), room.Title) {
				return l.Spans[0].Style.Reverse
			}
		}
		t.Fatal("no sidebar line for the room")
		return false
	}
	for y := 0; y < u.t.Rows && u.menu == nil; y++ {
		if u.sideChatAt(y) == room {
			u.openMenu(1, y)
		}
	}
	if u.menu == nil || !lit() {
		t.Fatalf("menu open: menu %v, line not highlighted", u.menu != nil)
	}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvAuthPrompt{Question: "Code", Reply: make(chan string, 1)}})
	if u.menu != nil || lit() {
		t.Fatalf("menu dropped by the prompt: menu %v, line still highlighted", u.menu != nil)
	}
}
