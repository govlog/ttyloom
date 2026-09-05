package tgc

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// Members of the F3 box. One network call per opening (two for a channel: the
// counters then the admins); the names come from the entities of the answer,
// never from a lookup per member — 200 users.getUsers fire a FLOOD_WAIT at
// once. Private chats do not come through here: the UI already has the name
// and the presence.

// partsLimit : cap of channels.getParticipants, with no paging.
// ponytail: above that, the box shows the first 200 recent members; add an
// offset only if somebody asks for the full list.
const partsLimit = 200

// errNoParts : built at each read, the language can change during a session.
func errNoParts() error { return errors.New(i18n.T("no_member_list")) }

func (c *Client) Participants(ctx context.Context, chat *model.Chat) {
	kind := chat.Kind // captured outside the goroutine: the UI writes it again in remember()
	go func() {
		ev := model.EvParticipants{ChatID: chat.ID}
		defer c.Guard("Participants", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		if !c.floodOK() {
			ev.Err = errFlood().Error()
			c.Post(ev)
			return
		}
		lines, err := c.parts(ctx, chat, kind)
		if err != nil {
			c.floodTrip(err)
			ev.Err = err.Error()
			c.Post(ev)
			return
		}
		ev.Lines = lines
		c.Post(ev)
	}()
}

// parts dispatches by kind of peer. Basic group → messages.getFullChat;
// supergroup → channels.getParticipants; channel → channels.getFullChannel
// (counters) then its admins when they can be reached.
func (c *Client) parts(ctx context.Context, chat *model.Chat, kind model.ChatKind) ([]model.Participant, error) {
	switch p := c.peer(chat).(type) {
	case *tg.InputPeerChat:
		full, err := c.api.MessagesGetFullChat(ctx, p.ChatID)
		if err != nil {
			return nil, err
		}
		ent := c.entities(full.Users)
		cf, ok := full.FullChat.(*tg.ChatFull)
		if !ok {
			return nil, errNoParts()
		}
		ar, _ := cf.GetAvailableReactions() // field missing: nil interface, postReactions ignores it
		c.postReactions(chat.ID, ar)
		ps, ok := cf.Participants.(*tg.ChatParticipants)
		if !ok {
			return nil, errNoParts() // chatParticipantsForbidden: member gone from the group
		}
		ids, admins := chatParts(ps.Participants)
		return c.lines(ids, admins, ent), nil
	case *tg.InputPeerChannel:
		ch := &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash}
		if kind == model.ChatChannel {
			return c.channelParts(ctx, chat.ID, ch)
		}
		ids, admins, ent, err := c.channelMembers(ctx, ch, &tg.ChannelParticipantsRecent{})
		if err != nil {
			return nil, err
		}
		return c.lines(ids, admins, ent), nil
	}
	return nil, errNoParts()
}

// channelParts : a broadcast channel does not show its subscribers, only
// their number. The admins are a bonus: their refusal is not an error.
func (c *Client) channelParts(ctx context.Context, id int64, ch *tg.InputChannel) ([]model.Participant, error) {
	full, err := c.api.ChannelsGetFullChannel(ctx, ch)
	if err != nil {
		return nil, err
	}
	cf, ok := full.FullChat.(*tg.ChannelFull)
	if !ok {
		return nil, errNoParts()
	}
	ar, _ := cf.GetAvailableReactions()
	c.postReactions(id, ar)
	var out []model.Participant
	if n, ok := cf.GetParticipantsCount(); ok {
		out = append(out, model.Participant{Text: i18n.T(pluralKey(n, "subscribers"), n)})
	}
	if n, ok := cf.GetAdminsCount(); ok {
		out = append(out, model.Participant{Text: i18n.T(pluralKey(n, "admins"), n)})
	}
	ids, admins, ent, err := c.channelMembers(ctx, ch, &tg.ChannelParticipantsAdmins{})
	if err != nil {
		c.floodTrip(err) // most often an access refusal, but a FLOOD_WAIT must open the breaker
		return out, nil
	}
	return append(out, c.lines(ids, admins, ent)...), nil
}

// postReactions : reaction restriction carried by a full record. It comes
// only with the member box (F3): no network call is added for it. r nil
// (field missing) = nothing to change.
func (c *Client) postReactions(id int64, r tg.ChatReactionsClass) {
	switch v := r.(type) {
	case *tg.ChatReactionsAll:
		c.Post(model.EvChatReactions{ChatID: id}) // no restriction left
	case *tg.ChatReactionsNone:
		c.Post(model.EvChatReactions{ChatID: id, None: true})
	case *tg.ChatReactionsSome:
		em := make([]string, 0, len(v.Reactions))
		for _, rc := range v.Reactions {
			// The custom (premium) ones cannot be set by emoticon: ignored.
			if e, ok := rc.(*tg.ReactionEmoji); ok {
				em = append(em, e.Emoticon)
			}
		}
		c.Post(model.EvChatReactions{ChatID: id, Emojis: em})
	}
}

