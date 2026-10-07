package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
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

// A message the network counts as a mention of me (Telegram: a mention or a
// reply to me) stays unread on the server until its window is looked at:
// then it is read there once — readHistory alone left the @ badge of the
// phone on.
func TestUnreadMentionsReadOnVisit(t *testing.T) {
	u, b := gifUI()
	u.focused, u.ws.Cur = true, 0 // the window of the chat is not the one shown
	w := u.ws.List[1]
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvNewMessage{Chat: w.Chat,
		Msg: model.Msg{ChatID: w.Chat.ID, ID: 9, From: "bob", Text: "@me look", Mentioned: true}}})
	if b.readMentions != 0 || !w.Chat.UnreadMentions {
		t.Fatalf("mention in a window not shown: read %d, flag %v", b.readMentions, w.Chat.UnreadMentions)
	}
	u.markRead(w)
	u.markRead(w)
	if b.readMentions != 1 || w.Chat.UnreadMentions {
		t.Fatalf("visit: read %d, flag %v", b.readMentions, w.Chat.UnreadMentions)
	}
}

// reactUI : gifUI with a terminal that keeps its output, the notifications
// on and the bell on, window 0 shown: the chat of window 1 is away.
func reactUI(out *bytes.Buffer) (*UI, *Window) {
	u, _ := gifUI()
	u.t = term.NewOffscreen(out, 100, 30)
	u.cfg.Bell, u.cfg.Notify = true, "terminal"
	u.focused, u.ws.Cur, u.dispatchNet = true, 0, netTelegram
	u.muted, u.dirty = map[model.ChatKey]bool{}, map[model.ChatKey]bool{}
	w := u.ws.List[1]
	w.Items = append(w.Items, &Item{Msg: &model.Msg{Net: netTelegram, ChatID: 1, ID: 5, Out: true, Text: "my joke"}})
	return u, w
}

// Someone reacts to my message in a chat I am not looking at: one
// notification without the bell, and the reaction stays next to the chat
// (sidebar, [Act: …]) — once, though the server repeats it while unread.
func TestReactionNotifiesAndMarksTheChat(t *testing.T) {
	var out bytes.Buffer
	u, _ := reactUI(&out)
	react := model.EvReactions{ChatID: 1, ID: 5, Reactions: []model.Reaction{{Emoji: "🔥", Count: 1}}, New: "🔥", By: "Eve"}
	signal := func() string {
		u.flushAlert(time.Now().Add(alertGap)) // past the rate limit of the alerts
		u.t.Flush()
		s := out.String()
		out.Reset()
		return s
	}
	u.reactions(react)
	if got := signal(); !strings.Contains(got, "]777;notify;") || !strings.Contains(got, "🔥 « my joke »") || strings.Contains(got, "\a") {
		t.Fatalf("notification %q: want one with the reaction and my text, no bell", got)
	}
	u.reactions(react)
	if got := signal(); strings.Contains(got, "]777;") {
		t.Fatal("the same unread reaction notifies once")
	}
	line := body(sidebarLines(sideChats, u.chatList, u.ws.List, nil, u.ws.Cur, u.th, testSideW, 1, 0, false, false, 0, -1, false, u.title, sideState{})[0])
	if !strings.HasSuffix(line, "🔥") {
		t.Errorf("sidebar line %q: want the reaction at its end", line)
	}
	var act strings.Builder
	for _, s := range u.actSpans(theme.Style{}, theme.Style{}) {
		act.WriteString(s.Text)
	}
	if act.String() != "1🔥" {
		t.Errorf("[Act: %s], want 1🔥", act.String())
	}
}

// Going to the window shows the message that got the reaction (selected,
// the view moves to it) and the reaction leaves the chat line.
func TestReactionVisitShowsTheMessage(t *testing.T) {
	var out bytes.Buffer
	u, w := reactUI(&out)
	w.Items = append(w.Items, &Item{Msg: &model.Msg{Net: netTelegram, ChatID: 1, ID: 6, Text: "later"}})
	u.reactions(model.EvReactions{ChatID: 1, ID: 5, Reactions: []model.Reaction{{Emoji: "🔥", Count: 1}}, New: "🔥", By: "Eve"})
	u.goTo(1)
	if w.Sel == nil || w.Sel.Msg.ID != 5 {
		t.Fatalf("visit: selection %+v, want message 5", w.Sel)
	}
	if reactBadge(w.Chat) != "" {
		t.Error("the visit must clear the reaction of the chat line")
	}
}

// The dialog list only says a chat holds unread reactions: a generic one
// marks it until the visit.
func TestReactionFromDialogList(t *testing.T) {
	u, _ := gifUI()
	u.dialogsSeen = map[string]bool{netTelegram: true}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvDialogs{Chats: []*model.Chat{
		{ID: 1, Kind: model.ChatUser, Title: "Alice", UnreadReactions: true}}}})
	if got := u.chats[model.ChatKey{Net: netTelegram, ID: 1}].LastReaction; got != genericReaction {
		t.Fatalf("reaction from the list %q, want %q", got, genericReaction)
	}
}
