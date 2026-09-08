package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

func TestDraftSwitch(t *testing.T) {
	a := &Window{Draft: "brouillon a"}
	b := &Window{Draft: "brouillon b"}
	restored := swapDraft(a, b, "en cours de saisie")
	if a.Draft != "en cours de saisie" {
		t.Fatalf("old.Draft = %q", a.Draft)
	}
	if b.Draft != "" {
		t.Fatalf("target.Draft = %q, want empty", b.Draft)
	}
	if restored != "brouillon b" {
		t.Fatalf("restored = %q", restored)
	}
	// Refresh of the current window (goTo(u.ws.Cur)): the same pointer on both
	// sides, and the text being typed must come back unchanged.
	w := &Window{}
	if got := swapDraft(w, w, "texte en cours"); got != "texte en cours" || w.Draft != "" {
		t.Fatalf("same window: got=%q draft=%q", got, w.Draft)
	}
}

func TestSearchWindowName(t *testing.T) {
	c := &model.Chat{ID: 5, Title: "Antonio"}
	chat := &Window{Chat: c}
	found := &Window{Chat: c, Search: "café"}
	if chat.Name() != "Antonio" || found.Name() != "?café" {
		t.Fatalf("names: %q / %q", chat.Name(), found.Name())
	}
	// ForChat ignores the search window: the live messages go into the window
	// of the chat, never into a search result.
	ws := &Windows{List: []*Window{{}, found, chat}}
	if ws.ForChat(c.Key()) != 2 {
		t.Fatalf("ForChat = %d, want 2", ws.ForChat(c.Key()))
	}
}

// TestMergeAround : a page centred on a message falls between two blocks
// already loaded — it must slot in, not stick to the head.
func TestMergeAround(t *testing.T) {
	w := &Window{}
	var have []*model.Msg
	for i := 1; i <= 50; i++ {
		have = append(have, &model.Msg{ID: i})
	}
	for i := 200; i <= 250; i++ {
		have = append(have, &model.Msg{ID: i})
	}
	w.Merge(have)

	page := func() []*model.Msg {
		var out []*model.Msg
		for i := 95; i <= 145; i++ {
			out = append(out, &model.Msg{ID: i})
		}
		return out
	}
	w.MergeAround(page())
	w.MergeAround(page()) // played again: nothing must double

	if n := len(w.Items); n != 50+51+51 {
		t.Fatalf("%d items, want %d", n, 50+51+51)
	}
	prev := 0
	seen := false
	for i, it := range w.Items {
		if it.Msg == nil {
			t.Fatalf("item %d without message", i)
		}
		if it.Msg.ID <= prev {
			t.Fatalf("order broken at %d: %d after %d", i, it.Msg.ID, prev)
		}
		prev = it.Msg.ID
		seen = seen || it.Msg.ID == 120
	}
	if !seen {
		t.Fatal("120 missing")
	}
	if w.OldestID() != 1 || w.LastID() != 250 {
		t.Fatalf("bounds: %d..%d", w.OldestID(), w.LastID())
	}
}

// TestRedlinePosition : the redline (/set redline) goes right after the last
// message read (MarkID) and before the first unread one, does not move while
// MarkID is not written again (even when ReadInboxMaxID moves on elsewhere),
// and goes away when everything is read or when redline is off.
func TestRedlinePosition(t *testing.T) {
	w := &Window{Chat: &model.Chat{ID: 1, ReadInboxMaxID: 6}, MarkID: 6}
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 10; i++ {
		w.Items = append(w.Items, &Item{Msg: &model.Msg{ID: i, Date: base, From: "alice", FromID: 7, Text: fmt.Sprintf("m%d", i)}})
	}
	o := render.Opts{Width: 60, Theme: theme.Terminal(), Redline: true}

	_, items, idx := w.LineItems(o)
	if idx < 0 || items[idx] != nil {
		t.Fatalf("redline missing or misplaced: idx=%d", idx)
	}
	if items[idx-1] == nil || items[idx-1].Msg.ID != 6 {
		t.Fatalf("before the redline: %+v, want message 6", items[idx-1])
	}
	if items[idx+1] == nil || items[idx+1].Msg.ID != 7 {
		t.Fatalf("after the redline: %+v, want message 7", items[idx+1])
	}

	// The window stays shown: ReadInboxMaxID moves on elsewhere (markRead),
	// but MarkID (frozen) is not written again → the redline does not move.
	w.Chat.ReadInboxMaxID = 9
	if _, _, idx2 := w.LineItems(o); idx2 != idx {
		t.Fatalf("redline moved: %d then %d", idx, idx2)
	}

	// Everything read: no line left.
	w.MarkID = 10
	if _, _, idx3 := w.LineItems(o); idx3 >= 0 {
		t.Fatalf("everything read: idx = %d, want -1", idx3)
	}

	// redline off: nothing, even with unread messages.
	w.MarkID = 6
	o.Redline = false
	if _, _, idx4 := w.LineItems(o); idx4 >= 0 {
		t.Fatalf("redline off: idx = %d, want -1", idx4)
	}
}

