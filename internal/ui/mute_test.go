package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// TestMute : a muted chat counts its unread messages but rings no bell, sends
// no notification and never turns hot; /unmute brings them back. The set is
// kept in muted.toml for the next start.
func TestMute(t *testing.T) {
	var out bytes.Buffer
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{Bell: true, Notify: "terminal"},
		t: term.NewOffscreen(&out, 80, 24), nets: map[string]model.Backend{netTelegram: &fakeBackend{}}, conn: map[string]bool{},
		focused: true, chats: map[model.ChatKey]*model.Chat{}, dirty: map[model.ChatKey]bool{}, self: map[string]selfInfo{}}
	dm := &model.Chat{Net: netTelegram, ID: 1, Kind: model.ChatUser, Title: "alice"}
	say := func(id int) string {
		u.t.Flush()
		out.Reset()
		u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvNewMessage{Chat: dm, Msg: model.Msg{ID: id, ChatID: 1, Text: "psst", From: "alice"}}})
		u.flushAlert(time.Now().Add(alertGap)) // past the rate limit of the alerts
		u.t.Flush()
		return out.String()
	}
	signalled := func(s string) bool { return strings.Contains(s, "\a") && strings.Contains(s, "\x1b]777;notify;") }

	if !signalled(say(1)) {
		t.Fatal("a private message must ring and notify before /mute")
	}
	w := u.ws.List[u.ws.ForChat(dm.Key())]
	u.muteCmd(w, "", true)
	if w.Hot {
		t.Error("/mute must stop the pulse already on")
	}
	if got := say(2); strings.Contains(got, "\a") || strings.Contains(got, "]777;") || w.Hot || w.Act != 2 {
		t.Errorf("muted: output %q, hot %v, act %d; want no signal, not hot, act 2", got, w.Hot, w.Act)
	}
	if m, _, _ := loadMuted(mutedPath()); !m[dm.Key()] {
		t.Error("muted.toml must hold the muted chat")
	}
	u.muteCmd(u.ws.List[0], "", true)
	if got := lastSys(u.ws.List[0]); !strings.Contains(got, "alice") {
		t.Errorf("/mute with no chat lists %q, want alice", got)
	}

	u.muteCmd(u.ws.List[0], "ali", false) // by a prefix of its name
	if !signalled(say(3)) || !w.Hot {
		t.Error("/unmute must bring the bell, the notification and the pulse back")
	}
	if m, _, _ := loadMuted(mutedPath()); len(m) != 0 {
		t.Errorf("muted.toml after /unmute: %v, want empty", m)
	}
}

