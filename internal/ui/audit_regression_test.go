package ui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/cache"
	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
)

func TestRegressionQueryNetworkIsolation(t *testing.T) {
	u, a, room, _ := queryUI()
	netB := model.IRCNet("second")
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
	u.authStart(model.NetTelegram, model.EvAuthPrompt{Secret: true, Reply: a})
	u.ed.Set("password")
	u.authStart(model.NetDiscord, model.EvAuthPrompt{Reply: b})
	u.submit()
	if u.promptNet != model.NetDiscord || u.ed.String() != "" {
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
	network := model.IRCNet("test")
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
	u.rekeyIRC(network, func(string) int64 { return 30 })
	key := model.ChatKey{Net: network, ID: 30}
	msgs, err := cc.LoadHistory(30)
	if err != nil || len(msgs) != 1 || msgs[0].ChatID != 30 || msgs[0].Text != "disk" {
		t.Fatalf("history migration: %v, %v", msgs, err)
	}
	if w.Chat.ID != 30 || w.Items[0].Msg.ChatID != 30 || u.aliases[key] != "friend" {
		t.Fatal("runtime references did not follow the new key")
	}
}

func TestIRCChannelsPersistInOrder(t *testing.T) {
	u, _, _, _ := queryUI()
	var err error
	u.cfg, err = config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u.cfg.IRC = []*config.IRCConfig{{Name: "test", Host: "irc.example", Nick: "me"}}
	for _, channels := range [][]string{{"#first"}, {"#second"}} {
		u.dispatch(model.Envelope{Net: model.IRCNet("test"), Ev: model.EvIRCChannels{Channels: channels}})
	}
	saved, err := config.LoadFrom(filepath.Dir(u.cfg.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.IRCByName("test").Channels; len(got) != 1 || got[0] != "#second" {
		t.Fatalf("saved channels: %v", got)
	}
}

// The ignore list of a network goes back to its [[irc]] table, like the
// channels: the masks of /ignore survive a restart.
func TestIRCIgnoresPersist(t *testing.T) {
	u, _, _, _ := queryUI()
	var err error
	u.cfg, err = config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u.cfg.IRC = []*config.IRCConfig{{Name: "test", Host: "irc.example", Nick: "me"}}
	u.dispatch(model.Envelope{Net: model.IRCNet("test"), Ev: model.EvIRCIgnores{Ignores: []string{"spammer!*@*"}}})
	saved, err := config.LoadFrom(filepath.Dir(u.cfg.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.IRCByName("test").Ignores; len(got) != 1 || got[0] != "spammer!*@*" {
		t.Fatalf("saved ignores: %v", got)
	}
}

func TestRegressionFilteredAggregateRead(t *testing.T) {
	u, b, room, _ := queryUI()
	u.focused, u.aggregate = true, true
	u.ws.Cur = 0
	u.setNetFilter(model.NetDiscord)
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
	u, _, room, _ := queryUI()
	u.self = map[string]selfInfo{}
	u.cacheSelf = map[string]int64{}
	u.conn = map[string]bool{}
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvReady{SelfID: 111, SelfName: "first account"}})
	u.winFor(room).Upsert(&model.Msg{Net: room.Net, ChatID: room.ID, ID: 10, Text: "first account private message"})
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvStopped{}})
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvReady{SelfID: 222, SelfName: "second account"}})
	if u.chats[room.Key()] != nil || u.ws.ForChat(room.Key()) >= 0 {
		t.Fatal("first account chat and history remain after second account login with no startup cache")
	}
}

func TestRegressionPromptOwnership(t *testing.T) {
	u, _, _, _ := queryUI()
	u.dispatch(model.Envelope{Net: model.NetTelegram, Ev: model.EvAuthPrompt{Question: "Password", Secret: true, Reply: make(chan string, 1)}})
	u.ed.Set("fictional-secret")
	u.dispatch(model.Envelope{Net: model.NetDiscord, Ev: model.EvAuthPrompt{Question: "Scan QR", Reply: make(chan string, 1)}})
	if u.prompt != nil && !u.prompt.Secret && u.ed.String() == "fictional-secret" {
		t.Fatal("secret input remains in editor after another network installs a visible prompt")
	}
}

func TestRegressionQRDoneOwnership(t *testing.T) {
	u, _, _, _ := queryUI()
	u.dispatch(model.Envelope{Net: model.NetTelegram, Ev: model.EvAuthPrompt{Question: "Code", Reply: make(chan string, 1)}})
	u.dispatch(model.Envelope{Net: model.NetDiscord, Ev: model.EvQRDone{}})
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
	u.dispatch(model.Envelope{Net: model.NetTelegram, Ev: model.EvAuthPrompt{Question: "Password", Secret: true, Reply: make(chan string, 1)}})
	u.ed.Set("fictional-secret")
	u.dispatch(model.Envelope{Net: model.NetDiscord, Ev: model.EvAuthPrompt{Question: "Scan QR", Reply: make(chan string, 1)}})
	u.draw()
	u.t.Flush()
	if strings.Contains(out.String(), "fictional-secret") {
		t.Fatal("fictional password appeared in terminal output")
	}
}

func TestRegressionDCCResolveCollision(t *testing.T) {
	u, b, room, _ := queryUI()
	network := model.IRCNet("test")
	irc := &queryBackend{}
	irc.caps = model.AllCaps()
	u.nets[network] = irc
	path := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(path, []byte("fictional data"), 0600); err != nil {
		t.Fatal(err)
	}
	u.resolve(u.winFor(room), "alice", false, []model.Backend{irc}, false, path)
	request := irc.requests[0]
	u.dispatch(model.Envelope{Net: model.NetTelegram, Ev: model.EvChat{Request: request, Query: "alice", Chat: &model.Chat{ID: 40, Title: "alice"}}})
	if b.file != 0 || len(u.lookups) != 1 {
		t.Fatal("unrelated network consumed a pending file send")
	}
	u.dispatch(model.Envelope{Net: network, Ev: model.EvChat{Request: request, Query: "alice", Chat: &model.Chat{ID: 50, Title: "alice"}}})
	if irc.file != 1 || len(u.lookups) != 0 {
		t.Fatal("file did not reach the requested IRC recipient")
	}
}

func TestRegressionIRCAliasRoundTrip(t *testing.T) {
	want := model.ChatKey{Net: model.IRCNet("libera"), ID: 123}
	got, ok := parseAliasKey(aliasKey(want))
	if !ok || got != want {
		t.Fatalf("IRC alias key does not round trip: encoded=%q got=%+v ok=%v", aliasKey(want), got, ok)
	}
}

func TestRegressionClosedWindowCache(t *testing.T) {
	u, _, _, _ := queryUI()
	network := model.IRCNet("local")
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
