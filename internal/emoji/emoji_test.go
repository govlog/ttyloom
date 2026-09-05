package emoji

import (
	"path/filepath"
	"testing"
)

func TestSearch(t *testing.T) {
	all := All()
	if len(all) < 1500 {
		t.Fatalf("emoji.txt trop court : %d", len(all))
	}
	got := Search("grinning face")
	if len(got) == 0 || got[0].Char != "😀" {
		t.Fatalf("%+v", got)
	}
	if len(Search("ZZZZNOPE")) != 0 {
		t.Fatal("no match expected")
	}
	if len(Search("")) != len(all) {
		t.Fatal("empty query = all")
	}
}

func TestRecent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "recent")
	for _, c := range []string{"😀", "❤️", "😀"} {
		AddRecent(p, c)
	}
	if r := Recent(p); len(r) != 2 || r[0] != "😀" || r[1] != "❤️" {
		t.Fatalf("%v", r)
	}
	for i := 0; i < 30; i++ {
		AddRecent(p, string(rune('a'+i)))
	}
	if r := Recent(p); len(r) != 24 {
		t.Fatalf("cap: %d", len(r))
	}
}

// Base : this is the form without a variation selector that Telegram takes as
// a reaction; the emojis that carry none do not move.
func TestReactionStrip(t *testing.T) {
	for in, want := range map[string]string{"❤️": "❤", "❤": "❤", "👍": "👍", "": ""} {
		if got := Base(in); got != want {
			t.Fatalf("Base(%q) = %q, attendu %q", in, got, want)
		}
	}
}
