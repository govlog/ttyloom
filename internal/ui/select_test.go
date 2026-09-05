package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

func TestSelectNext(t *testing.T) {
	w := &Window{}
	d := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 3; i++ {
		w.Upsert(&model.Msg{ID: i, Date: d, From: "a", Text: "x"})
	}
	w.Upsert(&model.Msg{ID: 4, Date: d, Service: "alice a rejoint"})
	w.AddSys("système")

	none := func(*Item) bool { return false }
	id := func() int {
		if w.Sel == nil {
			return 0
		}
		return w.Sel.Msg.ID
	}
	// From nil: nothing shown → last message, service and sys skipped.
	if !w.SelectNext(-1, none) || id() != 3 {
		t.Fatalf("up from nil: %d", id())
	}
	if !w.SelectNext(-1, none) || id() != 2 {
		t.Fatalf("up: %d", id())
	}
	if !w.SelectNext(1, none) || id() != 3 {
		t.Fatalf("down: %d", id())
	}
	if !w.SelectNext(1, none) || w.Sel != nil {
		t.Fatalf("down after the last: %d", id())
	}
	if w.SelectNext(1, none) {
		t.Fatal("down from nil: nothing to do")
	}
	// One item shown: the selection starts there.
	vis := func(it *Item) bool { return it.Msg != nil && it.Msg.ID == 1 }
	if !w.SelectNext(-1, vis) || id() != 1 {
		t.Fatalf("visible: %d", id())
	}
	if w.SelectNext(-1, vis) {
		t.Fatal("first message: nothing above any more")
	}
}

func TestLastOwn(t *testing.T) {
	d := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	own7 := func(m *model.Msg) bool { return render.Own(m, 7) }
	w := &Window{}
	if lastOwn(w, own7) != nil {
		t.Fatal("empty window")
	}
	w.Upsert(&model.Msg{ID: 1, Date: d, From: "moi", FromID: 7, Out: true, Text: "un"})
	w.Upsert(&model.Msg{ID: 2, Date: d, From: "alice", FromID: 9, Text: "deux"})
	if it := lastOwn(w, own7); it == nil || it.Msg.ID != 1 {
		t.Fatalf("my last message: %v", it)
	}
	// Deleted and in flight: not editable, we go further up.
	w.Upsert(&model.Msg{ID: 3, Date: d, From: "moi", FromID: 7, Out: true, Text: "trois", Deleted: true})
	w.Upsert(&model.Msg{TmpID: 4, Date: d, From: "moi", FromID: 7, Out: true, Text: "en vol", Pending: true})
	if it := lastOwn(w, own7); it == nil || it.Msg.ID != 1 {
		t.Fatalf("deleted and in flight skipped: %v", it)
	}
	other := &Window{}
	other.Upsert(&model.Msg{ID: 1, Date: d, From: "alice", FromID: 9, Text: "coucou"})
	if lastOwn(other, own7) != nil {
		t.Fatal("no message of mine")
	}
}

func TestReactionToggle(t *testing.T) {
	rs := []model.Reaction{{Emoji: "👍", Count: 1, Mine: true}, {Emoji: "❤", Count: 2}}
	if got := nextReaction(rs, "👍"); got != "" {
		t.Fatalf("already mine: %q", got)
	}
	if got := nextReaction(rs, "❤"); got != "❤" {
		t.Fatalf("new emoji on an already reacted message: %q", got)
	}
	if got := nextReaction(nil, "🔥"); got != "🔥" {
		t.Fatalf("no reaction: %q", got)
	}
}

func TestQuoteOf(t *testing.T) {
	q := quoteOf(&Item{Msg: &model.Msg{ID: 12, From: "alice", Text: "ligne 1\nligne 2"}})
	if q.ID != 12 || q.From != "alice" || q.Text != "ligne 1 ligne 2" {
		t.Fatalf("%+v", q)
	}
	q = quoteOf(&Item{Msg: &model.Msg{ID: 1, Text: strings.Repeat("é", 100)}})
	if r := []rune(q.Text); len(r) != 81 || r[80] != '…' {
		t.Fatalf("truncation: %d runes", len([]rune(q.Text)))
	}
	q = quoteOf(&Item{Msg: &model.Msg{ID: 2, From: "bob", Media: &model.Media{Label: "[photo 640x480]"}}})
	if q.Text != "[photo 640x480]" {
		t.Fatalf("media: %q", q.Text)
	}
}

