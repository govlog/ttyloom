package ui

import (
	"testing"

	"github.com/govlog/ttyloom/internal/model"
)

// stamp sets Net on every Chat/Msg the event carries; events with a bare
// ChatID stay as they are (their net comes from the envelope).
func TestStamp(t *testing.T) {
	n := stamp("discord", model.EvNewMessage{Msg: model.Msg{ID: 1, ChatID: 7}, Chat: &model.Chat{ID: 7}}).(model.EvNewMessage)
	if n.Msg.Net != "discord" || n.Chat.Net != "discord" {
		t.Fatalf("EvNewMessage: %+v %+v", n.Msg, n.Chat)
	}
	d := stamp("discord", model.EvDialogs{Chats: []*model.Chat{{ID: 1}, {ID: 2}}}).(model.EvDialogs)
	for _, c := range d.Chats {
		if c.Net != "discord" {
			t.Fatalf("EvDialogs: %+v", c)
		}
	}
	h := stamp("tg", model.EvHistory{ChatID: 7, Msgs: []model.Msg{{ID: 1}, {ID: 2}}}).(model.EvHistory)
	for _, m := range h.Msgs {
		if m.Net != "tg" {
			t.Fatalf("EvHistory: %+v", m)
		}
	}
	g := stamp("tg", model.EvSearchGlobal{Hits: []model.SearchHit{{Chat: &model.Chat{ID: 1}}}}).(model.EvSearchGlobal)
	if g.Hits[0].Chat.Net != "tg" {
		t.Fatalf("EvSearchGlobal: %+v", g.Hits[0].Chat)
	}
	r := stamp("tg", model.EvReadOutbox{ChatID: 7, MaxID: 3}).(model.EvReadOutbox)
	if r != (model.EvReadOutbox{ChatID: 7, MaxID: 3}) {
		t.Fatalf("bare ChatID changed: %+v", r)
	}
}

// A nil Chat is a normal answer (chat not found, no peer in the hit): stamp
// walks past it instead of panicking on the UI goroutine.
func TestStampNilChat(t *testing.T) {
	if c := stamp("tg", model.EvChat{Query: "@nobody", Err: "not found"}).(model.EvChat).Chat; c != nil {
		t.Fatalf("EvChat: %+v", c)
	}
	stamp("tg", model.EvSearchGlobal{Hits: []model.SearchHit{{MsgID: 1}}})
}
