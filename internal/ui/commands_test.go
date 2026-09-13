package ui

import (
	"context"
	"errors"
	"fmt"
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

// TestSetUnknownKey : "/set foobar" alone used to say nothing at all, while
// "/set foobar valeur" answered. Both answer now.
func TestSetUnknownKey(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{}, t: &term.Term{Cols: 80, Rows: 24}}
	w := u.ws.List[0]
	u.setCmd([]string{"foobar"})
	if len(w.Items) != 1 || !strings.Contains(w.Items[0].Sys, "foobar") {
		t.Fatalf("unknown key: %+v", w.Items)
	}
	// A known key still echoes its value, and nothing else.
	u.setCmd([]string{"bell"})
	if len(w.Items) != 2 || !strings.HasPrefix(w.Items[1].Sys, "bell = ") {
		t.Fatalf("known key: %+v", w.Items)
	}
	// "/set" with no argument still echoes the 22 keys, none unknown.
	u.setCmd(nil)
	for _, it := range w.Items[2:] {
		if strings.Contains(it.Sys, "inconnue") {
			t.Fatalf("setKeys key missing from show(): %q", it.Sys)
		}
	}
	if len(w.Items)-2 != len(setKeys) {
		t.Fatalf("%d lines for %d keys", len(w.Items)-2, len(setKeys))
	}
}

// TestSetHoverOffClearsZone : "/set hover off" drops the lit zone at once. The
// pointer is not moving when the command is typed, so without that the bar of
// the panel or the column of the scrollbar stayed lit until the next move,
// while the option promises the behaviour from before, to the byte.
func TestSetHoverOffClearsZone(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, t: &term.Term{Cols: 80, Rows: 24},
		cfg: &config.Config{Hover: config.HoverMenu}}
	u.zone = zoneSide
	u.setCmd([]string{"hover", "off"})
	if u.cfg.Hover != config.HoverOff || u.zone != zoneNone {
		t.Fatalf("hover = %q, zone = %v", u.cfg.Hover, u.zone)
	}
}

// netUI: interface with the given networks, two chats and two aggregated
// messages (one per network).
func netUI(names ...string) *UI {
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{}, t: &term.Term{Cols: 80, Rows: 24},
		nets: map[string]model.Backend{}, conn: map[string]bool{}}
	for i, n := range names {
		u.nets[n] = &fakeBackend{}
		c := &model.Chat{Net: n, ID: int64(i + 1), Title: n + "-chat", LastDate: time.Now()}
		u.chatList = append(u.chatList, c)
		u.agg.Items = append(u.agg.Items, &Item{Msg: &model.Msg{Net: n, ChatID: c.ID, ID: i + 1,
			Date: time.Now(), From: "alice", FromID: 7, Text: "message " + n}})
	}
	return u
}

// aggText: text of the lines of the aggregated view.
func aggText(u *UI) string {
	var b strings.Builder
	for _, l := range u.agg.Lines(render.Opts{Width: 40, Theme: theme.Terminal()}) {
		b.WriteString(render.LineText(l) + "\n")
	}
	return b.String()
}

func sideTitles(u *UI) []string {
	var out []string
	for _, c := range u.sideChats() {
		out = append(out, c.Title)
	}
	return out
}

// lastSys: last system line of a window.
func lastSys(w *Window) string {
	if len(w.Items) == 0 {
		return ""
	}
	return w.Items[len(w.Items)-1].Sys
}

// /query on a network that cannot resolve a name says so at once: no
// "resolving…" taken back on the next line, and no window left pending.
func TestBindNoResolver(t *testing.T) {
	u := netUI("discord") // fakeBackend, zero Caps: no Resolve
	u.lookups = map[uint64]*lookup{}
	w := u.ws.New(true)
	u.bind(w, "@nobody", false)

	var sys []string
	for _, it := range w.Items {
		sys = append(sys, it.Sys)
	}
	if len(sys) != 1 || !strings.Contains(sys[0], i18n.T("net_unsupported", "discord")) {
		t.Fatalf("sys lines: %q", sys)
	}
	if len(u.lookups) != 0 {
		t.Fatalf("window left lookups: %v", u.lookups)
	}
}

// A single backend: /net is inert. It names the network in place, sets no
// filter, and multiNet stays false.
func TestNetSingle(t *testing.T) {
	u := netUI(model.NetTelegram)
	if u.multiNet() {
		t.Fatal("single network: multiNet must be false")
	}
	// An unknown name just like the only valid name: same answer, no filter.
	for _, arg := range []string{"discord", model.NetTelegram, ""} {
		u.command("net", []string{arg}, arg)
		if u.netFilter != "" {
			t.Fatalf("/net %q: filter set on a single network: %q", arg, u.netFilter)
		}
		if got, want := lastSys(u.ws.List[0]), i18n.T("net_single", model.NetTelegram); got != want {
			t.Fatalf("/net %q: answer %q, want %q", arg, got, want)
		}
	}
	if len(sideTitles(u)) != 1 {
		t.Fatalf("sidebar: %v", sideTitles(u))
	}
}

