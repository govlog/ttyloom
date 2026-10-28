package dsc

import (
	"context"

	"github.com/diamondburned/arikawa/v3/discord"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// Pin pins (pin true) or unpins a message. The gateway answers with a
// CHANNEL_PINS_UPDATE, whose handler re-reads the pin list and posts EvPinned:
// nothing more to post here beyond the errors.
func (c *Client) Pin(ctx context.Context, chat *model.Chat, id int, pin bool) {
	go func() {
		defer c.Guard("Pin", nil)
		chID, _ := ids(chat)
		var err error
		if pin {
			err = c.rest(ctx).PinMessage(chID, discord.MessageID(id), "")
		} else {
			err = c.rest(ctx).UnpinMessage(chID, discord.MessageID(id), "")
		}
		if err != nil {
			c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("pin_error", err)})
		}
	}()
}

// fetchPins reads the pinned messages of a channel and posts the newest one
// (EvPinned, Fetched; Top nil with none): the banner of the UI needs it even
// when the pin is older than the loaded history. Called once per session per
// channel when its window opens (LoadHistory), and by the pins handler on
// every change.
func (c *Client) fetchPins(ctx context.Context, chatID int64, chID discord.ChannelID, guild discord.GuildID) {
	go func() {
		defer c.Guard("fetchPins", nil)
		ms, err := c.rest(ctx).PinnedMessages(chID)
		if err != nil {
			// DEBUG: a channel with no pin right is not an error worth window 0.
			c.Post(model.EvLog{Level: "DEBUG", Msg: "fetchPins: " + err.Error()})
			return
		}
		ev := model.EvPinned{ChatID: chatID, Fetched: true}
		top := -1
		for i := range ms {
			if top < 0 || ms[i].ID > ms[top].ID {
				top = i
			}
		}
		if top >= 0 {
			// A REST message carries no guild id: nameOf needs it (see history).
			ms[top].GuildID = guild
			m := c.msgOf(&ms[top])
			m.Pinned = true
			ev.Top = &m
		}
		c.Post(ev)
	}()
}
