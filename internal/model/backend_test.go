package model

import (
	"context"
	"testing"
	"time"
)

func TestPosterStopsWithSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := Poster{Events: make(chan Event)}
	p.SetContext(ctx)
	done := make(chan struct{})
	go func() { p.Post(EvLog{}); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event delivery blocked after session ended")
	}
}

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

func TestIRCNetName(t *testing.T) {
	if IRCNet("libera") != "irc:libera" || IRCName("irc:libera") != "libera" {
		t.Fatal("irc:libera round trip")
	}
	if IRCName("irc") != "" || IRCName("irc:") != "" || IRCName("discord") != "" {
		t.Fatal("not an IRC network key")
	}
}
