package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/theme"
)

// TestQRHalfblocks : two lines of modules per screen line, one span per line.
func TestQRHalfblocks(t *testing.T) {
	bits := [][]bool{
		{true, false, true, false},
		{true, true, false, false},
		{false, false, false, false},
		{true, false, true, true},
	}
	want := []string{"█▄▀ ", "▄ ▄▄"}
	lines := qrHalfblocks(bits)
	if len(lines) != len(want) {
		t.Fatalf("%d lines, %d expected", len(lines), len(want))
	}
	for i, l := range lines {
		if len(l.Spans) != 1 {
			t.Fatalf("line %d: %d spans, 1 expected", i, len(l.Spans))
		}
		if l.Spans[0].Text != want[i] {
			t.Errorf("line %d: %q, %q expected", i, l.Spans[0].Text, want[i])
		}
		if l.Spans[0].Style.FG != qrFG || l.Spans[0].Style.BG != qrBG {
			t.Errorf("line %d: theme colors instead of black on white", i)
		}
	}
	// Odd number of module lines: the last screen line has only a top.
	if l := qrHalfblocks(bits[:3]); len(l) != 2 || l[1].Spans[0].Text != "    " {
		t.Errorf("odd bitmap: %v", l)
	}
}

// TestQRRectCenter : box centred, never off the screen.
func TestQRRectCenter(t *testing.T) {
	for _, c := range []struct {
		cols, rows, w, h int
		want             rect
	}{
		{80, 24, 56, 24, rect{row: 0, col: 12, h: 24, w: 56}},
		{100, 40, 56, 24, rect{row: 8, col: 22, h: 24, w: 56}},
		{40, 10, 56, 24, rect{row: 0, col: 0, h: 24, w: 56}}, // too small: clamped
	} {
		if got := centerRect(c.cols, c.rows, c.w, c.h); got != c.want {
			t.Errorf("centerRect(%d,%d,%d,%d) = %+v, %+v expected", c.cols, c.rows, c.w, c.h, got, c.want)
		}
	}
}

// qrBox.Lines : borders, exact width, the code in half blocks or blank under
// kitty, and the apology line when the box does not fit.
func TestQRLines(t *testing.T) {
	q := newQRBox("https://t.me/login/abcdef", time.Now().Add(30*time.Second))
	if q == nil {
		t.Fatal("QR encoding")
	}
	th := theme.Terminal()
	lines := q.Lines(th, 200, 100, false)
	if len(lines) != q.rows()+qrChrome {
		t.Fatalf("%d lines, %d expected", len(lines), q.rows()+qrChrome)
	}
	checkBox(t, "qr", lines, q.width())
	if got := lines[3].Spans[1].Style.FG; got != qrFG {
		t.Errorf("modules in theme colors: %+v", got)
	}
	// kitty: the same footprint, the lines of the code left blank for the image.
	kit := q.Lines(th, 200, 100, true)
	if len(kit) != len(lines) {
		t.Fatalf("kitty: %d lines, %d expected", len(kit), len(lines))
	}
	checkBox(t, "qr kitty", kit, q.width())
	if sp := kit[3].Spans[1]; strings.TrimSpace(sp.Text) != "" || sp.Style.BG != qrBG {
		t.Errorf("kitty: code line %q, bg %+v", sp.Text, sp.Style.BG)
	}
	// Screen too small: the code gives way to one line, the box stays a box.
	small := q.Lines(th, 10, 10, false)
	if len(small) != 1+qrChrome {
		t.Fatalf("narrow screen: %d lines", len(small))
	}
	checkBox(t, "qr narrow", small, q.width())
}
