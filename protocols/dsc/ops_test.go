package dsc

import (
	"testing"

	"github.com/diamondburned/arikawa/v3/discord"
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
