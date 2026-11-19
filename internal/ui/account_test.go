package ui

import (
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/model"
)

// Another account on the same network never sees the pinned message of the
// first one: a private chat with the same peer has the same key, and its
// banner stays empty until the second account reads its own pin.
func TestRegressionAccountChangeForgetsPins(t *testing.T) {
	u, b, _, peer := queryUI()
	u.self, u.cacheSelf, u.conn = map[string]selfInfo{}, map[string]int64{}, map[string]bool{}
	u.typing = map[model.ChatKey]typing{}
	u.dispatch(model.Envelope{Net: peer.Net, Ev: model.EvReady{SelfID: 111, SelfName: "first account"}})
	u.dispatch(model.Envelope{Net: peer.Net, Ev: model.EvPinned{ChatID: peer.ID, Fetched: true,
		Top: &model.Msg{Net: peer.Net, ChatID: peer.ID, ID: 4, From: "Blop", Text: "first account secret", Pinned: true}}})
	u.dispatch(model.Envelope{Net: peer.Net, Ev: model.EvStopped{}})
	u.nets[peer.Net] = b
	u.dispatch(model.Envelope{Net: peer.Net, Ev: model.EvReady{SelfID: 222, SelfName: "second account"}})
	u.dispatch(model.Envelope{Net: peer.Net, Ev: model.EvNewMessage{Chat: &model.Chat{ID: peer.ID, Title: "Blop", Kind: model.ChatUser},
		Msg: model.Msg{ChatID: peer.ID, ID: 1, From: "Blop", Text: "hi", Date: time.Now()}}})
	w := u.winFor(u.chats[peer.Key()])
	if text, _, ok := u.pinBanner(w); ok {
		t.Fatalf("second account shows the banner %q of the first one", text)
	}
}

// A local name (/rename) stays when another account logs in on the same
// network: the id names the same person or room for every account, so the
// first account finds its names again, and aliases.toml keeps them.
func TestAccountChangeKeepsLocalNames(t *testing.T) {
	u := noteStart(t, accountNet)()
	noteReady(u, 111)
	noteOpen(u, alice)
	input(u, "/rename Maman")
	noteRelogin(u, 222)
	noteRelogin(u, 111)
	noteOpen(u, alice)
	if got := u.title(u.view().Chat); got != "Maman" {
		t.Fatalf("first account back: the chat shows %q, want its local name %q", got, "Maman")
	}
	if al, err := loadAliases(aliasPath()); err != nil || len(al) != 1 {
		t.Fatalf("aliases.toml after two account changes: %v, error %v; want the one local name", al, err)
	}
}
