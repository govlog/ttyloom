package dsc

import (
	"cmp"
	"context"
	"encoding/binary"
	"slices"

	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/utils/httputil"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// Stickers tab of the GIF box: the stickers of the server of the channel
// (Discord refuses the ones of another server without Nitro, which the account
// of Christopher does not have), the favorite and the most used ones first, as
// the synced settings give them. Everything is there at once: no pack to load,
// the box filters by name itself (Caps.StickerSearch false).
// ponytail: no Nitro; read User.Nitro of the READY and offer the other
// servers if it is ever needed.

// stickerPrefs : favorite and most used stickers, from the synced settings.
type stickerPrefs struct{ faved, used []uint64 }

// stickerTop reads FrecencyUserSettings: favorite_stickers (field 3, a
// repeated fixed64, packed or not) and sticker_frecency (field 4, a map of
// fixed64 to FrecencyItem) by score, then by uses.
func stickerTop(settings []byte) (faved, used []uint64) {
	type use struct {
		id           uint64
		score, total int64
	}
	var uses []use
	pbFields(settings, func(num int, _ uint64, data []byte) {
		switch num {
		case 3:
			pbFields(data, func(n int, v uint64, d []byte) {
				switch {
				case n != 1:
				case d == nil: // one id per field
					faved = append(faved, v)
				default: // packed
					for ; len(d) >= 8; d = d[8:] {
						faved = append(faved, binary.LittleEndian.Uint64(d))
					}
				}
			})
		case 4:
			pbFields(data, func(n int, _ uint64, entry []byte) {
				if n != 1 {
					return
				}
				var u use
				pbFields(entry, func(k int, v uint64, item []byte) {
					switch k {
					case 1:
						u.id = v
					case 2:
						pbFields(item, func(f int, x uint64, _ []byte) {
							switch f {
							case 1:
								u.total = int64(x)
							case 4:
								u.score = int64(int32(x))
							}
						})
					}
				})
				if u.id != 0 {
					uses = append(uses, u)
				}
			})
		}
	})
	slices.SortStableFunc(uses, func(a, b use) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(b.total, a.total))
	})
	for _, u := range uses {
		used = append(used, u.id)
	}
	return faved, used
}

// guildSections : the favorite stickers of the server, its most used ones,
// then all of them. Lottie ones are left out: nothing here draws them.
func guildSections(stickers []discord.Sticker, guildName string, faved, used []uint64) []model.StickerSection {
	// Each cell gets a picture of its own, also for a sticker in two sections:
	// the grid frees the picture of a cell far from the view.
	of := func(s discord.Sticker) model.Sticker {
		return model.Sticker{Name: s.Name, Tags: s.Tags, Send: s.ID,
			Preview: stickerMedia(discord.StickerItem{ID: s.ID, Name: s.Name, FormatType: s.FormatType})}
	}
	byID := map[uint64]discord.Sticker{}
	var all []model.Sticker
	for _, s := range stickers {
		if s.FormatType == discord.StickerFormatLottie {
			continue
		}
		byID[uint64(s.ID)] = s
		all = append(all, of(s))
	}
	pick := func(ids []uint64) []model.Sticker {
		var out []model.Sticker
		for _, id := range ids {
			if s, ok := byID[id]; ok {
				out = append(out, of(s))
			}
		}
		return out
	}
	var out []model.StickerSection
	if s := pick(faved); len(s) > 0 {
		out = append(out, model.StickerSection{Title: i18n.T("dsc_stickers_faved"), Stickers: s})
	}
	if s := pick(used); len(s) > 0 {
		out = append(out, model.StickerSection{Title: i18n.T("dsc_stickers_used"), Stickers: s})
	}
	if len(all) > 0 {
		out = append(out, model.StickerSection{Title: cmp.Or(guildName, i18n.T("dsc_stickers_server")), Stickers: all})
	}
	return out
}

func (c *Client) LoadStickers(ctx context.Context, chat *model.Chat) {
	go func() {
		ev := model.EvStickers{ChatID: chat.ID}
		defer c.Guard("LoadStickers", func(err string) { ev.Err = err; c.Post(ev) })
		_, guild := ids(chat)
		if !guild.IsValid() {
			ev.Err = i18n.T("dsc_stickers_none_here")
			c.Post(ev)
			return
		}
		list, ok := c.guildStickers.Load(guild)
		if !ok {
			var got []discord.Sticker
			if err := c.rest(ctx).RequestJSON(&got, "GET", api.EndpointGuilds+guild.String()+"/stickers"); err != nil {
				ev.Err = err.Error()
				c.Post(ev)
				return
			}
			c.guildStickers.Store(guild, got)
			list = got
		}
		var prefs stickerPrefs
		if p := c.stickerTop.Load(); p != nil {
			prefs = *p
		}
		ev.Sections = guildSections(list.([]discord.Sticker), c.guildName(guild), prefs.faved, prefs.used)
		if len(ev.Sections) == 0 {
			ev.Err = i18n.T("dsc_stickers_none_here")
		}
		c.Post(ev)
	}()
}

// Discord gives every sticker of the server at once: nothing to load nor to
// search on the network.
func (c *Client) LoadStickerPack(_ context.Context, chat *model.Chat, key string) {
	c.Refuse("LoadStickerPack", model.EvStickerPack{ChatID: chat.ID, Key: key, Err: unsupported()})
}

func (c *Client) SearchStickers(_ context.Context, chat *model.Chat, emoji string) {
	c.Refuse("SearchStickers", model.EvStickerSearch{ChatID: chat.ID, Query: emoji, Err: unsupported()})
}

// SendSticker posts a message holding the sticker alone. arikawa has no
// sticker_ids: the request goes as it is.
func (c *Client) SendSticker(ctx context.Context, chat *model.Chat, s model.Sticker, tmpID int64) {
	id, ok := s.Send.(discord.StickerID)
	if !ok { // handle of another network: nothing to post here
		c.Refuse("SendSticker", model.EvSent{ChatID: chat.ID, TmpID: tmpID, Err: i18n.T("media_foreign")})
		return
	}
	go func() {
		ev := model.EvSent{ChatID: chat.ID, TmpID: tmpID}
		defer c.Guard("SendSticker", func(err string) { c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID, Err: err}) })
		chID, _ := ids(chat)
		var m discord.Message
		err := c.rest(ctx).RequestJSON(&m, "POST", api.EndpointChannels+chID.String()+"/messages",
			httputil.WithJSONBody(map[string]any{"sticker_ids": []string{id.String()}}))
		if err != nil {
			ev.Err = err.Error()
		} else {
			ev.ID = int(m.ID)
		}
		c.Post(ev)
	}()
}
