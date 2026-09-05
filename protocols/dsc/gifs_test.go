package dsc

import (
	"context"
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
	if u := gifURL(""); u != "https://discord.com/api/v9/gifs/trending-gifs?provider=tenor&media_format=gif" {
		t.Fatalf("trending: %s", u)
	}
	if u := gifURL("a b&c"); u != "https://discord.com/api/v9/gifs/search?provider=tenor&media_format=gif&q=a+b%26c" {
		t.Fatalf("search: %s", u)
	}
}
