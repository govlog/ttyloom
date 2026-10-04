package dsc

import (
	"testing"

	"github.com/diamondburned/arikawa/v3/discord"
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
