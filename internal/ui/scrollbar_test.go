package ui

import (
	"strings"
	"testing"
)

func TestScrollbar(t *testing.T) {
	if _, _, ok := scrollbar(10, 10, 0); ok { // everything fits on the screen
		t.Fatal("bar shown with nothing to scroll")
	}
	if s := scrollFromY(3, 10, 10); s != 0 {
		t.Fatalf("scrollFromY with nothing to scroll: %d", s)
	}
	for _, c := range [][2]int{{100, 10}, {7, 5}, {41, 13}, {1000, 3}} {
		total, view := c[0], c[1]
		maxScroll := total - view
		top, length, ok := scrollbar(total, view, maxScroll) // at the very top
		if !ok || top != 0 || length < 1 {
			t.Fatalf("%v top: %d %d %v", c, top, length, ok)
		}
		span := view - length
		if top, _, _ := scrollbar(total, view, 0); top != span { // at the very bottom
			t.Fatalf("%v bottom: %d ≠ %d", c, top, span)
		}
		if s := scrollFromY(0, view, total); s != maxScroll {
			t.Fatalf("%v click top: %d ≠ %d", c, s, maxScroll)
		}
		if s := scrollFromY(view-1, view, total); s != 0 {
			t.Fatalf("%v click bottom: %d", c, s)
		}
		for y := 0; y < view; y++ { // round trip: y → Scroll → y (cursor bounded)
			top, _, _ := scrollbar(total, view, scrollFromY(y, view, total))
			if top != min(y, span) {
				t.Fatalf("%v round trip y=%d: %d ≠ %d", c, y, top, min(y, span))
			}
		}
	}
}

// The grabbed bar (dragBar) is drawn with a full block: thicker under the
// mouse, back to ┃ once released.
func TestScrollbarThickWhileDragged(t *testing.T) {
	u := &UI{}
	var b strings.Builder
	u.drawScrollbar(&b, 80, 10, 30, 0)
	if !strings.Contains(b.String(), "┃") || strings.Contains(b.String(), "█") {
		t.Fatalf("idle bar: want ┃ without █, got %q", b.String())
	}
	b.Reset()
	u.drag = dragBar
	u.drawScrollbar(&b, 80, 10, 30, 0)
	if !strings.Contains(b.String(), "█") {
		t.Fatalf("dragged bar: want █, got %q", b.String())
	}
}
