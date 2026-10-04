package ui

import (
	"context"
	"slices"
	"testing"

	"github.com/govlog/ttyloom/internal/model"
)

// actionBackend : a network with an action message of its own (IRC).
type actionBackend struct {
	queryBackend
	acts [][]model.Seg
}

func (b *actionBackend) SendAction(_ context.Context, _ *model.Chat, segs []model.Seg, _ int64) {
	b.acts = append(b.acts, segs)
}

// /me on a network with actions of its own sends the text alone, its styles
// kept — the network writes the "* nick" — and the echo reads like a received
// action, with no <nick> before it.
func TestMeAsAction(t *testing.T) {
	u, _, room, _ := queryUI()
	b := &actionBackend{}
	b.caps = model.AllCaps()
	u.nets[netTelegram] = b
	u.self = map[string]selfInfo{netTelegram: {ID: 5, Name: "me"}}
	w := u.winFor(room)
	u.sendMe(w, "waves \x02hard")
	if want := []model.Seg{{Text: "waves "}, {Text: "hard", Bold: true}}; len(b.acts) != 1 || !slices.Equal(b.acts[0], want) || len(b.sends) != 0 {
		t.Fatalf("actions %+v, sends %v: want the text alone as an action", b.acts, b.text)
	}
	m := w.Items[len(w.Items)-1].Msg
	if !m.Action || m.Text != "* me waves hard" {
		t.Fatalf("echo %+v: want an action line", m)
	}
}
