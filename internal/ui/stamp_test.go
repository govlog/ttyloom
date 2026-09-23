package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/cache"
	"github.com/govlog/ttyloom/internal/model"
)

// stamp sets Net on every Chat/Msg the event carries; events with a bare
// ChatID stay as they are (their net comes from the envelope).
func TestStamp(t *testing.T) {
	n := stamp("discord", model.EvNewMessage{Msg: model.Msg{ID: 1, ChatID: 7}, Chat: &model.Chat{ID: 7}}).(model.EvNewMessage)
	if n.Msg.Net != "discord" || n.Chat.Net != "discord" {
		t.Fatalf("EvNewMessage: %+v %+v", n.Msg, n.Chat)
	}
	d := stamp("discord", model.EvDialogs{Chats: []*model.Chat{{ID: 1}, {ID: 2}}}).(model.EvDialogs)
	for _, c := range d.Chats {
		if c.Net != "discord" {
			t.Fatalf("EvDialogs: %+v", c)
		}
	}
	h := stamp("tg", model.EvHistory{ChatID: 7, Msgs: []model.Msg{{ID: 1}, {ID: 2}}}).(model.EvHistory)
	for _, m := range h.Msgs {
		if m.Net != "tg" {
			t.Fatalf("EvHistory: %+v", m)
		}
	}
	g := stamp("tg", model.EvSearchGlobal{Hits: []model.SearchHit{{Chat: &model.Chat{ID: 1}}}}).(model.EvSearchGlobal)
	if g.Hits[0].Chat.Net != "tg" {
		t.Fatalf("EvSearchGlobal: %+v", g.Hits[0].Chat)
	}
	r := stamp("tg", model.EvReadOutbox{ChatID: 7, MaxID: 3}).(model.EvReadOutbox)
	if r != (model.EvReadOutbox{ChatID: 7, MaxID: 3}) {
		t.Fatalf("bare ChatID changed: %+v", r)
	}
}

// A nil Chat is a normal answer (chat not found, no peer in the hit): stamp
// walks past it instead of panicking on the UI goroutine.
func TestStampNilChat(t *testing.T) {
	if c := stamp("tg", model.EvChat{Query: "@nobody", Err: "not found"}).(model.EvChat).Chat; c != nil {
		t.Fatalf("EvChat: %+v", c)
	}
	stamp("tg", model.EvSearchGlobal{Hits: []model.SearchHit{{MsgID: 1}}})
}

