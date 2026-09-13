package irc

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

func cmd(c *Client, reply int64, room, name, text string) {
	c.Command(context.Background(), reply, room, name, strings.Fields(text), text)
}

// waitLines gives the first EvLines whose text contains sub.
func waitLines(t *testing.T, events chan model.Event, sub string) model.EvLines {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			if l, ok := ev.(model.EvLines); ok && strings.Contains(strings.Join(l.Lines, "\n"), sub) {
				return l
			}
		case <-deadline:
			t.Fatalf("no EvLines with %q", sub)
		}
	}
}

// /join #room key: the key goes on the JOIN line.
func TestJoinWithKey(t *testing.T) {
	c, s, _, _ := start(t, Config{}, false)
	c.Resolve(context.Background(), "#priv secret", true, 1)
	if l := s.expect("JOIN"); l != "JOIN #priv secret" {
		t.Fatalf("join: %q", l)
	}
}

// Channel commands: sent as typed, the room of the window as default target.
func TestModeKickPartCycle(t *testing.T) {
	c, s, events, _ := start(t, Config{Channels: []string{"#go"}}, false)
	s.expect("JOIN #go")
	cmd(c, 7, "#go", "mode", "#go +ntk toto")
	if l := s.expect("MODE"); l != "MODE #go +ntk toto" {
		t.Fatalf("mode: %q", l)
	}
	cmd(c, 7, "#go", "mode", "+i")
	if l := s.expect("MODE"); l != "MODE #go +i" {
		t.Fatalf("mode default room: %q", l)
	}
	cmd(c, 7, "#go", "mode", "")
	if l := s.expect("MODE"); l != "MODE #go" {
		t.Fatalf("mode alone: %q", l)
	}
	s.send(":srv 324 me #go +nt")
	if l := waitLines(t, events, "+nt"); l.ChatID != 7 {
		t.Fatalf("324 reply target: %+v", l)
	}
	s.send(":srv 329 me #go 1700000000") // closes the answer of /mode
	if l := waitLines(t, events, "created"); l.ChatID != 7 {
		t.Fatalf("329 reply target: %+v", l)
	}
	cmd(c, 7, "#go", "kick", "bob too loud")
	if l := s.expect("KICK"); l != "KICK #go bob :too loud" {
		t.Fatalf("kick: %q", l)
	}
	cmd(c, 7, "#go", "cycle", "")
	if l := s.expect("PART"); !strings.HasPrefix(l, "PART #go") {
		t.Fatalf("cycle part: %q", l)
	}
	s.expect("JOIN #go")
	cmd(c, 7, "#go", "part", "see you")
	if l := s.expect("PART"); l != "PART #go :see you" {
		t.Fatalf("part: %q", l)
	}
	if g := waitFor[model.EvChatGone](t, events); g.ChatID != chatID("#go") {
		t.Fatalf("part gone: %+v", g)
	}
}

// /ban nick asks USERHOST and bans *!*@host; /kickban adds the KICK; a full
// mask goes straight out.
func TestBanByUserhost(t *testing.T) {
	c, s, _, _ := start(t, Config{}, false)
	cmd(c, 0, "#go", "ban", "bob")
	if l := s.expect("USERHOST"); l != "USERHOST bob" {
		t.Fatalf("userhost: %q", l)
	}
	s.send(":srv 302 me :bob=+~b@h.example")
	if l := s.expect("MODE"); l != "MODE #go +b *!*@h.example" {
		t.Fatalf("ban: %q", l)
	}
	cmd(c, 0, "#go", "kickban", "bob spam bot")
	s.expect("USERHOST bob")
	s.send(":srv 302 me :bob=-b@h.example")
	if l := s.expect("MODE"); l != "MODE #go +b *!*@h.example" {
		t.Fatalf("kickban mode: %q", l)
	}
	if l := s.expect("KICK"); l != "KICK #go bob :spam bot" {
		t.Fatalf("kickban kick: %q", l)
	}
	cmd(c, 0, "#go", "ban", "*!*@evil.example")
	if l := s.expect("MODE"); l != "MODE #go +b *!*@evil.example" {
		t.Fatalf("mask ban: %q", l)
	}
	// A 302 with no host: "*!*@" is a ban the server widens to everyone.
	cmd(c, 0, "#go", "ban", "ghost")
	s.expect("USERHOST ghost")
	s.send(":srv 302 me :ghost=")
	s.never("MODE", 200*time.Millisecond)
}

// A /ban of a nick that is not connected says so: the server names nobody in
// its 302, and a moderation command must not fail silently.
func TestBanUnknownNick(t *testing.T) {
	c, s, events, _ := start(t, Config{}, false)
	cmd(c, 6, "#go", "ban", "nobody")
	if l := s.expect("USERHOST"); l != "USERHOST nobody" {
		t.Fatalf("userhost: %q", l)
	}
	s.send(":srv 302 me :")
	if l := waitLines(t, events, "nobody"); l.ChatID != 6 || !strings.Contains(l.Lines[0], i18n.T("irc_no_such_nick")) {
		t.Fatalf("unknown nick: %+v", l)
	}
	s.never("MODE", 200*time.Millisecond)
}

