package dsc

import (
	"encoding/json"
	"testing"
)

// The search answers groups of messages around each hit, the hit flagged;
// hitsOf keeps one message per group — the flagged one, else the first — and
// drops what does not decode.
func TestHitsOf(t *testing.T) {
	groups := [][]json.RawMessage{
		{json.RawMessage(`{"id":"1","channel_id":"5","content":"before"}`),
			json.RawMessage(`{"id":"2","channel_id":"5","content":"cat","hit":true}`)},
		{json.RawMessage(`{"id":"3","channel_id":"5","content":"cat too"}`)},
		{json.RawMessage(`not json`)},
	}
	ms := hitsOf(groups)
	if len(ms) != 2 || ms[0].ID != 2 || ms[1].ID != 3 {
		t.Fatalf("hits: %+v", ms)
	}
}
