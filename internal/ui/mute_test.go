package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
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
	if m, _ := loadMuted(mutedPath()); !m[dm.Key()] {
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
	if m, _ := loadMuted(mutedPath()); len(m) != 0 {
		t.Errorf("muted.toml after /unmute: %v, want empty", m)
	}
}