// Two backends: /net <name> cuts the sidebar and the aggregated view, /net
// all lifts the filter, /net with no argument cycles through the names in
// order.
func TestNetFilterTwoNets(t *testing.T) {
	u := netUI(model.NetTelegram, "discord")
	if !u.multiNet() {
		t.Fatal("two networks: multiNet must be true")
	}

	u.command("net", []string{model.NetTelegram}, model.NetTelegram)
	if u.netFilter != model.NetTelegram {
		t.Fatalf("filter: %q", u.netFilter)
	}
	if got := sideTitles(u); !slices.Equal(got, []string{"telegram-chat"}) {
		t.Fatalf("filtered sidebar: %v", got)
	}
	if got := aggText(u); !strings.Contains(got, "message telegram") || strings.Contains(got, "message discord") {
		t.Fatalf("filtered aggregate: %q", got)
	}

	// The selection does not land on a hidden message: the last item of the
	// aggregate is the discord one.
	if u.agg.SelectNext(-1, nil); u.agg.Sel == nil || u.agg.Sel.Msg.Net != model.NetTelegram {
		t.Fatalf("selection on a hidden message: %+v", u.agg.Sel)
	}
	// The nth media counts on what is on screen: the only media of the
	// aggregate is carried by the discord message, hidden by the filter.
	u.images = "off" // /view stops before opening a preview
	u.agg.Items[1].Msg.Media = &model.Media{Kind: model.MediaPhoto, Loc: struct{}{}, Path: "/nonexistent/x.bin"}
	u.openMedia(u.agg, 1)
	if got, want := lastSys(u.agg), i18n.T("no_media"); got != want {
		t.Fatalf("/open under filter: %q, want %q", got, want)
	}
	u.viewNth(u.agg, 1)
	if got, want := lastSys(u.agg), i18n.T("no_previewable_media"); got != want {
		t.Fatalf("/view under filter: %q, want %q", got, want)
	}
	u.agg.Items = u.agg.Items[:2] // remove the two system lines

	// The filter releases the selection it just hid: without that, e/r/o
	// would act on an invisible message.
	u.agg.Sel = u.agg.Items[0] // the telegram message, visible under this filter
	u.command("net", []string{"discord"}, "discord")
	if u.agg.Sel != nil {
		t.Fatalf("selection kept on a hidden message: %+v", u.agg.Sel.Msg)
	}
	u.command("net", []string{model.NetTelegram}, model.NetTelegram)

	// Unknown network: nothing moves, the available names are recalled.
	u.command("net", []string{"nope"}, "nope")
	if u.netFilter != model.NetTelegram {
		t.Fatalf("unknown name: filter switched to %q", u.netFilter)
	}
	if got := lastSys(u.ws.List[0]); !strings.Contains(got, "nope") || !strings.Contains(got, "discord") {
		t.Fatalf("answer to the unknown name: %q", got)
	}

	u.command("net", []string{"all"}, "all")
	if u.netFilter != "" || len(sideTitles(u)) != 2 || u.agg.Filter != nil {
		t.Fatalf("all: filter %q, sidebar %v, predicate set %v", u.netFilter, sideTitles(u), u.agg.Filter != nil)
	}
	if got := aggText(u); !strings.Contains(got, "message telegram") || !strings.Contains(got, "message discord") {
		t.Fatalf("aggregate without filter: %q", got)
	}

	// Cycle: all → discord → telegram → all (order of the names).
	for _, want := range []string{"discord", model.NetTelegram, ""} {
		u.command("net", nil, "")
		if u.netFilter != want {
			t.Fatalf("cycle: %q, want %q", u.netFilter, want)
		}
	}
}

// TestBotGateOrder: a bot account answers bot_unavailable on an unbound window
// too — the account gate comes before the "window not bound" test, as it did
// with a single network. With a second, human, network the account gate opens
// and the network of the chat closes the command again.
func TestBotGateOrder(t *testing.T) {
	u := netUI(model.NetTelegram)
	u.self = map[string]selfInfo{model.NetTelegram: {ID: 1, Bot: true}}
	w := u.ws.List[0] // window 0: bound to no chat
	for _, cmd := range []string{"history", "search"} {
		u.command(cmd, nil, "x")
		if got := lastSys(w); got != i18n.T("bot_unavailable") {
			t.Fatalf("/%s, bot account on an unbound window: %q, want %q", cmd, got, i18n.T("bot_unavailable"))
		}
	}
	u.nets["discord"] = &fakeBackend{}
	u.self["discord"] = selfInfo{ID: 2, Name: "bob"} // human: the account gate opens
	w.Chat = &model.Chat{Net: model.NetTelegram, ID: 1, Title: "tg"}
	for _, cmd := range []string{"history", "search"} {
		u.command(cmd, nil, "x")
		if got := lastSys(w); got != i18n.T("bot_unavailable") {
			t.Fatalf("/%s on a chat of the bot network: %q, want %q", cmd, got, i18n.T("bot_unavailable"))
		}
	}
}

