package ui

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

type queryBackend struct {
	fakeBackend
	sends    []model.ChatKey
	text     []string
	resolves []string
	typed    []model.ChatKey
}

func (b *queryBackend) Send(_ context.Context, c *model.Chat, text string, _ int64) {
	b.sends, b.text = append(b.sends, c.Key()), append(b.text, text)
}
func (b *queryBackend) Typing(_ context.Context, c *model.Chat, cancel bool) {
	if !cancel {
		b.typed = append(b.typed, c.Key())
	}
}
func (b *queryBackend) Resolve(_ context.Context, name string, _ bool) {
	b.resolves = append(b.resolves, name)
}

func (b *queryBackend) SendReply(ctx context.Context, c *model.Chat, text string, _ int, tmp int64) {
	b.Send(ctx, c, text, tmp)
}

func (b *queryBackend) SendStyled(ctx context.Context, c *model.Chat, segs []model.Seg, tmp int64) {
	b.Send(ctx, c, segs[0].Text, tmp)
}

func (b *queryBackend) SendPre(ctx context.Context, c *model.Chat, text string, tmp int64) {
	b.Send(ctx, c, text, tmp)
}

func queryUI() (*UI, *queryBackend, *model.Chat, *model.Chat) {
	u := listUI()
	b := &queryBackend{}
	b.caps = model.AllCaps()
	u.ctx = context.Background()
	u.nets = map[string]model.Backend{model.NetTelegram: b}
	u.chats = map[model.ChatKey]*model.Chat{}
	u.lastTyping = map[model.ChatKey]time.Time{}
	room := &model.Chat{Net: model.NetTelegram, ID: 1, Title: "Friends room", Kind: model.ChatGroup}
	peer := &model.Chat{Net: model.NetTelegram, ID: 2, Title: "Blop", Username: "blop"}
	for _, c := range []*model.Chat{room, peer} {
		u.remember(c)
		u.listChat(c)
		w := u.winFor(c)
		w.Loaded = true
	}
	return u, b, room, peer
}

func input(u *UI, line string) {
	u.ed.Set(line)
	u.key(term.Key{Code: term.Enter})
}

func TestQueryPersistentAcrossViews(t *testing.T) {
	for _, mode := range []string{"aggregate", "channel", "status", "search", "debug", "empty"} {
		t.Run(mode, func(t *testing.T) {
			u, b, room, peer := queryUI()
			switch mode {
			case "aggregate":
				u.aggregate = true
			case "channel":
				u.ws.Cur = 1
			case "search":
				u.ws.New(false).Chat, u.ws.Current().Search = room, "find"
			case "debug":
				u.showDebug = true
			case "empty":
				u.ws.New(false)
			}
			view, index := u.view(), u.ws.Cur
			base := view.Chat
			input(u, "/qu blop")
			input(u, "blah blah")
			u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvNewMessage{Chat: room,
				Msg: model.Msg{ID: 10, ChatID: room.ID, Text: "channel activity", Date: time.Now()}}})
			input(u, "blop blop")
			if !slices.Equal(b.sends, []model.ChatKey{peer.Key(), peer.Key()}) {
				t.Fatalf("activity stole the query target: %v", b.sends)
			}
			if u.view() != view || u.ws.Cur != index || view.Chat != base {
				t.Fatal("query switched or rebound the original view")
			}
			if len(u.agg.Items) < 3 {
				t.Fatal("aggregate stopped receiving messages")
			}
			var prompt strings.Builder
			u.drawInput(&prompt, 24, 0, 80)
			if !strings.Contains(prompt.String(), "→ @Blop") {
				t.Fatalf("prompt: %s", prompt.String())
			}
			u.ed.Set("@b")
			u.mentionScan()
			if u.mention == nil || u.mention.items[0].Query != "@blop" {
				t.Fatal("mentions used the base chat")
			}
			u.mention = nil
			u.sendTyping(view)
			if !slices.Equal(b.typed, []model.ChatKey{peer.Key()}) {
				t.Fatalf("typing: %v", b.typed)
			}
			input(u, "/j Friends room")
			input(u, "to the channel")
			if b.sends[len(b.sends)-1] != room.Key() {
				t.Fatal("multiword channel target failed")
			}
			input(u, "/q")
			if view.Target != nil || u.view() != view {
				t.Fatal("query did not close in place")
			}
			if mode == "aggregate" || mode == "channel" || mode == "search" {
				input(u, "back home")
				if b.sends[len(b.sends)-1] != room.Key() {
					t.Fatal("base routing was not restored")
				}
			}
		})
	}
}