func TestReactionsAllViews(t *testing.T) {
	// The same message in two views with separate pointers (a /search result
	// older than the loaded history): both get it.
	u := &UI{ws: NewWindows(), agg: &Window{}}
	c := &model.Chat{ID: 5, Title: "Antonio"}
	chat := u.ws.New(true)
	chat.Chat = c
	chat.Upsert(&model.Msg{ID: 7, ChatID: 5, Text: "coucou"})
	found := u.ws.New(true)
	found.Chat, found.Search = c, "coucou"
	found.Upsert(&model.Msg{ID: 7, ChatID: 5, Text: "coucou"})
	u.reactions(model.EvReactions{ChatID: 5, ID: 7, Reactions: []model.Reaction{{Emoji: "👍", Count: 1}}})
	for _, w := range []*Window{chat, found} {
		if len(w.Items[0].Msg.Reactions) != 1 {
			t.Fatalf("%s: reactions %+v", w.Name(), w.Items[0].Msg.Reactions)
		}
	}
}

func TestSelectionText(t *testing.T) {
	d := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	w := &Window{}
	w.Upsert(&model.Msg{ID: 1, Date: d, From: "alice", Text: "un"})
	w.Upsert(&model.Msg{ID: 2, Date: d, From: "bob", Media: &model.Media{Label: "[photo]"}})
	w.Upsert(&model.Msg{ID: 3, Date: d, From: "bob", Text: "deux"})
	w.AddSys("système")
	it := w.Items

	// Only one message copied: its text as it is, with no prefix.
	if got := selectionText(it, it[0], it[0]); got != "un" {
		t.Fatalf("one message: %q", got)
	}
	// Range reversed: display order, nick as a prefix, media and sys skipped.
	want := "<alice> un\n<bob> deux"
	if got := selectionText(it, it[3], it[0]); got != want {
		t.Fatalf("reversed range: %q", got)
	}
	// Media only: the label rather than an empty copy.
	if got := selectionText(it, it[1], it[1]); got != "[photo]" {
		t.Fatalf("media only: %q", got)
	}
	// Bound missing from the window (message dropped, view changed).
	if got := selectionText(it, it[0], &Item{}); got != "" {
		t.Fatalf("missing bound: %q", got)
	}
	// The copy goes to the clipboard of the system: the control characters of
	// a remote message must not survive there (a later paste in a shell with
	// no bracketed paste would run the line). The breaks between blocks stay.
	w2 := &Window{}
	w2.Upsert(&model.Msg{ID: 1, Date: d, From: "ev\x1b[31mil", Text: "rm -rf /\rok\x07"})
	w2.Upsert(&model.Msg{ID: 2, Date: d, From: "bob", Text: "a\nb"})
	got := selectionText(w2.Items, w2.Items[0], w2.Items[1])
	if strings.ContainsAny(got, "\x1b\r\x07") {
		t.Fatalf("control characters copied: %q", got)
	}
	if !strings.Contains(got, "a\nb") {
		t.Fatalf("body line breaks lost: %q", got)
	}
}

func TestOSC52(t *testing.T) {
	if got := osc52("salut"); got != "\x1b]52;c;c2FsdXQ=\a" {
		t.Fatalf("osc52: %q", got)
	}
}

// selDragTo : a move on the line of the press and on its message stays a
// click; changing line or message opens the drag.
func TestSelDragThreshold(t *testing.T) {
	it1, it2 := &Item{Msg: &model.Msg{ID: 1}}, &Item{Msg: &model.Msg{ID: 2}}
	u := &UI{ws: NewWindows(), agg: &Window{}, t: &term.Term{Cols: 80, Rows: 6}}
	u.view().Items = []*Item{it1, it2}
	u.hits = []rowHit{{item: it1}, {item: it1}, {item: it2}}
	u.drag, u.selAnchor, u.selY = dragText, it1, 0

	if u.selDragTo(term.MouseEvent{X: 20, Y: 0}) || u.selEnd != nil {
		t.Fatalf("same line, same message: %v", u.selEnd)
	}
	if !u.selDragTo(term.MouseEvent{X: 20, Y: 1}) || u.selEnd != it1 {
		t.Fatalf("next line: %v", u.selEnd)
	}
	if !u.selDragTo(term.MouseEvent{X: 20, Y: 2}) || u.selEnd != it2 {
		t.Fatalf("next message: %v", u.selEnd)
	}
	if u.selDragTo(term.MouseEvent{X: 21, Y: 2}) {
		t.Fatal("unchanged range: no repaint")
	}
}

