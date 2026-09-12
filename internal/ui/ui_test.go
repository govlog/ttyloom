package ui

import (
	"context"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/cache"
	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

func TestEditor(t *testing.T) {
	var e Editor
	e.Insert("hello world")
	e.KillWord()
	if e.String() != "hello " {
		t.Fatalf("killword: %q", e.String())
	}
	e.Backspace()
	e.Home()
	e.Insert(">")
	if e.String() != ">hello" || e.Cursor() != 1 {
		t.Fatalf("home+insert: %q %d", e.String(), e.Cursor())
	}
	if got := e.Submit(); got != ">hello" || e.String() != "" {
		t.Fatalf("submit: %q", got)
	}
	e.Insert("x")
	e.Up()
	if e.String() != ">hello" {
		t.Fatalf("history up: %q", e.String())
	}
	e.Down()
	if e.String() != "x" {
		t.Fatalf("history down: %q", e.String())
	}
	e.Set("abc def")
	e.Left()
	e.Left()
	e.Left()
	e.KillLine()
	if e.String() != "def" || e.Cursor() != 0 {
		t.Fatalf("killline: %q %d", e.String(), e.Cursor())
	}
}

func TestEditorComplete(t *testing.T) {
	cands := func(word string, atStart bool) []string {
		if atStart {
			return []string{"/query", "/quit", "/join"}
		}
		return []string{"alice", "alfred", "bob"}
	}
	var e Editor
	e.Insert("/j")
	e.Complete(cands)
	if e.String() != "/join " {
		t.Fatalf("unique: %q", e.String())
	}
	e.Insert("al")
	e.Complete(cands)
	if e.String() != "/join al" { // common prefix "al": nothing to add
		t.Fatalf("ambiguous: %q", e.String())
	}
	e.Insert("i")
	e.Complete(cands)
	if e.String() != "/join alice " {
		t.Fatalf("resolved: %q", e.String())
	}
}

func TestParseCommand(t *testing.T) {
	name, args, text, ok := ParseCommand("/win new hide")
	if !ok || name != "window" || !reflect.DeepEqual(args, []string{"new", "hide"}) || text != "new hide" {
		t.Fatalf("%q %q %q %v", name, args, text, ok)
	}
	if _, _, text, ok := ParseCommand("//slash"); ok || text != "/slash" {
		t.Fatalf("escaped: %q %v", text, ok)
	}
	if _, _, text, ok := ParseCommand("plain"); ok || text != "plain" {
		t.Fatalf("plain: %q %v", text, ok)
	}
	if name, _, _, _ := ParseCommand("/Q bob"); name != "query" {
		t.Fatalf("alias: %q", name)
	}
}

func TestMeEntity(t *testing.T) {
	text, ents := meMsg("alice", "part 😀")
	if text != "* alice part 😀" {
		t.Fatalf("text: %q", text)
	}
	// Offsets in runes: the astral 😀 counts for 1 → 14 runes, not 15.
	want := []model.Span{{Start: 0, End: 14, Kind: model.SpanItalic}}
	if !reflect.DeepEqual(ents, want) {
		t.Fatalf("entities: %+v", ents)
	}
}

func TestWindowsFlow(t *testing.T) {
	ws := NewWindows()
	ws.New(true) // /window new hide
	if ws.Cur != 0 || len(ws.List) != 2 {
		t.Fatalf("hide: cur=%d n=%d", ws.Cur, len(ws.List))
	}
	ws.Next() // Ctrl+X
	if ws.Cur != 1 {
		t.Fatalf("next: %d", ws.Cur)
	}
	antonio := &model.Chat{ID: 5, Title: "Antonio"}
	ws.Current().Chat = antonio
	ws.Next()
	if ws.Cur != 0 {
		t.Fatalf("cycle: %d", ws.Cur)
	}
	if ws.ForChat(antonio.Key()) != 1 || ws.ByName("ant", nil) != 1 || ws.ForChat(model.ChatKey{ID: 9}) != -1 {
		t.Fatal("lookup")
	}
	if ws.Close() != nil {
		t.Fatal("window 0 must not close")
	}
	ws.Switch(1)
	if ws.Close() == nil || len(ws.List) != 1 || ws.Cur != 0 {
		t.Fatal("close")
	}
}

func TestSentDedup(t *testing.T) {
	// Server copy before the receipt: the pending item is dropped, no duplicate.
	w := &Window{}
	w.Upsert(&model.Msg{TmpID: 1, Out: true, Text: "coucou", Pending: true})
	w.Upsert(&model.Msg{ID: 42, Out: true, Text: "coucou"})
	if !w.Sent(1, 42, "") {
		t.Fatal("pending item not found")
	}
	if len(w.Items) != 1 || w.Items[0].Msg.ID != 42 || w.Items[0].Msg.TmpID != 0 {
		t.Fatalf("dedup: %d items, %+v", len(w.Items), w.Items[0].Msg)
	}

	// Reverse order: the receipt stamps the item, the server copy replaces it.
	w = &Window{}
	w.Upsert(&model.Msg{TmpID: 1, Out: true, Text: "coucou", Pending: true})
	if !w.Sent(1, 42, "") {
		t.Fatal("stamp")
	}
	if w.Items[0].Msg.ID != 42 || w.Items[0].Msg.Pending {
		t.Fatalf("stamp: %+v", w.Items[0].Msg)
	}
	w.Upsert(&model.Msg{ID: 42, Out: true, Text: "coucou"})
	if len(w.Items) != 1 {
		t.Fatalf("replacement: %d items", len(w.Items))
	}
	if w.Sent(1, 42, "") {
		t.Fatal("no more pending item expected")
	}
}

func TestWindowUpsertAndSeparators(t *testing.T) {
	w := &Window{}
	d1 := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	d2 := d1.Add(24 * time.Hour)
	w.Upsert(&model.Msg{ID: 1, Date: d1, From: "a", Text: "x"})
	w.Upsert(&model.Msg{ID: 2, Date: d2, From: "a", Text: "y"})
	if !w.Upsert(&model.Msg{ID: 3, Date: d2, From: "a", Text: "z"}) || w.Upsert(&model.Msg{ID: 2, Date: d2, From: "a", Text: "y2"}) {
		t.Fatal("upsert return values")
	}
	if len(w.Items) != 3 || w.Items[1].Msg.Text != "y2" {
		t.Fatalf("items: %d", len(w.Items))
	}
	o := render.Opts{Width: 40, Theme: theme.Terminal(), Images: "off"}
	lines := w.Lines(o)
	// 2 day separators + 3 messages
	if len(lines) != 5 {
		t.Fatalf("lines: %d", len(lines))
	}
	if w.OldestID() != 1 || w.LastID() != 3 {
		t.Fatal("ids")
	}
}

func TestMentionsMe(t *testing.T) {
	m := &model.Msg{Text: "salut @Bob, ça va ?"}
	if !mentionsMe(m, 1, "bob") {
		t.Fatal("@bob case-insensitive not detected")
	}
	m2 := &model.Msg{Text: "salut @bobby"}
	if mentionsMe(m2, 1, "bob") {
		t.Fatal("false positive on @bobby")
	}
	m3 := &model.Msg{Text: "hello", Entities: []model.Span{{End: 5, Kind: model.SpanMention, UserID: 42}}}
	if !mentionsMe(m3, 42, "") {
		t.Fatal("mention by id not detected")
	}
	if mentionsMe(m3, 99, "") {
		t.Fatal("mention by id: false positive on another UserID")
	}
	// Mention with no id (@username, hashtag): never a mention of me.
	m4 := &model.Msg{Text: "#tag", Entities: []model.Span{{End: 4, Kind: model.SpanMention}}}
	if mentionsMe(m4, 0, "") {
		t.Fatal("mention with no id: false positive")
	}
}

func TestAutoOpen(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	old := &model.Chat{ID: 1, LastDate: now.AddDate(0, 0, -30)}
	recent := &model.Chat{ID: 2, LastDate: now.AddDate(0, 0, -1)}
	recent2 := &model.Chat{ID: 3, LastDate: now.AddDate(0, 0, -5)}
	got := autoOpenChats([]*model.Chat{old, recent2, recent}, now, 7)
	if len(got) != 2 || got[0] != recent || got[1] != recent2 {
		t.Fatalf("filter+sort: %+v", got)
	}
	if got := autoOpenChats([]*model.Chat{recent}, now, 0); got != nil {
		t.Fatalf("0 = disabled: %v", got)
	}
}

func TestAggregateView(t *testing.T) {
	c := &model.Chat{ID: 7, Title: "alice"}
	u := &UI{ws: NewWindows(), agg: &Window{}, chats: map[model.ChatKey]*model.Chat{c.Key(): c}}
	if u.view() != u.ws.List[0] {
		t.Fatal("normal mode: window 0 is the view")
	}
	u.aggregate = true
	if u.view() != u.agg || u.aggTarget() != nil || u.sendWin() != u.agg {
		t.Fatal("empty aggregate: the view is the aggregate, with no target")
	}
	w := u.ws.New(true)
	w.Chat = c
	u.agg.Upsert(&model.Msg{ID: 1, ChatID: 7})
	if u.aggTarget() != c || u.sendWin() != w {
		t.Fatalf("target = chat of the last message: %v %v", u.aggTarget(), u.sendWin())
	}
	u.ws.Cur = 1
	if u.view() != w || u.sendWin() != w {
		t.Fatal("outside window 0: never the aggregate")
	}
}

// TestDialogsNetCollision: two networks can give the same chat id. Both
// dialogs must stay two distinct entries.
func TestDialogsNetCollision(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{},
		chats: map[model.ChatKey]*model.Chat{}, dialogsSeen: map[string]bool{}}
	for _, net := range []string{"telegram", "discord"} {
		u.dispatch(model.Envelope{Net: net, Ev: model.EvDialogs{Chats: []*model.Chat{{ID: 42, Title: net}}}})
	}
	if len(u.chats) != 2 {
		t.Fatalf("cross-network collision: %d entries in u.chats", len(u.chats))
	}
	if len(u.chatList) != 2 { // mergeDialogs dedupes by chat, not by bare id
		t.Fatalf("sidebar: %d entries, want 2", len(u.chatList))
	}
	for _, net := range []string{"telegram", "discord"} {
		if c := u.chats[model.ChatKey{Net: net, ID: 42}]; c == nil || c.Title != net {
			t.Fatalf("chat %s missing or overwritten: %+v", net, c)
		}
	}
}

