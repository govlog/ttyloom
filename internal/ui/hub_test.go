package ui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

// hubUI : the fake module with two networks, one running and ready.
func hubUI() (*UI, *fakeMod) {
	u, m := modUI("fake:a", "fake:b")
	u.netList = []string{"fake:a", "fake:b"}
	u.nets["fake:a"] = &fakeBackend{}
	u.self = map[string]selfInfo{"fake:a": {Name: "chris"}}
	m.canAdd = true
	u.hubReturn = map[string]bool{}
	return u, m
}

func hubText(u *UI) string {
	u.hubRefresh()
	var s string
	for _, l := range u.hub.Lines(u.th, u.hubRect().w) {
		s += render.LineText(l) + "\n"
	}
	return s
}

// The list: one line per network with its state, then the add line.
func TestHubList(t *testing.T) {
	u, _ := hubUI()
	u.openHub()
	txt := hubText(u)
	for _, want := range []string{"Fake a", i18n.T("hub_up", "chris"), "Fake b", i18n.T("hub_stopped"), i18n.T("hub_add", "Fake")} {
		if !strings.Contains(txt, want) {
			t.Fatalf("missing %q:\n%s", want, txt)
		}
	}
}

// Enter on the add line opens the page in the hub; Esc on the page goes back
// to the list; a submit adds the network, closes the hub and marks the
// network for the way back.
func TestHubPage(t *testing.T) {
	u, m := hubUI()
	u.openHub()
	u.hub.cur = 2 // the add line
	u.hubKey(term.Key{Code: term.Enter})
	if m.setups != 1 || u.form == nil || !strings.HasPrefix(u.form.title, i18n.T("hub_title")) {
		t.Fatalf("page: %d %+v", m.setups, u.form)
	}
	u.formKey(term.Key{Code: term.Esc})
	if u.form != nil || u.hub == nil {
		t.Fatal("Esc on the page must go back to the list")
	}
	u.hubKey(term.Key{Code: term.Enter})
	typeKeys(u, "c")
	u.formKey(term.Key{Code: term.Enter})
	if u.hub != nil || !u.hubReturn["fake:c"] {
		t.Fatalf("after submit: hub %v, return %v", u.hub, u.hubReturn)
	}
	u.dispatch(model.Envelope{Net: "fake:c", Ev: model.EvReady{SelfID: 1, SelfName: "me"}})
	if u.hub == nil || u.hubReturn["fake:c"] {
		t.Fatal("EvReady of the network must bring the hub back")
	}
}

// The actions follow the state; Connect marks the way back, a failing start
// (no backend after it) brings the hub back at once.
func TestHubActions(t *testing.T) {
	u, _ := hubUI()
	u.openHub()
	u.hub.cur = 1 // fake:b, stopped
	u.hubKey(term.Key{Code: term.Enter})
	labels := u.hub.actionLabels()
	if !slices.Equal(labels, []string{i18n.T("hub_act_connect"), i18n.T("hub_act_remove")}) {
		t.Fatalf("actions of a stopped network: %v", labels)
	}
	u.launch = func(context.Context, string) (model.Backend, error) { return nil, errors.New("token_cmd failed") }
	u.hubKey(term.Key{Code: term.Enter}) // Connect
	if u.hub == nil || u.hubReturn["fake:b"] {
		t.Fatalf("failed start: hub %v, return %v", u.hub, u.hubReturn)
	}
}

// Remove asks first; a y within 300 ms cancels, a later one removes. The
// keys go through u.key, the way the terminal sends them.
func TestHubRemoveConfirm(t *testing.T) {
	u, m := hubUI()
	u.openHub()
	u.hub.cur = 1
	u.hubKey(term.Key{Code: term.Enter})
	u.hub.cur = 1 // Remove
	u.hubKey(term.Key{Code: term.Enter})
	u.key(term.Key{Rune: 'y'}) // at once: typing, not an answer
	if len(m.removed) != 0 {
		t.Fatal("a y within 300 ms removed")
	}
	u.hub.cur = 1
	u.hubKey(term.Key{Code: term.Enter})
	u.hub.askAt = time.Now().Add(-time.Second)
	u.key(term.Key{Rune: 'y'})
	if !slices.Equal(m.removed, []string{"fake:b"}) {
		t.Fatalf("removed %v", m.removed)
	}
}

// A login prompt closes the hub; /networks opens it.
func TestHubOpenClose(t *testing.T) {
	u, _ := hubUI()
	u.command("networks", nil, "")
	if u.hub == nil {
		t.Fatal("/networks")
	}
	u.authStart("fake:a", model.EvAuthPrompt{Question: "code?", Reply: make(chan string, 1)})
	if u.hub != nil {
		t.Fatal("a login prompt must close the hub")
	}
}
