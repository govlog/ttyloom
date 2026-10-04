package dsc

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/utils/httputil"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// Search of Discord, the one of the official client (a user account): a
// guild channel searches its guild with channel_id, a direct message its own
// channel. The answer comes newest first, each hit in a group with the
// messages around it.

// globalGuilds, globalDMs : guilds and direct message channels asked by a
// global search, the most recently active first — each one is a request of
// its own, and an account carries dozens of guilds and hundreds of DMs:
// dozens of searches at each query look like a self-bot. globalBudget bounds
// the whole sweep: the search route is rate limited on the Discord side, and
// the overlay shows the answers of every network only once the slowest is in.
const (
	globalGuilds = 10
	globalDMs    = 10
	globalBudget = 15 * time.Second
)

// globalPace : the wait between two searches of a sweep. A variable: the
// test shortens it.
var globalPace = 250 * time.Millisecond

// searchResponse : the groups as raw JSON — the "hit" mark that names the
// message of a group is not in the arikawa type. Code indexing comes with an
// HTTP 202 and no message: the index of the guild or DM is still being built
// (a first search, a long idle one), RetryAfter seconds to wait.
type searchResponse struct {
	Messages   [][]json.RawMessage `json:"messages"`
	Code       int                 `json:"code"`
	RetryAfter float64             `json:"retry_after"`
}

const indexing = 110000

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
	get := func() error {
		res = searchResponse{}
		return rest.RequestJSON(&res, "GET", url, httputil.WithSchema(rest, data))
	}
	if err := get(); err != nil {
		return nil, err
	}
	// An index being built answers with no message: that is no "no result".
	// The delay it gives is waited once (5 s at most), then it says so.
	if res.Code == indexing {
		select {
		case <-time.After(min(time.Duration(res.RetryAfter*float64(time.Second)), 5*time.Second)):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if err := get(); err != nil {
			return nil, err
		}
		if res.Code == indexing {
			return nil, errors.New(i18n.T("dsc_search_indexing"))
		}
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
		ev := model.EvSearch{Started: time.Now(), ChatID: chat.ID, Query: q}
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
		// A guild is as recent as the last message of its channels (the cache
		// alone: nothing is asked for the order).
		last := map[discord.GuildID]discord.MessageID{}
		for _, g := range guilds {
			chs, _ := c.state().Cabinet.Channels(g.ID)
			for _, ch := range chs {
				last[g.ID] = max(last[g.ID], ch.LastMessageID)
			}
		}
		slices.SortFunc(guilds, func(a, b discord.Guild) int { return cmp.Compare(last[b.ID], last[a.ID]) })
		guilds = guilds[:min(len(guilds), globalGuilds)]
		dms, _ := c.state().PrivateChannels()
		slices.SortFunc(dms, func(a, b discord.Channel) int { return cmp.Compare(b.LastMessageID, a.LastMessageID) })
		dms = dms[:min(len(dms), globalDMs)]
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
		type target struct {
			guild discord.GuildID
			ch    discord.ChannelID
		}
		var ts []target
		for _, g := range guilds {
			ts = append(ts, target{guild: g.ID})
		}
		for _, d := range dms {
			ts = append(ts, target{ch: d.ID})
		}
		for i, t := range ts {
			if i > 0 { // paced, not back to back
				select {
				case <-time.After(globalPace):
				case <-ctx.Done():
				}
			}
			if ctx.Err() != nil {
				break
			}
			add(c.search(ctx, t.guild, api.SearchData{Content: q, ChannelID: t.ch}))
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
	return model.SearchHit{Chat: c.chatFor(m.ChannelID), MsgID: msg.ID, Date: msg.Date, From: msg.From, Text: msg.Summary()}
}
