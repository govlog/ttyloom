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

// Lookup : the gemoji aliases and the codes made from the Unicode names both
// answer; an exact code leads, then a code that starts with the query, then a
// word of a code; inside a rank, the recents lead.
func TestLookup(t *testing.T) {
	first := func(q string, recent []string) Hit {
		t.Helper()
		h := Lookup(q, recent)
		if len(h) == 0 {
			t.Fatalf("Lookup(%q): no hit", q)
		}
		return h[0]
	}
	for q, want := range map[string]Hit{
		"+1":          {Char: "👍", Code: "+1", Exact: true},           // gemoji alias
		"tada":        {Char: "🎉", Code: "tada", Exact: true},         // gemoji alias
		"thumbs_up":   {Char: "👍", Code: "thumbs_up", Exact: true},    // made from "thumbs up"
		"flag_france": {Char: "🇫🇷", Code: "flag_france", Exact: true}, // "flag: France"
		"fire":        {Char: "🔥", Code: "fire", Exact: true},         // before fire_engine…
		"thumbsu":     {Char: "👍", Code: "thumbsup"},                  // prefix of an alias
	} {
		if got := first(q, nil); got != want {
			t.Errorf("Lookup(%q)[0] = %+v, want %+v", q, got, want)
		}
	}
	if got := first("popper", nil); got.Char != "🎉" || got.Code != "party_popper" {
		t.Errorf("a word inside a code: %+v", got)
	}
	// "ab": 🆎 (exact) leads; a recent 🔤 (abc, prefix) passes the prefix codes
	// earlier in the table, but never the exact one.
	if h := Lookup("ab", nil); len(h) < 3 || h[0].Char != "🆎" || h[1].Char == "🔤" {
		t.Fatalf("Lookup(ab) = %+v", h[:min(3, len(h))])
	}
	if h := Lookup("ab", []string{"🔤"}); h[0].Char != "🆎" || h[1].Char != "🔤" {
		t.Errorf("Lookup(ab, recent 🔤) = %+v", h[:3])
	}
	if Lookup("", nil) != nil || Lookup("zzznope", nil) != nil {
		t.Error("an empty or unknown query gives no hit")
	}
}
