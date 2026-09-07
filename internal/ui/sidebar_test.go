package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// testSideW : sidebar width of the tests (default of sidebar_width).
const testSideW = 26

func hasReverse(l render.Line) bool {
	for _, sp := range content(l) {
		if sp.Style.Reverse {
			return true
		}
	}
	return false
}

// content gives the spans of the content, separator left out.
func content(l render.Line) []render.Span { return l.Spans[:len(l.Spans)-1] }

// oneBar tells whether the whole line is one single inverted bar (same FG everywhere).
func oneBar(l render.Line) bool {
	for _, sp := range content(l) {
		if !sp.Style.Reverse || sp.Style.FG != content(l)[0].Style.FG {
			return false
		}
	}
	return render.Width(render.LineText(l)) == testSideW+1
}

// body gives the content of the line, separator and filling taken away.
func body(l render.Line) string {
	return strings.TrimRight(strings.TrimSuffix(render.LineText(l), "│"), " ")
}

func TestSidebarLines(t *testing.T) {
	th := theme.Terminal()
	now := time.Now()
	old := &model.Chat{ID: 1, Title: "ancien", LastDate: now.Add(-time.Hour)}
	recent := &model.Chat{ID: 2, Title: "récent\x1bx", Unread: 5, LastDate: now}
	pin := &model.Chat{ID: 3, Title: "épinglé", Pinned: true, LastDate: now.Add(-48 * time.Hour)}
	ws := []*Window{{}, {Chat: old}} // current window = 1, bound to "ancien"
	// sidebarLines no longer sorts (u.sortedChats() on the caller side): the
	// input list is already sorted, as the real caller would have it.
	sorted := sortChats([]*model.Chat{old, recent, pin}, "recent")

	lines := sidebarLines(sideChats, sorted, ws, nil, 1, th, testSideW, 5, 0, false, false, 0, -1, false, nil)
	if len(lines) != 5 {
		t.Fatalf("height: %d lines", len(lines))
	}
	for i, want := range []string{"épinglé", "récent x", "ancien"} { // pinned first, then recent
		if !strings.Contains(render.LineText(lines[i]), want) {
			t.Fatalf("line %d = %q, want %q", i, render.LineText(lines[i]), want)
		}
	}
	if strings.ContainsRune(render.LineText(lines[1]), 0x1b) {
		t.Fatalf("Clean not applied: %q", render.LineText(lines[1]))
	}
	if !strings.Contains(render.LineText(lines[1]), "5") {
		t.Fatalf("unread missing: %q", render.LineText(lines[1]))
	}
	if strings.Contains(render.LineText(lines[0]), "5") || strings.Contains(render.LineText(lines[2]), "5") {
		t.Fatal("unread shown at 0")
	}
	if !hasReverse(lines[2]) || hasReverse(lines[0]) || hasReverse(lines[1]) {
		t.Fatal("only the chat of the current window is reversed")
	}
	if !oneBar(lines[2]) { // bar in one piece, not three patchy spans
		t.Fatalf("current line patchy: %+v", content(lines[2]))
	}
	if !strings.Contains(render.LineText(lines[2]), "[·]") || strings.Contains(render.LineText(lines[0]), "[·]") {
		t.Fatalf("window marker: %q / %q", render.LineText(lines[2]), render.LineText(lines[0]))
	}
	for i, l := range lines {
		if got := render.Width(render.LineText(l)); got != testSideW+1 {
			t.Fatalf("line %d wide %d: %q", i, got, render.LineText(l))
		}
		if !strings.HasSuffix(render.LineText(l), "│") {
			t.Fatalf("line %d without separator: %q", i, render.LineText(l))
		}
	}

	ws[1].Act = 2
	lines = sidebarLines(sideWindows, nil, ws, nil, 0, th, testSideW, 3, 0, false, false, 0, -1, false, nil)
	if got := body(lines[0]); got != "*0: (none)" { // *: status window
		t.Fatalf("window 0: %q", got)
	}
	if got := body(lines[1]); got != "@1: ancien (2)" { // @: private chat
		t.Fatalf("window 1: %q", got)
	}
	if !hasReverse(lines[0]) || hasReverse(lines[1]) {
		t.Fatal("current window reversed")
	}
	if !oneBar(lines[0]) {
		t.Fatalf("current line patchy: %+v", content(lines[0]))
	}
	if got := body(lines[2]); got != "" {
		t.Fatalf("filler line: %q", got)
	}

	// Avatars: gutter of 3 cells at the head of the line, chat id on the line.
	av := sidebarLines(sideChats, sorted, ws, nil, 1, th, testSideW, 5, 0, true, false, 0, -1, false, nil)
	for i, want := range []int64{3, 2, 1} { // pinned, recent, old
		if av[i].Avatar != want {
			t.Fatalf("line %d: avatar %d, want %d", i, av[i].Avatar, want)
		}
		if !strings.HasPrefix(render.LineText(av[i]), "   ") {
			t.Fatalf("line %d without gutter: %q", i, render.LineText(av[i]))
		}
		if got := render.Width(render.LineText(av[i])); got != testSideW+1 {
			t.Fatalf("line %d wide %d: %q", i, got, render.LineText(av[i]))
		}
	}
	if av[3].Avatar != 0 { // filling line
		t.Fatalf("filler: avatar %d", av[3].Avatar)
	}
	if sidebarLines(sideWindows, nil, ws, nil, 0, th, testSideW, 3, 0, true, false, 0, -1, false, nil)[0].Avatar != 0 {
		t.Fatal("window mode: no avatar")
	}

	// sepRow : the vertical bar becomes ├ on that line, │ elsewhere, width
	// unchanged (message separator, not the one of the sidebar).
	sepLines := sidebarLines(sideChats, sorted, ws, nil, 1, th, testSideW, 5, 0, false, false, 0, 2, false, nil)
	for i, l := range sepLines {
		want := "│"
		if i == 2 {
			want = "├"
		}
		if !strings.HasSuffix(render.LineText(l), want) {
			t.Fatalf("line %d: separator %q expected, %q", i, want, render.LineText(l))
		}
		if got := render.Width(render.LineText(l)); got != testSideW+1 {
			t.Fatalf("line %d wide %d with sepRow: %q", i, got, render.LineText(l))
		}
	}
}

