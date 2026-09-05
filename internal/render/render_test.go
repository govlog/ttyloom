package render

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/theme"
)

func texts(lines []Line) []string {
	var out []string
	for _, l := range lines {
		s := ""
		for _, sp := range l.Spans {
			s += sp.Text
		}
		out = append(out, s)
	}
	return out
}

func TestWrapEmojiAndHardCut(t *testing.T) {
	st := theme.Style{}
	got := texts(Wrap([]Span{{"ab 😃cd ef", st}}, 5))
	want := []string{"ab", "😃cd", "ef"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("emoji: %q", got)
	}
	got = texts(Wrap([]Span{{"abcdefgh", st}}, 3))
	if len(got) != 3 || got[0] != "abc" || got[2] != "gh" {
		t.Fatalf("hard cut: %q", got)
	}
	got = texts(Wrap([]Span{{"a\nb", st}}, 10))
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("newline: %q", got)
	}
	if lines := Wrap([]Span{{"😃", st}}, 1); len(lines) != 1 || texts(lines)[0] != "😃" {
		t.Fatalf("wide rune at width 1: %+v", lines)
	}
	bold := theme.Style{Bold: true}
	lines := Wrap([]Span{{"hello ", st}, {"exam", st}, {"ple", bold}}, 10)
	got = texts(lines)
	if len(got) != 2 || got[0] != "hello" || got[1] != "example" {
		t.Fatalf("word across styles: %q", got)
	}
	if len(lines[1].Spans) != 2 || lines[1].Spans[0].Text != "exam" || lines[1].Spans[1].Text != "ple" || !lines[1].Spans[1].Style.Bold {
		t.Fatalf("word across styles spans: %+v", lines[1].Spans)
	}
	got = texts(Wrap([]Span{{"aaa  bbb", st}}, 3))
	if len(got) != 2 || got[0] != "aaa" || got[1] != "bbb" {
		t.Fatalf("double space: %q", got)
	}
}

