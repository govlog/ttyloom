package tgc

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/govlog/ttyloom/internal/emoji"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// loadReactions gets the global list of the reactions of the account, once
// per connection. The inactive ones are dropped, and the premium ones too
// outside a premium account: the server would refuse them.
func (c *Client) loadReactions(ctx context.Context) {
	defer c.Guard("loadReactions", nil)
	res, err := c.api.MessagesGetAvailableReactions(ctx, 0)
	if err != nil {
		c.floodTrip(err)
	}
	list, _ := res.(*tg.MessagesAvailableReactions)
	if err != nil || list == nil {
		c.Post(model.EvReactionsList{Emojis: model.DefaultReactions})
		return
	}
	me := c.me.Load()
	premium := me != nil && me.Premium
	out := make([]string, 0, len(list.Reactions))
	for _, r := range list.Reactions {
		if r.Inactive || (r.Premium && !premium) {
			continue
		}
		out = append(out, r.Reaction)
	}
	if len(out) == 0 {
		out = model.DefaultReactions
	}
	c.Post(model.EvReactionsList{Emojis: out})
}

// reactionReason gives the text to show in the window for a refused reaction,
// "" for the other errors (already in the log).
func reactionReason(err error) string {
	switch {
	case tgerr.Is(err, "REACTION_INVALID"):
		return i18n.T("reaction_not_available")
	case tgerr.Is(err, "REACTIONS_TOO_MANY"):
		return i18n.T("reaction_too_many")
	case tgerr.Is(err, "PREMIUM_ACCOUNT_REQUIRED"):
		// In "Saved Messages", a reaction is a tag: Premium only.
		return i18n.T("reaction_premium")
	}
	return ""
}

// React sends my reaction on a message; emoji == "" removes my reaction. The
// state up to date also comes through OnMessageReactions (server, or echo of
// this RPC): EvReactions replaces the list, posting twice does nothing.
func (c *Client) React(ctx context.Context, chat *model.Chat, id int, e string) {
	e = emoji.Base(e) // Telegram refuses ❤️ (with a variation selector), not ❤
	go func() {
		defer c.Guard("React", nil)
		var reaction []tg.ReactionClass
		if e != "" {
			reaction = []tg.ReactionClass{&tg.ReactionEmoji{Emoticon: e}}
		}
		upd, err := c.api.MessagesSendReaction(ctx, &tg.MessagesSendReactionRequest{
			Peer: c.peer(chat), MsgID: id, Reaction: reaction})
		if err != nil {
			c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("react_error", err)})
			if r := reactionReason(err); r != "" {
				c.Post(model.EvReactionFailed{ChatID: chat.ID, ID: id, Emoji: e, Reason: r})
			}
			return
		}
		if u := reactionUpdate(upd); u != nil {
			c.Post(model.EvReactions{ChatID: chat.ID, ID: id, Reactions: reactionsOf(u.Reactions.Results)})
		}
		// The update does not always carry the state after (emoji removed,
		// reaction set from another client): the re-read of the message
		// decides.
		if m, err := c.getMessage(ctx, chat, id); err == nil {
			r, ok := m.GetReactions()
			if !ok {
				// Field missing (flag 20): the server says nothing about the
				// reactions, and above all not that there are none left. Posting
				// an empty list here would wipe the one of the update.
				c.Post(model.EvLog{Level: "DEBUG", Msg: i18n.T("react_refetch_missing", id)})
				return
			}
			c.Post(model.EvReactions{ChatID: chat.ID, ID: id, Reactions: reactionsOf(r.Results), Refetch: true})
		}
		// ponytail: no EvLog when reactionUpdate finds nothing here: upd has also
		// just gone through the hook.UpdateHook middleware (see New()), which feeds
		// it back to the same dispatcher → OnMessageReactions → EvReactions. If a
		// reaction ever stays visibly stuck, this is where the EvLog should be
		// added, not before.
	}()
}

// getMessage re-reads a message (reactions up to date, info). No peer is
// resolved: only the message counts.
func (c *Client) getMessage(ctx context.Context, chat *model.Chat, id int) (*tg.Message, error) {
	if !c.floodOK() {
		return nil, errFlood()
	}
	res, err := c.getMessages(ctx, chat, []tg.InputMessageClass{&tg.InputMessageID{ID: id}})
	if err != nil {
		return nil, err
	}
	mod, ok := res.AsModified()
	if ok {
		for _, mc := range mod.GetMessages() {
			if m, ok := mc.(*tg.Message); ok && m.ID == id {
				return m, nil
			}
		}
	}
	return nil, fmt.Errorf(i18n.T("message_not_found"), id)
}

// reactorsLimit : cap of messages.getMessageReactionsList, with no paging.
// ponytail: above that, the line shows the first 50 people who reacted; page
// only if somebody asks for the full list.
const reactorsLimit = 50

// reactors gives "who reacted", on one line. In a private chat the authors
// come from the message itself (me or the peer), with no network; elsewhere
// one single request, the names taken from its entities.
func (c *Client) reactors(ctx context.Context, p tg.InputPeerClass, kind model.ChatKind, title string, rs []model.Reaction, id int) string {
	if kind == model.ChatUser {
		out := make([]string, 0, len(rs))
		for _, r := range rs {
			who := title
			if r.Mine {
				who = i18n.T("me")
			}
			out = append(out, r.Emoji+" "+who)
		}
		return strings.Join(out, " · ")
	}
	if !c.floodOK() {
		return i18n.T("unavailable")
	}
	res, err := c.api.MessagesGetMessageReactionsList(ctx, &tg.MessagesGetMessageReactionsListRequest{
		Peer: p, ID: id, Limit: reactorsLimit})
	if err != nil {
		c.floodTrip(err)
		return i18n.T("unavailable")
	}
	ent := c.entities(res.Users)
	var order []string
	names := map[string][]string{}
	for _, r := range res.Reactions {
		e := emojiOf(r.Reaction)
		if e == "" {
			continue
		}
		if _, seen := names[e]; !seen {
			order = append(order, e)
		}
		who := c.nameOf(ent, r.PeerID)
		if r.My {
			who = i18n.T("me")
		}
		names[e] = append(names[e], who)
	}
	if len(order) == 0 {
		return i18n.T("unavailable") // channel with anonymous reactions, or nothing to show
	}
	out := make([]string, 0, len(order))
	for _, e := range order {
		out = append(out, e+" "+strings.Join(names[e], ", "))
	}
	return strings.Join(out, " · ")
}

