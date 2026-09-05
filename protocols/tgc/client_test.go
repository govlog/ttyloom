package tgc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/log"

	"github.com/gotd/td/bin"

	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/govlog/ttyloom/internal/i18n"

	"github.com/govlog/ttyloom/internal/model"
)

// withName : a user whose @username flag is really set (SetUsername, not the
// raw field: peers.User.Username reads the flag).
func withName(id int64, name string) *tg.User {
	u := &tg.User{ID: id}
	u.SetUsername(name)
	return u
}

// offline gives a Client with no api: any network call would panic the test.
func offline(events chan<- model.Event) *Client {
	return &Client{peers: peers.Options{}.Build(nil), seen: map[int64]peers.Peer{}, Poster: model.Poster{Events: events},
		dlSem: make(chan struct{}, 3), ulSem: make(chan struct{}, 2)}
}

// New builds the whole gotd stack without opening a single connection; the
// session file is not read before Run.
func TestNewOffline(t *testing.T) {
	ev := make(chan model.Event, 8)
	c := New(Config{AppID: 1, AppHash: "hash", SessionPath: filepath.Join(t.TempDir(), "session.json")}, ev)
	if c.api == nil || c.peers == nil || c.sender == nil || c.gaps == nil || c.hook == nil {
		t.Fatalf("incomplete client: %+v", c)
	}
	if c.loggedIn == nil {
		t.Fatal("QR login signal not armed")
	}
}

// safe: the error goes through, a panic becomes an event and not a crash
// (Post, PostNB and Guard themselves are tested with model.Poster).
func TestSafe(t *testing.T) {
	ev := make(chan model.Event, 4)
	c := offline(ev)
	if err := c.safe("ok", func() error { return nil }); err != nil {
		t.Fatalf("safe without panic: %v", err)
	}
	_ = c.safe("ko", func() error { panic("aïe") })
	if e := (<-ev).(model.EvLog); e.Level != "ERROR" {
		t.Fatalf("safe with panic: %+v", e)
	}
}

// logger : WARN and above only, attributes appended to the message.
func TestLogger(t *testing.T) {
	ev := make(chan model.Event, 4)
	l := logger{offline(ev)}
	ctx := context.Background()
	if l.Enabled(ctx, log.LevelInfo) {
		t.Fatal("info must not pass through")
	}
	l.Log(ctx, log.LevelInfo, "ignoré")
	l.Log(ctx, log.LevelWarn, "souci", log.String("clé", "valeur"))
	select {
	case e := <-ev:
		if e.(model.EvLog).Msg != "souci clé=valeur" {
			t.Fatalf("%+v", e)
		}
	default:
		t.Fatal("the WARN was not delivered")
	}
}

// floodTrip opens the breaker on a FLOOD_WAIT and warns once, not once per
// refused call.
func TestFloodBreaker(t *testing.T) {
	ev := make(chan model.Event, 8)
	c := offline(ev)
	if !c.floodOK() {
		t.Fatal("breaker closed at start")
	}
	c.floodTrip(errFlood()) // not a FLOOD_WAIT: nothing happens
	if !c.floodOK() {
		t.Fatal("arbitrary error: the breaker must not open")
	}
	fw := tgerr.New(420, "FLOOD_WAIT_30")
	c.floodTrip(fw)
	if c.floodOK() {
		t.Fatal("FLOOD_WAIT: the breaker should be open")
	}
	e, ok := (<-ev).(model.EvLog)
	if !ok || e.Level != "WARN" {
		t.Fatalf("warning: %+v", e)
	}
	c.floodTrip(fw) // still open: no second warning
	select {
	case e := <-ev:
		t.Fatalf("second warning: %+v", e)
	default:
	}
}

