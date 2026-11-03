package intro

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/theme"
)

// stub : a part that draws one glyph moving with t.
type stub struct{ name string }

func (s stub) Name() string { return s.name }
func (s stub) Draw(l *Layer, c Canvas, t float64) {
	if f := l.At(int(t*10)%max(c.Cols, 1), 0); f != nil {
		*f = FX{Ch: '#', FG: theme.RGB{R: 255}, A: 1}
	}
}

// withStubParts registers n stub parts for one test.
func withStubParts(t *testing.T, n int) {
	saved := parts
	parts = nil
	for i := range n {
		name := fmt.Sprintf("stub%d", i)
		parts = append(parts, func() part { return stub{name} })
	}
	t.Cleanup(func() { parts = saved })
}

func TestShowPlaysEveryPartBeforeRepeating(t *testing.T) {
	withStubParts(t, 6)
	t0 := time.Unix(0, 0)
	s := NewShow(7, t0, "hint", "v0")
	seen := map[string]bool{}
	for i := range 6 {
		seen[s.Current(t0.Add(time.Duration(i)*15*time.Second+2*time.Second))] = true
	}
	if len(seen) != 6 {
		t.Fatalf("6 slots, %d parts: %v", len(seen), seen)
	}
	// The next round never opens on the part that closed the previous one.
	for seed := range uint64(50) {
		s := NewShow(seed, t0, "", "")
		for round := 1; round < 4; round++ {
			last := s.Current(t0.Add(time.Duration(6*round-1)*15*time.Second + 2*time.Second))
			next := s.Current(t0.Add(time.Duration(6*round)*15*time.Second + 2*time.Second))
			if last == next {
				t.Fatalf("seed %d round %d: %s twice in a row", seed, round, last)
			}
		}
	}
}

func TestShowFadesOutThenEnds(t *testing.T) {
	withStubParts(t, 1)
	t0 := time.Unix(0, 0)
	s := NewShow(1, t0, "", "")
	stop := t0.Add(5 * time.Second)
	s.Stop(stop)
	if !s.Stopping() {
		t.Fatal("Stopping after Stop")
	}
	_, dark := s.Frame(stop.Add(150*time.Millisecond), Canvas{Cols: 80, Rows: 24, Aspect: 2})
	if dark <= 0 || dark >= 1 || s.Done(stop.Add(150*time.Millisecond)) {
		t.Fatalf("mid fade: dark %v", dark)
	}
	if !s.Done(stop.Add(301 * time.Millisecond)) {
		t.Fatal("must end after 0.3 s")
	}
}

func TestShowFadesIn(t *testing.T) {
	withStubParts(t, 1)
	t0 := time.Unix(0, 0)
	s := NewShow(1, t0, "", "")
	if _, dark := s.Frame(t0, Canvas{Cols: 80, Rows: 24, Aspect: 2}); dark != 0 {
		t.Fatalf("first frame: dark %v, want 0", dark)
	}
	if _, dark := s.Frame(t0.Add(250*time.Millisecond), Canvas{Cols: 80, Rows: 24, Aspect: 2}); dark <= 0 || dark >= 1 {
		t.Fatalf("mid fade-in: dark %v", dark)
	}
	if _, dark := s.Frame(t0.Add(time.Second), Canvas{Cols: 80, Rows: 24, Aspect: 2}); dark != 1 {
		t.Fatalf("after the fade-in: dark %v, want 1", dark)
	}
}

func TestShowPauseFreezes(t *testing.T) {
	withStubParts(t, 1)
	t0, c := time.Unix(0, 0), Canvas{Cols: 80, Rows: 24, Aspect: 2}
	s := NewShow(1, t0, "", "")
	at := t0.Add(3 * time.Second)
	before, _ := s.Frame(at, c)
	s.Pause(at)
	still, _ := s.Frame(at.Add(10*time.Second), c)
	s.Resume(at.Add(10 * time.Second))
	after, _ := s.Frame(at.Add(10*time.Second), c)
	if !slices.Equal(before.Cells, still.Cells) || !slices.Equal(before.Cells, after.Cells) {
		t.Fatal("paused time moved")
	}
	moved, _ := s.Frame(at.Add(10*time.Second+500*time.Millisecond), c)
	if slices.Equal(before.Cells, moved.Cells) {
		t.Fatal("time must run again after Resume")
	}
}

// The hint comes after 4 s, not before.
func TestShowHintAfterFourSeconds(t *testing.T) {
	withStubParts(t, 1)
	t0, c := time.Unix(0, 0), Canvas{Cols: 80, Rows: 24, Aspect: 2}
	s := NewShow(1, t0, "press any key", "")
	has := func(l *Layer) bool {
		row := l.Cells[hintRow(c)*c.Cols : (hintRow(c)+1)*c.Cols]
		var b []byte
		for _, f := range row {
			if f.Ch != 0 {
				b = append(b, f.Ch)
			}
		}
		return strings.Contains(string(b), "press any key")
	}
	if l, _ := s.Frame(t0.Add(3*time.Second), c); has(l) {
		t.Fatal("hint before 4 s")
	}
	if l, _ := s.Frame(t0.Add(6*time.Second), c); !has(l) {
		t.Fatal("no hint at 6 s")
	}
}

func TestShowWithoutParts(t *testing.T) {
	withStubParts(t, 0)
	s := NewShow(1, time.Unix(0, 0), "", "")
	if l, _ := s.Frame(time.Unix(1, 0), Canvas{Cols: 10, Rows: 5, Aspect: 2}); l == nil || len(l.Cells) != 50 {
		t.Fatal("no part: an empty layer of the screen size")
	}
}
