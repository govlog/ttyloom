package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// benchUI : 500 chats in the sidebar, one window of 2000 messages shown.
func benchUI() *UI {
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, th: theme.Terminal(),
		cfg: &config.Config{SidebarSort: "recent", Hover: config.HoverMenu, Timestamps: true},
		t:   &term.Term{Cols: 120, Rows: 40}, side: sideChats, sideW: 26, folded: map[string]bool{},
		chats: map[model.ChatKey]*model.Chat{}, images: "off"}
	now := time.Now()
	for i := 1; i <= 500; i++ {
		c := &model.Chat{Net: model.NetTelegram, ID: int64(i), Kind: model.ChatGroup, Title: fmt.Sprintf("Salon numéro %d avec un titre long", i),
			LastDate: now.Add(-time.Duration(i) * time.Minute), Unread: i % 3}
		u.chats[c.Key()] = c
		u.chatList = append(u.chatList, c)
	}
	w := u.ws.New(false)
	w.Chat = u.chatList[0]
	for i := 1; i <= 2000; i++ {
		w.Items = append(w.Items, &Item{Msg: &model.Msg{Net: model.NetTelegram, ChatID: 1, ID: i, Date: now, From: "alice", FromID: int64(i % 5),
			Text: fmt.Sprintf("message %d — some text that wraps over a couple of lines when the window is narrow enough", i)}})
	}
	return u
}

func BenchmarkSideBlock(b *testing.B) {
	u := benchUI()
	b.ReportAllocs()
	for range b.N {
		u.sideBlock(-1)
	}
}

func BenchmarkMarqueeTick(b *testing.B) {
	u := benchUI()
	b.ReportAllocs()
	for range b.N {
		u.marqueeTick(time.Now())
	}
}

func BenchmarkLineItemsCached(b *testing.B) {
	u := benchUI()
	w := u.view()
	w.LineItems(u.opts())
	b.ReportAllocs()
	for range b.N {
		w.LineItems(u.opts())
	}
}

func BenchmarkLineItemsCold(b *testing.B) {
	u := benchUI()
	w := u.view()
	b.ReportAllocs()
	for range b.N {
		w.Invalidate()
		w.LineItems(u.opts())
	}
}

func BenchmarkScroll(b *testing.B) {
	u := benchUI()
	w := u.view()
	b.ReportAllocs()
	for range b.N {
		u.scroll(w, 3)
		u.scroll(w, -3)
	}
}