// TestSidebarKindPrefix : ircii style prefix before the title for Chat.Kind —
// # group/supergroup, & channel, @ private chat — line width unchanged
// (testSideW+1), in both sidebar modes.
func TestSidebarKindPrefix(t *testing.T) {
	th := theme.Terminal()
	priv := &model.Chat{ID: 1, Kind: model.ChatUser, Title: "Alice"}
	grp := &model.Chat{ID: 2, Kind: model.ChatGroup, Title: "Groupe"}
	canal := &model.Chat{ID: 3, Kind: model.ChatChannel, Title: "Canal"}

	lines := sidebarLines(sideChats, []*model.Chat{priv, grp, canal}, []*Window{{}}, nil, 0, th, testSideW, 3, 0, false, false, 0, -1, false, nil)
	want := []string{"@", "#", "&"} // entry order: sidebarLines no longer sorts (up to the caller)
	for i, l := range lines {
		b, pfx := body(l), want[i]
		if got := render.Width(render.LineText(l)); got != testSideW+1 {
			t.Fatalf("line %d wide %d: %q", i, got, render.LineText(l))
		}
		if pfx == "" {
			if strings.ContainsAny(b, "#&") {
				t.Fatalf("line %d (private): unexpected prefix %q", i, b)
			}
			continue
		}
		if !strings.Contains(b, pfx+" ") {
			t.Fatalf("line %d: prefix %q missing from %q", i, pfx, b)
		}
	}

	// Window mode: prefix for the chat bound to each window.
	ws := []*Window{{Chat: grp}, {Chat: canal}, {Chat: priv}, {}}
	wlines := sidebarLines(sideWindows, nil, ws, nil, 0, th, testSideW, len(ws), 0, false, false, 0, -1, false, nil)
	wwant := []string{"#", "&", "@", ""}
	for i, l := range wlines {
		b, pfx := body(l), wwant[i]
		if got := render.Width(render.LineText(l)); got != testSideW+1 {
			t.Fatalf("window %d wide %d: %q", i, got, render.LineText(l))
		}
		if pfx == "" {
			if strings.HasPrefix(b, "#") || strings.HasPrefix(b, "&") {
				t.Fatalf("window %d: unexpected prefix %q", i, b)
			}
			continue
		}
		if !strings.HasPrefix(b, pfx) {
			t.Fatalf("window %d: prefix %q missing from %q", i, pfx, b)
		}
	}
}

// fit must not shift the next column when the title holds an emoji with a
// variation selector (❤️ counts 2 cells, not 1).
func TestFitVS16(t *testing.T) {
	if got := render.Width(fit("Friends ❤️ TBD", 20)); got != 20 {
		t.Fatalf("fit ❤️: width %d", got)
	}
	if got := render.Width(fit("🙋🏻‍♀️ Support", 20)); got != 20 { // ZWJ + skin tone
		t.Fatalf("fit ZWJ: width %d", got)
	}
}

// TestMarquee : short title unchanged, long title at each step of exact width,
// last step ending on the end of the title, clusters (❤️, ZWJ) never cut.
func TestMarquee(t *testing.T) {
	short := "Support"
	if got, want := marquee(short, 20, 0), fit(short, 20); got != want {
		t.Fatalf("short title, step 0: %q, want %q", got, want)
	}
	if got, want := marquee(short, 20, 9), fit(short, 20); got != want { // step ignored when it fits
		t.Fatalf("short title, step 9: %q, want %q", got, want)
	}

	// ❤️ and 🙋🏻‍♀️ (ZWJ + skin tone) at the head: width always exact, even when
	// the window cuts just before a cluster of width 2.
	title := "❤️🙋🏻‍♀️ Support technique général clientèle très très long"
	const width = 12
	if render.Width(title) <= width {
		t.Fatalf("test title not long enough: %d cells", render.Width(title))
	}
	if got := marquee(title, width, 0); got != fit(title, width) {
		t.Fatalf("step 0 must show the beginning: %q, want %q", got, fit(title, width))
	}

	max := marqueeMax(title, width)
	if max <= 0 {
		t.Fatalf("marqueeMax: %d", max)
	}
	for step := 0; step <= max; step++ {
		got := marquee(title, width, step)
		if w := render.Width(got); w != width {
			t.Fatalf("step %d: width %d, want %d (%q) — cluster cut?", step, w, width, got)
		}
	}

	end := strings.TrimRight(marquee(title, width, max), " ")
	if !strings.HasSuffix(title, end) {
		t.Fatalf("step max = %q, must end the title %q", end, title)
	}
	if end2 := strings.TrimRight(marquee(title, width, max+5), " "); end2 != end { // step above max: capped
		t.Fatalf("step > max not capped: %q vs %q", end2, end)
	}
}

func TestSideOffset(t *testing.T) {
	if got := sideOffset(3, 5, 2); got != 0 { // everything fits: no scrolling
		t.Fatalf("short: %d", got)
	}
	if got := sideOffset(10, 3, 4); got != 4 { // the scroll asked for is kept
		t.Fatalf("wheel: %d", got)
	}
	if got := sideOffset(10, 3, 99); got != 7 { // bounded at the bottom of the list
		t.Fatalf("upper bound: %d", got)
	}
	if got := sideOffset(10, 3, -1); got != 0 {
		t.Fatalf("lower bound: %d", got)
	}
}

// chatTitles : titles in order, what the sort tests compare.
func chatTitles(cs []*model.Chat) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Title
	}
	return out
}

// TestSortChats : the 3 modes (F7 / sidebar_sort), pinned first in
// recent/alpha (and in unread on a tie of unread counts), ties kept stable,
// accents and case ignored in alpha (render.Fold).
func TestSortChats(t *testing.T) {
	now := time.Now()
	a := &model.Chat{ID: 1, Title: "Zèbre", LastDate: now} // unread 0, very recent
	b := &model.Chat{ID: 2, Title: "café", Unread: 5, LastDate: now.Add(-2 * time.Hour)}
	c := &model.Chat{ID: 3, Title: "Été", Pinned: true, Unread: 2, LastDate: now.Add(-5 * time.Hour)} // pinned, the oldest
	d := &model.Chat{ID: 4, Title: "bob", LastDate: now.Add(-time.Hour)}                              // unread 0
	list := []*model.Chat{a, b, c, d}

	// recent : pinned first, then from the newest to the oldest.
	if got := chatTitles(sortChats(list, "recent")); !slices.Equal(got, []string{"Été", "Zèbre", "bob", "café"}) {
		t.Fatalf("recent: %v", got)
	}
	// unknown mode -> recent.
	if got := chatTitles(sortChats(list, "n'importe quoi")); !slices.Equal(got, []string{"Été", "Zèbre", "bob", "café"}) {
		t.Fatalf("unknown mode: %v", got)
	}
	// alpha : pinned first, then by title (Fold: lower case with no accent).
	if got := chatTitles(sortChats(list, "alpha")); !slices.Equal(got, []string{"Été", "bob", "café", "Zèbre"}) {
		t.Fatalf("alpha: %v", got)
	}
	// unread : unread first (going down); on a tie (a, d at 0, none pinned)
	// -> entry order (stability).
	if got := chatTitles(sortChats(list, "unread")); !slices.Equal(got, []string{"café", "Été", "Zèbre", "bob"}) {
		t.Fatalf("unread: %v", got)
	}
	// unread, tie with a pinned one: it comes first even when it is later in
	// list (it falls back on the recent order, not on the entry order alone).
	x := &model.Chat{ID: 5, Title: "x", Unread: 2}
	y := &model.Chat{ID: 6, Title: "y", Unread: 2, Pinned: true}
	if got := chatTitles(sortChats([]*model.Chat{x, y}, "unread")); !slices.Equal(got, []string{"y", "x"}) {
		t.Fatalf("unread tie pinned: %v", got)
	}

	// sortChats does not change list (a new slice).
	if list[0] != a || list[1] != b || list[2] != c || list[3] != d {
		t.Fatalf("list modified in place: %v", chatTitles(list))
	}
}

