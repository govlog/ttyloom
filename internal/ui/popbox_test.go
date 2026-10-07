package ui

import (
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/emoji"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
)

// emojiUI : a UI on a window bound to chat, the recents in a temporary cache.
func emojiUI(t *testing.T, chat *model.Chat) *UI {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{}, t: &term.Term{Cols: 80, Rows: 24}}
	u.ws.List = append(u.ws.List, &Window{Chat: chat})
	u.ws.Cur = 1
	return u
}

// The :… box opens on a word that starts with ":" and holds a letter or a
// digit — never on a smiley, a time, a URL, a lone ":" or in a command.
func TestEmojiBoxOpens(t *testing.T) {
	u := emojiUI(t, &model.Chat{ID: 7, Kind: model.ChatGroup, Title: "grp"})
	for line, want := range map[string]bool{
		":a": true, "yo :fi": true, ":+1": true, ":thumbs_u": true,
		":": false, ": ": false, ":fire ": false, "12:30": false, "http://x": false,
		":)": false, ":-(": false, ":zzznope": false, "/me :fi": false,
	} {
		u.ed.Set(line)
		u.popScan()
		if got := u.pop != nil && u.pop.emoji; got != want {
			t.Errorf("%q: box open %v, want %v", line, got, want)
		}
		u.pop, u.popMute = nil, 0
	}
}

// Tab inserts the emoji and a space and makes it a recent. Enter does the
// same, except on a single letter not chosen with the arrows: ":D" then Enter
// is a smiley in text.
func TestEmojiBoxPick(t *testing.T) {
	u := emojiUI(t, &model.Chat{ID: 7, Kind: model.ChatGroup, Title: "grp"})
	u.ed.Set("yo :joy")
	u.popScan()
	if u.pop == nil || !u.popKey(term.Key{Code: term.Tab}) || u.ed.String() != "yo 😂 " || u.pop != nil {
		t.Fatalf("Tab: %q", u.ed.String())
	}
	if r := emoji.Recent(config.RecentPath()); len(r) != 1 || r[0] != "😂" {
		t.Fatalf("recents: %v", r)
	}
	u.ed.Set(":D")
	u.popScan()
	if u.pop == nil || u.popKey(term.Key{Code: term.Enter}) {
		t.Fatal(":D then Enter: the line must go as typed")
	}
	u.popKey(term.Key{Code: term.Down})
	if !u.popKey(term.Key{Code: term.Enter}) || u.ed.String() == ":D" {
		t.Fatalf("Enter after an arrow picks: %q", u.ed.String())
	}
	u.ed.Set(":jo")
	u.popScan()
	if !u.popKey(term.Key{Code: term.Enter}) || u.ed.String() != "😂 " {
		t.Fatalf("Enter on two letters picks, the recent first: %q", u.ed.String())
	}
}

// The closing ":" of a whole shortcode converts it, as Discord and Slack do;
// a custom emoji of the room with that name stays as typed (the backend sends
// it), and so does a code that is only a prefix.
func TestEmojiBoxClosingColon(t *testing.T) {
	colon := term.Key{Code: term.None, Rune: ':'}
	g := &model.Chat{ID: 7, Kind: model.ChatGroup, Title: "grp"}
	u := emojiUI(t, g)
	u.ed.Set("yo :fire")
	u.popScan()
	if u.pop == nil || !u.popKey(colon) || u.ed.String() != "yo 🔥" {
		t.Fatalf("whole code: %q", u.ed.String())
	}
	u.ed.Set("yo :fir")
	u.popScan()
	if u.popKey(colon) {
		t.Fatal("a prefix is no code: the : is typed")
	}
	g.Customs = []string{":fire:"}
	u.ed.Set("yo :fire")
	u.popScan()
	if u.pop == nil || u.pop.rows[0].insert != ":fire:" || u.popKey(colon) {
		t.Fatal("a custom emoji of the room leads and stays as typed")
	}
}
