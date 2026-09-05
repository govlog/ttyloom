//go:build !nospell

package spell

import (
	"os"
	"path/filepath"
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

func TestCheckerBadMode(t *testing.T) {
	if _, err := New("zz", ""); err == nil { // no hunspell-zz can exist
		t.Fatal("unknown mode: error expected")
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