// TestSortChatsGuildGrouping : the channels of one Discord guild stay next to
// each other whatever the mode — the guild takes the place of its best-ranked
// channel, and inside it the channels follow one another by title. A pinned
// channel does not drag its guild into the pinned block, and a Telegram title
// that looks like one ("notes / #general") is left alone.
func TestSortChatsGuildGrouping(t *testing.T) {
	now := time.Now()
	gen := &model.Chat{Net: model.NetDiscord, ID: 1, Title: "Gophers / #general", LastDate: now}
	bob := &model.Chat{Net: model.NetTelegram, ID: 2, Title: "Bob", LastDate: now.Add(-time.Hour)}
	ann := &model.Chat{Net: model.NetDiscord, ID: 3, Title: "Gophers / #annonces", LastDate: now.Add(-2 * time.Hour)}
	eve := &model.Chat{Net: model.NetDiscord, ID: 4, Title: "Eve", LastDate: now.Add(-3 * time.Hour)} // DM: no guild
	list := []*model.Chat{gen, bob, ann, eve}

	// recent : the guild sits where #general was, #annonces comes up with it.
	want := []string{"Gophers / #annonces", "Gophers / #general", "Bob", "Eve"}
	if got := chatTitles(sortChats(list, "recent")); !slices.Equal(got, want) {
		t.Fatalf("recent: %v, want %v", got, want)
	}
	// unread : same grouping, the guild follows its best-ranked channel.
	ann.Unread = 3
	want = []string{"Gophers / #annonces", "Gophers / #general", "Bob", "Eve"}
	if got := chatTitles(sortChats(list, "unread")); !slices.Equal(got, want) {
		t.Fatalf("unread: %v, want %v", got, want)
	}
	ann.Unread = 0

	// A pinned channel stays in the pinned block alone: grouping never crosses it.
	pin := &model.Chat{Net: model.NetDiscord, ID: 5, Title: "Gophers / #annonces", Pinned: true, LastDate: now.Add(-9 * time.Hour)}
	older := &model.Chat{Net: model.NetDiscord, ID: 6, Title: "Gophers / #general", LastDate: now.Add(-4 * time.Hour)}
	want = []string{"Gophers / #annonces", "Bob", "Gophers / #general"}
	if got := chatTitles(sortChats([]*model.Chat{pin, bob, older}, "recent")); !slices.Equal(got, want) {
		t.Fatalf("pinned: %v, want %v", got, want)
	}

	// Telegram only: the pass changes nothing, even on a title shaped like a
	// guild channel.
	t1 := &model.Chat{Net: model.NetTelegram, ID: 7, Title: "notes / #general", LastDate: now}
	t2 := &model.Chat{Net: model.NetTelegram, ID: 8, Title: "notes / #annonces", LastDate: now.Add(-2 * time.Hour)}
	mono := []*model.Chat{t1, bob, t2}
	want = []string{"notes / #general", "Bob", "notes / #annonces"}
	if got := chatTitles(sortChats(mono, "recent")); !slices.Equal(got, want) {
		t.Fatalf("telegram only: %v, want %v", got, want)
	}
}

// rowNames : one string per row — "=<name>" for a header, the chat title for
// a chat line. What the row model tests compare.
func rowNames(rows []sideRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		if r.chat == nil {
			out[i] = "=" + r.name
			continue
		}
		out[i] = r.chat.Title
	}
	return out
}

// TestSectionOf : the key of a section is the network, plus the guild for a
// Discord guild channel. A Telegram title shaped like one is not a guild.
func TestSectionOf(t *testing.T) {
	for _, c := range []struct {
		chat *model.Chat
		want string
	}{
		{&model.Chat{Net: model.NetTelegram, Title: "Bob"}, "telegram"},
		{&model.Chat{Net: model.NetDiscord, Title: "Eve"}, "discord"},
		{&model.Chat{Net: model.NetDiscord, Title: "Gophers / #general"}, "discord:Gophers"},
		{&model.Chat{Net: model.NetTelegram, Title: "notes / #general"}, "telegram"},
	} {
		if got := sectionOf(c.chat); got != c.want {
			t.Fatalf("%q: %q, want %q", c.chat.Title, got, c.want)
		}
		if got := sectionName(c.want); got != "Gophers" && got != c.want {
			t.Fatalf("sectionName(%q) = %q", c.want, got)
		}
	}
}

// TestSectionRows : under two sections the rows are the chats one for one,
// with no header — the mono-Telegram sidebar does not move. From two on, a
// header opens each section: networks in name order, and inside Discord the
// DMs before the guilds, the guilds in the order groupGuilds gave them.
func TestSectionRows(t *testing.T) {
	now := time.Now()
	bob := &model.Chat{Net: model.NetTelegram, ID: 1, Title: "Bob", LastDate: now}
	alice := &model.Chat{Net: model.NetTelegram, ID: 2, Title: "Alice", LastDate: now.Add(-time.Hour)}

	mono := sortChats([]*model.Chat{bob, alice}, "recent")
	if got := rowNames(sectionRows(mono, nil, map[string]bool{})); !slices.Equal(got, []string{"Bob", "Alice"}) {
		t.Fatalf("single section: %v", got)
	}

	eve := &model.Chat{Net: model.NetDiscord, ID: 3, Title: "Eve", LastDate: now.Add(-2 * time.Hour)}
	gen := &model.Chat{Net: model.NetDiscord, ID: 4, Title: "Gophers / #general", LastDate: now.Add(-3 * time.Hour)}
	ann := &model.Chat{Net: model.NetDiscord, ID: 5, Title: "Gophers / #annonces", LastDate: now.Add(-4 * time.Hour)}
	sorted := sortChats([]*model.Chat{bob, alice, eve, gen, ann}, "recent")

	want := []string{"=discord", "Eve", "=Gophers", "Gophers / #annonces", "Gophers / #general", "=telegram", "Bob", "Alice"}
	rows := sectionRows(sorted, nil, map[string]bool{})
	if got := rowNames(rows); !slices.Equal(got, want) {
		t.Fatalf("sections: %v, want %v", got, want)
	}
	if rows[1].sec != "discord" || rows[3].sec != "discord:Gophers" {
		t.Fatalf("chat rows carry their section: %q %q", rows[1].sec, rows[3].sec)
	}

	// Folded: the chats go, the header answers for their unread and for the
	// window one of them holds.
	ann.Unread, gen.Unread = 3, 4
	rows = sectionRows(sorted, []*Window{{Chat: gen}}, map[string]bool{"discord:Gophers": true})
	want = []string{"=discord", "Eve", "=Gophers", "=telegram", "Bob", "Alice"}
	if got := rowNames(rows); !slices.Equal(got, want) {
		t.Fatalf("folded: %v, want %v", got, want)
	}
	if h := rows[2]; !h.folded || h.unread != 7 || !h.window {
		t.Fatalf("aggregate of the folded header: %+v", h)
	}

	// nil fold state: no section at all, whatever the networks.
	if got := len(sectionRows(sorted, nil, nil)); got != len(sorted) {
		t.Fatalf("no fold state: %d rows for %d chats", got, len(sorted))
	}
}

// TestViewRows : one line less with the separator (/set separator).
func TestViewRows(t *testing.T) {
	u := &UI{cfg: &config.Config{}, t: &term.Term{Rows: 24}}
	if got := u.viewRows(); got != 22 {
		t.Fatalf("without separator: %d", got)
	}
	u.cfg.Separator = true
	if got := u.viewRows(); got != 21 {
		t.Fatalf("with separator: %d", got)
	}
}

