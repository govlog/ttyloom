package intro

import "time"

const (
	frameMin  = time.Second / 60 // 60 frames a second at best
	frameMax  = time.Second / 20 // a frame slower than this one is late
	minWait   = 2 * time.Millisecond
	slowLimit = time.Second // late frames in a row, in total, before the show gives up
)

// Pacer sets the time between two frames of the show from what the last one
// cost (drawn, composed and written: a slow link blocks the write). Late for
// a whole second, the show gives up: a key or a message must never wait
// behind late frames.
type Pacer struct {
	slow time.Duration
}

// Next gives the wait before the next frame, or giveUp.
func (p *Pacer) Next(cost time.Duration) (wait time.Duration, giveUp bool) {
	if cost > frameMax {
		p.slow += cost
	} else {
		p.slow = 0
	}
	if p.slow >= slowLimit {
		return 0, true
	}
	return max(frameMin-cost, minWait), false
}
