package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
)

type mediaAsk struct {
	tab    model.MediaFilter
	before int
}

// mediaBackend : a network that searches media (Telegram, Discord).
type mediaBackend struct {
	*fakeBackend
	asks []mediaAsk
}

func (f *mediaBackend) SearchMedia(_ context.Context, _ *model.Chat, t model.MediaFilter, before int) {
	f.asks = append(f.asks, mediaAsk{t, before})
}

// mediaItems : n photos from id down, each with a small picture.
func mediaItems(from, n int) []model.MediaItem {
	var out []model.MediaItem
	for id := from; id > from-n; id-- {
		out = append(out, model.MediaItem{
			Msg:   model.Msg{ChatID: 1, ID: id, From: "alice", Media: &model.Media{Kind: model.MediaPhoto, Loc: id, Ext: ".jpg", W: 800, H: 600, Size: 50_000}},
			Thumb: &model.Media{Kind: model.MediaPhoto, Loc: -id, Ext: ".jpg", W: 320, H: 240}})
	}
	return out
}

// Ctrl+M lists the photos and videos of the conversation, newest first: the
// cells on the screen fetch their small picture into the cache, the next page
// is asked from the oldest id once the grid nears the end, a page listed
// again adds nothing, Tab starts the next tab from its newest, and j goes to
// the message.
func TestMediaBox(t *testing.T) {
	u, fb := gifUI()
	b := &mediaBackend{fakeBackend: fb}
	u.nets[netTelegram] = b
	u.ws.Cur = 1
	w := u.ws.List[1]
	w.Upsert(&model.Msg{Net: netTelegram, ChatID: 1, ID: 99, From: "alice", Text: "look"})

	u.key(term.Key{Code: term.Ctrl, Rune: 'm'})
	if u.mbox == nil || len(b.asks) != 1 || b.asks[0] != (mediaAsk{model.TabMedia, 0}) {
		t.Fatalf("Ctrl+M: box %v, asks %v", u.mbox != nil, b.asks)
	}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvMedia{ChatID: 1, Filter: model.TabMedia, Items: mediaItems(100, 8), Next: 93}})
	u.overlay() // a frame: the cells on the screen ask for their picture, the end is near
	if len(fb.downloads) != 8 || !strings.Contains(fb.downloads[0], "thumbs") {
		t.Fatalf("downloads %v: want the 8 small pictures in the cache", fb.downloads)
	}
	if len(b.asks) != 2 || b.asks[1] != (mediaAsk{model.TabMedia, 93}) {
		t.Fatalf("asks %v: want the page before 93", b.asks)
	}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvMedia{ChatID: 1, Filter: model.TabMedia, Before: 93, Items: mediaItems(100, 8)}})
	if len(u.mbox.items) != 8 || u.mbox.next != 0 {
		t.Fatalf("overlapping page: %d items, next %d; want 8 and the end", len(u.mbox.items), u.mbox.next)
	}

	u.mboxKey(term.Key{Code: term.Right}) // id 99
	u.mboxKey(term.Key{Code: term.None, Rune: 'j'})
	if u.mbox != nil || w.Sel == nil || w.Sel.Msg.ID != 99 {
		t.Fatalf("j: box %v, selection %+v; want closed and message 99 selected", u.mbox != nil, w.Sel)
	}

	u.openMediaBox("")
	u.mboxKey(term.Key{Code: term.Tab})
	if u.mbox.tab != model.TabGIFs || len(u.mbox.items) != 0 || b.asks[len(b.asks)-1] != (mediaAsk{model.TabGIFs, 0}) {
		t.Fatalf("Tab: tab %v, %d items, asks %v", u.mbox.tab, len(u.mbox.items), b.asks)
	}
}

// A network with no media search (IRC) says so and opens nothing.
func TestMediaBoxUnsupported(t *testing.T) {
	u, _ := gifUI()
	u.ws.Cur = 1
	u.openMediaBox("")
	if u.mbox != nil {
		t.Fatal("box opened on a network that cannot search media")
	}
}

// "o" on a GIF whose page opens asks on the input line: the box closes first,
// it would hold the y/n. An error on the first page is asked again at the next
// move.
func TestMediaBoxOpenPageAndRetry(t *testing.T) {
	u, fb := gifUI()
	b := &mediaBackend{fakeBackend: fb}
	u.nets[netTelegram] = b
	u.ws.Cur = 1
	u.openMediaBox("")
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvMedia{ChatID: 1, Filter: model.TabMedia, Err: "index not ready"}})
	u.mboxKey(term.Key{Code: term.Down})
	if len(b.asks) != 2 || b.asks[1] != (mediaAsk{model.TabMedia, 0}) {
		t.Fatalf("asks %v: want the first page asked again after a move", b.asks)
	}
	gif := model.MediaItem{Msg: model.Msg{ChatID: 1, ID: 7, Media: &model.Media{Kind: model.MediaGIF, Loc: 7, URL: "https://tenor.com/view/x"}}}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvMedia{ChatID: 1, Filter: model.TabMedia, Items: []model.MediaItem{gif}}})
	u.mboxKey(term.Key{Code: term.None, Rune: 'o'})
	if u.mbox != nil || u.ask == nil {
		t.Fatalf("box open %v, question %v: want the box closed and the question asked", u.mbox != nil, u.ask != nil)
	}
}
