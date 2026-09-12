package dsc

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/diamondburned/arikawa/v3/api"
	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/utils/json/option"
	"github.com/diamondburned/arikawa/v3/utils/sendpart"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	rend "github.com/govlog/ttyloom/internal/render"
)

// The rest of the Backend interface: sends, edits, reactions, history,
// members, transfers — and the plain answers of what Discord does not do.
// Every method gives the hand back at once (goroutine + event), and every
// goroutine is guarded: a panic on remote data must not take the terminal
// down with it.

// ids reads the opaque handle of a chat: its channel, and the guild it
// belongs to (invalid for a DM). A chat whose Peer was lost still gives its
// channel — the id of a Discord chat is its channel id.
func ids(chat *model.Chat) (discord.ChannelID, discord.GuildID) {
	if p, ok := chat.Peer.(peer); ok {
		return discord.ChannelID(p.Channel), discord.GuildID(p.Guild)
	}
	return discord.ChannelID(chat.ID), 0
}

// rest gives the REST client bound to ctx: arikawa carries the context on the
// client, not on each call.
func (c *Client) rest(ctx context.Context) *api.Client { return c.state().Client.WithContext(ctx) }

// --- what Discord does not do ---

// unsupported : text shown in the window of a chat for an action Discord has
// no equivalent of.
func unsupported() string { return i18n.T("net_unsupported", model.NetDiscord) }

// warn : answer of a method Discord has no equivalent of and that the UI
// waits no event from. Same goroutine + post shape as refuse: postNB would
// drop the line whenever the UI channel is full, and the promised trace in
// /debug is the only thing these methods leave behind.
//
// ponytail: the line lands in the debug log only (/debug), never in a window.
// Leave and Block no longer reach it from the menu — Caps.Leave and Caps.Block
// gate their entries out — so what is left here is DeleteChat on a guild
// channel, refused with nothing but that one log line.
func (c *Client) warn(name string) {
	c.refuse(name, model.EvLog{Level: "WARN", Msg: "discord: " + name + " not supported"})
}

// refuse : same thing for a method the UI does wait an event from — the
// window would keep waiting for an answer that will never come.
func (c *Client) refuse(name string, ev model.Event) {
	go func() {
		defer c.Guard(name, nil)
		c.Post(ev)
	}()
}

func (c *Client) SearchContacts(_ context.Context, q string, _ int) {
	c.refuse("SearchContacts", model.EvContactsFound{Query: q, Err: unsupported()})
}

func (c *Client) Contacts(context.Context) {
	c.refuse("Contacts", model.EvContacts{Err: unsupported()})
}

func (c *Client) Resolve(_ context.Context, q string, _ bool) {
	c.refuse("Resolve", model.EvChat{Query: q, Err: unsupported()})
}

func (c *Client) Whois(_ context.Context, chat *model.Chat) {
	c.refuse("Whois", model.EvWhois{ChatID: chat.ID, Err: unsupported()})
}

func (c *Client) WhoisMember(context.Context, string) {
	c.refuse("WhoisMember", model.EvWhois{Err: unsupported()})
}

// WhoRead : Discord has no read receipt (Caps.ReadReceipts false), so the
// popup of the tick never opens on its own. The line says so all the same.
func (c *Client) WhoRead(_ context.Context, chat *model.Chat, id, _ int) {
	c.refuse("WhoRead", model.EvWho{ChatID: chat.ID, ID: id, Text: unsupported()})
}

// Block, BlockMember and Leave are out of the v1: nothing half done, and no
// event that would drop the chat from the sidebar for an action that never
// happened.
func (c *Client) Block(context.Context, *model.Chat)  { c.warn("Block") }
func (c *Client) BlockMember(context.Context, string) { c.warn("BlockMember") }
func (c *Client) Leave(context.Context, *model.Chat)  { c.warn("Leave") }

// DownloadMap : no Discord media carries coordinates, the UI never asks. The
// event still comes back — without it the media would spin for ever.
func (c *Client) DownloadMap(_ context.Context, m *model.Media, _ string) {
	c.refuse("DownloadMap", model.EvDownloaded{Media: m, Err: unsupported()})
}

// --- sends ---

