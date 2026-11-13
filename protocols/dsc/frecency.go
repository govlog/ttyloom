package dsc

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/binary"
	"slices"
	"strings"
	"sync"

	"github.com/diamondburned/arikawa/v3/api"

	"github.com/govlog/ttyloom/internal/emoji"
	"github.com/govlog/ttyloom/internal/model"
)

// topMax : the most reactions put at the head of the picker.
const topMax = 24

// loadReactionsTop reads the reactions the account uses most from the
// settings Discord syncs between its apps (settings-proto type 2,
// FrecencyUserSettings), once per Run. A failure posts nothing: the picker
// keeps the local recents first.
// ponytail: the order is the one of the start; the gateway resumes and
// USER_SETTINGS_PROTO_UPDATE do not read it again. Follow that event if the
// order must move while connected.
func (c *Client) loadReactionsTop(ctx context.Context) {
	defer c.Guard("loadReactionsTop", nil)
	var res struct {
		Settings string `json:"settings"`
	}
	if err := c.rest(ctx).RequestJSON(&res, "GET", api.EndpointMe+"/settings-proto/2"); err != nil {
		return
	}
	blob, err := base64.StdEncoding.DecodeString(res.Settings)
	if err != nil {
		return
	}
	faved, used := stickerTop(blob)
	c.stickerTop.Store(&stickerPrefs{faved, used})
	if top := reactionTop(blob, c.customByID()); len(top) > 0 {
		c.Post(model.EvReactionsTop{Emojis: top})
	}
}

// customByID gives the ":name:" of a custom emoji of a guild in the cache from
// its id, "" when none holds it. The table is made at the first call.
func (c *Client) customByID() func(id string) string {
	var names map[string]string
	return func(id string) string {
		if names == nil {
			names = map[string]string{}
			gs, _ := c.state().Cabinet.Guilds()
			for _, g := range gs {
				es, _ := c.state().Cabinet.Emojis(g.ID)
				for _, e := range es {
					names[e.ID.String()] = ":" + e.Name + ":"
				}
			}
		}
		return names[id]
	}
}

// reactionTop reads FrecencyUserSettings: the favorite emojis (field 5) first,
// then the reaction frecency (field 13) by score, then by uses. A key is a
// Unicode emoji, a name ("joy", "100") or the id of a custom emoji, which
// custom turns into ":name:" ("" = none known). Each emoji comes once, in the form of
// the emoji table; at most topMax. A blob cut or broken gives what was read
// before the break.
func reactionTop(settings []byte, custom func(id string) string) []string {
	var favorites []string
	type used struct {
		key          string
		score, total int64
	}
	var frecency []used
	pbFields(settings, func(num int, _ uint64, data []byte) {
		switch num {
		case 5: // FavoriteEmojis{repeated string emojis = 1}
			pbFields(data, func(n int, _ uint64, s []byte) {
				if n == 1 {
					favorites = append(favorites, string(s))
				}
			})
		case 13: // EmojiFrecency{map<string, FrecencyItem> emojis = 1}
			pbFields(data, func(n int, _ uint64, entry []byte) {
				if n != 1 {
					return
				}
				var u used
				pbFields(entry, func(k int, _ uint64, v []byte) {
					switch k {
					case 1:
						u.key = string(v)
					case 2: // FrecencyItem{total_uses = 1, score = 4}
						pbFields(v, func(f int, x uint64, _ []byte) {
							switch f {
							case 1:
								u.total = int64(x)
							case 4:
								u.score = int64(int32(x)) // int32: a negative one takes ten bytes
							}
						})
					}
				})
				if u.key != "" {
					frecency = append(frecency, u)
				}
			})
		}
	})
	slices.SortStableFunc(frecency, func(a, b used) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(b.total, a.total))
	})
	keys := favorites
	for _, u := range frecency {
		keys = append(keys, u.key)
	}
	var out []string
	seen := map[string]bool{}
	for _, k := range keys {
		var e string
		if strings.Trim(k, "0123456789") == "" {
			e = custom(k) // the id of a custom emoji…
		}
		if e == "" {
			e = tableEmoji(k) // …or an emoji, or a name: "100" is 💯
		}
		if e != "" && !seen[e] && len(out) < topMax {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
}

var (
	tableOnce sync.Once
	tableBy   map[string]string // emoji.Base(char) and shortcode → char of the table
)

// tableEmoji gives the emoji of the table for a Unicode emoji or a shortcode
// ("joy"), "" when the table holds neither.
func tableEmoji(k string) string {
	tableOnce.Do(func() {
		tableBy = map[string]string{}
		for _, e := range emoji.All() {
			tableBy[emoji.Base(e.Char)] = e.Char
			for _, code := range e.Codes {
				if _, ok := tableBy[code]; !ok {
					tableBy[code] = e.Char
				}
			}
		}
	})
	if e, ok := tableBy[emoji.Base(k)]; ok {
		return e
	}
	return tableBy[k]
}

// pbFields walks the fields of a protobuf message and gives f the number of
// each one with its varint (wire type 0), its fixed 64 bit value (type 1) or
// its bytes (type 2); fixed 32 bit fields are skipped. It stops at the first field cut or of a type it
// cannot skip.
func pbFields(b []byte, f func(num int, v uint64, data []byte)) {
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			return
		}
		b = b[n:]
		num, typ := int(tag>>3), tag&7
		switch typ {
		case 0:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return
			}
			b = b[n:]
			f(num, v, nil)
		case 1:
			if len(b) < 8 {
				return
			}
			f(num, binary.LittleEndian.Uint64(b), nil)
			b = b[8:]
		case 5:
			if len(b) < 4 {
				return
			}
			b = b[4:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || l > uint64(len(b)-n) {
				return
			}
			f(num, 0, b[n:n+int(l)])
			b = b[n+int(l):]
		default:
			return
		}
	}
}
