package hook

import (
	"regexp"
	"slices"
	"testing"

	"github.com/govlog/ttyloom/internal/model"
)

// TestMatch : each filter set must take the message; an empty hook takes
// everything. from compares ids, or names where the name is the id.
func TestMatch(t *testing.T) {
	msg := Msg{Net: "irc:libera", Chat: "#Go", Kind: KindGroup, From: "Alice", FromID: 42, Text: "!meteo Paris", Mention: true}
	byName := msg
	byName.NameIsID = true
	quiet := msg
	quiet.Mention = false
	re := regexp.MustCompile(`^!meteo (?P<ville>\w+)$`)
	for _, c := range []struct {
		name string
		h    Hook
		m    Msg
		ok   bool
	}{
		{"no filter", Hook{}, msg, true},
		{"network", Hook{Net: "irc:libera"}, msg, true},
		{"module", Hook{Net: "irc"}, msg, true},
		{"other network", Hook{Net: "irc:oftc"}, msg, false},
		{"chat, case apart", Hook{Chats: []string{"#rust", "#go"}}, msg, true},
		{"other chat", Hook{Chats: []string{"#rust"}}, msg, false},
		{"kind", Hook{Kinds: []string{KindPrivate, KindGroup}}, msg, true},
		{"other kind", Hook{Kinds: []string{KindChannel}}, msg, false},
		{"from by id", Hook{From: []string{"7", "42"}}, msg, true},
		{"a name where ids count", Hook{From: []string{"alice"}}, msg, false},
		{"a name where the name is the id", Hook{From: []string{"alice"}}, byName, true},
		{"an id where the name is the id", Hook{From: []string{"42"}}, byName, false},
		{"mention", Hook{Mention: true}, msg, true},
		{"no mention", Hook{Mention: true}, quiet, false},
		{"match", Hook{Match: re}, msg, true},
		{"no match", Hook{Match: re}, Msg{Text: "!meteo"}, false},
		{"every filter must take it", Hook{Net: "irc", Chats: []string{"#rust"}, Match: re}, msg, false},
	} {
		if _, ok := Match(c.h, c.m); ok != c.ok {
			t.Errorf("%s: %v, want %v", c.name, ok, c.ok)
		}
	}
	got, _ := Match(Hook{Match: re}, msg)
	if got.Whole != "!meteo Paris" || !slices.Equal(got.Groups, []string{"Paris"}) || got.Named["ville"] != "Paris" {
		t.Errorf("captures: %+v", got)
	}
}

// TestKindOf : the chat kinds of the model under their names of hooks.toml.
func TestKindOf(t *testing.T) {
	for k, want := range map[model.ChatKind]string{model.ChatUser: KindPrivate, model.ChatGroup: KindGroup, model.ChatChannel: KindChannel} {
		if got := KindOf(k); got != want {
			t.Errorf("KindOf(%d) = %q, want %q", k, got, want)
		}
	}
}

// TestEnv : the variables of a run, in their order; a NUL of the message is
// dropped (an environment cannot carry one).
func TestEnv(t *testing.T) {
	m := Msg{Net: "irc:libera", Chat: "#go", ChatID: 5, Kind: KindGroup, From: "alice", FromID: 42, ID: 9,
		Text: "!m Pa\x00ris", Mention: true}
	c := Captures{Whole: "!m Paris", Groups: []string{"Paris", "x"}, Named: map[string]string{"ville": "Paris", "b": "x"}}
	want := []string{"TTYLOOM_HOOK=meteo", "TTYLOOM_NET=irc:libera", "TTYLOOM_CHAT=#go", "TTYLOOM_CHAT_ID=5",
		"TTYLOOM_CHAT_KIND=group", "TTYLOOM_FROM=alice", "TTYLOOM_FROM_ID=42", "TTYLOOM_MSG_ID=9",
		"TTYLOOM_TEXT=!m Paris", "TTYLOOM_MENTION=1", "TTYLOOM_MATCH=!m Paris", "TTYLOOM_MATCH_1=Paris",
		"TTYLOOM_MATCH_2=x", "TTYLOOM_MATCH_B=x", "TTYLOOM_MATCH_VILLE=Paris"}
	if got := Env(Hook{Name: "meteo"}, m, c); !slices.Equal(got, want) {
		t.Fatalf("env\n got %q\nwant %q", got, want)
	}
}
