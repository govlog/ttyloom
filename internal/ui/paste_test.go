package ui

import (
	"strings"
	"testing"
	"time"
)

func TestPasteDecision(t *testing.T) {
	if pasteNeedsPrompt("une ligne") || !pasteNeedsPrompt("a\nb") {
		t.Fatal("classification")
	}
}

// TestDrawInputLongLine : a single-line paste made every repaint quadratic
// (2.3 s per frame at 16 000 characters, so at every key press). 64 KB, the
// cap of pasteText, must stay far under a frame.
func TestDrawInputLongLine(t *testing.T) {
	if testing.Short() {
		t.Skip("timing measurement")
	}
	u := &UI{ws: NewWindows(), agg: &Window{}}
	u.ed.Set(strings.Repeat("a", maxPasteBytes))
	start := time.Now()
	const n = 10
	for range n {
		var b strings.Builder
		u.drawInput(&b, 1, 0, 80)
	}
	if d := time.Since(start) / n; d > 50*time.Millisecond {
		t.Fatalf("%v per render", d)
	}
}

// TestPasteCap : above the cap the paste is refused with a message, and the
// input line stays untouched.
func TestPasteCap(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}}
	w := u.view()
	u.pasteText(w, strings.Repeat("a", maxPasteBytes+1))
	if u.ed.String() != "" {
		t.Fatalf("paste inserted: %d bytes", len(u.ed.String()))
	}
	if len(w.Items) != 1 || w.Items[0].Sys == "" {
		t.Fatalf("no refusal message: %+v", w.Items)
	}
	u.pasteText(w, strings.Repeat("a", maxPasteBytes))
	if len(u.ed.String()) != maxPasteBytes {
		t.Fatalf("paste under the cap refused: %d", len(u.ed.String()))
	}
}
