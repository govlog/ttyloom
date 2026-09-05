package tgc

import (
	"context"
	"errors"
	"strconv"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// Actions of the sidebar context menu: leave, report/block, wipe the history.
// They all follow the same path — goroutine, FLOOD_WAIT breaker, error in
// window 0, event on success.

// chatAction : network call on a chat. ev is posted only on success.
func (c *Client) chatAction(action string, chat *model.Chat, ev model.Event, f func() error) {
	title := chat.Title // read here: the goroutine never touches the state of the UI
	name := i18n.T(action)
	go func() {
		defer c.Guard(name, nil)
		if !c.floodOK() {
			c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("chat_action_flood", name, errFlood())})
			return
		}
		if err := f(); err != nil {
			c.floodTrip(err)
			c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("chat_action_error", name, title, err)})
			return
		}
		c.Post(ev)
	}()
}

// inputChannel gives the InputChannel of a channel or supergroup peer.
func inputChannel(p *tg.InputPeerChannel) *tg.InputChannel {
	return &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash}
}

// Leave leaves a room: a basic group (InputPeerChat) through
// messages.deleteChatUser(self), a supergroup or a channel through channels.leaveChannel.
func (c *Client) Leave(ctx context.Context, chat *model.Chat) {
	peer := c.peer(chat) // same: no field of the chat is read from the goroutine any more
	c.chatAction("action_leave", chat, model.EvChatGone{ChatID: chat.ID}, func() error {
		switch p := peer.(type) {
		case *tg.InputPeerChannel:
			_, err := c.api.ChannelsLeaveChannel(ctx, inputChannel(p))
			return err
		case *tg.InputPeerChat:
			_, err := c.api.MessagesDeleteChatUser(ctx, &tg.MessagesDeleteChatUserRequest{
				ChatID: p.ChatID, UserID: &tg.InputUserSelf{}})
			return err
		}
		return errors.New(i18n.T("not_group_nor_channel"))
	})
}

// Block blocks the peer and reports it as spam. The report comes second: its
// failure does not fail the block, which is what the user asked for.
func (c *Client) Block(ctx context.Context, chat *model.Chat) {
	peer := c.peer(chat) // same: no field of the chat is read from the goroutine any more
	c.chatAction("action_block", chat, model.EvChatGone{ChatID: chat.ID}, func() error {
		if _, err := c.api.ContactsBlock(ctx, &tg.ContactsBlockRequest{ID: peer}); err != nil {
			return err
		}
		_, _ = c.api.MessagesReportSpam(ctx, peer)
		return nil
	})
}

// DeleteChat deletes the chat: the dialog leaves the list. Private chat and
// basic group: messages.deleteHistory, MaxID 0 = the whole history and
// just_clear false also drops the dialog. Supergroup and channel:
// channels.deleteHistory only empties the history, and only
// channels.leaveChannel drops the dialog.
func (c *Client) DeleteChat(ctx context.Context, chat *model.Chat) {
	peer := c.peer(chat) // same: no field of the chat is read from the goroutine any more
	c.chatAction("action_delete", chat, model.EvChatGone{ChatID: chat.ID}, func() error {
		if p, ok := peer.(*tg.InputPeerChannel); ok {
			_, err := c.api.ChannelsLeaveChannel(ctx, inputChannel(p))
			return err
		}
		_, err := c.api.MessagesDeleteHistory(ctx, &tg.MessagesDeleteHistoryRequest{Peer: peer})
		return err
	})
}

// memberPeer : peer of a token of the member box — a TDLib id already seen in
// the session (no network, the members go through rememberAll), else an @name
// resolved by the network. Same rule as Resolve, without a window.
func (c *Client) memberPeer(ctx context.Context, token string) (peers.Peer, error) {
	if id, err := strconv.ParseInt(token, 10, 64); err == nil {
		c.mu.Lock()
		p, ok := c.seen[id]
		c.mu.Unlock()
		if ok {
			return p, nil
		}
	}
	if !c.floodOK() {
		return nil, errFlood()
	}
	p, err := c.peers.Resolve(ctx, token)
	if err != nil {
		c.floodTrip(err)
		return nil, err
	}
	return c.remember(p), nil
}

// BlockMember blocks and reports a member of the member box, whether a
// private chat is open with them or not. On success: EvChatGone on their id,
// which drops the private chat from the list when there was one.
func (c *Client) BlockMember(ctx context.Context, token string) {
	go func() {
		defer c.Guard("BlockMember", nil)
		p, err := c.memberPeer(ctx, token)
		if err == nil {
			_, err = c.api.ContactsBlock(ctx, &tg.ContactsBlockRequest{ID: p.InputPeer()})
		}
		if err != nil {
			c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("block_member_error", token, err)})
			return
		}
		_, _ = c.api.MessagesReportSpam(ctx, p.InputPeer()) // second, like Block
		c.Post(model.EvChatGone{ChatID: int64(p.TDLibPeerID())})
	}()
}

// WhoisMember gives the profile of a member of the member box, with no
// private chat open with them.
func (c *Client) WhoisMember(ctx context.Context, token string) {
	go func() {
		defer c.Guard("WhoisMember", func(err string) { c.Post(model.EvWhois{Err: err}) })
		p, err := c.memberPeer(ctx, token)
		if err != nil {
			c.Post(model.EvWhois{Err: err.Error()})
			return
		}
		c.Whois(ctx, c.chatOf(p))
	}()
}