// foldUI : two networks and a Discord guild — three sections, fold state
// loaded, as sideBlock wires it.
func foldUI() *UI {
	u := netUI(model.NetTelegram, model.NetDiscord)
	u.th, u.side, u.sideW, u.folded = theme.Terminal(), sideChats, testSideW, map[string]bool{}
	u.chatList = append(u.chatList, &model.Chat{Net: model.NetDiscord, ID: 3, Title: "Gophers / #general", LastDate: time.Now()})
	return u
}

// TestFoldCmdMono : a single network with no guild carries no section, so
// /fold folds nothing and says so instead of writing a dead key.
func TestFoldCmdMono(t *testing.T) {
	t.Setenv("TTYLOOM_DIR", t.TempDir())
	u := netUI(model.NetTelegram)
	u.folded = map[string]bool{}
	u.command("fold", nil, "")
	if got := lastSys(u.view()); got != i18n.T("fold_none") {
		t.Fatalf("mono: %q", got)
	}
	if len(u.folded) != 0 {
		t.Fatalf("key written: %v", u.folded)
	}
}

// TestFoldCmd : /fold names a section by its key or by a prefix of the name
// shown, folds it and unfolds it on a second call.
func TestFoldCmd(t *testing.T) {
	t.Setenv("TTYLOOM_DIR", t.TempDir())
	u := foldUI()
	w := u.view()

	u.command("fold", []string{"discord:Gophers"}, "discord:Gophers")
	if !u.folded["discord:Gophers"] {
		t.Fatalf("by key: %v", u.folded)
	}
	// The header of a folded section shows [+] and hides its chats.
	var hdr string
	for _, r := range u.sideRowList() {
		if r.sec == "discord:Gophers" && r.chat == nil {
			hdr = render.LineText(render.Line{Spans: sideSecLine(r, u.th, testSideW, 0)})
		}
	}
	if !strings.Contains(hdr, "[+]") || !strings.Contains(hdr, "Gophers") {
		t.Fatalf("header of the folded section: %q", hdr)
	}
	u.command("fold", []string{"discord:Gophers"}, "discord:Gophers")
	if u.folded["discord:Gophers"] {
		t.Fatalf("second call unfolds: %v", u.folded)
	}
	// Prefix of the name shown, case and accents apart.
	u.command("fold", []string{"goph"}, "goph")
	if !u.folded["discord:Gophers"] {
		t.Fatalf("by prefix: %v", u.folded)
	}
	// A network key stays exact, guild section or not.
	u.command("fold", []string{"telegram"}, "telegram")
	if !u.folded[model.NetTelegram] {
		t.Fatalf("by network key: %v", u.folded)
	}
	if lastSys(w) != "" {
		t.Fatalf("a fold that works says nothing: %q", lastSys(w))
	}
}

// TestFoldCmdUnknown : a typo folds nothing and writes nothing — it lists the
// sections instead.
func TestFoldCmdUnknown(t *testing.T) {
	t.Setenv("TTYLOOM_DIR", t.TempDir())
	u := foldUI()
	u.command("fold", []string{"nawak"}, "nawak")
	got := lastSys(u.view())
	if !strings.Contains(got, "nawak") || !strings.Contains(got, model.NetTelegram) || !strings.Contains(got, "discord:Gophers") {
		t.Fatalf("unknown section: %q", got)
	}
	if len(u.folded) != 0 {
		t.Fatalf("key written for a typo: %v", u.folded)
	}
}

// TestFoldCmdList : /fold with no argument lists the sections and their state.
func TestFoldCmdList(t *testing.T) {
	t.Setenv("TTYLOOM_DIR", t.TempDir())
	u := foldUI()
	u.folded["discord:Gophers"] = true
	u.command("fold", nil, "")
	got := lastSys(u.view())
	if !strings.Contains(got, "discord:Gophers [+]") || !strings.Contains(got, model.NetTelegram+" [-]") {
		t.Fatalf("list: %q", got)
	}
	if len(u.folded) != 1 {
		t.Fatalf("the list folds nothing: %v", u.folded)
	}
}

// TestFoldCandidates : Tab after /fold proposes the section keys.
func TestFoldCandidates(t *testing.T) {
	u := foldUI()
	if got := sectionKeys(u.foldSections()); !slices.Equal(got, []string{"discord", "discord:Gophers", model.NetTelegram}) {
		t.Fatalf("candidates: %v", got)
	}
	if got := sectionKeys(netUI(model.NetTelegram).foldSections()); got != nil {
		t.Fatalf("mono: %v", got)
	}
}

