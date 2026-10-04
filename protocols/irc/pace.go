package irc

import (
	"context"
	"sync"
	"time"
)

// Output pacing: what the user sends (a pasted text, a run of commands, DCC
// offers) goes out at outBurst lines, then one every outFill — under the
// "Excess Flood" limit of the servers. The callbacks of the read goroutine
// never wait on it: their lines go straight to send.
const outBurst = 4

var outFill = time.Second // a variable: the pacing test shortens it

// pacer : a token bucket, burst tokens at rest and one more every fill.
type pacer struct {
	mu     sync.Mutex
	burst  float64
	fill   time.Duration
	tokens float64
	last   time.Time
}

func newPacer(burst int, fill time.Duration) *pacer {
	return &pacer{burst: float64(burst), fill: fill, tokens: float64(burst), last: time.Now()}
}

// refill credits the time gone by; mu held.
func (p *pacer) refill() {
	now := time.Now()
	p.tokens = min(p.burst, p.tokens+float64(now.Sub(p.last))/float64(p.fill))
	p.last = now
}

// take reserves a token and gives the time to wait for it. The count may go
// below zero — tokens owed — so the callers queue in the order they came.
func (p *pacer) take() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refill()
	p.tokens--
	if p.tokens >= 0 {
		return 0
	}
	return time.Duration(-p.tokens * float64(p.fill))
}

// allow takes a token when one is there, false otherwise; never waits.
func (p *pacer) allow() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refill()
	if p.tokens < 1 {
		return false
	}
	p.tokens--
	return true
}

// pace holds a user-initiated line until the output bucket has a token for
// it; ctx (the network: a logout, a disconnect) cuts the wait. Never called
// from a callback.
func (c *Client) pace(ctx context.Context) error {
	d := c.out.take()
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