// Info gives the information lines of a message (key "i"). readMax is the
// ReadOutboxMaxID of the chat (my messages), readInboxMax its ReadInboxMaxID
// (received messages) — only the UI keeps them up to date, and they are never
// read again from chat in this goroutine.
func (c *Client) Info(ctx context.Context, chat *model.Chat, id, readMax, readInboxMax int) {
	// Title and Kind change under remember() (UI goroutine): read here.
	title, kind := chat.Title, chat.Kind
	go func() {
		ev := model.EvInfo{ChatID: chat.ID, ID: id}
		fail := func(err string) {
			ev.Lines = []string{i18n.T("info_error", err)}
			c.Post(ev)
		}
		defer c.Guard("Info", fail)
		m, err := c.getMessage(ctx, chat, id)
		if err != nil {
			fail(err.Error())
			return
		}
		ev.Lines = append(ev.Lines, i18n.T("info_sent_at", i18n.LocalTime(unixTime(m.Date))))
		if d, ok := m.GetEditDate(); ok && d != 0 {
			ev.Lines = append(ev.Lines, i18n.T("info_edited_at", i18n.LocalTime(unixTime(d))))
		}
		if m.Out {
			if id <= readMax {
				ev.Lines = append(ev.Lines, i18n.T("info_read"))
			} else {
				ev.Lines = append(ev.Lines, i18n.T("info_sent"))
			}
			if kind == model.ChatGroup { // neither a private chat nor a channel
				ev.Lines = append(ev.Lines, i18n.T("info_read_by", c.readers(ctx, chat, id)))
			}
		} else {
			if id <= readInboxMax {
				ev.Lines = append(ev.Lines, i18n.T("info_read_by_me"))
			} else {
				ev.Lines = append(ev.Lines, i18n.T("info_unread"))
			}
			if v, ok := m.GetViews(); ok && v > 0 {
				ev.Lines = append(ev.Lines, i18n.T("info_views", v))
			}
		}
		if r, ok := m.GetReactions(); ok {
			if rs := reactionsOf(r.Results); len(rs) > 0 {
				ev.Lines = append(ev.Lines, i18n.T("info_reactions", c.reactors(ctx, c.peer(chat), kind, title, rs, id)))
			}
		}
		ev.Lines = append(ev.Lines, i18n.T("info_id", id)) // both branches
		c.Post(ev)
	}()
}

// readers gives the members who read the message, named from the entities
// already seen (never a network lookup: an active group would list dozens).
func (c *Client) readers(ctx context.Context, chat *model.Chat, id int) string {
	if !c.floodOK() {
		return i18n.T("unavailable")
	}
	parts, err := c.api.MessagesGetMessageReadParticipants(ctx,
		&tg.MessagesGetMessageReadParticipantsRequest{Peer: c.peer(chat), MsgID: id})
	if err != nil {
		c.floodTrip(err)
		return i18n.T("unavailable")
	}
	if len(parts) == 0 {
		return i18n.T("nobody")
	}
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		names = append(names, c.nameOf(peer.Entities{}, &tg.PeerUser{UserID: p.UserID}))
	}
	return strings.Join(names, ", ")
}

// WhoRead gives the "who read" line of the hover popup of the tick: read
// date in a private chat, names of the readers in a group, plain read state
// elsewhere. readMax is the ReadOutboxMaxID kept by the UI.
func (c *Client) WhoRead(ctx context.Context, chat *model.Chat, id, readMax int) {
	kind := chat.Kind // Kind changes under remember() (UI goroutine): read here
	go func() {
		defer c.Guard("WhoRead", nil)
		ev := model.EvWho{ChatID: chat.ID, ID: id}
		switch {
		case id > readMax:
			ev.Text = i18n.T("info_unread")
		case kind == model.ChatUser:
			ev.Text = i18n.T("info_read")
			if c.floodOK() {
				res, err := c.api.MessagesGetOutboxReadDate(ctx,
					&tg.MessagesGetOutboxReadDateRequest{Peer: c.peer(chat), MsgID: id})
				if err != nil {
					c.floodTrip(err) // privacy of the peer: the plain "read" stays
				} else {
					ev.Text = i18n.T("who_read_at", i18n.LocalTime(unixTime(res.Date)))
				}
			}
		case kind == model.ChatGroup:
			ev.Text = i18n.T("info_read_by", c.readers(ctx, chat, id))
		default: // channel: no reader list
			ev.Text = i18n.T("info_read")
		}
		c.Post(ev)
	}()
}

// WhoReacted gives the "who reacted" line of the hover popup of a reaction.
func (c *Client) WhoReacted(ctx context.Context, chat *model.Chat, id int, rs []model.Reaction) {
	kind, title := chat.Kind, chat.Title
	go func() {
		defer c.Guard("WhoReacted", nil)
		c.Post(model.EvWho{ChatID: chat.ID, ID: id, React: true,
			Text: c.reactors(ctx, c.peer(chat), kind, title, rs, id)})
	}()
}