// tdlibID, isMin, nick, peerOf, nameOf, chatOf : no network at all.
func TestPeerHelpers(t *testing.T) {
	c := offline(nil)
	if got := tdlibID(&tg.PeerUser{UserID: 42}); got != 42 {
		t.Fatalf("tdlibID user: %d", got)
	}
	if tdlibID(&tg.PeerChat{ChatID: 7}) >= 0 || tdlibID(&tg.PeerChannel{ChannelID: 7}) >= 0 {
		t.Fatal("group and channel: negative id expected")
	}
	if got := tdlibID(nil); got != 0 {
		t.Fatalf("unknown peer: %d", got)
	}
	if !isMin(c.peers.User(&tg.User{ID: 1, Min: true})) {
		t.Fatal("min user")
	}
	if isMin(c.peers.Chat(&tg.Chat{ID: 2})) {
		t.Fatal("small group: never min")
	}
	if got := nick(c.peers.User(withName(1, "alice"))); got != "alice" {
		t.Fatalf("nick with username: %q", got)
	}
	if got := nick(c.peers.User(&tg.User{ID: 1, FirstName: "Alice"})); got != "Alice" {
		t.Fatalf("nick without username: %q", got)
	}
	// A min entity never replaces a peer already known.
	full := c.remember(c.peers.User(&tg.User{ID: 5, FirstName: "Complet"}))
	if got := c.remember(c.peers.User(&tg.User{ID: 5, FirstName: "Min", Min: true})); nick(got) != nick(full) {
		t.Fatalf("min overrides the known peer: %q", nick(got))
	}
	ent := peer.NewEntities(
		map[int64]*tg.User{9: {ID: 9, FirstName: "Bob"}},
		map[int64]*tg.Chat{8: {ID: 8, Title: "le groupe"}},
		map[int64]*tg.Channel{7: {ID: 7, Title: "le canal", Broadcast: true}})
	c.rememberAll(ent)
	for _, p := range []tg.PeerClass{&tg.PeerUser{UserID: 9}, &tg.PeerChat{ChatID: 8}, &tg.PeerChannel{ChannelID: 7}} {
		if _, ok := c.peerOf(peer.Entities{}, p); !ok {
			t.Fatalf("forgotten peer: %T", p) // rememberAll should have kept the three
		}
	}
	if got := c.nameOf(ent, &tg.PeerUser{UserID: 9}); got != "Bob" {
		t.Fatalf("nameOf: %q", got)
	}
	if got := c.nameOf(ent, nil); got != "?" {
		t.Fatalf("nameOf(nil): %q", got)
	}
	if got := c.nameOf(peer.Entities{}, &tg.PeerUser{UserID: 404}); got != "?" {
		t.Fatalf("nameOf unknown: %q", got)
	}
	// chatOf : kind, title, username and photo, straight from the peer.
	bob := withName(9, "bob")
	bob.FirstName = "Bob"
	bob.SetPhoto(&tg.UserProfilePhoto{PhotoID: 11})
	u := c.peers.User(bob)
	ch := c.chatOf(u)
	if ch.Kind != model.ChatUser || ch.Title != "Bob" || ch.Username != "bob" || ch.PhotoLoc == nil || ch.Channel {
		t.Fatalf("chatOf user: %+v", ch)
	}
	if ch := c.chatOf(c.peers.Chat(&tg.Chat{ID: 8, Title: "le groupe"})); ch.Kind != model.ChatGroup || ch.PhotoLoc != nil || ch.Channel {
		t.Fatalf("chatOf group: %+v", ch)
	}
	if ch := c.chatOf(c.peers.Channel(&tg.Channel{ID: 7, Title: "le canal", Broadcast: true,
		Photo: &tg.ChatPhoto{PhotoID: 12}})); ch.Kind != model.ChatChannel || ch.PhotoLoc == nil || !ch.Channel {
		t.Fatalf("chatOf channel: %+v", ch)
	}
	// Supergroup: Kind stays ChatGroup, but Channel is true — that's what backs
	// the EvDeleted guard on the ui side (channel ids never in a global delete).
	if ch := c.chatOf(c.peers.Channel(&tg.Channel{ID: 6, Title: "le supergroupe"})); ch.Kind != model.ChatGroup || !ch.Channel {
		t.Fatalf("chatOf supergroup: %+v", ch)
	}
}

// chatParts / channelParts : creator and admins marked, server order kept,
// the forms with no user id left out.
func TestPartsIDs(t *testing.T) {
	ids, admins := chatParts([]tg.ChatParticipantClass{
		&tg.ChatParticipant{UserID: 3},
		&tg.ChatParticipantCreator{UserID: 1},
		&tg.ChatParticipantAdmin{UserID: 2},
	})
	if len(ids) != 3 || ids[0] != 3 || !admins[1] || !admins[2] || admins[3] {
		t.Fatalf("chatParts: %v %v", ids, admins)
	}
	ids, admins = channelParts([]tg.ChannelParticipantClass{
		&tg.ChannelParticipant{UserID: 4},
		&tg.ChannelParticipantSelf{UserID: 5},
		&tg.ChannelParticipantCreator{UserID: 1},
		&tg.ChannelParticipantAdmin{UserID: 2},
		&tg.ChannelParticipantBanned{}, // no user id: left out
	})
	if len(ids) != 4 || ids[0] != 4 || !admins[1] || !admins[2] || admins[5] {
		t.Fatalf("channelParts: %v %v", ids, admins)
	}
}

// entities + lines : the members of a batch, named without a network lookup.
func TestClientLines(t *testing.T) {
	c := offline(nil)
	c.me = &tg.User{ID: 3}
	ent := c.entities([]tg.UserClass{
		withName(1, "alice"),
		&tg.User{ID: 2, FirstName: "Bob"},
		&tg.User{ID: 3, FirstName: "Moi"},
	})
	got := c.lines([]int64{2, 1, 3}, map[int64]bool{1: true}, ent)
	if len(got) != 3 || got[0].Query != "@alice" || !strings.HasPrefix(got[0].Text, "★ ") {
		t.Fatalf("admin first: %+v", got)
	}
	if got[1].Text != "Bob" || got[1].Query != "2" {
		t.Fatalf("no username: %+v", got[1])
	}
	if !strings.Contains(got[2].Text, "moi") {
		t.Fatalf("\"moi\" marker: %+v", got[2])
	}
	if pluralKey(1, "member_count") == pluralKey(3, "member_count") {
		t.Fatal("singular and plural should differ")
	}
	if errNoParts() == nil || errFlood() == nil {
		t.Fatal("errors built fresh on each read")
	}
	if inputChannel(&tg.InputPeerChannel{ChannelID: 7, AccessHash: 8}).ChannelID != 7 {
		t.Fatal("inputChannel")
	}
}