// allowedReactions : nil = global list, empty non-nil = none, else the
// intersection in Telegram order (variation selector ignored).
func TestAllowedReactions(t *testing.T) {
	global := []string{"👍", "👎", "❤", "🔥"}
	if got := allowedReactions(global, nil); len(got) != 4 {
		t.Fatalf("unknown chat: %v", got)
	}
	if got := allowedReactions(global, &model.Chat{ID: 1}); len(got) != 4 {
		t.Fatalf("no restriction: %v", got)
	}
	if got := allowedReactions(global, &model.Chat{ID: 1, Reactions: []string{}}); len(got) != 0 {
		t.Fatalf("no reaction: %v", got)
	}
	c := &model.Chat{ID: 1, Reactions: []string{"🔥", "❤️", "💕"}}
	got := allowedReactions(global, c)
	if len(got) != 2 || got[0] != "❤" || got[1] != "🔥" {
		t.Fatalf("restriction: %v", got) // global order, 💕 outside the list of the account
	}
}

// react : a reaction outside the list does not go to the server, it is told in
// the window of the chat.
func TestReactNotAllowed(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, chats: map[model.ChatKey]*model.Chat{},
		nets: map[string]model.Backend{model.NetTelegram: &fakeBackend{caps: model.AllCaps()}}}
	c := &model.Chat{Net: model.NetTelegram, ID: 5, Reactions: []string{"👍"}}
	u.chats[c.Key()] = c
	u.reactList = map[string][]string{model.NetTelegram: {"👍", "🔥"}}
	w := u.ws.New(true)
	w.Chat = c
	it := &Item{Msg: &model.Msg{Net: model.NetTelegram, ID: 7, ChatID: 5}}
	w.Items = append(w.Items, it)
	u.react(it, "💕") // outside the list: refused before any call to the network
	last := w.Items[len(w.Items)-1]
	if last.Sys != "réaction non disponible ici : 💕" {
		t.Fatalf("system message: %q", last.Sys)
	}
}

// A network with no reactions takes none: the click, the double click and the
// picker all end in react(), which lets nothing out.
func TestReactNoCapability(t *testing.T) {
	b := &fakeBackend{}
	u := &UI{ws: NewWindows(), agg: &Window{}, chats: map[model.ChatKey]*model.Chat{},
		nets:        map[string]model.Backend{model.NetTelegram: b},
		dispatchNet: model.NetTelegram}
	c := &model.Chat{Net: model.NetTelegram, ID: 5}
	u.chats[c.Key()] = c
	u.reactList = map[string][]string{model.NetTelegram: {"👍"}}
	w := u.ws.New(true)
	w.Chat = c
	it := &Item{Msg: &model.Msg{Net: model.NetTelegram, ID: 7, ChatID: 5}}
	w.Items = append(w.Items, it)
	u.react(it, "👍")
	if b.reacts != 0 {
		t.Fatalf("React called %d time(s) with no reaction capability", b.reacts)
	}
	b.caps = model.AllCaps()
	u.react(it, "👍")
	if b.reacts != 1 {
		t.Fatalf("React calls with the capability: %d", b.reacts)
	}
}

// Re-read of the message after a reaction: the server sometimes answers with
// no "reactions" field, or before it applied it. An empty list from there must
// not wipe the one the update has just set.
func TestReactionsRefetchKeepsList(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, dirty: map[model.ChatKey]bool{}}
	w := u.ws.New(true)
	w.Chat = &model.Chat{ID: 5}
	w.Upsert(&model.Msg{ID: 7, ChatID: 5, Text: "coucou"})
	m := w.Items[0].Msg

	u.reactions(model.EvReactions{ChatID: 5, ID: 7, Reactions: []model.Reaction{{Emoji: "👍", Count: 1, Mine: true}}})
	u.reactions(model.EvReactions{ChatID: 5, ID: 7, Refetch: true}) // empty re-read
	if len(m.Reactions) != 1 {
		t.Fatalf("empty reread: %+v", m.Reactions)
	}
	// A re-read that sees other reactions has the say, and an update too —
	// including to empty it (my reaction removed).
	u.reactions(model.EvReactions{ChatID: 5, ID: 7, Refetch: true,
		Reactions: []model.Reaction{{Emoji: "👍", Count: 2}}})
	if len(m.Reactions) != 1 || m.Reactions[0].Count != 2 {
		t.Fatalf("full reread: %+v", m.Reactions)
	}
	u.reactions(model.EvReactions{ChatID: 5, ID: 7})
	if len(m.Reactions) != 0 {
		t.Fatalf("empty update: %+v", m.Reactions)
	}
}

