package tgc

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/log"
	"github.com/gotd/td/constant"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/model"
)

// --- gotd logs → debug window (ERROR also in window 0) ---

type logger struct{ c *Client }

func (l logger) Enabled(_ context.Context, lvl log.Level) bool { return lvl >= log.LevelWarn }

func (l logger) Log(ctx context.Context, lvl log.Level, msg string, attrs ...log.Attr) {
	if !l.Enabled(ctx, lvl) { // log.Helper calls Log without filtering
		return
	}
	var b strings.Builder
	b.WriteString(msg)
	for _, a := range attrs {
		fmt.Fprintf(&b, " %s=%s", a.Key, a.Value.String())
	}
	l.c.PostNB(model.EvLog{Level: lvl.String(), Msg: b.String()})
}

// --- updates ---

// safe : a panic in an update handler (remote data) becomes an EvLog, never a crash.
func (c *Client) safe(name string, f func() error) (err error) {
	defer c.Guard(name, nil)
	return f()
}

func (c *Client) registerHandlers(d tg.UpdateDispatcher) {
	d.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		return c.safe("OnNewMessage", func() error { return c.onMessage(ctx, u.Message, peer.EntitiesFromUpdate(e), false) })
	})
	d.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewChannelMessage) error {
		return c.safe("OnNewChannelMessage", func() error { return c.onMessage(ctx, u.Message, peer.EntitiesFromUpdate(e), false) })
	})
	d.OnEditMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateEditMessage) error {
		return c.safe("OnEditMessage", func() error { return c.onMessage(ctx, u.Message, peer.EntitiesFromUpdate(e), true) })
	})
	d.OnEditChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateEditChannelMessage) error {
		return c.safe("OnEditChannelMessage", func() error { return c.onMessage(ctx, u.Message, peer.EntitiesFromUpdate(e), true) })
	})
	d.OnDeleteMessages(func(_ context.Context, _ tg.Entities, u *tg.UpdateDeleteMessages) error {
		return c.safe("OnDeleteMessages", func() error {
			c.Post(model.EvDeleted{IDs: u.Messages})
			return nil
		})
	})
	d.OnDeleteChannelMessages(func(_ context.Context, _ tg.Entities, u *tg.UpdateDeleteChannelMessages) error {
		return c.safe("OnDeleteChannelMessages", func() error {
			var id constant.TDLibPeerID
			id.Channel(u.ChannelID)
			c.Post(model.EvDeleted{ChatID: int64(id), IDs: u.Messages})
			return nil
		})
	})
	d.OnUserTyping(func(_ context.Context, e tg.Entities, u *tg.UpdateUserTyping) error {
		return c.safe("OnUserTyping", func() error {
			if !composing(u.Action) {
				return nil
			}
			var id constant.TDLibPeerID
			id.User(u.UserID)
			c.Post(model.EvTyping{ChatID: int64(id), Who: c.nameOf(peer.EntitiesFromUpdate(e), &tg.PeerUser{UserID: u.UserID})})
			return nil
		})
	})
	d.OnChatUserTyping(func(_ context.Context, e tg.Entities, u *tg.UpdateChatUserTyping) error {
		return c.safe("OnChatUserTyping", func() error {
			if !composing(u.Action) {
				return nil
			}
			var id constant.TDLibPeerID
			id.Chat(u.ChatID)
			c.Post(model.EvTyping{ChatID: int64(id), Who: c.nameOf(peer.EntitiesFromUpdate(e), u.FromID)})
			return nil
		})
	})
	d.OnChannelUserTyping(func(_ context.Context, e tg.Entities, u *tg.UpdateChannelUserTyping) error {
		return c.safe("OnChannelUserTyping", func() error {
			if !composing(u.Action) {
				return nil
			}
			var id constant.TDLibPeerID
			id.Channel(u.ChannelID)
			c.Post(model.EvTyping{ChatID: int64(id), Who: c.nameOf(peer.EntitiesFromUpdate(e), u.FromID)})
			return nil
		})
	})
	d.OnUserStatus(func(_ context.Context, _ tg.Entities, u *tg.UpdateUserStatus) error {
		return c.safe("OnUserStatus", func() error {
			c.Post(model.EvPresence{UserID: u.UserID, Status: formatStatus(u.Status, time.Now())})
			return nil
		})
	})
	d.OnReadHistoryOutbox(func(_ context.Context, _ tg.Entities, u *tg.UpdateReadHistoryOutbox) error {
		return c.safe("OnReadHistoryOutbox", func() error {
			c.Post(model.EvReadOutbox{ChatID: tdlibID(u.Peer), MaxID: u.MaxID})
			return nil
		})
	})
	d.OnReadChannelOutbox(func(_ context.Context, _ tg.Entities, u *tg.UpdateReadChannelOutbox) error {
		return c.safe("OnReadChannelOutbox", func() error {
			var id constant.TDLibPeerID
			id.Channel(u.ChannelID)
			c.Post(model.EvReadOutbox{ChatID: int64(id), MaxID: u.MaxID})
			return nil
		})
	})
	d.OnReadHistoryInbox(func(_ context.Context, _ tg.Entities, u *tg.UpdateReadHistoryInbox) error {
		return c.safe("OnReadHistoryInbox", func() error {
			c.Post(model.EvReadInbox{ChatID: tdlibID(u.Peer), MaxID: u.MaxID,
				Unread: u.StillUnreadCount, HasUnread: true})
			return nil
		})
	})
	d.OnReadChannelInbox(func(_ context.Context, _ tg.Entities, u *tg.UpdateReadChannelInbox) error {
		return c.safe("OnReadChannelInbox", func() error {
			var id constant.TDLibPeerID
			id.Channel(u.ChannelID)
			c.Post(model.EvReadInbox{ChatID: int64(id), MaxID: u.MaxID,
				Unread: u.StillUnreadCount, HasUnread: true})
			return nil
		})
	})
	d.OnMessageReactions(func(_ context.Context, e tg.Entities, u *tg.UpdateMessageReactions) error {
		return c.safe("OnMessageReactions", func() error {
			// The TDLib id is computed without resolving the peer; when the chat is
			// not open, the UI simply ignores the event.
			ev := model.EvReactions{ChatID: tdlibID(u.Peer), ID: u.MsgID, Reactions: reactionsOf(u.Reactions.Results)}
			ev.New, ev.By = c.newReaction(peer.EntitiesFromUpdate(e), u.Reactions.RecentReactions)
			c.Post(ev)
			return nil
		})
	})
}

