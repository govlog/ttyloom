package dsc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
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

// A search on an index still being built answers HTTP 202 with no message:
// the delay it gives is waited once and the search asked again; an index
// still not ready then is an error, never an empty "no result".
func TestSearchIndexNotReady(t *testing.T) {
	var ready atomic.Bool
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if !ready.Load() || asked.Load()%2 == 1 {
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"message":"Index not yet available. Try again later","code":110000,"documents_indexed":0,"retry_after":0.01}`))
			return
		}
		w.Write([]byte(`{"total_results":1,"messages":[[{"id":"4194304000","channel_id":"5","content":"cat","hit":true}]]}`))
	}))
	defer srv.Close()
	ev := make(chan model.Event, 4)
	c := testClient(ev)
	useTestAPI(t, c, srv)
	chat := &model.Chat{ID: 5, Peer: peer{Channel: 5}}

	ready.Store(true) // built while the client waits
	c.Search(context.Background(), chat, "cat", 50)
	if e := next(t, ev).(model.EvSearch); e.Err != "" || len(e.Msgs) != 1 || asked.Load() != 2 {
		t.Fatalf("search after the wait: %+v, %d requests", e, asked.Load())
	}
	ready.Store(false)
	c.Search(context.Background(), chat, "cat", 50)
	if e := next(t, ev).(model.EvSearch); e.Err != i18n.T("dsc_search_indexing") || asked.Load() != 4 {
		t.Fatalf("index never ready: %+v, %d requests; want the error after one retry", e, asked.Load())
	}
}

// A global search asks the ten guilds where something was said last, one at
// a time with a pause between two: one search per guild of the account at
// each query, back to back, looks like a self-bot.
func TestSearchGlobalGuildsCapped(t *testing.T) {
	old := globalPace
	globalPace = 20 * time.Millisecond
	t.Cleanup(func() { globalPace = old })
	var mu sync.Mutex
	var guilds []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g, ok := strings.CutPrefix(r.URL.Path, "/api/v9/guilds/"); ok {
			mu.Lock()
			guilds = append(guilds, strings.TrimSuffix(g, "/messages/search"))
			mu.Unlock()
			w.Write([]byte(`{"messages":[]}`))
			return
		}
		w.Write([]byte(`[]`)) // no DM
	}))
	defer srv.Close()
	ev := make(chan model.Event, 4)
	c := testClient(ev)
	useTestAPI(t, c, srv)
	for i := range 15 {
		g := discord.GuildID(100 + i)
		if err := c.state().Cabinet.GuildSet(&discord.Guild{ID: g, Name: fmt.Sprint(g)}, false); err != nil {
			t.Fatal(err)
		}
		ch := &discord.Channel{ID: discord.ChannelID(1000 + i), GuildID: g, Type: discord.GuildText, LastMessageID: msgID(int64(1 + i))}
		if err := c.state().Cabinet.ChannelSet(ch, false); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	c.SearchGlobal(context.Background(), "cat", 50)
	if e := next(t, ev).(model.EvSearchGlobal); e.Err != "" {
		t.Fatalf("search: %+v", e)
	}
	want := "[114 113 112 111 110 109 108 107 106 105]"
	if got := fmt.Sprint(guilds); got != want {
		t.Fatalf("guilds searched %s, want the ten most recent %s", got, want)
	}
	if d := time.Since(start); d < 9*globalPace {
		t.Fatalf("ten searches in %v: not paced", d)
	}
}
