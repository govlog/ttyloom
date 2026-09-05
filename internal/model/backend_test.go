package model

import "testing"

// Guard turns a panic into an ERROR event and hands its text to the callback;
// PostNB drops on a full channel instead of blocking.
func TestPosterGuard(t *testing.T) {
	ev := make(chan Event, 2)
	p := Poster{Events: ev}
	var got string
	func() {
		defer p.Guard("boom", func(err string) { got = err })
		panic("broken")
	}()
	if got != "broken" {
		t.Fatalf("callback: %q", got)
	}
	if e, ok := (<-ev).(EvLog); !ok || e.Level != "ERROR" {
		t.Fatalf("event: %+v", e)
	}
	Poster{Events: make(chan Event)}.PostNB(EvLog{}) // no reader: must not block
}