// TestDigitWindowCommand : "/2" goes to window 2 — an ircii habit. Out of
// range: one system line and the current window does not move.
func TestDigitWindowCommand(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{},
		chats: map[model.ChatKey]*model.Chat{}}
	u.ws.New(true) // window 1
	u.ws.New(true) // window 2
	run := func(line string) {
		name, args, text, ok := ParseCommand(line, commandNames)
		if !ok {
			t.Fatalf("%q: not read as a command", line)
		}
		u.command(name, args, text)
	}

	run("/2")
	if u.ws.Cur != 2 {
		t.Fatalf("/2: current window %d, want 2", u.ws.Cur)
	}
	before := len(u.view().Items)
	run("/99")
	if u.ws.Cur != 2 {
		t.Fatalf("/99: current window %d, want 2 unchanged", u.ws.Cur)
	}
	if got := len(u.view().Items) - before; got != 1 {
		t.Fatalf("/99: %d system lines, want 1", got)
	}
	if got, want := lastSys(u.view()), i18n.T("no_window_n", 99); got != want {
		t.Fatalf("/99: %q, want %q", got, want)
	}
	run("/0")
	if u.ws.Cur != 0 {
		t.Fatalf("/0: current window %d, want 0", u.ws.Cur)
	}
}

// TestSetLangRefreshesStoredTexts : the i18n texts kept in the state were
// translated when they were built. /set lang has to bring them into the new
// language: a presence would otherwise stay in the old one until the next
// update of the peer, and the cache mark, which is looked up by its text,
// could never be dropped again.
func TestSetLangRefreshesStoredTexts(t *testing.T) {
	t.Setenv("TTYLOOM_DIR", t.TempDir()) // /set saves the config
	t.Cleanup(func() { i18n.Set("fr") }) // TestMain sets fr for the package
	c := &model.Chat{Net: model.NetTelegram, ID: 1, Kind: model.ChatUser, Title: "Alice"}
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{},
		t: &term.Term{Cols: 80, Rows: 24}, presence: map[model.ChatKey]string{}}
	w := u.ws.List[0]
	w.Chat = c
	u.presence[c.Key()] = i18n.T("presence_online") // as an EvPresence stores it
	w.AddSys(cacheMark())

	u.setCmd([]string{"lang", "en"})

	if got := u.presence[c.Key()]; got != i18n.T("presence_online") {
		t.Fatalf("presence kept in the old language: %q", got)
	}
	if !u.online(c) {
		t.Fatal("online marker lost by the language change")
	}
	n := len(w.Items)
	w.dropCacheMark()
	if len(w.Items) != n-1 {
		t.Fatalf("cache mark not droppable any more: %q", w.Items[0].Sys)
	}
}

// TestCycleUnread : /set cycle_mode last_unread — Ctrl+X goes to the next
// window with activity, in number order, then back to the window left at the
// first jump; with nothing unread and no home left, the classic cycle.
func TestCycleUnread(t *testing.T) {
	u := &UI{ws: NewWindows(), cfg: &config.Config{CycleMode: "last_unread"}}
	for range 3 {
		u.ws.New(true) // windows 1, 2, 3
	}
	press := func() int { // Ctrl+X, with what goTo does to the window reached
		n := u.cycleTarget()
		u.ws.Cur, u.ws.List[n].Act = n, 0
		return n
	}
	u.ws.Cur = 1 // on A (1); B (2) and C (3) have unread
	u.ws.List[2].Act, u.ws.List[3].Act = 2, 1
	if a, b, c := press(), press(), press(); a != 2 || b != 3 || c != 1 {
		t.Fatalf("tour: %d %d %d, want B, C, then back to A", a, b, c)
	}
	if press() != 2 { // nothing unread, home taken back: the classic cycle
		t.Fatal("no unread: next window expected")
	}
	// Home closed during the tour: the classic cycle takes over.
	u.ws.Cur, u.ws.List[3].Act = 1, 1
	press()         // to C, home = A
	u.ws.CloseAt(1) // A is gone; C is window 2 now
	if press() != 0 {
		t.Fatal("home gone: next window expected")
	}
	u.cfg.CycleMode, u.ws.Cur, u.ws.List[2].Act = "next", 0, 1
	if press() != 1 {
		t.Fatal("mode next: the window after the current one, unread or not")
	}
}

// TestSetCycleMode : the key takes next | last_unread only.
func TestSetCycleMode(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{CycleMode: "next"}, t: &term.Term{Cols: 80, Rows: 24}}
	u.setCmd([]string{"cycle_mode", "sideways"})
	if u.cfg.CycleMode != "next" {
		t.Fatalf("bad value taken: %q", u.cfg.CycleMode)
	}
	u.setCmd([]string{"cycle_mode", "last_unread"})
	if u.cfg.CycleMode != "last_unread" {
		t.Fatalf("value refused: %q", u.cfg.CycleMode)
	}
}

// launchUI : interface with no live network, discord and telegram configured,
// and a launcher that counts its calls and gives a fake backend (or err).
func launchUI(err error) (*UI, *int) {
	u := netUI()
	u.ctx = context.Background()
	u.netList = []string{model.NetDiscord, model.NetTelegram}
	u.netCancel = map[string]context.CancelFunc{}
	u.self = map[string]selfInfo{}
	n := new(int)
	u.launch = func(context.Context, string) (model.Backend, error) {
		*n++
		if err != nil {
			return nil, err
		}
		return &fakeBackend{}, nil
	}
	return u, n
}

