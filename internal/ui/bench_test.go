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
		c := &model.Chat{Net: netTelegram, ID: int64(i), Kind: model.ChatGroup, Title: fmt.Sprintf("Salon numéro %d avec un titre long", i),
			LastDate: now.Add(-time.Duration(i) * time.Minute), Unread: i % 3}
		u.chats[c.Key()] = c
		u.chatList = append(u.chatList, c)
	}
	w := u.ws.New(false)
	w.Chat = u.chatList[0]
	for i := 1; i <= 2000; i++ {
		w.Items = append(w.Items, &Item{Msg: &model.Msg{Net: netTelegram, ChatID: 1, ID: i, Date: now, From: "alice", FromID: int64(i % 5),
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

// BenchmarkReadInbox : one read receipt with 100 windows of 2000 messages open.
func BenchmarkReadInbox(b *testing.B) {
	u := &UI{ws: NewWindows(), agg: &Window{}}
	for i := 1; i <= 100; i++ {
		w := u.ws.New(true)
		w.Chat = &model.Chat{Net: netTelegram, ID: int64(i)}
		for j := 1; j <= 2000; j++ {
			w.Items = append(w.Items, &Item{Msg: &model.Msg{Net: netTelegram, ChatID: int64(i), ID: j}})
		}
	}
	c := u.ws.List[1].Chat
	b.ReportAllocs()
	b.ResetTimer()
	for n := range b.N {
		u.readInbox(c, n+1, 0, false) // a new max each time: the sweep runs
	}
}

// benchGuildsUI : benchUI with 1000 chats on two networks — half of them
// Discord channels in 20 guilds — 100 windows open and the sidebar sorted a→z,
// the dearest sort.
func benchGuildsUI() *UI {
	u := benchUI()
	u.cfg.SidebarSort = "alpha"
	now := time.Now()
	for i := 501; i <= 1000; i++ {
		g := fmt.Sprintf("Guilde %d", i%20)
		c := &model.Chat{Net: "discord", ID: int64(i), Kind: model.ChatGroup, Group: g, Title: fmt.Sprintf("%s / #salon-%d", g, i),
			LastDate: now.Add(-time.Duration(i) * time.Minute)}
		u.chats[c.Key()] = c
		u.chatList = append(u.chatList, c)
	}
	for _, c := range u.chatList[1:100] {
		u.bindChat(u.ws.New(true), c)
	}
	return u
}

func BenchmarkSideBlockGuilds(b *testing.B) {
	u := benchGuildsUI()
	b.ReportAllocs()
	for range b.N {
		u.sideBlock(-1)
	}
}

func BenchmarkMarqueeTickGuilds(b *testing.B) {
	u := benchGuildsUI()
	b.ReportAllocs()
	for range b.N {
		u.marqueeTick(time.Now())
	}
}

// BenchmarkGlobalDelete : one Telegram global delete of 100 ids with 100
// windows of 2000 messages open, and the aggregate holding them all.
func BenchmarkGlobalDelete(b *testing.B) {
	u := &UI{ws: NewWindows(), agg: &Window{max: 200000}, dirty: map[model.ChatKey]bool{}}
	for i := 1; i <= 100; i++ {
		w := u.ws.New(true)
		w.Chat = &model.Chat{Net: netTelegram, ID: int64(i)}
		for j := 1; j <= 2000; j++ {
			m := &model.Msg{Net: netTelegram, ChatID: int64(i), ID: i*10000 + j}
			w.Items = append(w.Items, &Item{Msg: m})
			u.agg.Items = append(u.agg.Items, &Item{Msg: m})
		}
	}
	ids := make([]int, 100)
	for k := range ids {
		ids[k] = (k+1)*10000 + 1000 // one message in each of the first 100 windows
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvDeleted{IDs: ids}})
	}
}