// send is the one path of every text send. The REST answer only gives the id
// back (EvSent unpends the local line); the message itself comes in through
// MessageCreate, like tgc.
func (c *Client) send(ctx context.Context, name string, chat *model.Chat, tmpID int64, data api.SendMessageData) {
	go func() {
		ev := model.EvSent{ChatID: chat.ID, TmpID: tmpID}
		defer c.Guard(name, func(err string) { c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID, Err: err}) })
		chID, guild := ids(chat)
		data.Content = c.customs(data.Content, guild)
		m, err := c.rest(ctx).SendMessageComplex(chID, data)
		switch {
		case err != nil:
			ev.Err = err.Error()
		case m != nil:
			ev.ID = int(m.ID)
		}
		c.Post(ev)
	}()
}

func (c *Client) Send(ctx context.Context, chat *model.Chat, text string, tmpID int64) {
	c.send(ctx, "Send", chat, tmpID, api.SendMessageData{Content: text})
}

// SendReply : like Send, as a reply to the message replyTo. Only MessageID is
// needed, the channel is the one of the call.
func (c *Client) SendReply(ctx context.Context, chat *model.Chat, text string, replyTo int, tmpID int64) {
	c.send(ctx, "SendReply", chat, tmpID, api.SendMessageData{Content: text,
		Reference: &discord.MessageReference{MessageID: discord.MessageID(replyTo)}})
}

// SendStyled : like Send, the segments rendered back into Discord markdown.
func (c *Client) SendStyled(ctx context.Context, chat *model.Chat, segs []model.Seg, tmpID int64) {
	c.send(ctx, "SendStyled", chat, tmpID, api.SendMessageData{Content: render(segs)})
}

// SendPre : like Send, but text goes as a code block (multiline paste).
func (c *Client) SendPre(ctx context.Context, chat *model.Chat, text string, tmpID int64) {
	c.SendStyled(ctx, chat, []model.Seg{{Text: text, Kind: model.SegPre}}, tmpID)
}

// SendPhoto and SendFile are the same call: Discord makes no difference at
// the send, it decides from the type of the file it receives.
func (c *Client) SendPhoto(ctx context.Context, chat *model.Chat, path, caption string, removeAfter bool, tmpID int64) {
	c.upload(ctx, chat, path, caption, removeAfter, tmpID)
}

func (c *Client) SendFile(ctx context.Context, chat *model.Chat, path, caption string, removeAfter bool, tmpID int64) {
	c.upload(ctx, chat, path, caption, removeAfter, tmpID)
}

// upload sends a local file. EvSent like the text sends, since the UI shows
// the send as a pending line: the receipt unpends it, and the echo of the
// message comes in besides through MessageCreate.
// removeAfter : work file (pasted image), erased once the send worked.
func (c *Client) upload(ctx context.Context, chat *model.Chat, path, caption string, removeAfter bool, tmpID int64) {
	go func() {
		fail := func(err string) {
			c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID,
				Err: i18n.T("upload_error", filepath.Base(path), err)})
		}
		defer c.Guard("upload", fail)
		// Own semaphore: a send never waits behind three downloads.
		c.ulSem <- struct{}{}
		defer func() { <-c.ulSem }()
		f, err := os.Open(path)
		if err != nil {
			fail(err.Error())
			return
		}
		defer f.Close()
		chID, _ := ids(chat)
		m, err := c.rest(ctx).SendMessageComplex(chID, api.SendMessageData{Content: caption,
			Files: []sendpart.File{{Name: filepath.Base(path), Reader: f}}})
		if err != nil {
			fail(err.Error()) // the file stays: the send can be tried again
			return
		}
		if removeAfter {
			os.Remove(path)
		}
		ev := model.EvSent{ChatID: chat.ID, TmpID: tmpID}
		if m != nil {
			ev.ID = int(m.ID)
		}
		c.Post(ev)
	}()
}

// reCustom : ":name:" as typed, the shape of a custom emoji name.
var reCustom = regexp.MustCompile(`:([A-Za-z0-9_~]+):`)