// TestNetLogin : /discord login starts the network through the launcher once;
// a second login does not start it again, and status says who we are.
func TestNetLogin(t *testing.T) {
	u, n := launchUI(nil)
	w := u.ws.List[0]
	u.command("discord", []string{"status"}, "status")
	if len(w.Items) != 1 || !strings.Contains(w.Items[0].Sys, "/discord login") {
		t.Fatalf("status before login: %+v", w.Items)
	}
	u.command("discord", []string{"login"}, "login")
	if *n != 1 || u.nets[model.NetDiscord] == nil || u.netCancel[model.NetDiscord] == nil {
		t.Fatalf("login: launched %d, net %v", *n, u.nets[model.NetDiscord])
	}
	u.command("discord", []string{"login"}, "login")
	if *n != 1 {
		t.Fatalf("second login launched again: %d", *n)
	}
	u.self[model.NetDiscord] = selfInfo{ID: 1, Name: "alice"}
	u.conn[model.NetDiscord] = true
	u.command("discord", nil, "")
	if last := w.Items[len(w.Items)-1].Sys; !strings.Contains(last, "alice") {
		t.Fatalf("status after login: %q", last)
	}
}

// TestNetLoginError : a launcher that fails (token_cmd: exit status 1) puts
// the error in window 0 and starts nothing.
func TestNetLoginError(t *testing.T) {
	u, _ := launchUI(errors.New("discord: token_cmd: exit status 1"))
	u.command("discord", []string{"login"}, "login")
	if u.nets[model.NetDiscord] != nil {
		t.Fatal("backend kept after a failed launch")
	}
	it := u.ws.List[0].Items
	if n := len(it); n < 2 || !strings.Contains(it[n-2].Sys, "token_cmd: exit status 1") || !strings.Contains(it[n-1].Sys, "/discord login") {
		t.Fatalf("window 0: %+v", it)
	}
}

// TestNetLogoutStopped : /discord logout cancels the network's context, and
// the EvStopped that follows drops the backend so that login can start it
// again. EvStopped with an error says so in window 0.
func TestNetLogoutStopped(t *testing.T) {
	u, _ := launchUI(nil)
	u.command("discord", []string{"login"}, "login")
	var ctx context.Context
	u.launch = func(c context.Context, _ string) (model.Backend, error) { ctx = c; return &fakeBackend{}, nil }
	u.command("discord", []string{"logout"}, "logout")
	u.command("discord", []string{"login"}, "login") // still up until EvStopped: nothing starts
	if ctx != nil {
		t.Fatal("login while stopping launched a second backend")
	}
	if u.nets[model.NetDiscord] == nil {
		t.Fatal("backend dropped before EvStopped")
	}
	u.dispatch(model.Envelope{Net: model.NetDiscord, Ev: model.EvStopped{}})
	if u.nets[model.NetDiscord] != nil || u.netCancel[model.NetDiscord] != nil {
		t.Fatal("backend kept after EvStopped")
	}
	u.dispatch(model.Envelope{Net: model.NetDiscord, Ev: model.EvStopped{Err: "discord: 4004"}})
	if last := u.ws.List[0].Items[len(u.ws.List[0].Items)-1].Sys; !strings.Contains(last, "4004") || !strings.Contains(last, "/discord login") {
		t.Fatalf("window 0: %q", last)
	}
}

// TestFindChatContains : a word from inside the title finds the chat when it
// is the only one — what the completion of "/query cop" hands over.
func TestFindChatContains(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{}, t: &term.Term{Cols: 80, Rows: 24},
		chatList: []*model.Chat{{Net: "telegram", ID: 1, Title: "Les copains du foot"}, {Net: "telegram", ID: 2, Title: "Foot en salle"},
			{Net: "telegram", ID: 3, Title: "Club de foot"}}}
	if c, _ := u.findChat("copains du foot"); c == nil || c.ID != 1 {
		t.Fatalf("inside the title: %+v", c)
	}
	if c, _ := u.findChat("foot"); c == nil || c.ID != 2 { // a prefix wins over the pieces inside
		t.Fatalf("prefix first: %+v", c)
	}
	if c, _ := u.findChat("de foot"); c == nil || c.ID != 3 { // inside one title only
		t.Fatalf("inside, unique: %+v", c)
	}
}

// tabsUI : two networks, one bound window each (1 = discord, 2 = telegram).
func tabsUI() *UI {
	u := netUI(model.NetDiscord, model.NetTelegram)
	u.chats = map[model.ChatKey]*model.Chat{}
	u.dirty = map[model.ChatKey]bool{}
	u.self = map[string]selfInfo{}
	for _, c := range u.chatList {
		u.chats[c.Key()] = c
		u.bindChat(u.ws.New(true), c)
	}
	return u
}

