package dsc

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/utils/httputil"

	"github.com/govlog/ttyloom/internal/model"
)

// Search of Discord, the one of the official client (a user account): a
// guild channel searches its guild with channel_id, a direct message its own
// channel. The answer comes newest first, each hit in a group with the
// messages around it.

// globalDMs : direct message channels asked by a global search, the most
// recent first — each one is a request of its own, and an account carries
// hundreds. globalBudget bounds the whole sweep: the search route is rate
// limited on the Discord side, and the overlay shows the answers of every
// network only once the slowest is in.
const (
	globalDMs    = 10
	globalBudget = 15 * time.Second
)

// searchResponse : the groups as raw JSON — the "hit" mark that names the
// message of a group is not in the arikawa type.
type searchResponse struct {
	Messages [][]json.RawMessage `json:"messages"`
}

// hitsOf keeps one message per group: the one flagged hit, else the first.
func hitsOf(groups [][]json.RawMessage) []discord.Message {
	var out []discord.Message
	for _, g := range groups {
		if len(g) == 0 {
			continue
		}
		raw := g[0]
		for _, r := range g {
			var mark struct {
				Hit bool `json:"hit"`
			}
			if json.Unmarshal(r, &mark) == nil && mark.Hit {
				raw = r
				break
			}
		}
		var m discord.Message
		if json.Unmarshal(raw, &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// search runs one search — the guild route, or the channel route with no
// guild — and gives its hits, newest first.
func (c *Client) search(ctx context.Context, guild discord.GuildID, data api.SearchData) ([]discord.Message, error) {
	rest := c.rest(ctx)
	url := api.EndpointChannels + data.ChannelID.String() + "/messages/search"
	if guild.IsValid() {
		url = api.EndpointGuilds + guild.String() + "/messages/search"
	}
	var res searchResponse
	if err := rest.RequestJSON(&res, "GET", url, httputil.WithSchema(rest, data)); err != nil {
		return nil, err
	}
	ms := hitsOf(res.Messages)
	for i := range ms { // a REST message carries no guild: the nickname needs it
		ms[i].GuildID = guild
	}
	return ms, nil
}

// Search : /search in a chat, limit results at most, oldest first.
func (c *Client) Search(ctx context.Context, chat *model.Chat, q string, limit int) {
	go func() {
		ev := model.EvSearch{ChatID: chat.ID, Query: q}
		defer c.Guard("Search", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		chID, guild := ids(chat)
		ms, err := c.search(ctx, guild, api.SearchData{Content: q, ChannelID: chID})
		if err != nil {
			ev.Err = err.Error()
			c.Post(ev)
			return
		}
		if len(ms) > limit {
			ms = ms[:limit]
		}
		ev.Msgs = c.msgsOf(ms)
		c.Post(ev)
	}()
}

// SearchGlobal : one search per guild (all its channels at once) and one per
// recent direct message channel, the hits merged newest first, limit at most.
// A guild that refuses is skipped, and the budget spent ends the sweep with
// what it has; the error shows only when nothing answered.
func (c *Client) SearchGlobal(ctx context.Context, q string, limit int) {
	go func() {
		ev := model.EvSearchGlobal{Query: q}
		defer c.Guard("SearchGlobal", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		ctx, cancel := context.WithTimeout(ctx, globalBudget)
		defer cancel()
		guilds, err := c.state().Guilds()
		if err != nil {
			ev.Err = err.Error()
			c.Post(ev)
			return
		}
		dms, _ := c.state().PrivateChannels()
		slices.SortFunc(dms, func(a, b discord.Channel) int { return cmp.Compare(b.LastMessageID, a.LastMessageID) })
		if len(dms) > globalDMs {
			dms = dms[:globalDMs]
		}
		var errs []string
		var hits []model.SearchHit
		add := func(ms []discord.Message, err error) {
			if err != nil {
				errs = append(errs, err.Error())
				return
			}
			for i := range ms {
				hits = append(hits, c.hitOf(&ms[i]))
			}
		}
		for _, g := range guilds {
			if ctx.Err() != nil {
				break
			}
			add(c.search(ctx, g.ID, api.SearchData{Content: q}))
		}
		for _, d := range dms {
			if ctx.Err() != nil {
				break
			}
			add(c.search(ctx, 0, api.SearchData{Content: q, ChannelID: d.ID}))
		}
		slices.SortStableFunc(hits, func(a, b model.SearchHit) int { return b.Date.Compare(a.Date) })
		if len(hits) > limit {
			hits = hits[:limit]
		}
		if len(hits) == 0 && len(errs) > 0 {
			ev.Err = strings.Join(errs, "; ")
		} else if len(errs) > 0 {
			c.Post(model.EvLog{Level: "WARN", Msg: fmt.Sprintf("discord: search: %s", strings.Join(errs, "; "))})
		}
		ev.Hits = hits
		c.Post(ev)
	}()
}

// hitOf : one line of the global search overlay. The chat comes from the
// cache, with its id for a title when it is not there.
func (c *Client) hitOf(m *discord.Message) model.SearchHit {
	msg := c.msgOf(m)
	text := msg.Text
	if text == "" && msg.Media != nil {
		text = msg.Media.Label
	}
	if msg.Service != "" {
		text = msg.Service
	}
	return model.SearchHit{Chat: c.chatFor(m.ChannelID), MsgID: msg.ID, Date: msg.Date, From: msg.From, Text: text}
}
