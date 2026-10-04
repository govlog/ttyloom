package dsc

import (
	"context"
	"fmt"
	"strings"

	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/discord"

	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	rend "github.com/govlog/ttyloom/internal/render"
)

// Media browser (Ctrl+M): the search route with the "has" closest to the
// tab, one page at a time, newest first; the tab keeps its own kinds.

// searchPage : hits in a page of the search route, a size Discord fixes.
const searchPage = 25

// thumbPx : the largest side of a cell picture.
const thumbPx = 320

// mediaHas : a GIF is a Tenor or KLIPY link unfurled (an embed); photos,
// videos and files are attachments, told apart by their type.
func mediaHas(f model.MediaFilter) string {
	if f == model.TabGIFs {
		return "embed"
	}
	return "file"
}

// SearchMedia posts one page of the media of tab f older than before.
func (c *Client) SearchMedia(ctx context.Context, chat *model.Chat, f model.MediaFilter, before int) {
	go func() {
		ev := model.EvMedia{ChatID: chat.ID, Filter: f, Before: before}
		defer c.Guard("SearchMedia", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		chID, guild := ids(chat)
		data := api.SearchData{ChannelID: chID, Has: mediaHas(f)}
		if before > 0 {
			data.MaxID = discord.MessageID(before)
		}
		ms, err := c.search(ctx, guild, data)
		if err != nil {
			ev.Err = err.Error()
			c.Post(ev)
			return
		}
		for i := range ms {
			if m := c.msgOf(&ms[i]); f.Keeps(m.Media) {
				ev.Items = append(ev.Items, model.MediaItem{Msg: m, Thumb: thumbOf(&ms[i])})
			}
		}
		if len(ms) == searchPage { // a full page: there may be more
			ev.Next = int(ms[len(ms)-1].ID)
		}
		c.Post(ev)
	}()
}

// thumbOf : an image attachment resized for its cell by the media proxy of
// Discord; nil for anything else.
func thumbOf(m *discord.Message) *model.Media {
	if len(m.Attachments) == 0 {
		return nil
	}
	a := m.Attachments[0]
	mime, _, _ := strings.Cut(a.ContentType, ";")
	if !strings.HasPrefix(mime, "image/") || a.Width == 0 || a.Height == 0 || !rend.SafeURL(string(a.Proxy)) {
		return nil
	}
	w, h := int(a.Width), int(a.Height)
	if s := max(w, h); s > thumbPx {
		w, h = max(1, w*thumbPx/s), max(1, h*thumbPx/s)
	}
	sep := "?"
	if strings.Contains(string(a.Proxy), "?") {
		sep = "&"
	}
	return &model.Media{Kind: model.MediaPhoto, W: w, H: h, Mime: mime, Ext: media.Extension(mime),
		Loc: fileURL(fmt.Sprintf("%s%swidth=%d&height=%d", a.Proxy, sep, w, h)), Label: "[photo]"}
}
