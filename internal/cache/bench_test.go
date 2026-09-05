package cache

import (
	"fmt"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/model"
)

func BenchmarkLoadHistory2000(b *testing.B) {
	c := New(b.TempDir(), 2000)
	msgs := make([]model.Msg, 2000)
	for i := range msgs {
		msgs[i] = model.Msg{ID: i + 1, ChatID: 1, Date: time.Now(), From: "alice", FromID: 7,
			Text:     fmt.Sprintf("message %d with some body text of a usual length for a chat", i),
			Entities: []model.Span{{Start: 0, End: 7, Kind: model.SpanBold}}}
	}
	if err := c.SaveHistory(1, msgs); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := c.LoadHistory(1); err != nil {
			b.Fatal(err)
		}
	}
}
