package ui

import (
	"testing"

	"github.com/govlog/ttyloom/internal/model"
)

// A new name of the account on a network (IRC /nick, a nick taken back after
// a 433, a reconnection under another one) is the one mentions, hooks and /me
// use from then on — not the one of the first registration.
func TestSelfNameFollowsNick(t *testing.T) {
	u, _, room, _ := queryUI()
	u.self = map[string]selfInfo{netTelegram: {ID: 5, Name: "chris"}}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvSelfName{Name: "chris_\x1b[31m"}})
	if got := u.selfOf(room.Net); got.Name != "chris_ [31m" || got.ID != 5 {
		t.Fatalf("self after the nick change: %+v, want the new name (cleaned), same id", got)
	}
}
