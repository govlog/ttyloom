package dsc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/diamondburned/arikawa/v3/discord"

	"github.com/govlog/ttyloom/internal/model"
)

// The cell picture of an image attachment is the proxy URL asked at most
// 320 px on its longest side, its query kept; anything else has none.
func TestThumbOf(t *testing.T) {
	img := discord.Attachment{ContentType: "image/png; charset=binary", Width: 1280, Height: 640,
		Proxy: "https://media.discordapp.net/attachments/1/2/a.png?ex=1&hm=2"}
	md := thumbOf(img)
	if md == nil || md.W != 320 || md.H != 160 || md.Mime != "image/png" {
		t.Fatalf("thumb: %+v", md)
	}
	if got := string(md.Loc.(fileURL)); got != "https://media.discordapp.net/attachments/1/2/a.png?ex=1&hm=2&width=320&height=160" {
		t.Errorf("thumb URL %s", got)
	}
	video := discord.Attachment{ContentType: "video/mp4", Width: 640, Height: 480, Proxy: "https://media.discordapp.net/v.mp4"}
	for _, a := range []discord.Attachment{{}, video} {
		if md := thumbOf(a); md != nil {
			t.Errorf("a picture for %+v", a)
		}
	}
}

// A GIF upload plays (MediaGIF) but stays with the pictures of the Media tab:
// the GIFs tab asks for the embeds of the providers, and would never find it.
func TestSearchMediaKeepsGIFUploads(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"messages":[[{"id":"4194304000","channel_id":"5","hit":true,"attachments":[{"id":"1",
			"filename":"cat.gif","content_type":"image/gif","size":2048,"width":200,"height":100,
			"url":"https://cdn.discordapp.com/attachments/5/1/cat.gif","proxy_url":"https://media.discordapp.net/attachments/5/1/cat.gif"}]}]]}`))
	}))
	defer srv.Close()
	ev := make(chan model.Event, 4)
	c := testClient(ev)
	useTestAPI(t, c, srv)
	c.SearchMedia(context.Background(), &model.Chat{ID: 5, Peer: peer{Channel: 5}}, model.TabMedia, 0)
	if e := next(t, ev).(model.EvMedia); e.Err != "" || len(e.Items) != 1 || e.Items[0].Msg.Media.Kind != model.MediaGIF {
		t.Fatalf("media tab: %+v, want the GIF upload", e)
	}
}

// Several photos sent at once are one message, but each takes a cell of its
// own: its own media, its own small picture.
func TestSearchMediaCellPerAttachment(t *testing.T) {
	names := []string{"a.jpg", "b.jpg", "c.jpg"}
	var as []string
	for i, n := range names {
		as = append(as, fmt.Sprintf(`{"id":"%d","filename":"%s","content_type":"image/jpeg","size":2048,"width":200,"height":100,`+
			`"url":"https://cdn.discordapp.com/attachments/5/%d/%s","proxy_url":"https://media.discordapp.net/attachments/5/%d/%s"}`,
			i+1, n, i+1, n, i+1, n))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"messages":[[{"id":"4194304000","channel_id":"5","hit":true,"attachments":[` + strings.Join(as, ",") + `]}]]}`))
	}))
	defer srv.Close()
	ev := make(chan model.Event, 4)
	c := testClient(ev)
	useTestAPI(t, c, srv)
	c.SearchMedia(context.Background(), &model.Chat{ID: 5, Peer: peer{Channel: 5}}, model.TabMedia, 0)
	e := next(t, ev).(model.EvMedia)
	if e.Err != "" || len(e.Items) != len(names) {
		t.Fatalf("media tab: %d cells (err %q), want one per attachment (%d)", len(e.Items), e.Err, len(names))
	}
	for i, n := range names {
		it := e.Items[i]
		if it.Msg.Media == nil || it.Msg.Media.Name != n || it.Thumb == nil || !strings.Contains(string(it.Thumb.Loc.(fileURL)), "/"+n) {
			t.Fatalf("cell %d: media %+v, thumb %+v; want %s", i, it.Msg.Media, it.Thumb, n)
		}
	}
}
