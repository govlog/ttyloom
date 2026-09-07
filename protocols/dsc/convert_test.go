package dsc

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/ningen/v3"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// msgID builds a snowflake whose timestamp part is ms after the Discord epoch:
// the id and the date of a message come from the same number.
func msgID(ms int64) discord.MessageID { return discord.MessageID(ms << 22) }

// A whole message: markdown, an image attachment, our own reaction, a custom
// emoji and a reply. Field by field, that is the contract with the UI.
func TestMsgOf(t *testing.T) {
	c := dmState(t)
	c.self.Store(7)
	// The quoted message carries no guild of its own: without the guild of the
	// message that quotes it, the nickname of the member is lost.
	if err := c.state().Cabinet.MemberSet(9,
		&discord.Member{User: discord.User{ID: 42, Username: "bob"}, Nick: "Bobby"}, false); err != nil {
		t.Fatalf("member of the test: %s", err)
	}
	m := &discord.Message{
		ID:        msgID(1000),
		ChannelID: 55,
		GuildID:   9,
		Author:    discord.User{ID: 7, Username: "alice", DisplayName: "Alice", Avatar: "abc"},
		Content:   "hi **bob** <@42>",
		Timestamp: discord.Timestamp(time.Unix(1, 0)),
		// IsValid() only: the date shown comes from the id.
		EditedTimestamp: discord.Timestamp(time.Unix(2, 0)),
		Mentions:        []discord.GuildUser{{User: discord.User{ID: 42, Username: "bob"}}},
		Attachments: []discord.Attachment{{
			Filename: "cat.png", ContentType: "image/png", Size: 2048,
			Width: 640, Height: 480, URL: "https://cdn.example/cat.png"}},
		Reactions: []discord.Reaction{
			{Count: 3, Me: true, Emoji: discord.Emoji{Name: "🔥"}},
			{Count: 1, Emoji: discord.Emoji{ID: 88, Name: "party"}},
		},
		ReferencedMessage: &discord.Message{
			ID: msgID(900), Author: discord.User{ID: 42, Username: "bob"}, Content: "**q**"},
	}
	got := c.msgOf(m)

	want := model.Msg{
		ID:     int(msgID(1000)),
		ChatID: 55,
		Date:   msgID(1000).Time(),
		From:   "Alice",
		FromID: 7,
		Out:    true,
		Edited: true,
		Text:   "hi bob @bob",
		Entities: []model.Span{
			{Start: 3, End: 6, Kind: model.SpanBold},
			{Start: 7, End: 11, Kind: model.SpanMention, UserID: 42},
		},
		Reactions: []model.Reaction{
			{Emoji: "🔥", Count: 3, Mine: true},
			{Emoji: ":party:", Count: 1},
		},
	}
	// Compared apart: pointers and the opaque handles never match with ==.
	media, reply, photo := got.Media, got.Reply, got.FromPhoto
	got.Media, got.Reply, got.FromPhoto = nil, nil, nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("msgOf = %+v, want %+v", got, want)
	}
	if photo != fileURL("https://cdn.discordapp.com/avatars/7/abc.png") {
		t.Fatalf("author photo: %#v", photo)
	}
	wantMedia := model.Media{Kind: model.MediaPhoto, Label: "[photo 640x480 · 2 KB]",
		W: 640, H: 480, Size: 2048, Name: "cat.png", Mime: "image/png", Ext: ".png",
		Loc: fileURL("https://cdn.example/cat.png")}
	if media == nil || !reflect.DeepEqual(*media, wantMedia) {
		t.Fatalf("attachment = %+v, want %+v", media, wantMedia)
	}
	wantReply := model.Quote{ID: int(msgID(900)), From: "Bobby", Text: "q"}
	if reply == nil || *reply != wantReply {
		t.Fatalf("reply = %+v, want %+v", reply, wantReply)
	}
}

// A message received from someone else is not Out, and its lone embed becomes
// the link preview — no attachment, so nothing to download.
func TestMsgOfEmbed(t *testing.T) {
	c := &Client{}
	c.self.Store(7)
	got := c.msgOf(&discord.Message{
		ID: msgID(10), ChannelID: 55, Author: discord.User{ID: 42, Username: "bob"},
		Content: "see https://x.io/a",
		Embeds: []discord.Embed{{
			URL: "https://x.io/a", Title: "The page", Description: "what it says"}},
	})
	if got.Out || got.From != "bob" {
		t.Fatalf("author: Out=%v From=%q", got.Out, got.From)
	}
	want := model.Media{Kind: model.MediaWebPage, Label: "[link · The page]",
		Name: "what it says", URL: "https://x.io/a"}
	if got.Media == nil || !reflect.DeepEqual(*got.Media, want) {
		t.Fatalf("embed = %+v, want %+v", got.Media, want)
	}
}

