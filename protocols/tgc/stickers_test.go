package tgc

import (
	"slices"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/model"
)

func stickerDoc(id int64, mime string, thumbs ...tg.PhotoSizeClass) *tg.Document {
	return &tg.Document{ID: id, AccessHash: id * 10, FileReference: []byte{1}, MimeType: mime, Thumbs: thumbs,
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeSticker{Alt: "😂"}}}
}

// A sticker of the box has a picture to show and the document to send back;
// one with nothing to show (Lottie with no thumb) is not offered.
func TestStickerOf(t *testing.T) {
	s, ok := stickerOf(stickerDoc(1, "image/webp"))
	doc, _ := s.Send.(*tg.InputDocument)
	if !ok || s.Emoji != "😂" || s.Preview == nil || s.Preview.Kind != model.MediaSticker ||
		doc == nil || doc.ID != 1 || doc.AccessHash != 10 {
		t.Fatalf("webp sticker: %+v, %v", s, ok)
	}
	if s, ok := stickerOf(stickerDoc(2, "application/x-tgsticker", &tg.PhotoSize{Type: "m", W: 128, H: 128})); !ok || !s.Preview.Previewable() {
		t.Fatalf("Lottie sticker with a thumb: %+v, %v", s, ok)
	}
	if _, ok := stickerOf(stickerDoc(3, "application/x-tgsticker")); ok {
		t.Fatal("Lottie sticker with no thumb offered")
	}
}

// A video sticker shows its still thumb in the grid, not the whole video; with
// no thumb it is not offered.
func TestStickerOfVideo(t *testing.T) {
	s, ok := stickerOf(stickerDoc(4, "video/webm", &tg.PhotoSize{Type: "m", W: 128, H: 128}))
	loc, _ := s.Preview.Loc.(*tg.InputDocumentFileLocation)
	if !ok || s.Preview.Kind != model.MediaSticker || loc == nil || loc.ThumbSize != "m" {
		t.Fatalf("video sticker: %+v, %v", s.Preview, ok)
	}
	if _, ok := stickerOf(stickerDoc(5, "video/webm")); ok {
		t.Fatal("video sticker with no thumb offered")
	}
}

// The stickers of the session are asked again after stickerTTL: their file
// references expire and a send with an old one fails.
func TestStickerCacheExpires(t *testing.T) {
	now := time.Now()
	sc := stickerCache{loaded: true, at: now.Add(-stickerTTL - time.Minute)}
	if sc.fresh(now) {
		t.Fatal("a cache older than stickerTTL is used")
	}
	if sc.at = now.Add(-time.Minute); !sc.fresh(now) {
		t.Fatal("a cache of a minute is asked again")
	}
}

// The sections: recents, favorites (each left out when empty), then the packs
// to load, keyed by their id and access hash.
func TestSectionsOf(t *testing.T) {
	secs := sectionsOf([]tg.DocumentClass{stickerDoc(1, "image/webp")}, nil,
		[]tg.StickerSet{{ID: 7, AccessHash: 8, Title: "Pepe"}})
	var got []string
	for _, s := range secs {
		got = append(got, s.Title+"|"+s.Key)
	}
	if want := []string{"Récents|", "Pepe|7:8"}; !slices.Equal(got, want) || len(secs[0].Stickers) != 1 || secs[1].Stickers != nil {
		t.Fatalf("sections %v (stickers %d, pack %v), want %v", got, len(secs[0].Stickers), secs[1].Stickers, want)
	}
}
