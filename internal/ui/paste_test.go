package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/term"
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

// Ctrl+V reads the clipboard in the background: its prompt opens between two
// typed keys. An e or a c within 300 ms is typing, not an answer: it is
// swallowed and the prompt stays. Later, e sends.
func TestRegressionPastePromptIgnoresTypedKey(t *testing.T) {
	u, b, room, _ := queryUI()
	u.goTo(u.ws.ForChat(room.Key()))
	u.event(evClipText{Text: "first line\nsecond line"})
	u.key(term.Key{Rune: 'e'})
	u.key(term.Key{Rune: 'c'})
	if len(b.sends) != 0 || u.pasteAsk == "" {
		t.Fatalf("e and c typed right after Ctrl+V: sent %q, prompt open %v; want nothing sent and the prompt kept", b.text, u.pasteAsk != "")
	}
	u.pasteAt = time.Now().Add(-promptGuard)
	u.key(term.Key{Rune: 'e'})
	if len(b.text) != 1 || b.text[0] != "first line\nsecond line" || u.pasteAsk != "" {
		t.Fatalf("e 300 ms after the prompt: sent %q, prompt open %v; want the paste sent once", b.text, u.pasteAsk != "")
	}
}

// A code block pasted in the middle of a line: each fence is alone on its
// line, so the send parser finds the block and no raw fence goes out.
func TestRegressionCodePasteMidLine(t *testing.T) {
	u, _, _, _ := queryUI()
	u.multi = true
	u.ed.Set("see this: and then the end")
	for range len("and then the end") {
		u.key(term.Key{Code: term.Left})
	}
	u.key(term.Key{Code: term.Paste, Text: "a := 1\nb := 2"})
	u.pasteAt = time.Now().Add(-promptGuard)
	u.key(term.Key{Rune: 'c'})
	want := "see this: \n```\na := 1\nb := 2\n```\nand then the end"
	if got := u.ed.String(); got != want || len(parseFences(got)) != 3 {
		t.Fatalf("draft %q (%d segments), want %q: text, code block, text", got, len(parseFences(got)), want)
	}
}

// TestPasteKeepsIndent : a tab of a paste becomes four spaces. The draft
// cannot hold a raw tab, and cleaned as a control character each one gave a
// single space: pasted code lost its indentation.
func TestPasteKeepsIndent(t *testing.T) {
	got := normalizePaste("func f() {\r\n\tif x {\n\t\treturn\n\t}\n}")
	if want := "func f() {\n    if x {\n        return\n    }\n}"; got != want {
		t.Fatalf("%q, want %q", got, want)
	}
}
