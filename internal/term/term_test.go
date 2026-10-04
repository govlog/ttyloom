package term

import (
	"strings"
	"testing"
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
