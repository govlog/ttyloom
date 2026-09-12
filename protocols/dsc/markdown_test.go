package dsc

import (
	"slices"
	"testing"

	"github.com/govlog/ttyloom/internal/model"
)

// names of the test: 42 is known, everything else is not.
func testNames(id uint64) string {
	if id == 42 {
		return "bob"
	}
	return ""
}

// parse: the markers leave the text and become spans over what is left,
// offsets in runes.
func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name  string
		md    string
		text  string
		spans []model.Span
	}{
		{"bold", "a **b** c", "a b c", []model.Span{{Start: 2, End: 3, Kind: model.SpanBold}}},
		{"italic star", "*i* x", "i x", []model.Span{{Start: 0, End: 1, Kind: model.SpanItalic}}},
		{"italic underscore", "_i_ x", "i x", []model.Span{{Start: 0, End: 1, Kind: model.SpanItalic}}},
		{"underline", "__u__ x", "u x", []model.Span{{Start: 0, End: 1, Kind: model.SpanUnderline}}},
		{"strike", "~~s~~ x", "s x", []model.Span{{Start: 0, End: 1, Kind: model.SpanStrike}}},
		{"spoiler", "||s||!", "s!", []model.Span{{Start: 0, End: 1, Kind: model.SpanSpoiler}}},
		// A code run is opaque: the markers inside it are text.
		{"code opaque", "say `**x**` now", "say **x** now", []model.Span{{Start: 4, End: 9, Kind: model.SpanCode}}},
		{"pre with lang", "```go\na\nb\n```", "a\nb", []model.Span{{Start: 0, End: 3, Kind: model.SpanPre, Lang: "go"}}},
		{"pre without lang", "x ```\ncode\n```", "x code", []model.Span{{Start: 2, End: 6, Kind: model.SpanPre}}},
		{"mention", "x <@42> y", "x @bob y", []model.Span{{Start: 2, End: 6, Kind: model.SpanMention, UserID: 42}}},
		{"mention unknown", "<@!7>", "@7", []model.Span{{Start: 0, End: 2, Kind: model.SpanMention, UserID: 7}}},
		{"channel has no span", "see <#99> now", "see #99 now", nil},
		{"role mention stays text", "<@&5> hi", "<@&5> hi", nil},
		{"custom emoji", "hi <:emoji_7:1488279885599473847>", "hi :emoji_7:", nil},
		{"animated custom emoji", "<a:wave:12> x", ":wave: x", nil},
		{"custom emoji without id stays text", "<:x:y>", "<:x:y>", nil},
		{"bare url", "go https://x.io/a now", "go https://x.io/a now", []model.Span{{Start: 3, End: 17, Kind: model.SpanURL, URL: "https://x.io/a"}}},
		// The closing punctuation of a sentence is not part of the target.
		{"url in brackets", "see (https://x.io/a) now", "see (https://x.io/a) now", []model.Span{{Start: 5, End: 19, Kind: model.SpanURL, URL: "https://x.io/a"}}},
		// An "_" glued to a word belongs to the word: snake_case, file names, env
		// variables. Nothing leaves the text, nothing is styled.
		{"underscore inside a word", "foo_bar_baz", "foo_bar_baz", nil},
		{"underscore in a file name", "my_file_name.txt", "my_file_name.txt", nil},
		{"underscore ending two words", "a_b c_d", "a_b c_d", nil},
		{"underscore run inside a word", "foo__bar__baz", "foo__bar__baz", nil},
		{"italic underscore with a space", "_real italic_", "real italic", []model.Span{{Start: 0, End: 11, Kind: model.SpanItalic}}},
		{"unclosed marker is literal", "a **b c", "a **b c", nil},
		{"escape", `a \*b\* c`, "a *b* c", nil},
		// Astral rune before the marker: 2 UTF-16 units, 1 rune.
		{"astral offsets", "🩷 **b** c", "🩷 b c", []model.Span{{Start: 2, End: 3, Kind: model.SpanBold}}},
		// No nesting: a different marker inside a run is literal text.
		{"inner marker is literal", "**a *b* c**", "a *b* c", []model.Span{{Start: 0, End: 7, Kind: model.SpanBold}}},
		// A mention still counts inside a styled run, a marker does not.
		{"mention inside bold", "**hi <@42>**", "hi @bob", []model.Span{
			{Start: 3, End: 7, Kind: model.SpanMention, UserID: 42},
			{Start: 0, End: 7, Kind: model.SpanBold},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, spans := parse(tc.md, testNames)
			if text != tc.text {
				t.Fatalf("text: %q, want %q", text, tc.text)
			}
			if !slices.Equal(spans, tc.spans) {
				t.Fatalf("spans:\n got %+v\nwant %+v", spans, tc.spans)
			}
		})
	}
}

// Without a resolver a mention falls back on the id, and the span still
// carries it.
func TestParseNilNames(t *testing.T) {
	text, spans := parse("<@42> hi", nil)
	want := []model.Span{{Start: 0, End: 3, Kind: model.SpanMention, UserID: 42}}
	if text != "@42 hi" || !slices.Equal(spans, want) {
		t.Fatalf("nil names: %q %+v", text, spans)
	}
}

// render: one "\n" between the blocks like fenceText, then the round trip
// gives the text and the spans of the local echo back.
func TestRenderRoundTrip(t *testing.T) {
	segs := []model.Seg{{Text: "a"}, {Text: "code\nmore", Kind: model.SegPre, Lang: "go"}, {Text: "c", Italic: true}}
	md := render(segs)
	if md != "a\n```go\ncode\nmore\n```\n*c*" {
		t.Fatalf("render: %q", md)
	}
	text, spans := parse(md, nil)
	if text != "a\ncode\nmore\nc" {
		t.Fatalf("round trip text: %q", text)
	}
	want := []model.Span{
		{Start: 2, End: 11, Kind: model.SpanPre, Lang: "go"},
		{Start: 12, End: 13, Kind: model.SpanItalic},
	}
	if !slices.Equal(spans, want) {
		t.Fatalf("round trip spans:\n got %+v\nwant %+v", spans, want)
	}
}

// A marker inside an italic segment (/me with a "*") must not close the run
// on the Discord side: it goes out escaped.
func TestRenderEscapesItalicBody(t *testing.T) {
	if got := render([]model.Seg{{Text: "a*b_c", Italic: true}}); got != `*a\*b\_c*` {
		t.Fatalf("italic body: %q", got)
	}
}

// Styled runs of one line: no break between them, the markers nest and a
// style kept from one run to the next is not closed then opened again
// (**b****__c__** would break Discord's parser).
func TestRenderStyledRuns(t *testing.T) {
	segs := []model.Seg{{Text: "a"}, {Text: "b", Bold: true}, {Text: "c", Bold: true, Underline: true}, {Text: "d", Underline: true}, {Text: "e"}}
	if got := render(segs); got != "a**b__c__**__d__e" {
		t.Fatalf("render: %q", got)
	}
	// ponytail: no round trip here — the incoming parser reads no nested
	// run (the local echo takes its spans from the segments, not from it).
}