func TestWidthVS16(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"❤️", 2}, // U+2764 U+FE0F : Ghostty/kitty draw it on 2 cells
		{"a❤️b", 4},
		{"🥺", 2},
		{"❤", 1},       // without vs16: go-runewidth alone
		{"👨‍👩‍👧‍👦", 2}, // ZWJ family: capped at 2, not the sum of the runes
		{"🏳️‍🌈", 2},    // rainbow flag: vs16 + ZWJ
		{"👍🏻", 2},      // thumb + skin tone
		{"🇫🇷", 2},      // FR flag: 2 regional indicators
	}
	for _, c := range cases {
		if got := Width(c.s); got != c.want {
			t.Fatalf("Width(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

func TestWrapVS16(t *testing.T) {
	st := theme.Style{}
	for i, s := range texts(Wrap([]Span{{"aa ❤️ bb", st}}, 5)) {
		if w := Width(s); w > 5 {
			t.Fatalf("line %d too wide (%d): %q", i, w, s)
		}
	}
}

func TestClean(t *testing.T) {
	// 1 control rune = 1 space (UTF-16 length kept): nothing separates "e" from
	// "f" on the input, so nothing separates them on the output.
	if got := Clean("a\x1bb\tc\nd\x7fef"); got != "a b c\nd ef" {
		t.Fatalf("clean: %q", got)
	}
	// Bidi overrides and isolates spoof what is read: "gpj.exe" drawn "exe.jpg".
	if got := Clean("a\u202Eb\u2066c"); got != "a b c" {
		t.Fatalf("bidi: %q", got)
	}
	if SafeURL("https://x/\u202Ea") {
		t.Fatal("bidi override in a URL accepted")
	}
}

func TestRunsTextURLControlChars(t *testing.T) {
	ents := []model.Span{{Start: 0, End: 4, Kind: model.SpanURL, URL: "https://x\x1b\\"}}
	spans := Runs("link", ents, theme.Style{}, theme.Terminal())
	if len(spans) != 1 || spans[0].Style.URL != "" || !spans[0].Style.Underline {
		t.Fatalf("control url: %+v", spans)
	}
}

func TestRunsRuneOffsetsBoldURL(t *testing.T) {
	// Offsets in runes: the astral 😃 counts for 1 → "bold" at 5,
	// "example.org" at 10. The UTF-16 conversion is tgc business.
	text := "hi 😃 bold example.org"
	ents := []model.Span{
		{Start: 5, End: 9, Kind: model.SpanBold},
		{Start: 10, End: 21, Kind: model.SpanURL, URL: "https://example.org"},
	}
	th := theme.Terminal()
	spans := Runs(text, ents, theme.Style{}, th)
	if len(spans) != 4 {
		t.Fatalf("spans: %+v", spans)
	}
	if spans[0].Text != "hi 😃 " || spans[1].Text != "bold" || !spans[1].Style.Bold {
		t.Fatalf("bold: %+v", spans[:2])
	}
	if spans[3].Text != "example.org" || spans[3].Style.URL != "https://example.org" || !spans[3].Style.Underline {
		t.Fatalf("url: %+v", spans[3])
	}
}

func TestMessageLayout(t *testing.T) {
	o := Opts{Width: 40, Theme: theme.Terminal(), Timestamps: true, Images: "off"}
	m := &model.Msg{ID: 1, Date: time.Date(2026, 8, 29, 12, 1, 0, 0, time.UTC), From: "alice", FromID: 7,
		Text: "salut tout le monde, ça va bien ?", Reply: &model.Quote{From: "bob", Text: "yo"}}
	got := texts(Message(m, o))
	if got[0] != "12:01 <alice> │ bob: yo" {
		t.Fatalf("line0: %q", got[0])
	}
	if got[1] != "              salut tout le monde, ça va" || got[2] != "              bien ?" {
		t.Fatalf("wrap: %q", got)
	}
}

func TestBox(t *testing.T) {
	cols, rows := Box(800, 600, 60, 10, 10, 20) // 10 lines x 20 px = 200 px high → s = 1/3
	if cols != 27 || rows != 10 {
		t.Fatalf("box: %d %d", cols, rows)
	}
	if c, r := Box(100, 40, 60, 10, 10, 20); c != 10 || r != 2 { // never made bigger
		t.Fatalf("no upscale: %d %d", c, r)
	}
}

func TestHalfblocks(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	img.Set(1, 0, color.RGBA{255, 0, 0, 255})
	img.Set(0, 1, color.RGBA{0, 0, 255, 255})
	img.Set(1, 1, color.RGBA{0, 0, 255, 255})
	lines := Halfblocks(img, 2, 1)
	if len(lines) != 1 || len(lines[0].Spans) != 1 || lines[0].Spans[0].Text != "▀▀" {
		t.Fatalf("%+v", lines)
	}
	st := lines[0].Spans[0].Style
	if st.FG.RGB != (theme.RGB{R: 255, G: 0, B: 0}) || st.BG.RGB != (theme.RGB{R: 0, G: 0, B: 255}) {
		t.Fatalf("colors: %+v", st)
	}
}

func TestMessageSelected(t *testing.T) {
	o := Opts{Width: 40, Theme: theme.Terminal(), Images: "off", Self: self7, Caps: allCaps}
	m := &model.Msg{ID: 1, Date: time.Date(2026, 8, 29, 12, 1, 0, 0, time.UTC), From: "moi", FromID: 7,
		Out: true, Text: "salut tout le monde"}
	for i, l := range Message(m, o) {
		if len(l.Spans) > 0 && l.Spans[0].Text == "▌" {
			t.Fatalf("not selected, marker on line %d", i)
		}
	}

	o.Selected = m
	lines := Message(m, o)
	got := texts(lines)
	for i, l := range lines {
		if len(l.Spans) == 0 || l.Spans[0].Text != "▌" {
			t.Fatalf("line %d without marker: %q", i, got[i])
		}
		if Width(got[i]) > o.Width {
			t.Fatalf("line %d too wide: %q", i, got[i])
		}
	}
	all := strings.Join(got, "\n")
	if last := got[len(got)-1]; !strings.Contains(last, "Esc") {
		t.Fatalf("help missing: %q", last)
	}
	if !strings.Contains(all, "e éditer") || !strings.Contains(all, "d supprimer") {
		t.Fatalf("my message: %q", all)
	}
	if strings.Contains(all, "o ouvrir") {
		t.Fatalf("no media: %q", all)
	}

	// Message of somebody else, with media: no edit, but "o open".
	other := &model.Msg{ID: 2, Date: m.Date, From: "alice", FromID: 9, Text: "coucou",
		Media: &model.Media{Label: "[photo]", Loc: &tg.InputPhotoFileLocation{}}}
	// Width 70: the help line fits on one line, no word is cut.
	all = strings.Join(texts(Message(other, Opts{Width: 70, Theme: theme.Terminal(), Images: "off", Self: self7, Selected: other, Caps: allCaps})), "\n")
	if strings.Contains(all, "e éditer") || strings.Contains(all, "d supprimer") {
		t.Fatalf("someone else's message: %q", all)
	}
	if !strings.Contains(all, "o ouvrir") || !strings.Contains(all, "p répondre") {
		t.Fatalf("media: %q", all)
	}

	// Service message: never selectable.
	svc := &model.Msg{ID: 3, Date: m.Date, Service: "alice a rejoint"}
	for _, l := range Message(svc, Opts{Width: 40, Theme: theme.Terminal(), Images: "off", Selected: svc}) {
		if len(l.Spans) > 0 && l.Spans[0].Text == "▌" {
			t.Fatal("service selected")
		}
	}
}

// Hover: same clickable help line as the selection, background, but no
// marker; selected and hovered at once, only one help line.
func TestHoverHelpLine(t *testing.T) {
	m := &model.Msg{ID: 1, Date: time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC), From: "alice", FromID: 9, Text: "coucou"}
	o := Opts{Width: 70, Theme: theme.Terminal(), Images: "off", Self: self7, Hover: m, HoverHelp: true}
	lines := Message(m, o)
	got := texts(lines)
	if last := got[len(got)-1]; !strings.Contains(last, "Esc") || !strings.Contains(last, "p répondre") {
		t.Fatalf("help missing: %q", last)
	}
	for i, l := range lines {
		if len(l.Spans) > 0 && l.Spans[0].Text == selMark {
			t.Fatalf("marker on line %d", i)
		}
		if l.Spans[0].Style.BG.Kind == 0 {
			t.Fatalf("line %d without hover background", i)
		}
	}
	if len(lines[len(lines)-1].Actions) == 0 {
		t.Fatal("help line not clickable")
	}

	o.Selected = m
	lines = Message(m, o)
	n := 0
	for _, l := range texts(lines) {
		if strings.Contains(l, "Esc") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("help lines: %d", n)
	}
	if lines[0].Spans[0].Text != selMark {
		t.Fatalf("marker missing: %q", texts(lines)[0])
	}
}

// TestHoverModes : highlight = background only (no help line on hover);
// menu (HoverHelp) = background and help line, as before.
func TestHoverModes(t *testing.T) {
	m := &model.Msg{ID: 1, Date: time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC), From: "alice", FromID: 9, Text: "coucou"}
	o := Opts{Width: 70, Theme: theme.Terminal(), Images: "off", Self: self7, Hover: m} // highlight: HoverHelp false
	lines := Message(m, o)
	if lines[0].Spans[0].Style.BG.Kind == 0 {
		t.Fatal("highlight: hover background missing")
	}
	for _, l := range texts(lines) {
		if strings.Contains(l, "Esc") {
			t.Fatalf("highlight: help line present: %q", l)
		}
	}

	o.HoverHelp = true // menu
	lines = Message(m, o)
	if last := texts(lines)[len(lines)-1]; !strings.Contains(last, "Esc") {
		t.Fatalf("menu: help line missing: %q", last)
	}
	if lines[0].Spans[0].Style.BG.Kind == 0 {
		t.Fatal("menu: hover background missing")
	}
}

// halfblockMsg : fixture outside the test — built in place, it fires the
// go1.26.7 miscompile: the first call
// to Message then draws the message as not selected.
func halfblockMsg(t *testing.T) *model.Msg {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	img.Set(1, 0, color.RGBA{255, 0, 0, 255})
	img.Set(0, 1, color.RGBA{0, 0, 255, 255})
	img.Set(1, 1, color.RGBA{0, 0, 255, 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return &model.Msg{ID: 4, From: "alice", FromID: 9, Media: &model.Media{Label: "[photo]",
		State: model.MediaReady, Frames: [][]byte{buf.Bytes()}, FrameW: 2, FrameH: 2}}
}

// A selected half-block image: the marker is there and the background of the
// half blocks (bottom pixel) survives the selection background.
func TestMessageSelectedHalfblock(t *testing.T) {
	m := halfblockMsg(t)
	o := Opts{Width: 40, Theme: theme.Terminal(), Images: "halfblock",
		CellW: 1, CellH: 2, MaxImgCols: 10, MaxImgRows: 5, Self: self7, Selected: m}
	marks, blue := 0, 0
	for _, l := range Message(m, o) {
		if len(l.Spans) > 0 && l.Spans[0].Text == selMark {
			marks++
		}
		for _, sp := range l.Spans {
			if sp.Style.BG == (theme.Color{Kind: 2, RGB: theme.RGB{B: 255}}) {
				blue++
			}
		}
	}
	if marks == 0 { // without the marker, the assertion on the background would prove nothing
		t.Fatal("block not selected")
	}
	if blue == 0 {
		t.Fatal("half-block background overwritten by the selection")
	}
}

// Code block: "┃ " goes in front of each line of the block (like "│ " for the
// blockquote), with the code background already set by the existing Pre.
func TestRunsPreBlock(t *testing.T) {
	ents := []model.Span{{Start: 0, End: 3, Kind: model.SpanPre}}
	spans := Runs("a\nb", ents, theme.Style{}, theme.Terminal())
	if len(spans) != 2 || spans[0].Text != "┃ a\n" || spans[1].Text != "┃ b" {
		t.Fatalf("pre block: %+v", spans)
	}
}

func TestMessageChatLabel(t *testing.T) {
	o := Opts{Width: 60, Theme: theme.Terminal(), Timestamps: true, Images: "off", ShowChat: true}
	m := &model.Msg{ID: 1, ChatID: 42, ChatLabel: "alice", Date: time.Date(2026, 8, 29, 12, 1, 0, 0, time.UTC),
		From: "bob", FromID: 7, Text: "salut"}
	if got := texts(Message(m, o))[0]; got != "12:01 [alice] <bob> salut" {
		t.Fatalf("label: %q", got)
	}
	o.ShowChat = false
	if got := texts(Message(m, o))[0]; got != "12:01 <bob> salut" {
		t.Fatalf("without label: %q", got)
	}
}

func TestMessageAvatarGutter(t *testing.T) {
	o := Opts{Width: 40, Theme: theme.Terminal(), Timestamps: true, Images: "kitty", Avatars: true}
	m := &model.Msg{ID: 1, Date: time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC), From: "alice", FromID: 7,
		Text: "salut"}
	lines := Message(m, o)
	if got := lines[0].Spans[1].Text; got != "   " { // 3 cells after the timestamp
		t.Fatalf("gutter: %q", got)
	}
	if lines[0].Avatar != 7 || lines[0].AvatarCol != 6 {
		t.Fatalf("avatar: id %d col %d", lines[0].Avatar, lines[0].AvatarCol)
	}
	if got := texts(lines)[0]; got != "12:01    <alice> salut" {
		t.Fatalf("line: %q", got)
	}

	o.Selected = m // the selection marker moves the gutter by one column
	if l := Message(m, o)[0]; l.Avatar != 7 || l.AvatarCol != 7 {
		t.Fatalf("selection: id %d col %d", l.Avatar, l.AvatarCol)
	}
	o.Selected = nil

	o.Avatars = false
	lines = Message(m, o)
	if lines[0].Avatar != 0 || lines[0].AvatarCol != 0 {
		t.Fatalf("avatar with option off: id %d col %d", lines[0].Avatar, lines[0].AvatarCol)
	}
	if got := texts(lines)[0]; got != "12:01 <alice> salut" {
		t.Fatalf("line with option off: %q", got)
	}
}

// TestAvatarPrivateFallback : incoming private message with no FromID (cache
// written before the peer-without-from_id fix) — the avatar uses the chat.
func TestAvatarPrivateFallback(t *testing.T) {
	o := Opts{Width: 40, Theme: theme.Terminal(), Images: "kitty", Avatars: true,
		ChatKind: func(m *model.Msg) model.ChatKind {
			if m.ChatID == 99 {
				return model.ChatUser
			}
			return model.ChatGroup
		}}
	m := &model.Msg{ID: 1, ChatID: 99, Date: time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC), Text: "salut"}
	if l := Message(m, o)[0]; l.Avatar != 99 {
		t.Fatalf("private fallback: avatar %d", l.Avatar)
	}

	o.ChatKind = func(*model.Msg) model.ChatKind { return model.ChatGroup } // group: no fallback
	if l := Message(m, o)[0]; l.Avatar != 0 {
		t.Fatalf("group without from_id: avatar %d, want 0", l.Avatar)
	}
}

// colText : substring of text that takes exactly the columns [c0, c1).
func colText(text string, c0, c1 int) string {
	off := func(col int) int {
		for i := range text { // rune boundaries
			if Width(text[:i]) == col {
				return i
			}
		}
		if Width(text) == col {
			return len(text)
		}
		return -1
	}
	a, b := off(c0), off(c1)
	if a < 0 || b < 0 || b < a {
		return ""
	}
	return text[a:b]
}

// The columns of each action frame its label exactly in the drawn line,
// selection marker and indent included.
func TestHelpActions(t *testing.T) {
	labels := map[rune]string{KeyReact: "🔥", KeyTicks: " ✓", 'e': "e éditer", 'd': "d supprimer", 'p': "p répondre",
		'r': "r réagir", 'c': "c copier", 'i': "i info", 'o': "o ouvrir", 'v': "v voir", KeyEsc: "Esc", KeyView: "[photo]"}
	m := &model.Msg{ID: 1, Date: time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC), From: "moi", FromID: 7,
		Out: true, Text: "salut", Media: &model.Media{Label: "[photo]", Loc: &tg.InputPhotoFileLocation{}}}
	check := func(width int) map[rune]bool {
		seen := map[rune]bool{}
		for _, l := range Message(m, Opts{Width: width, Theme: theme.Terminal(), Images: "off", Self: self7, Selected: m, Caps: allCaps}) {
			text := LineText(l)
			for _, a := range l.Actions {
				want, ok := labels[a.Key]
				if !ok {
					t.Fatalf("unknown action: %q", a.Key)
				}
				if got := colText(text, a.Col0, a.Col1); got != want {
					t.Fatalf("width %d, action %q: columns %d-%d → %q, want %q",
						width, a.Key, a.Col0, a.Col1, got, want)
				}
				seen[a.Key] = true
			}
		}
		return seen
	}
	if seen := check(100); len(seen) != len(labels) { // everything fits on one line
		t.Fatalf("actions placed: %d of %d", len(seen), len(labels))
	}
	check(40) // wrapped help: a cut label loses its action, the others hold
}