// l pauses then resumes a video being played, s stops it on the first frame; s
// on a video at rest is not kept (the input gets it).
func TestVideoPauseStop(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, images: "kitty"}
	w := u.ws.Current()
	md := &model.Media{Kind: model.MediaVideo, Label: "[video 00:04]", Path: "/tmp/v.mp4",
		State: model.MediaReady, Delay: 100 * time.Millisecond, Frame: 2,
		Frames: [][]byte{{1}, {2}, {3}}}
	w.Upsert(&model.Msg{ID: 1, Date: time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC),
		From: "alice", Media: md})
	u.setSel(w, w.Items[0])

	if !u.selKey(w, 'l') || !md.Paused {
		t.Fatalf("l: pause=%v", md.Paused)
	}
	if !u.selKey(w, 'l') || md.Paused {
		t.Fatalf("l: resume=%v", md.Paused)
	}
	if !u.selKey(w, 's') || !md.Paused || md.Frame != 0 {
		t.Fatalf("s: pause=%v frame=%d", md.Paused, md.Frame)
	}
	u.setSel(w, w.Items[0])
	if u.selKey(w, 's') { // already stopped: no longer in the palette, no longer kept
		t.Fatal("s consumed after the stop")
	}
	md.Frames, md.Frame, md.Paused = md.Frames[:1], 0, false // video at rest
	u.setSel(w, w.Items[0])
	if u.selKey(w, 's') {
		t.Fatal("s consumed with no playback running")
	}
}

// Step by step decoding: each partial report grows the frames without putting
// the animation back to the first, and the last one lifts the progress.
func TestFramesPartialGrow(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, images: "kitty"}
	md := &model.Media{Kind: model.MediaVideo, Path: "/tmp/v.mp4", State: model.MediaReady,
		Frames: [][]byte{{1}}, Want: 60, Frame: 0}
	png := func(n int) [][]byte {
		out := make([][]byte, n)
		for i := range out {
			out[i] = []byte{byte(i)}
		}
		return out
	}
	u.framesLoaded(evFrames{Media: md, Frames: &media.Frames{PNG: png(30), Delay: 100 * time.Millisecond},
		Partial: true})
	if len(md.Frames) != 30 || md.Want != 60 {
		t.Fatalf("1st report: %d frames, want %d", len(md.Frames), md.Want)
	}
	md.Frame = 12 // animation running
	u.framesLoaded(evFrames{Media: md, Frames: &media.Frames{PNG: png(60), Delay: 100 * time.Millisecond}})
	if len(md.Frames) != 60 || md.Frame != 12 || md.Want != 0 {
		t.Fatalf("end: %d frames, frame %d, want %d", len(md.Frames), md.Frame, md.Want)
	}
}

// A decoding that comes back after the media was freed (window closed,
// display mode changed) does not bring it back to life.
func TestFramesLateIgnored(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, images: "kitty"}
	md := &model.Media{Kind: model.MediaVideo, Path: "/tmp/v.mp4", State: model.MediaNone}
	fr := &media.Frames{PNG: [][]byte{{1}, {2}}, Delay: 100 * time.Millisecond}
	u.framesLoaded(evFrames{Media: md, Frames: fr, Partial: true})
	u.framesLoaded(evFrames{Media: md, Frames: fr})
	if md.State != model.MediaNone || len(md.Frames) != 0 {
		t.Fatalf("media resurrected: state %v, %d frames", md.State, len(md.Frames))
	}
}

// TestAskKeys : y confirms, anything else cancels, the mouse leaves the
// question alone. The prompt never stays stuck.
func TestAskKeys(t *testing.T) {
	u := &UI{}
	ran := 0
	do := func() { ran++ }

	u.confirm("ok ?", do)
	if !u.askKey(term.Key{Code: term.Mouse}) || u.ask == nil || ran != 0 {
		t.Fatalf("mouse: ask=%v ran=%d", u.ask, ran)
	}
	if !u.askKey(term.Key{Rune: 'y'}) || u.ask != nil || ran != 1 {
		t.Fatalf("y: ask=%v ran=%d", u.ask, ran)
	}
	u.confirm("ok ?", do)
	u.askKey(term.Key{Rune: 'Y'})
	if u.ask != nil || ran != 2 {
		t.Fatalf("Y: ask=%v ran=%d", u.ask, ran)
	}
	for _, k := range []term.Key{{Rune: 'n'}, {Code: term.Esc}, {Code: term.Enter}} {
		u.confirm("ok ?", do)
		u.askKey(k)
		if u.ask != nil || ran != 2 {
			t.Fatalf("%+v: ask=%v ran=%d", k, u.ask, ran)
		}
	}
}

// selectionText : the copy as one string, what copySel hands to the clipboard.
func selectionText(items []*Item, a, b *Item) string {
	return strings.Join(selBlocks(items, a, b), "\n")
}