// A network whose dialog list is the whole account (Complete) loses the chats
// missing from it: a Discord server one leaves must go from the sidebar, live
// and at the next start — mergeDialogs alone would keep it forever. Telegram
// pages its dialogs, a chat missing from a list means nothing there, and
// nothing is dropped.
func TestDialogsCompletePrunes(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{},
		chats: map[model.ChatKey]*model.Chat{}, dialogsSeen: map[string]bool{},
		nets: map[string]model.Backend{model.NetTelegram: &fakeBackend{}, model.NetDiscord: &fakeBackend{}}}
	dialogs := func(net string, complete bool, ids ...int64) {
		chats := make([]*model.Chat, 0, len(ids))
		for _, id := range ids {
			chats = append(chats, &model.Chat{ID: id, Title: net})
		}
		u.dispatch(model.Envelope{Net: net, Ev: model.EvDialogs{Chats: chats, Complete: complete}})
	}
	pruned := func() int {
		n := 0
		for _, w := range u.ws.List {
			for _, it := range w.Items {
				if it.Sys == i18n.T("chats_pruned", 1) {
					n++
				}
			}
		}
		return n
	}
	dialogs(model.NetTelegram, false, 1, 2)
	dialogs(model.NetDiscord, false, 1, 2)
	gone := model.ChatKey{Net: model.NetDiscord, ID: 2}
	u.bindChat(u.ws.New(false), u.chats[gone]) // a window open on the chat about to go
	windows := len(u.ws.List)

	dialogs(model.NetDiscord, true, 1) // the second discord chat has left the account

	if u.chats[gone] != nil {
		t.Fatal("chat gone still in u.chats")
	}
	if i := slices.IndexFunc(u.chatList, func(c *model.Chat) bool { return c.Key() == gone }); i >= 0 {
		t.Fatalf("chat gone still in the sidebar at %d", i)
	}
	if len(u.ws.List) != windows-1 {
		t.Fatalf("%d windows, want %d: the one bound to the chat gone stays open", len(u.ws.List), windows-1)
	}
	if n := pruned(); n != 1 {
		t.Fatalf("%d chats_pruned lines, want exactly 1", n)
	}
	for _, id := range []int64{1, 2} {
		if u.chats[model.ChatKey{Net: model.NetTelegram, ID: id}] == nil {
			t.Fatalf("telegram chat %d dropped by a discord list", id)
		}
	}

	// Telegram: an incomplete list drops nothing, whatever is missing from it.
	dialogs(model.NetTelegram, false, 1)
	if u.chats[model.ChatKey{Net: model.NetTelegram, ID: 2}] == nil {
		t.Fatal("telegram chat dropped by a partial list")
	}
	if n := pruned(); n != 1 {
		t.Fatalf("%d chats_pruned lines after the telegram list, want still 1", n)
	}
}

