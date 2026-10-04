package dsc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/diamondburned/arikawa/v3/discord"

	"github.com/govlog/ttyloom/internal/model"
)

// The cell picture of an image attachment is the proxy URL asked at most
// 320 px on its longest side, its query kept; anything else has none.
func TestThumbOf(t *testing.T) {
	img := discord.Attachment{ContentType: "image/png; charset=binary", Width: 1280, Height: 640,
		Proxy: "https://media.discordapp.net/attachments/1/2/a.png?ex=1&hm=2"}
	md := thumbOf(&discord.Message{Attachments: []discord.Attachment{img}})
	if md == nil || md.W != 320 || md.H != 160 || md.Mime != "image/png" {
		t.Fatalf("thumb: %+v", md)
	}
	if got := string(md.Loc.(fileURL)); got != "https://media.discordapp.net/attachments/1/2/a.png?ex=1&hm=2&width=320&height=160" {
		t.Errorf("thumb URL %s", got)
	}
	video := discord.Attachment{ContentType: "video/mp4", Width: 640, Height: 480, Proxy: "https://media.discordapp.net/v.mp4"}
	for _, m := range []discord.Message{{}, {Attachments: []discord.Attachment{video}}} {
		if md := thumbOf(&m); md != nil {
			t.Errorf("a picture for %+v", m.Attachments)
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
