package dsc

import (
	"encoding/binary"
	"slices"
	"testing"
)

// pbVarint, pbVar and pbLen write the protobuf wire format, enough for the
// settings Discord syncs.
func pbVarint(v uint64) []byte { return binary.AppendUvarint(nil, v) }

func pbVar(field int, v uint64) []byte {
	return append(pbVarint(uint64(field)<<3), pbVarint(v)...)
}

func pbLen(field int, data []byte) []byte {
	b := append(pbVarint(uint64(field)<<3|2), pbVarint(uint64(len(data)))...)
	return append(b, data...)
}

// pbEntry : one entry of the emoji_reaction_frecency map.
func pbEntry(key string, score, total uint64) []byte {
	item := slices.Concat(pbVar(1, total), pbLen(2, pbVarint(1700000000000)), pbVar(3, 5), pbVar(4, score))
	return pbLen(1, slices.Concat(pbLen(1, []byte(key)), pbLen(2, item)))
}

// The favorite emojis come first, then the reactions by score, then by uses
// when the scores tie. A key is an emoji, a name ("joy") or the id of a custom
// emoji; an id of no known guild and an unknown field are left out. A blob
// cut in the middle gives what came before, with no panic.
func TestReactionTop(t *testing.T) {
	frecency := slices.Concat(
		pbEntry("🤣", 50, 10),
		pbEntry("joy", 80, 1),
		pbEntry("123", 90, 1),
		pbEntry("999", 70, 1),
		pbEntry("100", 60, 1), // the name of 💯, not the id of a custom emoji
		pbEntry("👍", 50, 30),
	)
	blob := slices.Concat(
		pbLen(5, pbLen(1, []byte("😂"))),
		pbVar(99, 7),
		pbLen(13, frecency),
	)
	custom := func(id string) string {
		if id == "123" {
			return ":kek:"
		}
		return ""
	}
	want := []string{"😂", ":kek:", "💯", "👍", "🤣"}
	if got := reactionTop(blob, custom); !slices.Equal(got, want) {
		t.Fatalf("reactionTop = %q, want %q", got, want)
	}
	for cut := range len(blob) {
		got := reactionTop(blob[:cut], custom)
		if len(got) > len(want) || !slices.Equal(got, want[:len(got)]) {
			t.Fatalf("blob cut at %d: %q, want a prefix of %q", cut, got, want)
		}
	}
}

// pbFixed : fixed64 values, packed as Discord writes repeated fixed64.
func pbFixed(vs ...uint64) []byte {
	var b []byte
	for _, v := range vs {
		b = binary.LittleEndian.AppendUint64(b, v)
	}
	return b
}

// pbStickerEntry : one entry of the sticker_frecency map (fixed64 key).
func pbStickerEntry(id, score uint64) []byte {
	key := append(pbVarint(1<<3|1), pbFixed(id)...)
	return pbLen(1, slices.Concat(key, pbLen(2, slices.Concat(pbVar(1, 1), pbVar(4, score)))))
}

// The favorite stickers (field 3, packed ids) and the most used ones (field
// 4, by score) of the same synced settings.
func TestStickerTop(t *testing.T) {
	blob := slices.Concat(
		pbLen(3, pbLen(1, pbFixed(7, 8))),
		pbLen(4, slices.Concat(pbStickerEntry(9, 10), pbStickerEntry(7, 50))),
	)
	faved, used := stickerTop(blob)
	if !slices.Equal(faved, []uint64{7, 8}) || !slices.Equal(used, []uint64{7, 9}) {
		t.Fatalf("stickerTop = %v, %v; want [7 8], [7 9]", faved, used)
	}
}
