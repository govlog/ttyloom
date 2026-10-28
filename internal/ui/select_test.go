package ui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
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

// "c" copies the text of the selected message, or the label of a media with
// no text. The control characters of a remote message never reach the
// clipboard (a later paste in a shell with no bracketed paste would run the
// line); its breaks stay.
func TestCopyKey(t *testing.T) {
	for _, c := range []struct {
		m    model.Msg
		want string
	}{
		{model.Msg{ID: 1, From: "alice", Text: "rm -rf /\rok\x07\nnext"}, "rm -rf / ok \nnext"},
		{model.Msg{ID: 2, From: "bob", Media: &model.Media{Label: "[photo]"}}, "[photo]"},
	} {
		u := hoverUI()
		var out bytes.Buffer
		u.t = term.NewOffscreen(&out, 80, 10)
		w := u.view()
		c.m.Date = time.Now()
		w.Upsert(&c.m)
		w.Sel = w.Items[0]
		u.selKey(w, 'c')
		u.t.Flush()
		if got := copied(t, out.String()); got != c.want {
			t.Errorf("%q: copied %q, want %q", c.m.Text, got, c.want)
		}
	}
}

// copied : the text of the last OSC 52 sequence written, "" when none.
func copied(t *testing.T, out string) string {
	t.Helper()
	i := strings.LastIndex(out, "\x1b]52;c;")
	if i < 0 {
		return ""
	}
	s := out[i+len("\x1b]52;c;"):]
	s = s[:strings.IndexByte(s, '\a')]
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// dragUI : two messages drawn 39 columns wide — the first one wraps at a
// space, the second one cuts a long word and has a line break. at gives the
// cell of the first drawn occurrence of s.
func dragUI(t *testing.T) (u *UI, out *bytes.Buffer, at func(s string) term.MouseEvent) {
	u = hoverUI()
	u.side = sideHidden
	out = &bytes.Buffer{}
	u.t = term.NewOffscreen(out, 40, 12)
	w := u.view()
	d := time.Now()
	w.Upsert(&model.Msg{ID: 1, From: "alice", FromID: 7, Date: d, Text: "one two three four five six seven eight nine ten"})
	w.Upsert(&model.Msg{ID: 2, From: "bob", FromID: 8, Date: d, Text: "https://example.org/" + strings.Repeat("x", 40) + "\nend of it"})
	u.draw()
	if len(w.Items[0].lines) < 2 || len(w.Items[1].lines) < 3 {
		t.Fatalf("setup: no wrap (%d, %d lines)", len(w.Items[0].lines), len(w.Items[1].lines))
	}
	return u, out, func(s string) term.MouseEvent { t.Helper(); return cellAt(t, u, s) }
}

// cellAt : a press on the first occurrence of s drawn at the last frame.
func cellAt(t *testing.T, u *UI, s string) term.MouseEvent {
	t.Helper()
	x0, _ := u.layout()
	for y, h := range u.hits {
		if h.item == nil {
			continue
		}
		txt := render.LineText(h.item.lines[h.sub])
		if i := strings.Index(txt, s); i >= 0 {
			return term.MouseEvent{X: x0 + render.Width(txt[:i]), Y: y, Press: true}
		}
	}
	t.Fatalf("%q not drawn", s)
	return term.MouseEvent{}
}

// A drag copies on release the text it covers, from the cell of the press to
// the one of the release: never the <nick> nor the indent of a message; a
// paragraph cut by the wrap joins back with its space, a cut word with none;
// a new message or a line break stays a break. The status bar says so.
func TestSelDragCopiesText(t *testing.T) {
	u, out, at := dragUI(t)
	u.key(term.Key{Code: term.Mouse, Mouse: at("two")})
	end := at("end of")
	end.X += len("end of") - 1
	end.Motion = true
	u.selDragTo(end)
	end.Press, end.Motion = false, false
	u.key(term.Key{Code: term.Mouse, Mouse: end})
	u.t.Flush()
	want := "two three four five six seven eight nine ten\nhttps://example.org/" + strings.Repeat("x", 40) + "\nend of"
	if got := copied(t, out.String()); got != want {
		t.Fatalf("copied %q, want %q", got, want)
	}
	if u.flashMsg != i18n.T("copied") {
		t.Fatalf("status note %q", u.flashMsg)
	}
	if u.view().Sel != nil {
		t.Fatal("a drag selects no message")
	}
}

// While the button is held, the cells of the range show in reverse video, the
// ones before the press do not.
func TestSelDragShows(t *testing.T) {
	u, out, at := dragUI(t)
	u.key(term.Key{Code: term.Mouse, Mouse: at("two")})
	end := at("three")
	end.X += len("three") - 1
	end.Motion = true
	u.selDragTo(end)
	out.Reset()
	u.draw()
	frame := out.String()
	sgrBefore := func(s string) string {
		t.Helper()
		i := strings.Index(frame, s)
		if i < 0 {
			t.Fatalf("%q not in the frame", s)
		}
		return frame[strings.LastIndex(frame[:i], "\x1b["):i]
	}
	if sgr := sgrBefore("two three"); !strings.Contains(sgr, ";7") {
		t.Fatalf("range not in reverse video: %q", sgr)
	}
	if sgr := sgrBefore("one "); strings.Contains(sgr, ";7") {
		t.Fatalf("text before the press in reverse video: %q", sgr)
	}
}

func TestOSC52(t *testing.T) {
	if got := osc52("salut"); got != "\x1b]52;c;c2FsdXQ=\a" {
		t.Fatalf("osc52: %q", got)
	}
}

// A drag held on the top row of the message area scrolls the view up one
// line a tick while there is buffer above, and the range follows: the copy
// takes the lines that came in. Held under the area, the view scrolls back
// down to the bottom.
func TestSelDragScrollsAtEdge(t *testing.T) {
	u := hoverUI()
	u.side = sideHidden
	var out bytes.Buffer
	u.t = term.NewOffscreen(&out, 40, 8)
	w := u.view()
	var want []string
	for i := 1; i <= 20; i++ {
		want = append(want, fmt.Sprintf("line %02d", i))
		w.Upsert(&model.Msg{ID: i, From: "alice", FromID: 7, Date: time.Now(), Text: want[i-1]})
	}
	u.draw()
	text := cellAt(t, u, "line 20") // first column of the text
	press := text
	press.X += len("line 20") - 1
	u.key(term.Key{Code: term.Mouse, Mouse: press})
	top := term.MouseEvent{X: text.X, Y: 0, Press: true, Motion: true}
	u.selDragTo(top)
	for range 40 {
		u.tick()
	}
	if w.Scroll == 0 {
		t.Fatal("no scroll at the top edge")
	}
	top.Press, top.Motion = false, false
	u.key(term.Key{Code: term.Mouse, Mouse: top})
	u.t.Flush()
	if got := copied(t, out.String()); got != strings.Join(want, "\n") {
		t.Fatalf("copied %q, want lines 01 to 20", got)
	}

	u.draw()
	u.key(term.Key{Code: term.Mouse, Mouse: cellAt(t, u, "line 01")})
	u.selDragTo(term.MouseEvent{X: press.X, Y: u.t.Rows - 1, Press: true, Motion: true}) // on the input line
	for range 40 {
		u.tick()
	}
	if w.Scroll != 0 {
		t.Fatalf("scroll %d after the bottom edge, want 0", w.Scroll)
	}
}

// selDragTo : a move inside the cell of the press stays a click; another cell
// starts the range; a row with no item (day separator) keeps its end; under
// the message area the last row counts; the same cell again asks no repaint.
func TestSelDragThreshold(t *testing.T) {
	it1, it2 := &Item{Msg: &model.Msg{ID: 1}}, &Item{Msg: &model.Msg{ID: 2}}
	u := &UI{ws: NewWindows(), agg: &Window{}, t: &term.Term{Cols: 80, Rows: 6}}
	u.hits = []rowHit{{item: it1}, {item: it1, sub: 1}, {}, {item: it2}}
	u.drag, u.selA = dragText, selPos{it1, 0, 20}

	if u.selDragTo(term.MouseEvent{X: 20, Y: 0}) || u.selB.item != nil {
		t.Fatalf("cell of the press: %+v", u.selB)
	}
	if !u.selDragTo(term.MouseEvent{X: 20, Y: 1}) || u.selB != (selPos{it1, 1, 20}) {
		t.Fatalf("next line: %+v", u.selB)
	}
	if u.selDragTo(term.MouseEvent{X: 5, Y: 2}) || u.selB != (selPos{it1, 1, 20}) {
		t.Fatalf("separator row: %+v", u.selB)
	}
	if !u.selDragTo(term.MouseEvent{X: 3, Y: 9}) || u.selB != (selPos{it2, 0, 3}) {
		t.Fatalf("under the area: %+v", u.selB)
	}
	if u.selDragTo(term.MouseEvent{X: 3, Y: 3}) {
		t.Fatal("same cell: no repaint")
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
		nets: map[string]model.Backend{netTelegram: &fakeBackend{caps: model.AllCaps()}}}
	c := &model.Chat{Net: netTelegram, ID: 5, Reactions: []string{"👍"}}
	u.chats[c.Key()] = c
	u.reactList = map[string][]string{netTelegram: {"👍", "🔥"}}
	w := u.ws.New(true)
	w.Chat = c
	it := &Item{Msg: &model.Msg{Net: netTelegram, ID: 7, ChatID: 5}}
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
		nets:        map[string]model.Backend{netTelegram: b},
		dispatchNet: netTelegram}
	c := &model.Chat{Net: netTelegram, ID: 5}
	u.chats[c.Key()] = c
	u.reactList = map[string][]string{netTelegram: {"👍"}}
	w := u.ws.New(true)
	w.Chat = c
	it := &Item{Msg: &model.Msg{Net: netTelegram, ID: 7, ChatID: 5}}
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
	past := time.Now().Add(-time.Second) // the question has been on screen for a while

	u.confirm("ok ?", do)
	u.ask.at = past
	if !u.askKey(term.Key{Code: term.Mouse}) || u.ask == nil || ran != 0 {
		t.Fatalf("mouse: ask=%v ran=%d", u.ask, ran)
	}
	if !u.askKey(term.Key{Rune: 'y'}) || u.ask != nil || ran != 1 {
		t.Fatalf("y: ask=%v ran=%d", u.ask, ran)
	}
	u.confirm("ok ?", do)
	u.ask.at = past
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

// TestClickRevealsDeleted : a click on a deleted message shows its content,
// the next click hides it again.
func TestClickRevealsDeleted(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, t: &term.Term{Cols: 80, Rows: 24}}
	w := u.ws.Current()
	w.Upsert(&model.Msg{ID: 1, From: "alice", FromID: 7, Text: "oups", Deleted: true})
	it := w.Items[0]
	for _, want := range []bool{true, false} {
		u.drag, u.selA = dragText, selPos{item: it}
		u.selRelease()
		if it.Reveal != want {
			t.Fatalf("after a click: reveal %v, want %v", it.Reveal, want)
		}
	}
}