// chatOf: one channel, one sidebar entry. The kind and the title are what the
// user sees, so they are what is checked.
func TestChatOf(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ch    discord.Channel
		guild string
		want  model.Chat
	}{
		{"dm", discord.Channel{ID: 5, Type: discord.DirectMessage,
			DMRecipients: []discord.User{{ID: 42, Username: "bob", DisplayName: "Bob"}}}, "",
			model.Chat{ID: 5, Kind: model.ChatUser, Title: "Bob", Username: "bob"}},
		{"group dm named", discord.Channel{ID: 6, Type: discord.GroupDM, Name: "the four"}, "",
			model.Chat{ID: 6, Kind: model.ChatGroup, Title: "the four"}},
		{"group dm unnamed", discord.Channel{ID: 7, Type: discord.GroupDM,
			DMRecipients: []discord.User{{Username: "bob"}, {Username: "eve", DisplayName: "Eve"}}}, "",
			model.Chat{ID: 7, Kind: model.ChatGroup, Title: "bob, Eve"}},
		{"guild channel", discord.Channel{ID: 8, GuildID: 9, Type: discord.GuildText, Name: "general"},
			"Gophers", model.Chat{ID: 8, Kind: model.ChatGroup, Title: "Gophers / #general"}},
		{"guild channel, guild unknown", discord.Channel{ID: 8, GuildID: 9, Type: discord.GuildText, Name: "general"},
			"", model.Chat{ID: 8, Kind: model.ChatGroup, Title: "#general"}},
		// No name anywhere: the id is at least something to click on.
		{"dm with no recipient", discord.Channel{ID: 9, Type: discord.DirectMessage}, "",
			model.Chat{ID: 9, Kind: model.ChatUser, Title: "9"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := chatOf(&tc.ch, tc.guild)
			want := tc.want
			want.Peer = peer{Channel: uint64(tc.ch.ID), Guild: uint64(tc.ch.GuildID)}
			got.PhotoLoc = nil // the avatar URL is the business of the DM case alone
			if !reflect.DeepEqual(*got, want) {
				t.Fatalf("chatOf = %+v, want %+v", *got, want)
			}
		})
	}
}

// The date of the last message of a channel comes from its id; an invalid id
// leaves the chat without a date rather than at the Discord epoch.
func TestChatOfLastDate(t *testing.T) {
	last := msgID(1000)
	if got := chatOf(&discord.Channel{ID: 5, LastMessageID: last}, ""); !got.LastDate.Equal(last.Time()) {
		t.Fatalf("LastDate = %v, want %v", got.LastDate, last.Time())
	}
	if got := chatOf(&discord.Channel{ID: 5}, ""); !got.LastDate.IsZero() {
		t.Fatalf("LastDate with no message = %v, want zero", got.LastDate)
	}
}

// The DM avatar is the handle the UI hands back to Download.
func TestChatOfPhoto(t *testing.T) {
	got := chatOf(&discord.Channel{ID: 5, Type: discord.DirectMessage,
		DMRecipients: []discord.User{{ID: 42, Username: "bob", Avatar: "abc"}}}, "")
	if got.PhotoLoc != fileURL("https://cdn.discordapp.com/avatars/42/abc.png") {
		t.Fatalf("PhotoLoc = %#v", got.PhotoLoc)
	}
}

// readOf: the read marks of the sidebar. Discord gives a mention count and a
// last read id, never a number of unread messages — 1 then just means "there
// is something after what I read".
func TestReadOf(t *testing.T) {
	c := testClient(nil)
	ch := &discord.Channel{ID: 5, Type: discord.DirectMessage, LastMessageID: msgID(1000),
		DMRecipients: []discord.User{{ID: 42, Username: "bob"}}}
	if err := c.state().Cabinet.ChannelSet(ch, false); err != nil {
		t.Fatalf("channel of the test: %s", err)
	}

	// Nothing read in this channel: no mark at all.
	chat := &model.Chat{}
	c.readOf(chat, ch)
	if chat.ReadInboxMaxID != 0 || chat.Unread != 0 {
		t.Fatalf("no read state: ReadInboxMaxID=%d Unread=%d, want 0 and 0", chat.ReadInboxMaxID, chat.Unread)
	}

	// Read up to an older message: unread, with no count to give.
	c.state().ReadState.MarkRead(ch.ID, msgID(900))
	chat = &model.Chat{}
	c.readOf(chat, ch)
	if chat.ReadInboxMaxID != int(msgID(900)) || chat.Unread != 1 {
		t.Fatalf("read behind: ReadInboxMaxID=%d Unread=%d, want %d and 1", chat.ReadInboxMaxID, chat.Unread, int(msgID(900)))
	}

	// Two mentions since: the count of the mentions wins, it is the one worth
	// showing.
	c.state().ReadState.MarkUnread(ch.ID, msgID(1000), 2)
	chat = &model.Chat{}
	c.readOf(chat, ch)
	if chat.Unread != 2 {
		t.Fatalf("mentions: Unread=%d, want 2", chat.Unread)
	}
}

