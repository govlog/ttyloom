package intro

import (
	"slices"
	"testing"

	"github.com/govlog/ttyloom/internal/theme"
)

// Parse models what a terminal shows for a frame of draw(): styles, the erase
// to the end of the row, wide clusters, overwrites, and every sequence it does
// not draw skipped. Emit gives a frame that shows the same screen.
func TestParseModelsTheFrame(t *testing.T) {
	red := theme.Style{FG: theme.Color{Kind: 2, RGB: theme.RGB{R: 255}}, BG: theme.Ansi(4)}
	text := "\x1b[1;1H" + red.SGR() + "ab\x1b[K" +
		"\x1b[2;3H\x1b]8;;http://x\x1b\\" + theme.Style{}.SGR() + "😀z\x1b]8;;\x1b\\" +
		"\x1b_Ga=p,i=1\x1b\\" + "\x1b[3;1HXY\x1b[3;2H😀" // the emoji over the Y and the cell after it
	s := Parse(text, 6, 3)
	if c := s.At(0, 0); c.Text != "a" || c.Style.FG != red.FG || c.Style.BG != red.BG {
		t.Fatalf("a: %+v", c)
	}
	if c := s.At(5, 0); c.Text != " " || c.Style.BG != red.BG || c.Style.FG.Kind != 0 {
		t.Fatalf("EL fill: %+v", c)
	}
	if s.At(2, 1).Text != "😀" || s.At(3, 1).Text != "" || s.At(4, 1).Text != "z" {
		t.Fatalf("wide cluster: %+v", s.Cells[6:12])
	}
	if s.At(2, 1).Style.URL != "" {
		t.Fatal("links are not kept")
	}
	if s.At(0, 2).Text != "X" || s.At(1, 2).Text != "😀" || s.At(2, 2).Text != "" {
		t.Fatalf("overwrite: %+v", s.Cells[12:])
	}
	if s.At(6, 0) != nil || s.At(-1, 0) != nil || s.At(0, 3) != nil {
		t.Fatal("At outside the screen")
	}
	if s2 := Parse(s.Emit(), 6, 3); !slices.Equal(s.Cells, s2.Cells) {
		t.Fatalf("Emit then Parse changes the screen:\n%+v\n%+v", s.Cells, s2.Cells)
	}
}

// A narrow glyph written over either half of a wide one blanks the other
// half, as a terminal does; text past the last column is dropped.
func TestParseBreaksWideOnOverwrite(t *testing.T) {
	plain := theme.Style{}.SGR()
	s := Parse("\x1b[1;1H"+plain+"😀😀\x1b[1;2Hx\x1b[1;3Hy", 4, 1)
	want := []string{" ", "x", "y", " "}
	for i, w := range want {
		if s.Cells[i].Text != w {
			t.Fatalf("cell %d: %q, want %q (%+v)", i, s.Cells[i].Text, w, s.Cells)
		}
	}
	if s := Parse("\x1b[1;3H"+plain+"abc", 3, 1); s.At(2, 0).Text != "a" {
		t.Fatalf("past the end: %+v", s.Cells)
	}
	if s := Parse("\x1b[1;3H"+plain+"😀", 3, 1); s.At(2, 0).Text != " " {
		t.Fatalf("a wide cluster that does not fit is dropped: %+v", s.Cells)
	}
}

// Emit writes the same SGR as theme.Style.SGR, for every attribute and kind
// of colour: a terminal sees the styles the UI drew.
func TestEmitSGRMatchesTheme(t *testing.T) {
	theme.Curly = true
	defer func() { theme.Curly = false }()
	for _, st := range []theme.Style{
		{},
		{Bold: true, Dim: true, Italic: true, Reverse: true, Strike: true},
		{Underline: true},
		{Underline: true, Curly: true, FG: theme.Color{Kind: 2, RGB: theme.RGB{R: 1, G: 2, B: 3}}},
		{FG: theme.Ansi(3), BG: theme.Ansi(12)},
		{FG: theme.Ansi(9), BG: theme.Color{Kind: 2, RGB: theme.RGB{R: 255, G: 128}}},
	} {
		if got, want := string(appendSGR(nil, st)), st.SGR(); got != want {
			t.Errorf("%+v: %q, want %q", st, got, want)
		}
	}
}

// A cluster that opens with an ASCII byte (a keycap, a letter with a
// combining accent) stays one cluster, with its width.
func TestParseClustersAfterASCII(t *testing.T) {
	s := Parse("\x1b[1;1H"+theme.Style{}.SGR()+"#\uFE0F\u20E3xe\u0301y", 6, 1)
	want := []string{"#\uFE0F\u20E3", "", "x", "e\u0301", "y", " "}
	for i, w := range want {
		if s.Cells[i].Text != w {
			t.Fatalf("cell %d: %q, want %q (%+v)", i, s.Cells[i].Text, w, s.Cells)
		}
	}
}

// Emit writes only the colours that change: a frame of the demo changes
// colour at almost every cell, and a slow link pays for every byte.
func TestEmitWritesOnlyWhatChanges(t *testing.T) {
	s := &Screen{Cols: 80, Rows: 1, Cells: make([]Cell, 80)}
	bg := theme.Color{Kind: 2, RGB: theme.RGB{R: 30, G: 30, B: 46}}
	for i := range s.Cells {
		fg := theme.Color{Kind: 2, RGB: theme.RGB{R: 200, G: uint8(100 + i%2), B: 200}}
		s.Cells[i] = Cell{Text: "x", Style: theme.Style{FG: fg, BG: bg}}
	}
	if n := len(s.Emit()); n > 80*24 {
		t.Fatalf("%d bytes for 80 cells whose text colour alone changes", n)
	}
}

// Whatever the styles that follow each other, the screen Emit writes is the
// screen it was given.
func TestEmitRoundTrip(t *testing.T) {
	colours := []theme.Color{{}, theme.Ansi(1), theme.Ansi(12), {Kind: 2, RGB: theme.RGB{R: 1, G: 2, B: 3}}, {Kind: 2, RGB: theme.RGB{R: 250}}}
	s := &Screen{Cols: 40, Rows: 30, Cells: make([]Cell, 1200)}
	h := uint32(7)
	next := func(n int) int { h = h*1664525 + 1013904223; return int(h>>16) % n }
	for i := range s.Cells {
		s.Cells[i] = Cell{Text: string(rune('a' + next(26))), Style: theme.Style{
			FG: colours[next(len(colours))], BG: colours[next(len(colours))],
			Bold: next(4) == 0, Dim: next(5) == 0, Italic: next(4) == 0, Underline: next(4) == 0, Reverse: next(6) == 0, Strike: next(7) == 0}}
	}
	if s2 := Parse(s.Emit(), 40, 30); !slices.Equal(s.Cells, s2.Cells) {
		for i := range s.Cells {
			if s.Cells[i] != s2.Cells[i] {
				t.Fatalf("cell %d: %+v, want %+v", i, s2.Cells[i], s.Cells[i])
			}
		}
	}
}