func TestQueryWindowSwitchAndReceipts(t *testing.T) {
	u, b, room, peer := queryUI()
	u.ws.Cur = 1
	input(u, "/q blop")
	input(u, "private")
	u.dispatch(model.Envelope{Net: peer.Net, Ev: model.EvSent{ChatID: peer.ID, TmpID: u.tmpID, ID: 50}})
	w := u.winFor(peer)
	if len(w.Items) != 1 || w.Items[0].Msg.ID != 50 || w.Items[0].Msg.Pending {
		t.Fatal("receipt lost")
	}
	u.goTo(0)
	input(u, "/j Friends room")
	u.goTo(1)
	input(u, "still private")
	if b.sends[len(b.sends)-1] != peer.Key() {
		t.Fatal("switching windows lost its query")
	}
	input(u, "/q")
	input(u, "back in the room")
	if b.sends[len(b.sends)-1] != room.Key() {
		t.Fatal("query closed the underlying channel")
	}
	u.goTo(0)
	if u.sendWin().Chat != room {
		t.Fatal("another window's target was cleared")
	}
}

func TestQueryLateResolution(t *testing.T) {
	u, b, room, peer := queryUI()
	u.aggregate = true
	input(u, "/q @unknown")
	input(u, "/q")
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvChat{Query: "@unknown", Chat: &model.Chat{ID: 3, Title: "Unknown"}}})
	if u.agg.Target != nil {
		t.Fatal("late answer reopened a closed query")
	}
	input(u, "/q @another")
	input(u, "/q blop")
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvChat{Query: "@another", Chat: &model.Chat{ID: 4, Title: "Another"}}})
	if u.agg.Target != peer {
		t.Fatal("old lookup replaced the new target")
	}
	// Two windows can wait for the same lookup without stealing each other.
	input(u, "/q @shared")
	u.goTo(1)
	input(u, "/q @shared")
	u.dispatch(model.Envelope{Net: room.Net, Ev: model.EvChat{Query: "@shared", Chat: &model.Chat{ID: 5, Title: "Shared"}}})
	if u.agg.Target == nil || u.agg.Target.ID != 5 || u.ws.List[1].Target != u.agg.Target {
		t.Fatal("shared lookup lost a window")
	}
	if len(b.resolves) != 3 {
		t.Fatalf("duplicate resolution: %v", b.resolves)
	}
}

func TestQueryPendingKeepsDraftAndBlocksSends(t *testing.T) {
	u, b, _, _ := queryUI()
	u.ws.Cur = 1
	input(u, "/q @unknown")
	input(u, "do not send to the channel")
	if len(b.sends) != 0 || u.ed.String() != "do not send to the channel" {
		t.Fatal("sent to the old target or lost the draft while resolving")
	}
	input(u, "/me waits")
	input(u, "/send /nonexistent")
	u.openGifs("")
	u.pasteClip()
	u.sendTyping(u.view())
	if len(b.sends) != 0 || len(b.typed) != 0 || u.gifs != nil {
		t.Fatal("a send path used the old target while resolving")
	}
	u.dispatch(model.Envelope{Net: model.NetTelegram, Ev: model.EvChat{Query: "@unknown", Chat: &model.Chat{ID: 9, Title: "Unknown"}}})
	input(u, "now resolved")
	if len(b.sends) != 1 || b.sends[0].ID != 9 {
		t.Fatalf("resolved send: %v", b.sends)
	}
}

func TestQueryRepliesAndExplicitMessages(t *testing.T) {
	u, b, room, peer := queryUI()
	u.ws.Cur = 1
	input(u, "/q blop")
	input(u, "/me waves")
	u.sendWith(u.sendWin(), "a code block", true)
	if !slices.Equal(b.sends, []model.ChatKey{peer.Key(), peer.Key()}) {
		t.Fatalf("styled sends: %v", b.sends)
	}
	input(u, "/m Friends a one-off channel message")
	if b.sends[len(b.sends)-1] != room.Key() || u.view().Target != peer {
		t.Fatal("/m changed the query")
	}
	u.reply = &Item{Msg: &model.Msg{Net: room.Net, ChatID: room.ID, ID: 12, From: "friend", Text: "question"}}
	input(u, "reply to that channel message")
	if b.sends[len(b.sends)-1] != room.Key() {
		t.Fatal("reply followed the query instead of its message")
	}
	input(u, "private again")
	if b.sends[len(b.sends)-1] != peer.Key() {
		t.Fatal("reply consumed the persistent query")
	}
}