// Reactions: emoji of a raw reaction, model conversion, and the update dug
// out of an RPC answer.
func TestReactionHelpers(t *testing.T) {
	if got := emojiOf(&tg.ReactionEmoji{Emoticon: "👍"}); got != "👍" {
		t.Fatalf("emojiOf: %q", got)
	}
	if got := emojiOf(&tg.ReactionCustomEmoji{DocumentID: 1}); got != "⭐" {
		t.Fatalf("custom emoji: %q", got)
	}
	if got := emojiOf(&tg.ReactionEmpty{}); got != "" {
		t.Fatalf("empty reaction: %q", got)
	}
	mine := tg.ReactionCount{Reaction: &tg.ReactionEmoji{Emoticon: "❤"}, Count: 2}
	mine.SetChosenOrder(0)
	rs := reactionsOf([]tg.ReactionCount{
		{Reaction: &tg.ReactionEmpty{}, Count: 9}, // no emoji: dropped
		{Reaction: &tg.ReactionEmoji{Emoticon: "👍"}, Count: 1},
		mine,
	})
	if len(rs) != 2 || rs[0].Emoji != "👍" || rs[0].Mine || !rs[1].Mine || rs[1].Count != 2 {
		t.Fatalf("reactionsOf: %+v", rs)
	}
	up := &tg.UpdateMessageReactions{MsgID: 5}
	for _, u := range []tg.UpdatesClass{
		&tg.UpdateShort{Update: up},
		&tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateUserTyping{}, up}},
		&tg.UpdatesCombined{Updates: []tg.UpdateClass{up}},
	} {
		if got := reactionUpdate(u); got != up {
			t.Fatalf("reactionUpdate %T: %+v", u, got)
		}
	}
	if got := reactionUpdate(&tg.UpdatesTooLong{}); got != nil {
		t.Fatalf("form without reaction: %+v", got)
	}
	if got := reactionUpdate(&tg.UpdateShort{Update: &tg.UpdateUserTyping{}}); got != nil {
		t.Fatalf("update without reaction: %+v", got)
	}
	// reactionReason : only the three refusals worth showing.
	for _, code := range []string{"REACTION_INVALID", "REACTIONS_TOO_MANY", "PREMIUM_ACCOUNT_REQUIRED"} {
		if reactionReason(tgerr.New(400, code)) == "" {
			t.Fatalf("%s with no explanation", code)
		}
	}
	if got := reactionReason(tgerr.New(400, "AUTRE_CHOSE")); got != "" {
		t.Fatalf("arbitrary error: %q", got)
	}
}

// describeAction : the known service messages, the raw type name otherwise.
func TestDescribeAction(t *testing.T) {
	for _, c := range []struct {
		a    tg.MessageActionClass
		want string
	}{
		{&tg.MessageActionChatAddUser{}, "a rejoint"},
		{&tg.MessageActionChatJoinedByLink{}, "a rejoint"},
		{&tg.MessageActionChatDeleteUser{}, "a quitté"},
		{&tg.MessageActionPinMessage{}, "a épinglé un message"},
	} {
		if got := describeAction(c.a); got != c.want {
			t.Errorf("%T: %q, %q expected", c.a, got, c.want)
		}
	}
	if got := describeAction(&tg.MessageActionChatEditTitle{Title: "Nouveau"}); !strings.Contains(got, "Nouveau") {
		t.Errorf("rename: %q", got)
	}
	if got := describeAction(&tg.MessageActionChatCreate{Title: "Groupe"}); !strings.Contains(got, "Groupe") {
		t.Errorf("group creation: %q", got)
	}
	if got := describeAction(&tg.MessageActionChannelCreate{Title: "Canal"}); !strings.Contains(got, "Canal") {
		t.Errorf("channel creation: %q", got)
	}
	if describeAction(&tg.MessageActionContactSignUp{}) == "" {
		t.Error("signup: text expected")
	}
	// Unknown action: the raw name without its prefix, never empty.
	if got := describeAction(&tg.MessageActionGiftPremium{}); got == "" || strings.HasPrefix(got, "messageAction") {
		t.Errorf("unknown action: %q", got)
	}
}

// mimeOf : extension of the local file, unknown = octet-stream.
func TestMimeOf(t *testing.T) {
	for _, c := range []struct{ path, want string }{
		{"/tmp/photo.JPG", "image/jpeg"},
		{"/tmp/clip.mp4", "video/mp4"},
		{"/tmp/sans-extension", "application/octet-stream"},
		{"/tmp/truc.zzzz", "application/octet-stream"},
	} {
		if got := mimeOf(c.path); got != c.want {
			t.Errorf("mimeOf(%q) = %q, %q expected", c.path, got, c.want)
		}
	}
	if i18n.LocalTime(time.Date(2026, 8, 30, 14, 5, 0, 0, time.Local)) == "" {
		t.Error("localTime empty")
	}
}