// F9 turns the tab mode on and walks all → discord → telegram → all; each
// network tab lands on a window of that network, "all" stays put.
func TestF9CyclesTabs(t *testing.T) {
	u := tabsUI()
	u.key(term.Key{Code: term.F9})
	if !u.cfg.Tabs || u.netFilter != model.NetDiscord || u.ws.Cur != 1 {
		t.Fatalf("first F9: tabs=%v filter=%q cur=%d", u.cfg.Tabs, u.netFilter, u.ws.Cur)
	}
	u.key(term.Key{Code: term.F9})
	if u.netFilter != model.NetTelegram || u.ws.Cur != 2 {
		t.Fatalf("second F9: filter=%q cur=%d", u.netFilter, u.ws.Cur)
	}
	u.key(term.Key{Code: term.F9})
	if u.netFilter != "" || u.ws.Cur != 2 {
		t.Fatalf("third F9 (all): filter=%q cur=%d", u.netFilter, u.ws.Cur)
	}
	u.key(term.Key{Code: term.F9}) // back on discord: its last window again
	if u.netFilter != model.NetDiscord || u.ws.Cur != 1 {
		t.Fatalf("fourth F9: filter=%q cur=%d", u.netFilter, u.ws.Cur)
	}
}

// F9 with a single network changes nothing, silently.
func TestF9SingleNet(t *testing.T) {
	u := netUI(model.NetTelegram)
	u.key(term.Key{Code: term.F9})
	if u.cfg.Tabs || u.netFilter != "" || len(u.ws.List[0].Items) != 0 {
		t.Fatalf("single network: tabs=%v filter=%q items=%d", u.cfg.Tabs, u.netFilter, len(u.ws.List[0].Items))
	}
}

// In tab mode Ctrl+X and Alt+←/→ skip the windows of the other networks;
// Alt+N still reaches any window, and the tab follows it.
func TestTabsRestrictCycle(t *testing.T) {
	u := tabsUI()
	u.bindChat(u.ws.New(true), &model.Chat{Net: model.NetDiscord, ID: 9, Title: "discord-2"}) // window 3
	u.cfg.Tabs = true
	u.tabTo(model.NetDiscord) // window 1
	u.key(term.Key{Code: term.Ctrl, Rune: 'x'})
	if u.ws.Cur != 3 {
		t.Fatalf("Ctrl+X: cur=%d, want 3 (telegram window 2 skipped)", u.ws.Cur)
	}
	u.key(term.Key{Code: term.Ctrl, Rune: 'x'})
	if u.ws.Cur != 0 {
		t.Fatalf("Ctrl+X wrap: cur=%d, want 0", u.ws.Cur)
	}
	u.key(term.Key{Code: term.Left, Alt: true})
	if u.ws.Cur != 3 {
		t.Fatalf("Alt+Left: cur=%d, want 3", u.ws.Cur)
	}
	u.key(term.Key{Rune: '2', Alt: true})
	if u.ws.Cur != 2 || u.netFilter != model.NetTelegram {
		t.Fatalf("Alt+2: cur=%d filter=%q, want 2 telegram", u.ws.Cur, u.netFilter)
	}
}

// /set tabs on|off is the switch; /net <name> in tab mode moves to the tab.
func TestSetTabsAndNet(t *testing.T) {
	u := tabsUI()
	u.command("set", []string{"tabs", "on"}, "tabs on")
	if !u.cfg.Tabs || !u.tabsOn() {
		t.Fatal("/set tabs on: not on")
	}
	u.command("net", []string{model.NetTelegram}, model.NetTelegram)
	if u.netFilter != model.NetTelegram || u.ws.Cur != 2 {
		t.Fatalf("/net in tab mode: filter=%q cur=%d", u.netFilter, u.ws.Cur)
	}
	u.command("set", []string{"tabs", "off"}, "tabs off")
	if u.cfg.Tabs || u.tabsOn() {
		t.Fatal("/set tabs off: still on")
	}
}

// fakeIRC : an IRC-like backend that records the commands routed to it.
type fakeIRC struct {
	fakeBackend
	cmds []string // "name|room|reply|text"
}

func (f *fakeIRC) Command(_ context.Context, reply int64, room, name string, _ []string, text string) {
	f.cmds = append(f.cmds, fmt.Sprintf("%s|%s|%d|%s", name, room, reply, text))
}

func (f *fakeIRC) Members(c *model.Chat) []string {
	if c.Kind == model.ChatUser {
		return []string{c.Title}
	}
	return []string{"alice", "bob"}
}

// ircUI : one Telegram network and two IRC networks (so no single IRC network
// can be inferred), window 1 on the #go room of libera, window 2 on the
// Telegram chat. The fake returned is the one of libera.
func ircUI() (*UI, *fakeIRC) {
	u := netUI(model.NetTelegram)
	irc := &fakeIRC{}
	u.nets[model.IRCNet("libera")] = irc
	u.nets[model.IRCNet("oftc")] = &fakeIRC{}
	u.netList = []string{model.NetTelegram, model.IRCNet("libera"), model.IRCNet("oftc")}
	u.chats = map[model.ChatKey]*model.Chat{}
	room := &model.Chat{Net: model.IRCNet("libera"), ID: 77, Kind: model.ChatGroup, Title: "#go"}
	u.chats[room.Key()] = room
	u.bindChat(u.ws.New(true), room)
	u.chats[u.chatList[0].Key()] = u.chatList[0]
	u.bindChat(u.ws.New(true), u.chatList[0])
	return u, irc
}

