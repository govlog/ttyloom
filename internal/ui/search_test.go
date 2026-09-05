package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/theme"
)

func TestFold(t *testing.T) {
	if got := render.Fold("Élève à Noël"); got != "eleve a noel" {
		t.Fatalf("fold: %q", got)
	}
}

func TestHighlight(t *testing.T) {
	st := theme.Style{Reverse: true}
	l := render.Line{Spans: []render.Span{{Text: "hello world"}}}
	got := highlight(l, "WOR", st)
	want := []render.Span{
		{Text: "hello "},
		{Text: "wor", Style: st},
		{Text: "ld"},
	}
	if len(got.Spans) != len(want) {
		t.Fatalf("spans: %+v", got.Spans)
	}
	for i, sp := range want {
		if got.Spans[i] != sp {
			t.Fatalf("span %d: %+v", i, got.Spans[i])
		}
	}
	// With no hit, the first line is drawn as it is.
	if same := highlight(l, "zzz", st); len(same.Spans) != 1 || same.Spans[0].Text != "hello world" {
		t.Fatalf("no match: %+v", same.Spans)
	}
}

func TestMatchLines(t *testing.T) {
	lines := []render.Line{
		{Spans: []render.Span{{Text: "12:01 <alice> Café au lait"}}},
		{Spans: []render.Span{{Text: "12:02 <bob> rien à voir"}}},
		{Spans: []render.Span{{Text: "cafe cafe"}}},
	}
	hits := matchLines(lines, "CAFÉ")
	if len(hits) != 3 {
		t.Fatalf("matches: %+v", hits)
	}
	if hits[0].line != 0 || hits[0].col0 != 14 || hits[0].col1 != 18 {
		t.Fatalf("1st match: %+v", hits[0])
	}
	if hits[1] != (hl{line: 2, col0: 0, col1: 4}) || hits[2] != (hl{line: 2, col0: 5, col1: 9}) {
		t.Fatalf("line 2: %+v", hits[1:])
	}
	if got := matchLines(lines, ""); got != nil {
		t.Fatalf("empty query: %+v", got)
	}
}

func TestShowRows(t *testing.T) {
	// 100 lines, view of 10: Scroll=0 shows [90,100).
	if got := showRows(0, 20, 20, 100, 10, 90); got != 70 {
		t.Fatalf("line above the view: %d", got)
	}
	if got := showRows(0, 95, 95, 100, 10, 90); got != 0 {
		t.Fatalf("line already visible: %d", got)
	}
}

func TestReuseMsgs(t *testing.T) {
	kept := &model.Msg{ID: 7, ChatID: 5, Text: "déjà affiché"}
	origin := &Window{Items: []*Item{{Sys: "ligne système"}, {Msg: kept}}}
	got := reuseMsgs(origin, []model.Msg{{ID: 7, ChatID: 5, Text: "copie du serveur"}, {ID: 9, ChatID: 5}})
	if len(got) != 2 {
		t.Fatalf("results: %d", len(got))
	}
	// Message already known: the pointer of the first window, so that edit,
	// reaction and delete stay shared.
	if got[0] != kept {
		t.Fatalf("known message: %+v, want the original pointer", got[0])
	}
	if got[1] == nil || got[1].ID != 9 {
		t.Fatalf("new message: %+v", got[1])
	}
}

func TestSearchSnippet(t *testing.T) {
	long := strings.Repeat("bla ", 30) + "Café au lait " + strings.Repeat("blo ", 30)
	got := snippet(long, "café", 40)
	if render.Width(got) != 40 {
		t.Fatalf("width %d: %q", render.Width(got), got)
	}
	if !strings.Contains(got, "Café au lait") {
		t.Fatalf("lost match: %q", got)
	}
	// Text shorter than the window: drawn as it is, with no cut mark.
	if got := snippet("court", "ou", 40); got != "court" {
		t.Fatalf("short text: %q", got)
	}
	// With no hit: the start of the text, at the exact width.
	if got := snippet(long, "zzz", 20); render.Width(got) != 20 || !strings.HasPrefix(got, "bla") {
		t.Fatalf("no match: %q", got)
	}
}

func TestSearchOverlayNav(t *testing.T) {
	rows := 4
	g := &globalSearch{hits: make([]model.SearchHit, 10)}
	g.move(1, rows)
	if g.cur != 1 || g.scroll != 0 {
		t.Fatalf("down: cur=%d scroll=%d", g.cur, g.scroll)
	}
	g.move(-5, rows) // bounded at the head
	if g.cur != 0 || g.scroll != 0 {
		t.Fatalf("upper bound: cur=%d scroll=%d", g.cur, g.scroll)
	}
	g.move(len(g.hits), rows) // bounded at the tail, the scroll follows
	if g.cur != 9 || g.scroll != 6 {
		t.Fatalf("lower bound: cur=%d scroll=%d", g.cur, g.scroll)
	}
	g.move(-3, rows) // the current one stays in the view: nothing moves
	if g.cur != 6 || g.scroll != 6 {
		t.Fatalf("scroll up: cur=%d scroll=%d", g.cur, g.scroll)
	}
	g.move(-1, rows) // gone out at the top: the scroll catches it
	if g.cur != 5 || g.scroll != 5 {
		t.Fatalf("scroll up out of view: cur=%d scroll=%d", g.cur, g.scroll)
	}
	empty := &globalSearch{}
	empty.move(1, rows)
	if empty.cur != 0 || empty.scroll != 0 {
		t.Fatalf("empty list: cur=%d scroll=%d", empty.cur, empty.scroll)
	}
}

