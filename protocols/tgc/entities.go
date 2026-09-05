package tgc

import (
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/model"
)

// spansOf turns the UTF-16 entity offsets into rune offsets and keeps only
// the kinds the UI styles. The mention family (hashtag, cashtag, bot
// command, @username) all land on SpanMention.
func spansOf(text string, ents []tg.MessageEntityClass) []model.Span {
	runes := []rune(text)
	off := make([]int, len(runes)+1) // off[i] = UTF-16 length of runes[:i]
	for i, r := range runes {
		n := utf16.RuneLen(r)
		if n < 0 {
			n = 1
		}
		off[i+1] = off[i] + n
	}
	var out []model.Span
	for _, e := range ents {
		ri, rj := runeRange(off, e.GetOffset(), e.GetOffset()+e.GetLength())
		if ri >= rj {
			continue
		}
		s := model.Span{Start: ri, End: rj}
		switch v := e.(type) {
		case *tg.MessageEntityBold:
			s.Kind = model.SpanBold
		case *tg.MessageEntityItalic:
			s.Kind = model.SpanItalic
		case *tg.MessageEntityUnderline:
			s.Kind = model.SpanUnderline
		case *tg.MessageEntityStrike:
			s.Kind = model.SpanStrike
		case *tg.MessageEntityCode:
			s.Kind = model.SpanCode
		case *tg.MessageEntityPre:
			s.Kind, s.Lang = model.SpanPre, v.Language
		case *tg.MessageEntitySpoiler:
			s.Kind = model.SpanSpoiler
		case *tg.MessageEntityBlockquote:
			s.Kind = model.SpanQuote
		case *tg.MessageEntityURL:
			s.Kind, s.URL = model.SpanURL, href(string(runes[ri:rj]))
		case *tg.MessageEntityTextURL:
			s.Kind, s.URL = model.SpanURL, v.URL
		case *tg.MessageEntityEmail:
			s.Kind, s.URL = model.SpanURL, "mailto:"+string(runes[ri:rj])
		case *tg.MessageEntityMentionName:
			s.Kind, s.UserID = model.SpanMention, v.UserID
		case *tg.MessageEntityMention, *tg.MessageEntityHashtag,
			*tg.MessageEntityCashtag, *tg.MessageEntityBotCommand:
			s.Kind = model.SpanMention
		default:
			continue
		}
		out = append(out, s)
	}
	return out
}

// stylingOf turns neutral segments into gotd styling options. One "\n"
// between the blocks, like the local echo of the sender counts it.
func stylingOf(segs []model.Seg) []styling.StyledTextOption {
	out := make([]styling.StyledTextOption, 0, 2*len(segs))
	for i, s := range segs {
		if i > 0 {
			out = append(out, styling.Plain("\n"))
		}
		switch s.Kind {
		case model.SegPre:
			out = append(out, styling.Pre(s.Text, s.Lang))
		case model.SegItalic:
			out = append(out, styling.Italic(s.Text))
		default:
			out = append(out, styling.Plain(s.Text))
		}
	}
	return out
}

// runeRange gives the rune range for the UTF-16 range [start, end) of an
// entity. off grows strictly, so a binary search does it: the linear scan
// cost one pass over the whole text per entity (25 ms for 4096 characters
// and 20 000 entities).
func runeRange(off []int, start, end int) (int, int) {
	n := len(off) - 1
	ri := sort.SearchInts(off[:n], start)
	return ri, ri + sort.SearchInts(off[ri:n], end)
}

func href(s string) string {
	if strings.Contains(s, "://") {
		return s
	}
	return "https://" + s
}