// docMedia : one label and one kind per document form.
func TestDocMediaKinds(t *testing.T) {
	doc := func(mime string, attrs ...tg.DocumentAttributeClass) *tg.Document {
		return &tg.Document{ID: 1, AccessHash: 2, MimeType: mime, Size: 4096, Attributes: attrs}
	}
	m := docMedia(doc("image/webp", &tg.DocumentAttributeSticker{Alt: "😀"}))
	if m.Kind != model.MediaSticker || m.Animated || m.Label != "[sticker 😀]" {
		t.Fatalf("sticker: %+v", m)
	}
	if m := docMedia(doc("application/x-tgsticker", &tg.DocumentAttributeSticker{Alt: "🎉"})); !m.Animated {
		t.Fatalf("animated sticker: %+v", m)
	}
	m = docMedia(doc("audio/ogg", &tg.DocumentAttributeAudio{Duration: 5, Voice: true}))
	if m.Kind != model.MediaVoice || m.Label != "[voice 0:05]" {
		t.Fatalf("voice: %+v", m)
	}
	m = docMedia(doc("audio/mpeg",
		&tg.DocumentAttributeAudio{Duration: 200, Performer: "Groupe", Title: "Titre"},
		&tg.DocumentAttributeFilename{FileName: "piste.mp3"}))
	if m.Kind != model.MediaAudio || m.Name != "Groupe – Titre" {
		t.Fatalf("audio : %+v", m) // the file name must not overwrite the tags
	}
	m = docMedia(doc("video/mp4", &tg.DocumentAttributeVideo{Duration: 65, W: 640, H: 480}))
	if m.Kind != model.MediaVideo || m.Label != "[video 1:05 · 4 Ko]" || m.W != 640 {
		t.Fatalf("video: %+v", m)
	}
	m = docMedia(doc("image/png", &tg.DocumentAttributeImageSize{W: 10, H: 20},
		&tg.DocumentAttributeFilename{FileName: "capture.png"}))
	if m.Kind != model.MediaPhoto || m.Ext != ".png" || m.H != 20 {
		t.Fatalf("image sent as file: %+v", m)
	}
	m = docMedia(doc("application/pdf", &tg.DocumentAttributeFilename{FileName: "doc.pdf"}))
	if m.Kind != model.MediaFile || !strings.Contains(m.Label, "doc.pdf") {
		t.Fatalf("file: %+v", m)
	}
}

// upProgress : one event per whole percent, nothing on an unknown size.
func TestUploadProgress(t *testing.T) {
	ev := make(chan model.Event, 4)
	p := &upProgress{c: offline(ev)}
	ctx := context.Background()
	if err := p.Chunk(ctx, uploader.ProgressState{Total: 0, Uploaded: 10}); err != nil {
		t.Fatal(err)
	}
	if err := p.Chunk(ctx, uploader.ProgressState{Total: 100, Uploaded: 50}); err != nil {
		t.Fatal(err)
	}
	if e := (<-ev).(model.EvUpload); !strings.Contains(e.Text, "50") {
		t.Fatalf("progress: %+v", e)
	}
	p.Chunk(ctx, uploader.ProgressState{Total: 100, Uploaded: 50}) // same percent: no event
	select {
	case e := <-ev:
		t.Fatalf("duplicate: %+v", e)
	default:
	}
}

// TestSendAttrsVideo : a video must carry DocumentAttributeVideo, otherwise
// the receiver only sees an attachment. A probe that failed (dur = 0) falls
// back to a plain document.
func TestSendAttrsVideo(t *testing.T) {
	attrs := sendAttrs("video/mp4", 1920, 1080, 42500*time.Millisecond)
	if len(attrs) != 1 {
		t.Fatalf("attrs = %d, expected 1", len(attrs))
	}
	v, ok := attrs[0].(*tg.DocumentAttributeVideo)
	if !ok {
		t.Fatalf("attr = %T, expected *tg.DocumentAttributeVideo", attrs[0])
	}
	if v.W != 1920 || v.H != 1080 || v.Duration != 42.5 || !v.SupportsStreaming {
		t.Errorf("attr = %+v", v)
	}

	if a := sendAttrs("audio/mpeg", 0, 0, 3*time.Second); len(a) != 1 {
		t.Errorf("audio: %d attrs, expected 1", len(a))
	} else if au, ok := a[0].(*tg.DocumentAttributeAudio); !ok || au.Duration != 3 {
		t.Errorf("audio: %#v", a[0])
	}

	for _, c := range []struct {
		mt   string
		w, h int
		dur  time.Duration
	}{
		{"video/mp4", 1920, 1080, 0},          // ffprobe missing or failing
		{"video/mp4", 0, 0, time.Second},      // no video stream
		{"application/pdf", 0, 0, 0},          // ordinary document
		{"image/gif", 320, 240, time.Second},  // a gif stays a gif
		{"audio/webm", 320, 240, time.Second}, // webm video announced as audio/webm
	} {
		if a := sendAttrs(c.mt, c.w, c.h, c.dur); a != nil {
			t.Errorf("%s %dx%d %v: attrs = %#v, expected nil", c.mt, c.w, c.h, c.dur, a)
		}
	}
}

// WhoRead / WhoReacted : the branches with no network — unread (no call at
// all), reactions of a private chat (the authors come from the message).
func TestWhoOffline(t *testing.T) {
	ev := make(chan model.Event, 4)
	c := offline(ev)
	chat := &model.Chat{ID: 7, Kind: model.ChatUser, Title: "alice", Peer: &tg.InputPeerUser{UserID: 7}}
	c.WhoRead(context.Background(), chat, 5, 3) // 5 > 3: unread
	e := (<-ev).(model.EvWho)
	if e.ChatID != 7 || e.ID != 5 || e.React || e.Text != i18n.T("info_unread") {
		t.Fatalf("unread: %+v", e)
	}
	c.WhoReacted(context.Background(), chat, 5, []model.Reaction{{Emoji: "👍"}, {Emoji: "❤", Mine: true}})
	w := (<-ev).(model.EvWho)
	if w.ChatID != 7 || w.ID != 5 || !w.React || w.Text != "👍 alice · ❤ "+i18n.T("me") {
		t.Fatalf("reactions: %+v", w)
	}
}