// TestMuteSection : /mute with a section key (the key of /fold) mutes every
// chat of the section, the ones that come later too; a name that is not an
// exact key stays a chat name. The set is kept in muted.toml.
func TestMuteSection(t *testing.T) {
	t.Setenv("TTYLOOM_DIR", t.TempDir())
	const net = "discord"
	var out bytes.Buffer
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{Bell: true, Notify: "terminal"},
		t: term.NewOffscreen(&out, 80, 24), nets: map[string]model.Backend{net: &fakeBackend{}}, conn: map[string]bool{},
		focused: true, chats: map[model.ChatKey]*model.Chat{}, dirty: map[model.ChatKey]bool{}, self: map[string]selfInfo{net: {ID: 7}}}
	gen := &model.Chat{Net: net, ID: 10, Kind: model.ChatGroup, Title: "Gophers / #general", Group: "Gophers"}
	random := &model.Chat{Net: net, ID: 11, Kind: model.ChatGroup, Title: "Gophers / #random", Group: "Gophers"}
	id := 0
	ping := func(c *model.Chat) string { // a mention of me in c
		id++
		u.t.Flush()
		out.Reset()
		u.dispatch(model.Envelope{Net: net, Ev: model.EvNewMessage{Chat: c, Msg: model.Msg{ID: id, ChatID: c.ID, Text: "ping", From: "bob",
			Entities: []model.Span{{Kind: model.SpanMention, UserID: 7}}}}})
		u.flushAlert(time.Now().Add(alertGap))
		u.t.Flush()
		return out.String()
	}
	signalled := func(s string) bool { return strings.Contains(s, "\a") || strings.Contains(s, "\x1b]777;notify;") }
	win := func(c *model.Chat) *Window { return u.ws.List[u.ws.ForChat(c.Key())] }
	secs := func() map[string]bool { _, s, _ := loadMuted(mutedPath()); return s }

	if !signalled(ping(gen)) || !win(gen).Hot {
		t.Fatal("a mention must ring and pulse before /mute")
	}
	inAct := func(w *Window) bool { // w is in [Act: …]
		_, hits := u.actSpans(theme.Style{}, theme.Style{})
		return slices.ContainsFunc(hits, func(h actHit) bool { return h.w == w })
	}
	if !inAct(win(gen)) {
		t.Fatal("an unread window must be in [Act: …] before /mute")
	}
	u.muteCmd(u.ws.List[0], "discord:Goph", true) // not an exact key: a chat name, and no chat has it
	if len(secs()) != 0 || len(u.muted) != 0 {
		t.Fatalf("/mute discord:Goph muted sections %v, chats %v; want nothing", secs(), u.muted)
	}
	u.muteCmd(u.ws.List[0], "discord:gophers", true) // case apart
	if inAct(win(gen)) {
		t.Error("a window of a muted section must leave [Act: …]")
	}
	if win(gen).Hot {
		t.Error("/mute of a section must stop the pulse already on")
	}
	if got := ping(gen); signalled(got) || win(gen).Hot || win(gen).Act != 2 {
		t.Errorf("muted section: output %q, hot %v, act %d; want no signal, not hot, act 2", got, win(gen).Hot, win(gen).Act)
	}
	if got := ping(random); signalled(got) || win(random).Hot { // a channel never seen before the mute
		t.Errorf("a new channel of a muted section: output %q, hot %v; want no signal, not hot", got, win(random).Hot)
	}
	if !secs()["discord:Gophers"] {
		t.Errorf("muted.toml sections: %v, want discord:Gophers", secs())
	}
	u.muteCmd(u.ws.List[0], "", true)
	if got := lastSys(u.ws.List[0]); !strings.Contains(got, "discord:Gophers") {
		t.Errorf("/mute with no chat lists %q, want discord:Gophers", got)
	}
	u.muteCmd(win(gen), "", false) // /unmute in a chat muted by its section only
	if got := lastSys(win(gen)); !strings.Contains(got, "/unmute discord:Gophers") || !secs()["discord:Gophers"] {
		t.Errorf("/unmute in a chat of a muted section says %q, sections %v; want the command of the section, the section still muted", got, secs())
	}

	// The network is not up: no chat known, the section can still be unmuted.
	off := &UI{ws: NewWindows(), chats: map[model.ChatKey]*model.Chat{}}
	off.muted, off.mutedSecs, _ = loadMuted(mutedPath())
	off.muteCmd(off.ws.List[0], "discord:Gophers", false)
	if len(secs()) != 0 {
		t.Errorf("muted.toml sections after /unmute: %v, want empty", secs())
	}

	// A file written before sections existed.
	old := filepath.Join(t.TempDir(), "muted.toml")
	if err := os.WriteFile(old, []byte("chats = [\"telegram:1\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chats, s, err := loadMuted(old)
	if err != nil || !chats[model.ChatKey{Net: netTelegram, ID: 1}] || len(s) != 0 {
		t.Errorf("old muted.toml: chats %v, sections %v, err %v; want telegram:1, no section, no error", chats, s, err)
	}
}

// A chat whose exact name is also a section key keeps the command it always
// had: /mute telegram mutes the chat "Telegram", not every Telegram chat.
func TestMuteChatNameOverSection(t *testing.T) {
	t.Setenv("TTYLOOM_DIR", t.TempDir())
	u := netUI(netTelegram)
	svc := &model.Chat{Net: netTelegram, ID: 777000, Kind: model.ChatUser, Title: "Telegram"}
	u.chatList = append(u.chatList, svc)
	u.muteCmd(u.ws.List[0], "telegram", true)
	if !u.muted[svc.Key()] || len(u.mutedSecs) != 0 {
		t.Errorf("/mute telegram: chats %v, sections %v; want the chat Telegram only", u.muted, u.mutedSecs)
	}
}

// Tab after /mute and /unmute completes a section key too.
func TestMuteTabSection(t *testing.T) {
	for _, cmd := range []string{"/mute", "/unmute"} {
		u := foldUI()
		u.ed.Set(cmd + " discord:G")
		u.completeTab()
		if got := u.ed.String(); !strings.HasPrefix(got, cmd+" discord:Gophers") {
			t.Errorf("%s discord:G + Tab: draft %q, want the key discord:Gophers", cmd, got)
		}
	}
}

// Tab on the network part alone lists the servers when several are left.
func TestMuteTabSectionList(t *testing.T) {
	u := foldUI()
	u.chatList = append(u.chatList, &model.Chat{Net: netDiscord, ID: 4, Title: "Rusty Space / #general", Group: "Rusty Space", LastDate: time.Now()})
	u.ed.Set("/mute discord:")
	u.completeTab()
	if got := lastSys(u.view()); !strings.Contains(got, "discord:Gophers") || !strings.Contains(got, "discord:Rusty Space") {
		t.Errorf("/mute discord: + Tab lists %q, want both servers", got)
	}
}

// A burst of hot messages rings once at once, then once more for the last
// one when the gap is over — never once per message.
func TestAlertBurst(t *testing.T) {
	var out bytes.Buffer
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{Bell: true, Notify: "terminal"},
		t: term.NewOffscreen(&out, 80, 24), nets: map[string]model.Backend{netTelegram: &fakeBackend{}}, conn: map[string]bool{},
		focused: true, chats: map[model.ChatKey]*model.Chat{}, dirty: map[model.ChatKey]bool{}, self: map[string]selfInfo{}}
	for i := 1; i <= 50; i++ {
		dm := &model.Chat{Net: netTelegram, ID: int64(i), Kind: model.ChatUser, Title: "spam"}
		u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvNewMessage{Chat: dm, Msg: model.Msg{ID: i, ChatID: int64(i), Text: "hi", From: "spam"}}})
	}
	u.t.Flush()
	if n := strings.Count(out.String(), "\a"); n != 1 {
		t.Fatalf("%d bells for a burst, want 1", n)
	}
	u.flushAlert(time.Now().Add(alertGap))
	u.t.Flush()
	if n := strings.Count(out.String(), "\a"); n != 2 {
		t.Fatalf("%d bells after the gap, want 2", n)
	}
}

