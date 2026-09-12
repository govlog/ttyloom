package ui

import (
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/emoji"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

func TestPickerGrid(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var picked string
	p := newPicker(31, 8, func(s string) { picked = s })
	if p.cols != 10 || p.rows != 4 { // 29 inner cells / 3, 8 - borders - search - name
		t.Fatalf("grid: %dx%d", p.cols, p.rows)
	}
	n := len(p.items)
	if n < 1500 {
		t.Fatalf("items: %d", n)
	}
	p.Key(term.Key{Code: term.Left})
	if p.cur != 0 {
		t.Fatalf("lower bound: %d", p.cur)
	}
	p.Key(term.Key{Code: term.Right})
	p.Key(term.Key{Code: term.Down})
	if p.cur != 11 {
		t.Fatalf("right then down: %d", p.cur)
	}
	p.Key(term.Key{Code: term.Up})
	if p.cur != 1 {
		t.Fatalf("up: %d", p.cur)
	}
	for i := 0; i < 200; i++ {
		p.Key(term.Key{Code: term.PgDn})
	}
	if p.cur != n-1 {
		t.Fatalf("upper bound: %d / %d", p.cur, n)
	}
	for _, r := range "grin" {
		p.Key(term.Key{Rune: r})
	}
	if len(p.items) == 0 || len(p.items) >= n || p.cur != 0 {
		t.Fatalf("filter: %d items, cur=%d", len(p.items), p.cur)
	}
	p.Key(term.Key{Code: term.Backspace})
	if string(p.query) != "gri" || len(p.items) != len(emoji.Search("gri")) {
		t.Fatalf("backspace: %q, %d items", string(p.query), len(p.items))
	}
	p.Key(term.Key{Rune: 'n'})
	first := p.items[0].Char
	if done := p.Key(term.Key{Code: term.Enter}); !done || picked != first {
		t.Fatalf("enter: done=%v picked=%q want %q", done, picked, first)
	}
	if r := emoji.Recent(config.RecentPath()); len(r) != 1 || r[0] != first {
		t.Fatalf("recents: %v", r)
	}
	// Esc closes with no choice; an empty search = recents first.
	q := newPicker(31, 8, func(string) { t.Fatal("Esc must choose nothing") })
	if q.items[0].Char != first {
		t.Fatalf("recent first: %q", q.items[0].Char)
	}
	if !q.Key(term.Key{Code: term.Esc}) {
		t.Fatal("Esc closes")
	}
}

func TestPickerLines(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	p := newPicker(31, 8, nil)
	lines := p.Lines(theme.Terminal())
	if len(lines) != 8 {
		t.Fatalf("height: %d", len(lines))
	}
	text := render.LineText
	if got := text(lines[0]); got != "┌"+strings.Repeat("─", 29)+"┐" {
		t.Fatalf("top border: %q", got)
	}
	if got := text(lines[7]); got != "└"+strings.Repeat("─", 29)+"┘" {
		t.Fatalf("bottom border: %q", got)
	}
	if got := text(lines[1]); !strings.HasPrefix(got, "│🔍 ") || render.Width(got) != 31 {
		t.Fatalf("search: %q (%d)", got, render.Width(got))
	}
	if got := text(lines[6]); !strings.Contains(got, p.items[0].Name) {
		t.Fatalf("name: %q", got)
	}
	// The grid shows cols emojis per line, the current one in its own span.
	if got := text(lines[2]); !strings.HasPrefix(got, "│"+p.items[0].Char) {
		t.Fatalf("grid: %q", got)
	}
	if lines[2].Spans[1].Text != p.items[0].Char || !lines[2].Spans[1].Style.Reverse {
		t.Fatalf("current emoji reversed: %+v", lines[2].Spans[1])
	}
}

// Click : the grid starts 2 lines under the top of the box, columns of 3
// cells from x=1 on.
func TestPickerClick(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var picked string
	p := newPicker(31, 8, func(s string) { picked = s })
	want := p.items[1].Char
	if !p.Click(4, 2) || picked != want || p.cur != 1 {
		t.Fatalf("cell (1,0): picked=%q cur=%d want %q", picked, p.cur, want)
	}
	for _, c := range [][2]int{{4, 1}, {0, 2}, {p.width() - 1, 2}, {4, p.rows + 2}} {
		if p.Click(c[0], c[1]) {
			t.Fatalf("outside grid: %v", c)
		}
	}
	if i := p.cellAt(1, 3); i != p.cols { // 2nd line of the grid
		t.Fatalf("2nd line: %d", i)
	}
}

// Reaction picker: nothing but the allowed list, in Telegram order, deaf to
// the search; header "reaction".
func TestPickerReactions(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	list := []string{"🔥", "👍", "❤"}
	var picked string
	p := newReactPicker(31, 8, list, func(s string) { picked = s })
	if len(p.items) != 3 || p.items[0].Char != "🔥" || p.items[2].Char != "❤" {
		t.Fatalf("items: %+v", p.items)
	}
	if p.items[2].Name == "" || p.items[0].Name == "" { // names taken from the table (❤️ is qualified there)
		t.Fatalf("names: %+v", p.items)
	}
	for _, r := range "grin" { // the search does not apply
		p.Key(term.Key{Rune: r})
	}
	if len(p.items) != 3 {
		t.Fatalf("search: %d items", len(p.items))
	}
	if got := render.LineText(p.Lines(theme.Terminal())[1]); !strings.HasPrefix(got, "│réaction") {
		t.Fatalf("header: %q", got)
	}
	p.Key(term.Key{Code: term.Right})
	if done := p.Key(term.Key{Code: term.Enter}); !done || picked != "👍" {
		t.Fatalf("choice: done=%v picked=%q", done, picked)
	}
}

// A network that takes any reaction (Discord) gets the whole table with the
// search, and a ":name:" typed is a custom emoji of the room, sent as it is.
func TestOpenReactPickerAny(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	c := &model.Chat{Net: "discord", ID: 5}
	b := &fakeBackend{caps: model.Caps{Reactions: true, AnyReaction: true}}
	u := &UI{ws: NewWindows(), agg: &Window{}, t: &term.Term{Cols: 80, Rows: 24},
		chats: map[model.ChatKey]*model.Chat{c.Key(): c}, nets: map[string]model.Backend{"discord": b}}
	it := &Item{Msg: &model.Msg{Net: "discord", ID: 7, ChatID: 5}}
	u.openReactPicker(it)
	if u.picker == nil || len(u.picker.items) < 1000 {
		t.Fatalf("picker: %+v", u.picker)
	}
	for _, r := range ":emoji_7:" {
		u.picker.Key(term.Key{Rune: r})
	}
	if u.picker.items[0].Char != ":emoji_7:" {
		t.Fatalf("custom name typed: %+v", u.picker.items[:1])
	}
	u.picker.Key(term.Key{Code: term.Enter})
	if b.reacted != ":emoji_7:" {
		t.Fatalf("reaction sent = %q, want the custom name", b.reacted)
	}
}

// The custom emojis of the room come first, answer the search by name and
// are drawn by the start of their name; the choice is the ":name:" itself.
func TestPickerCustoms(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var picked string
	p := newPicker(31, 8, func(s string) { picked = s })
	p.customs, p.recent = []string{":emoji_7:", ":profil:"}, nil // the recents stay first when there are some
	p.filter()
	if len(p.items) < 1000 || p.items[0].Char != ":emoji_7:" || p.items[1].Char != ":profil:" {
		t.Fatalf("head of the items: %+v", p.items[:2])
	}
	if got := render.LineText(p.Lines(theme.Terminal())[2]); !strings.HasPrefix(got, "│em pr ") {
		t.Fatalf("grid: %q", got)
	}
	for _, r := range "PROF" {
		p.Key(term.Key{Rune: r})
	}
	if len(p.items) != 1 || p.items[0].Name != "profil" {
		t.Fatalf("search by name: %+v", p.items)
	}
	p.Key(term.Key{Code: term.Enter})
	if picked != ":profil:" {
		t.Fatalf("picked %q", picked)
	}
}

// openReactPicker : the list comes from the chat of the message; no reaction
// allowed = no picker, just a status message.
func TestOpenReactPicker(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	c := &model.Chat{Net: model.NetTelegram, ID: 5, Reactions: []string{"🔥"}}
	u := &UI{ws: NewWindows(), agg: &Window{}, t: &term.Term{Cols: 80, Rows: 24},
		chats:     map[model.ChatKey]*model.Chat{c.Key(): c},
		reactList: map[string][]string{model.NetTelegram: {"👍", "🔥"}}}
	it := &Item{Msg: &model.Msg{Net: model.NetTelegram, ID: 7, ChatID: 5}}
	u.openReactPicker(it)
	if u.picker == nil || len(u.picker.items) != 1 || u.picker.items[0].Char != "🔥" {
		t.Fatalf("picker: %+v", u.picker)
	}
	u.picker = nil
	c.Reactions = []string{}
	u.openReactPicker(it)
	if u.picker != nil || u.flashMsg != "aucune réaction autorisée ici" {
		t.Fatalf("chat without reaction: picker=%v flash=%q", u.picker != nil, u.flashMsg)
	}
}

// An emoji from the server does not drive the terminal: nothing raw in the
// grid nor on the name line, box width unchanged — but pick() does give the
// first string back.
func TestPickerReactionsClean(t *testing.T) {
	const hostile = "\x1b]0;pwn\a"
	var picked string
	p := newReactPicker(31, 8, []string{hostile, "👍"}, func(s string) { picked = s })
	for _, l := range p.Lines(theme.Terminal()) {
		if got := render.LineText(l); strings.ContainsAny(got, "\x1b\a") {
			t.Fatalf("control sequence: %q", got)
		} else if w := render.Width(got); w != p.width() {
			t.Fatalf("width %d, expected %d: %q", w, p.width(), got)
		}
	}
	if p.Key(term.Key{Code: term.Enter}); picked != hostile {
		t.Fatalf("emoji returned: %q", picked)
	}
}

// TestPickerCellWidth : an emoji cell always reads cellW-1 by render.Width —
// text presentation by default included (✍, ☃, ❤…), which reads 1 with no
// normalisation while Ghostty shows it on 2 cells — and no line of the grid
// goes past the width of the box.
func TestPickerCellWidth(t *testing.T) {
	chars := []string{"✍", "☃", "❤", "👍", "🇫🇷"}
	for _, e := range chars {
		if w := render.Width(emojiCell(e)); w != cellW-1 {
			t.Fatalf("emojiCell(%q) width %d, want %d", e, w, cellW-1)
		}
	}
	p := newReactPicker(31, 8, chars, nil)
	for _, l := range p.Lines(theme.Terminal()) {
		if got, want := render.Width(render.LineText(l)), p.width(); got != want {
			t.Fatalf("line %q: width %d, want %d", render.LineText(l), got, want)
		}
	}
}

// TestReactPickerHeight : the box of the reaction picker fits the number of
// items (ceil / cols) instead of always taking the height cap.
func TestReactPickerHeight(t *testing.T) {
	items := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "👍"
		}
		return out
	}
	if p := newReactPicker(25, 20, items(5), nil); p.cols != 8 || p.rows != 1 {
		t.Fatalf("5 items/8 cols: %dx%d, want 8x1", p.cols, p.rows)
	}
	if p := newReactPicker(25, 20, items(20), nil); p.rows != 3 {
		t.Fatalf("20 items/8 cols: %d lines, want 3", p.rows)
	}
	if p := newReactPicker(25, 6, items(20), nil); p.rows != 2 { // height-4 = 2: cap
		t.Fatalf("height cap: %d lines, want 2", p.rows)
	}
}

// TestEmojiCellSingleLine : ReactionEmoji.Emoticon comes from the server and
// nothing bounds it client side; the grid cell must stay on its line and keep
// its exact width.
func TestEmojiCellSingleLine(t *testing.T) {
	got := emojiCell(strings.Repeat("\n", 500))
	if strings.ContainsRune(got, '\n') || render.Width(got) != cellW-1 {
		t.Fatalf("%q wide %d", got, render.Width(got))
	}
}

// A chat already known (disk cache) takes the custom emojis of the fresh
// dialog list: without them the picker of an old chat would list none.
func TestRememberCustoms(t *testing.T) {
	old := &model.Chat{Net: "discord", ID: 5}
	u := &UI{chats: map[model.ChatKey]*model.Chat{old.Key(): old}}
	got := u.remember(&model.Chat{Net: "discord", ID: 5, Customs: []string{":emoji_7:"}})
	if got != old || len(old.Customs) != 1 {
		t.Fatalf("customs after remember = %v", old.Customs)
	}
}
