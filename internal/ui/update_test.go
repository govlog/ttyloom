package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/update"
)

// TestUpdateCheck : off asks nothing; /set update_check on asks the API at
// once and window 0 names the newer release.
func TestUpdateCheck(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		switch r.URL.Path {
		case "/repos/govlog/ttyloom/releases":
			w.Write([]byte(`[{"tag_name": "v0.12-beta", "published_at": "2026-10-10T00:00:00Z"}]`))
		case "/repos/govlog/ttyloom/commits/v0.12-beta":
			w.Write([]byte("2222222222222222222222222222222222222222"))
		}
	}))
	defer srv.Close()
	old := updateAPI
	updateAPI = srv.URL
	t.Cleanup(func() { updateAPI = old })

	u := &UI{ctx: context.Background(), ws: NewWindows(), agg: &Window{}, cfg: &config.Config{}, t: &term.Term{Cols: 80, Rows: 24},
		events: make(chan model.Event, 4), build: update.Build{Version: "v0.11-beta", Commit: "1111111111111111111111111111111111111111"}}
	u.setCmd([]string{"update_check", "off"})
	u.setCmd([]string{"update_check", "on"})
	select {
	case ev := <-u.events:
		u.event(ev)
	case <-time.After(10 * time.Second):
		t.Fatal("no answer from the update check")
	}
	if got := lastSys(u.ws.List[0]); !strings.Contains(got, "v0.12-beta") || !strings.Contains(got, "22222222") || !strings.Contains(got, "11111111") {
		t.Errorf("window 0 says %q, want the new release, its build and this build", got)
	}
	if n := asked.Load(); n != 2 {
		t.Errorf("%d requests, want 2 (none while off)", n)
	}
}