// TestMergeAroundFullWindow : window at its cap, page centred on a message
// older than everything it holds. A crop from the head would drop exactly the
// page inserted — and the jump would say nothing.
func TestMergeAroundFullWindow(t *testing.T) {
	w := &Window{}
	var have []*model.Msg
	for i := 1001; i <= 3000; i++ { // defaultMaxItems items
		have = append(have, &model.Msg{ID: i})
	}
	w.Merge(have)
	if len(w.Items) != defaultMaxItems {
		t.Fatalf("starting window: %d items", len(w.Items))
	}
	var page []*model.Msg
	for i := 475; i <= 525; i++ {
		page = append(page, &model.Msg{ID: i})
	}
	w.MergeAround(page)
	if itemByID(w, 500) == nil {
		t.Fatal("500 trimmed: the centred page disappeared")
	}
	if w.OldestID() != 475 || w.LastID() != 3000 {
		t.Fatalf("bounds: %d..%d", w.OldestID(), w.LastID())
	}
}

// TestCacheMessagesLive : /set cache_messages lines the memory of the windows
// up at once — and lets it come back down, floor at defaultMaxItems.
func TestCacheMessagesLive(t *testing.T) {
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: cfg,
		t: &term.Term{Cols: 80, Rows: 24}}
	u.setCmd([]string{"cache_messages", "3000"})
	made := u.ws.New(true) // window made after the change: it takes the limit too
	for _, w := range []*Window{u.ws.List[0], u.agg, u.debug, made} {
		if w.max != 3000 {
			t.Fatalf("raise: max = %d", w.max)
		}
	}
	var ms []*model.Msg
	for i := 1; i <= 2500; i++ {
		ms = append(ms, &model.Msg{ID: i})
	}
	made.Merge(ms)
	if len(made.Items) != 2500 {
		t.Fatalf("trimmed to %d items though cache_messages = 3000", len(made.Items))
	}
	// Down again: the limit is not monotone any more.
	u.setCmd([]string{"cache_messages", "500"})
	if made.max != defaultMaxItems {
		t.Fatalf("lower: max = %d", made.max)
	}
	made.Merge(nil) // the trim comes at the next Merge
	if len(made.Items) != defaultMaxItems {
		t.Fatalf("after Merge: %d items", len(made.Items))
	}
}

// TestHistoryGapMarker : a page loaded around a far away message (jump from a
// search result, from a quote) leaves a hole that no scroll fills — scrolling
// up loads above the oldest message shown, thus above the hole and never
// inside it. Nothing marked the seam. A "…" line now does, and it goes as
// soon as a later page fills the hole.
func TestHistoryGapMarker(t *testing.T) {
	page := func(from, to int) []*model.Msg {
		var out []*model.Msg
		for id := from; id <= to; id++ {
			out = append(out, &model.Msg{ID: id})
		}
		return out
	}
	w := &Window{}
	for _, m := range page(1, 10) {
		w.Upsert(m)
	}

	w.MergeAround(page(50, 60))

	k := -1
	for i, it := range w.Items {
		if it.Msg == nil && it.Sys != "" {
			if k >= 0 {
				t.Fatalf("two markers for one hole: %d and %d", k, i)
			}
			k = i
		}
	}
	if k <= 0 || k+1 >= len(w.Items) {
		t.Fatalf("no seam marker: %d items", len(w.Items))
	}
	if got := w.Items[k].Sys; got != i18n.T("history_gap") {
		t.Fatalf("marker text: %q", got)
	}
	if before, after := w.Items[k-1].Msg, w.Items[k+1].Msg; before.ID != 10 || after.ID != 50 {
		t.Fatalf("marker between %d and %d, want between 10 and 50", before.ID, after.ID)
	}

	w.MergeAround(page(11, 49)) // the hole is filled: the marker goes

	for _, it := range w.Items {
		if it.Msg == nil && it.Sys != "" {
			t.Fatal("marker kept while the hole is filled")
		}
	}
	if len(w.Items) != 60 {
		t.Fatalf("%d messages, want 60", len(w.Items))
	}
}

