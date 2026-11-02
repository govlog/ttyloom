package ui

import (
	"fmt"
	"slices"
	"strings"
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

// draftUI : queryUI, its two windows, and the room shown with text typed in
// its input.
func draftUI() (*UI, *queryBackend, *Window, *Window) {
	u, b, room, peer := queryUI()
	wr, wp := u.winFor(room), u.winFor(peer)
	u.goTo(u.ws.ForChat(room.Key()))
	u.ed.Set("draft for the room")
	return u, b, wr, wp
}

// Opening a chat that has no window yet files the draft of the window left,
// as a click on a chat that has one does: it never lands in the new chat.
func TestRegressionOpenChatKeepsDraft(t *testing.T) {
	u, _, wr, _ := draftUI()
	carol := &model.Chat{Net: netTelegram, ID: 3, Title: "Carol", Kind: model.ChatUser}
	u.remember(carol)
	u.openChat(carol)
	if u.view().Chat != carol || u.ed.String() != "" || wr.Draft != "draft for the room" {
		t.Fatalf("in %q: input %q, room draft %q", u.view().Name(), u.ed.String(), wr.Draft)
	}
}

// The input history is browsed for the window where Up was pressed: another
// window does not take its draft back with Down.
func TestRegressionHistoryBrowseEndsAtWindowChange(t *testing.T) {
	u, _, _, wp := draftUI()
	u.ed.hist = []string{"an old line"}
	u.ed.hi = 1
	u.key(term.Key{Code: term.Up})
	u.goTo(slices.Index(u.ws.List, wp))
	u.key(term.Key{Code: term.Down})
	if got := u.ed.String(); got != "" {
		t.Fatalf("input of the peer window: %q, want empty", got)
	}
}

// Closing the shown window lands on another one with that window's own draft:
// the text it kept is neither wiped nor replaced by the input of the closed one.
func TestRegressionCloseWindowKeepsLandingDraft(t *testing.T) {
	u, _, wr, wp := draftUI()
	u.goTo(slices.Index(u.ws.List, wp)) // the room keeps its draft
	input(u, "/close")
	if u.view() != wr || u.ed.String() != "draft for the room" {
		t.Fatalf("after /close: in %q, input %q", u.view().Name(), u.ed.String())
	}
	// The menu entry "close" of a private chat closes its window too.
	u.openChat(wp.Chat) // a window again: the room files its draft
	u.ed.Set("text for the peer")
	u.menuDo(&ctxMenu{chat: wp.Chat}, "leave")
	if u.view() != wr || u.ed.String() != "draft for the room" {
		t.Fatalf("after the menu close: in %q, input %q", u.view().Name(), u.ed.String())
	}
	// A chat gone from the account takes its window away under the input.
	u.ws.List[0].Draft = "draft of window 0"
	u.chatGone(wr.Chat.Key())
	if u.ws.Cur != 0 || u.ed.String() != "draft of window 0" {
		t.Fatalf("after the chat went: window %d, input %q", u.ws.Cur, u.ed.String())
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

// Ctrl+L right after a message with several pictures hides them all, and the
// screen stays clear when that message is then deleted, as with a message of
// one picture.
func TestClearKeepsAlbumHidden(t *testing.T) {
	u, _ := gifUI()
	u.ws.Cur = 1
	w := u.ws.List[1]
	w.AddSys("old line")
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvNewMessage{Msg: *album(1), Chat: w.Chat}})
	w.HideBacklog()
	if lines, _ := w.Visible(u.optsFor(w)); len(lines) != 0 {
		t.Fatalf("after Ctrl+L: %d lines", len(lines))
	}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvDeleted{ChatID: 1, IDs: []int{1}}})
	if lines, _ := w.Visible(u.optsFor(w)); len(lines) != 0 {
		t.Fatalf("after the delete: %d lines back, want the screen still clear", len(lines))
	}
}

// album : one message with three pictures (Media, then More), as Discord
// gives several photos sent at once; labels "[photo a]" to "[photo c]".
func album(id int) *model.Msg {
	m := &model.Msg{Net: netTelegram, ChatID: 1, ID: id, From: "bob", Text: "look",
		Date: time.Date(2026, 10, 30, 12, 0, 0, 0, time.Local)}
	for _, n := range []string{"a", "b", "c"} {
		md := &model.Media{Kind: model.MediaPhoto, Label: "[photo " + n + "]", Name: n + ".jpg",
			Mime: "image/jpeg", Ext: ".jpg", Loc: n, Size: 100, W: 10, H: 10}
		if m.Media == nil {
			m.Media = md
		} else {
			m.More = append(m.More, md)
		}
	}
	return m
}

// shownText : the drawing of w with images off, as the screen reads it.
func shownText(w *Window) string {
	var sb strings.Builder
	for _, l := range w.Lines(render.Opts{Width: 80, Theme: theme.Terminal(), Images: "off"}) {
		sb.WriteString(render.LineText(l) + "\n")
	}
	return sb.String()
}

// onceEach fails unless each of want shows exactly once in text.
func onceEach(t *testing.T, where, text string, want ...string) {
	t.Helper()
	for _, s := range want {
		if n := strings.Count(text, s); n != 1 {
			t.Fatalf("%s: %q shown %d times, want once:\n%s", where, s, n, text)
		}
	}
}