// The opaque peer comes back typed by the central cast; a foreign handle gives nil.
func TestPeerCast(t *testing.T) {
	c := offline(make(chan model.Event, 1))
	if p := c.peer(&model.Chat{Peer: &tg.InputPeerUser{UserID: 7}}); p == nil {
		t.Fatal("tg peer expected")
	}
	if p := c.peer(&model.Chat{Peer: "pas-un-peer"}); p != nil {
		t.Fatal("foreign handle: nil expected")
	}
}

// The Telegram client is a complete Backend, every capability open.
func TestBackendComplete(t *testing.T) {
	var b model.Backend = offline(make(chan model.Event, 1))
	c := b.Caps()
	if !c.ReadReceipts || !c.Reactions || !c.Edit || !c.Whois || !c.GlobalSearch || !c.Contacts {
		t.Fatalf("caps: %+v", c)
	}
}

// An upload reports through the tmpID of the pending line the UI has already
// shown, exactly like a text send: without that the line would stay pending
// for ever when the send fails. Missing file: nothing goes to the network.
func TestUploadReportsTmpID(t *testing.T) {
	ev := make(chan model.Event, 4)
	c := offline(ev)
	c.SendPhoto(context.Background(), &model.Chat{ID: 5}, filepath.Join(t.TempDir(), "absent.png"), "", false, 42)
	select {
	case e := <-ev:
		s, ok := e.(model.EvSent)
		if !ok || s.ChatID != 5 || s.TmpID != 42 || s.Err == "" {
			t.Fatalf("event: %#v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
	}
}

// TestLoadDialogs : one page of dialogs becomes the chat list — unread
// counter, read marks and date of the last message included. messages.dialogs
// (not dialogsSlice) is the complete form: the gotd iterator stops after one
// batch (query/dialogs/iter.go:121).
func TestLoadDialogs(t *testing.T) {
	ev := make(chan model.Event, 4)
	inv := &fakeInvoker{answer: func(req bin.Encoder) (any, error) {
		if _, ok := req.(*tg.MessagesGetDialogsRequest); !ok {
			return nil, fmt.Errorf("per-dialog lookup: %T", req)
		}
		return &tg.MessagesDialogs{
			Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerUser{UserID: 7}, TopMessage: 12,
				UnreadCount: 3, ReadInboxMaxID: 9, ReadOutboxMaxID: 11}},
			Messages: []tg.MessageClass{&tg.Message{ID: 12, PeerID: &tg.PeerUser{UserID: 7}, Date: 1700000000}},
			Users:    []tg.UserClass{withName(7, "alice")},
		}, nil
	}}
	fakeClient(inv, ev).LoadDialogs(context.Background())
	e := next(t, ev).(model.EvDialogs)
	if e.Err != "" || len(e.Chats) != 1 {
		t.Fatalf("dialogs: %+v", e)
	}
	c := e.Chats[0]
	if c.ID != 7 || c.Username != "alice" || c.Unread != 3 || c.ReadInboxMaxID != 9 || c.ReadOutboxMaxID != 11 {
		t.Fatalf("chat: %+v", c)
	}
	if c.TopMessage != 12 || c.LastDate.Unix() != 1700000000 {
		t.Fatalf("last message: id %d, date %v", c.TopMessage, c.LastDate)
	}
	// Every call is a getDialogs: the dialogs are named from the entities of the
	// batch, with no users.getUsers of their own. The count is left alone on
	// purpose — it belongs to gotd (Collect asks the total first, and Next fires
	// one last request on a dry buffer), not to the invariant under test.
	for _, r := range inv.calls {
		if _, ok := r.(*tg.MessagesGetDialogsRequest); !ok {
			t.Fatalf("per-dialog lookup: %T", r)
		}
	}
}

// TestLoadDialogsError : the RPC fails, the failure comes back on the event
// and not as a panic — the UI has one single way to learn about it.
func TestLoadDialogsError(t *testing.T) {
	ev := make(chan model.Event, 4)
	inv := &fakeInvoker{answer: func(bin.Encoder) (any, error) { return nil, tgerr.New(400, "CHAT_ID_INVALID") }}
	fakeClient(inv, ev).LoadDialogs(context.Background())
	if e := next(t, ev).(model.EvDialogs); e.Err == "" || len(e.Chats) != 0 {
		t.Fatalf("failure expected: %+v", e)
	}
}

