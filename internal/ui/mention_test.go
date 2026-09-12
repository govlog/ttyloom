package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
)

func TestMentionWord(t *testing.T) {
	for _, c := range []struct {
		line  string
		cur   int
		start int
		q     string
		ok    bool
	}{
		{"@al", 3, 0, "al", true},
		{"salut @al", 9, 6, "al", true},
		{"salut @", 7, 6, "", true},      // bare @: full list
		{"salut @al", 7, 6, "", true},    // cursor right after the @
		{"mail a@b.c", 10, 0, "", false}, // @ in the middle of a word
		{"salut @al ", 10, 0, "", false}, // cursor after the word
		{"salut", 5, 0, "", false},
		{"", 0, 0, "", false},
	} {
		start, q, ok := mentionWord([]rune(c.line), c.cur)
		if ok != c.ok || (ok && (start != c.start || q != c.q)) {
			t.Errorf("%q cur=%d: start=%d q=%q ok=%v, want start=%d q=%q ok=%v",
				c.line, c.cur, start, q, ok, c.start, c.q, c.ok)
		}
	}
}

func TestMentionFilter(t *testing.T) {
	all := []model.Participant{
		{Text: "Alice", Query: "@alice"},
		{Text: "Éloi Dupont", Query: "@edu"},
		{Text: "Bob", Query: "42"}, // no @username: offered by name, mention by id
		{Text: "3 admins"},         // header line: never offered
	}
	if got := mentionFilter(all, ""); len(got) != 3 {
		t.Fatalf("empty filter: %d candidates, want 3", len(got))
	}
	if got := mentionFilter(all, "bo"); len(got) != 1 || got[0].Query != "42" || mentionInsert(got[0]) != "@Bob" {
		t.Fatalf("filter by name without username: %v", got)
	}
	if got := mentionFilter(all, "eloi"); len(got) != 1 || got[0].Query != "@edu" {
		t.Fatalf("filter by folded name: %v", got)
	}
	if got := mentionFilter(all, "ali"); len(got) != 1 || got[0].Query != "@alice" {
		t.Fatalf("filter by username: %v", got)
	}
}

func TestEditorReplace(t *testing.T) {
	var e Editor
	e.Set("salut @al fin")
	e.cur = 9 // after "@al"
	e.Replace(6, 9, "@alice ")
	if e.String() != "salut @alice  fin" || e.cur != 13 {
		t.Fatalf("Replace: %q cur=%d", e.String(), e.cur)
	}
}

// The box opens while typing @…, Esc mutes it for that word, Enter inserts
// the username and a space.
func TestMentionScanAndPick(t *testing.T) {
	g := &model.Chat{ID: 7, Kind: model.ChatGroup, Title: "grp"}
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{},
		t: &term.Term{Cols: 80, Rows: 24},
		partsCache: map[model.ChatKey]partsEntry{g.Key(): {at: time.Now(),
			lines: []model.Participant{{Text: "Alice", Query: "@alice"}, {Text: "Bob", Query: "@bob"}}}}}
	u.ws.List = append(u.ws.List, &Window{Chat: g})
	u.ws.Cur = 1
	u.ed.Set("yo @a")
	u.mentionScan()
	if u.mention == nil || len(u.mention.items) != 1 || u.mention.items[0].Query != "@alice" {
		t.Fatalf("scan: %+v", u.mention)
	}
	if !u.mentionKey(term.Key{Code: term.Esc}) || u.mention != nil {
		t.Fatal("Esc should close the box")
	}
	u.mentionScan()
	if u.mention != nil {
		t.Fatal("silent after Esc on the same word")
	}
	u.ed.Set("yo @ab") // the mutated word extends: still nothing
	u.mentionScan()
	if u.mention != nil {
		t.Fatal("silent while still inside the word")
	}
	u.ed.Set("yo ") // leaving the word rearms the box
	u.mentionScan()
	u.ed.Set("yo @b")
	u.mentionScan()
	if u.mention == nil {
		t.Fatal("new word: the box reopens")
	}
	if !u.mentionKey(term.Key{Code: term.Enter}) {
		t.Fatal("Enter should pick")
	}
	if u.ed.String() != "yo @bob " || u.mention != nil {
		t.Fatalf("insertion: %q", u.ed.String())
	}
}

// A member with no @username: the pick inserts @Name, the send turns it into
// a mention by id (the @ dropped), "Bob" leaving "@Bobby" alone.
func TestMentionByID(t *testing.T) {
	g := &model.Chat{ID: 7, Kind: model.ChatGroup, Title: "grp"}
	u := &UI{partsCache: map[model.ChatKey]partsEntry{g.Key(): {at: time.Now(),
		lines: []model.Participant{{Text: "Bob", Query: "42"}, {Text: "Kevin Homri", Query: "43"}, {Text: "Al", Query: "@al"}}}}}
	segs, ok := u.mentionSegs(g, []model.Seg{{Text: "yo @Kevin Homri, @Bobby et @Bob", Bold: true}})
	want := []model.Seg{{Text: "yo ", Bold: true}, {Text: "Kevin Homri", Kind: model.SegMention, UserID: 43, Bold: true},
		{Text: ", @Bobby et ", Bold: true}, {Text: "Bob", Kind: model.SegMention, UserID: 42, Bold: true}}
	if !ok || !slices.Equal(segs, want) {
		t.Fatalf("segs:\n got %+v\nwant %+v", segs, want)
	}
	if _, ok := u.mentionSegs(g, []model.Seg{{Text: "yo @al"}}); ok {
		t.Fatal("a @username is no mention by id")
	}
	ents := fenceEntities(segs)
	if len(ents) != 6 || ents[1] != (model.Span{Start: 3, End: 14, Kind: model.SpanMention, UserID: 43}) {
		t.Fatalf("echo spans: %+v", ents)
	}
}
