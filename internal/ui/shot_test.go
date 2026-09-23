package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/clipperhouse/uax29/v2/graphemes"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/emoji"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// TestScreenshots draws the frames of docs/screenshots from sample data —
// the drawing code itself, offscreen, including kitty image placements. Skipped unless
// TTYLOOM_SHOTS names the directory the ANSI frames go to;
// docs/screenshots/ansi2svg.py turns them into the SVG files of the README.
func TestScreenshots(t *testing.T) {
	dir := os.Getenv("TTYLOOM_SHOTS")
	if dir == "" {
		t.Skip("TTYLOOM_SHOTS unset")
	}
	i18n.Set("en")       // the documentation is in English
	defer i18n.Set("fr") // TestMain fixed the language of the other tests
	th, err := theme.Parse(strings.NewReader(mocha), "Catppuccin Mocha")
	if err != nil {
		t.Fatal(err)
	}
	for name, scene := range map[string]func(*testing.T, *UI){
		"main":    shotMain,
		"discord": shotDiscord,
		"search":  shotSearch,
		"gifs":    shotGifs,
		"members": shotMembers,
		"newchat": shotNewChat,
		"picker":  shotPicker,
		"tabs":    shotTabs,
	} {
		var buf bytes.Buffer
		u := shotUI(t, th, &buf)
		scene(t, u)
		u.draw()
		frame := bytes.ReplaceAll(buf.Bytes(), []byte(time.Now().Format("[15:04]")), []byte("[15:42]"))
		if err := os.WriteFile(filepath.Join(dir, name+".ansi"), frame, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".widths"), clusterWidths(frame), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// clusterWidths : the grapheme clusters of a frame that are not plain ASCII,
// each with the width render.Width gives it and whether it is an emoji, as
// JSON — {"😂":{"w":2,"emoji":true},"é":{"w":1}}. ansi2svg.py reads that
// sidecar next to the frame: the width says on how many columns the renderer
// that drew the frame put the cluster, the emoji flag says to rasterise it
// with a colour font instead of trusting the fonts of the reader.
// Escape sequences are ASCII and drop out of the table on their own.
func clusterWidths(frame []byte) []byte {
	type cluster struct {
		W     int  `json:"w"`
		Emoji bool `json:"emoji,omitempty"`
	}
	table := map[string]cluster{}
	g := graphemes.FromString(string(frame))
	for g.Next() {
		cl := g.Value()
		if isASCII(cl) {
			continue
		}
		w := render.Width(cl)
		table[cl] = cluster{W: w, Emoji: w == 2 && isPictograph(cl)}
	}
	out, err := json.Marshal(table) // sorted keys: the sidecar is stable
	if err != nil {
		panic(err)
	}
	return out
}

// emojiTable : the Unicode emoji of internal/emoji, fully-qualified and
// without the variation selector, as a set built once.
var emojiTable = sync.OnceValue(func() map[string]bool {
	set := map[string]bool{}
	for _, e := range emoji.All() {
		set[e.Char], set[emoji.Base(e.Char)] = true, true
	}
	return set
})

// isPictograph : the cluster is in the Unicode emoji table, or holds a code
// point above U+1F000 — flags included, the regional indicators start at
// U+1F1E6. clusterWidths keeps it as an emoji only when the renderer also
// gave it two cells, which is its own test for the emoji presentation: ↪ is
// in the table (as ↪️) but the client prints it as a one-cell text glyph, in
// the colour of the text, and the export must do the same.
func isPictograph(cl string) bool {
	set := emojiTable()
	if set[cl] || set[emoji.Base(cl)] {
		return true
	}
	for _, r := range cl {
		if r >= 0x1F000 {
			return true
		}
	}
	return false
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// mocha : Catppuccin Mocha, in the Ghostty theme format theme.Parse reads.
const mocha = `palette = 0=#45475a
palette = 1=#f38ba8
palette = 2=#a6e3a1
palette = 3=#f9e2af
palette = 4=#89b4fa
palette = 5=#f5c2e7
palette = 6=#94e2d5
palette = 7=#bac2de
palette = 8=#585b70
palette = 9=#f38ba8
palette = 10=#a6e3a1
palette = 11=#f9e2af
palette = 12=#89b4fa
palette = 13=#f5c2e7
palette = 14=#94e2d5
palette = 15=#a6adc8
background = #1e1e2e
foreground = #cdd6f4
`

// shotCols leaves the message area 110 cells (the sidebar takes 27 of them,
// the scrollbar one): enough for the status line to hold the tabs and the
// activity at the same time.
const shotCols, shotRows = 138, 34

var shotTime = time.Date(2026, 9, 5, 15, 42, 0, 0, time.UTC)

// span : the entity over sub in text, offsets in runes like the model.
func span(text, sub string, kind model.SpanKind, url string) model.Span {
	start := len([]rune(text[:strings.Index(text, sub)]))
	return model.Span{Start: start, End: start + len([]rune(sub)), Kind: kind, URL: url}
}

// fixturePNG reads a GIF frame of docs/screenshots/fixtures and its size.
func fixturePNG(t *testing.T, name string) (data []byte, w, h int) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "screenshots", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return data, cfg.Width, cfg.Height
}

// gif : a GIF whose preview is a fixture frame, fitted like the pickers do;
// the GIF itself is five times that, as the label says.
func gif(t *testing.T, name string) *model.Media {
	data, w, h := fixturePNG(t, name)
	return &model.Media{Kind: model.MediaGIF, Label: fmt.Sprintf("[gif %dx%d]", 5*w, 5*h), State: model.MediaReady, Path: "x",
		Frames: [][]byte{data}, FrameW: w, FrameH: h, W: 5 * w, H: 5 * h, Loc: 1, Ext: ".gif", Mime: "image/gif"}
}

// shotUI builds a UI the way Run does, on an offscreen terminal, with three
// networks, a sidebar of sections and a group window of styled messages.
// Windows: 0 unbound, 1 Gophers (telegram), 2 #go and 3 alice (irc), then the
// hidden windows the tab bar counts.
func shotUI(t *testing.T, th theme.Theme, out *bytes.Buffer) *UI {
	tm := term.NewOffscreen(out, shotCols, shotRows)
	tm.Kitty, tm.CellW, tm.CellH = true, 8, 17
	cfg := &config.Config{SidebarSort: "recent", Hover: config.HoverMenu, Timestamps: true, LinkPreviews: true,
		Separator: true, Redline: true, SidebarWidth: 26, Images: "kitty", Video: "show", Notify: "off", KittyImages: 48}
	tg := &fakeBackend{caps: model.AllCaps()}
	dc := &fakeBackend{caps: model.Caps{Reactions: true, Edit: true, Gifs: true, Search: true, GlobalSearch: true}}
	irc := &fakeBackend{caps: model.Caps{Whois: true, Resolve: true, Leave: true}}
	libera := model.IRCNet("libera")
	u := &UI{ctx: context.Background(), t: tm, cfg: cfg, th: th, events: make(chan model.Event, 8), ws: NewWindows(),
		agg: &Window{}, debug: &Window{}, focused: true, images: "kitty", side: sideChats, sideW: 26,
		nets:  map[string]model.Backend{model.NetTelegram: tg, model.NetDiscord: dc, libera: irc},
		chats: map[model.ChatKey]*model.Chat{}, lookups: map[uint64]*lookup{}, typing: map[model.ChatKey]typing{},
		lastTyping: map[model.ChatKey]time.Time{}, avatars: map[model.ChatKey]*model.Media{}, openNext: map[*model.Media]bool{},
		presence: map[model.ChatKey]string{}, dirty: map[model.ChatKey]bool{},
		partsCache: map[model.ChatKey]partsEntry{}, whoCache: map[whoKey]whoEntry{}, aliases: map[model.ChatKey]string{},
		folded: map[string]bool{}, tabLast: map[string]*Window{},
		self: map[string]selfInfo{model.NetTelegram: {ID: 1, Name: "chris"}, model.NetDiscord: {ID: 2, Name: "chris"}, libera: {ID: 3, Name: "chris"}},
		conn: map[string]bool{model.NetTelegram: true, model.NetDiscord: true, libera: true}, dialogsSeen: map[string]bool{},
		reactList: map[string][]string{}}
	u.setMaxItems(2000)
	now := shotTime
	add := func(c *model.Chat) *model.Chat {
		u.chats[c.Key()] = c
		u.chatList = append(u.chatList, c)
		return c
	}
	gophers := add(&model.Chat{Net: model.NetTelegram, ID: 100, Kind: model.ChatGroup, Title: "Gophers", Username: "gophers", LastDate: now, ReadOutboxMaxID: 8, ReadInboxMaxID: 6, Unread: 2, Pinned: true})
	add(&model.Chat{Net: model.NetTelegram, ID: 7, Kind: model.ChatUser, Title: "Alice", Username: "alice", LastDate: now.Add(-9 * time.Minute), Unread: 1})
	add(&model.Chat{Net: model.NetTelegram, ID: 8, Kind: model.ChatUser, Title: "Bob", Username: "bob", LastDate: now.Add(-3 * time.Hour)})
	add(&model.Chat{Net: model.NetTelegram, ID: 101, Kind: model.ChatChannel, Title: "Go Announcements", Username: "golang_news", LastDate: now.Add(-26 * time.Hour), Channel: true})
	add(&model.Chat{Net: model.NetDiscord, ID: 9, Kind: model.ChatUser, Title: "dave", Username: "dave", LastDate: now.Add(-40 * time.Minute), Unread: 3})
	add(&model.Chat{Net: model.NetDiscord, ID: 201, Kind: model.ChatGroup, Title: "Gophers / #general", LastDate: now.Add(-20 * time.Minute)})
	add(&model.Chat{Net: model.NetDiscord, ID: 202, Kind: model.ChatGroup, Title: "Gophers / #help", LastDate: now.Add(-2 * time.Hour), Unread: 12})
	add(&model.Chat{Net: model.NetDiscord, ID: 203, Kind: model.ChatGroup, Title: "Gophers / #offtopic", LastDate: now.Add(-5 * time.Hour)})
	goChan := add(&model.Chat{Net: libera, ID: 900, Kind: model.ChatGroup, Title: "#go", LastDate: now.Add(-3 * time.Minute)})
	aliceIRC := add(&model.Chat{Net: libera, ID: 901, Kind: model.ChatUser, Title: "alice", LastDate: now.Add(-7 * time.Minute), Unread: 2})
	u.presence[model.ChatKey{Net: model.NetTelegram, ID: 7}] = "online"

	w := u.ws.New(false)
	w.Chat, w.Loaded, w.MarkID = gophers, true, 6
	msg := func(id int, from string, fromID int64, at time.Duration, text string) *model.Msg {
		return &model.Msg{Net: model.NetTelegram, ChatID: 100, ID: id, Date: now.Add(at), From: from, FromID: fromID, Text: text}
	}
	m1 := msg(1, "alice", 7, -52*time.Minute, "Morning! Has anyone tried the kitty graphics protocol from Go yet?")
	m1.Entities = []model.Span{span(m1.Text, "kitty graphics protocol", model.SpanBold, "")}
	m2 := msg(2, "bob", 8, -50*time.Minute, "Yes — rsc.io/qr encodes and kitty renders it, no cgo, no surprises.")
	m2.Entities = []model.Span{span(m2.Text, "rsc.io/qr", model.SpanCode, "")}
	m2.Reply = &model.Quote{ID: 1, From: "alice", Text: "Has anyone tried the kitty graphics protocol"}
	m2.Reactions = []model.Reaction{{Emoji: "👍", Count: 3, Mine: true}, {Emoji: "🔥", Count: 1}}
	m3 := msg(3, "chris", 1, -47*time.Minute, "TTYloom: many networks, one terminal. Code at https://github.com/govlog/ttyloom")
	m3.Out = true
	m3.Entities = []model.Span{span(m3.Text, "https://github.com/govlog/ttyloom", model.SpanURL, "https://github.com/govlog/ttyloom")}
	m4 := msg(4, "alice", 7, -31*time.Minute, "")
	m4.Media = gif(t, "gif-telegram-7.png")
	m5 := msg(5, "carol", 11, -30*time.Minute, "")
	m5.Service = "carol joined the group"
	m6 := msg(6, "dave", 9, -12*time.Minute, "TTY means terminal. A loom weaves threads together. That is where the name comes from.")
	m6.FwdFrom = "Project notes"
	m7 := msg(7, "alice", 7, -2*time.Minute, "Also: /gif works on both networks now 🎉")
	m8 := msg(8, "bob", 8, -1*time.Minute, "Nice. Ctrl+G, type, Enter — that is it?")
	for _, m := range []*model.Msg{m1, m2, m3, m4, m5, m6, m7, m8} {
		m.ChatLabel = gophers.Title
		w.Upsert(m)
		u.agg.Upsert(m)
	}
	bind := func(c *model.Chat, act int) *Window {
		h := u.ws.New(true)
		h.Chat, h.Loaded, h.Act = c, true, act
		return h
	}
	bind(goChan, 0)
	bind(aliceIRC, 0)
	bind(u.chatList[4], 15) // dave: the discord count of the tab bar
	bind(u.chatList[1], 3)  // Alice: the telegram one
	u.setSel(w, w.Items[1]) // bob's answer: palette line and marker
	u.typing[gophers.Key()] = typing{who: "alice", until: now.Add(time.Hour)}
	u.ws.List[0].Act = 0
	u.ed.Set("Exactly. Enter sends the one under the cursor")
	return u
}

func shotSearch(_ *testing.T, u *UI) {
	u.search = &searchState{q: []rune("kitty"), cur: 0}
	tg, dc := u.chatList[0], u.chatList[5]
	now := shotTime
	u.gsearch = &globalSearch{query: []rune("kitty"), sent: "kitty", hits: []model.SearchHit{
		{Chat: tg, MsgID: 1, Date: now.Add(-52 * time.Minute), From: "alice", Text: "Morning! Has anyone tried the kitty graphics protocol from Go yet?"},
		{Chat: dc, MsgID: 40, Date: now.Add(-3 * time.Hour), From: "dave", Text: "kitty and Ghostty both do the graphics protocol, WezTerm too"},
		{Chat: u.chatList[1], MsgID: 12, Date: now.Add(-26 * time.Hour), From: "alice", Text: "switched to kitty for the images, worth it"},
		{Chat: u.chatList[6], MsgID: 77, Date: now.Add(-2 * 24 * time.Hour), From: "erin", Text: "is the kitty keyboard protocol on by default?"},
	}}
}

func shotGifs(t *testing.T, u *UI) {
	u.gifs = &gifBox{chat: u.chatList[0], query: []rune("landscape"), sent: "landscape", asked: true}
	// The frames answered by the two GIF searches for that query, as
	// docs/screenshots/fixtures/SOURCES.md lists them.
	for i, name := range []string{"gif-discord-2.png", "gif-discord-4.png", "gif-discord-6.png",
		"gif-telegram-4.png", "gif-telegram-7.png", "gif-telegram-3.png"} {
		md := gif(t, name)
		md.Label, md.Loc = "[gif]", i
		u.gifs.gifs = append(u.gifs.gifs, model.Gif{Preview: md, Send: i})
	}
	u.gifLayout()
	u.gifs.cur = 1
}

func shotMembers(_ *testing.T, u *UI) {
	u.partsOn = true
	u.parts = &partsBox{chat: u.chatList[0].Key(), title: "Gophers", mark: 2, lines: []model.Participant{
		{Text: "★ chris (you)", Query: "@chris", Online: true},
		{Text: "★ alice", Query: "@alice", Online: true},
		{Text: "bob", Query: "@bob"},
		{Text: "carol", Query: "@carol", Online: true},
		{Text: "dave", Query: "@dave"},
		{Text: "erin", Query: "@erin"},
	}}
	r, _ := u.partsRect()
	u.menu = &ctxMenu{chat: u.chatList[0], member: "@bob", entries: menuEntries(model.ChatUser, true, model.AllCaps()), x: r.col + 4, y: r.row + 3}
}

func shotNewChat(_ *testing.T, u *UI) {
	u.newChat = &newChatBox{query: []rune("go")}
	u.ncFilter()
	u.newChat.found = []*model.Chat{{Net: model.NetTelegram, ID: 300, Kind: model.ChatChannel, Title: "Go Weekly", Username: "goweekly"}}
	u.ncFilter()
}

func shotPicker(_ *testing.T, u *UI) {
	u.picker = newPicker(min(60, u.t.Cols-4), min(14, u.t.Rows-4), func(string) {})
	u.picker.filter()
	u.picker.cur = 7
}

func shotDiscord(t *testing.T, u *UI) {
	chat := u.chatList[5]
	w := u.ws.List[1]
	w.Chat, w.Items, w.MarkID, w.Sel = chat, nil, 0, nil
	messages := []struct{ from, text string }{
		{"dave", "Welcome to #general. Telegram is still one window away."},
		{"erin", "Same shortcuts here: reply, edit, react, search."},
		{"chris", "And the same images, right inside the terminal."},
		{"dave", ""},
		{"erin", "Press Ctrl+F twice to search across both networks."},
	}
	for i, item := range messages {
		m := &model.Msg{Net: model.NetDiscord, ChatID: chat.ID, ChatLabel: chat.Title, ID: 40 + i,
			Date: shotTime.Add(time.Duration(i-5) * time.Minute), From: item.from, FromID: int64(i + 10), Text: item.text}
		if i == 1 {
			m.Reactions = []model.Reaction{{Emoji: "👍", Count: 4, Mine: true}}
		}
		if i == 2 {
			m.Out = true
		}
		if i == 3 {
			m.Media = gif(t, "gif-discord-6.png")
		}
		w.Upsert(m)
	}
	u.ed.Set("One terminal. All the conversations.")
}

// shotMain : the opening view, tab bar on, with the all tab current.
func shotMain(_ *testing.T, u *UI) { u.cfg.Tabs = true }

// shotTabs : the irc:libera tab — the #go window with its service lines, a
// WHOIS answer and a CTCP reply, a private window waiting with a hot counter,
// and the away message in the status bar.
func shotTabs(_ *testing.T, u *UI) {
	u.cfg.Tabs = true
	libera := model.IRCNet("libera")
	u.setNetFilter(libera)
	u.ws.Cur = 2 // #go
	w := u.ws.Current()
	now := shotTime
	line := func(id int, from string, fromID int64, at time.Duration, text string) *model.Msg {
		return &model.Msg{Net: libera, ChatID: 900, ChatLabel: "#go", ID: id, Date: now.Add(at), From: from, FromID: fromID, Text: text}
	}
	join := line(1, "chris", 3, -18*time.Minute, "")
	join.Service = "You have joined #go"
	topic := line(2, "chris", 3, -18*time.Minute, "")
	topic.Service = "Topic: Go on IRC · https://go.dev"
	m3 := line(3, "alice", 11, -6*time.Minute, "The WHOIS of this server answers with the channel list too.")
	m4 := line(4, "bob", 12, -4*time.Minute, "And /ctcp alice VERSION comes back as a line of the window.")
	m5 := line(5, "chris", 3, -2*time.Minute, "Both, yes. F9 keeps every network on its own tab.")
	m5.Out = true
	for _, m := range []*model.Msg{join, topic, m3, m4, m5} {
		w.Upsert(m)
	}
	for _, s := range []string{
		"┌ alice (~alice@user/alice)",
		"│ ircname  : Alice",
		"│ server   : platinum.libera.chat (Stockholm, SE)",
		"│ channels : @#go #dev",
		"│ idle     : 2m0s, signon 2026-09-13 12:04",
		"└ End of WHOIS",
		"[ctcp(bob)] VERSION ttyloom v0.5-beta",
	} {
		w.AddSys(s)
	}
	priv := u.ws.List[3] // alice in private: waiting, and hot
	priv.Act, priv.Hot = 2, true
	u.pulse = true
	u.setAway(libera, "lunch")
	u.ed.Set("/mode #go +ntk ")
}
