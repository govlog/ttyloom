package term

import (
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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
