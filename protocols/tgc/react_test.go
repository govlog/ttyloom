package tgc

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// Only the plain emoji reactions of the most used list can be offered: a
// custom emoji (Premium) or a paid star is no emoji to pick. The variation
// selector goes, as for the list of the server.
func TestTopEmojis(t *testing.T) {
	res := &tg.MessagesReactions{Reactions: []tg.ReactionClass{
		&tg.ReactionEmoji{Emoticon: "❤️"},
		&tg.ReactionCustomEmoji{DocumentID: 1},
		&tg.ReactionPaid{},
		&tg.ReactionEmoji{Emoticon: "🤣"},
	}}
	if got := topEmojis(res); !slices.Equal(got, []string{"❤", "🤣"}) {
		t.Fatalf("topEmojis = %q, want [❤ 🤣]", got)
	}
	if got := topEmojis(&tg.MessagesReactionsNotModified{}); got != nil {
		t.Fatalf("not modified: %q, want nil", got)
	}
}

// TestWhoReactedBothInPrivate : in a private chat both people react with the
// same emoji; the server sends one count of 2 marked chosen. The popup names
// both, with no request.
func TestWhoReactedBothInPrivate(t *testing.T) {
	ev := make(chan model.Event, 1)
	c := fakeClient(&fakeInvoker{}, ev)
	chat := &model.Chat{ID: 7, Kind: model.ChatUser, Title: "alice", Peer: &tg.InputPeerUser{UserID: 7}}
	c.WhoReacted(context.Background(), chat, 5, []model.Reaction{{Emoji: "❤", Count: 2, Mine: true}, {Emoji: "👍", Count: 1, Mine: true}})
	if w := next(t, ev).(model.EvWho); w.Text != "❤ "+i18n.T("me")+", alice · 👍 "+i18n.T("me") {
		t.Fatalf("who reacted: %q, want the peer named beside me on the count of 2", w.Text)
	}
}

// TestWhoReactedAsChannel : in a group, a member can react as a channel
// (send as). The channel comes in the chats of the answer and is named.
func TestWhoReactedAsChannel(t *testing.T) {
	ev := make(chan model.Event, 1)
	thumb := &tg.ReactionEmoji{Emoticon: "👍"}
	inv := &fakeInvoker{answer: func(req bin.Encoder) (any, error) {
		if _, ok := req.(*tg.MessagesGetMessageReactionsListRequest); !ok {
			return nil, fmt.Errorf("unexpected request %T", req)
		}
		return &tg.MessagesMessageReactionsList{Count: 2, Reactions: []tg.MessagePeerReaction{
			{PeerID: &tg.PeerChannel{ChannelID: 55}, Reaction: thumb},
			{PeerID: &tg.PeerUser{UserID: 9}, Reaction: thumb},
		}, Chats: []tg.ChatClass{&tg.Channel{ID: 55, Title: "News"}}, Users: []tg.UserClass{withName(9, "bob")}}, nil
	}}
	c := fakeClient(inv, ev)
	group := &model.Chat{ID: -1000000000123, Kind: model.ChatGroup, Peer: &tg.InputPeerChannel{ChannelID: 123}}
	c.WhoReacted(context.Background(), group, 5, []model.Reaction{{Emoji: "👍", Count: 2}})
	if w := next(t, ev).(model.EvWho); w.Text != "👍 News, bob" {
		t.Fatalf("who reacted: %q, want the channel named from the chats of the answer", w.Text)
	}
}
