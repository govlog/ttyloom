package tgc

import (
	"slices"
	"testing"

	"github.com/gotd/td/tg"
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