func TestTicks(t *testing.T) {
	d := time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC)
	o := Opts{Width: 40, Theme: theme.Terminal(), Images: "off", Self: self7,
		ReadOutbox: func(*model.Msg) int { return 5 }, Caps: allCaps}
	read := &model.Msg{ID: 5, ChatID: 3, Date: d, From: "moi", FromID: 7, Out: true, Text: "lu"}
	if got := texts(Message(read, o))[0]; got != "<moi> lu ✓✓" {
		t.Fatalf("read: %q", got)
	}
	sent := &model.Msg{ID: 6, ChatID: 3, Date: d, From: "moi", FromID: 7, Out: true, Text: "envoyé"}
	if got := texts(Message(sent, o))[0]; got != "<moi> envoyé ✓" {
		t.Fatalf("sent: %q", got)
	}
	other := &model.Msg{ID: 4, ChatID: 3, Date: d, From: "alice", FromID: 9, Text: "coucou"}
	if got := texts(Message(other, o))[0]; got != "<alice> coucou" {
		t.Fatalf("someone else's message: %q", got)
	}
	// The tick counts in the width available: the body wraps sooner.
	long := &model.Msg{ID: 5, ChatID: 3, Date: d, From: "moi", FromID: 7, Out: true,
		Text: strings.Repeat("mot ", 30)}
	for i, l := range texts(Message(long, o)) {
		if w := Width(l); w > o.Width {
			t.Fatalf("line %d too wide (%d): %q", i, w, l)
		}
	}
}