func TestDebugRouting(t *testing.T) {
	// isDebugLog : the rule is the level. WARN/DEBUG/INFO debug-only; ERROR stays
	// shown in window 0 (direct result of a user action).
	if !isDebugLog(model.EvLog{Level: "WARN", Msg: "x"}) {
		t.Fatal("EvLog WARN must be classified debug-only")
	}
	if isDebugLog(model.EvLog{Level: "ERROR", Msg: "x"}) {
		t.Fatal("EvLog ERROR is not debug-only: also visible in window 0")
	}
	if isDebugLog(model.EvReady{}) || isDebugLog(model.EvNewMessage{}) {
		t.Fatal("an event other than EvLog is never debug")
	}

	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{}, chats: map[model.ChatKey]*model.Chat{}}
	u.ws.New(false) // window 1 current: Act goes up only with status0 (ERROR)

	u.event(model.EvLog{Level: "WARN", Msg: "peer inconnu : flood wait"})
	if len(u.debug.Items) != 1 || u.debug.Items[0].Sys != "WARN peer inconnu : flood wait" {
		t.Fatalf("EvLog WARN missing from the debug log: %+v", u.debug.Items)
	}
	if len(u.ws.List[0].Items) != 0 {
		t.Fatal("EvLog WARN must never land in window 0")
	}
	if u.ws.List[0].Act != 0 {
		t.Fatal("EvLog WARN does not raise Act")
	}

	u.event(model.EvLog{Level: "ERROR", Msg: "suppression : boom"})
	if len(u.debug.Items) != 2 || u.debug.Items[1].Sys != "ERROR suppression : boom" {
		t.Fatalf("EvLog ERROR missing from the debug log: %+v", u.debug.Items)
	}
	if len(u.ws.List[0].Items) != 1 || u.ws.List[0].Items[0].Sys != "ERROR suppression : boom" {
		t.Fatalf("EvLog ERROR must stay visible in window 0, as before: %+v", u.ws.List[0].Items)
	}
	if u.ws.List[0].Act != 1 {
		t.Fatalf("EvLog ERROR must raise Act as before: %d", u.ws.List[0].Act)
	}

	// /debug (setDebug): it wins over the aggregate in window 0.
	u.ws.Cur = 0
	u.aggregate = true
	u.setDebug(true)
	if u.view() != u.debug {
		t.Fatal("showDebug takes priority over the aggregate in window 0")
	}
	u.setDebug(false)
	if u.view() != u.agg {
		t.Fatal("/debug again: back to the previous view (aggregate)")
	}

	// Alt+A (setAggregate) closes the log again.
	u.showDebug = true
	u.setAggregate(false)
	if u.showDebug {
		t.Fatal("Alt+A must close the debug log")
	}

	// goTo (window change) closes the log again.
	u.showDebug = true
	u.goTo(1)
	if u.showDebug {
		t.Fatal("goTo must close the debug log")
	}
}

