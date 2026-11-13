package dsc

import (
	"slices"
	"testing"

	"github.com/diamondburned/arikawa/v3/discord"
)

// The tab of a server: its stickers the account favors, then those it uses
// most, then all of them — a favorite of another server is not usable here
// without Nitro, and a Lottie one has no picture to show.
func TestGuildSections(t *testing.T) {
	stickers := []discord.Sticker{
		{ID: 7, Name: "wave", Tags: "👋", FormatType: discord.StickerFormatPNG},
		{ID: 8, Name: "wumpus", FormatType: discord.StickerFormatLottie},
		{ID: 9, Name: "spin", FormatType: 4},
	}
	secs := guildSections(stickers, "Gophers", []uint64{7, 42}, nil)
	var got []string
	for _, s := range secs {
		got = append(got, s.Title)
		for _, st := range s.Stickers {
			got = append(got, st.Name)
		}
	}
	if want := []string{"Favorites", "wave", "Gophers", "wave", "spin"}; !slices.Equal(got, want) {
		t.Fatalf("sections %v, want %v", got, want)
	}
	if id, ok := secs[0].Stickers[0].Send.(discord.StickerID); !ok || id != 7 || secs[0].Stickers[0].Tags != "👋" {
		t.Fatalf("sticker: %+v", secs[0].Stickers[0])
	}
}

// A sticker shown in two sections has a picture in each: the grid frees the
// picture of a cell far from the view, which must not empty the one on it.
func TestGuildSectionsOwnPictures(t *testing.T) {
	secs := guildSections([]discord.Sticker{{ID: 7, Name: "wave", FormatType: discord.StickerFormatPNG}}, "g", []uint64{7}, nil)
	if len(secs) != 2 || secs[0].Stickers[0].Preview == secs[1].Stickers[0].Preview {
		t.Fatalf("sections %d, shared picture %v; want two sections with a picture each", len(secs),
			len(secs) == 2 && secs[0].Stickers[0].Preview == secs[1].Stickers[0].Preview)
	}
}
