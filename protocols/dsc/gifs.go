package dsc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"

	"github.com/diamondburned/arikawa/v3/api"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	rend "github.com/govlog/ttyloom/internal/render"
)

// GIF search through the routes of the official client (currently KLIPY),
// with the token of the account already in place. A GIF is sent the way the
// client sends it: a message whose content is the page URL, which Discord
// unfurls into the animation at the other end.

// gifURL : search route, or the trending one on an empty query.
func gifURL(q string) string {
	if q == "" {
		return api.Endpoint + "gifs/trending-gifs?media_format=mp4"
	}
	return api.Endpoint + "gifs/search?media_format=mp4&q=" + url.QueryEscape(q)
}

// discordGif : one entry of the answer, independent of Discord's provider.
type discordGif struct {
	URL    string `json:"url"`     // the page: what is sent
	Src    string `json:"src"`     // mp4 clip
	GifSrc string `json:"gif_src"` // "gif": an animated WebP in practice
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// gifsOf reads the answer of a search (a list) or of the trending route (an
// object with a "gifs" list). An entry whose page or animation is not an
// http(s) URL is dropped: both go out as they are, to the network and to the
// terminal.
func gifsOf(body []byte) ([]model.Gif, error) {
	var list []discordGif
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, err
		}
	} else {
		var obj struct {
			Gifs []discordGif `json:"gifs"`
		}
		if err := json.Unmarshal(body, &obj); err != nil {
			return nil, err
		}
		list = obj.Gifs
	}
	var out []model.Gif
	for _, g := range list {
		if !rend.SafeURL(g.URL) {
			continue
		}
		// The mp4 clip first: smaller, and ffmpeg reads it everywhere — the
		// provider's "gif" can be an animated WebP the Go decoder cannot open.
		src, mime, ext := g.Src, "video/mp4", ".mp4"
		if src == "" {
			src, mime, ext = g.GifSrc, "image/gif", ".gif"
		}
		if !rend.SafeURL(src) {
			continue
		}
		md := &model.Media{Kind: model.MediaGIF, W: g.Width, H: g.Height, Mime: mime, Ext: ext,
			Loc: fileURL(src), Label: "[gif]"}
		out = append(out, model.Gif{Preview: md, Send: g.URL})
	}
	return out, nil
}

// SearchGifs asks Discord's GIF provider for q (empty: the trending ones).
func (c *Client) SearchGifs(ctx context.Context, _ *model.Chat, q string) {
	go func() {
		ev := model.EvGifs{Query: q}
		defer c.Guard("SearchGifs", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		var raw json.RawMessage
		if err := c.rest(ctx).RequestJSON(&raw, "GET", gifURL(q)); err != nil {
			ev.Err = err.Error()
			c.Post(ev)
			return
		}
		gifs, err := gifsOf(raw)
		if err != nil {
			ev.Err = err.Error()
		}
		ev.Gifs = gifs
		c.Post(ev)
	}()
}

// SendGif posts the page URL of the result: Discord shows the animation.
func (c *Client) SendGif(ctx context.Context, chat *model.Chat, g model.Gif, tmpID int64) {
	page, ok := g.Send.(string)
	if !ok || !rend.SafeURL(page) { // handle of another network: nothing to post here
		c.refuse("SendGif", model.EvSent{ChatID: chat.ID, TmpID: tmpID, Err: i18n.T("media_foreign")})
		return
	}
	c.send(ctx, "SendGif", chat, tmpID, api.SendMessageData{Content: page})
}
