package tgc

import (
	"context"
	"fmt"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/model"
)

// SearchMedia asks messages.search with the filter of the tab from the
// message asked, keeps the media of the tab with a small picture for their
// cell, and names a next page only after a full one.
func TestSearchMedia(t *testing.T) {
	ev := make(chan model.Event, 4)
	photo := func(id int) tg.MessageClass {
		return &tg.Message{ID: id, PeerID: &tg.PeerUser{UserID: 7}, Date: 1700000000 + id, Media: &tg.MessageMediaPhoto{
			Photo: &tg.Photo{ID: int64(id), Sizes: []tg.PhotoSizeClass{
				&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 1000}, &tg.PhotoSize{Type: "y", W: 800, H: 600, Size: 5000}}}}}
	}
	var full []tg.MessageClass
	for id := 100; id > 100-mediaPage; id-- {
		if id%2 == 0 {
			full = append(full, photo(id))
		} else { // a text the server would not send, dropped all the same
			full = append(full, &tg.Message{ID: id, PeerID: &tg.PeerUser{UserID: 7}, Date: 1700000000 + id, Message: "hi"})
		}
	}
	inv := &fakeInvoker{answer: func(req bin.Encoder) (any, error) {
		r, ok := req.(*tg.MessagesSearchRequest)
		if !ok {
			return nil, fmt.Errorf("unexpected request %T", req)
		}
		if _, ok := r.Filter.(*tg.InputMessagesFilterPhotoVideo); !ok {
			return nil, fmt.Errorf("filter %T", r.Filter)
		}
		page := full
		if r.OffsetID != 101 {
			page = full[:3]
		}
		return &tg.MessagesMessagesSlice{Count: 1000, Users: []tg.UserClass{withName(7, "alice")}, Messages: page}, nil
	}}
	c := fakeClient(inv, ev)
	chat := &model.Chat{ID: 7, Kind: model.ChatUser, Peer: &tg.InputPeerUser{UserID: 7}}

	c.SearchMedia(context.Background(), chat, model.TabMedia, 101)
	e := next(t, ev).(model.EvMedia)
	if e.Err != "" || len(e.Items) != mediaPage/2 || e.Next != 100-mediaPage+1 || e.Before != 101 {
		t.Fatalf("full page: err %q, %d items, next %d; want %d photos, next %d", e.Err, len(e.Items), e.Next, mediaPage/2, 100-mediaPage+1)
	}
	if it := e.Items[0]; it.Msg.ID != 100 || it.Msg.Media.W != 800 || it.Thumb == nil || it.Thumb.W != 320 {
		t.Fatalf("first item: id %d, media %+v, thumb %+v; want 100, the 800 px size and a 320 px picture", it.Msg.ID, it.Msg.Media, it.Thumb)
	}

	c.SearchMedia(context.Background(), chat, model.TabMedia, 50)
	if e := next(t, ev).(model.EvMedia); e.Err != "" || len(e.Items) != 2 || e.Next != 0 {
		t.Fatalf("short page: err %q, %d items, next %d; want 2 photos and the end", e.Err, len(e.Items), e.Next)
	}
}