// TestSideWidthClamp : the sidebar width stays in [12, cols/2], whatever the
// config or the drag of the bar asks for.
func TestSideWidthClamp(t *testing.T) {
	if got := clampSideW(26, 80); got != 26 {
		t.Fatalf("normal value: %d", got)
	}
	if got := clampSideW(200, 80); got != 40 { // cap: half of the screen
		t.Fatalf("ceiling: %d", got)
	}
	if got := clampSideW(3, 80); got != 12 { // floor
		t.Fatalf("floor: %d", got)
	}
	if got := clampSideW(26, 10); got != 12 { // narrow screen: the floor wins, layout() hides it
		t.Fatalf("narrow screen: %d", got)
	}
}

// TestSideDrag : press on the │ bar of the sidebar = grab, move = new width,
// release = width saved.
func TestSideDrag(t *testing.T) {
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: cfg, th: theme.Terminal(),
		t: &term.Term{Cols: 80, Rows: 24}, side: sideChats, sideW: 26}
	u.mouse(term.MouseEvent{Button: 0, X: 26, Y: 3, Press: true}) // column of the bar
	if u.drag != dragSide {
		t.Fatalf("press on the bar: drag = %v", u.drag)
	}
	u.mouse(term.MouseEvent{Button: 0, X: 40, Y: 3, Press: true, Motion: true})
	if u.sideW != 40 {
		t.Fatalf("move: sideW = %d", u.sideW)
	}
	if x0, _ := u.layout(); x0 != 41 {
		t.Fatalf("layout: x0 = %d", x0)
	}
	u.mouse(term.MouseEvent{Button: 0, X: 40, Y: 3})
	if u.drag != dragNone || cfg.SidebarWidth != 40 {
		t.Fatalf("release: drag = %v, sidebar_width = %d", u.drag, cfg.SidebarWidth)
	}
}

// TestSideMouseNewLine : the last line of the sidebar in chat mode is the
// "+ new message" line — it carries no chat and opens the overlay.
func TestSideMouseNewLine(t *testing.T) {
	c := &model.Chat{ID: 1, Kind: model.ChatUser, Title: "Alice"}
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{},
		cfg: &config.Config{SidebarSort: "recent"}, t: &term.Term{Cols: 80, Rows: 24},
		side: sideChats, sideW: 26, chatList: []*model.Chat{c},
		chats: map[model.ChatKey]*model.Chat{c.Key(): c}, aliases: map[model.ChatKey]string{},
		gotContacts: true} // no network call on opening
	last := u.t.Rows - 1
	if u.sideChatAt(last) != nil {
		t.Fatal("the last line carries no chat")
	}
	u.sideMouse(term.MouseEvent{X: 2, Y: last, Button: 0, Press: true})
	if u.newChat == nil {
		t.Fatal("click on \"new conversation\": the box should open")
	}
	u.newChat = nil
	// Right click on that line: no chat, so no context menu.
	u.sideMouse(term.MouseEvent{X: 2, Y: last, Button: 2, Press: true})
	if u.menu != nil {
		t.Fatal("context menu on a line without chat")
	}
	// Wheel: hover is not off, so the notch goes through sideStep — the chat
	// has no window, so there is no step and nothing opens.
	u.sideMouse(term.MouseEvent{X: 2, Y: 0, Button: 65, Press: true})
	if u.newChat != nil || u.menu != nil {
		t.Fatal("wheel: nothing should open")
	}
	// Windows mode: the last line is a plain line again.
	u.side = sideWindows
	u.sideMouse(term.MouseEvent{X: 2, Y: last, Button: 0, Press: true})
	if u.newChat != nil {
		t.Fatal("window mode: no \"new conversation\" line")
	}
}

// TestSidebarSingleLine : a chat title comes from the network and nothing
// bounds it; a \n written as it is after an absolute cursor move destroys the
// frame at each repaint. render.CleanLine keeps the bar on its lines.
func TestSidebarSingleLine(t *testing.T) {
	evil := &model.Chat{ID: 1, Title: strings.Repeat("\n", 500) + "TITRE", LastDate: time.Now()}
	lines := sidebarLines(sideChats, []*model.Chat{evil}, []*Window{{}}, nil, 0,
		theme.Terminal(), testSideW, 3, 0, false, false, 0, -1, false, nil)
	for i, l := range lines {
		if strings.ContainsRune(render.LineText(l), '\n') {
			t.Fatalf("line %d: remote line break written raw", i)
		}
		if got := render.Width(render.LineText(l)); got != testSideW+1 {
			t.Fatalf("line %d wide %d: %q", i, got, render.LineText(l))
		}
	}
}

// Multi-network: a discreet cell in front of the title carries the
// network's first letter as a superscript. Single-network, the column does
// not exist; in both cases the line width does not move.
func TestSidebarNetBadge(t *testing.T) {
	th := theme.Terminal()
	chats := []*model.Chat{
		{Net: model.NetTelegram, ID: 1, Title: "tg", LastDate: time.Now()},
		{Net: "discord", ID: 2, Title: "dc", LastDate: time.Now()},
	}
	off := sidebarLines(sideChats, chats, nil, nil, -1, th, testSideW, 2, 0, false, false, 0, -1, false, nil)
	for i, l := range off {
		if strings.ContainsAny(render.LineText(l), "ᵗᵈ") {
			t.Fatalf("line %d: badge shown in single-network mode: %q", i, render.LineText(l))
		}
	}
	on := sidebarLines(sideChats, chats, nil, nil, -1, th, testSideW, 2, 0, false, true, 0, -1, false, nil)
	for i, want := range []string{"ᵗ tg", "ᵈ dc"} {
		if !strings.Contains(render.LineText(on[i]), want) {
			t.Fatalf("line %d: %q, want %q", i, render.LineText(on[i]), want)
		}
	}
	for i, l := range append(off, on...) {
		if got := render.Width(render.LineText(l)); got != testSideW+1 {
			t.Fatalf("line %d wide %d: %q", i, got, render.LineText(l))
		}
	}
}

// TestSidebarSections : from two sections on, a header opens each of them,
// the ᵗ/ᵈ badge goes (the header names the network) and a guild channel drops
// the "Guild / " its header already carries. Line width never moves.
func TestSidebarSections(t *testing.T) {
	th := theme.Terminal()
	now := time.Now()
	bob := &model.Chat{Net: model.NetTelegram, ID: 1, Kind: model.ChatUser, Title: "Bob", LastDate: now}
	gen := &model.Chat{Net: model.NetDiscord, ID: 2, Kind: model.ChatGroup, Title: "Gophers / #general", LastDate: now.Add(-time.Hour)}
	sorted := sortChats([]*model.Chat{bob, gen}, "recent")

	sideSections = map[string]bool{} // sections on, nothing folded (sideBlock does this)
	defer func() { sideSections = nil }()
	lines := sidebarLines(sideChats, sorted, nil, nil, -1, th, testSideW, 4, 0, false, true, 0, -1, false, nil)
	for i, want := range []string{"── Gophers ", "# general", "── telegram ", "@ Bob"} {
		if !strings.Contains(render.LineText(lines[i]), want) {
			t.Fatalf("line %d = %q, want %q", i, render.LineText(lines[i]), want)
		}
	}
	if strings.ContainsAny(render.LineText(lines[1])+render.LineText(lines[3]), "ᵗᵈ") {
		t.Fatalf("badge kept under a header: %q %q", render.LineText(lines[1]), render.LineText(lines[3]))
	}
	if !strings.Contains(render.LineText(lines[0]), "[-]") || !strings.Contains(render.LineText(lines[2]), "[-]") {
		t.Fatalf("unfolded marker: %q %q", render.LineText(lines[0]), render.LineText(lines[2]))
	}
	for i, l := range lines {
		if got := render.Width(render.LineText(l)); got != testSideW+1 {
			t.Fatalf("line %d wide %d: %q", i, got, render.LineText(l))
		}
	}
}

