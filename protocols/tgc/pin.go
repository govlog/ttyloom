package tgc

import (
	"context"

	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

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
// not marked — a later login as a user takes the read back. A failed read
// drops the mark, so the next open of the chat reads again; a refusal of the
// server (no right to search) keeps it. This bounds the retries: at most one
// read per open of the window, no request while the FLOOD_WAIT breaker is
// open (c.each refuses the read), none after a refusal.
func (c *Client) fetchPins(ctx context.Context, chat *model.Chat) {
	if me := c.me.Load(); me == nil || me.Bot {
		return
	}
	c.mu.Lock()
	done := c.pinFetched[chat.ID]
	c.pinFetched[chat.ID] = true
	c.mu.Unlock()
	if done {
		return
	}
	go func() {
		defer c.Guard("fetchPins", nil)
		ev := model.EvPinned{ChatID: chat.ID, Fetched: true}
		it := query.Messages(c.api).Search(c.peer(chat)).Filter(&tg.InputMessagesFilterPinned{}).BatchSize(1).Iter()
		msgs, err := c.page(ctx, chat, it, 1, 0)
		if err != nil {
			_, flood := tgerr.AsFloodWait(err)
			if e, ok := tgerr.As(err); !ok || flood || e.Code < 400 || e.Code >= 500 {
				c.mu.Lock()
				delete(c.pinFetched, chat.ID)
				c.mu.Unlock()
			}
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