// customs turns every ":name:" the guild knows as a custom emoji into the
// <:name:id> Discord shows as the image — the mirror of parse. An unknown
// name stays text, and outside a guild nothing changes.
// ponytail: the emojis of the other guilds are not looked at; add them when
// somebody with Nitro asks.
func (c *Client) customs(text string, guild discord.GuildID) string {
	if !guild.IsValid() || !strings.Contains(text, ":") {
		return text
	}
	es, err := c.state().Cabinet.Emojis(guild)
	if err != nil || len(es) == 0 {
		return text
	}
	return reCustom.ReplaceAllStringFunc(text, func(m string) string {
		name := m[1 : len(m)-1]
		for _, e := range es {
			if e.Name == name {
				return e.String() // <:name:id>, <a:name:id> when animated
			}
		}
		return m
	})
}

// --- edit, delete ---

func (c *Client) Edit(ctx context.Context, chat *model.Chat, id int, text string) {
	c.edit(ctx, "Edit", chat, id, text)
}

// EditStyled : like Edit, the segments rendered back into Discord markdown.
func (c *Client) EditStyled(ctx context.Context, chat *model.Chat, id int, segs []model.Seg) {
	c.edit(ctx, "EditStyled", chat, id, render(segs))
}

// edit : nullable content, not the plain EditMessage — that one leaves an
// empty text untouched, and an edit down to nothing would then look like it
// worked while nothing changed.
func (c *Client) edit(ctx context.Context, name string, chat *model.Chat, id int, text string) {
	go func() {
		ev := model.EvEdited{ChatID: chat.ID, ID: id}
		defer c.Guard(name, func(err string) { c.Post(model.EvEdited{ChatID: chat.ID, ID: id, Err: err}) })
		chID, guild := ids(chat)
		text = c.customs(text, guild)
		_, err := c.rest(ctx).EditMessageComplex(chID, discord.MessageID(id),
			api.EditMessageData{Content: option.NewNullableString(text)})
		if err != nil {
			ev.Err = err.Error()
		}
		c.Post(ev)
	}()
}

// Delete deletes a message. The gateway update comes too: the UI drops the
// duplicate on Deleted.
func (c *Client) Delete(ctx context.Context, chat *model.Chat, id int) {
	go func() {
		defer c.Guard("Delete", nil)
		chID, _ := ids(chat)
		if err := c.rest(ctx).DeleteMessage(chID, discord.MessageID(id), ""); err != nil {
			c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("delete_error", err)})
			return
		}
		c.Post(model.EvDeleted{ChatID: chat.ID, IDs: []int{id}})
	}()
}

// DeleteChat closes a DM: Discord drops it from the list, the history stays
// and writing again brings it back. On a group DM the same route means
// leaving the group, which is what "delete the chat" says there.
//
// The type of the channel decides, never the guild id of the handle: a chat
// the cache did not hold when it was made carries Peer{Guild: 0} (see
// chatFor), and DeleteChannel on a guild channel with MANAGE_CHANNELS would
// delete the channel for everybody. Unknown channel = refused.
func (c *Client) DeleteChat(ctx context.Context, chat *model.Chat) {
	chID, _ := ids(chat)
	ch, err := c.state().Cabinet.Channel(chID)
	if err != nil || (ch.Type != discord.DirectMessage && ch.Type != discord.GroupDM) {
		c.warn("DeleteChat")
		return
	}
	title := chat.Title // read here: the goroutine never touches the state of the UI
	go func() {
		defer c.Guard("DeleteChat", nil)
		if err := c.rest(ctx).DeleteChannel(chID, ""); err != nil {
			c.Post(model.EvLog{Level: "ERROR",
				Msg: i18n.T("chat_action_error", i18n.T("action_delete"), title, err)})
			return
		}
		c.Post(model.EvChatGone{ChatID: chat.ID})
	}()
}

// --- reactions ---

// React sets or drops my reaction on a message. The list up to date comes
// back on its own through MessageReactionAdd/Remove, so nothing is posted
// here beyond the errors.
func (c *Client) React(ctx context.Context, chat *model.Chat, id int, e string) {
	go func() {
		defer c.Guard("React", nil)
		chID, guild := ids(chat)
		mid := discord.MessageID(id)
		// Cache only: a REST read per click would be neither sober nor useful,
		// the message under the cursor is one the window has just shown.
		m, _ := c.state().Cabinet.Message(chID, mid)
		e, add := toggleReaction(m, e)
		if e == "" {
			return // nothing of mine to drop
		}
		ae, ok := c.apiEmoji(e, m, guild)
		if !ok {
			c.Post(model.EvReactionFailed{ChatID: chat.ID, ID: id, Emoji: e,
				Reason: i18n.T("reaction_not_available")})
			return
		}
		var err error
		if add {
			err = c.rest(ctx).React(chID, mid, ae)
		} else {
			err = c.rest(ctx).Unreact(chID, mid, ae)
		}
		if err != nil {
			c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("react_error", err)})
		}
	}()
}