// TestSidebarSectionFolded : a folded section hides its chats, and its header
// answers for them — unread total and window marker, in the columns of the
// chat lines.
func TestSidebarSectionFolded(t *testing.T) {
	th := theme.Terminal()
	now := time.Now()
	bob := &model.Chat{Net: model.NetTelegram, ID: 1, Kind: model.ChatUser, Title: "Bob", LastDate: now}
	gen := &model.Chat{Net: model.NetDiscord, ID: 2, Kind: model.ChatGroup, Title: "Gophers / #general", Unread: 4, LastDate: now.Add(-time.Hour)}
	ann := &model.Chat{Net: model.NetDiscord, ID: 3, Kind: model.ChatGroup, Title: "Gophers / #annonces", Unread: 3, LastDate: now.Add(-2 * time.Hour)}
	sorted := sortChats([]*model.Chat{bob, gen, ann}, "recent")

	sideSections = map[string]bool{"discord:Gophers": true}
	defer func() { sideSections = nil }()
	lines := sidebarLines(sideChats, sorted, []*Window{{Chat: gen}}, nil, -1, th, testSideW, 3, 0, false, true, 0, -1, false, nil)
	if head := render.LineText(lines[0]); !strings.Contains(head, "[+]") || !strings.Contains(head, "[·]") || !strings.Contains(head, " 7") {
		t.Fatalf("folded header: %q", head)
	}
	if strings.Contains(render.LineText(lines[1]), "general") || strings.Contains(render.LineText(lines[1]), "annonces") {
		t.Fatalf("hidden chat still drawn: %q", render.LineText(lines[1]))
	}
	for i, l := range lines {
		if got := render.Width(render.LineText(l)); got != testSideW+1 {
			t.Fatalf("line %d wide %d: %q", i, got, render.LineText(l))
		}
	}
}

// TestSideBlockMonoNoHeader : one network, no guild, fold state loaded (map
// not nil) — the sidebar still carries no header at all. Hard constraint of
// the feature.
func TestSideBlockMonoNoHeader(t *testing.T) {
	u := netUI(model.NetTelegram)
	u.th, u.side, u.sideW, u.folded = theme.Terminal(), sideChats, testSideW, map[string]bool{}
	lines, rows := u.sideBlock(-1)
	if len(rows) != 1 || rows[0].chat == nil {
		t.Fatalf("rows: %v", rowNames(rows))
	}
	for i, l := range lines {
		if strings.Contains(render.LineText(l), "[-]") || strings.Contains(render.LineText(l), "[+]") {
			t.Fatalf("line %d carries a fold marker: %q", i, render.LineText(l))
		}
	}
}

// TestSideBlockSections : two networks and the fold state loaded — sideBlock
// wires the headers and drops the badge.
func TestSideBlockSections(t *testing.T) {
	u := netUI(model.NetTelegram, model.NetDiscord)
	u.th, u.side, u.sideW, u.folded = theme.Terminal(), sideChats, testSideW, map[string]bool{}
	lines, rows := u.sideBlock(-1)
	if len(rows) != 4 {
		t.Fatalf("%d rows, want 2 headers + 2 chats: %v", len(rows), rowNames(rows))
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(render.LineText(l) + "\n")
	}
	if got := b.String(); !strings.Contains(got, "── discord ") || !strings.Contains(got, "── telegram ") || strings.ContainsAny(got, "ᵗᵈ") {
		t.Fatalf("sections: %q", got)
	}
}

// TestSideSectionToggle : a click on a header folds its section and a second
// one unfolds it; the row→chat mapping counts the header lines, and the wheel
// bounds itself on the rows.
func TestSideSectionToggle(t *testing.T) {
	u := netUI(model.NetTelegram, model.NetDiscord)
	u.th, u.side, u.sideW, u.folded = theme.Terminal(), sideChats, testSideW, map[string]bool{}
	if got := rowNames(u.sideRowList()); !slices.Equal(got, []string{"=discord", "discord-chat", "=telegram", "telegram-chat"}) {
		t.Fatalf("rows: %v", got)
	}
	// Mapping: the first list line is a header, the one under it a chat.
	if u.sideChatAt(sideHdr) != nil {
		t.Fatal("a header line carries no chat")
	}
	if c := u.sideChatAt(sideHdr + 1); c == nil || c.Net != model.NetDiscord {
		t.Fatalf("chat line under the header: %+v", c)
	}
	// Right click on a header: no chat, so no context menu.
	u.sideMouse(term.MouseEvent{X: 2, Y: sideHdr, Button: 2, Press: true})
	if u.menu != nil {
		t.Fatal("context menu on a header line")
	}
	u.sideClick(sideHdr)
	if !u.folded[model.NetDiscord] {
		t.Fatalf("click on the header: %v", u.folded)
	}
	if u.ws.Cur != 0 {
		t.Fatalf("a header opens no window: cur = %d", u.ws.Cur)
	}
	if got := rowNames(u.sideRowList()); !slices.Equal(got, []string{"=discord", "=telegram", "telegram-chat"}) {
		t.Fatalf("folded: %v", got)
	}
	if c := u.sideChatAt(sideHdr + 2); c == nil || c.Net != model.NetTelegram {
		t.Fatalf("mapping after the fold: %+v", c) // the hidden row is gone from the count
	}
	u.sideClick(sideHdr)
	if u.folded[model.NetDiscord] {
		t.Fatalf("second click: %v", u.folded)
	}
	// Wheel: 4 rows for 3 lines shown (24 -> 6 rows of screen).
	u.t.Rows = 6
	u.sideWheel(9)
	if u.sideScroll != 1 {
		t.Fatalf("wheel bound on the rows: %d", u.sideScroll)
	}
}

// TestSideRevealSection : sideReveal brings the ROW of the current chat into
// view, header lines counted; a chat hidden by a fold moves nothing, and a
// window with no chat still finds no line (it must not land on a header).
func TestSideRevealSection(t *testing.T) {
	u := netUI(model.NetTelegram, model.NetDiscord)
	u.th, u.side, u.sideW, u.folded = theme.Terminal(), sideChats, testSideW, map[string]bool{}
	u.t.Rows = 4 // sideRows() = 1: one row shown
	w := u.ws.New(false)
	w.Chat = u.chatList[0] // the telegram chat: last of the 4 rows
	u.ws.Cur = 1
	u.sideReveal()
	if u.sideScroll != 3 {
		t.Fatalf("row of the current chat: scroll %d, want 3", u.sideScroll)
	}
	u.folded[model.NetTelegram], u.sideScroll = true, 0
	u.sideReveal()
	if u.sideScroll != 0 {
		t.Fatalf("chat hidden by a fold: scroll %d", u.sideScroll)
	}
	w.Chat, u.sideScroll = nil, 0
	u.sideReveal()
	if u.sideScroll != 0 {
		t.Fatalf("window with no chat: scroll %d", u.sideScroll)
	}
}