// TestHistoryGapOverlapNoMarker : the page brought back overlaps what the
// window already holds — the common case, a jump to some fifty messages above
// the oldest one loaded. The duplicates are dropped before insertion, so the
// ids left do not touch the window any more; the range test must read the
// page, not what is left of it, otherwise a marker settles between two
// neighbouring messages and nothing can ever drop it.
func TestHistoryGapOverlapNoMarker(t *testing.T) {
	page := func(from, to int) []*model.Msg {
		var out []*model.Msg
		for id := from; id <= to; id++ {
			out = append(out, &model.Msg{ID: id})
		}
		return out
	}
	w := &Window{}
	for _, m := range page(900, 1000) {
		w.Upsert(m)
	}

	w.MergeAround(page(800, 900)) // 900 in common: no hole

	for _, it := range w.Items {
		if it.Msg == nil && it.Sys != "" {
			t.Fatalf("marker on a page that overlaps: %q", it.Sys)
		}
	}
	if len(w.Items) != 201 {
		t.Fatalf("%d messages, want 201", len(w.Items))
	}
}

// TestUpdateReplacedMedia : an edit that replaces the media (another photo)
// must not keep the old one; an edit of the text alone keeps the loaded media.
func TestUpdateReplacedMedia(t *testing.T) {
	w := &Window{}
	old := &model.Media{Kind: model.MediaPhoto, Size: 100, State: model.MediaReady}
	w.Upsert(&model.Msg{ID: 1, Text: "a", Media: old})
	w.Update(&model.Msg{ID: 1, Text: "b", Media: &model.Media{Kind: model.MediaPhoto, Size: 100}})
	if w.Items[0].Msg.Media != old {
		t.Fatal("text edit: loaded media dropped")
	}
	w.Update(&model.Msg{ID: 1, Text: "b", Media: &model.Media{Kind: model.MediaPhoto, Size: 200}})
	if w.Items[0].Msg.Media == old {
		t.Fatal("media replaced by the edit: the old one is still shown")
	}
}

// TestSysTimestamp : with timestamps on, a system line of a status window
// (window 0, aggregate, log) carries the time of its arrival like a message;
// a chat window keeps its bare "*** " lines.
func TestSysTimestamp(t *testing.T) {
	o := render.Opts{Width: 60, Theme: theme.Terminal(), Timestamps: true}
	w := &Window{}
	w.AddSys("connected")
	w.Items[0].At = time.Date(2026, 9, 7, 14, 3, 0, 0, time.UTC)
	if got := render.LineText(w.Lines(o)[0]); got != "14:03 *** connected" {
		t.Fatalf("status window: %q", got)
	}
	c := &Window{Chat: &model.Chat{ID: 1}}
	c.AddSys("connected")
	if got := render.LineText(c.Lines(o)[0]); got != "*** connected" {
		t.Fatalf("chat window: %q", got)
	}
	o.Timestamps = false
	w.Invalidate()
	if got := render.LineText(w.Lines(o)[0]); got != "*** connected" {
		t.Fatalf("timestamps off: %q", got)
	}
}

// TestHideBacklog : Ctrl+L leaves the window blank at its bottom, the lines
// that come after show, and the whole backlog is back as soon as the window
// scrolls (PgUp); /clear (Items dropped) forgets the mark.
func TestHideBacklog(t *testing.T) {
	o := render.Opts{Width: 40, Theme: theme.Terminal()}
	w := &Window{}
	w.AddSys("old one")
	w.AddSys("old two")
	w.HideBacklog()
	if lines, _ := w.Visible(o); len(lines) != 0 {
		t.Fatalf("after Ctrl+L: %d lines", len(lines))
	}
	w.AddSys("new")
	if lines, items := w.Visible(o); len(lines) != 1 || items[0].Sys != "new" {
		t.Fatalf("after a new line: %d lines", len(lines))
	}
	w.Scroll = 1
	if lines, _ := w.Visible(o); len(lines) != 3 {
		t.Fatalf("scrolled: %d lines, want the whole backlog", len(lines))
	}
	w.Scroll, w.Items = 0, nil
	w.AddSys("fresh")
	if lines, _ := w.Visible(o); len(lines) != 1 || w.hidden != nil {
		t.Fatalf("after /clear: %d lines, hidden=%v", len(lines), w.hidden)
	}
}
