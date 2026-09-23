package ui

import (
	"testing"

	"github.com/govlog/ttyloom/internal/model"
)

// A reaction to one of my messages, in the window shown while the terminal
// has the focus, is read on the server at once (the phone drops its badge); a
// reaction to someone else's message reads nothing.
func TestReactionOnMyMessageIsReadAtOnce(t *testing.T) {
	u, b := gifUI()
	u.ws.Cur, u.focused, u.dispatchNet = 1, true, netTelegram
	w := u.ws.List[1]
	mine := &model.Msg{Net: netTelegram, ChatID: 1, ID: 5, Out: true}
	theirs := &model.Msg{Net: netTelegram, ChatID: 1, ID: 6}
	w.Items = append(w.Items, &Item{Msg: mine}, &Item{Msg: theirs})

	u.reactions(model.EvReactions{ChatID: 1, ID: 6, Reactions: []model.Reaction{{Emoji: "👍", Count: 1}}})
	if b.readReacts != 0 || w.Chat.UnreadReactions {
		t.Fatalf("reaction to someone else's message: read %d, flag %v", b.readReacts, w.Chat.UnreadReactions)
	}
	u.reactions(model.EvReactions{ChatID: 1, ID: 5, Reactions: []model.Reaction{{Emoji: "👍", Count: 1}}})
	if b.readReacts != 1 || w.Chat.UnreadReactions {
		t.Fatalf("reaction to my message: read %d, flag %v", b.readReacts, w.Chat.UnreadReactions)
	}
}

// Unread reactions reported by the dialog list (or a reaction that came while
// the window was not shown) are read at the next visit of the window, once.
func TestUnreadReactionsReadOnVisit(t *testing.T) {
	u, b := gifUI()
	u.focused = true
	w := u.ws.List[1]
	w.Chat.UnreadReactions = true
	u.markRead(w)
	u.markRead(w)
	if b.readReacts != 1 || w.Chat.UnreadReactions {
		t.Fatalf("read %d, flag %v", b.readReacts, w.Chat.UnreadReactions)
	}
}