func TestAggregatePerChatIDs(t *testing.T) {
	// The Telegram ids are per chat: the same id in two chats gives two
	// messages, never an overwrite.
	agg := &Window{}
	agg.Upsert(&model.Msg{ID: 42, ChatID: 1, Text: "un"})
	agg.Upsert(&model.Msg{ID: 42, ChatID: 2, Text: "deux"})
	if len(agg.Items) != 2 || agg.Items[0].Msg.Text != "un" || agg.Items[1].Msg.Text != "deux" {
		t.Fatalf("overwrite between chats: %d items", len(agg.Items))
	}
	// Send receipt in chat 2: the id 42 of chat 1 is not a duplicate.
	w := &Window{}
	w.Upsert(&model.Msg{ID: 42, ChatID: 1})
	w.Upsert(&model.Msg{TmpID: 9, ChatID: 2, Pending: true})
	if !w.Sent(9, 42, "") {
		t.Fatal("send receipt not applied")
	}
	if len(w.Items) != 2 || w.Items[1].Msg.ID != 42 || w.Items[1].Msg.Pending {
		t.Fatalf("pending send dropped as a duplicate: %d items", len(w.Items))
	}
	// Real duplicate (same chat): the pending item goes away.
	w.Upsert(&model.Msg{TmpID: 10, ChatID: 1, Pending: true})
	if !w.Sent(10, 42, "") || len(w.Items) != 2 {
		t.Fatalf("duplicate of the same chat not deduplicated: %d items", len(w.Items))
	}
}

func TestFocusDeferredRead(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}}
	w := u.ws.Current()
	w.Act = 3
	u.key(term.Key{Code: term.FocusOut})
	u.markRead(w)
	if u.focused || w.Act != 3 {
		t.Fatalf("absent: nothing is read (focused=%v act=%d)", u.focused, w.Act)
	}
	u.key(term.Key{Code: term.FocusIn})
	if !u.focused || w.Act != 0 {
		t.Fatalf("return: the current window is read (focused=%v act=%d)", u.focused, w.Act)
	}
}

// scrollTo puts the index idx (of total lines) on the 3rd line (rank 2) of the
// view, unless it is already in the last view lines (0, nothing to do) or too
// close to the start to leave 2 lines above (clamped at the top).
func TestScrollTo(t *testing.T) {
	if got := scrollTo(100, 40, 20); got != 42 {
		t.Fatalf("scrollTo = %d, want 42", got)
	}
	if got := scrollTo(100, 85, 20); got != 0 {
		t.Fatalf("already visible: %d, want 0", got)
	}
	if got := scrollTo(100, 1, 20); got != 80 {
		t.Fatalf("clamp top: %d, want 80", got)
	}
}

func TestLogLine(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC)
	m := &model.Msg{From: "bob", Text: "salut"}
	if got, want := logLine(m, now), "2026-08-30 12:01 <bob> salut"; got != want {
		t.Fatalf("text: %q, want %q", got, want)
	}
	media := &model.Msg{From: "bob", Media: &model.Media{Label: "[photo 640x480 · 42 Ko]"}}
	if got, want := logLine(media, now), "2026-08-30 12:01 <bob> [photo 640x480 · 42 Ko]"; got != want {
		t.Fatalf("media: %q, want %q", got, want)
	}
	svc := &model.Msg{Service: "alice a rejoint"}
	if got, want := logLine(svc, now), "*** alice a rejoint"; got != want {
		t.Fatalf("service: %q, want %q", got, want)
	}
	ctrl := &model.Msg{From: "bob", Text: "ligne1\nligne2\x07fin"}
	if got, want := logLine(ctrl, now), "2026-08-30 12:01 <bob> ligne1 ligne2 fin"; got != want {
		t.Fatalf("control: %q, want %q", got, want)
	}
}