func TestTicksIncoming(t *testing.T) {
	d := time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC)
	o := Opts{Width: 40, Theme: theme.Terminal(), Images: "off", Self: self7,
		ReadOutbox: func(*model.Msg) int { return 5 }, ReadInbox: func(*model.Msg) int { return 4 }, Caps: allCaps}
	read := &model.Msg{ID: 4, ChatID: 3, Date: d, From: "alice", FromID: 9, Text: "lu"}
	if got := texts(Message(read, o))[0]; got != "<alice> lu ✓✓" {
		t.Fatalf("read by me: %q", got)
	}
	unread := &model.Msg{ID: 6, ChatID: 3, Date: d, From: "alice", FromID: 9, Text: "pas lu"}
	if got := texts(Message(unread, o))[0]; got != "<alice> pas lu •" {
		t.Fatalf("unread: %q", got)
	}
	// Outgoing: not changed by ReadInbox.
	sent := &model.Msg{ID: 6, ChatID: 3, Date: d, From: "moi", FromID: 7, Out: true, Text: "envoyé"}
	if got := texts(Message(sent, o))[0]; got != "<moi> envoyé ✓" {
		t.Fatalf("outgoing: %q", got)
	}
	// Service: never a tick.
	svc := &model.Msg{ID: 7, ChatID: 3, Date: d, Service: "alice a rejoint"}
	if got := texts(Message(svc, o))[0]; got != "*** alice a rejoint" {
		t.Fatalf("service: %q", got)
	}
}