// toggleReaction decides what one React call does on m: a reaction the
// account already set goes away, any other one is added — clicking one's own
// reaction again removes it, like the Discord client. e "" means "drop my
// reaction": the UI counts one reaction per message (Telegram) and does not
// name it, so it is read from the message. m nil = message not in the cache.
//
// ponytail: with several reactions of mine on the same message (set from
// another client — Discord allows it, the UI does not), a "" drops the first
// one found. Name the emoji in the Backend call if that ever shows in use.
func toggleReaction(m *discord.Message, e string) (string, bool) {
	if m != nil {
		for _, r := range m.Reactions {
			switch {
			case e == "" && r.Me:
				return emojiOf(r.Emoji), false
			case e != "" && emojiOf(r.Emoji) == e:
				return e, !r.Me
			}
		}
	}
	return e, e != ""
}

// apiEmoji turns what the UI carries (":name:" for a custom emoji, the glyph
// itself otherwise) into what the REST route wants — "name:id" for a custom
// one. The reactions already on the message come first: clicking one of them
// is the usual way in, and each carries its own id. Failing that the emojis
// of the guild are read from the cache. false = nothing that can be sent.
func (c *Client) apiEmoji(e string, m *discord.Message, guild discord.GuildID) (discord.APIEmoji, bool) {
	name, ok := strings.CutPrefix(e, ":")
	if !ok {
		return discord.APIEmoji(e), e != "" // unicode: the glyph is the identifier
	}
	name, ok = strings.CutSuffix(name, ":")
	if !ok || name == "" {
		return discord.APIEmoji(e), true // a lone ":" is text, not a custom emoji
	}
	if m != nil {
		for _, r := range m.Reactions {
			if r.Emoji.ID.IsValid() && r.Emoji.Name == name {
				return discord.NewAPIEmoji(r.Emoji.ID, name), true
			}
		}
	}
	if guild.IsValid() {
		es, err := c.state().Cabinet.Emojis(guild)
		if err == nil {
			for _, em := range es {
				if em.Name == name {
					return discord.NewAPIEmoji(em.ID, name), true
				}
			}
		}
	}
	return "", false
}

// reactorsLimit : people named per reaction.
// ponytail: the first 50, no paging — page only if somebody asks for the full
// list, like the same cap in tgc.
const reactorsLimit = 50

// reactors gives the "who reacted" line: one group per emoji, names joined.
// One call per emoji of the message — it only happens on the key that asks
// for it, and a message with many different reactions is rare.
func (c *Client) reactors(ctx context.Context, chID discord.ChannelID, guild discord.GuildID, m *discord.Message, rs []model.Reaction) string {
	rest := c.rest(ctx)
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		e, ok := c.apiEmoji(r.Emoji, m, guild)
		if !ok {
			continue // custom emoji of another guild: no id to ask with
		}
		us, err := rest.Reactions(chID, m.ID, e, reactorsLimit)
		if err != nil {
			continue
		}
		names := make([]string, 0, len(us))
		for _, u := range us {
			if int64(u.ID) == c.self.Load() {
				names = append(names, i18n.T("me"))
				continue
			}
			names = append(names, u.DisplayOrUsername())
		}
		out = append(out, r.Emoji+" "+strings.Join(names, ", "))
	}
	if len(out) == 0 {
		return i18n.T("unavailable")
	}
	return strings.Join(out, " · ")
}

// WhoReacted gives the "who reacted" line of the hover popup of a reaction.
func (c *Client) WhoReacted(ctx context.Context, chat *model.Chat, id int, rs []model.Reaction) {
	go func() {
		defer c.Guard("WhoReacted", nil)
		chID, guild := ids(chat)
		mid := discord.MessageID(id)
		m, err := c.state().Cabinet.Message(chID, mid) // for the ids of the custom emojis
		if err != nil {
			m = &discord.Message{ID: mid, ChannelID: chID}
		}
		c.Post(model.EvWho{ChatID: chat.ID, ID: id, React: true,
			Text: c.reactors(ctx, chID, guild, m, rs)})
	}()
}