// TestSearchOverlayWidth : each line of the box is exactly its width. A line
// too long would go past it onto the screen instead of staying in the box.
func TestSearchOverlayWidth(t *testing.T) {
	g := &globalSearch{query: []rune("café"), cur: 0, hits: []model.SearchHit{{
		Chat: &model.Chat{ID: 1, Kind: model.ChatGroup, Title: "salon au titre interminable"},
		Date: time.Date(2026, 8, 30, 14, 22, 0, 0, time.UTC), From: "bob",
		Text: strings.Repeat("bla ", 40) + "un Café au lait",
	}}}
	for _, w := range []int{24, 60, 100} {
		for i, l := range g.Lines(theme.Terminal(), w, 12, nil) {
			if got := render.Width(render.LineText(l)); got != w {
				t.Errorf("box of %d, line %d: width %d (%q)", w, i, got, render.LineText(l))
			}
		}
	}
}

// TestGsChatListing : a chat seen in a result (or in the contacts) is kept
// without entering the sidebar; listChat puts it there, only once.
func TestGsChatListing(t *testing.T) {
	known := &model.Chat{ID: 1, Title: "connu"}
	u := &UI{chats: map[model.ChatKey]*model.Chat{known.Key(): known}}
	if got := u.gsChat(&model.Chat{ID: 1, Title: "copie du serveur"}); got != known {
		t.Fatalf("known chat: %+v, want the original pointer", got)
	}
	neuf := &model.Chat{ID: 2, Title: "inédit"}
	if got := u.gsChat(neuf); got != neuf || u.chats[neuf.Key()] != neuf {
		t.Fatalf("new chat: %+v", got)
	}
	if len(u.chatList) != 0 {
		t.Fatalf("sidebar polluted by a mere result: %d entries", len(u.chatList))
	}
	u.listChat(neuf)
	u.listChat(neuf)
	if len(u.chatList) != 1 || u.chatList[0] != neuf {
		t.Fatalf("listChat: %+v", u.chatList)
	}
}

// TestJumpClosesSearch : Enter on a global result also closes the local
// search. It keeps the keyboard while it is open — and when the target is
// already in the current window, no window change closes it.
func TestJumpClosesSearch(t *testing.T) {
	c := &model.Chat{ID: 1, Title: "salon"}
	m := &model.Msg{ID: 42, ChatID: 1, Text: "la cible"}
	w := &Window{Chat: c, Items: []*Item{{Msg: m}}}
	u := &UI{ws: &Windows{List: []*Window{{}, w}, Cur: 1}, agg: &Window{},
		chats:   map[model.ChatKey]*model.Chat{c.Key(): c},
		search:  &searchState{cur: -1},
		gsearch: &globalSearch{cur: 0, hits: []model.SearchHit{{Chat: c, MsgID: 42}}}}
	u.gsOpen()
	if u.gsearch != nil || u.search != nil {
		t.Fatalf("searches still open: gsearch=%v search=%v", u.gsearch != nil, u.search != nil)
	}
	if w.Sel == nil || w.Sel.Msg != m {
		t.Fatalf("target not selected: %+v", w.Sel)
	}
	if u.jump.msgID != 0 {
		t.Fatalf("unnecessary rendezvous set: %d", u.jump.msgID)
	}
}

func (f *fakeBackend) SearchGlobal(_ context.Context, q string, _ int) {
	f.gsearch = append(f.gsearch, q)
}

// The global search asks every network that searches — or the one of the
// /net filter alone (F2 in windows mode cycles it) — and shows the answers
// merged, newest first, once they are all in.
func TestGlobalSearchNetsMerged(t *testing.T) {
	u := netUI(model.NetTelegram, model.NetDiscord)
	u.chats = map[model.ChatKey]*model.Chat{}
	for _, b := range u.nets {
		b.(*fakeBackend).caps = model.Caps{GlobalSearch: true}
	}
	tg, dc := u.nets[model.NetTelegram].(*fakeBackend), u.nets[model.NetDiscord].(*fakeBackend)
	u.search = &searchState{q: []rune("cat")}
	u.searchGlobalOpen()
	if len(tg.gsearch) != 1 || len(dc.gsearch) != 1 {
		t.Fatalf("both networks asked: tg %v, dc %v", tg.gsearch, dc.gsearch)
	}
	old, recent := time.Now().Add(-time.Hour), time.Now()
	u.dispatch(model.Envelope{Net: model.NetTelegram, Ev: model.EvSearchGlobal{Query: "cat",
		Hits: []model.SearchHit{{Chat: u.chatList[0], MsgID: 1, Date: old, Text: "tg"}}}})
	if g := u.gsearch; g.inflight == "" || len(g.hits) != 0 {
		t.Fatalf("one answer of two: inflight %q, %d hits shown", g.inflight, len(g.hits))
	}
	u.dispatch(model.Envelope{Net: model.NetDiscord, Ev: model.EvSearchGlobal{Query: "cat",
		Hits: []model.SearchHit{{Chat: u.chatList[1], MsgID: 2, Date: recent, Text: "dc"}}}})
	g := u.gsearch
	if g.inflight != "" || len(g.hits) != 2 || g.hits[0].Text != "dc" || g.hits[1].Text != "tg" {
		t.Fatalf("merged: inflight %q, hits %+v", g.inflight, g.hits)
	}
	// /net discord: only that network is asked.
	u.setNetFilter(model.NetDiscord)
	g.query = []rune("dog")
	u.gsSend()
	if len(tg.gsearch) != 1 || len(dc.gsearch) != 2 {
		t.Fatalf("filtered: tg %v, dc %v", tg.gsearch, dc.gsearch)
	}
}