func TestReactionActions(t *testing.T) {
	m := &model.Msg{ID: 1, Date: time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC), From: "alice", FromID: 9,
		Text: "coucou", Reactions: []model.Reaction{{Emoji: "👍", Count: 2, Mine: true}, {Emoji: "🔥", Count: 1}}}
	var acts []Action
	var text string
	for _, l := range Message(m, Opts{Width: 60, Theme: theme.Terminal(), Images: "off", Self: self7}) {
		if len(l.Actions) > 0 {
			acts, text = l.Actions, LineText(l)
		}
	}
	if len(acts) != 2 {
		t.Fatalf("actions: %+v", acts)
	}
	for i, want := range []struct{ emoji, label string }{{"👍", "👍 2"}, {"🔥", "🔥 1"}} {
		a := acts[i]
		if a.Key != KeyReact || a.Emoji != want.emoji {
			t.Fatalf("action %d: %+v", i, a)
		}
		if got := colText(text, a.Col0, a.Col1); got != want.label {
			t.Fatalf("action %d: columns %d-%d → %q, want %q", i, a.Col0, a.Col1, got, want.label)
		}
	}
}

// TestImageLinesHoverMode : in hover mode the media keeps no line free — the
// label alone — but the label line carries what is needed to draw on top
// (size of the block, end of the label).
func TestImageLinesHoverMode(t *testing.T) {
	m := &model.Msg{ID: 4, From: "alice", FromID: 9, Media: &model.Media{Label: "[photo 640x480]",
		State: model.MediaReady, Frames: [][]byte{{1}}, FrameW: 640, FrameH: 480}}
	o := Opts{Width: 60, Theme: theme.Terminal(), Images: "kitty", ImagesHover: true,
		CellW: 8, CellH: 16, MaxImgCols: 20, MaxImgRows: 10}
	lines := Message(m, o)
	label, hover := false, 0
	for _, l := range lines {
		if l.Img != nil {
			t.Fatal("hover mode: no line should be reserved")
		}
		for _, sp := range l.Spans {
			if strings.Contains(sp.Text, "[photo 640x480]") {
				label = true
			}
		}
		if h := l.Hover; h != nil {
			hover++
			if h.Media != m.Media || h.Cols < 1 || h.Rows < 1 || h.Col < 1 {
				t.Fatalf("overlay: %+v", h)
			}
		}
	}
	if !label || hover != 1 {
		t.Fatalf("label %v, hover lines %d", label, hover)
	}
	// Outside hover mode: the image block comes back, with no on-top mark.
	o.ImagesHover = false
	blocks := 0
	for _, l := range Message(m, o) {
		if l.Hover != nil {
			t.Fatal("normal mode: no overlay")
		}
		if l.Img != nil {
			blocks++
		}
	}
	if blocks == 0 { // otherwise the assertion of the hover mode would prove nothing
		t.Fatal("normal mode: image block missing")
	}
}

// TestVideoModes : /set video. hidden reserves no line for a video — the
// label alone, the v preview and the o key stay available — while show
// keeps the block of the first image. Other media types are not touched.
func TestVideoModes(t *testing.T) {
	vid := &model.Msg{ID: 5, From: "alice", FromID: 9, Media: &model.Media{Kind: model.MediaVideo,
		Label: "[video 12s]", State: model.MediaReady, Frames: [][]byte{{1}}, FrameW: 640, FrameH: 480}}
	pic := &model.Msg{ID: 6, From: "alice", FromID: 9, Media: &model.Media{Kind: model.MediaPhoto,
		Label: "[photo 640x480]", State: model.MediaReady, Frames: [][]byte{{1}}, FrameW: 640, FrameH: 480}}
	o := Opts{Width: 60, Theme: theme.Terminal(), Images: "kitty",
		CellW: 8, CellH: 16, MaxImgCols: 20, MaxImgRows: 10}

	blocks := func(m *model.Msg, o Opts) (n int, label bool) {
		for _, l := range Message(m, o) {
			if l.Img != nil {
				n++
			}
			for _, sp := range l.Spans {
				if strings.Contains(sp.Text, m.Media.Label) {
					label = true
				}
			}
		}
		return n, label
	}

	o.Video = "hidden"
	if n, label := blocks(vid, o); n != 0 || !label {
		t.Fatalf("hidden: %d lines reserved, label %v", n, label)
	}
	if n, _ := blocks(pic, o); n == 0 {
		t.Fatal("hidden: a photo's block must remain")
	}
	for _, mode := range []string{"show", "autoplay", ""} {
		o.Video = mode
		if n, label := blocks(vid, o); n == 0 || !label {
			t.Fatalf("video=%q: %d lines reserved, label %v", mode, n, label)
		}
	}
}

// quickEmoji : my reaction first, else the most frequent one, else the content.
func TestQuickEmoji(t *testing.T) {
	mine := &model.Msg{Text: "coucou", Reactions: []model.Reaction{{Emoji: "🔥", Count: 5}, {Emoji: "👏", Count: 1, Mine: true}}}
	if got := quickEmoji(mine, nil); got != "👏" {
		t.Fatalf("my reaction: %q", got)
	}
	pop := &model.Msg{Text: "merci", Reactions: []model.Reaction{{Emoji: "🔥", Count: 2}, {Emoji: "👏", Count: 5}}}
	if got := quickEmoji(pop, nil); got != "👏" {
		t.Fatalf("most frequent reaction: %q", got)
	}
	// Allowed list: a guessed emoji outside the list falls back to the first one,
	// an empty list (chat with no reaction) offers nothing.
	if got := quickEmoji(pop, []string{"👍", "🔥"}); got != "👍" {
		t.Fatalf("outside the list: %q", got)
	}
	if got := quickEmoji(pop, []string{"👏", "👍"}); got != "👏" {
		t.Fatalf("in the list: %q", got)
	}
	if got := quickEmoji(pop, []string{}); got != "" {
		t.Fatalf("no reaction allowed: %q", got)
	}
	for _, c := range []struct {
		msg  *model.Msg
		want string
	}{
		{&model.Msg{Text: "Ça marche ?"}, "🤔"},
		{&model.Msg{Text: "Merci beaucoup"}, "🙏"},
		{&model.Msg{Text: "thanks a lot"}, "🙏"},
		{&model.Msg{Text: "Bravo"}, "👏"},
		{&model.Msg{Text: "gg"}, "👏"},
		{&model.Msg{Text: "Félicitations"}, "👏"},
		{&model.Msg{Text: "mdr"}, "🤣"},
		{&model.Msg{Text: "😂"}, "🤣"},
		{&model.Msg{Text: "désolé"}, "😢"},
		{&model.Msg{Text: "rip"}, "😢"},
		{&model.Msg{Media: &model.Media{Kind: model.MediaVideo}}, "🔥"},
		{&model.Msg{Media: &model.Media{Kind: model.MediaSticker}}, "❤"},
		{&model.Msg{Text: "merci", Media: &model.Media{Kind: model.MediaPhoto}}, "🙏"}, // the text wins
		{&model.Msg{Text: "salut"}, "👍"},
	} {
		if got := quickEmoji(c.msg, nil); got != c.want {
			t.Fatalf("%q → %q, want %q", c.msg.Text, got, c.want)
		}
	}
}

