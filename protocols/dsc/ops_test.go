package dsc

import (
	"fmt"
	"testing"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
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
		// A code block shows the text as it is, <:emoji_7:77> included.
		"```\nx := \":emoji_7:\"\n``` :wave:": "```\nx := \":emoji_7:\"\n``` <a:wave:78>",
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
