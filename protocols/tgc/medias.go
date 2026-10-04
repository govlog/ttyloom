package tgc

import (
	"context"

	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/model"
)

// Media browser (Ctrl+M): messages.search with the filter of the tab, one
// page at a time, newest first.

// mediaPage : messages read per page — one request.
const mediaPage = 30

// thumbPx : the largest side of a cell picture; the cells are a few hundred
// pixels wide at most.
const thumbPx = 320

func mediaFilter(f model.MediaFilter) tg.MessagesFilterClass {
	switch f {
	case model.TabGIFs:
		return &tg.InputMessagesFilterGif{}
	case model.TabFiles:
		return &tg.InputMessagesFilterDocument{}
	}
	return &tg.InputMessagesFilterPhotoVideo{}
}

// SearchMedia posts one page of the media of tab f older than before.
func (c *Client) SearchMedia(ctx context.Context, chat *model.Chat, f model.MediaFilter, before int) {
	snapshot := *chat // The UI can update chat fields while the request runs.
	chat = &snapshot
	go func() {
		ev := model.EvMedia{ChatID: chat.ID, Filter: f, Before: before}
		defer c.Guard("SearchMedia", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		if !c.floodOK() {
			ev.Err = errFlood().Error()
			c.Post(ev)
			return
		}
		q := query.Messages(c.api).Search(c.peer(chat)).Filter(mediaFilter(f)).BatchSize(mediaPage)
		if before > 0 {
			q = q.OffsetID(before)
		}
		n, oldest := 0, 0
		err := c.each(ctx, q.Iter(), func(mc tg.MessageClass, ents peer.Entities) bool {
			n, oldest = n+1, mc.GetID()
			if m, _, ok := c.convert(ctx, mc, ents, chat); ok && f.Keeps(m.Media) {
				ev.Items = append(ev.Items, model.MediaItem{Msg: m, Thumb: thumbOf(mc)})
			}
			return n < mediaPage
		})
		if err != nil {
			ev.Err = err.Error()
			ev.Items = nil
		} else if n == mediaPage {
			ev.Next = oldest // a full page: there may be more
		}
		c.Post(ev)
	}()
}

// thumbOf : a small picture of the media of mc for its cell — a size of the
// photo under thumbPx, the largest thumbnail of a document under it; nil when
// there is none.
func thumbOf(mc tg.MessageClass) *model.Media {
	msg, ok := mc.(*tg.Message)
	if !ok {
		return nil
	}
	switch v := msg.Media.(type) {
	case *tg.MessageMediaPhoto:
		if p, ok := v.Photo.(*tg.Photo); ok {
			if t := photoMedia(p, thumbPx); t.Loc != nil {
				t.Full = nil
				return t
			}
		}
	case *tg.MessageMediaDocument:
		if d, ok := v.Document.(*tg.Document); ok {
			var best *tg.PhotoSize
			for _, s := range d.Thumbs {
				if p, ok := s.(*tg.PhotoSize); ok && p.W <= thumbPx && p.H <= thumbPx && (best == nil || p.W > best.W) {
					best = p
				}
			}
			if best != nil {
				return &model.Media{Kind: model.MediaPhoto, W: best.W, H: best.H, Size: int64(best.Size), Ext: ".jpg",
					Mime: "image/jpeg", Loc: d.AsInputDocumentFileLocation(best.Type), Label: "[photo]"}
			}
		}
	}
	return nil
}