// Hover/selection: the quick reaction emoji is the first entry of the help
// line, clickable; it never moves the body of the message (no preamble left
// and no gutter taken over, see user feedback H22).
func TestHoverQuickAction(t *testing.T) {
	m := &model.Msg{ID: 1, Date: time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC), From: "alice", FromID: 9, Text: "coucou"}
	o := Opts{Width: 70, Theme: theme.Terminal(), Images: "off", Self: self7, HoverHelp: true, Caps: allCaps} // menu mode
	plain := Message(m, o)
	for _, a := range plain[0].Actions {
		if a.Key == KeyReact {
			t.Fatalf("reaction action without hover: %+v", a)
		}
	}

	o.Hover = m
	lines := Message(m, o)
	if got, want := LineText(lines[0]), LineText(plain[0]); got != want {
		t.Fatalf("body shifted on hover: %q, want %q", got, want)
	}
	help := lines[len(lines)-1] // help line: always the last one placed
	var act *Action
	for i, a := range help.Actions {
		if a.Key == KeyReact {
			act = &help.Actions[i]
		}
	}
	if act == nil {
		t.Fatalf("no reaction action in the help: %q", texts(lines))
	}
	if act.Emoji != "👍" {
		t.Fatalf("emoji: %q", act.Emoji)
	}
	if got := colText(LineText(help), act.Col0, act.Col1); got != "👍" {
		t.Fatalf("columns %d-%d → %q", act.Col0, act.Col1, got)
	}
	if got := strings.TrimLeft(LineText(help), " "); !strings.HasPrefix(got, "👍 · ") {
		t.Fatalf("emoji not at the head of the help: %q", got)
	}

	// Wrap width: nothing sticks out.
	long := &model.Msg{ID: 2, Date: m.Date, From: "alice", FromID: 9, Text: strings.Repeat("mot ", 40)}
	for _, l := range Message(long, Opts{Width: 40, Theme: theme.Terminal(), Images: "off", Self: self7, Hover: long, HoverHelp: true, Caps: allCaps}) {
		if w := Width(LineText(l)); w > 40 {
			t.Fatalf("line of %d columns for 40", w)
		}
	}

	// Emoji from the server: cleaned when drawn (no terminal sequence), but sent
	// back raw to the server through the action.
	hostile := &model.Msg{ID: 3, Date: m.Date, From: "alice", FromID: 9, Text: "coucou",
		Reactions: []model.Reaction{{Emoji: "\x1b[31mX", Count: 1, Mine: true}}}
	hl := Message(hostile, Opts{Width: 70, Theme: theme.Terminal(), Images: "off", Self: self7, Hover: hostile, HoverHelp: true, Caps: allCaps})
	for _, l := range hl {
		if strings.ContainsRune(LineText(l), 0x1b) {
			t.Fatalf("terminal sequence rendered: %q", LineText(l))
		}
	}
	if got, want := LineText(hl[0]), LineText(plain[0]); got != want {
		t.Fatalf("body shifted (hostile emoji): %q, want %q", got, want)
	}
	hHelp := hl[len(hl)-1]
	var hact *Action
	for i, a := range hHelp.Actions {
		if a.Key == KeyReact {
			hact = &hHelp.Actions[i]
		}
	}
	if hact == nil || hact.Emoji != "\x1b[31mX" {
		t.Fatalf("raw action emoji: %+v", hact)
	}

	// Avatar: hover no longer moves it nor drops it.
	o.Avatars = true
	hov := Message(m, o)
	o.Hover = nil
	noHov := Message(m, o)
	if hov[0].Avatar != noHov[0].Avatar || hov[0].AvatarCol != noHov[0].AvatarCol {
		t.Fatalf("avatar moved on hover: %+v vs %+v", hov[0], noHov[0])
	}
	if a, b := Width(LineText(hov[0])), Width(LineText(noHov[0])); a != b {
		t.Fatalf("body shifted (avatars): widths %d vs %d", a, b)
	}
}

// "l play" is offered only for a downloaded video, "s stop" once the play has
// started.
func TestHelpActionsVideo(t *testing.T) {
	md := &model.Media{Kind: model.MediaVideo, Label: "[video 00:42 · 38 Mo]", Loc: &tg.InputPhotoFileLocation{}}
	m := &model.Msg{ID: 1, Date: time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC), From: "alice", FromID: 9, Media: md}
	keys := func() string {
		s := ""
		for _, l := range Message(m, Opts{Width: 100, Theme: theme.Terminal(), Images: "off", Self: self7, Selected: m}) {
			for _, a := range l.Actions {
				s += string(a.Key)
			}
		}
		return s
	}
	if got := keys(); strings.ContainsRune(got, 'l') {
		t.Fatalf("video not downloaded: %q", got)
	}
	md.Path = "/tmp/v.mp4"
	if got := keys(); !strings.ContainsRune(got, 'l') || strings.ContainsRune(got, 's') {
		t.Fatalf("video downloaded: %q", got)
	}
	md.Frames = [][]byte{{1}, {2}} // play started
	if got := keys(); !strings.ContainsRune(got, 'l') || !strings.ContainsRune(got, 's') {
		t.Fatalf("playback started: %q", got)
	}
}

