package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
)

func TestNotifyArgs(t *testing.T) {
	title, body := notifyArgs("hi\x01there;now\nend", strings.Repeat("a", 50)+";"+strings.Repeat("b", 300))
	if title != "hi there,now end" {
		t.Fatalf("title = %q", title)
	}
	want := strings.Repeat("a", 50) + "," + strings.Repeat("b", 149)
	if body != want {
		t.Fatalf("body : longueur %d, attendu %d", len(body), len(want))
	}
}

// Where the name is the identity (IRC), my nick as a word in a room
// ("chris: are you there?") is a highlight: it rings and marks the window hot,
// as the hooks see it. A muted room stays quiet all the same.
func TestRegressionIRCHighlightAlerts(t *testing.T) {
	const net = "irc:libera"
	var out bytes.Buffer
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{Bell: true},
		t: term.NewOffscreen(&out, 80, 24), nets: map[string]model.Backend{net: &fakeBackend{caps: model.Caps{NameIsID: true}}},
		conn: map[string]bool{}, chats: map[model.ChatKey]*model.Chat{}, dirty: map[model.ChatKey]bool{},
		self: map[string]selfInfo{net: {ID: 9, Name: "chris"}}}
	room := &model.Chat{Net: net, ID: 3, Kind: model.ChatGroup, Title: "#go"}
	say := func(id int) (hot, rang bool) {
		out.Reset()
		u.dispatch(model.Envelope{Net: net, Ev: model.EvNewMessage{Chat: room,
			Msg: model.Msg{ID: id, ChatID: room.ID, From: "bob", FromID: 7, Text: "chris: are you there?"}}})
		u.t.Flush()
		w := u.ws.List[u.ws.ForChat(room.Key())]
		hot, w.Hot = w.Hot, false
		return hot, strings.Contains(out.String(), "\a")
	}
	if hot, rang := say(1); !hot || !rang {
		t.Fatalf("highlight: hot %v, bell %v; want both", hot, rang)
	}
	u.muted = map[model.ChatKey]bool{room.Key(): true}
	u.alertAt = time.Time{} // past the gap between two alerts
	if hot, rang := say(2); hot || rang {
		t.Fatalf("highlight in a muted room: hot %v, bell %v; want neither", hot, rang)
	}
}

// A private message drawn in the aggregate view the user reads is seen: it
// is marked read there, and rings no bell.
func TestRegressionNoBellForAggregateShown(t *testing.T) {
	var out bytes.Buffer
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{Bell: true},
		t: term.NewOffscreen(&out, 80, 24), nets: map[string]model.Backend{netTelegram: &fakeBackend{}}, conn: map[string]bool{},
		focused: true, aggregate: true, chats: map[model.ChatKey]*model.Chat{}, dirty: map[model.ChatKey]bool{}, self: map[string]selfInfo{}}
	dm := &model.Chat{Net: netTelegram, ID: 1, Kind: model.ChatUser, Title: "alice"}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvNewMessage{Chat: dm, Msg: model.Msg{ID: 1, ChatID: 1, Text: "psst", From: "alice"}}})
	u.t.Flush()
	if strings.Contains(out.String(), "\a") {
		t.Fatal("bell for a message shown in the aggregate")
	}
}