// TestHistoryPaging : a first full batch holding one empty message gives fewer
// usable messages than its size, so a second page is asked for, offset on the
// smallest id of the first one. The window gets them oldest first.
func TestHistoryPaging(t *testing.T) {
	ev := make(chan model.Event, 4)
	msg := func(id int) tg.MessageClass {
		return &tg.Message{ID: id, PeerID: &tg.PeerUser{UserID: 7}, Date: 1700000000 + id, Message: fmt.Sprint(id)}
	}
	inv := &fakeInvoker{answer: func(req bin.Encoder) (any, error) {
		r, ok := req.(*tg.MessagesGetHistoryRequest)
		if !ok {
			return nil, fmt.Errorf("unexpected request %T", req)
		}
		if r.OffsetID == 0 { // first page: 4 for a batch of 4, one of them empty
			return &tg.MessagesMessagesSlice{Count: 10, Users: []tg.UserClass{withName(7, "alice")},
				Messages: []tg.MessageClass{msg(10), msg(9), &tg.MessageEmpty{ID: 8}, msg(7)}}, nil
		}
		return &tg.MessagesMessagesSlice{Count: 10, Users: []tg.UserClass{withName(7, "alice")},
			Messages: []tg.MessageClass{msg(6)}}, nil
	}}
	c := fakeClient(inv, ev)
	chat := &model.Chat{ID: 7, Kind: model.ChatUser, Peer: &tg.InputPeerUser{UserID: 7}}
	c.history(context.Background(), chat, 0, 0, 4, false)
	e := next(t, ev).(model.EvHistory)
	if e.Err != "" || e.ChatID != 7 || e.Older || e.Since {
		t.Fatalf("history: %+v", e)
	}
	var ids []int
	for _, m := range e.Msgs {
		ids = append(ids, m.ID)
	}
	if fmt.Sprint(ids) != "[6 7 9 10]" { // oldest first, the empty one dropped
		t.Fatalf("ids: %v", ids)
	}
	if len(inv.calls) != 2 {
		t.Fatalf("%d calls, two pages expected", len(inv.calls))
	}
	if off := inv.calls[1].(*tg.MessagesGetHistoryRequest).OffsetID; off != 7 {
		t.Fatalf("second page offset %d, 7 expected", off)
	}
}

// TestHistoryStopsAtMinID : a sync (LoadHistorySince) stops at the first
// message already known, and never says the history is complete — scrolling
// up in the window must stay possible.
func TestHistoryStopsAtMinID(t *testing.T) {
	ev := make(chan model.Event, 4)
	inv := &fakeInvoker{answer: func(bin.Encoder) (any, error) {
		return &tg.MessagesMessages{Users: []tg.UserClass{withName(7, "alice")},
			Messages: []tg.MessageClass{
				&tg.Message{ID: 10, PeerID: &tg.PeerUser{UserID: 7}, Date: 1700000010, Message: "neuf"},
				&tg.Message{ID: 5, PeerID: &tg.PeerUser{UserID: 7}, Date: 1700000005, Message: "connu"},
			}}, nil
	}}
	c := fakeClient(inv, ev)
	chat := &model.Chat{ID: 7, Kind: model.ChatUser, Peer: &tg.InputPeerUser{UserID: 7}}
	c.history(context.Background(), chat, 0, 5, 50, true)
	e := next(t, ev).(model.EvHistory)
	if len(e.Msgs) != 1 || e.Msgs[0].ID != 10 {
		t.Fatalf("messages: %+v", e.Msgs)
	}
	if !e.Since || e.Done {
		t.Fatalf("sync: Since=%v Done=%v", e.Since, e.Done)
	}
}

// TestSendGivesID : Send unpacks the id of the message out of the updates and
// stamps the pending item; a failure comes back on the same event.
func TestSendGivesID(t *testing.T) {
	ev := make(chan model.Event, 4)
	inv := &fakeInvoker{answer: func(req bin.Encoder) (any, error) {
		if _, ok := req.(*tg.MessagesSendMessageRequest); !ok {
			return nil, fmt.Errorf("unexpected request %T", req)
		}
		return &tg.Updates{Updates: []tg.UpdateClass{
			&tg.UpdateNewMessage{Message: &tg.Message{ID: 4242, PeerID: &tg.PeerUser{UserID: 7}}},
		}}, nil
	}}
	c := fakeClient(inv, ev)
	chat := &model.Chat{ID: 7, Kind: model.ChatUser, Peer: &tg.InputPeerUser{UserID: 7}}
	c.Send(context.Background(), chat, "bonjour", 99)
	e := next(t, ev).(model.EvSent)
	if e.ChatID != 7 || e.TmpID != 99 || e.ID != 4242 || e.Err != "" {
		t.Fatalf("sent: %+v", e)
	}
	if txt := inv.calls[0].(*tg.MessagesSendMessageRequest).Message; txt != "bonjour" {
		t.Fatalf("text sent: %q", txt)
	}
}

// TestSendFails : the RPC refuses, the pending item is marked failed and no
// id is given — the UI leaves the "…" and shows the reason.
func TestSendFails(t *testing.T) {
	ev := make(chan model.Event, 4)
	inv := &fakeInvoker{answer: func(bin.Encoder) (any, error) { return nil, tgerr.New(403, "CHAT_WRITE_FORBIDDEN") }}
	c := fakeClient(inv, ev)
	c.Send(context.Background(), &model.Chat{ID: 7, Peer: &tg.InputPeerUser{UserID: 7}}, "bonjour", 99)
	if e := next(t, ev).(model.EvSent); e.ID != 0 || e.Err == "" || e.TmpID != 99 {
		t.Fatalf("failure expected: %+v", e)
	}
}

