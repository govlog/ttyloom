package ui

import (
	"slices"
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

// whoZone : the tick and a reaction already on the message carry the popup;
// the quick-react button (emoji not on the message) and the rest do not.
func TestWhoZone(t *testing.T) {
	it := &Item{Msg: &model.Msg{ID: 1, Reactions: []model.Reaction{{Emoji: "👍", Count: 1}}}}
	tick := render.Action{Key: render.KeyTicks}
	rea := render.Action{Key: render.KeyReact, Emoji: "👍"}
	quick := render.Action{Key: render.KeyReact, Emoji: "🔥"}
	if whoZone(hit{item: it, act: &tick}) == nil || whoZone(hit{item: it, act: &rea}) == nil {
		t.Fatal("tick and reaction: popup expected")
	}
	if whoZone(hit{item: it, act: &quick}) != nil || whoZone(hit{item: it}) != nil || whoZone(hit{}) != nil {
		t.Fatal("quick reaction / no action: no popup")
	}
}

// whoAt : entering the zone opens the popup ("loading"), staying put repaints
// nothing, leaving closes it. EvWho fills the cache and the open popup; a
// warm cache answers with no new request.
func TestWhoAtEv(t *testing.T) {
	it := &Item{Msg: &model.Msg{Net: netTelegram, ID: 5, ChatID: 7, Out: true}}
	b := &fakeBackend{caps: model.AllCaps()}
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{Hover: config.HoverMenu},
		t: &term.Term{Cols: 80, Rows: 6}, whoCache: map[whoKey]whoEntry{},
		chats:       map[model.ChatKey]*model.Chat{tgk(7): {Net: netTelegram, ID: 7}},
		nets:        map[string]model.Backend{netTelegram: b},
		dispatchNet: netTelegram}
	u.hits = []rowHit{{item: it, acts: []render.Action{{Col0: 10, Col1: 13, Key: render.KeyTicks}}}}
	if !u.whoAt(11, 0) || u.who == nil || u.who.text != i18n.T("loading") {
		t.Fatalf("enter: %+v", u.who)
	}
	if u.whoAt(12, 0) {
		t.Fatal("same zone: no repaint")
	}
	u.whoEv(model.EvWho{ChatID: 7, ID: 5, Text: "lu par alice"})
	if u.who == nil || u.who.text != "lu par alice" {
		t.Fatalf("EvWho: %+v", u.who)
	}
	if !u.whoAt(5, 0) || u.who != nil {
		t.Fatal("leave: popup closed")
	}
	if !u.whoAt(11, 0) || u.who == nil || u.who.text != "lu par alice" {
		t.Fatalf("warm cache: %+v", u.who)
	}
	u.cfg.Hover = config.HoverOff // popup never without hover
	u.who = nil
	if u.whoAt(11, 0) || u.who != nil {
		t.Fatal("hover off: no popup")
	}
	// Network without read receipts: nothing is asked, and the popup says so
	// rather than staying stuck on "loading".
	u.cfg.Hover, b.caps, u.who = config.HoverMenu, model.Caps{}, nil
	u.whoCache = map[whoKey]whoEntry{}
	asked := b.whoRead
	if !u.whoAt(11, 0) || u.who == nil || u.who.text != i18n.T("net_unsupported", netTelegram) {
		t.Fatalf("no read receipts: %+v", u.who)
	}
	if b.whoRead != asked {
		t.Fatalf("WhoRead called with no read receipts: %d", b.whoRead)
	}
}

// The box wraps its text between borders and stays on the screen.
func TestWhoBox(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{},
		t:   &term.Term{Cols: 30, Rows: 10},
		who: &whoBox{x: 28, y: 8, text: "lu par alice, bob et carole"}}
	r := u.whoRect()
	if r.col+r.w > 30 || r.row+r.h > u.viewRows() || r.w < 4 || r.h < 3 {
		t.Fatalf("rect off screen: %+v", r)
	}
	ls := u.who.Lines(u.th, r.w, r.h)
	if len(ls) != r.h {
		t.Fatalf("%d lines for h=%d", len(ls), r.h)
	}
}

// /whois of a chat renamed locally shows its real title, and names its
// network: the line was "Telegram title" on every network.
func TestWhoisRealTitleNamesNetwork(t *testing.T) {
	u := netUI("discord")
	c := u.chatList[0]
	u.chats = map[model.ChatKey]*model.Chat{c.Key(): c}
	u.aliases = map[model.ChatKey]string{c.Key(): "friend"}
	u.dispatch(model.Envelope{Net: "discord", Ev: model.EvWhois{ChatID: c.ID, Lines: []string{"x"}}})
	var got []string
	for _, it := range u.view().Items {
		got = append(got, it.Sys)
	}
	if want := i18n.T("whois_real_title", "discord", c.Title); !slices.Contains(got, want) {
		t.Fatalf("lines %q, want %q", got, want)
	}
}