// --- history ---

// history is the one path of the three loads. The three Discord routes all
// answer newest first; msgsOf turns the page around. minID drops what the
// window already holds (sync), 0 keeps the whole page.
func (c *Client) history(ctx context.Context, name string, chat *model.Chat, ev model.EvHistory, limit, minID int, fetch func(*api.Client, discord.ChannelID) ([]discord.Message, error)) {
	go func() {
		defer c.Guard(name, func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		chID, guild := ids(chat)
		ms, err := fetch(c.rest(ctx), chID)
		if err != nil {
			ev.Err = err.Error()
			c.Post(ev)
			return
		}
		// A REST page is the only way a message reaches the store outside the
		// gateway, and the whole package reads the store: without this, an ack
		// (ningen refuses to ack a message it does not hold), dropping one's
		// own reaction and the ids of the custom emojis all miss on everything
		// the window loaded at opening. Seeded before the filter: a message
		// already shown is one the user can still act on.
		//
		// The guild goes back on first: a REST message object carries no
		// guild_id, only a gateway event does, and without it nameOf reads the
		// display name instead of the nickname of the member — on the page
		// shown here, and on the store copy an edit or a reaction re-converts
		// later.
		for i := range ms {
			ms[i].GuildID = guild
			c.state().Cabinet.MessageSet(&ms[i], false)
		}
		ms = newerThan(ms, minID)
		ev.Msgs = c.msgsOf(ms)
		// A short sync proves nothing about the older messages, and neither
		// does a page centred on a jump: Done stays false there, otherwise
		// scrolling up in the window would be blocked.
		ev.Done = !ev.Since && !ev.Around && len(ms) < limit
		c.Post(ev)
	}()
}

// newerThan keeps the messages with an id above minID. The page comes newest
// first, so the first one already known ends it — same walk as tgc. minID 0
// keeps everything.
func newerThan(ms []discord.Message, minID int) []discord.Message {
	for i, m := range ms {
		if int(m.ID) <= minID {
			return ms[:i]
		}
	}
	return ms
}

// msgsOf converts a history page, oldest first — the order EvHistory goes up
// in, and the one the window reads.
func (c *Client) msgsOf(ms []discord.Message) []model.Msg {
	out := make([]model.Msg, 0, len(ms))
	for i := len(ms) - 1; i >= 0; i-- {
		out = append(out, c.msgOf(&ms[i]))
	}
	return out
}

// page : limit of a fetch. Zero means "everything" for arikawa, which would
// page over the whole channel; the UI never asks for it.
func page(limit int) uint { return uint(max(limit, 1)) }

// LoadHistory loads limit messages before beforeID (0 = the newest ones).
func (c *Client) LoadHistory(ctx context.Context, chat *model.Chat, beforeID, limit int) {
	c.history(ctx, "LoadHistory", chat, model.EvHistory{ChatID: chat.ID, Older: beforeID > 0}, limit, 0,
		func(a *api.Client, ch discord.ChannelID) ([]discord.Message, error) {
			return a.MessagesBefore(ch, discord.MessageID(beforeID), page(limit))
		})
}

// LoadHistoryAround loads a page centred on id: a precise jump must not end
// on its target.
func (c *Client) LoadHistoryAround(ctx context.Context, chat *model.Chat, id, limit int) {
	c.history(ctx, "LoadHistoryAround", chat,
		model.EvHistory{ChatID: chat.ID, Around: true, AroundID: id}, limit, 0,
		func(a *api.Client, ch discord.ChannelID) ([]discord.Message, error) {
			return a.MessagesAround(ch, discord.MessageID(id), page(limit))
		})
}

// LoadHistorySince loads up to limit messages with an id above minID (sync of
// a chat at start). minID zero: the recent history.
//
// MessagesAfter is not used: it pages forward, so a channel with more than
// limit new messages would give the OLDEST ones after minID and leave an
// unmarked hole in the middle of the window. The walk goes from the newest
// down to minID instead, like tgc — a page too short can then only miss the
// far end of the past, which the window loads on its own by scrolling up.
func (c *Client) LoadHistorySince(ctx context.Context, chat *model.Chat, minID, limit int) {
	c.history(ctx, "LoadHistorySince", chat, model.EvHistory{ChatID: chat.ID, Since: true}, limit, minID,
		func(a *api.Client, ch discord.ChannelID) ([]discord.Message, error) {
			return a.MessagesBefore(ch, 0, page(limit))
		})
}

// --- message information ---

// Info gives the information lines of a message (key "i"). readMax and
// readInboxMax are ignored: Discord has no read receipt, and the "read by"
// lines of tgc have nothing to show here.
func (c *Client) Info(ctx context.Context, chat *model.Chat, id, _, _ int) {
	go func() {
		ev := model.EvInfo{ChatID: chat.ID, ID: id}
		fail := func(err string) {
			ev.Lines = []string{i18n.T("info_error", err)}
			c.Post(ev)
		}
		defer c.Guard("Info", fail)
		chID, guild := ids(chat)
		mid := discord.MessageID(id)
		m, err := c.state().Message(chID, mid) // the cache first, the network after
		if err != nil {
			fail(err.Error())
			return
		}
		// The snowflake is the send date, to the millisecond.
		ev.Lines = append(ev.Lines, i18n.T("info_sent_at", i18n.LocalTime(mid.Time())))
		if m.EditedTimestamp.IsValid() {
			ev.Lines = append(ev.Lines, i18n.T("info_edited_at", i18n.LocalTime(m.EditedTimestamp.Time())))
		}
		if rs := reactionsOf(m.Reactions); len(rs) > 0 {
			ev.Lines = append(ev.Lines, i18n.T("info_reactions", c.reactors(ctx, chID, guild, m, rs)))
		}
		ev.Lines = append(ev.Lines, i18n.T("info_id", id))
		c.Post(ev)
	}()
}

// --- members ---

// partsLimit : members shown in the box.
// ponytail: the first 200 held by the cache, a count line above them; add
// paging only if somebody asks for the full list of a big guild.
const partsLimit = 200

// Participants fills the member box (F3). A DM has its recipients; a guild
// channel has the members the gateway has sent so far.
func (c *Client) Participants(_ context.Context, chat *model.Chat) {
	go func() {
		ev := model.EvParticipants{ChatID: chat.ID}
		defer c.Guard("Participants", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		chID, guild := ids(chat)
		if !guild.IsValid() { // DM or group DM
			ch, err := c.state().Cabinet.Channel(chID)
			if err != nil {
				ev.Err = err.Error()
				c.Post(ev)
				return
			}
			for _, u := range ch.DMRecipients {
				ev.Lines = append(ev.Lines, c.member(guild, u, ""))
			}
			c.Post(ev)
			return
		}
		// Only the gateway fills the member list of a guild. The request goes
		// out (asynchronous, it subscribes the guild on the way) and what the
		// cache already holds is shown at once: a first opening can be short,
		// the next one is complete.
		c.state().MemberState.RequestMemberList(guild, chID, 0)
		ms, err := c.state().Cabinet.Members(guild)
		if err != nil {
			ev.Err = err.Error()
			c.Post(ev)
			return
		}
		if len(ms) > partsLimit {
			ev.Lines = append(ev.Lines,
				model.Participant{Text: i18n.T(i18n.Plural(len(ms), "members"), len(ms))})
			ms = ms[:partsLimit]
		}
		for _, m := range ms {
			ev.Lines = append(ev.Lines, c.member(guild, m.User, m.Nick))
		}
		c.Post(ev)
	}()
}

// member : one line of the box. Query is what a click sends — the @name when
// there is one, the id otherwise, same rule as tgc. Online is the accent
// colour, and only "online" earns it: idle, do-not-disturb and invisible all
// mean "not available", the same rule presenceOf reads.
//
// ponytail: a click routes the Query through Resolve, which Discord does not
// have — the line is readable, not clickable. Opening a DM from the box is a
// matter for the UI, not for this handle.
func (c *Client) member(guild discord.GuildID, u discord.User, nick string) model.Participant {
	p := model.Participant{Text: cmp.Or(nick, u.DisplayOrUsername()),
		Query: strconv.FormatUint(uint64(u.ID), 10)}
	if u.Username != "" {
		p.Query = "@" + u.Username
	}
	if int64(u.ID) == c.self.Load() {
		p.Text += i18n.T("member_me_suffix")
	}
	if pr, err := c.state().Cabinet.Presence(guild, u.ID); err == nil {
		p.Online = pr.Status == discord.OnlineStatus
	}
	return p
}

// --- typing, read marks ---

// Typing tells "typing" in chat. cancel is ignored: Discord has no cancel,
// the hint dies out on its own after ten seconds.
func (c *Client) Typing(ctx context.Context, chat *model.Chat, cancel bool) {
	if cancel {
		return
	}
	go func() {
		defer c.Guard("Typing", nil)
		chID, _ := ids(chat)
		c.rest(ctx).Typing(chID) // fire and forget: a missed hint is not an error path
	}()
}

// MarkRead acks the channel up to maxID. The state sends the ack itself and
// gives no error back — same fire and forget as tgc.
func (c *Client) MarkRead(_ context.Context, chat *model.Chat, maxID int) {
	go func() {
		defer c.Guard("MarkRead", nil)
		chID, _ := ids(chat)
		c.state().ReadState.MarkRead(chID, discord.MessageID(maxID))
	}()
}

// --- downloads ---

// dlClient : the CDN of Discord. A timeout on the whole exchange, so a
// stalled connection cannot hold a slot of the semaphore for ever.
var dlClient = &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 || !mediaRequestAllowed(req) {
		return errors.New("discord: media redirect refused")
	}
	return nil
}}

