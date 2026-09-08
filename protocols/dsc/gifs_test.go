package dsc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/govlog/ttyloom/internal/model"
)

// TestGifsOfTenor : the JSON of /gifs/search becomes previewable GIFs whose
// send handle is the page URL; an entry with no safe URL or no animation is
// dropped, and the object form of /gifs/trending is read too.
func TestGifsOfTenor(t *testing.T) {
	body := `[{"id":"1","title":"cat","url":"https://tenor.com/view/cat-1","src":"https://media.tenor.com/a.mp4","gif_src":"https://media.tenor.com/a.gif","width":498,"height":280},
	 {"id":"2","url":"javascript:alert(1)","gif_src":"https://media.tenor.com/b.gif","width":1,"height":1},
	 {"id":"3","url":"https://tenor.com/view/3","gif_src":"","src":"","width":1,"height":1}]`
	gs, err := gifsOf([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 1 {
		t.Fatalf("gifs: %+v", gs)
	}
	g := gs[0]
	// The mp4 clip first: smaller, and ffmpeg reads it everywhere — the
	// "gif" of Tenor is an animated WebP the Go decoder cannot open.
	if g.Preview.Kind != model.MediaGIF || g.Preview.W != 498 || g.Preview.H != 280 || !g.Preview.Previewable() ||
		g.Preview.Loc != fileURL("https://media.tenor.com/a.mp4") || g.Preview.Ext != ".mp4" || g.Preview.Mime != "video/mp4" {
		t.Fatalf("preview: %+v", g.Preview)
	}
	if g.Send != "https://tenor.com/view/cat-1" {
		t.Fatalf("send handle: %#v", g.Send)
	}
	gs, err = gifsOf([]byte(`{"categories":[],"gifs":[{"url":"https://tenor.com/view/t","gif_src":"https://media.tenor.com/t.gif","width":2,"height":2}]}`))
	if err != nil || len(gs) != 1 || gs[0].Preview.Mime != "image/gif" || gs[0].Preview.Ext != ".gif" {
		t.Fatalf("trending object, gif fallback: %+v %v", gs, err)
	}
}

// A handle of another network is refused at once, never sent.
func TestSendGifForeign(t *testing.T) {
	e := evOf(t, func(c *Client) { c.SendGif(context.Background(), &model.Chat{ID: 5}, model.Gif{Send: 42}, 9) })
	if s, ok := e.(model.EvSent); !ok || s.Err == "" || s.TmpID != 9 || s.ChatID != 5 {
		t.Fatalf("event: %#v", e)
	}
}

// The search routes are the ones of the official client; an empty query asks
// for the trending ones and the query is escaped.
func TestGifURL(t *testing.T) {
	if u := gifURL(""); u != "https://discord.com/api/v9/gifs/trending-gifs?media_format=mp4" {
		t.Fatalf("trending: %s", u)
	}
	if u := gifURL("a b&c"); u != "https://discord.com/api/v9/gifs/search?media_format=mp4&q=a+b%26c" {
		t.Fatalf("search: %s", u)
	}
}

// Regression: Discord returns KLIPY URLs even for provider=tenor. The old
// CDN allowlist left every preview on "discord: media URL refused".
func TestKlipyPreviewDownload(t *testing.T) {
	gs, err := gifsOf([]byte(`[{"url":"https://klipy.com/gifs/good-night-peanuts",
	 "src":"https://static.klipy.com/ii/sample/preview.mp4","gif_src":"https://static.klipy.com/ii/sample/preview.webp",
	 "width":640,"height":442}]`))
	if err != nil || len(gs) != 1 {
		t.Fatalf("GIF response: %v %v", gs, err)
	}
	if gs[0].Preview.Mime != "video/mp4" || gs[0].Send != "https://klipy.com/gifs/good-night-peanuts" {
		t.Fatalf("GIF metadata: %+v", gs[0])
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("token sent to GIF CDN")
		}
		if r.URL.Path == "/ii/sample/preview.mp4" {
			http.Redirect(w, r, "https://static.klipy.com/ii/sample/final.mp4", http.StatusFound)
			return
		}
		w.Write([]byte("preview bytes"))
	}))
	defer srv.Close()
	useTestCDN(t, srv.URL)
	path := filepath.Join(t.TempDir(), "preview.mp4")
	if err := fetch(context.Background(), string(gs[0].Preview.Loc.(fileURL)), path, 100); err != nil {
		t.Fatalf("KLIPY preview (with redirect) refused: %v", err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "preview bytes" {
		t.Fatalf("preview download: %q %v", b, err)
	}
	for _, raw := range []string{
		"http://static.klipy.com/x", "https://static.klipy.com.evil.test/x", "https://evil-static.klipy.com/x",
		"https://static.klipy.com:8443/x", "https://user@static.klipy.com/x", "https://127.0.0.1/x",
	} {
		r, err := http.NewRequest("GET", raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		if mediaRequestAllowed(r) {
			t.Errorf("unexpected media destination allowed: %s", raw)
		}
	}
}