// TestRegressionStampCleansRemoteText : every remote string an event carries
// is cleaned once, when the event comes in — an escape sequence never reaches
// the stored value, whatever the display site. The text of a message keeps
// its line breaks (the entity offsets depend on them), a title does not.
func TestRegressionStampCleansRemoteText(t *testing.T) {
	const bad = "x\x1b]0;pwn\ay"
	chat := func() *model.Chat { return &model.Chat{ID: 1, Title: "t\n" + bad, Username: bad} }
	msg := func() model.Msg {
		return model.Msg{ID: 1, Text: "a\nb" + bad, From: bad, ChatLabel: bad, Service: bad,
			Media: &model.Media{Label: bad, Name: bad, Err: bad}}
	}
	chatStrings := func(c *model.Chat) []string { return []string{c.Title, c.Username} }
	msgStrings := func(m model.Msg) []string {
		return []string{m.Text, m.From, m.ChatLabel, m.Service, m.Media.Label, m.Media.Name, m.Media.Err}
	}
	cases := []struct {
		name string
		ev   model.Event
		got  func(model.Event) []string
	}{
		{"dialogs", model.EvDialogs{Chats: []*model.Chat{chat()}},
			func(e model.Event) []string { return chatStrings(e.(model.EvDialogs).Chats[0]) }},
		{"contacts", model.EvContacts{Peers: []*model.Chat{chat()}, Err: bad},
			func(e model.Event) []string {
				return append(chatStrings(e.(model.EvContacts).Peers[0]), e.(model.EvContacts).Err)
			}},
		{"contacts_found", model.EvContactsFound{Peers: []*model.Chat{chat()}, Err: bad},
			func(e model.Event) []string {
				return append(chatStrings(e.(model.EvContactsFound).Peers[0]), e.(model.EvContactsFound).Err)
			}},
		{"chat", model.EvChat{Chat: chat()}, func(e model.Event) []string { return chatStrings(e.(model.EvChat).Chat) }},
		{"search_global", model.EvSearchGlobal{Hits: []model.SearchHit{{Chat: chat(), From: bad, Text: bad}}, Err: bad},
			func(e model.Event) []string {
				h := e.(model.EvSearchGlobal).Hits[0]
				return append(chatStrings(h.Chat), h.From, h.Text, e.(model.EvSearchGlobal).Err)
			}},
		{"history", model.EvHistory{Msgs: []model.Msg{msg()}},
			func(e model.Event) []string { return msgStrings(e.(model.EvHistory).Msgs[0]) }},
		{"search", model.EvSearch{Msgs: []model.Msg{msg()}},
			func(e model.Event) []string { return msgStrings(e.(model.EvSearch).Msgs[0]) }},
		{"new_message", model.EvNewMessage{Msg: msg(), Chat: chat()},
			func(e model.Event) []string {
				return append(msgStrings(e.(model.EvNewMessage).Msg), chatStrings(e.(model.EvNewMessage).Chat)...)
			}},
		{"edit_message", model.EvEditMessage{Msg: msg()},
			func(e model.Event) []string { return msgStrings(e.(model.EvEditMessage).Msg) }},
		{"lines", model.EvLines{Lines: []string{bad}}, func(e model.Event) []string { return e.(model.EvLines).Lines }},
		{"info", model.EvInfo{Lines: []string{bad}}, func(e model.Event) []string { return e.(model.EvInfo).Lines }},
		{"whois", model.EvWhois{Lines: []string{bad}, Err: bad},
			func(e model.Event) []string { return append(e.(model.EvWhois).Lines, e.(model.EvWhois).Err) }},
		{"who", model.EvWho{Text: bad}, func(e model.Event) []string { return []string{e.(model.EvWho).Text} }},
		{"auth_prompt", model.EvAuthPrompt{Question: bad}, func(e model.Event) []string { return []string{e.(model.EvAuthPrompt).Question} }},
		{"typing", model.EvTyping{Who: bad}, func(e model.Event) []string { return []string{e.(model.EvTyping).Who} }},
		{"participants", model.EvParticipants{Lines: []model.Participant{{Text: bad, Name: bad}}},
			func(e model.Event) []string {
				p := e.(model.EvParticipants).Lines[0]
				return []string{p.Text, p.Name}
			}},
		{"gifs", model.EvGifs{Err: bad}, func(e model.Event) []string { return []string{e.(model.EvGifs).Err} }},
		{"downloaded", model.EvDownloaded{Media: &model.Media{}, Err: bad}, func(e model.Event) []string { return []string{e.(model.EvDownloaded).Err} }},
	}
	for _, c := range cases {
		out := stamp("net", c.ev)
		for i, s := range c.got(out) {
			if strings.ContainsAny(s, "\x1b\a") {
				t.Errorf("%s: string %d stored raw: %q", c.name, i, s)
			}
		}
	}
	nm := stamp("net", model.EvNewMessage{Msg: msg(), Chat: chat()}).(model.EvNewMessage)
	if !strings.HasPrefix(nm.Msg.Text, "a\nb") || nm.Msg.Net != "net" || nm.Chat.Net != "net" {
		t.Fatalf("message text lost its line break or the net: %+v", nm.Msg)
	}
	if strings.ContainsRune(nm.Chat.Title, '\n') {
		t.Fatalf("chat title kept a line break: %q", nm.Chat.Title)
	}
}

// TestRegressionCacheLoadCleansRemoteText : a cache written before the
// cleaning at ingestion may hold raw text — it is cleaned on its way in too,
// the chats and the played history alike.
func TestRegressionCacheLoadCleansRemoteText(t *testing.T) {
	const net = "irc:libera"
	c := cache.New(t.TempDir(), 2000)
	if err := c.SaveDialogs([]model.Chat{{ID: 42, Title: "#go\x1b]0;pwn\a", Kind: model.ChatGroup, LastDate: time.Now()}}, 1001); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveHistory(42, []model.Msg{{ID: 7, ChatID: 42, Text: "rm\x1b[2J", From: "ev\x1bil", Date: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	u := listUI()
	u.caches = map[string]*cache.Cache{net: c}
	u.chats = map[model.ChatKey]*model.Chat{}
	u.cfg.AutoOpenDays = 7
	u.loadCache()
	room := u.chats[model.ChatKey{Net: net, ID: 42}]
	if room == nil || strings.ContainsRune(room.Title, 0x1b) {
		t.Fatalf("cached title stored raw: %+v", room)
	}
	i := u.ws.ForChat(room.Key())
	if i < 0 {
		t.Fatal("no window opened for the cached chat")
	}
	played := 0
	for _, it := range u.ws.List[i].Items {
		if it.Msg == nil {
			continue
		}
		played++
		if strings.ContainsRune(it.Msg.Text+it.Msg.From, 0x1b) {
			t.Fatalf("cached message stored raw: <%q> %q", it.Msg.From, it.Msg.Text)
		}
	}
	if played != 1 {
		t.Fatalf("%d cached messages played, want 1", played)
	}
}
