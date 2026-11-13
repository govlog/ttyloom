package tgc

import (
	"context"
	"fmt"
	"time"

	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// Stickers tab of the GIF box. The recents, the favorites and the list of
// the installed packs come at its first opening, three requests; a pack comes
// when the box reaches it, one request. Everything stays for stickerTTL: a box
// opened again meanwhile asks nothing. Each request waits for the FLOOD_WAIT
// breaker.
// ponytail: no hash refresh; a pack installed from the phone shows after
// stickerTTL. Use the hashes of Telegram if that matters.

// stickerTTL : how long the answers stay. Their file references expire (a send
// with an old one fails), within hours.
const stickerTTL = time.Hour

// stickerCache : the answers of Telegram (under c.mu).
type stickerCache struct {
	loaded        bool
	at            time.Time // when they came
	recent, faved []tg.DocumentClass
	sets          []tg.StickerSet
	packs         map[string][]tg.DocumentClass
}

// fresh : the answers are there and young enough to use.
func (sc *stickerCache) fresh(now time.Time) bool { return sc.loaded && now.Sub(sc.at) < stickerTTL }

// stickerOf : a sticker of the box, false when it has nothing to show (a
// Lottie or video one with no still thumb). The grid shows still pictures: a
// video sticker shows its thumb, not the whole video. Send is the document,
// sent back as it is.
func stickerOf(d *tg.Document) (model.Sticker, bool) {
	md := docMedia(d)
	if md.Kind == model.MediaGIF { // a video sticker
		t, w, h := stillThumb(d.Thumbs)
		if t == "" {
			return model.Sticker{}, false
		}
		md = &model.Media{Kind: model.MediaSticker, Label: md.Label, Emoji: md.Emoji, Mime: "image/webp", Ext: ".webp",
			W: w, H: h, Loc: d.AsInputDocumentFileLocation(t)}
	}
	if md.Kind != model.MediaSticker || !md.Previewable() {
		return model.Sticker{}, false
	}
	return model.Sticker{Preview: md, Emoji: md.Emoji,
		Send: &tg.InputDocument{ID: d.ID, AccessHash: d.AccessHash, FileReference: d.FileReference}}, true
}

func stickersOf(docs []tg.DocumentClass) []model.Sticker {
	var out []model.Sticker
	for _, dc := range docs {
		if d, ok := dc.(*tg.Document); ok {
			if s, ok := stickerOf(d); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// sectionsOf : recents and favorites (left out when empty), then the packs,
// empty, keyed "<id>:<access hash>" for LoadStickerPack.
func sectionsOf(recent, faved []tg.DocumentClass, sets []tg.StickerSet) []model.StickerSection {
	var out []model.StickerSection
	if s := stickersOf(recent); len(s) > 0 {
		out = append(out, model.StickerSection{Title: i18n.T("tg_stickers_recent"), Stickers: s})
	}
	if s := stickersOf(faved); len(s) > 0 {
		out = append(out, model.StickerSection{Title: i18n.T("tg_stickers_faved"), Stickers: s})
	}
	for _, set := range sets {
		out = append(out, model.StickerSection{Title: set.Title, Key: fmt.Sprintf("%d:%d", set.ID, set.AccessHash)})
	}
	return out
}

func (c *Client) LoadStickers(ctx context.Context, chat *model.Chat) {
	go func() {
		ev := model.EvStickers{ChatID: chat.ID}
		defer c.Guard("LoadStickers", func(err string) { ev.Err = err; c.Post(ev) })
		c.mu.Lock()
		sc := c.stickers
		c.mu.Unlock()
		if !sc.fresh(time.Now()) {
			sc = stickerCache{}
			if err := c.fetchStickers(ctx, &sc); err != nil {
				ev.Err = err.Error()
				c.Post(ev)
				return
			}
			sc.loaded, sc.at = true, time.Now()
			c.mu.Lock()
			c.stickers = sc // the packs of before go: their references are as old
			c.mu.Unlock()
		}
		ev.Sections = sectionsOf(sc.recent, sc.faved, sc.sets)
		c.Post(ev)
	}()
}

// fetchStickers asks for the recents, the favorites and the packs.
func (c *Client) fetchStickers(ctx context.Context, sc *stickerCache) error {
	if !c.floodOK() {
		return errFlood()
	}
	rec, err := c.api.MessagesGetRecentStickers(ctx, &tg.MessagesGetRecentStickersRequest{})
	if err != nil {
		c.floodTrip(err)
		return err
	}
	fav, err := c.api.MessagesGetFavedStickers(ctx, 0)
	if err != nil {
		c.floodTrip(err)
		return err
	}
	all, err := c.api.MessagesGetAllStickers(ctx, 0)
	if err != nil {
		c.floodTrip(err)
		return err
	}
	if r, ok := rec.(*tg.MessagesRecentStickers); ok {
		sc.recent = r.Stickers
	}
	if f, ok := fav.(*tg.MessagesFavedStickers); ok {
		sc.faved = f.Stickers
	}
	if a, ok := all.(*tg.MessagesAllStickers); ok {
		sc.sets = a.Sets
	}
	return nil
}

func (c *Client) LoadStickerPack(ctx context.Context, chat *model.Chat, key string) {
	go func() {
		ev := model.EvStickerPack{ChatID: chat.ID, Key: key}
		defer c.Guard("LoadStickerPack", func(err string) { ev.Err = err; c.Post(ev) })
		c.mu.Lock()
		docs, ok := c.stickers.packs[key]
		c.mu.Unlock()
		if !ok {
			var id, hash int64
			if _, err := fmt.Sscanf(key, "%d:%d", &id, &hash); err != nil {
				ev.Err = err.Error()
				c.Post(ev)
				return
			}
			if !c.floodOK() {
				ev.Err = errFlood().Error()
				c.Post(ev)
				return
			}
			res, err := c.api.MessagesGetStickerSet(ctx, &tg.MessagesGetStickerSetRequest{
				Stickerset: &tg.InputStickerSetID{ID: id, AccessHash: hash}})
			if err != nil {
				c.floodTrip(err)
				ev.Err = err.Error()
				c.Post(ev)
				return
			}
			if set, ok := res.(*tg.MessagesStickerSet); ok {
				docs = set.Documents
			}
			c.mu.Lock()
			if c.stickers.packs == nil {
				c.stickers.packs = map[string][]tg.DocumentClass{}
			}
			c.stickers.packs[key] = docs
			c.mu.Unlock()
		}
		ev.Stickers = stickersOf(docs)
		c.Post(ev)
	}()
}

func (c *Client) SearchStickers(ctx context.Context, chat *model.Chat, emoji string) {
	go func() {
		ev := model.EvStickerSearch{ChatID: chat.ID, Query: emoji}
		defer c.Guard("SearchStickers", func(err string) { ev.Err = err; c.Post(ev) })
		if !c.floodOK() {
			ev.Err = errFlood().Error()
			c.Post(ev)
			return
		}
		res, err := c.api.MessagesGetStickers(ctx, &tg.MessagesGetStickersRequest{Emoticon: emoji})
		if err != nil {
			c.floodTrip(err)
			ev.Err = err.Error()
		} else if s, ok := res.(*tg.MessagesStickers); ok {
			ev.Stickers = stickersOf(s.Stickers)
		}
		c.Post(ev)
	}()
}

// SendSticker posts the document of a sticker of the box.
// ponytail: an expired file reference (a box kept open for hours) fails the
// send with its error; ask the pack again then if it shows up.
func (c *Client) SendSticker(ctx context.Context, chat *model.Chat, s model.Sticker, tmpID int64) {
	doc, ok := s.Send.(*tg.InputDocument)
	if !ok { // handle of another network: nothing to post here
		c.Refuse("SendSticker", model.EvSent{ChatID: chat.ID, TmpID: tmpID, Err: i18n.T("media_foreign")})
		return
	}
	c.sent("SendSticker", chat, tmpID, func() (tg.UpdatesClass, error) {
		return c.sender.To(c.peer(chat)).Media(ctx, message.Media(&tg.InputMediaDocument{ID: doc}))
	})
}
