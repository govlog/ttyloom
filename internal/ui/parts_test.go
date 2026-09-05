package ui

import (
	"testing"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// TestParticipantsBox : the box is stuck to the upper right corner of the
// message area (the scrollbar is outside cols), capped in width as well as in
// height, and never goes past the edge of a narrow area.
func TestParticipantsBox(t *testing.T) {
	x, y, w, h := partsBoxRect(90, 40, 10)
	if x != 60 || y != 0 || w != 30 || h != 12 { // cols/3, len+2
		t.Fatalf("%d %d %d %d", x, y, w, h)
	}
	if _, _, w, h := partsBoxRect(200, 20, 50); w != 32 || h != 10 { // caps: 32, rows/2
		t.Fatalf("%d %d", w, h)
	}
	if x, _, w, h := partsBoxRect(20, 4, 0); x != 4 || w != 16 || h != 3 { // floors
		t.Fatalf("%d %d %d", x, w, h)
	}
	if x, _, w, h := partsBoxRect(10, 2, 0); x != 0 || w != 10 || h != 2 { // area smaller than the floors
		t.Fatalf("%d %d %d", x, w, h)
	}
	// Degenerate area: the box keeps its two borders and nothing more.
	if _, _, w, h := partsBoxRect(1, 1, 0); w != 2 || h != 2 {
		t.Fatalf("%d %d", w, h)
	}
	if n := len((&partsBox{}).Lines(theme.Terminal(), 2, 2)); n != 2 {
		t.Fatalf("%d lines for h=2", n)
	}
	// Drawing: h lines of w cells exactly, title cut in the border.
	p := &partsBox{title: "un titre beaucoup trop long pour la boîte",
		lines: []model.Participant{{Text: "★ alice"}, {Text: "bob"}}}
	_, _, w, h = partsBoxRect(60, 40, len(p.lines))
	ls := p.Lines(theme.Terminal(), w, h)
	if len(ls) != h {
		t.Fatalf("%d lines for h=%d", len(ls), h)
	}
	for i, l := range ls {
		got := 0
		for _, sp := range l.Spans {
			got += render.Width(sp.Text)
		}
		if got != w {
			t.Fatalf("line %d: %d cells for w=%d", i, got, w)
		}
	}
}

// TestPartsCloseHit : the [x] cross of the top border takes the 3 cells before
// the ┐ corner, and a click on it closes the box.
func TestPartsCloseHit(t *testing.T) {
	for _, w := range []int{16, 30, 32} {
		c0, ok := partsCloseCol(w)
		if !ok || c0 != w-4 {
			t.Fatalf("w=%d: cross at %d (%v)", w, c0, ok)
		}
		l := (&partsBox{title: "titre"}).Lines(theme.Terminal(), w, 3)[0]
		if got := []rune(render.LineText(l)); string(got[c0:c0+3]) != "[x]" || string(got[w-1]) != "┐" {
			t.Fatalf("w=%d: border %q", w, render.LineText(l))
		}
	}
	if _, ok := partsCloseCol(5); ok { // box too narrow: no cross
		t.Fatal("cross on a 5-cell box")
	}
	// Click on the cross: the box closes (partsOn follows).
	u := &UI{ws: NewWindows(), agg: &Window{}, partsOn: true,
		parts: &partsBox{title: "titre", lines: []model.Participant{{Text: "alice"}}}}
	r := rect{row: 2, col: 10, w: 20, h: 3}
	c0, _ := partsCloseCol(r.w)
	u.partsMouse(term.MouseEvent{Button: 0, X: r.col + c0 + 1, Y: r.row, Press: true}, r)
	if u.partsOn || u.parts != nil {
		t.Fatalf("cross: partsOn = %v, parts = %v", u.partsOn, u.parts)
	}
}
