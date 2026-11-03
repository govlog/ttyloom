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