// composing : the actions that mean "is writing something" (text, a voice
// note, a video, a sticker…). The same updates also carry a cancel (the draft
// was cleared), a voice-chat speaker, emoji taps and bot drafts: none of them
// is typing.
func composing(a tg.SendMessageActionClass) bool {
	switch a.(type) {
	case *tg.SendMessageCancelAction, *tg.SpeakingInGroupCallAction, *tg.SendMessageEmojiInteraction,
		*tg.SendMessageEmojiInteractionSeen, *tg.SendMessageHistoryImportAction,
		*tg.SendMessageTextDraftAction, *tg.SendMessageRichMessageDraftAction, *tg.SendMessageStopDraftAction:
		return false
	}
	return true
}

func (c *Client) onMessage(ctx context.Context, mc tg.MessageClass, ent peer.Entities, edit bool) error {
	c.rememberAll(ent)
	m, chat, ok := c.convert(ctx, mc, ent, nil)
	if !ok {
		return nil
	}
	// The quote is read after the post, off the update loop: a getMessages per
	// reply there held every update behind it, for up to the 15 s of a
	// FLOOD_WAIT the middleware sleeps.
	fill := m.Reply != nil && m.Reply.Text == "" && c.floodOK()
	k := [2]int64{chat.ID, int64(m.ID)}
	c.mu.Lock()
	c.fillSeq++
	seq := c.fillSeq
	delete(c.fills, k) // this version replaces any older one: its read posts nothing
	if fill {
		c.fills[k] = seq
	}
	c.mu.Unlock()
	if edit {
		c.Post(model.EvEditMessage{Msg: m})
	} else {
		c.Post(model.EvNewMessage{Msg: m, Chat: chat})
	}
	if fill {
		q := *m.Reply // a quote of its own: the UI holds the one posted
		m.Reply = &q
		cc := *chat // the UI writes the chat it was posted with
		go c.fillLater(ctx, &cc, m, k, seq)
	}
	return nil
}

// fillLater reads the quote of m and posts it alone, unless a newer version
// of m came meanwhile. Two reads at a time: a burst of replies after a
// reconnection must not fire a request per reply at once.
func (c *Client) fillLater(ctx context.Context, chat *model.Chat, m model.Msg, k [2]int64, seq uint64) {
	defer c.Guard("fillLater", nil)
	select {
	case c.fillSem <- struct{}{}:
		defer func() { <-c.fillSem }()
	case <-ctx.Done():
		return
	}
	c.fillReplies(ctx, chat, []*model.Msg{&m})
	c.mu.Lock()
	last := c.fills[k] == seq
	if last {
		delete(c.fills, k)
	}
	c.mu.Unlock()
	if last && m.Reply.Text != "" {
		c.Post(model.EvQuote{ChatID: chat.ID, ID: m.ID, Quote: *m.Reply})
	}
}