// sideBlock is the sidebar as draw() builds it: that's where the /net
// filter and the badge are wired. Calling sidebarLines by hand would not
// prove that wiring.
// No fold state here: this test covers the /net filter alone, sections off.
func TestSideBlockNetFilter(t *testing.T) {
	u := netUI(model.NetTelegram, "discord")
	u.th, u.side, u.sideW = theme.Terminal(), sideChats, testSideW
	text := func() string {
		lines, sorted := u.sideBlock(-1)
		var b strings.Builder
		for _, l := range lines {
			b.WriteString(render.LineText(l) + "\n")
		}
		return fmt.Sprintf("%d|%s", len(sorted), b.String())
	}
	got := text()
	if !strings.Contains(got, "ᵗ telegram-chat") || !strings.Contains(got, "ᵈ discord-chat") || !strings.HasPrefix(got, "2|") {
		t.Fatalf("without filter: %q", got)
	}
	u.command("net", []string{"discord"}, "discord")
	if got = text(); !strings.Contains(got, "ᵈ discord-chat") || strings.Contains(got, "telegram-chat") || !strings.HasPrefix(got, "1|") {
		t.Fatalf("under discord filter: %q", got)
	}
}

// The unread badge is red (Error), not accent: it must catch the eye.
func TestSidebarUnreadRed(t *testing.T) {
	th := theme.Terminal()
	c := &model.Chat{ID: 1, Title: "chat", Unread: 3, LastDate: time.Now()}
	lines := sidebarLines(sideChats, []*model.Chat{c}, nil, nil, -1, th, testSideW, 1, 0, false, false, 0, -1, false, nil)
	for _, sp := range lines[0].Spans {
		if strings.TrimSpace(sp.Text) == "3" {
			if sp.Style.FG != th.Color(theme.Error) {
				t.Fatalf("unread badge: FG = %v, want Error %v", sp.Style.FG, th.Color(theme.Error))
			}
			return
		}
	}
	t.Fatalf("badge not found: %v", lines[0].Spans)
}

// The sidebar has a 2-line header: title + sort (clickable), then a ─ rule.
// Window 0 (status) gets the * prefix in window mode.
func TestSideHeader(t *testing.T) {
	th := theme.Terminal()
	hd := sideHeader(sideChats, "recent", th, testSideW, false)
	if len(hd) != 2 {
		t.Fatalf("header: %d lines", len(hd))
	}
	l0, l1 := render.LineText(hd[0]), render.LineText(hd[1])
	if !strings.Contains(l0, i18n.T("sidebar_title_chats")) || !strings.Contains(l0, "["+i18n.T("sort_recent")+"]") {
		t.Fatalf("title line: %q", l0)
	}
	if !strings.Contains(l1, "──") || !strings.HasSuffix(l1, "│") {
		t.Fatalf("rule line: %q", l1)
	}
	if w := render.LineText(hd[0]); !strings.HasSuffix(w, "│") {
		t.Fatalf("separator missing from the title: %q", w)
	}
}

func TestSideWindowZeroPrefix(t *testing.T) {
	th := theme.Terminal()
	ws := []*Window{{}, {Chat: &model.Chat{ID: 1, Kind: model.ChatGroup, Title: "g"}}}
	lines := sidebarLines(sideWindows, nil, ws, nil, 1, th, testSideW, 2, 0, false, false, 0, -1, false, nil)
	if got := render.LineText(lines[0]); !strings.HasPrefix(got, "*0:") {
		t.Fatalf("window 0: %q, prefix * expected", got)
	}
	if got := render.LineText(lines[1]); !strings.HasPrefix(got, "#1:") {
		t.Fatalf("window 1: %q", got)
	}
}

// Click mapping with the header: line 0 cycles the sort, the list starts at
// sideHdr.
func TestSideHeaderClick(t *testing.T) {
	c := &model.Chat{ID: 1, Kind: model.ChatUser, Title: "Alice"}
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{},
		cfg: &config.Config{SidebarSort: "recent"}, t: &term.Term{Cols: 80, Rows: 24},
		side: sideChats, sideW: 26, chatList: []*model.Chat{c},
		chats: map[model.ChatKey]*model.Chat{c.Key(): c}, aliases: map[model.ChatKey]string{}}
	if u.sideChatAt(0) != nil || u.sideChatAt(1) != nil {
		t.Fatal("the header carries no chat")
	}
	if u.sideChatAt(sideHdr) != c {
		t.Fatalf("first list line: %v", u.sideChatAt(sideHdr))
	}
	u.sideClick(0)
	if u.cfg.SidebarSort != "alpha" {
		t.Fatalf("header click: sort = %q, want alpha", u.cfg.SidebarSort)
	}
}

// TestSideHeaderWindowsSort : the sort rules both panel modes, so the windows
// header carries the same [sort:…] tag and its title line answers the click.
func TestSideHeaderWindowsSort(t *testing.T) {
	t.Setenv("TTYLOOM_DIR", t.TempDir()) // cycleSort saves the config
	l0 := render.LineText(sideHeader(sideWindows, "recent", theme.Terminal(), testSideW, false)[0])
	if !strings.Contains(l0, i18n.T("sidebar_title_windows")) || !strings.Contains(l0, "["+i18n.T("sort_recent")+"]") {
		t.Fatalf("windows title: %q", l0)
	}
	u := winSortUI() // side = sideWindows
	u.sideClick(0)
	if u.cfg.SidebarSort != "alpha" {
		t.Fatalf("header click in windows mode: sort = %q, want alpha", u.cfg.SidebarSort)
	}
}

// The windows list obeys the /net filter too: a window bound to a chat of
// another network goes away, one bound to no chat (window 0) stays, and the
// lines left keep pointing at their own window.
func TestSideWindowsHonoursNetFilter(t *testing.T) {
	u := netUI(model.NetTelegram, "discord")
	u.th, u.sideW, u.side = theme.Terminal(), testSideW, sideWindows
	for _, c := range u.chatList { // window 1: telegram-chat, window 2: discord-chat
		u.ws.New(true).Chat = c
	}
	u.command("net", []string{"discord"}, "discord")

	var rows []string
	lines, _ := u.sideBlock(-1)
	for _, l := range lines[sideHdr:] {
		if s := body(l); s != "" {
			rows = append(rows, s)
		}
	}
	if !slices.Equal(rows, []string{"*0: (none)", "@2: discord-chat"}) {
		t.Fatalf("filtered windows list: %v", rows)
	}
	if got := u.sideChatAt(sideHdr + 1); got != u.ws.List[2].Chat {
		t.Fatalf("second line: chat %v, want the one of window 2", got)
	}
}