// TestLogName : the journal file is keyed on the chat, not on the title alone.
// Two long titles that only differ past the readable cut must not share one
// file, and whatever the title the name stays a plain ASCII file name (the cut
// falls on a rune boundary, never inside a multi-byte character).
func TestLogName(t *testing.T) {
	long := strings.Repeat("a", 70) // longer than any readable cut
	a := &Window{Chat: &model.Chat{Net: model.NetTelegram, ID: 1, Title: long + "alpha"}}
	b := &Window{Chat: &model.Chat{Net: model.NetTelegram, ID: 2, Title: long + "beta"}}
	if logName(a) == logName(b) {
		t.Fatalf("two chats in one journal: %q", logName(a))
	}
	accented := &Window{Chat: &model.Chat{Net: model.NetTelegram, ID: 3, Title: strings.Repeat("é", 60)}}
	if got := logName(accented); !regexp.MustCompile(`^[A-Za-z0-9_-]+\.log$`).MatchString(got) {
		t.Fatalf("not a plain file name: %q", got)
	}
	if got := logName(&Window{Chat: &model.Chat{Net: model.NetDiscord, ID: 7, Title: "Salon des amis"}}); got != "Salon_des_amis-discord-7.log" {
		t.Fatalf("short title: %q", got)
	}
}

func TestKittyLRU(t *testing.T) {
	a, b, c := &model.Media{}, &model.Media{}, &model.Media{}
	l := lru{cap: 2}
	if out := l.touch(a); out != nil {
		t.Fatalf("premature eviction: %v", out)
	}
	l.touch(b)
	l.touch(a) // promotion: a becomes the newest again
	out := l.touch(c)
	if len(out) != 1 || out[0] != b {
		t.Fatalf("eviction: %v, want [b]", out)
	}
	if !reflect.DeepEqual(l.list, []*model.Media{a, c}) {
		t.Fatalf("order: %v", l.list)
	}
	l.drop(a)
	if !reflect.DeepEqual(l.list, []*model.Media{c}) {
		t.Fatalf("drop: %v", l.list)
	}
}

func TestKeyEndOfLine(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}}
	u.ed.Insert("hello")
	u.ed.Home()
	u.key(term.Key{Code: term.Ctrl, Rune: 'e'})
	if u.ed.Cursor() != len("hello") {
		t.Fatalf("Ctrl+E must place the cursor at the end of the line: %d", u.ed.Cursor())
	}
}

func TestImagesCycle(t *testing.T) {
	cases := []struct {
		cur   string
		kitty bool
		want  string
	}{
		{"kitty", true, "halfblock"},
		{"halfblock", true, "off"},
		{"off", true, "kitty"},
		{"halfblock", false, "off"},
		{"off", false, "halfblock"},
	}
	for _, c := range cases {
		if got := nextImages(c.cur, c.kitty); got != c.want {
			t.Errorf("nextImages(%q, %v) = %q, want %q", c.cur, c.kitty, got, c.want)
		}
	}
}

// Last net before xdg-open: our paths go through, a scheme that is not
// http(s) is refused. The URLs from the network are already filtered by render.SafeURL.
func TestOpenable(t *testing.T) {
	ok := []string{"/home/me/Downloads/x.jpg", "/home/me/100%.png", "https://exemple.fr/a?b=1", "http://exemple.fr", "mailto:a@b.fr"}
	ko := []string{"file:///etc/passwd", "javascript:alert(1)", "ssh://exemple.fr", "data:text/html,<script>",
		"https://exemple.fr/\x1b]0;x\a"}
	for _, s := range ok {
		if !openable(s) {
			t.Errorf("wrongly refused: %q", s)
		}
	}
	for _, s := range ko {
		if openable(s) {
			t.Errorf("wrongly accepted: %q", s)
		}
	}
}

// Shift+Enter inserts a line break, the input line shows it as ⏎ and Enter
// sends the whole thing; a command, though, stays on one line.
func TestMultilineInsert(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}}
	u.ed.Set("a")
	u.key(term.Key{Code: term.Enter, Shift: true})
	u.key(term.Key{Rune: 'b'})
	u.key(term.Key{Code: term.Enter, Alt: true})
	if u.ed.String() != "a\nb\n" || u.ed.Cursor() != 4 {
		t.Fatalf("line break: %q cursor %d", u.ed.String(), u.ed.Cursor())
	}
	var b strings.Builder
	u.drawInput(&b, 1, 0, 40)
	if !strings.Contains(b.String(), "a⏎b⏎") {
		t.Fatalf("rendered: %q", b.String())
	}
	u.ed.Set("/quit\nx")
	u.submit()
	if u.ed.String() != "/quit\nx" || len(u.view().Items) == 0 {
		t.Fatalf("multiline command: not refused (input %q, %d lines)", u.ed.String(), len(u.view().Items))
	}
}

// TestOpenRefusesBin : a file saved with a neutralised extension (.bin, the
// type is not on the allow list of tgc.extOf) is never handed to xdg-open.
func TestOpenRefusesBin(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}}
	u.open("/home/me/Downloads/ttyloom/20260830-120000_chat_12.bin")
	items := u.view().Items
	if len(items) != 1 || !strings.Contains(items[0].Sys, "20260830-120000_chat_12.bin") {
		t.Fatalf("items: %+v", items)
	}
}

