package irc

import (
	"context"
	"slices"
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
// it; ctx (the network: a logout, a disconnect) cuts the wait. A ctx that
// ended while the line waited in the queue takes no token: the line is
// dropped. Never called from a callback.
func (c *Client) pace(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

// queue puts one send of the user (all its lines) at the end of the output
// queue and comes back at once. The worker of Run runs the jobs one at a
// time, in the order of the calls: a message typed during a paste goes out
// after the whole paste. A job paces its lines and posts its own answer.
//
// ponytail: no cap on the queue. The UI bounds each job (a paste is 64 KB at
// most, and only after a prompt) and the rate (a person types; a hook sends
// hookMaxSends a minute). Add a cap that fails the job with its error if a
// program ever calls Send in a loop.
func (c *Client) queue(job func()) {
	c.mu.Lock()
	done := c.outDone
	if !done {
		c.outQ = append(c.outQ, job)
	}
	c.mu.Unlock()
	if done {
		// Run has ended: no line can go out. The job runs on its own and posts
		// its error, as each send did before the queue.
		go job()
		return
	}
	select {
	case c.wake <- struct{}{}:
	default: // a wake is pending already
	}
}

// work runs the jobs of the output queue, oldest first, until ctx ends (Run
// returns). The jobs still in the queue then run too: the connection is
// down, so each one only posts its error. After that, queue runs each new
// job on its own goroutine.
func (c *Client) work(ctx context.Context) {
	for {
		c.mu.Lock()
		end := ctx.Err() != nil
		var job func()
		if len(c.outQ) > 0 {
			job = c.outQ[0]
			c.outQ = slices.Delete(c.outQ, 0, 1) // clears the old slot: the text of the job can go
		} else if end {
			c.outDone = true
		}
		c.mu.Unlock()
		switch {
		case job != nil:
			job()
		case end:
			return
		default:
			select {
			case <-c.wake:
			case <-ctx.Done():
			}
		}
	}
}