// dmState : a client whose state holds one DM with bob in it.
func dmState(t *testing.T) *Client {
	t.Helper()
	c := testClient(nil)
	ch := &discord.Channel{ID: 5, Type: discord.DirectMessage,
		DMRecipients: []discord.User{{ID: 42, Username: "bob", DisplayName: "Bob"}}}
	if err := c.state().Cabinet.ChannelSet(ch, false); err != nil {
		t.Fatalf("channel of the test: %s", err)
	}
	return c
}

// typist: a DM event carries no member, so the name has to come from the
// recipients of the channel — a presence only ever exists for a friend.
func TestTypist(t *testing.T) {
	c := dmState(t)
	if got := c.typist(&gateway.TypingStartEvent{ChannelID: 5, UserID: 42}); got != "Bob" {
		t.Fatalf("typist in a DM = %q, want %q", got, "Bob")
	}
	// A guild event: the nickname of the member wins over everything else.
	e := &gateway.TypingStartEvent{ChannelID: 8, UserID: 42, GuildID: 9,
		Member: &discord.Member{User: discord.User{ID: 42, Username: "bob"}, Nick: "Bobby"}}
	if got := c.typist(e); got != "Bobby" {
		t.Fatalf("typist in a guild = %q, want %q", got, "Bobby")
	}
	// Nobody known: the id, never an empty name.
	if got := c.typist(&gateway.TypingStartEvent{ChannelID: 5, UserID: 7}); got != "7" {
		t.Fatalf("typist unknown = %q, want %q", got, "7")
	}
}

// reactions: the whole set of a message, read from the state. A message the
// state does not hold posts nothing — it must never cost a network read.
func TestReactions(t *testing.T) {
	c := dmState(t)
	ev := make(chan model.Event, 2)
	c.Poster = model.Poster{Events: ev}
	m := discord.Message{ID: msgID(1000), ChannelID: 5, Author: discord.User{ID: 42},
		Reactions: []discord.Reaction{{Count: 2, Me: true, Emoji: discord.Emoji{Name: "🔥"}}}}
	if err := c.state().Cabinet.MessageSet(&m, false); err != nil {
		t.Fatalf("message of the test: %s", err)
	}

	c.reactions(5, msgID(1000))
	want := model.EvReactions{ChatID: 5, ID: int(msgID(1000)),
		Reactions: []model.Reaction{{Emoji: "🔥", Count: 2, Mine: true}}}
	select {
	case got := <-ev:
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("reactions = %#v, want %#v", got, want)
		}
	default:
		t.Fatal("reactions: no event")
	}

	c.reactions(5, msgID(999)) // never seen
	select {
	case got := <-ev:
		t.Fatalf("reactions on an unknown message: %#v", got)
	default:
	}
}

// A guild left, deleted or a kick drops its text channels from the sidebar.
// A guild that only went unavailable is an outage on the Discord side: it
// comes back, and nothing is dropped.
func TestGuildDeleteGone(t *testing.T) {
	ev := make(chan model.Event, 8)
	c := testClient(ev)
	for _, ch := range []discord.Channel{
		{ID: 10, GuildID: 9, Type: discord.GuildText, Name: "general"},
		{ID: 11, GuildID: 9, Type: discord.GuildAnnouncement, Name: "annonces"},
		{ID: 12, GuildID: 9, Type: discord.GuildVoice, Name: "vocal"},
	} {
		if err := c.state().Cabinet.ChannelSet(&ch, false); err != nil {
			t.Fatalf("channel of the test: %s", err)
		}
	}

	c.wire() // the handler has to be registered, that was the bug
	c.state().Handler.Call(&gateway.GuildDeleteEvent{ID: 9, Unavailable: true})
	if len(ev) != 0 {
		t.Fatalf("unavailable guild: %d event(s), want none", len(ev))
	}

	c.state().Handler.Call(&gateway.GuildDeleteEvent{ID: 9})
	var got []int64
	for len(ev) > 0 {
		e, ok := (<-ev).(model.EvChatGone)
		if !ok {
			t.Fatalf("event of another kind: %#v", e)
		}
		got = append(got, e.ChatID)
	}
	slices.Sort(got)
	if !reflect.DeepEqual(got, []int64{10, 11}) {
		t.Fatalf("channels gone = %v, want the two text ones (the voice one was never listed)", got)
	}
}