// The IRC casemapping rekey moves the keys of a network's chats: a muted room
// stays muted under its new key, in muted.toml too.
func TestMuteFollowsIRCRekey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TTYLOOM_DIR", dir)
	u, _, _, _ := queryUI()
	cfg, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	u.cfg = cfg
	network := ircNet("test")
	c := &model.Chat{Net: network, ID: 20, Title: "#go["}
	u.remember(c)
	u.listChat(c)
	u.muted = map[model.ChatKey]bool{c.Key(): true}
	u.rekeyChats(network, func(string) int64 { return 30 })
	key := model.ChatKey{Net: network, ID: 30}
	if m, _, _ := loadMuted(mutedPath()); !u.muted[key] || len(u.muted) != 1 || !m[key] || len(m) != 1 {
		t.Fatalf("muted after the rekey: %v, file %v; want only %v", u.muted, m, key)
	}
}

// An alert held by the rate limit is dropped when its message is read
// before it goes out.
func TestAlertDroppedOnceRead(t *testing.T) {
	var out bytes.Buffer
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{Bell: true, Notify: "terminal"},
		t: term.NewOffscreen(&out, 80, 24), nets: map[string]model.Backend{netTelegram: &fakeBackend{}}, conn: map[string]bool{},
		focused: true, chats: map[model.ChatKey]*model.Chat{}, dirty: map[model.ChatKey]bool{}, self: map[string]selfInfo{}}
	say := func(id int64) {
		dm := &model.Chat{Net: netTelegram, ID: id, Kind: model.ChatUser, Title: "alice"}
		u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvNewMessage{Chat: dm, Msg: model.Msg{ID: int(id), ChatID: id, Text: "hi", From: "alice"}}})
	}
	say(1) // rings at once
	say(2) // held by the gap
	u.goTo(u.ws.ForChat(model.ChatKey{Net: netTelegram, ID: 2}))
	u.flushAlert(time.Now().Add(alertGap))
	u.t.Flush()
	if n := strings.Count(out.String(), "\a"); n != 1 {
		t.Fatalf("%d bells, want 1: the held one was read before it went out", n)
	}
}

// /unmute of a chat that its section mutes too lifts the mute of the chat
// and says the section still mutes it, with the command that undoes that.
func TestRegressionUnmuteChatOfMutedSection(t *testing.T) {
	t.Setenv("TTYLOOM_DIR", t.TempDir())
	u, _, room, _ := queryUI()
	w := u.winFor(room)
	sec := sectionOf(room)
	u.muteCmd(w, "", true)
	u.muteCmd(w, sec, true)
	u.muteCmd(w, "", false)
	chats, secs, _ := loadMuted(mutedPath())
	if got := lastSys(w); !strings.Contains(got, "/unmute "+sec) || chats[room.Key()] || !secs[sec] || !u.isMuted(room) {
		t.Fatalf("/unmute says %q, muted.toml chats %v sections %v; want the chat unmuted, the section kept and its command said", got, chats, secs)
	}
}
