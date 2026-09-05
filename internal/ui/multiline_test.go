package ui

import (
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
)

func multiUI() *UI {
	return &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{Multiline: true}, t: &term.Term{Rows: 24, Cols: 80},
		nets: map[string]model.Backend{model.NetTelegram: &fakeBackend{caps: model.AllCaps()}}}
}

// /set multiline on: Shift+Enter opens the expanded zone, Enter sends and
// folds it back, and a refused command leaves it open with its text.
func TestMultilineExpand(t *testing.T) {
	u := multiUI()
	u.ed.Set("a")
	u.key(term.Key{Code: term.Enter, Shift: true})
	if !u.multi || u.ed.String() != "a\n" {
		t.Fatalf("expand: multi=%v %q", u.multi, u.ed.String())
	}
	u.key(term.Key{Rune: 'b'})
	u.key(term.Key{Code: term.Enter}) // no chat bound: consumed anyway
	if u.multi || u.ed.String() != "" {
		t.Fatalf("send: multi=%v %q", u.multi, u.ed.String())
	}
	u.multi = true
	u.ed.Set("/quit\nx")
	u.key(term.Key{Code: term.Enter})
	if !u.multi || u.ed.String() != "/quit\nx" {
		t.Fatalf("refused command: multi=%v %q", u.multi, u.ed.String())
	}
}

// Esc folds the zone, the draft kept; the status bar carries the hint.
func TestMultilineEscFolds(t *testing.T) {
	u := multiUI()
	u.multi = true
	u.ed.Set("a\nb")
	u.key(term.Key{Code: term.Esc})
	if u.multi || u.ed.String() != "a\nb" || u.flashMsg == "" {
		t.Fatalf("fold: multi=%v %q flash=%q", u.multi, u.ed.String(), u.flashMsg)
	}
}

// Editing a message that holds line breaks comes back in the expanded zone;
// a one-line message does not.
func TestMultilineStartEdit(t *testing.T) {
	u := multiUI()
	u.startEdit(&Item{Msg: &model.Msg{Net: model.NetTelegram, ID: 1, Text: "a\nb"}})
	if !u.multi {
		t.Fatal("multiline edit: zone not open")
	}
	u.cancelMode()
	if u.multi {
		t.Fatal("cancelMode: zone stayed open")
	}
	u.startEdit(&Item{Msg: &model.Msg{Net: model.NetTelegram, ID: 2, Text: "une ligne"}})
	if u.multi {
		t.Fatal("one-line edit: zone wrongly open")
	}
}

// In the expanded zone, ↑/↓ move the cursor between the lines and Ctrl+←/→
// jump from word to word.
func TestMultilineCursorKeys(t *testing.T) {
	u := multiUI()
	u.multi = true
	u.ed.Set("un deux\ntrois")
	u.key(term.Key{Code: term.Up})
	if u.ed.Cursor() != 5 {
		t.Fatalf("↑: cursor %d", u.ed.Cursor())
	}
	u.key(term.Key{Code: term.Left, Ctrl: true})
	if u.ed.Cursor() != 3 {
		t.Fatalf("Ctrl+←: cursor %d", u.ed.Cursor())
	}
	u.key(term.Key{Code: term.Right, Ctrl: true})
	if u.ed.Cursor() != 7 {
		t.Fatalf("Ctrl+→: cursor %d", u.ed.Cursor())
	}
	u.key(term.Key{Code: term.Home})
	if u.ed.Cursor() != 0 {
		t.Fatalf("Home line: cursor %d", u.ed.Cursor())
	}
	u.key(term.Key{Code: term.Down})
	u.key(term.Key{Code: term.End})
	if u.ed.Cursor() != 13 {
		t.Fatalf("End line: cursor %d", u.ed.Cursor())
	}
}

// A multiline paste in the expanded zone asks, then inserts: as is with e,
// between ``` fences with c. Nothing is sent.
func TestMultilinePasteInsert(t *testing.T) {
	u := multiUI()
	u.multi = true
	u.ed.Set("avant\n")
	u.pasteText(u.view(), "x\ny")
	if u.pasteAsk == "" || !u.pasteIns {
		t.Fatalf("prompt: ask=%q ins=%v", u.pasteAsk, u.pasteIns)
	}
	u.key(term.Key{Rune: 'c'})
	if got := u.ed.String(); got != "avant\n```\nx\ny\n```" {
		t.Fatalf("code insertion: %q", got)
	}
	if u.pasteAsk != "" || u.pasteIns {
		t.Fatal("prompt not cleared")
	}
	u.ed.Set("")
	u.pasteText(u.view(), "x\ny")
	u.key(term.Key{Rune: 'e'})
	if got := u.ed.String(); got != "x\ny" {
		t.Fatalf("as-is insertion: %q", got)
	}
}

// The expanded zone: one screen line per draft line, the height capped at
// half the screen, and the message area shrunk by as much.
func TestMultilineLayout(t *testing.T) {
	u := multiUI()
	if u.inputRows() != 1 {
		t.Fatalf("folded: %d", u.inputRows())
	}
	u.multi = true
	u.ed.Set("a\nb")
	if u.inputRows() != 3 { // floor of the expanded zone
		t.Fatalf("floor: %d", u.inputRows())
	}
	u.ed.Set(strings.Repeat("x\n", 30))
	if u.inputRows() != 12 { // 24/2
		t.Fatalf("ceiling: %d", u.inputRows())
	}
	if u.viewRows() != 24-1-12 {
		t.Fatalf("viewRows: %d", u.viewRows())
	}
	u.ed.Set("aa\nbb")
	var b strings.Builder
	row, col := u.drawInput(&b, 10, 0, 40)
	out := b.String()
	if !strings.Contains(out, "aa") || !strings.Contains(out, "bb") {
		t.Fatalf("rendered: %q", out)
	}
	if row != 9 || col == 0 { // cursor at the end of "bb", second line of the zone
		t.Fatalf("cursor: line %d col %d", row, col)
	}
}
