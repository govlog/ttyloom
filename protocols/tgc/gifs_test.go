package tgc

import (
	"context"
	"fmt"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/model"
)

// TestSearchGifs : @gif is resolved once (contacts.resolveUsername), the
// animated documents of messages.getInlineBotResults become previewable GIFs
// and the query id travels in the send handle; the send carries that id and
// the result id, and EvSent the tmp id.
func TestSearchGifs(t *testing.T) {
	ev := make(chan model.Event, 4)
	doc := &tg.Document{ID: 55, AccessHash: 9, MimeType: "video/mp4", Size: 4096,
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAnimated{}, &tg.DocumentAttributeVideo{W: 320, H: 240, Duration: 2}}}
	bot := withName(140, "gif")
	bot.Bot = true
	inv := &fakeInvoker{answer: func(req bin.Encoder) (any, error) {
		switch r := req.(type) {
		case *tg.ContactsResolveUsernameRequest:
			return &tg.ContactsResolvedPeer{Peer: &tg.PeerUser{UserID: 140}, Users: []tg.UserClass{bot}}, nil
		case *tg.MessagesGetInlineBotResultsRequest:
			if r.Query != "cat" {
				return nil, fmt.Errorf("query %q", r.Query)
			}
			return &tg.MessagesBotResults{QueryID: 777, Results: []tg.BotInlineResultClass{
				&tg.BotInlineMediaResult{ID: "r1", Type: "gif", Document: doc},
				&tg.BotInlineResult{ID: "r2", Type: "article"}, // no document: nothing to show
			}}, nil
		}
		return nil, fmt.Errorf("unexpected %T", req)
	}}
	c := fakeClient(inv, ev)
	chat := &model.Chat{ID: 7, Peer: &tg.InputPeerUser{UserID: 7}}
	c.SearchGifs(context.Background(), chat, "cat")
	e := next(t, ev).(model.EvGifs)
	if e.Err != "" || e.Query != "cat" || len(e.Gifs) != 1 {
		t.Fatalf("gifs: %+v", e)
	}
	g := e.Gifs[0]
	if g.Preview == nil || g.Preview.Kind != model.MediaGIF || !g.Preview.Previewable() || g.Preview.W != 320 {
		t.Fatalf("preview: %+v", g.Preview)
	}
	if ref, ok := g.Send.(inlineRef); !ok || ref.QueryID != 777 || ref.ID != "r1" {
		t.Fatalf("send handle: %#v", g.Send)
	}
	c.SearchGifs(context.Background(), chat, "cat") // the bot is kept: no second resolve
	next(t, ev)
	resolves := 0
	for _, r := range inv.calls {
		if _, ok := r.(*tg.ContactsResolveUsernameRequest); ok {
			resolves++
		}
	}
	if resolves != 1 {
		t.Fatalf("@gif resolved %d times", resolves)
	}

	inv.answer = func(req bin.Encoder) (any, error) {
		r, ok := req.(*tg.MessagesSendInlineBotResultRequest)
		if !ok || r.QueryID != 777 || r.ID != "r1" || r.RandomID == 0 {
			return nil, fmt.Errorf("bad send: %+v", req)
		}
		return &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 31, RandomID: r.RandomID},
			&tg.UpdateNewMessage{Message: &tg.Message{ID: 31, PeerID: &tg.PeerUser{UserID: 7}}}}}, nil
	}
	c.SendGif(context.Background(), chat, g, 42)
	if s := next(t, ev).(model.EvSent); s.Err != "" || s.TmpID != 42 || s.ID != 31 || s.ChatID != 7 {
		t.Fatalf("sent: %+v", s)
	}
	// A handle of another network: refused at once, never sent.
	c.SendGif(context.Background(), chat, model.Gif{Send: "https://tenor.com/x"}, 43)
	if s := next(t, ev).(model.EvSent); s.Err == "" || s.TmpID != 43 {
		t.Fatalf("foreign handle: %+v", s)
	}
}
