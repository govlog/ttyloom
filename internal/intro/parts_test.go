package intro

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Every registered part, at sizes from one cell to a huge screen and at times
// up to well past its slot: no panic, ASCII glyphs only, A in 0..1.
func TestPartsStayInBounds(t *testing.T) {
	for _, mk := range parts {
		for _, sz := range [][2]int{{1, 1}, {40, 12}, {80, 24}, {320, 100}} {
			for _, ts := range []float64{0, 0.016, 1.5, 7.3, 14.99, 120} {
				p, c := mk(), Canvas{Cols: sz[0], Rows: sz[1], Aspect: 2.125}
				l := NewLayer(c.Cols, c.Rows)
				p.Draw(l, c, ts)
				for i, f := range l.Cells {
					if f.Ch != 0 && (f.Ch < 0x20 || f.Ch > 0x7e) {
						t.Fatalf("%s %v t=%v cell %d: glyph %q", p.Name(), sz, ts, i, f.Ch)
					}
					if !(f.A >= 0 && f.A <= 1) {
						t.Fatalf("%s %v t=%v cell %d: A %v", p.Name(), sz, ts, i, f.A)
					}
				}
			}
		}
	}
}

// The show plays the parts of the spec.
func TestPartsRegistered(t *testing.T) {
	var got []string
	for _, mk := range parts {
		got = append(got, mk().Name())
	}
	for _, want := range []string{"tunnel", "fire", "cube", "torus", "copper", "matrix"} {
		if !slices.Contains(got, want) {
			t.Fatalf("part %q missing: %v", want, got)
		}
	}
}

// BenchmarkFrame : one frame of each part at 200 × 60, as the UI makes it:
// the part, the composition over a chat frame, the text sent. The budget is
// 4 ms a frame.
func BenchmarkFrame(b *testing.B) {
	c := Canvas{Cols: 200, Rows: 60, Aspect: 2, Version: "v0.17-beta"}
	var chat strings.Builder
	for y := range c.Rows {
		fmt.Fprintf(&chat, "\x1b[%d;1H\x1b[0;38;2;205;214;244;48;2;30;30;46m%s\x1b[K", y+1, strings.Repeat("chat text 😀 ", 14))
	}
	screen := chat.String()
	for _, mk := range parts {
		p := mk()
		b.Run(p.Name(), func(b *testing.B) {
			t := 3.0
			for b.Loop() {
				l := NewLayer(c.Cols, c.Rows)
				p.Draw(l, c, t)
				_ = Composite(Parse(screen, c.Cols, c.Rows), l, 1, rgbKnown).Emit()
				t += 1.0 / 60
			}
		})
	}
}

// The copper scroller never runs under the hint, whatever the size.
func TestCopperLeavesTheHintRow(t *testing.T) {
	for _, sz := range [][2]int{{40, 12}, {80, 24}, {138, 34}, {200, 60}} {
		c := Canvas{Cols: sz[0], Rows: sz[1], Aspect: 2, Version: "v0.17-beta"}
		y := hintRow(c)
		for ts := 0.0; ts < partLen; ts += 0.05 {
			l := NewLayer(c.Cols, c.Rows)
			newCopper().Draw(l, c, ts)
			for x := 0; x < c.Cols; x++ {
				if f := l.At(x, y); f.Ch != 0 {
					t.Fatalf("%v t=%.2f: glyph %q on the hint row at column %d", sz, ts, f.Ch, x)
				}
			}
		}
	}
}

// The scroller writes the version of any build: the small font has every
// letter, digit and sign a version holds.
func TestSmallFontCoversVersions(t *testing.T) {
	for _, r := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-*_ " {
		if _, ok := smallFont[r]; !ok {
			t.Errorf("small font: no %q", r)
		}
	}
}

// BenchmarkTransition : a frame in the middle of a transition, both parts
// drawn, for every pair, as BenchmarkFrame does.
func BenchmarkTransition(b *testing.B) {
	c := Canvas{Cols: 200, Rows: 60, Aspect: 2, Version: "v0.17-beta"}
	var chat strings.Builder
	for y := range c.Rows {
		fmt.Fprintf(&chat, "\x1b[%d;1H\x1b[0;38;2;205;214;244;48;2;30;30;46m%s\x1b[K", y+1, strings.Repeat("chat text 😀 ", 14))
	}
	screen := chat.String()
	for _, mkA := range parts {
		for _, mkB := range parts {
			pa, pb := mkA(), mkB()
			if pa.Name() == pb.Name() {
				continue
			}
			b.Run(pa.Name()+"-"+pb.Name(), func(b *testing.B) {
				t := 14.5
				for b.Loop() {
					la, lb := NewLayer(c.Cols, c.Rows), NewLayer(c.Cols, c.Rows)
					pa.Draw(la, c, t)
					pb.Draw(lb, c, t-14)
					transition(la, lb, 0.5, transCross)
					_ = Composite(Parse(screen, c.Cols, c.Rows), la, 1, rgbKnown).Emit()
					t += 1.0 / 60
				}
			})
		}
	}
}