// channelMembers : one page of channels.getParticipants, entities kept.
func (c *Client) channelMembers(ctx context.Context, ch *tg.InputChannel, f tg.ChannelParticipantsFilterClass) ([]int64, map[int64]bool, peer.Entities, error) {
	res, err := c.api.ChannelsGetParticipants(ctx, &tg.ChannelsGetParticipantsRequest{
		Channel: ch, Filter: f, Limit: partsLimit})
	if err != nil {
		return nil, nil, peer.Entities{}, err
	}
	cp, ok := res.(*tg.ChannelsChannelParticipants)
	if !ok {
		return nil, nil, peer.Entities{}, errNoParts()
	}
	ids, admins := channelParts(cp.Participants)
	return ids, admins, c.entities(cp.Users), nil
}

// entities keeps the users of an answer on the way — a click on a member with
// no @name then resolves them by id, with no network.
func (c *Client) entities(users []tg.UserClass) peer.Entities {
	ent := peer.NewEntities(tg.UserClassArray(users).UserToMap(), nil, nil)
	c.rememberAll(ent)
	return ent
}

// lines formats the members, names resolved in the entities of that batch only.
func (c *Client) lines(ids []int64, admins map[int64]bool, ent peer.Entities) []model.Participant {
	var me int64
	if c.me != nil {
		me = c.me.ID
	}
	return participantLines(ids, admins, ent.Users(), me, time.Now(), func(id int64) string {
		return c.nameOf(ent, &tg.PeerUser{UserID: id})
	})
}

// chatParts gives the ids and admins of a basic group, in server order.
func chatParts(ps []tg.ChatParticipantClass) ([]int64, map[int64]bool) {
	ids, admins := make([]int64, 0, len(ps)), map[int64]bool{}
	for _, p := range ps {
		switch v := p.(type) {
		case *tg.ChatParticipantCreator:
			ids, admins[v.UserID] = append(ids, v.UserID), true
		case *tg.ChatParticipantAdmin:
			ids, admins[v.UserID] = append(ids, v.UserID), true
		case *tg.ChatParticipant:
			ids = append(ids, v.UserID)
		}
	}
	return ids, admins
}

// channelParts does the same for a supergroup or a channel. The banned and
// the gone have no user id: the filters used do not give them back.
func channelParts(ps []tg.ChannelParticipantClass) ([]int64, map[int64]bool) {
	ids, admins := make([]int64, 0, len(ps)), map[int64]bool{}
	for _, p := range ps {
		switch v := p.(type) {
		case *tg.ChannelParticipantCreator:
			ids, admins[v.UserID] = append(ids, v.UserID), true
		case *tg.ChannelParticipantAdmin:
			ids, admins[v.UserID] = append(ids, v.UserID), true
		case *tg.ChannelParticipantSelf:
			ids = append(ids, v.UserID)
		case *tg.ChannelParticipant:
			ids = append(ids, v.UserID)
		}
	}
	return ids, admins
}

// participantLines gives one line per member, creator and admins first with
// ★, the server order kept inside each group. name resolves an id with no
// network; users carries the statuses of the answer, with the online TTL.
func participantLines(ids []int64, admins map[int64]bool, users map[int64]*tg.User, me int64, now time.Time, name func(int64) string) []model.Participant {
	var head, tail []model.Participant
	for _, id := range ids {
		p := model.Participant{Text: name(id), Query: strconv.FormatInt(id, 10)}
		self := me != 0 && id == me
		if u := users[id]; u != nil {
			if u.Username != "" {
				p.Query = "@" + u.Username
			}
			if st, ok := u.Status.(*tg.UserStatusOnline); ok {
				p.Online = now.Before(unixTime(st.Expires)) // Expires is a TTL, see formatStatus
			}
			self = self || u.Self
		}
		if self {
			p.Text += i18n.T("member_me_suffix")
		}
		if admins[id] {
			p.Text = "★ " + p.Text
			head = append(head, p)
			continue
		}
		tail = append(tail, p)
	}
	return append(head, tail...)
}

// pluralKey : i18n key, singular or plural, for a count.
func pluralKey(n int, base string) string { return i18n.Plural(n, base) }