func TestQueryCrossNetworkAndRemoval(t *testing.T) {
	u, b, _, peer := queryUI()
	dc := &queryBackend{}
	u.nets[model.NetDiscord] = dc
	other := &model.Chat{Net: model.NetDiscord, ID: peer.ID, Title: "Discord peer"}
	u.listChat(u.remember(other))
	u.aggregate = true
	input(u, "/q Discord peer")
	u.setNetFilter(model.NetTelegram)
	input(u, "on Discord despite the filter")
	if len(b.sends) != 0 || !slices.Equal(dc.sends, []model.ChatKey{other.Key()}) {
		t.Fatal("query crossed networks")
	}
	u.dropChat(other.Key())
	if u.agg.Target != nil {
		t.Fatal("removed chat remains a send target")
	}
}

func TestQueryReplyAndEditInputContext(t *testing.T) {
	for _, mode := range []string{"reply", "edit"} {
		t.Run(mode, func(t *testing.T) {
			u, b, room, peer := queryUI()
			u.ws.Cur = 1
			input(u, "/q blop")
			it := &Item{Msg: &model.Msg{Net: room.Net, ChatID: room.ID, ID: 12, Text: "channel message"}}
			if mode == "reply" {
				u.reply = it
			} else {
				u.startEdit(it)
			}
			u.partsCache = map[model.ChatKey]partsEntry{room.Key(): {
				at: time.Now(), lines: []model.Participant{{Text: "Friend", Query: "@friend"}},
			}}
			u.ed.Set("@f")
			u.mentionScan()
			if u.mention == nil || u.mention.items[0].Query != "@friend" {
				t.Fatal("mentions followed the query instead of the selected message")
			}
			u.sendTyping(u.view())
			if !slices.Equal(b.typed, []model.ChatKey{room.Key()}) {
				t.Fatalf("typing went to the wrong recipient: %v", b.typed)
			}
			u.cancelMode()
			if u.inputChat(u.view()) != peer {
				t.Fatal("ending the message mode lost the query")
			}
		})
	}
}

func TestQueryEditLastUsesTarget(t *testing.T) {
	for _, aggregate := range []bool{false, true} {
		u, _, room, peer := queryUI()
		u.aggregate = aggregate
		if !aggregate {
			u.ws.Cur = 1
		}
		input(u, "/q blop")
		private := &model.Msg{Net: peer.Net, ChatID: peer.ID, ID: 20, Out: true, Text: "private"}
		public := &model.Msg{Net: room.Net, ChatID: room.ID, ID: 21, Out: true, Text: "public"}
		u.winFor(peer).Upsert(private)
		u.winFor(room).Upsert(public)
		u.agg.Upsert(private)
		u.agg.Upsert(public) // a later public message must not win
		u.key(term.Key{Code: term.Up})
		if u.edit == nil || u.edit.Msg.Key() != peer.Key() || u.ed.String() != "private" {
			t.Fatalf("aggregate=%v: Up edited the underlying channel", aggregate)
		}
		u.cancelMode()
		input(u, "/q @unknown")
		if u.editLast(u.view()) {
			t.Fatal("Up edited the old target while resolving")
		}
	}
}

// TestQueryLocalEcho : sent from the room to blop (/q, then a one-off /m),
// the message shows in the room window as [msg(Blop)] text, between the
// opening and closing notices, without entering the room's history; the
// receipt updates the echo (here a failure).
func TestQueryLocalEcho(t *testing.T) {
	u, _, _, peer := queryUI()
	u.ws.Cur = 1
	view := u.view()
	input(u, "/q blop")
	input(u, "hello blop")
	failed := u.tmpID
	input(u, "/q")
	input(u, "/m blop one-off")
	u.dispatch(model.Envelope{Net: peer.Net, Ev: model.EvSent{ChatID: peer.ID, TmpID: failed, Err: "boom"}})
	var lines []string
	for _, l := range view.Lines(render.Opts{Width: 80, Theme: u.th}) {
		lines = append(lines, strings.TrimRight(render.LineText(l), " "))
	}
	text := strings.Join(lines, "\n")
	want := []string{"*** " + i18n.T("query_target", "Blop"), "[msg(Blop)] hello blop", i18n.T("msg_failed", "boom"),
		"*** " + i18n.T("query_closed", "Blop"), "[msg(Blop)] … one-off"}
	at := -1
	for _, s := range want {
		i := strings.Index(text, s)
		if i < at {
			t.Fatalf("%q missing or out of order in:\n%s", s, text)
		}
		at = i
	}
	msgs := func(w *Window) (n int) {
		for _, it := range w.Items {
			if it.Msg != nil {
				n++
			}
		}
		return n
	}
	if msgs(view) != 0 || msgs(u.winFor(peer)) != 2 {
		t.Fatalf("history: room %d, blop %d", msgs(view), msgs(u.winFor(peer)))
	}
	if view.Target != nil {
		t.Fatal("/m opened the query mode")
	}
}
