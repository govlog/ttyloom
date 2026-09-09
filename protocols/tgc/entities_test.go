package tgc

import (
	"slices"
	"testing"

	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/model"
)

// spansOf : the UTF-16 offsets of entities become runes; styled kinds are
// covered, the rest falls off. "🩷" weighs 2 UTF-16 units, 1 rune.
func TestSpansOf(t *testing.T) {
	text := "🩷 gras lien"
	ents := []tg.MessageEntityClass{
		&tg.MessageEntityBold{Offset: 3, Length: 4},                         // "gras"
		&tg.MessageEntityTextURL{Offset: 8, Length: 4, URL: "https://x.io"}, // "lien"
		&tg.MessageEntityBankCard{Offset: 0, Length: 2},                     // ignored
	}
	got := spansOf(text, ents)
	want := []model.Span{
		{Start: 2, End: 6, Kind: model.SpanBold},
		{Start: 7, End: 11, Kind: model.SpanURL, URL: "https://x.io"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("spansOf:\n got %+v\nwant %+v", got, want)
	}
}

// Kinds with metadata: Pre keeps the language, MentionName the pair, bare URL
// its target, Email its mailto.
func TestSpansOfKinds(t *testing.T) {
	text := "code x@y.z go.dev @bob"
	ents := []tg.MessageEntityClass{
		&tg.MessageEntityPre{Offset: 0, Length: 4, Language: "go"},
		&tg.MessageEntityEmail{Offset: 5, Length: 5},
		&tg.MessageEntityURL{Offset: 11, Length: 6},
		&tg.MessageEntityMentionName{Offset: 18, Length: 4, UserID: 42},
	}
	got := spansOf(text, ents)
	want := []model.Span{
		{Start: 0, End: 4, Kind: model.SpanPre, Lang: "go"},
		{Start: 5, End: 10, Kind: model.SpanURL, URL: "mailto:x@y.z"},
		{Start: 11, End: 17, Kind: model.SpanURL, URL: "https://go.dev"},
		{Start: 18, End: 22, Kind: model.SpanMention, UserID: 42},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("kinds:\n got %+v\nwant %+v", got, want)
	}
}

// stylingOf : one StyledTextOption per segment, plus a "\n" between blocks
// like the local echo. Round trip through gotd: the text and entities
// actually sent come back as Span.
func TestStylingOf(t *testing.T) {
	var b entity.Builder
	segs := []model.Seg{{Text: "a"}, {Text: "code", Kind: model.SegPre, Lang: "go"}, {Text: "c", Italic: true}}
	if err := styling.Perform(&b, stylingOf(segs)...); err != nil {
		t.Fatalf("perform: %v", err)
	}
	text, ents := b.Complete()
	if text != "a\ncode\nc" { // one break between blocks, neither before nor after
		t.Fatalf("text: %q", text)
	}
	got := spansOf(text, ents)
	want := []model.Span{
		{Start: 2, End: 6, Kind: model.SpanPre, Lang: "go"},
		{Start: 7, End: 8, Kind: model.SpanItalic},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("spans:\n got %+v\nwant %+v", got, want)
	}
}

// Styled runs of one line: no break, and the styles of one run stack as
// several entities over the same range.
func TestStylingOfRuns(t *testing.T) {
	var b entity.Builder
	segs := []model.Seg{{Text: "a"}, {Text: "b", Bold: true, Underline: true}, {Text: "c"}}
	if err := styling.Perform(&b, stylingOf(segs)...); err != nil {
		t.Fatalf("perform: %v", err)
	}
	text, ents := b.Complete()
	if text != "abc" {
		t.Fatalf("text: %q", text)
	}
	got := spansOf(text, ents)
	want := []model.Span{{Start: 1, End: 2, Kind: model.SpanBold}, {Start: 1, End: 2, Kind: model.SpanUnderline}}
	if !slices.Equal(got, want) {
		t.Fatalf("spans:\n got %+v\nwant %+v", got, want)
	}
}