// WHO, WHOWAS and MOTD answer in the window that asked; the MOTD of the
// connection goes to window 0 in one event.
func TestWhoWhowasMotd(t *testing.T) {
	c, s, events, _ := start(t, Config{}, false)
	cmd(c, 5, "#go", "who", "")
	s.expect("WHO #go")
	s.send(":srv 352 me #go al host.example srv1 alice H@ :0 Alice A")
	s.send(":srv 315 me #go :End of WHO")
	if l := waitLines(t, events, "alice"); l.ChatID != 5 || !strings.Contains(l.Lines[0], "al@host.example") || !strings.Contains(l.Lines[0], "Alice A") {
		t.Fatalf("who: %+v", l)
	}
	cmd(c, 5, "", "whowas", "ghost")
	s.expect("WHOWAS ghost")
	s.send(":srv 314 me ghost g old.example * :Ghost")
	s.send(":srv 312 me ghost old.example :Sun Jan 1") // 312 answers a WHOWAS too
	s.send(":srv 369 me ghost :End of WHOWAS")
	if l := waitLines(t, events, "g@old.example"); l.ChatID != 5 {
		t.Fatalf("whowas: %+v", l)
	}
	if l := waitLines(t, events, "ghost was on old.example"); l.ChatID != 5 {
		t.Fatalf("whowas server line: %+v", l)
	}
	cmd(c, 5, "", "motd", "")
	s.expect("MOTD")
	s.send(":srv 375 me :- srv Message of the day -")
	s.send(":srv 372 me :- hello")
	s.send(":srv 372 me :- world")
	s.send(":srv 376 me :End of MOTD")
	l := waitLines(t, events, "world")
	if l.ChatID != 5 || len(l.Lines) != 4 {
		t.Fatalf("motd: %+v", l)
	}
	// Unasked (a reconnection): window 0.
	s.send(":srv 375 me :- srv Message of the day -")
	s.send(":srv 376 me :End of MOTD")
	if l = waitLines(t, events, "Message of the day"); l.ChatID != 0 {
		t.Fatalf("motd at connection: %+v", l)
	}
}

// /ignore drops the lines of a matching source and saves the list.
func TestIgnore(t *testing.T) {
	c, s, events, _ := start(t, Config{}, false)
	cmd(c, 0, "", "ignore", "bob")
	if ig := waitFor[model.EvIRCIgnores](t, events); len(ig.Ignores) != 1 || ig.Ignores[0] != "bob!*@*" {
		t.Fatalf("ignores: %+v", ig)
	}
	s.send(":bob!b@h PRIVMSG #go :spam")
	s.send(":carol!c@h PRIVMSG #go :hello")
	m := waitMsg(t, events, func(model.EvNewMessage) bool { return true })
	if m.Msg.From != "carol" {
		t.Fatalf("ignored line came through: %+v", m.Msg)
	}
	cmd(c, 0, "", "ignore", "bob") // again: removed
	if ig := waitFor[model.EvIRCIgnores](t, events); len(ig.Ignores) != 0 {
		t.Fatalf("toggle off: %+v", ig)
	}
}

// A mask read from the configuration is normalised like a typed one.
func TestIgnoreFromConfig(t *testing.T) {
	_, s, events, _ := start(t, Config{Ignores: []string{"bob"}}, false)
	s.send(":bob!b@h PRIVMSG #go :spam")
	s.send(":carol!c@h PRIVMSG #go :hello")
	if m := waitMsg(t, events, func(model.EvNewMessage) bool { return true }); m.Msg.From != "carol" {
		t.Fatalf("config mask did not match: %+v", m.Msg)
	}
}

// /ctcp, /quote and /away on the wire; a CTCP reply shows as [ctcp(nick)].
func TestCtcpQuoteAway(t *testing.T) {
	c, s, events, _ := start(t, Config{}, false)
	cmd(c, 3, "", "ctcp", "bob VERSION")
	if l := s.expect("PRIVMSG bob"); !strings.HasPrefix(l, "PRIVMSG bob ") || !strings.HasSuffix(l, "\x01VERSION\x01") {
		t.Fatalf("ctcp: %q", l)
	}
	s.send(":bob!b@h NOTICE me :\x01VERSION ttyloom 1\x01")
	if l := waitLines(t, events, "[ctcp(bob)] VERSION ttyloom 1"); l.ChatID != 3 {
		t.Fatalf("ctcp reply: %+v", l)
	}
	cmd(c, 3, "", "ctcp", "bob  TIME zone") // two spaces before the command
	if l := s.expect("PRIVMSG bob"); !strings.HasSuffix(l, "\x01TIME zone\x01") {
		t.Fatalf("ctcp spacing: %q", l)
	}
	cmd(c, 0, "", "quote", "PRIVMSG #go :raw line")
	if l := s.expect("PRIVMSG #go"); l != "PRIVMSG #go :raw line" {
		t.Fatalf("quote: %q", l)
	}
	c.Away(context.Background(), "be right back")
	if l := s.expect("AWAY"); l != "AWAY :be right back" {
		t.Fatalf("away: %q", l)
	}
	s.send(":srv 306 me :You have been marked as being away")
	if l := waitLines(t, events, "away"); l.ChatID != 0 {
		t.Fatalf("306: %+v", l)
	}
	c.Away(context.Background(), "")
	if l := s.expect("AWAY"); l != "AWAY" {
		t.Fatalf("unaway: %q", l)
	}
}

// Pure helpers.
func TestBanMaskAndGlob(t *testing.T) {
	if m := banMask("~b@h.example"); m != "*!*@h.example" {
		t.Fatalf("banMask: %q", m)
	}
	for _, tc := range []struct {
		pat, s string
		want   bool
	}{{"bob!*@*", "bob!b@h", true}, {"*!*@h.*", "x!y@h.example", true}, {"bob!*@*", "bobby!b@h", false}, {"b?b!*@*", "BOB!b@h", true}} {
		if got := globMatch(tc.pat, tc.s); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v", tc.pat, tc.s, got)
		}
	}
}
