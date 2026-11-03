package intro

import (
	"testing"
	"time"
)

func TestPacer(t *testing.T) {
	var p Pacer
	for range 200 {
		if w, g := p.Next(5 * time.Millisecond); g || w < 11*time.Millisecond || w > 12*time.Millisecond {
			t.Fatalf("fast: %v %v", w, g)
		}
	}
	var q Pacer
	for i := range 12 {
		if _, g := q.Next(80 * time.Millisecond); g {
			t.Fatalf("gave up after %d frames (< 1 s)", i+1)
		}
	}
	if _, g := q.Next(80 * time.Millisecond); !g {
		t.Fatal("13 × 80 ms over 1/20 s: must give up")
	}
	var r Pacer
	for range 100 {
		r.Next(80 * time.Millisecond)
		if _, g := r.Next(10 * time.Millisecond); g {
			t.Fatal("a fast frame resets")
		}
	}
	var s Pacer
	if w, _ := s.Next(30 * time.Millisecond); w != 2*time.Millisecond {
		t.Fatalf("over 1/60 s: the floor of 2 ms, got %v", w)
	}
}