// A READY that carries a guild Discord could not send gives an incomplete
// dialog list: the UI must not take it for the whole account and drop the
// channels of that guild.
func TestReadyIncomplete(t *testing.T) {
	if readyIncomplete(gateway.ReadyEvent{Guilds: []gateway.GuildCreateEvent{{}, {}}}) {
		t.Fatal("every guild sent: the list is whole")
	}
	if !readyIncomplete(gateway.ReadyEvent{Guilds: []gateway.GuildCreateEvent{{}, {Unavailable: true}}}) {
		t.Fatal("one unavailable guild: the list is not whole")
	}
}

// A channel deleted, or a DM closed, drops its entry: the gateway gives the
// channel itself, its id is the one of the chat.
func TestChannelDeleteGone(t *testing.T) {
	ev := make(chan model.Event, 4)
	c := testClient(ev)
	c.wire()

	c.state().Handler.Call(&gateway.ChannelDeleteEvent{Channel: discord.Channel{ID: 5, Type: discord.DirectMessage}})

	select {
	case got := <-ev:
		if want := (model.EvChatGone{ChatID: 5}); got != want {
			t.Fatalf("channel deleted = %#v, want %#v", got, want)
		}
	default:
		t.Fatal("channel deleted: no event")
	}
}

// A Discord system message — member join, pin, boost — carries no content at
// all: shown as it is it would be an empty line from its author. It becomes a
// Service line instead, the one the window never lets anybody select.
func TestMsgOfService(t *testing.T) {
	c := dmState(t)
	got := c.msgOf(&discord.Message{ID: msgID(1000), ChannelID: 5,
		Type:   discord.GuildMemberJoinMessage,
		Author: discord.User{ID: 42, Username: "bob", DisplayName: "Bob"}})
	if got.Service != i18n.T("dsc_sys_join", "Bob") {
		t.Fatalf("service = %q, want %q", got.Service, i18n.T("dsc_sys_join", "Bob"))
	}
	if got.Text != "" {
		t.Fatalf("text kept on a system message: %q", got.Text)
	}
	if got.From != "Bob" { // the author still names the line
		t.Fatalf("from = %q", got.From)
	}
	// A plain message is left alone, and so is every type Discord gives an
	// ordinary message: a reply, and the answer of a bot to a slash command or
	// to a context-menu command. All three carry a content of their own.
	for _, tc := range []discord.MessageType{discord.DefaultMessage, discord.InlinedReplyMessage,
		discord.ChatInputCommandMessage, discord.ContextMenuCommand} {
		m := c.msgOf(&discord.Message{ID: msgID(1000), ChannelID: 5, Type: tc,
			Author: discord.User{ID: 42, Username: "bob"}, Content: "hi"})
		if m.Service != "" || m.Text != "hi" {
			t.Fatalf("type %d = %+v", tc, m)
		}
	}
}

// An attachment of a type the allow list does not know is saved as .bin, like
// tgc: the .bin guard of the UI then keeps it away from xdg-open.
func TestAttachMediaUnknownTypeBin(t *testing.T) {
	m := attachMedia(discord.Attachment{Filename: "run.sh", ContentType: "application/x-sh", URL: "https://cdn/x"})
	if m.Ext != ".bin" {
		t.Fatalf("unknown type: ext %q, want .bin", m.Ext)
	}
}

// A Tenor GIF received (what SendGif posts) comes as a "gifv" embed with a
// video: it becomes an animated GIF of the message, the page kept as its URL,
// instead of a link label nobody can play.
func TestMsgOfEmbedGIFV(t *testing.T) {
	c := &Client{}
	got := c.msgOf(&discord.Message{ID: msgID(10), ChannelID: 55, Author: discord.User{ID: 42, Username: "bob"},
		Content: "https://tenor.com/view/cat-1",
		Embeds: []discord.Embed{{Type: discord.GIFVEmbed, URL: "https://tenor.com/view/cat-1",
			Video: &discord.EmbedVideo{URL: "https://media.tenor.com/a.mp4", Width: 498, Height: 280}}}})
	md := got.Media
	if md == nil || md.Kind != model.MediaGIF || md.Loc != fileURL("https://media.tenor.com/a.mp4") || md.W != 498 ||
		md.Mime != "video/mp4" || md.Ext != ".mp4" || md.URL != "https://tenor.com/view/cat-1" || !md.Previewable() {
		t.Fatalf("gifv embed: %+v", md)
	}
}

// testClient : a client on an empty state, with events on ev when given.
func testClient(ev chan<- model.Event) *Client {
	c := &Client{Poster: model.Poster{Events: ev}}
	c.st.Store(ningen.New(""))
	return c
}
