package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/spell"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// fakeSpell : "bonjor" and "fenetre" are wrong, everything else passes.
type fakeSpell struct{ added, ignored []string }

func (f *fakeSpell) Check(w string) bool { return w != "bonjor" && w != "fenetre" }
func (f *fakeSpell) Suggest(w string) []string {
	if w == "bonjor" {
		return []string{"bonjour", "bonsoir"}
	}
	return []string{"fenêtre"}
}
func (f *fakeSpell) Ignore(w string)           { f.ignored = append(f.ignored, w) }
func (f *fakeSpell) AddPersist(w string) error { f.added = append(f.added, w); return nil }

func spellUI(text string) (*UI, *fakeSpell) {
	f := &fakeSpell{}
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{Spell: "fr"},
		t: &term.Term{Cols: 80, Rows: 24}, spell: f}
	u.ed.Set(text)
	return u, f
}

func TestStyleRanges(t *testing.T) {
	base, bad := theme.Style{}, theme.Style{Underline: true}
	spans := styleRanges([]rune("ab cd"), 0, []spell.Range{{Start: 3, End: 5}}, base, bad)
	if len(spans) != 2 || spans[0].Text != "ab " || spans[1].Text != "cd" || !spans[1].Style.Underline {
		t.Fatalf("spans: %+v", spans)
	}
	// shifted view window: the range [3,5) starts before lo=4
	spans = styleRanges([]rune("d"), 4, []spell.Range{{Start: 3, End: 5}}, base, bad)
	if len(spans) != 1 || !spans[0].Style.Underline {
		t.Fatalf("shifted spans: %+v", spans)
	}
}

// spellBadRanges : the text's mistakes, without the word carrying the cursor.
func TestSpellBadRanges(t *testing.T) {
	u, _ := spellUI("bonjor les amis")
	u.ed.cur = 6 // stuck at the end of "bonjor": word in progress, not underlined
	if got := u.spellBadRanges(); len(got) != 0 {
		t.Fatalf("word under the cursor: %v", got)
	}
	u.ed.cur = 7 // after the space: underlined
	if got := u.spellBadRanges(); len(got) != 1 || got[0].Start != 0 || got[0].End != 6 {
		t.Fatalf("mistakes: %v", got)
	}
}

// Ctrl+R: walk through the mistakes — Enter corrects, a adds, the end closes.
func TestSpellFixWalk(t *testing.T) {
	u, f := spellUI("bonjor les fenetre la")
	u.spellFixStart()
	if u.spellFix == nil || u.spellFix.sugg[0] != "bonjour" {
		t.Fatalf("box: %+v", u.spellFix)
	}
	if !u.spellFixKey(term.Key{Code: term.Enter}) {
		t.Fatal("Enter consumed")
	}
	if got := u.ed.String(); got != "bonjour les fenetre la" {
		t.Fatalf("after Enter: %q", got)
	}
	if u.spellFix == nil || u.spellFix.sugg[0] != "fenêtre" {
		t.Fatalf("next word: %+v", u.spellFix)
	}
	if !u.spellFixKey(term.Key{Code: term.None, Rune: 'a'}) {
		t.Fatal("a consumed")
	}
	if len(f.added) != 1 || f.added[0] != "fenetre" {
		t.Fatalf("add: %v", f.added)
	}
	if u.spellFix != nil {
		t.Fatal("no more mistakes: the box closes")
	}
}

// i ignores the word for the session; an ordinary keystroke closes the box
// and passes through to the editor.
func TestSpellFixIgnoreAndTypeThrough(t *testing.T) {
	u, f := spellUI("bonjor fenetre")
	u.spellFixStart()
	if !u.spellFixKey(term.Key{Code: term.None, Rune: 'i'}) || len(f.ignored) != 1 {
		t.Fatalf("i: %v", f.ignored)
	}
	if u.spellFix == nil {
		t.Fatal("fenetre still remains")
	}
	if u.spellFixKey(term.Key{Code: term.None, Rune: 'x'}) {
		t.Fatal("ordinary keystroke: the key returns to the editor")
	}
	if u.spellFix != nil {
		t.Fatal("ordinary keystroke: box closed")
	}
}

// "/set spell <refused code>": the error is shown, the checker in place
// survives and the refused value does not enter the config.
func TestSetSpellRefusedKeepsChecker(t *testing.T) {
	u, f := spellUI("")
	old := spellNew
	defer func() { spellNew = old }()
	spellNew = func(mode, perso string) (spellChecker, error) { return nil, errors.New("unknown language: frr") }
	u.setCmd([]string{"spell", "frr"})
	if u.spell != spellChecker(f) {
		t.Fatal("the checker in place must survive a refused code")
	}
	if u.cfg.Spell != "fr" {
		t.Fatalf("cfg.Spell = %q: the refused value must not be saved", u.cfg.Spell)
	}
	if w := u.view(); len(w.Items) != 1 || !strings.Contains(w.Items[0].Sys, "unknown language") {
		t.Fatalf("message: %+v", w.Items)
	}
}
