package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const (
	sha10 = "1111111111111111111111111111111111111111"
	sha9  = "9999999999999999999999999999999999999999"
)

// github : a fake API with v0.10-beta published on 2026-10-01, a newer draft
// and an older release listed after it.
func github(t *testing.T, sha string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/govlog/ttyloom/releases", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"tag_name": "v0.11-beta", "draft": true, "published_at": null},
			{"tag_name": "v0.9-beta", "published_at": "2026-09-24T08:12:00Z"},
			{"tag_name": "v0.10-beta", "published_at": "2026-10-01T10:00:00Z"},
			{"tag_name": "v0.12-beta\u001b[2J", "published_at": "2026-10-02T10:00:00Z"}
		]`))
	})
	mux.HandleFunc("GET /repos/govlog/ttyloom/commits/v0.10-beta", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github.sha" {
			http.Error(w, "json", http.StatusUnsupportedMediaType)
			return
		}
		w.Write([]byte(sha))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestCheck(t *testing.T) {
	api := github(t, sha10)
	at := func(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }
	for _, tc := range []struct {
		name  string
		build Build
		newer bool
	}{
		{"older release build", Build{Version: "v0.9-beta", Commit: sha9}, true},
		{"same release build", Build{Version: "v0.10-beta", Commit: sha10}, false},
		{"release build ahead of the list", Build{Version: "v0.11-beta", Commit: sha9}, false},
		{"local build made before the release", Build{Version: "dev", Commit: sha9, Time: at("2026-09-30T00:00:00Z")}, true},
		{"local build made after the release", Build{Version: "dev", Commit: sha9, Time: at("2026-10-03T00:00:00Z")}, false},
		{"local build of the release commit", Build{Version: "dev", Commit: sha10, Time: at("2026-09-30T00:00:00Z")}, false},
		{"build with no stamp", Build{Version: "dev"}, false},
	} {
		rel, newer, err := Check(context.Background(), http.DefaultClient, api, tc.build)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if newer != tc.newer {
			t.Errorf("%s: newer = %v, want %v", tc.name, newer, tc.newer)
		}
		if rel.Tag != "v0.10-beta" || rel.ID() != "11111111" || rel.URL != "https://github.com/govlog/ttyloom/releases/tag/v0.10-beta" {
			t.Errorf("%s: release = %+v, want the newest published v0.10-beta", tc.name, rel)
		}
	}
}

func TestCheckRefusesABadAnswer(t *testing.T) {
	if _, _, err := Check(context.Background(), http.DefaultClient, github(t, "<html>"), Build{Version: "v0.9-beta"}); err == nil {
		t.Error("a commit answer that is not a hash must fail the check")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()
	if _, _, err := Check(context.Background(), http.DefaultClient, srv.URL, Build{Version: "v0.9-beta"}); err == nil || err.Error() != "GitHub: HTTP 403" {
		t.Errorf("err = %v, want GitHub: HTTP 403", err)
	}
}

func TestBuildID(t *testing.T) {
	if id := (Build{Commit: "0cd0f72aa0cd0f72aa0cd0f72aa0cd0f72aa0cd0"}).ID(); id != "0cd0f72a" {
		t.Errorf("ID = %q, want the first 8 hex digits", id)
	}
	if b := Current("v0.11-beta", "not-a-hash"); b.Commit != "" && len(b.Commit) != 40 {
		t.Errorf("Current kept a commit that is not a hash: %q", b.Commit)
	}
}
