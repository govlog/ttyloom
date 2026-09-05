package ui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
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
	lang := os.Getenv("TTYLOOM_SHOTS_LANG")
	if lang == "" {
		lang = "en"
	}
	i18n.Set(lang)
	defer i18n.Set("fr") // TestMain fixed the language of the other tests
	th, err := theme.Parse(strings.NewReader(mocha), "Catppuccin Mocha")
	if err != nil {
		t.Fatal(err)
	}
	for name, scene := range map[string]func(*UI){
		"main":    func(*UI) {},
		"discord": shotDiscord,
		"search":  shotSearch,
		"gifs":    shotGifs,
		"members": shotMembers,
		"newchat": shotNewChat,
		"picker":  shotPicker,
	} {
		var buf bytes.Buffer
		u := shotUI(th, &buf)
		scene(u)
		u.draw()
		frame := bytes.ReplaceAll(buf.Bytes(), []byte(time.Now().Format("[15:04]")), []byte("[15:42]"))
		if err := os.WriteFile(filepath.Join(dir, name+".ansi"), frame, 0o644); err != nil {
			t.Fatal(err)
		}
	}
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

const shotCols, shotRows = 112, 34

var shotTime = time.Date(2026, 9, 5, 15, 42, 0, 0, time.UTC)

// samplePNG draws a mountain landscape for the screenshot fixtures.
func samplePNG(w, h int, tint color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			f, g := float64(x)/float64(w), float64(y)/float64(h)
			c := color.RGBA{uint8(float64(tint.R) * (0.4 + 0.6*g)), uint8(float64(tint.G) * (0.4 + 0.6*g)), tint.B, 255}
			dx, dy := f-0.72, (g-0.29)*float64(h)/float64(w)
			if dx*dx+dy*dy < 0.004 {
				c = color.RGBA{255, 223, 164, 255}
			}
			if g > 0.85-0.65*(1-math.Abs(f-0.3)*2) {
				c = color.RGBA{91, 88, 142, 255}
			}
			if g > 0.95-0.55*(1-math.Abs(f-0.78)*2) {
				c = color.RGBA{55, 65, 102, 255}
			}
			if g > 0.83 {
				c = color.RGBA{uint8(37 + 20*g), uint8(69 + 30*f), uint8(105 + 40*f), 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// span : the entity over sub in text, offsets in runes like the model.
func span(text, sub string, kind model.SpanKind, url string) model.Span {
	start := len([]rune(text[:strings.Index(text, sub)]))
	return model.Span{Start: start, End: start + len([]rune(sub)), Kind: kind, URL: url}
}

func photo(label string, tint color.RGBA) *model.Media {
	return &model.Media{Kind: model.MediaPhoto, Label: label, State: model.MediaReady, Path: "x",
		Frames: [][]byte{samplePNG(320, 180, tint)}, FrameW: 320, FrameH: 180, W: 1280, H: 720, Loc: 1, Ext: ".jpg", Mime: "image/jpeg"}
}

// shotUI builds a UI the way Run does, on an offscreen terminal, with two
// networks, a sidebar of sections and a group window of styled messages.
func shotUI(th theme.Theme, out *bytes.Buffer) *UI {
	tm := term.NewOffscreen(out, shotCols, shotRows)
	tm.Kitty, tm.CellW, tm.CellH = true, 8, 17
	cfg := &config.Config{SidebarSort: "recent", Hover: config.HoverMenu, Timestamps: true, LinkPreviews: true,
		Separator: true, Redline: true, SidebarWidth: 26, Images: "kitty", Video: "show", Notify: "off", KittyImages: 48}
	tg := &fakeBackend{caps: model.AllCaps()}
	dc := &fakeBackend{caps: model.Caps{Reactions: true, Edit: true, Gifs: true, Search: true, GlobalSearch: true}}
	u := &UI{ctx: context.Background(), t: tm, cfg: cfg, th: th, events: make(chan model.Event, 8), ws: NewWindows(),
		agg: &Window{}, debug: &Window{}, focused: true, images: "kitty", side: sideChats, sideW: 26,
		nets:  map[string]model.Backend{model.NetTelegram: tg, model.NetDiscord: dc},
		chats: map[model.ChatKey]*model.Chat{}, pending: map[string]*Window{}, typing: map[model.ChatKey]typing{},
		lastTyping: map[model.ChatKey]time.Time{}, avatars: map[model.ChatKey]*model.Media{}, openNext: map[*model.Media]bool{},
		pendingMsg: map[int64]string{}, presence: map[model.ChatKey]string{}, dirty: map[model.ChatKey]bool{},
		partsCache: map[model.ChatKey]partsEntry{}, whoCache: map[whoKey]whoEntry{}, aliases: map[model.ChatKey]string{},
		folded: map[string]bool{}, self: map[string]selfInfo{model.NetTelegram: {ID: 1, Name: "chris"}, model.NetDiscord: {ID: 2, Name: "chris"}},
		conn: map[string]bool{model.NetTelegram: true, model.NetDiscord: true}, dialogsSeen: map[string]bool{},
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
	m4.Media = photo("[photo 1280x720 · 212 KB]", color.RGBA{120, 90, 220, 255})
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
	u.setSel(w, w.Items[1]) // bob's answer: palette line and marker
	u.typing[gophers.Key()] = typing{who: "alice", until: now.Add(time.Hour)}
	u.ws.List[0].Act = 0
	u.ed.Set("Exactly. Enter sends the one under the cursor")
	return u
}

func shotSearch(u *UI) {
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

func shotGifs(u *UI) {
	u.gifs = &gifBox{chat: u.chatList[0], query: []rune("landscape"), sent: "landscape", asked: true}
	tints := []color.RGBA{{230, 120, 90, 255}, {90, 200, 160, 255}, {120, 120, 240, 255}, {240, 200, 80, 255}, {200, 90, 200, 255}, {80, 180, 230, 255}}
	for i, tint := range tints {
		md := &model.Media{Kind: model.MediaGIF, Label: "[gif]", State: model.MediaReady, Path: "x", Loc: i, Ext: ".gif", Mime: "image/gif",
			Frames: [][]byte{samplePNG(96, 54, tint)}, FrameW: 96, FrameH: 54, W: 498, H: 280}
		u.gifs.gifs = append(u.gifs.gifs, model.Gif{Preview: md, Send: i})
	}
	u.gifLayout()
	u.gifs.cur = 1
}

func shotMembers(u *UI) {
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

func shotNewChat(u *UI) {
	u.newChat = &newChatBox{query: []rune("go")}
	u.ncFilter()
	u.newChat.found = []*model.Chat{{Net: model.NetTelegram, ID: 300, Kind: model.ChatChannel, Title: "Go Weekly", Username: "goweekly"}}
	u.ncFilter()
}

func shotPicker(u *UI) {
	u.picker = newPicker(min(60, u.t.Cols-4), min(14, u.t.Rows-4), func(string) {})
	u.picker.filter()
	u.picker.cur = 7
}

func shotDiscord(u *UI) {
	chat := u.chatList[5]
	w := u.ws.List[1]
	w.Chat, w.Items, w.MarkID, w.Sel = chat, nil, 0, nil
	messages := []struct{ from, text string }{
		{"dave", "Welcome to #general. Telegram is still one window away."},
		{"erin", "Same shortcuts here: reply, edit, react, search."},
		{"chris", "And the same photos, right inside the terminal."},
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
			m.Media = photo("[photo 1280x720 · 184 KB]", color.RGBA{230, 145, 140, 255})
		}
		w.Upsert(m)
	}
	u.ed.Set("One terminal. All the conversations.")
}