// An IRC command typed in an IRC room goes to that network with the room and
// the window's chat as reply target; the same name outside an IRC context
// asks which network, and the completion only lists it inside one.
func TestIRCCommandRouting(t *testing.T) {
	u, irc := ircUI()
	u.goTo(1)
	name, args, text, ok := ParseCommand("/kick bob flood", u.commandNames())
	if !ok || name != "kick" {
		t.Fatalf("parse in IRC context: %q %v", name, ok)
	}
	u.command(name, args, text)
	if len(irc.cmds) != 1 || irc.cmds[0] != "kick|#go|77|bob flood" {
		t.Fatalf("routed: %v", irc.cmds)
	}
	if !slices.Contains(u.commandNames(), "/kick") {
		t.Fatal("completion: /kick missing in IRC context")
	}
	// The IRC names do not take the prefixes of the generic commands.
	if n, _, _, _ := ParseCommand("/i", u.commandNames()); n != "irc" {
		t.Fatalf("/i in IRC context: %q", n)
	}
	if n, _, _, _ := ParseCommand("/wh x", u.commandNames()); n != "whois" {
		t.Fatalf("/wh in IRC context: %q", n)
	}
	u.goTo(2) // Telegram window: no IRC context (two networks, none IRC in front)
	u.netFilter = ""
	if slices.Contains(u.commandNames(), "/kick") {
		t.Fatal("completion: /kick offered outside IRC context")
	}
	name, args, text, _ = ParseCommand("/kick bob", u.commandNames())
	u.command(name, args, text)
	if got := lastSys(u.view()); got != i18n.T("irc_which_net") {
		t.Fatalf("outside IRC: %q", got)
	}
	// The IRC tab makes the context: from window 0 the command reaches the network with no room.
	u.goTo(0)
	u.netFilter = model.IRCNet("libera")
	u.command("motd", nil, "")
	if len(irc.cmds) != 2 || irc.cmds[1] != "motd||0|" {
		t.Fatalf("from window 0 on the IRC tab: %v", irc.cmds)
	}
	// With no IRC network configured at all, an IRC name is simply unknown.
	u.netList, u.netFilter = []string{model.NetTelegram}, ""
	delete(u.nets, model.IRCNet("libera"))
	delete(u.nets, model.IRCNet("oftc"))
	u.command("kick", []string{"bob"}, "bob")
	if got := lastSys(u.view()); got != i18n.T("unknown_command", "kick") {
		t.Fatalf("no IRC network: %q", got)
	}
}

// Tab in an IRC room completes the arguments of the IRC commands: the members
// of the room, the rooms joined, the CTCP names; a nick opening a message
// addresses it.
func TestIRCArgCompletion(t *testing.T) {
	u, _ := ircUI()
	u.goTo(1)
	u.listChat(u.view().Chat) // the room is listed, as after a join
	for _, c := range []struct {
		line, word string
		atStart    bool
		want       string
	}{
		{"/kick b", "b", false, "bob"},
		{"/ctcp alice V", "V", false, "VERSION"},
		{"/part #", "#", false, "#go"},
		{"al", "al", true, "alice:"}, // the editor adds the space
	} {
		u.ed.Set(c.line)
		if got := u.candidates(c.word, c.atStart); !slices.Contains(got, c.want) {
			t.Fatalf("%q: %v, want %q", c.line, got, c.want)
		}
	}
	// Typing it whole: one candidate, one space.
	u.ed.Set("al")
	u.ed.Complete(u.candidates)
	if got := u.ed.String(); got != "alice: " {
		t.Fatalf("address: %q", got)
	}
	// A private chat completes its peer, the room of the window being one.
	carol := &model.Chat{Net: model.IRCNet("libera"), ID: 88, Kind: model.ChatUser, Title: "carol"}
	u.chats[carol.Key()] = carol
	u.bindChat(u.ws.New(true), carol)
	u.goTo(3)
	u.ed.Set("/ctcp c")
	if got := u.candidates("c", false); !slices.Contains(got, "carol") {
		t.Fatalf("private chat: %v", got)
	}
}

// A private IRC window has no room: a bare /part must not aim at the peer.
func TestIRCCommandPrivateNoRoom(t *testing.T) {
	u, irc := ircUI()
	carol := &model.Chat{Net: model.IRCNet("libera"), ID: 88, Kind: model.ChatUser, Title: "carol"}
	u.chats[carol.Key()] = carol
	u.bindChat(u.ws.New(true), carol)
	u.goTo(3)
	u.command("part", nil, "")
	if len(irc.cmds) != 1 || irc.cmds[0] != "part||88|" {
		t.Fatalf("private window: %v", irc.cmds)
	}
}

