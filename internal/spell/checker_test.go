//go:build !nospell

package spell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newFR(t *testing.T, perso string) *Checker {
	c, err := New("fr", perso)
	if err != nil {
		t.Skip("fr dictionary missing:", err)
	}
	return c
}

func TestCheckerFR(t *testing.T) {
	c := newFR(t, "")
	if !c.Check("fenêtre") || c.Check("fenetre") {
		t.Fatal("fenêtre must pass, fenetre must fail")
	}
	if s := c.Suggest("fenetre"); len(s) == 0 || s[0] != "fenêtre" {
		t.Fatalf("suggestions: %v", s)
	}
	if !c.Check("l'eau") || !c.Check("aujourd'hui") {
		t.Fatal("elision")
	}
	if !c.Check("l’eau") {
		t.Fatal("typographic apostrophe")
	}
	c.Ignore("tototruc")
	if !c.Check("tototruc") {
		t.Fatal("session ignore")
	}
}

func TestCheckerFrUs(t *testing.T) {
	c, err := New("fr+us", "")
	if err != nil {
		t.Skip("dictionaries missing:", err)
	}
	if !c.Check("window") || !c.Check("fenêtre") {
		t.Fatal("fr+us: both languages pass")
	}
	if c.Check("fenetre") {
		t.Fatal("fenetre still wrong")
	}
}

func TestCheckerPerso(t *testing.T) {
	p := filepath.Join(t.TempDir(), "perso.txt")
	os.WriteFile(p, []byte("loomword\n"), 0o644)
	c := newFR(t, p)
	if !c.Check("loomword") {
		t.Fatal("custom dict loaded")
	}
	if err := c.AddPersist("telegrame"); err != nil || !c.Check("telegrame") {
		t.Fatal("AddPersist")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "loomword\ntelegrame\n" {
		t.Fatalf("custom dict file: %q", b)
	}
}

// tinyDict writes a dictionary that holds "café" in ISO8859-1 bytes, with
// set as the SET line of its .aff, and gives the paths of its .aff and .dic.
func tinyDict(t *testing.T, set string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	aff, dic := filepath.Join(dir, "xx.aff"), filepath.Join(dir, "xx.dic")
	if err := os.WriteFile(aff, []byte("SET "+set+"\nTRY \xe9eacf\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dic, []byte("1\ncaf\xe9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return aff, dic
}

// A dictionary that is not UTF-8 gets the words in its own encoding, and its
// suggestions come back in UTF-8. Every accented word used to be wrong, and a
// suggestion came back as invalid UTF-8. A word the encoding cannot hold is
// wrong.
func TestCheckerLatin1Dictionary(t *testing.T) {
	h, err := newHunspell(tinyDict(t, "ISO8859-1"))
	if err != nil {
		t.Fatal(err)
	}
	c := &Checker{hs: []*hunhandle{h}, cache: map[string]bool{}}
	if !c.Check("café") {
		t.Error(`"café" is in the dictionary: want right`)
	}
	if s := c.Suggest("cafe"); len(s) == 0 || s[0] != "café" {
		t.Errorf(`suggestions for "cafe": %q, want "café" first`, s)
	}
	if c.Check("caféŝ") {
		t.Error(`"caféŝ" (ŝ is not in ISO8859-1): want wrong`)
	}
	if s := c.Suggest("caféŝ"); len(s) != 0 {
		t.Errorf(`suggestions for "caféŝ": %q, want none`, s)
	}
}

// A dictionary in an encoding that has no converter here is refused with its
// name: its words would be wrong and its suggestions invalid UTF-8.
func TestNewHunspellRefusesUnknownEncoding(t *testing.T) {
	if _, err := newHunspell(tinyDict(t, "ISCII-DEVANAGARI")); err == nil || !strings.Contains(err.Error(), "ISCII-DEVANAGARI") {
		t.Fatalf("ISCII-DEVANAGARI dictionary: err %v, want it refused with the encoding named", err)
	}
}

func TestCheckerBadMode(t *testing.T) {
	if _, err := New("zz", ""); err == nil { // no hunspell-zz can exist
		t.Fatal("unknown mode: error expected")
	}
}

// A personal file edited by hand may end without a newline: the added word
// goes on a line of its own. It used to join the last line ("foobar").
func TestAddPersistAfterLineWithoutNewline(t *testing.T) {
	p := filepath.Join(t.TempDir(), "spell.txt")
	if err := os.WriteFile(p, []byte("foo"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Checker{perso: p, cache: map[string]bool{}}
	if err := c.AddPersist("bar"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "foo\nbar\n" {
		t.Fatalf("spell.txt = %q, want %q", b, "foo\nbar\n")
	}
}

// The personal word list stays private like the rest of the config directory.
func TestAddPersistPrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "spell.txt")
	c := &Checker{perso: p, cache: map[string]bool{}}
	if err := c.AddPersist("mot"); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("perm: %v", st.Mode())
	}
}
