package term

import (
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/govlog/ttyloom/internal/theme"
)

// Everything written between two flushes goes out as one synchronized update:
// the terminal paints the frame whole or not at all.
func TestFlushIsOneSynchronizedUpdate(t *testing.T) {
	var b strings.Builder
	tm := NewOffscreen(&b, 80, 24)
	tm.WriteString("\x1b[1;1Hone")
	tm.WriteString("\x1b[2;1Htwo")
	tm.Flush()
	tm.Flush() // nothing queued: nothing written
	if got := b.String(); got != "\x1b[?2026h\x1b[1;1Hone\x1b[2;1Htwo\x1b[?2026l" {
		t.Fatalf("frame %q", got)
	}
}

// While another program has the terminal, the keys typed are its own and
// nothing of the interface reaches the screen; back gives both back.
func TestHandStopsReadingAndWriting(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var b strings.Builder
	tm := NewOffscreen(&b, 80, 24)
	tm.in, tm.parked, tm.Kitty = r, make(chan chan struct{}), true
	if err := unix.Pipe2(tm.wake[:], unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	go tm.readLoop()

	back := tm.Hand()
	w.WriteString("q") // a key for the player
	tm.WriteString("frame")
	tm.Flush()
	select {
	case k := <-tm.Keys():
		t.Fatalf("key %q read while the terminal was handed", k.Rune)
	case <-time.After(100 * time.Millisecond):
	}
	if strings.Contains(b.String(), "frame") {
		t.Fatalf("write reached the screen while the terminal was handed: %q", b.String())
	}

	back()
	select {
	case k := <-tm.Keys():
		if k.Rune != 'q' {
			t.Fatalf("key after back = %q, want q", k.Rune)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no key read after back")
	}
	if !strings.HasSuffix(b.String(), "\x1b[?1049h"+modesOn+"\x1b[2J\x1b_Ga=d,d=A,q=2\x1b\\\x1b[?2026l") {
		t.Fatalf("back did not restore the modes and wipe the screen: %q", b.String())
	}
	w.Close() // end of input: the loop closes the keys and is over before r closes
	for range tm.Keys() {
	}
}

// The colours the terminal gives back to OSC 10, 11 and 4 are read with 1 to
// 4 hex digits per channel and either end (ST or BEL); a colour with no answer
// stays unknown.
func TestParsePalette(t *testing.T) {
	resp := []byte("\x1b]10;rgb:cdcd/d6d6/f4f4\x1b\\" + "\x1b]11;rgb:1e/1e/2e\x07" +
		"\x1b]4;1;rgb:f3f3/8b8b/a8a8\x1b\\" + "\x1b]4;12;rgb:8/9/f\x1b\\" + "\x1b[?62;4c")
	p := parsePalette(resp)
	if !p.HasFG || p.FG != (theme.RGB{R: 0xcd, G: 0xd6, B: 0xf4}) {
		t.Fatalf("fg: %+v", p)
	}
	if !p.HasBG || p.BG != (theme.RGB{R: 0x1e, G: 0x1e, B: 0x2e}) {
		t.Fatalf("bg (BEL end, 2 digits): %+v", p)
	}
	if !p.HasANSI[1] || p.ANSI[1] != (theme.RGB{R: 0xf3, G: 0x8b, B: 0xa8}) {
		t.Fatalf("ansi 1: %+v", p)
	}
	if !p.HasANSI[12] || p.ANSI[12] != (theme.RGB{R: 0x88, G: 0x99, B: 0xff}) {
		t.Fatalf("ansi 12 (1 digit): %+v", p)
	}
	if p.HasANSI[0] {
		t.Fatal("ansi 0 never answered")
	}
	if q := parsePalette([]byte("\x1b]4;3;rgb:8000/8000/8000\x1b\\")); q.ANSI[3] != (theme.RGB{R: 128, G: 128, B: 128}) {
		t.Fatalf("8000 (127.5) rounds to 128: %+v", q.ANSI[3])
	}
	if q := parsePalette([]byte("\x1b[?62;4c")); q.HasFG || q.HasBG {
		t.Fatal("silent terminal: unknown")
	}
}
