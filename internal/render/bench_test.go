package render

import (
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/theme"
)

// longMsg : a message at the Telegram cap, a span every 80 runes.
func longMsg() *model.Msg {
	text := strings.Repeat("lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor ", 52)
	var ents []model.Span
	for i := 0; i+40 < 4000; i += 80 {
		ents = append(ents, model.Span{Start: i, End: i + 40, Kind: model.SpanBold})
	}
	return &model.Msg{ID: 1, From: "alice", FromID: 7, Text: text, Entities: ents}
}

func BenchmarkMessageLong(b *testing.B) {
	m := longMsg()
	o := Opts{Width: 100, Theme: theme.Terminal(), Timestamps: true, Caps: func(*model.Msg) model.Caps { return model.AllCaps() }}
	b.ReportAllocs()
	for range b.N {
		Message(m, o)
	}
}

func BenchmarkRunsLong(b *testing.B) {
	m := longMsg()
	b.ReportAllocs()
	for range b.N {
		Runs(m.Text, m.Entities, theme.Style{}, theme.Terminal())
	}
}

func BenchmarkWidth(b *testing.B) {
	s := strings.Repeat("héllo wörld 👍 ", 12)
	b.ReportAllocs()
	for range b.N {
		Width(s)
	}
}

func BenchmarkWrapLong(b *testing.B) {
	m := longMsg()
	sp := []Span{{m.Text, theme.Style{}}}
	b.ReportAllocs()
	for range b.N {
		Wrap(sp, 100)
	}
}