// EvLines lands in the window of its chat (activity counted when it is not
// shown), in window 0 for ChatID 0.
func TestEvLines(t *testing.T) {
	u, _ := ircUI()
	u.goTo(2)
	u.dispatch(model.Envelope{Net: model.IRCNet("libera"), Ev: model.EvLines{ChatID: 77, Lines: []string{"a", "b"}}})
	if w := u.ws.List[1]; lastSys(w) != "b" || w.Act != 1 {
		t.Fatalf("room window: last=%q act=%d", lastSys(w), w.Act)
	}
	u.dispatch(model.Envelope{Net: model.IRCNet("libera"), Ev: model.EvLines{Lines: []string{"a", "b", "motd"}}})
	if w := u.ws.List[0]; lastSys(w) != "motd" || w.Act != 1 { // one event, one activity
		t.Fatalf("window 0: last=%q act=%d", lastSys(w), w.Act)
	}
}

// A single IRC network makes window 0 an inferred IRC context: /whois looks
// the name up as a chat first, and only asks IRC for one no chat carries.
func TestIRCWhoisInferred(t *testing.T) {
	u, irc := ircUI()
	delete(u.nets, model.IRCNet("oftc"))
	u.netList = []string{model.NetTelegram, model.IRCNet("libera")}
	tg := u.nets[model.NetTelegram].(*fakeBackend)
	tg.caps = model.Caps{Whois: true}
	alice := &model.Chat{Net: model.NetTelegram, ID: 9, Kind: model.ChatUser, Title: "alice"}
	u.chatList = append(u.chatList, alice)
	u.chats[alice.Key()] = alice
	u.goTo(0)
	u.command("whois", []string{"alice"}, "alice")
	if tg.whois != 1 || len(irc.members) != 0 {
		t.Fatalf("the contact first: telegram=%d irc=%v", tg.whois, irc.members)
	}
	u.command("whois", []string{"ghost"}, "ghost")
	if !slices.Equal(irc.members, []string{"ghost"}) || tg.whois != 1 {
		t.Fatalf("a name no chat carries goes to IRC: %v telegram=%d", irc.members, tg.whois)
	}
}

// /whois <nick> in an anchored IRC context asks the network straight away,
// with no chat to find first.
func TestIRCWhoisNick(t *testing.T) {
	u, irc := ircUI()
	u.goTo(1)
	u.command("whois", []string{"ghost"}, "ghost")
	if !slices.Equal(irc.members, []string{"ghost"}) {
		t.Fatalf("WhoisMember: %v", irc.members)
	}
	// A single IRC network makes every window an IRC context: /whois in the
	// Telegram window still looks the contact up, and asks IRC nothing.
	u.netList = []string{model.NetTelegram, model.IRCNet("libera")}
	u.goTo(2)
	u.command("whois", []string{"@telegram-chat"}, "@telegram-chat")
	if !slices.Equal(irc.members, []string{"ghost"}) {
		t.Fatalf("WhoisMember from the Telegram window: %v", irc.members)
	}
}

type fakeAway struct {
	fakeBackend
	msgs []string
}

func (f *fakeAway) Away(_ context.Context, msg string) { f.msgs = append(f.msgs, msg) }

// /away reaches every network that can when no network is filtered, the
// filtered one only otherwise; a network that cannot says so; /away alone
// comes back.
func TestAwayScope(t *testing.T) {
	u := netUI(model.NetTelegram)
	d, i := &fakeAway{}, &fakeAway{}
	u.nets[model.NetDiscord], u.nets[model.IRCNet("libera")] = d, i
	u.command("away", []string{"not", "here"}, "not here")
	if !slices.Equal(d.msgs, []string{"not here"}) || !slices.Equal(i.msgs, []string{"not here"}) {
		t.Fatalf("all networks: discord=%v irc=%v", d.msgs, i.msgs)
	}
	if u.awayOf(nil) != "not here" || u.awayOf(&model.Chat{Net: model.NetTelegram}) != "" {
		t.Fatalf("awayOf: any=%q telegram=%q", u.awayOf(nil), u.awayOf(&model.Chat{Net: model.NetTelegram}))
	}
	u.netFilter = model.NetDiscord
	u.command("away", nil, "")
	if !slices.Equal(d.msgs, []string{"not here", ""}) || len(i.msgs) != 1 {
		t.Fatalf("filtered: discord=%v irc=%v", d.msgs, i.msgs)
	}
	if u.awayOf(&model.Chat{Net: model.NetDiscord}) != "" || u.awayOf(&model.Chat{Net: model.IRCNet("libera")}) != "not here" {
		t.Fatal("away state after coming back on discord only")
	}
	u.netFilter = model.NetTelegram
	u.command("away", []string{"x"}, "x")
	if got := lastSys(u.view()); got != i18n.T("net_unsupported", model.NetTelegram) {
		t.Fatalf("telegram: %q", got)
	}
}