// Automatic media reads stay on the CDNs used by Discord and its GIF providers.
func mediaRequestAllowed(req *http.Request) bool {
	u := req.URL
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	// Discord now serves GIF search results from KLIPY. Keep this scoped to
	// its media CDN; the page URL is only used when sending the selected GIF.
	if host == "static.klipy.com" {
		return true
	}
	for _, domain := range []string{"discordapp.com", "discordapp.net", "tenor.com", "giphy.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

// dlAgent : neutral User-Agent. A request with none at all gets refused by
// the CDN, and the token of the account has nothing to do in a media read.
const dlAgent = "ttyloom/1.0"

// maxFetch : cap of one download — the ceiling Discord itself puts on an
// upload (Nitro). The size the API declares gates the automatic downloads; a
// body bigger than announced must not fill the disk. unknownFetch : cap of a
// media whose size the API did not give (a gifv embed): those are clips of a
// GIF provider, and the automatic gate cannot read a size that is not there.
const (
	maxFetch     = 512 << 20
	unknownFetch = 16 << 20
)

// Download downloads m.Loc to path (3 in parallel at most). A file already
// there = success at once.
func (c *Client) Download(ctx context.Context, m *model.Media, path string) {
	go func() {
		defer c.Guard("Download", func(err string) { c.Post(model.EvDownloaded{Media: m, Err: err}) })
		c.dlSem <- struct{}{}
		defer func() { <-c.dlSem }()
		if _, err := os.Stat(path); err == nil {
			c.Post(model.EvDownloaded{Media: m, Path: path})
			return
		}
		u, ok := m.Loc.(fileURL)
		if !ok { // handle of another backend: nothing to download here
			c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("media_foreign")})
			c.Post(model.EvDownloaded{Media: m, Err: i18n.T("media_foreign")})
			return
		}
		limit := int64(maxFetch)
		if m.Size == 0 {
			limit = unknownFetch
		}
		if err := fetch(ctx, string(u), path, limit); err != nil {
			c.Post(model.EvDownloaded{Media: m, Err: err.Error()})
			return
		}
		c.Post(model.EvDownloaded{Media: m, Path: path})
	}()
}

// fetch writes url to path, max bytes at most. 0700 on the directory and a
// temp file of its own like tgc: a private media must not be readable by the
// other accounts of the machine, two downloads of the same media no longer
// walk on each other, and no pre-existing symbolic link is followed.
func fetch(ctx context.Context, url, path string, max int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if !mediaRequestAllowed(req) {
		return errors.New("discord: media URL refused")
	}
	req.Header.Set("User-Agent", dlAgent)
	resp, err := dlClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".part-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	n, cerr := io.Copy(f, io.LimitReader(resp.Body, max+1))
	if n > max {
		cerr = fmt.Errorf(i18n.T("download_too_big"), rend.HumanSize(max))
	}
	if err := cmp.Or(cerr, f.Close()); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