// TestFoldsFile : the fold state survives a restart — a missing file is not an
// error, only the folded sections are written, and a file edited by hand (a
// key at false, a broken line) never folds anything by surprise.
func TestFoldsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sidebar.toml")
	got, err := loadFolds(path) // missing file: nothing folded, no error
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("missing file: %v, %v", got, err)
	}
	if err := saveFolds(path, map[string]bool{"discord:Gophers": true, model.NetTelegram: false}); err != nil {
		t.Fatal(err)
	}
	if got, err = loadFolds(path); err != nil || len(got) != 1 || !got["discord:Gophers"] {
		t.Fatalf("reread: %v, %v", got, err)
	}
	// Nothing folded any more: the file keeps no stale key.
	if err := saveFolds(path, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if got, err = loadFolds(path); err != nil || len(got) != 0 {
		t.Fatalf("after the unfold: %v, %v", got, err)
	}
	// Unreadable file: an empty state and an error, never a made-up fold.
	if err := os.WriteFile(path, []byte("[folded\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err = loadFolds(path); err == nil || got == nil || len(got) != 0 {
		t.Fatalf("broken file: %v, %v", got, err)
	}
}

// TestSideToggleSaves : the click that folds a section writes sidebar.toml at
// once, the unfold takes the key out of it again, and a write that fails gives
// one line instead of bringing the interface down.
func TestSideToggleSaves(t *testing.T) {
	t.Setenv("TTYLOOM_DIR", t.TempDir())
	u := netUI(model.NetTelegram, model.NetDiscord)
	u.th, u.side, u.sideW, u.folded = theme.Terminal(), sideChats, testSideW, map[string]bool{}
	u.sideClick(sideHdr) // header of the discord section, first line of the list
	got, err := loadFolds(sidebarPath())
	if err != nil || !got[model.NetDiscord] {
		t.Fatalf("after the fold: %v, %v", got, err)
	}
	u.sideClick(sideHdr)
	if got, err = loadFolds(sidebarPath()); err != nil || len(got) != 0 {
		t.Fatalf("after the unfold: %v, %v", got, err)
	}
	// Directory gone: the fold still happens on the screen, and the failed
	// write says so on one line.
	t.Setenv("TTYLOOM_DIR", filepath.Join(t.TempDir(), "gone"))
	u.sideClick(sideHdr)
	if !u.folded[model.NetDiscord] {
		t.Fatalf("fold dropped by the write failure: %v", u.folded)
	}
	if s := lastSys(u.view()); !strings.Contains(s, "sidebar.toml") {
		t.Fatalf("no line for the write failure: %q", s)
	}
}

// TestFoldsStaleMono : a sidebar.toml left over from a multi-network setup
// folds nothing in mono-Telegram — one section is not a section, so the keys
// read from the file light up no header.
func TestFoldsStaleMono(t *testing.T) {
	u := netUI(model.NetTelegram)
	u.th, u.side, u.sideW = theme.Terminal(), sideChats, testSideW
	u.folded = map[string]bool{"discord:Gophers": true, model.NetTelegram: true}
	if rows := u.sideRowList(); len(rows) != 1 || rows[0].chat == nil {
		t.Fatalf("stale keys: %v", rowNames(rows))
	}
}

// winSortUI : windows mode with window 0, three windows bound to chats whose
// titles, dates and unread counts give a different order in each sort mode,
// and one window bound to nothing.
func winSortUI() *UI {
	now := time.Now()
	c1 := &model.Chat{Net: model.NetTelegram, ID: 1, Title: "Charlie", LastDate: now}
	c2 := &model.Chat{Net: model.NetTelegram, ID: 2, Title: "alice", LastDate: now.Add(-2 * time.Hour), Unread: 1}
	c3 := &model.Chat{Net: model.NetTelegram, ID: 3, Title: "Bob", LastDate: now.Add(-time.Hour), Unread: 5}
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{},
		cfg: &config.Config{SidebarSort: "recent"}, t: &term.Term{Cols: 80, Rows: 24},
		th: theme.Terminal(), sideW: testSideW, side: sideWindows,
		chatList: []*model.Chat{c1, c2, c3}, aliases: map[model.ChatKey]string{},
		chats: map[model.ChatKey]*model.Chat{}}
	for _, c := range u.chatList {
		u.ws.New(true).Chat = c // windows 1, 2, 3
	}
	u.ws.New(true) // window 4: bound to nothing
	return u
}

// TestSideWinsFollowsSort : the windows mode orders its lines with the sort of
// the chat mode (F7). Window 0 stays at the head, the windows bound to
// nothing go to the end in index order.
func TestSideWinsFollowsSort(t *testing.T) {
	u := winSortUI()
	for _, tc := range []struct {
		sort string
		want []int
	}{
		{"recent", []int{0, 1, 3, 2, 4}}, // Charlie, Bob, alice
		{"alpha", []int{0, 2, 3, 1, 4}},  // alice, Bob, Charlie
		{"unread", []int{0, 3, 2, 1, 4}}, // 5, 1, 0
	} {
		u.cfg.SidebarSort = tc.sort
		if got := u.sideWins(); !slices.Equal(got, tc.want) {
			t.Fatalf("sort %q: %v, want %v", tc.sort, got, tc.want)
		}
	}
}

// TestSideWinsF7Live : F7 while the windows mode is shown changes the order at
// once — the cycle is not gated on the chat mode.
func TestSideWinsF7Live(t *testing.T) {
	u := winSortUI()
	before := u.sideWins()
	u.cycleSort() // recent -> alpha
	if u.cfg.SidebarSort != "alpha" {
		t.Fatalf("F7 in windows mode: sort = %q", u.cfg.SidebarSort)
	}
	if got := u.sideWins(); slices.Equal(got, before) {
		t.Fatalf("order unchanged after F7: %v", got)
	}
}

// TestSideWinsClickMapping : the click opens the window drawn on that line,
// sorted order included — the drawn "N:" is the real window index.
func TestSideWinsClickMapping(t *testing.T) {
	t.Setenv("TTYLOOM_DIR", t.TempDir()) // cycleSort saves the config
	for _, sort := range []string{"recent", "alpha", "unread"} {
		u := winSortUI()
		u.cfg.SidebarSort = sort
		lines, _ := u.sideBlock(-1)
		row := body(lines[sideHdr+1]) // second drawn line of the list
		u.sideClick(sideHdr + 1)
		if want := fmt.Sprintf("%d:", u.ws.Cur); !strings.Contains(row, want) {
			t.Fatalf("sort %q: line %q opened window %d", sort, row, u.ws.Cur)
		}
	}
}

// TestStepIdx : rank moved by one, bounded — never a wrap at the ends; with
// nothing current (-1) the wheel enters by the end it comes from.
func TestStepIdx(t *testing.T) {
	for _, c := range [][4]int{{-1, 1, 3, 0}, {-1, -1, 3, 2}, {0, -1, 3, 0}, {2, 1, 3, 2}, {1, 1, 3, 2}, {0, 1, 0, -1}, {0, 1, 1, 0}} {
		if got := stepIdx(c[0], c[1], c[2]); got != c[3] {
			t.Fatalf("stepIdx(%d, %d, %d) = %d, want %d", c[0], c[1], c[2], got, c[3])
		}
	}
}

