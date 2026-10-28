package ui

import (
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/model"
)

// TestPinnedBanner : EvPinned drives the banner — a fetch shows the newest
// pinned message (a click would jump to it), a live unpin of it clears it,
// and the pin flag of the loaded copy follows both ways.
func TestPinnedBanner(t *testing.T) {
	u, _, w := msgUI(t)
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvPinned{ChatID: 5, Fetched: true,
		Top: &model.Msg{Net: netTelegram, ChatID: 5, ID: 2, From: "alice", Text: "deux", Pinned: true}}})
	text, jump, ok := u.pinBanner(w)
	if !ok || jump != 2 || !strings.Contains(text, "alice") || !strings.Contains(text, "deux") {
		t.Fatalf("banner after the fetch: %q jump %d ok %v", text, jump, ok)
	}
	if !w.Items[1].Msg.Pinned {
		t.Fatal("the loaded copy must carry the pin flag")
	}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvPinned{ChatID: 5, IDs: []int{2}, Pinned: false}})
	if _, _, ok := u.pinBanner(w); ok || w.Items[1].Msg.Pinned {
		t.Fatal("an unpin must clear the banner and the flag")
	}
}

// TestPinKey : the key "n" on a selected message asks the network to pin it,
// and to unpin it when it is already pinned.
func TestPinKey(t *testing.T) {
	u, b, w := msgUI(t)
	u.setSel(w, w.Items[1])
	u.selKey(w, 'n')
	w.Items[1].Msg.Pinned = true
	u.setSel(w, w.Items[1])
	u.selKey(w, 'n')
	if len(b.pins) != 2 || b.pins[0] != "2:true" || b.pins[1] != "2:false" {
		t.Fatalf("Pin calls: %v, want [2:true 2:false]", b.pins)
	}
}