// TestReactSetsAndClears : posing a reaction posts the state of the update
// then the one of the re-read (Refetch). Removing it on a message whose
// answer carries no reactions field posts nothing but a DEBUG log: an empty
// list there would wipe the one of the update (client.go:1189-1196).
func TestReactSetsAndClears(t *testing.T) {
	ev := make(chan model.Event, 8)
	full := &tg.Message{ID: 12, PeerID: &tg.PeerUser{UserID: 7}, Date: 1700000000}
	count := tg.ReactionCount{Reaction: &tg.ReactionEmoji{Emoticon: "👍"}, Count: 1}
	count.SetChosenOrder(0)
	full.SetReactions(tg.MessageReactions{Results: []tg.ReactionCount{count}})
	bare := &tg.Message{ID: 12, PeerID: &tg.PeerUser{UserID: 7}, Date: 1700000000}
	msg := tg.MessageClass(full)
	inv := &fakeInvoker{answer: func(req bin.Encoder) (any, error) {
		switch req.(type) {
		case *tg.MessagesSendReactionRequest:
			return &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageReactions{
				Peer: &tg.PeerUser{UserID: 7}, MsgID: 12,
				Reactions: tg.MessageReactions{Results: []tg.ReactionCount{count}}}}}, nil
		case *tg.MessagesGetMessagesRequest:
			return &tg.MessagesMessages{Messages: []tg.MessageClass{msg}}, nil
		}
		return nil, fmt.Errorf("unexpected request %T", req)
	}}
	c := fakeClient(inv, ev)
	chat := &model.Chat{ID: 7, Kind: model.ChatUser, Peer: &tg.InputPeerUser{UserID: 7}}

	c.React(context.Background(), chat, 12, "👍")
	e := next(t, ev).(model.EvReactions)
	if e.Refetch || len(e.Reactions) != 1 || e.Reactions[0].Emoji != "👍" || !e.Reactions[0].Mine {
		t.Fatalf("update: %+v", e)
	}
	if r := next(t, ev).(model.EvReactions); !r.Refetch || len(r.Reactions) != 1 {
		t.Fatalf("refetch: %+v", r)
	}

	// Removal: the server answers with the field gone. No empty EvReactions.
	msg = bare
	c.React(context.Background(), chat, 12, "")
	if e := next(t, ev).(model.EvReactions); e.Refetch {
		t.Fatalf("update of the removal expected first: %+v", e)
	}
	if l := next(t, ev).(model.EvLog); l.Level != "DEBUG" {
		t.Fatalf("missing field: DEBUG expected, got %+v", l)
	}
}

// TestInfoPrivateChat : "i" on one of my messages, read, in a private chat —
// three lines and one single RPC (no reader list outside a group).
func TestInfoPrivateChat(t *testing.T) {
	ev := make(chan model.Event, 4)
	inv := &fakeInvoker{answer: func(req bin.Encoder) (any, error) {
		if _, ok := req.(*tg.MessagesGetMessagesRequest); !ok {
			return nil, fmt.Errorf("unexpected request %T", req)
		}
		return &tg.MessagesMessages{Messages: []tg.MessageClass{
			&tg.Message{ID: 12, PeerID: &tg.PeerUser{UserID: 7}, Date: 1700000000, Out: true},
		}}, nil
	}}
	c := fakeClient(inv, ev)
	chat := &model.Chat{ID: 7, Kind: model.ChatUser, Title: "alice", Peer: &tg.InputPeerUser{UserID: 7}}
	c.Info(context.Background(), chat, 12, 12, 0)
	e := next(t, ev).(model.EvInfo)
	if e.ChatID != 7 || e.ID != 12 || len(e.Lines) != 3 {
		t.Fatalf("info: %+v", e)
	}
	if e.Lines[1] != i18n.T("info_read") || e.Lines[2] != i18n.T("info_id", 12) {
		t.Fatalf("lines: %q", e.Lines)
	}
	if len(inv.calls) != 1 {
		t.Fatalf("%d calls, one expected", len(inv.calls))
	}
}

// TestWhoReadBranches : private chat = read date from the server, and its
// failure (the peer hides it) leaves the plain "read"; group = the names of
// the readers, taken from the peers already seen with no lookup of their own.
func TestWhoReadBranches(t *testing.T) {
	ev := make(chan model.Event, 4)
	fail := false
	inv := &fakeInvoker{answer: func(req bin.Encoder) (any, error) {
		switch req.(type) {
		case *tg.MessagesGetOutboxReadDateRequest:
			if fail {
				return nil, tgerr.New(400, "USER_PRIVACY_RESTRICTED")
			}
			return &tg.OutboxReadDate{Date: 1700000000}, nil
		case *tg.MessagesGetMessageReadParticipantsRequest:
			return []tg.ReadParticipantDate{{UserID: 7}}, nil
		}
		return nil, fmt.Errorf("unexpected request %T", req)
	}}
	c := fakeClient(inv, ev)
	user := &model.Chat{ID: 7, Kind: model.ChatUser, Peer: &tg.InputPeerUser{UserID: 7}}

	c.WhoRead(context.Background(), user, 12, 12)
	if e := next(t, ev).(model.EvWho); e.Text != i18n.T("who_read_at", i18n.LocalTime(unixTime(1700000000))) {
		t.Fatalf("read date: %q", e.Text)
	}
	fail = true
	c.WhoRead(context.Background(), user, 12, 12)
	if e := next(t, ev).(model.EvWho); e.Text != i18n.T("info_read") {
		t.Fatalf("privacy: %q", e.Text)
	}

	c.remember(c.peers.User(withName(7, "alice"))) // known peer: readers names it with no RPC
	group := &model.Chat{ID: -100, Kind: model.ChatGroup, Peer: &tg.InputPeerChat{ChatID: 100}}
	c.WhoRead(context.Background(), group, 12, 12)
	if e := next(t, ev).(model.EvWho); e.Text != i18n.T("info_read_by", "alice") {
		t.Fatalf("group: %q", e.Text)
	}
}