func TestRenderLinkPreview(t *testing.T) {
	md := &model.Media{Kind: model.MediaWebPage, Label: "[lien · Exemple · Un titre]",
		URL: "https://exemple.fr/article", Name: strings.Repeat("mot ", 40)}
	m := &model.Msg{ID: 1, From: "alice", FromID: 7, Text: "regarde", Media: md}
	o := Opts{Width: 40, Theme: theme.Terminal(), Images: "off", LinkPreviews: true}

	got := texts(Message(m, o))
	quotes := 0
	for _, l := range got {
		if strings.Contains(l, "│ ") {
			quotes++
		}
	}
	if quotes != 1+maxLinkDesc { // label + capped description
		t.Fatalf("│ block: %d lines\n%q", quotes, got)
	}
	if !strings.Contains(got[1], "│ [lien · Exemple · Un titre]") {
		t.Fatalf("label: %q", got)
	}
	if !strings.HasSuffix(got[len(got)-1], "…") {
		t.Fatalf("truncated description: %q", got)
	}
	// The label carries the URL: click and OSC 8 link.
	var url string
	for _, sp := range Message(m, o)[1].Spans {
		if sp.Style.URL != "" {
			url = sp.Style.URL
		}
	}
	if url != md.URL {
		t.Fatalf("label url: %q", url)
	}

	o.LinkPreviews = false
	for _, l := range texts(Message(m, o)) {
		if strings.Contains(l, "│") {
			t.Fatalf("preview disabled: %q", l)
		}
	}

	// Cut previews: the thumbnail goes with the block, the label stays.
	md.State, md.Frames, md.FrameW, md.FrameH = model.MediaReady, [][]byte{{1}}, 400, 200
	o.Images, o.CellW, o.CellH, o.MaxImgCols, o.MaxImgRows = "kitty", 8, 16, 20, 40
	for _, l := range Message(m, o) {
		if l.Img != nil {
			t.Fatal("preview disabled: image block present")
		}
	}
	imgs := 0
	o.LinkPreviews = true
	for _, l := range Message(m, o) {
		if l.Img != nil {
			imgs++
		}
	}
	if imgs == 0 || imgs > MaxLinkRows { // otherwise the assertion above would prove nothing
		t.Fatalf("image block: %d lines", imgs)
	}
}

func TestSafeURL(t *testing.T) {
	ok := []string{"https://exemple.fr/a?b=1#c", "HTTP://EXEMPLE.FR", "mailto:a@b.fr"}
	ko := []string{"", "/home/v/Downloads/evil.desktop", "file:///etc/passwd", "javascript:alert(1)",
		"data:text/html,<script>", "exemple.fr", "https://exemple.fr/\x1b]8;;x\x07",
		"https://exemple.fr/" + strings.Repeat("a", maxURL)}
	for _, s := range ok {
		if !SafeURL(s) {
			t.Errorf("wrongly refused: %q", s)
		}
	}
	for _, s := range ko {
		if SafeURL(s) {
			t.Errorf("wrongly accepted: %q", s)
		}
	}
}

// TestJumpAction : the quote line of a reply is clickable and aims at the
// quoted message; the palette offers "g quoted", or "g go" in a search
// window.
func TestJumpAction(t *testing.T) {
	o := Opts{Width: 60, Theme: theme.Terminal(), Images: "off", Self: self7}
	m := &model.Msg{ID: 9, Date: time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC),
		From: "alice", FromID: 3, Text: "oui",
		Reply: &model.Quote{ID: 4, From: "bob", Text: "on y va ?"}}

	var jumps []Action
	for _, l := range Message(m, o) {
		for _, a := range l.Actions {
			if a.Key == KeyJump {
				jumps = append(jumps, a)
			}
		}
	}
	if len(jumps) != 1 {
		t.Fatalf("%d jump zones, want 1", len(jumps))
	}
	if a := jumps[0]; a.ID != 4 || a.Col1 <= a.Col0 {
		t.Fatalf("jump zone: %+v", a)
	}

	o.Selected = m
	if all := strings.Join(texts(Message(m, o)), "\n"); !strings.Contains(all, "g cité") {
		t.Fatalf("palette without \"g cité\": %q", all)
	}
	o.Jump = true // search window: the result points back to its chat
	all := strings.Join(texts(Message(m, o)), "\n")
	if !strings.Contains(all, "g aller") || strings.Contains(all, "g cité") {
		t.Fatalf("search palette: %q", all)
	}
}

// The ✓/✓✓ tick of my messages is a hover zone (KeyTicks), stuck to the end
// of the first line, marker of the selection included.
func TestMessageTickAction(t *testing.T) {
	o := Opts{Width: 40, Theme: theme.Terminal(), Images: "off",
		ReadOutbox: func(*model.Msg) int { return 10 }, Caps: allCaps}
	m := &model.Msg{ID: 5, From: "moi", FromID: 7, Out: true, Text: "salut"}
	tickAct := func(ls []Line) *Action {
		for i := range ls[0].Actions {
			if ls[0].Actions[i].Key == KeyTicks {
				return &ls[0].Actions[i]
			}
		}
		return nil
	}
	ls := Message(m, o)
	a := tickAct(ls)
	if a == nil {
		t.Fatal("no KeyTicks action")
	}
	if w := lineWidth(ls[0]); a.Col1 != w || a.Col0 != w-Width(" ✓✓") {
		t.Fatalf("columns: %+v, width %d", a, w)
	}
	o.Selected = m // marker ▌ in front: the zone follows
	ls = Message(m, o)
	if a = tickAct(ls); a == nil || a.Col1 != lineWidth(ls[0]) {
		t.Fatalf("selection: %+v, width %d", a, lineWidth(ls[0]))
	}
	in := &model.Msg{ID: 5, From: "alice", FromID: 9, Text: "yo"}
	o.Selected, o.ReadInbox = nil, func(*model.Msg) int { return 10 }
	if a = tickAct(Message(in, o)); a != nil {
		t.Fatalf("incoming message: no zone, got %+v", a)
	}
}

