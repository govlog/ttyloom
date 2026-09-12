package dsc

import (
	"slices"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/ningen/v3"

	"github.com/govlog/ttyloom/internal/model"
)

// Gateway → model.Event. Every handler is registered synchronous: ningen
// calls them one after the other, in the order the gateway sent the events,
// and that order is the one the messages have to reach the UI in.
//
// The price is a hard rule: a handler never waits on the network. The op
// channel of the gateway holds one element, and the heartbeat shares the
// select that feeds it (utils/ws/gateway.go:238 and 321-352) — so a handler
// that blocks blocks the heartbeat too, and a UI frozen for longer than the
// interval Discord gives (~41 s) costs a reconnect and a Resume. That is
// self-healing but it is not tgc, where the same jam only slows the updates
// down. The one wait taken here is the send on the event channel, which main
// buffers to 256; everything else reads from the cache of the state.
//
// Each one is guarded: without it a panic would take the reading loop of the
// gateway down with it, and the UI would wait for a terminal event that never
// comes.
func (c *Client) wire() {
	st := c.state()
	st.AddSyncHandler(func(e *gateway.ReadyEvent) {
		defer c.Guard("Ready", nil)
		// Held before any message of this connection: msgOf tells our own
		// messages apart by this id.
		c.self.Store(int64(e.User.ID))
	})
	st.AddSyncHandler(func(e *gateway.MessageCreateEvent) {
		defer c.Guard("MessageCreate", nil)
		// Chat never nil: the UI lists the chat of a message it does not know.
		c.Post(model.EvNewMessage{Msg: c.msgOf(&e.Message), Chat: c.chatFor(e.ChannelID)})
	})
	st.AddSyncHandler(func(e *gateway.MessageUpdateEvent) {
		defer c.Guard("MessageUpdate", nil)
		// An update carries only what changed (an embed showing up on a link,
		// often with no text at all). The state has already merged it into the
		// message it keeps: that copy is the whole one.
		m, err := st.Cabinet.Message(e.ChannelID, e.ID)
		if err != nil {
			if e.Content == "" {
				return // partial update of a message we do not hold: nothing to say
			}
			m = &e.Message
		}
		c.Post(model.EvEditMessage{Msg: c.msgOf(m)})
	})
	st.AddSyncHandler(func(e *gateway.MessageDeleteEvent) {
		defer c.Guard("MessageDelete", nil)
		c.Post(model.EvDeleted{ChatID: int64(e.ChannelID), IDs: []int{int(e.ID)}})
	})
	st.AddSyncHandler(func(e *gateway.MessageDeleteBulkEvent) {
		defer c.Guard("MessageDeleteBulk", nil)
		ids := make([]int, 0, len(e.IDs))
		for _, id := range e.IDs {
			ids = append(ids, int(id))
		}
		c.Post(model.EvDeleted{ChatID: int64(e.ChannelID), IDs: ids})
	})
	st.AddSyncHandler(func(e *gateway.TypingStartEvent) {
		defer c.Guard("TypingStart", nil)
		// No end event is needed: the UI drops a typing line after six seconds.
		c.Post(model.EvTyping{ChatID: int64(e.ChannelID), Who: c.typist(e)})
	})
	st.AddSyncHandler(func(e *gateway.PresenceUpdateEvent) {
		defer c.Guard("PresenceUpdate", nil)
		// The UI keys presences by chat id, and the chat of a user is the DM
		// channel. With no DM open the presence has nowhere to show.
		dm, err := st.Cabinet.CreatePrivateChannel(e.User.ID) // a cache read, no call
		if err != nil {
			return
		}
		c.Post(model.EvPresence{UserID: int64(dm.ID), Status: presenceOf(e.Status)})
	})
	st.AddSyncHandler(func(e *gateway.MessageReactionAddEvent) {
		defer c.Guard("MessageReactionAdd", nil)
		c.reactions(e.ChannelID, e.MessageID)
	})
	st.AddSyncHandler(func(e *gateway.MessageReactionRemoveEvent) {
		defer c.Guard("MessageReactionRemove", nil)
		c.reactions(e.ChannelID, e.MessageID)
	})
	st.AddSyncHandler(func(e *gateway.MessageAckEvent) {
		defer c.Guard("MessageAck", nil)
		// An ack, ours or from another device, means read up to there: the
		// unread count of the channel goes back to zero with it.
		c.Post(model.EvReadInbox{ChatID: int64(e.ChannelID), MaxID: int(e.MessageID), HasUnread: true})
	})
	st.AddSyncHandler(func(e *gateway.GuildDeleteEvent) {
		defer c.Guard("GuildDelete", nil)
		c.guildGone(e)
	})
	st.AddSyncHandler(func(e *gateway.ChannelDeleteEvent) {
		defer c.Guard("ChannelDelete", nil)
		// Closed DM as well as deleted channel. A channel of a type the
		// sidebar never listed costs nothing: the UI drops an unknown chat
		// without a word.
		c.Post(model.EvChatGone{ChatID: int64(e.ID)})
	})
	// Ready and Resumed both give a ConnectedEvent: the indicator comes back
	// on its own after a reconnection, and EvReady stays posted by Run.
	st.AddSyncHandler(func(*ningen.ConnectedEvent) {
		defer c.Guard("Connected", nil)
		c.Post(model.EvConnected{})
	})
	st.AddSyncHandler(func(*ningen.DisconnectedEvent) {
		defer c.Guard("Disconnected", nil)
		c.Post(model.EvDisconnected{})
	})
}

