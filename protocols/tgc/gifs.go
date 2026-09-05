package tgc

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"errors"

	"github.com/gotd/td/telegram/message/unpack"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// GIF search and send through the inline bot @gif — the results the official
// clients show under their GIF button, no third-party key. The GIF goes out
// as an inline result of that query ("via @gif" at the other end).

// gifBot : username of the inline bot, resolved once per session.
const gifBot = "gif"

// inlineRef : model.Gif.Send — the result to post, valid with the query that
// gave it.
type inlineRef struct {
	QueryID int64
	ID      string
}

// gifBotUser resolves @gif the first time and keeps it: every search would
// otherwise cost a contacts.resolveUsername on top of the query.
func (c *Client) gifBotUser(ctx context.Context) (tg.InputUserClass, error) {
	c.mu.Lock()
	u := c.gifUser
	c.mu.Unlock()
	if u != nil {
		return u, nil
	}
	p, err := c.peers.Resolve(ctx, gifBot)
	if err != nil {
		return nil, err
	}
	usr, ok := p.(peers.User)
	if !ok {
		return nil, errors.New(i18n.T("gif_bot_missing"))
	}
	u = usr.InputUser()
	c.mu.Lock()
	c.gifUser = u
	c.mu.Unlock()
	return u, nil
}

// SearchGifs asks @gif for q in chat (q empty: the trending ones).
func (c *Client) SearchGifs(ctx context.Context, chat *model.Chat, q string) {
	go func() {
		ev := model.EvGifs{Query: q}
		defer c.Guard("SearchGifs", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		if !c.floodOK() {
			ev.Err = errFlood().Error()
			c.Post(ev)
			return
		}
		bot, err := c.gifBotUser(ctx)
		if err == nil {
			var res *tg.MessagesBotResults
			res, err = c.api.MessagesGetInlineBotResults(ctx, &tg.MessagesGetInlineBotResultsRequest{
				Bot: bot, Peer: c.peer(chat), Query: q})
			if err == nil {
				ev.Gifs = gifsOf(res)
			}
		}
		if err != nil {
			c.floodTrip(err)
			ev.Err = err.Error()
		}
		c.Post(ev)
	}()
}

// gifsOf keeps the results that carry a document: @gif answers mp4 clips
// flagged animated, and a clip without the flag is a GIF all the same.
func gifsOf(res *tg.MessagesBotResults) []model.Gif {
	var out []model.Gif
	for _, r := range res.Results {
		m, ok := r.(*tg.BotInlineMediaResult)
		if !ok {
			continue
		}
		d, ok := m.Document.(*tg.Document)
		if !ok {
			continue
		}
		md := docMedia(d)
		if md.Kind != model.MediaGIF && md.Kind != model.MediaVideo {
			continue
		}
		md.Kind = model.MediaGIF
		out = append(out, model.Gif{Preview: md, Send: inlineRef{QueryID: res.QueryID, ID: m.ID}})
	}
	return out
}

// SendGif posts an inline result in chat. EvSent like a text send: the UI
// unpends its line, and the echo comes back through the updates of the RPC.
func (c *Client) SendGif(ctx context.Context, chat *model.Chat, g model.Gif, tmpID int64) {
	go func() {
		defer c.Guard("SendGif", func(err string) { c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID, Err: err}) })
		ev := model.EvSent{ChatID: chat.ID, TmpID: tmpID}
		ref, ok := g.Send.(inlineRef)
		if !ok { // handle of another network: nothing to post here
			ev.Err = i18n.T("media_foreign")
			c.Post(ev)
			return
		}
		id, err := unpack.MessageID(c.api.MessagesSendInlineBotResult(ctx, &tg.MessagesSendInlineBotResultRequest{
			Peer: c.peer(chat), RandomID: randomID(), QueryID: ref.QueryID, ID: ref.ID}))
		ev.ID = id
		if err != nil {
			ev.Err = err.Error()
		}
		c.Post(ev)
	}()
}

// randomID : the random_id of a send, from the system generator like the
// sender of gotd — a repeat would make the server drop the message as a
// duplicate.
func randomID() int64 {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		panic(err) // the system generator never fails on Linux
	}
	return int64(binary.LittleEndian.Uint64(b[:]))
}