// fakeBackend : stub of model.Backend. The embedded interface is nil — only
// the methods a test really calls need a body.
type fakeBackend struct {
	model.Backend
	caps     model.Caps // what the fake network can do; zero = nothing
	members  []string   // tokens received by WhoisMember
	whois    int        // Whois calls
	reacts   int        // React calls
	reacted  string     // emoji of the last React call
	whoRead  int        // WhoRead calls
	dialogs  int        // LoadDialogs calls
	search   int        // Search calls
	since    int        // LoadHistorySince calls
	history  int        // LoadHistory calls
	photo    int        // SendPhoto calls
	photoTmp int64      // tmpID of the last SendPhoto
	file     int        // SendFile calls
	fileTmp  int64      // tmpID of the last SendFile
	// GIF box (gifs_test.go): queries sent, results posted, downloads asked.
	gsearch    []string // SearchGlobal queries
	gifQueries []string
	gifsSent   []model.Gif
	downloads  []string
}

func (f *fakeBackend) Caps() model.Caps { return f.caps }

func (f *fakeBackend) LoadDialogs(context.Context) { f.dialogs++ }

func (f *fakeBackend) WhoisMember(_ context.Context, q string) { f.members = append(f.members, q) }

func (f *fakeBackend) Whois(context.Context, *model.Chat) { f.whois++ }

func (f *fakeBackend) Search(context.Context, *model.Chat, string, int) { f.search++ }

func (f *fakeBackend) React(_ context.Context, _ *model.Chat, _ int, e string) {
	f.reacts++
	f.reacted = e
}

func (f *fakeBackend) WhoRead(context.Context, *model.Chat, int, int) { f.whoRead++ }

func (f *fakeBackend) LoadHistorySince(context.Context, *model.Chat, int, int) { f.since++ }

func (f *fakeBackend) LoadHistory(context.Context, *model.Chat, int, int) { f.history++ }

// net routes by the Net of the chat; nil for a nil chat, one not stamped, or
// one whose network is gone. Every call site skips the call on nil.
func TestNetRouting(t *testing.T) {
	b := &fakeBackend{}
	u := &UI{nets: map[string]model.Backend{model.NetTelegram: b}}
	if u.net(&model.Chat{Net: model.NetTelegram}) != model.Backend(b) {
		t.Fatal("route telegram")
	}
	if u.netOf(model.NetTelegram) != model.Backend(b) {
		t.Fatal("netOf telegram")
	}
	if u.net(nil) != nil || u.net(&model.Chat{}) != nil || u.net(&model.Chat{Net: "discord"}) != nil {
		t.Fatal("nil expected: nil chat, not stamped, network gone")
	}
	var seen []model.Backend
	u.eachNet(func(b model.Backend) { seen = append(seen, b) })
	if len(seen) != 1 || seen[0] != model.Backend(b) {
		t.Fatalf("eachNet: %v", seen)
	}
}

// eachNetCap only calls the backends that carry the capability, and says when
// none of them does — the caller then answers net_unsupported.
func TestEachNetCap(t *testing.T) {
	tg := &fakeBackend{caps: model.Caps{GlobalSearch: true}}
	dc := &fakeBackend{}
	u := &UI{nets: map[string]model.Backend{model.NetTelegram: tg, "discord": dc}}
	var seen []model.Backend
	if !u.eachNetCap(func(c model.Caps) bool { return c.GlobalSearch },
		func(b model.Backend) { seen = append(seen, b) }) {
		t.Fatal("global search: telegram carries it")
	}
	if len(seen) != 1 || seen[0] != model.Backend(tg) {
		t.Fatalf("backends called: %v", seen)
	}
	if u.eachNetCap(func(c model.Caps) bool { return c.Contacts },
		func(model.Backend) { t.Error("contacts: no backend carries it") }) {
		t.Fatal("contacts: no backend, true given back")
	}
}

// A command the network cannot do is refused with net_unsupported and the
// backend is never asked; with the capability it goes through.
func TestWhoisUnsupported(t *testing.T) {
	u := netUI(model.NetTelegram)
	b := u.nets[model.NetTelegram].(*fakeBackend)
	w := u.ws.List[0]
	w.Chat = &model.Chat{Net: model.NetTelegram, ID: 1, Kind: model.ChatUser, Title: "alice"}
	u.command("whois", nil, "")
	if b.whois != 0 {
		t.Fatalf("Whois called %d time(s) with no whois capability", b.whois)
	}
	if got := lastSys(w); got != i18n.T("net_unsupported", model.NetTelegram) {
		t.Fatalf("refusal line: %q", got)
	}
	b.caps = model.AllCaps()
	u.command("whois", nil, "")
	if b.whois != 1 {
		t.Fatalf("Whois calls with the capability: %d", b.whois)
	}
}