// TestDownloadError : the download fails, the failure comes back on the event
// and the .part file left behind is removed — a half file must never be taken
// for the media (client.go:1604-1611).
func TestDownloadError(t *testing.T) {
	ev := make(chan model.Event, 4)
	inv := &fakeInvoker{answer: func(bin.Encoder) (any, error) { return nil, tgerr.New(400, "LOCATION_INVALID") }}
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "photo.jpg")
	m := &model.Media{Loc: &tg.InputPhotoFileLocation{ID: 1}}
	fakeClient(inv, ev).Download(context.Background(), m, path)
	if e := next(t, ev).(model.EvDownloaded); e.Err == "" || e.Path != "" {
		t.Fatalf("failure expected: %+v", e)
	}
	if len(inv.calls) != 1 { // 400 is not a case the CDN state machine retries
		t.Fatalf("%d calls, one shot expected", len(inv.calls))
	}
	got, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(got) != 0 {
		t.Fatalf("leftover files: %v (%v)", got, err)
	}
}

// TestDownloadForeignHandle : a media coming from another backend has nothing
// to download here — one ERROR log and one failed event, no temp file.
func TestDownloadForeignHandle(t *testing.T) {
	ev := make(chan model.Event, 4)
	dir := t.TempDir()
	path := filepath.Join(dir, "photo.jpg")
	fakeClient(&fakeInvoker{}, ev).Download(context.Background(), &model.Media{Loc: "discord"}, path)
	if l := next(t, ev).(model.EvLog); l.Level != "ERROR" {
		t.Fatalf("log: %+v", l)
	}
	if e := next(t, ev).(model.EvDownloaded); e.Err != i18n.T("media_foreign") {
		t.Fatalf("event: %+v", e)
	}
	if got, _ := os.ReadDir(dir); len(got) != 0 {
		t.Fatalf("leftover files: %v", got)
	}
}

// TestUploadGivesID : an upload unpacks the id of the message it sent, like a
// text send. Without it Window.Sent cannot tell the pending line from the echo
// of the server, and the window keeps both — the placeholder for ever beside
// the real message.
func TestUploadGivesID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\npetit"), 0o600); err != nil {
		t.Fatal(err)
	}
	ev := make(chan model.Event, 8)
	inv := &fakeInvoker{answer: func(req bin.Encoder) (any, error) {
		switch req.(type) {
		case *tg.UploadSaveFilePartRequest:
			return &tg.BoolTrue{}, nil
		case *tg.MessagesSendMediaRequest:
			return &tg.Updates{Updates: []tg.UpdateClass{
				&tg.UpdateNewMessage{Message: &tg.Message{ID: 77, PeerID: &tg.PeerUser{UserID: 7}}},
			}}, nil
		}
		return nil, fmt.Errorf("unexpected request %T", req)
	}}
	c := fakeClient(inv, ev)
	chat := &model.Chat{ID: 7, Kind: model.ChatUser, Peer: &tg.InputPeerUser{UserID: 7}}
	c.SendPhoto(context.Background(), chat, path, "légende", false, 99)

	var e model.EvSent
	for range 4 { // the progress of the upload posts EvUpload before the receipt
		if s, ok := next(t, ev).(model.EvSent); ok {
			e = s
			break
		}
	}
	if e.ChatID != 7 || e.TmpID != 99 || e.ID != 77 || e.Err != "" {
		t.Fatalf("sent: %+v", e)
	}
}

func TestHistoryUsesChatSnapshot(t *testing.T) {
	for _, method := range []string{"history", "around", "since", "search"} {
		t.Run(method, func(t *testing.T) {
			ev := make(chan model.Event, 4)
			started, resume := make(chan struct{}), make(chan struct{})
			inv := &fakeInvoker{answer: func(bin.Encoder) (any, error) {
				close(started)
				<-resume
				return &tg.MessagesMessagesSlice{Count: 1, Users: []tg.UserClass{withName(7, "Alice")}, Messages: []tg.MessageClass{
					&tg.Message{ID: 42, PeerID: &tg.PeerUser{UserID: 7}, Date: 1700000000, Message: "hello"},
				}}, nil
			}}
			c := fakeClient(inv, ev)
			chat := &model.Chat{ID: 7, Kind: model.ChatUser, Title: "Alice", Peer: &tg.InputPeerUser{UserID: 7}}
			ctx := context.Background()
			switch method {
			case "history":
				c.LoadHistory(ctx, chat, 0, 1)
			case "around":
				c.LoadHistoryAround(ctx, chat, 1, 10)
			case "since":
				c.LoadHistorySince(ctx, chat, 0, 1)
			case "search":
				c.Search(ctx, chat, "hello", 1)
			}
			<-started
			chat.Title = "Updated name"
			close(resume)
			var msgs []model.Msg
			switch e := next(t, ev).(type) {
			case model.EvHistory:
				if e.Err != "" {
					t.Fatal(e.Err)
				}
				msgs = e.Msgs
			case model.EvSearch:
				if e.Err != "" {
					t.Fatal(e.Err)
				}
				msgs = e.Msgs
			default:
				t.Fatalf("unexpected event: %T %+v", e, e)
			}
			if len(msgs) != 1 || msgs[0].From != "Alice" {
				t.Fatalf("request did not keep its chat snapshot: %+v", msgs)
			}
		})
	}
}
