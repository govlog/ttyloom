package tgc

import (
	"context"

	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// Pin pins (pin true) or unpins a message. The update the RPC gives back goes
// through the hook middleware (see New) and comes back as OnPinnedMessages →
// EvPinned: nothing more to post here.
func (c *Client) Pin(ctx context.Context, chat *model.Chat, id int, pin bool) {
	go func() {
		defer c.Guard("Pin", nil)
		_, err := c.api.MessagesUpdatePinnedMessage(ctx, &tg.MessagesUpdatePinnedMessageRequest{
			Peer: c.peer(chat), ID: id, Unpin: !pin})
		if err != nil {
			c.floodTrip(err)
			c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("pin_error", err)})
		}
	}()
}

// fetchPins reads the newest pinned message of the chat, once per session
// (messages.search, filter pinned): the banner of the UI needs it even when
// the pin is older than the loaded history. A bot cannot search: skipped, and
// not marked — a later login as a user takes the read back.
func (c *Client) fetchPins(ctx context.Context, chat *model.Chat) {
	if me := c.me.Load(); me == nil || me.Bot || c.pinFetched[chat.ID] {
		return
	}
	c.pinFetched[chat.ID] = true
	go func() {
		defer c.Guard("fetchPins", nil)
		ev := model.EvPinned{ChatID: chat.ID, Fetched: true}
		it := query.Messages(c.api).Search(c.peer(chat)).Filter(&tg.InputMessagesFilterPinned{}).BatchSize(1).Iter()
		msgs, err := c.page(ctx, chat, it, 1, 0)
		if err != nil {
			// DEBUG: a chat with no pin right is not an error worth window 0.
			c.Post(model.EvLog{Level: "DEBUG", Msg: "fetchPins: " + err.Error()})
			return
		}
		if len(msgs) > 0 {
			msgs[0].Pinned = true
			ev.Top = &msgs[0]
		}
		c.Post(ev)
	}()
}