// /search on a network with no in-chat search is refused, and the entry of
// the context menu says the same rather than filling in a command that would
// be refused right after.
func TestSearchUnsupported(t *testing.T) {
	u := netUI(model.NetTelegram)
	b := u.nets[model.NetTelegram].(*fakeBackend)
	w := u.ws.List[0]
	w.Chat = &model.Chat{Net: model.NetTelegram, ID: 1, Title: "room"}
	u.command("search", nil, "hello")
	if b.search != 0 {
		t.Fatalf("Search called %d time(s) with no search capability", b.search)
	}
	if got := lastSys(w); got != i18n.T("net_unsupported", model.NetTelegram) {
		t.Fatalf("refusal line: %q", got)
	}
	u.menuDo(&ctxMenu{chat: w.Chat}, "search")
	if u.ed.String() != "" {
		t.Fatalf("menu entry with no capability: input %q", u.ed.String())
	}
	b.caps = model.AllCaps()
	u.command("search", nil, "hello")
	if b.search != 1 {
		t.Fatalf("Search calls with the capability: %d", b.search)
	}
}

// The connection state is per network: a Discord drop must not say Telegram
// is gone, and a Discord comeback must not hide a Telegram drop. The status
// segment names the network as soon as there are two.
func TestConnPerNet(t *testing.T) {
	u := netUI(model.NetTelegram, model.NetDiscord)
	if u.connStatus() != i18n.T("status_disconnected_net", model.NetDiscord+", "+model.NetTelegram) {
		t.Fatalf("before any connection: %q", u.connStatus())
	}
	for _, n := range []string{model.NetTelegram, model.NetDiscord} {
		u.dispatchNet = n
		u.event(model.EvConnected{})
	}
	if u.connStatus() != "" {
		t.Fatalf("two connected networks: %q", u.connStatus())
	}
	u.dispatchNet = model.NetDiscord
	u.event(model.EvDisconnected{})
	if got := u.connStatus(); got != i18n.T("status_disconnected_net", model.NetDiscord) {
		t.Fatalf("discord gone: %q", got)
	}
	if !u.conn[model.NetTelegram] {
		t.Fatal("telegram is still connected")
	}
	u.event(model.EvConnected{}) // discord comes back while telegram drops
	u.dispatchNet = model.NetTelegram
	u.event(model.EvDisconnected{})
	if got := u.connStatus(); got != i18n.T("status_disconnected_net", model.NetTelegram) {
		t.Fatalf("telegram gone: %q", got)
	}
}

// With a single network the segment stays the one of before: no name.
func TestConnStatusMono(t *testing.T) {
	u := netUI(model.NetTelegram)
	if got := u.connStatus(); got != i18n.T("status_disconnected") {
		t.Fatalf("single network, disconnected: %q", got)
	}
	u.dispatchNet = model.NetTelegram
	u.event(model.EvConnected{})
	if got := u.connStatus(); got != "" {
		t.Fatalf("single network, connected: %q", got)
	}
}

// TestSelfPerNet: two networks, two identities. The EvReady of one network
// never speaks for the other — Own() reads the id of the network of the
// message — and an account change on one network wipes its cache alone.
func TestSelfPerNet(t *testing.T) {
	tgCache, dcCache := cache.New(t.TempDir(), 2000), cache.New(t.TempDir(), 2000)
	// Both caches were written by an account of their own; only the discord one
	// will find a different id at EvReady time.
	for _, c := range []struct {
		cc   *cache.Cache
		self int64
	}{{tgCache, 10}, {dcCache, 20}} {
		if err := c.cc.SaveDialogs([]model.Chat{{ID: 1, Title: "chat", LastDate: time.Now()}}, c.self); err != nil {
			t.Fatal(err)
		}
		if err := c.cc.SaveHistory(1, []model.Msg{{ID: 4, ChatID: 1, Text: "bonjour"}}); err != nil {
			t.Fatal(err)
		}
	}
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{AutoOpenDays: 7},
		caches: map[string]*cache.Cache{model.NetTelegram: tgCache, "discord": dcCache},
		nets:   map[string]model.Backend{model.NetTelegram: &fakeBackend{}, "discord": &fakeBackend{}},
		chats:  map[model.ChatKey]*model.Chat{}, dirty: map[model.ChatKey]bool{},
		self: map[string]selfInfo{}, dialogsSeen: map[string]bool{}, reactList: map[string][]string{}}
	u.loadCache()

	u.dispatch(model.Envelope{Net: model.NetTelegram, Ev: model.EvReady{SelfID: 10, SelfName: "alice"}})
	u.dispatch(model.Envelope{Net: "discord", Ev: model.EvReady{SelfID: 77, SelfName: "bob", Bot: true}})

	// EvReady asks for the dialogs of its own network alone; the discord one is
	// a bot and asks for none.
	if got := u.nets[model.NetTelegram].(*fakeBackend).dialogs; got != 1 {
		t.Fatalf("telegram LoadDialogs: %d call(s), want 1", got)
	}
	if got := u.nets["discord"].(*fakeBackend).dialogs; got != 0 {
		t.Fatalf("discord (bot) LoadDialogs: %d call(s), want 0", got)
	}
	if len(u.self) != 2 {
		t.Fatalf("%d identities, want one per network: %+v", len(u.self), u.self)
	}
	if got := u.selfOf(model.NetTelegram); got != (selfInfo{ID: 10, Name: "alice"}) {
		t.Fatalf("telegram identity: %+v", got)
	}
	if got := u.selfOf("discord"); got != (selfInfo{ID: 77, Name: "bob", Bot: true}) {
		t.Fatalf("discord identity: %+v", got)
	}

	// Own reads the identity of the network of the message: the discord id owns
	// a discord message and nothing on telegram.
	mine := &model.Msg{Net: "discord", FromID: 77}
	if !u.own(mine) {
		t.Fatal("discord message of the discord account: Own must be true")
	}
	if other := (&model.Msg{Net: model.NetTelegram, FromID: 77}); u.own(other) {
		t.Fatal("the discord id must not own a telegram message")
	}

	// The discord account is not the one of its cache (77 vs 20): that cache
	// goes, the telegram one stays whole.
	if u.chats[model.ChatKey{Net: "discord", ID: 1}] != nil {
		t.Fatalf("discord cache kept after an account change: %+v", u.chats)
	}
	if msgs, _ := dcCache.LoadHistory(1); msgs != nil {
		t.Fatalf("discord history file kept: %+v", msgs)
	}
	if u.chats[tgk(1)] == nil {
		t.Fatalf("telegram cache dropped by the discord account change: %+v", u.chats)
	}
	if msgs, _ := tgCache.LoadHistory(1); len(msgs) != 1 {
		t.Fatalf("telegram history file wiped: %+v", msgs)
	}
}