// guildGone : guild left, deleted, or the account kicked out of it — every
// channel of it leaves the sidebar. Unavailable is an outage on the Discord
// side, not a departure: the guild comes back on its own and nothing is
// dropped.
//
// The line of the guild itself is already gone when we are called (onEvent
// runs before the handlers, state/state_events.go:19 and 137); its channels
// are not, arikawa never drops those on a guild delete. The cache alone is
// read, never the network — the rule of every handler of this file.
func (c *Client) guildGone(e *gateway.GuildDeleteEvent) {
	if e.Unavailable {
		return
	}
	chs, err := c.state().Cabinet.Channels(e.ID)
	if err != nil {
		return // guild never cached: nothing was listed from it either
	}
	for _, ch := range chs {
		if slices.Contains(textChannels, ch.Type) { // the filter of LoadDialogs
			c.Post(model.EvChatGone{ChatID: int64(ch.ID)})
		}
	}
}

// chatFor : the chat a gateway event belongs to. Never nil — the UI takes it
// as the entry to create for a chat it has never seen. A channel missing from
// the cache leaves an entry with its id for a title rather than nothing.
func (c *Client) chatFor(id discord.ChannelID) *model.Chat {
	ch, err := c.state().Cabinet.Channel(id)
	if err != nil {
		return &model.Chat{ID: int64(id), Kind: model.ChatGroup,
			Peer: peer{Channel: uint64(id)}, Title: "#" + id.String()}
	}
	chat := chatOf(ch, c.guildName(ch.GuildID))
	chat.Customs, chat.CustomLocs = c.customsOf(ch.GuildID)
	return chat
}

// customsOf : the custom emojis of a guild as ":name:", and the image of each
// one (".gif" when animated) as a download handle; none for a DM or a guild
// not cached.
func (c *Client) customsOf(guild discord.GuildID) ([]string, map[string]any) {
	if !guild.IsValid() {
		return nil, nil
	}
	es, err := c.state().Cabinet.Emojis(guild)
	if err != nil {
		return nil, nil
	}
	out := make([]string, 0, len(es))
	locs := make(map[string]any, len(es))
	for _, e := range es {
		out = append(out, ":"+e.Name+":")
		locs[":"+e.Name+":"] = fileURL(e.EmojiURL())
	}
	return out, locs
}

// guildName : name of a guild from the cache, "" when it is not known — the
// title of the channel then goes without its prefix.
func (c *Client) guildName(id discord.GuildID) string {
	if !id.IsValid() {
		return ""
	}
	if g, err := c.state().Cabinet.Guild(id); err == nil {
		return g.Name
	}
	return ""
}

// typist : name to show in the "… typing" line. A guild event carries the
// member; a DM carries none, and the recipients of the channel are then the
// name to read — a presence is only ever there for a friend, and a DM from
// anyone else would show a bare id.
func (c *Client) typist(e *gateway.TypingStartEvent) string {
	if e.Member != nil {
		if e.Member.Nick != "" {
			return e.Member.Nick
		}
		return e.Member.User.DisplayOrUsername()
	}
	if ch, err := c.state().Cabinet.Channel(e.ChannelID); err == nil {
		for _, u := range ch.DMRecipients {
			if u.ID == e.UserID {
				return u.DisplayOrUsername()
			}
		}
	}
	if p, err := c.state().Cabinet.Presence(0, e.UserID); err == nil {
		return p.User.DisplayOrUsername()
	}
	return e.UserID.String()
}

// reactions posts the whole reaction list of a message again: the gateway
// sends one add or one remove at a time, the UI shows the set. Read from the
// cache and posted here, in the handler: the state applied the change just
// before it was called, so the list is the right one, and an add followed by a
// remove cannot land in the wrong order. A message the cache does not hold is
// left alone — a REST read per reaction would be neither sober nor ordered.
func (c *Client) reactions(chID discord.ChannelID, id discord.MessageID) {
	m, err := c.state().Cabinet.Message(chID, id)
	if err != nil {
		return
	}
	// Refetch stays false: this list is the one the state holds after the
	// update, so an empty one really means the last reaction is gone.
	c.Post(model.EvReactions{ChatID: int64(chID), ID: int(id), Reactions: reactionsOf(m.Reactions)})
}