// capsFunc : Opts.Caps of a network with the given capabilities.
func capsFunc(c model.Caps) func(*model.Msg) model.Caps {
	return func(*model.Msg) model.Caps { return c }
}

// allCaps : Opts.Caps of a network that can do everything — what the UI poses
// for Telegram.
var allCaps = capsFunc(model.AllCaps())

// A capability off takes what it carries out of the drawing: no "r réagir" nor
// quick emoji with no reactions, no tick nor KeyTicks zone with no read
// receipts, no "e éditer" with no edit.
func TestCapsGating(t *testing.T) {
	m := &model.Msg{ID: 5, ChatID: 3, Date: time.Date(2026, 9, 1, 12, 1, 0, 0, time.UTC),
		From: "moi", FromID: 7, Out: true, Text: "salut"}
	draw := func(caps model.Caps) (string, []Action) {
		o := Opts{Width: 80, Theme: theme.Terminal(), Images: "off", Self: self7, Selected: m,
			ReadOutbox: func(*model.Msg) int { return 1 }, Caps: capsFunc(caps)}
		lines := Message(m, o)
		var acts []Action
		for _, l := range lines {
			acts = append(acts, l.Actions...)
		}
		return strings.Join(texts(lines), "\n"), acts
	}
	has := func(acts []Action, k rune) bool {
		for _, a := range acts {
			if a.Key == k {
				return true
			}
		}
		return false
	}

	all, acts := draw(model.AllCaps())
	if !strings.Contains(all, "r réagir") || !strings.Contains(all, "e éditer") || !strings.Contains(all, " ✓") {
		t.Fatalf("every capability on: %q", all)
	}
	if !has(acts, KeyReact) || !has(acts, KeyTicks) {
		t.Fatalf("every capability on, actions: %+v", acts)
	}

	caps := model.AllCaps()
	caps.Reactions = false
	all, acts = draw(caps)
	if strings.Contains(all, "r réagir") {
		t.Fatalf("no reactions: %q", all)
	}
	if has(acts, KeyReact) { // quick emoji of the help line
		t.Fatalf("no reactions, actions: %+v", acts)
	}

	caps = model.AllCaps()
	caps.ReadReceipts = false
	all, acts = draw(caps)
	if strings.Contains(all, "✓") {
		t.Fatalf("no read receipts: %q", all)
	}
	if has(acts, KeyTicks) {
		t.Fatalf("no read receipts, actions: %+v", acts)
	}

	caps = model.AllCaps()
	caps.Edit = false
	if all, _ = draw(caps); strings.Contains(all, "e éditer") || !strings.Contains(all, "d supprimer") {
		t.Fatalf("no edit: %q", all)
	}
}

// A click on the label of a photo or a video opens the preview: the label
// carries KeyView. A link preview keeps its URL instead — the label opens the
// page.
func TestMessageLabelViewAction(t *testing.T) {
	o := Opts{Width: 60, Theme: theme.Terminal(), Images: "off", Caps: func(*model.Msg) model.Caps { return model.AllCaps() }}
	has := func(m *model.Msg) bool {
		for _, l := range Message(m, o) {
			for _, a := range l.Actions {
				if a.Key == KeyView {
					return true
				}
			}
		}
		return false
	}
	m := &model.Msg{ID: 1, From: "a", Media: &model.Media{Kind: model.MediaPhoto, Label: "[photo]", Loc: 1}}
	if !has(m) {
		t.Fatal("photo label: no view action")
	}
	m.Media = &model.Media{Kind: model.MediaVideo, Label: "[video]", Loc: 1}
	if !has(m) {
		t.Fatal("video label: no view action")
	}
	m.Media = &model.Media{Kind: model.MediaWebPage, Label: "[lien]", URL: "https://x"}
	if has(m) {
		t.Fatal("link preview label: view action instead of the page")
	}
}

// TestMessageSeconds : /set timestamps_seconds on — the prefix carries the seconds.
func TestMessageSeconds(t *testing.T) {
	o := Opts{Width: 40, Theme: theme.Terminal(), Timestamps: true, Seconds: true, Images: "off"}
	m := &model.Msg{ID: 1, Date: time.Date(2026, 8, 29, 12, 1, 5, 0, time.UTC), From: "alice", FromID: 7, Text: "yo"}
	if got := texts(Message(m, o)); got[0] != "12:01:05 <alice> yo" {
		t.Fatalf("line0: %q", got[0])
	}
}

func TestMapAttribution(t *testing.T) {
	m := &model.Msg{Media: &model.Media{Kind: model.MediaMap, Label: "[location]"}}
	var credit string
	for _, line := range Message(m, Opts{Width: 100, Theme: theme.Terminal(), Images: "off"}) {
		for _, span := range line.Spans {
			if span.Style.URL == media.MapCopyrightURL {
				credit += span.Text
			}
		}
	}
	if !strings.Contains(credit, "OpenStreetMap contributors") {
		t.Fatalf("map credit missing: %q", credit)
	}
}
