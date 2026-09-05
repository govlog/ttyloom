package ui

import (
	"strconv"
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

func TestPager(t *testing.T) {
	var ls []render.Line
	for i := 0; i < 25; i++ {
		ls = append(ls, render.Line{Spans: []render.Span{{Text: strconv.Itoa(i)}}})
	}
	p := &pager{rest: ls}
	if got := p.Next(10); len(got) != 10 || got[9].Spans[0].Text != "9" || p.Done() {
		t.Fatalf("page 1: %d", len(got))
	}
	if got := p.Next(10); len(got) != 10 || got[0].Spans[0].Text != "10" {
		t.Fatalf("page 2")
	}
	if got := p.Next(10); len(got) != 5 || !p.Done() {
		t.Fatalf("last: %d done=%v", len(got), p.Done())
	}
}

// TestPagerKeys : space and Enter show one more page, q shows the rest, Esc
// drops it; a click is swallowed while the wheel goes through.
func TestPagerKeys(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{},
		t: &term.Term{Cols: 80, Rows: 10}}
	w := u.ws.List[0]
	var ls []render.Line
	for i := 0; i < 25; i++ {
		ls = append(ls, render.Line{Spans: []render.Span{{Text: strconv.Itoa(i)}}})
	}
	page := max(u.viewRows()-1, 1)

	u.emit(w, ls)
	if u.pager == nil || len(u.pager.rest) != 25-page {
		t.Fatalf("first page: %v", u.pager)
	}
	if !u.pagerKey(term.Key{Rune: ' '}) || len(u.pager.rest) != 25-2*page {
		t.Fatalf("space: %v", u.pager)
	}
	if !u.pagerKey(term.Key{Code: term.Enter}) || u.pager == nil || len(u.pager.rest) != 25-3*page {
		t.Fatalf("enter: %v", u.pager)
	}
	u.emit(w, ls)
	if !u.pagerKey(term.Key{Rune: 'q'}) || u.pager != nil {
		t.Fatalf("q: %v", u.pager)
	}
	u.emit(w, ls)
	if !u.pagerKey(term.Key{Code: term.Esc}) || u.pager != nil {
		t.Fatalf("esc: %v", u.pager)
	}
	// Click: the rest goes out and the key is swallowed (the hits are stale).
	u.emit(w, ls)
	if !u.pagerKey(term.Key{Code: term.Mouse, Mouse: term.MouseEvent{Button: 0}}) || u.pager != nil {
		t.Fatalf("click: %v", u.pager)
	}
	// Wheel: the rest goes out too, but the key goes on to the window.
	u.emit(w, ls)
	if u.pagerKey(term.Key{Code: term.Mouse, Mouse: term.MouseEvent{Button: 64}}) || u.pager != nil {
		t.Fatalf("wheel: %v", u.pager)
	}
}