// wheelUI : two networks, three chats, and a window already open for two of
// them — the frame the wheel of the panel walks. The backends are fakes that
// count their calls: a notch that would open a chat is caught by the count.
func wheelUI() *UI {
	now := time.Now()
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, th: theme.Terminal(),
		cfg: &config.Config{SidebarSort: "recent", Hover: config.HoverMenu},
		t:   &term.Term{Cols: 80, Rows: 24}, side: sideChats, sideW: testSideW,
		nets:    map[string]model.Backend{model.NetDiscord: &fakeBackend{}, model.NetTelegram: &fakeBackend{}},
		chats:   map[model.ChatKey]*model.Chat{},
		aliases: map[model.ChatKey]string{}, folded: map[string]bool{}, gotContacts: true}
	for _, c := range []*model.Chat{
		{Net: model.NetDiscord, ID: 1, Kind: model.ChatUser, Title: "discord-win", LastDate: now},
		{Net: model.NetTelegram, ID: 2, Kind: model.ChatUser, Title: "telegram-none", LastDate: now.Add(-time.Hour)},
		{Net: model.NetTelegram, ID: 3, Kind: model.ChatUser, Title: "telegram-win", LastDate: now.Add(-2 * time.Hour)},
	} {
		u.chatList = append(u.chatList, c)
		u.chats[c.Key()] = c
		if c.Title == "telegram-none" {
			continue // never opened: the wheel walks past its row
		}
		w := u.ws.New(true)        // hidden: window 0, bound to nothing, stays the current one
		w.Chat, w.Loaded = c, true // loaded, as an open window is: goTo asks for no page
	}
	return u
}

// wheelOn sends a wheel notch over the panel, through the whole routing of
// mouse(): 64 = up, 65 = down.
func wheelOn(u *UI, button int) {
	u.mouse(term.MouseEvent{X: 2, Y: sideHdr, Button: button, Press: true})
}

// TestSideWheelChangesChat : the wheel over the panel walks the chats that
// already have a window — a header and a chat never opened are not steps, the
// ends clamp, and not one notch opens a window or calls the network.
func TestSideWheelChangesChat(t *testing.T) {
	u := wheelUI()
	want := []string{"=discord", "discord-win", "=telegram", "telegram-none", "telegram-win"}
	if got := rowNames(u.sideRowList()); !slices.Equal(got, want) {
		t.Fatalf("rows: %v", got)
	}
	wheelOn(u, 65)
	if u.ws.Cur != 1 { // window 0 carries no chat: the wheel down enters by the top
		t.Fatalf("first notch: window %d", u.ws.Cur)
	}
	wheelOn(u, 65)
	if u.ws.Cur != 2 { // the telegram header and telegram-none are walked past
		t.Fatalf("second notch: window %d", u.ws.Cur)
	}
	wheelOn(u, 65)
	if u.ws.Cur != 2 { // last step: it clamps, it never wraps
		t.Fatalf("clamp at the bottom: window %d", u.ws.Cur)
	}
	wheelOn(u, 64)
	if u.ws.Cur != 1 {
		t.Fatalf("wheel up: window %d", u.ws.Cur)
	}
	wheelOn(u, 64)
	if u.ws.Cur != 1 { // first step: clamp again
		t.Fatalf("clamp at the top: window %d", u.ws.Cur)
	}
	if len(u.ws.List) != 3 {
		t.Fatalf("%d windows: a notch opened one", len(u.ws.List))
	}
	for net, b := range u.nets {
		if f := b.(*fakeBackend); f.history != 0 {
			t.Fatalf("%s: %d history calls, a notch must cost no network", net, f.history)
		}
	}
	// The section of the current chat gets folded: its row is gone, so the
	// wheel enters the list again by the end it comes from.
	u.folded[model.NetDiscord] = true
	wheelOn(u, 65)
	if u.ws.Cur != 2 {
		t.Fatalf("current chat folded away: window %d", u.ws.Cur)
	}
	delete(u.folded, model.NetDiscord)
	u.ws.Cur = 0 // nothing current again: upwards it enters by the last step
	wheelOn(u, 64)
	if u.ws.Cur != 2 {
		t.Fatalf("nothing current, wheel up: window %d", u.ws.Cur)
	}
	// A window pre-opened by auto_open_days is hidden and Loaded false: it is
	// a step like the others, and landing on it loads its first page, exactly
	// as a click or Alt+N would. The notches above walked loaded windows only,
	// which is why the counters were still at zero.
	auto := &model.Chat{Net: model.NetTelegram, ID: 4, Kind: model.ChatUser,
		Title: "telegram-auto", LastDate: time.Now().Add(-3 * time.Hour)}
	u.chatList = append(u.chatList, auto)
	u.chats[auto.Key()] = auto
	u.ws.New(true).Chat = auto // Loaded stays false: never visited
	tg := u.nets[model.NetTelegram].(*fakeBackend)
	wheelOn(u, 65) // from telegram-win, the step just above it
	if u.ws.Cur != 3 || tg.history != 1 {
		t.Fatalf("landing on a window never visited: window %d, %d history calls", u.ws.Cur, tg.history)
	}
}

// TestSideWheelWindowsMode : in windows mode the wheel goes from drawn window
// to drawn window, with the same clamp.
func TestSideWheelWindowsMode(t *testing.T) {
	u := wheelUI()
	u.side = sideWindows
	if got := u.sideWins(); !slices.Equal(got, []int{0, 1, 2}) {
		t.Fatalf("windows drawn: %v", got)
	}
	wheelOn(u, 65)
	if u.ws.Cur != 1 {
		t.Fatalf("wheel down: window %d", u.ws.Cur)
	}
	wheelOn(u, 65)
	if u.ws.Cur != 2 {
		t.Fatalf("second notch: window %d", u.ws.Cur)
	}
	wheelOn(u, 65)
	if u.ws.Cur != 2 { // last window: clamp
		t.Fatalf("clamp: window %d", u.ws.Cur)
	}
	wheelOn(u, 64)
	if u.ws.Cur != 1 {
		t.Fatalf("wheel up: window %d", u.ws.Cur)
	}
}

// TestSideWheelHoverOffScrollsList : hover off, the wheel over the panel
// scrolls the list one row a notch (term folds the bursts of a terminal with
// a scroll multiplier) and changes no window.
func TestSideWheelHoverOffScrollsList(t *testing.T) {
	u := wheelUI()
	u.cfg.Hover, u.t.Rows = config.HoverOff, 6 // 3 lines shown for 5 rows
	wheelOn(u, 65)
	if u.sideScroll != 1 || u.ws.Cur != 0 {
		t.Fatalf("hover off: scroll %d, window %d", u.sideScroll, u.ws.Cur)
	}
}

// TestSideWindowsActRed : in window mode the activity counter " (N)" is drawn
// like the unread badge of the chat mode — red, bold — and not in the plain
// style of the name. The current line keeps its single reversed style.
func TestSideWindowsActRed(t *testing.T) {
	th := theme.Terminal()
	ws := []*Window{{}, {Chat: &model.Chat{ID: 1, Kind: model.ChatUser, Title: "ancien"}, Act: 2}}
	lines := sidebarLines(sideWindows, nil, ws, nil, 0, th, testSideW, 2, 0, false, false, 0, -1, false, nil)
	red := theme.Style{FG: th.Color(theme.Error), Bold: true}
	var act *render.Span
	for i := range content(lines[1]) {
		if strings.TrimSpace(content(lines[1])[i].Text) == "(2)" {
			act = &content(lines[1])[i]
		}
	}
	if act == nil || act.Style != red {
		t.Fatalf("activity span: %+v", content(lines[1]))
	}
	if got := body(lines[1]); got != "@1: ancien (2)" {
		t.Fatalf("window 1: %q", got)
	}
	if !oneBar(lines[0]) {
		t.Fatalf("current line patchy: %+v", content(lines[0]))
	}
}