// Shift+F2 cycles the /net filter, like /net with no argument: same order,
// same status line. It leaves the side panel where it is.
func TestShiftF2CyclesNet(t *testing.T) {
	u := netUI(model.NetTelegram, "discord")
	for _, want := range []string{"discord", model.NetTelegram, netAll} {
		u.key(term.Key{Code: term.F2, Shift: true})
		got := u.netFilter
		if got == "" {
			got = netAll
		}
		if got != want {
			t.Fatalf("cycle: %q, want %q", got, want)
		}
		if line, exp := lastSys(u.ws.List[0]), i18n.T("net_filter", want); line != exp {
			t.Fatalf("answer: %q, want %q", line, exp)
		}
		if u.side != sideHidden {
			t.Fatalf("Shift+F2 moved the side panel: %d", u.side)
		}
	}
	// A single network: the key changes nothing and answers nothing. /net says
	// "a single network"; a key must not fill the window with a line nobody
	// asked for.
	m := netUI(model.NetTelegram)
	m.key(term.Key{Code: term.F2, Shift: true})
	if m.netFilter != "" || len(m.ws.List[0].Items) != 0 {
		t.Fatalf("single network: filter %q, %d lines", m.netFilter, len(m.ws.List[0].Items))
	}
}

// F2 in windows mode walks the networks before hiding the panel: the windows
// of one network at a time, then off with the filter lifted. With a single
// network the three stops of F2 stay what they were.
func TestF2CyclesNetsInWindowsMode(t *testing.T) {
	u := netUI(model.NetTelegram, "discord")
	want := []struct {
		side sideMode
		net  string
	}{{sideChats, ""}, {sideWindows, ""}, {sideWindows, "discord"}, {sideWindows, model.NetTelegram}, {sideHidden, ""}}
	for i, w := range want {
		u.key(term.Key{Code: term.F2})
		if u.side != w.side || u.netFilter != w.net {
			t.Fatalf("press %d: side %d filter %q, want side %d filter %q", i+1, u.side, u.netFilter, w.side, w.net)
		}
	}

	// A filter set beforehand is kept when the windows mode opens: the cycle
	// goes on from there, and its wrap lifts the filter, predicate included.
	f := netUI(model.NetTelegram, "discord")
	f.command("net", []string{model.NetTelegram}, model.NetTelegram)
	f.key(term.Key{Code: term.F2})
	f.key(term.Key{Code: term.F2})
	if f.side != sideWindows || f.netFilter != model.NetTelegram {
		t.Fatalf("windows mode entered: side %d filter %q", f.side, f.netFilter)
	}
	f.key(term.Key{Code: term.F2})
	if f.side != sideHidden || f.netFilter != "" || f.agg.Filter != nil {
		t.Fatalf("wrap: side %d filter %q predicate set %v", f.side, f.netFilter, f.agg.Filter != nil)
	}

	m := netUI(model.NetTelegram)
	for i, w := range []sideMode{sideChats, sideWindows, sideHidden} {
		m.key(term.Key{Code: term.F2})
		if m.side != w || m.netFilter != "" {
			t.Fatalf("single network, press %d: side %d filter %q", i+1, m.side, m.netFilter)
		}
	}
}

func (f *fakeBackend) SendPhoto(_ context.Context, _ *model.Chat, _, _ string, _ bool, tmpID int64) {
	f.photo, f.photoTmp = f.photo+1, tmpID
}

func (f *fakeBackend) SendFile(_ context.Context, _ *model.Chat, _, _ string, _ bool, tmpID int64) {
	f.file, f.fileTmp = f.file+1, tmpID
}