// Several photos sent at once are one message: the window shows the label of
// each under it, and the author, the quote, the text and the reactions once.
func TestWindowShowsEveryAttachment(t *testing.T) {
	m := album(1)
	m.Reply = &model.Quote{ID: 5, From: "eve", Text: "pics?"}
	m.Reactions = []model.Reaction{{Emoji: "🔥", Count: 2}}
	w := &Window{}
	w.Upsert(m)
	onceEach(t, "window", shownText(w), "<bob>", "eve: pics?", "look", "🔥 2", "[photo a]", "[photo b]", "[photo c]")
}

// An edit of the text, then a page of history with the message edited once
// more, leave each picture of the message once under the newest text, in the
// window of the chat and in the aggregate; the pictures already loaded stay
// loaded.
func TestAttachmentsOnceAfterEdit(t *testing.T) {
	u, _ := gifUI()
	w := u.ws.List[1]
	m := album(1)
	loaded := append([]*model.Media{m.Media}, m.More...)
	for _, md := range loaded {
		md.State = model.MediaReady
	}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvNewMessage{Msg: *m, Chat: w.Chat}})
	shows := func(text, gone string) {
		t.Helper()
		for _, v := range []*Window{w, u.agg} {
			got := viewText(u, v)
			onceEach(t, v.Name(), got, text, "[photo a]", "[photo b]", "[photo c]")
			if strings.Contains(got, gone) {
				t.Fatalf("%s: %q still shows:\n%s", v.Name(), gone, got)
			}
		}
	}
	edit := album(1)
	edit.Text = "edited once"
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvEditMessage{Msg: *edit}})
	shows("edited once", "look")
	page := album(1)
	page.Text = "edited twice"
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvHistory{ChatID: 1, Msgs: []model.Msg{*page}}})
	shows("edited twice", "edited once")
	for _, v := range []*Window{w, u.agg} {
		var shown []*model.Media
		for _, it := range v.Items {
			if it.Msg != nil && it.Msg.Media != nil {
				shown = append(shown, it.Msg.Media)
			}
		}
		if !slices.Equal(shown, loaded) {
			t.Fatalf("%s: media %v shown, want the loaded ones %v", v.Name(), shown, loaded)
		}
	}
}

// A deleted message hides its pictures with its text, in the window of the
// chat and in the aggregate.
func TestAttachmentsHiddenWithDeletedMessage(t *testing.T) {
	u, _ := gifUI()
	w := u.ws.List[1]
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvNewMessage{Msg: *album(1), Chat: w.Chat}})
	labels := []string{"[photo a]", "[photo b]", "[photo c]"}
	for _, v := range []*Window{w, u.agg} {
		onceEach(t, v.Name()+" before the delete", viewText(u, v), labels...)
	}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvDeleted{ChatID: 1, IDs: []int{1}}})
	for _, v := range []*Window{w, u.agg} {
		if text := viewText(u, v); strings.Contains(text, "[photo") {
			t.Fatalf("%s: a picture of the deleted message still shows:\n%s", v.Name(), text)
		}
	}
}

// A message cut by the cap of the window takes its pictures with it: none is
// left at the top without its message.
func TestTrimTakesAttachments(t *testing.T) {
	w := &Window{max: 4}
	w.Upsert(album(1)) // the message and its two parts: 3 items
	w.AddSys("one")
	w.AddSys("two") // 5 items: the message goes
	if text := shownText(w); strings.Contains(text, "[photo") {
		t.Fatalf("a picture outlived its message:\n%s", text)
	}
}

// The lines of "i" go under the pictures of the message, not between the
// message and them.
func TestInfoUnderAttachments(t *testing.T) {
	u, _ := gifUI()
	u.ws.Cur = 1
	w := u.ws.List[1]
	w.Upsert(album(1))
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvInfo{ChatID: 1, ID: 1, Lines: []string{"sent at noon"}}})
	text := viewText(u, w)
	if i, c := strings.Index(text, "sent at noon"), strings.Index(text, "[photo c]"); i < 0 || c < 0 || i < c {
		t.Fatalf("info lines not under the last picture:\n%s", text)
	}
}

// A /search result that holds a message the window already has shows its
// pictures once each: the ones the window already loaded.
func TestSearchKeepsAttachments(t *testing.T) {
	u, _ := gifUI()
	w := u.ws.List[1]
	m := album(1)
	loaded := append([]*model.Media{m.Media}, m.More...)
	w.Upsert(m)
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvSearch{ChatID: 1, Query: "look", Msgs: []model.Msg{*album(1)}}})
	found := u.ws.List[len(u.ws.List)-1]
	var shown []*model.Media
	for _, it := range found.Items {
		if it.Msg != nil && it.Msg.Media != nil {
			shown = append(shown, it.Msg.Media)
		}
	}
	if found.Search == "" || !slices.Equal(shown, loaded) {
		t.Fatalf("search result %q shows media %v, want the window's %v", found.Search, shown, loaded)
	}
}

// A picture selected on its own acts on its whole message: the network gets
// the id of the message.
func TestAttachmentActsOnMessage(t *testing.T) {
	u, b := gifUI()
	w := u.ws.List[1]
	w.Upsert(album(7))
	last := w.Items[len(w.Items)-1]
	if last.Msg.Media == nil || last.Msg.Media.Label != "[photo c]" {
		t.Fatalf("last line of the window: %+v, want the third picture", last.Msg.Media)
	}
	u.setSel(w, last)
	u.selKey(w, 'n')
	if len(b.pins) != 1 || b.pins[0] != "7:true" {
		t.Fatalf("Pin calls: %v, want [7:true]", b.pins)
	}
}
