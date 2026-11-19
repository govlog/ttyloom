package dsc

import (
	"context"
	"fmt"
	"testing"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/state/store"
	"github.com/diamondburned/ningen/v3/states/member"

	"github.com/govlog/ttyloom/internal/model"
)

// A ":name:" the guild knows goes out as the <:name:id> Discord renders; an
// unknown one and a DM stay as typed.
func TestCustoms(t *testing.T) {
	c := dmState(t)
	if err := c.state().Cabinet.EmojiSet(9, []discord.Emoji{
		{ID: 77, Name: "emoji_7"}, {ID: 78, Name: "wave", Animated: true}}, false); err != nil {
		t.Fatalf("emojis of the test: %s", err)
	}
	for in, want := range map[string]string{
		"hi :emoji_7: :wave: :nope: 10:30": "hi <:emoji_7:77> <a:wave:78> :nope: 10:30",
		"no colon":                         "no colon",
		// Code shows the text as it is, <:emoji_7:77> included: a code block,
		// inline code, both, and a span of two backticks with one inside.
		"```\nx := \":emoji_7:\"\n``` :wave:": "```\nx := \":emoji_7:\"\n``` <a:wave:78>",
		"`:emoji_7:` :wave:":                  "`:emoji_7:` <a:wave:78>",
		"```\n:wave:\n``` `:wave:` :emoji_7:": "```\n:wave:\n``` `:wave:` <:emoji_7:77>",
		"``a`:wave:`` :wave:":                 "``a`:wave:`` <a:wave:78>",
		// A backtick never closed, or escaped, opens no code.
		"it`s :wave:":         "it`s <a:wave:78>",
		"\\`:wave:\\` :wave:": "\\`<a:wave:78>\\` <a:wave:78>",
	} {
		if got := c.customs(in, 9); got != want {
			t.Fatalf("customs(%q) = %q, want %q", in, got, want)
		}
	}
	if got := c.customs(":emoji_7:", 0); got != ":emoji_7:" {
		t.Fatalf("customs outside a guild = %q, want the text as typed", got)
	}
}

// The chat of a guild channel carries the custom emojis of the guild, the
// picker lists them; a DM carries none.
func TestChatCustoms(t *testing.T) {
	c := dmState(t)
	if err := c.state().Cabinet.EmojiSet(9, []discord.Emoji{{ID: 77, Name: "emoji_7"}}, false); err != nil {
		t.Fatalf("emojis of the test: %s", err)
	}
	if err := c.state().Cabinet.ChannelSet(&discord.Channel{ID: 8, GuildID: 9, Name: "gen"}, false); err != nil {
		t.Fatalf("channel of the test: %s", err)
	}
	if got := c.chatFor(8).Customs; len(got) != 1 || got[0] != ":emoji_7:" {
		t.Fatalf("customs of the guild channel = %v", got)
	}
	if got := c.chatFor(5).Customs; got != nil {
		t.Fatalf("customs of a DM = %v, want none", got)
	}
}

// The custom emojis of a guild are built once for all its chats: a copy per
// chat and per incoming message grew the dialog list two hundred times. A
// change of the emojis of the guild is seen at the next message.
func TestCustomsOncePerGuild(t *testing.T) {
	c := dmState(t)
	c.wire()
	es := make([]discord.Emoji, 250)
	for i := range es {
		es[i] = discord.Emoji{ID: discord.EmojiID(100 + i), Name: fmt.Sprintf("e%d", i)}
	}
	if err := c.state().Cabinet.EmojiSet(9, es, false); err != nil {
		t.Fatal(err)
	}
	if err := c.state().Cabinet.ChannelSet(&discord.Channel{ID: 8, GuildID: 9, Name: "gen"}, false); err != nil {
		t.Fatal(err)
	}
	if n := testing.AllocsPerRun(20, func() { c.chatFor(8) }); n > 50 {
		t.Fatalf("%v allocations per message of the guild: its 250 emojis copied each time", n)
	}
	if err := c.state().Cabinet.EmojiSet(9, []discord.Emoji{{ID: 1, Name: "new"}}, true); err != nil {
		t.Fatal(err)
	}
	c.state().Handler.Call(&gateway.GuildEmojisUpdateEvent{GuildID: 9})
	if got := c.chatFor(8).Customs; len(got) != 1 || got[0] != ":new:" {
		t.Fatalf("customs after the update = %d entries, want the new one alone", len(got))
	}
}

// hookedEmojis : the emoji store of the state, with a hook that runs once,
// just after a read. The test puts a gateway event there: the event then
// lands between the read of customsOf and its cache write, every time.
type hookedEmojis struct {
	store.EmojiStore
	after func()
}

func (h *hookedEmojis) Emojis(g discord.GuildID) ([]discord.Emoji, error) {
	es, err := h.EmojiStore.Emojis(g)
	if f := h.after; f != nil {
		h.after = nil
		f()
	}
	return es, err
}

// An emoji update that comes while customsOf builds the list from the old
// emojis does not leave the old list in the cache: the next chat of the guild
// gets the new one.
func TestCustomsUpdateDuringBuild(t *testing.T) {
	c := dmState(t)
	c.wire()
	st := c.state()
	if err := st.Cabinet.ChannelSet(&discord.Channel{ID: 8, GuildID: 9, Name: "gen"}, false); err != nil {
		t.Fatal(err)
	}
	if err := st.Cabinet.EmojiSet(9, []discord.Emoji{{ID: 1, Name: "old"}}, false); err != nil {
		t.Fatal(err)
	}
	st.Cabinet.EmojiStore = &hookedEmojis{EmojiStore: st.Cabinet.EmojiStore, after: func() {
		st.State.Session.Handler.Call(&gateway.GuildEmojisUpdateEvent{GuildID: 9, Emojis: []discord.Emoji{{ID: 2, Name: "new"}}})
	}}
	c.chatFor(8) // reads the old emojis; the update comes before the cache write
	if got := c.chatFor(8).Customs; len(got) != 1 || got[0] != ":new:" {
		t.Fatalf("customs after the update = %v, want [:new:]", got)
	}
}

// A guild under 100 visible members whose list the gateway has already sent
// (another channel opened it): the box of a new channel shows the cached
// members. ningen used to panic there (makeslice: cap out of range).
func TestParticipantsSmallKnownList(t *testing.T) {
	ev := make(chan model.Event, 4)
	c := testClient(ev)
	st := c.state()
	if err := st.Cabinet.ChannelSet(&discord.Channel{ID: 8, GuildID: 9, Name: "gen"}, false); err != nil {
		t.Fatalf("channel of the test: %s", err)
	}
	if err := st.Cabinet.MemberSet(9, &discord.Member{User: discord.User{ID: 42, Username: "bob"}}, false); err != nil {
		t.Fatalf("member of the test: %s", err)
	}
	st.State.Handler.Call(&gateway.GuildMemberListUpdateEvent{
		GuildID: 9, ID: member.ComputeListID(nil), MemberCount: 1, OnlineCount: 1})
	c.Participants(context.Background(), c.chatFor(8))
	for {
		switch e := next(t, ev).(type) {
		case model.EvLog:
			t.Fatalf("log instead of the members: %s", e.Msg)
		case model.EvParticipants:
			if e.Err != "" || len(e.Lines) != 1 || e.Lines[0].Query != "@bob" {
				t.Fatalf("members = %+v, err %q", e.Lines, e.Err)
			}
			return
		}
	}
}
