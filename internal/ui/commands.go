package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/theme"
)

var aliases = map[string]string{
	"w": "window", "win": "window", "q": "query", "qu": "query", "j": "join", "m": "msg",
	"hist": "history", "t": "theme", "o": "open", "c": "clear", "h": "help", "exit": "quit",
}

var commandNames = []string{"/window", "/close", "/query", "/join", "/new", "/msg", "/me", "/chats", "/net", "/fold", "/history",
	"/search", "/whois", "/rename", "/unrename", "/open", "/view", "/send", "/theme", "/set", "/clear", "/log", "/debug", "/emoji", "/gif", "/help", "/quit",
	"/telegram", "/discord"}

// ParseCommand : "/win new hide" → ("window", [new hide], "new hide", true).
// "//x" → text "/x"; with no slash → text as it is, ok=false.
func ParseCommand(line string) (name string, args []string, text string, ok bool) {
	if strings.HasPrefix(line, "//") {
		return "", nil, line[1:], false
	}
	if !strings.HasPrefix(line, "/") {
		return "", nil, line, false
	}
	name, text, _ = strings.Cut(line[1:], " ")
	name = strings.ToLower(name)
	name = resolveCommand(name)
	text = strings.TrimSpace(text)
	return name, strings.Fields(text), text, true
}

// resolveCommand keeps aliases exact, then accepts a command prefix when it
// names one command only. /qu is an explicit alias: query and quit would
// otherwise both match it. Ambiguous prefixes stay available for Tab cycling.
func resolveCommand(name string) string {
	if a, ok := aliases[name]; ok {
		return a
	}
	if slices.Contains(commandNames, "/"+name) {
		return name
	}
	var match string
	for _, command := range commandNames {
		candidate := strings.TrimPrefix(command, "/")
		if candidate == name {
			return candidate
		}
		if strings.HasPrefix(candidate, name) {
			if match != "" {
				return name // ambiguous: keep the original for the error message
			}
			match = candidate
		}
	}
	if match != "" {
		return match
	}
	return name
}

// onOff reads a boolean of /set. Anything else is false; the echo that
// follows shows the value kept.
func onOff(v string) bool { return v == "on" || v == "true" || v == "1" }

func (u *UI) command(name string, args []string, text string) {
	w := u.view()
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	switch name {
	case "window":
		switch arg(0) {
		case "new":
			u.ws.New(arg(1) == "hide")
			if arg(1) != "hide" {
				u.goTo(u.ws.Cur)
			}
		case "close":
			u.closeWindow()
		case "", "list":
			sys := u.th.Style(theme.System)
			var lines []render.Line
			for i, x := range u.ws.List {
				s := fmt.Sprintf("%d: %s", i, u.winName(x))
				if x.Act > 0 {
					s += fmt.Sprintf(" (%d)", x.Act)
				}
				lines = append(lines, render.Plain("*** "+s, sys, u.width())...)
			}
			u.emit(w, lines)
		default:
			u.switchTo(arg(0))
		}
	case "close":
		u.closeWindow()
	case "query", "join":
		u.query(w, text, name == "join")
	case "new":
		u.openNewChat()
	case "msg":
		if len(args) < 2 {
			w.AddSys(i18n.T("usage_msg"))
			return
		}
		c, _ := u.findChat(arg(0))
		if c == nil {
			w.AddSys(i18n.T("unknown_name", arg(0)))
			return
		}
		body := strings.TrimSpace(strings.TrimPrefix(text, arg(0)))
		u.send(u.winFor(c), body) // always keep the message in its own conversation too
	case "me":
		if text == "" {
			w.AddSys(i18n.T("usage_me"))
			return
		}
		u.sendMe(u.sendWin(), text)
	case "chats":
		if u.botOnly() {
			w.AddSys(i18n.T("bot_unavailable"))
			return
		}
		u.listChats()
	case "net":
		u.netCmd(w, arg(0))
	case model.NetTelegram, model.NetDiscord:
		u.netAction(w, name, strings.ToLower(arg(0)))
	case "fold":
		u.foldCmd(w, text)
	case "history":
		if u.botOnly() {
			w.AddSys(i18n.T("bot_unavailable"))
			return
		}
		if w.Chat == nil {
			w.AddSys(i18n.T("window_unbound"))
			return
		}
		if u.selfOf(w.Chat.Net).Bot {
			w.AddSys(i18n.T("bot_unavailable"))
			return
		}
		if w.Search != "" {
			w.AddSys(i18n.T("search_window_unavailable"))
			return
		}
		n, err := strconv.Atoi(arg(0))
		if err != nil || n < 1 {
			n = 50
		}
		b := u.net(w.Chat)
		if b == nil {
			return
		}
		w.Loading = true
		b.LoadHistory(u.ctx, w.Chat, w.OldestID(), n)
	case "search":
		if u.botOnly() {
			w.AddSys(i18n.T("bot_unavailable"))
			return
		}
		if w.Chat == nil {
			w.AddSys(i18n.T("window_unbound"))
			return
		}
		if u.selfOf(w.Chat.Net).Bot {
			w.AddSys(i18n.T("bot_unavailable"))
			return
		}
		if text == "" {
			w.AddSys(i18n.T("usage_search"))
			return
		}
		if !u.caps(w.Chat).Search {
			w.AddSys(i18n.T("net_unsupported", w.Chat.Net))
			return
		}
		if b := u.net(w.Chat); b != nil {
			w.AddSys(i18n.T("searching_for", text))
			b.Search(u.ctx, w.Chat, text, searchLimit)
		}
	case "whois":
		c := w.Chat
		if arg(0) != "" {
			var ambiguous bool
			if c, ambiguous = u.findChat(arg(0)); c == nil {
				if !ambiguous {
					w.AddSys(i18n.T("unknown_name", arg(0)))
				}
				return
			}
		}
		if c == nil {
			w.AddSys(i18n.T("usage_whois"))
			return
		}
		if c.Kind != model.ChatUser {
			w.AddSys(i18n.T("users_only"))
			return
		}
		if !u.caps(c).Whois {
			w.AddSys(i18n.T("net_unsupported", c.Net))
			return
		}
		if b := u.net(c); b != nil {
			b.Whois(u.ctx, c)
		}
	case "rename":
		u.rename(w, text)
	case "unrename":
		u.unrename(w, text) // the whole line: a target can hold several words
	case "open":
		n, err := strconv.Atoi(arg(0))
		if err != nil || n < 1 {
			n = 1
		}
		u.openMedia(w, n)
	case "view":
		n, err := strconv.Atoi(arg(0))
		if err != nil || n < 1 {
			n = 1
		}
		u.viewNth(w, n)
	case "send":
		if text == "" {
			w.AddSys(i18n.T("usage_send"))
			return
		}
		u.sendCmd(u.sendWin(), text)
	case "theme":
		u.themeCmd(args)
	case "set":
		u.setCmd(args)
	case "clear":
		if w != u.agg { // shared media: freeing them would break the chat windows
			u.freeImages(w)
		}
		w.Items, w.Sel, w.Scroll = nil, nil, 0
	case "log":
		if w.Chat == nil {
			w.AddSys(i18n.T("window_not_bound"))
			return
		}
		switch arg(0) {
		case "on":
			w.Log = true
		case "off":
			w.Log = false
		case "":
			w.Log = !w.Log
		default:
			w.AddSys(i18n.T("usage_log"))
			return
		}
		w.AddSys(fmt.Sprintf("log = %v", w.Log))
	case "debug":
		u.setDebug(!u.showDebug)
		w = u.view() // the view may have changed: the answer goes where the user looks
		w.AddSys(fmt.Sprintf("debug = %v", u.showDebug))
	case "emoji":
		u.openPicker(u.view().Chat, func(s string) { u.ed.Insert(s) })
	case "gif":
		u.openGifs(text)
	case "help":
		if arg(0) == "" {
			u.emit(w, helpLines(u.width()))
		} else {
			u.emit(w, helpTopic(arg(0), u.width()))
		}
	case "quit":
		u.cancel()
	default:
		// "/5", "/21": an all-digit name is the ircii shorthand of "/window N".
		// Out of range, goTo answers with one system line.
		if n, err := strconv.Atoi(name); err == nil && n >= 0 {
			u.goTo(n)
			return
		}
		w.AddSys(i18n.T("unknown_command", name))
	}
}

func (u *UI) closeWindow() {
	w := u.ws.Close()
	if w == nil {
		u.sys(i18n.T("window0_no_close"))
		return
	}
	u.freeImages(w)
	u.goTo(u.ws.Cur)
}

func (u *UI) switchTo(s string) {
	if n, err := strconv.Atoi(s); err == nil {
		u.goTo(n)
		return
	}
	if i := u.ws.ByName(s, u.title); i >= 0 {
		u.goTo(i)
		return
	}
	u.sys(i18n.T("no_window_named", s))
}

// bind opens a resolved conversation in its own window (member picker).
func (u *UI) bind(w *Window, name string, join bool) {
	if w == u.ws.List[0] || w == u.agg || w == u.debug {
		w.AddSys(i18n.T("window0_no_bind"))
		return
	}
	if c, ambiguous := u.findChat(name); c != nil {
		u.attach(w, c)
		return
	} else if ambiguous {
		return
	}
	// Only the networks that can resolve are asked: one that cannot would
	// answer at once with its refusal and take the pending window away from
	// the lookup that is still running. The list is made before anything is
	// said, so a network that cannot resolve never shows a "resolving…" it
	// would take back on the next line.
	var resolvers []model.Backend
	for _, b := range u.nets {
		if b.Caps().Resolve {
			resolvers = append(resolvers, b)
		}
	}
	if len(resolvers) == 0 {
		w.AddSys(i18n.T("net_unsupported", strings.Join(u.netNames(), ", ")))
		return
	}
	w.AddSys(i18n.T("resolving", name))
	u.pending[name] = w
	// ponytail: u.pending holds one window, so the first answer binds it and
	// the later ones find nothing waiting — and a "not found" from another
	// network then posts resolve_failed (chatResolved, w == nil) over a lookup
	// that worked. Fine while a single network resolves; count the answers,
	// and a chooser, when there are two.
	for _, b := range resolvers {
		b.Resolve(u.ctx, name, join)
	}
}

// netAll : the /net argument that lifts the filter, and the last stop of its cycle.
const netAll = "all"

// netCmd : /net — with no argument it cycles all → the networks in name order
// → all, a name filters at once, "all" lifts the filter. With a single backend
// it names the network in place and changes nothing: the badge, the filter and
// the status segment stay away.
func (u *UI) netCmd(w *Window, name string) {
	names := u.netNames()
	if !u.multiNet() {
		w.AddSys(i18n.T("net_single", strings.Join(names, ", ")))
		return
	}
	name = strings.ToLower(name)
	switch {
	case name == "":
		u.setNetFilter(nextNet(names, u.netFilter))
	case name == netAll:
		u.setNetFilter("")
	case slices.Contains(names, name):
		u.setNetFilter(name)
	default:
		w.AddSys(i18n.T("net_unknown", name, strings.Join(append(names, netAll), ", ")))
		return
	}
	w.AddSys(i18n.T("net_filter", cmp.Or(u.netFilter, netAll)))
}

// cycleNet : the /net cycle on a key (Shift+F2), with the same status line.
// Inert and silent with a single network: /net answers net_single there, a key
// must not fill the window with a line nobody asked for.
func (u *UI) cycleNet() {
	if u.multiNet() {
		u.netCmd(u.view(), "")
	}
}

// foldCmd : /fold [section] — folds or unfolds a section of the sidebar, the
// keyboard twin of the click on its header, as F7 is the twin of the click on
// the sort line. With no argument it lists the sections and their state.
// The name is resolved against the sections drawn before anything moves: a
// typo must not leave a dead key behind in sidebar.toml. Silent when it works,
// like F7: the sidebar shows the result.
func (u *UI) foldCmd(w *Window, name string) {
	secs := u.foldSections()
	if len(secs) == 0 {
		w.AddSys(i18n.T("fold_none"))
		return
	}
	if name == "" {
		list := make([]string, len(secs))
		for i, r := range secs {
			mark := " [-]"
			if r.folded {
				mark = " [+]"
			}
			list[i] = r.sec + mark
		}
		w.AddSys(i18n.T("fold_list", strings.Join(list, ", ")))
		return
	}
	switch sec, hits := matchSection(secs, name); {
	case sec != "":
		u.sideToggle(sec)
	case len(hits) > 0:
		w.AddSys(i18n.T("fold_ambiguous", name, strings.Join(hits, ", ")))
	default:
		w.AddSys(i18n.T("fold_unknown", name, strings.Join(sectionKeys(secs), ", ")))
	}
}

// listChats : lines drawn ahead, so render.Clean is explicit on the remote text.
func (u *UI) listChats() {
	acc := theme.Style{FG: u.th.Color(theme.Accent), Bold: true}
	dim := u.th.Style(theme.Dim)
	var lines []render.Line
	for _, c := range u.chatList {
		var spans []render.Span
		unread := "   "
		if c.Unread > 0 {
			unread = fmt.Sprintf("%3d", c.Unread)
		}
		st := theme.Style{}
		if c.Unread > 0 {
			st = acc
		}
		spans = append(spans, render.Span{Text: unread + " ", Style: dim}, render.Span{Text: render.CleanLine(u.title(c)), Style: st})
		if c.Username != "" {
			spans = append(spans, render.Span{Text: " @" + render.CleanLine(c.Username), Style: dim})
		}
		if i := u.ws.ForChat(c.Key()); i >= 0 {
			spans = append(spans, render.Span{Text: fmt.Sprintf(" [win %d]", i), Style: dim})
		}
		lines = append(lines, render.Line{Spans: spans})
	}
	u.goTo(0) // before the emit: in aggregated mode, the view of window 0 is the aggregate
	u.emit(u.view(), lines)
}

func (u *UI) themeCmd(args []string) {
	w := u.view()
	if len(args) == 0 { // with no argument: picker on top
		u.openThemePicker()
		return
	}
	if args[0] == "list" {
		filter := ""
		if len(args) > 1 {
			filter = strings.ToLower(strings.Join(args[1:], " "))
		}
		sys := u.th.Style(theme.System)
		n := 0
		var lines []render.Line
		for _, name := range theme.Names() {
			if filter == "" || strings.Contains(strings.ToLower(name), filter) {
				lines = append(lines, render.Plain("*** "+name, sys, u.width())...)
				n++
			}
		}
		lines = append(lines, render.Plain(i18n.T("theme_list_footer", n, u.th.Name), sys, u.width())...)
		u.goTo(0)
		u.emit(u.view(), lines)
		return
	}
	th, err := theme.Load(strings.Join(args, " "))
	if err != nil {
		w.AddSys(err.Error())
		return
	}
	u.th = th
	u.cfg.Theme = th.Name
	u.saveCfg()
	u.clear()
}

func (u *UI) setCmd(args []string) {
	w := u.view()
	show := func(k string) {
		switch k {
		case "timestamps":
			w.AddSys(fmt.Sprintf("timestamps = %v", u.cfg.Timestamps))
		case "timestamps_seconds":
			w.AddSys(fmt.Sprintf("timestamps_seconds = %v", u.cfg.TimestampsSeconds))
		case "cycle_mode":
			w.AddSys("cycle_mode = " + u.cfg.CycleMode)
		case "multiline":
			w.AddSys(fmt.Sprintf("multiline = %v", u.cfg.Multiline))
		case "link_previews":
			w.AddSys(fmt.Sprintf("link_previews = %v", u.cfg.LinkPreviews))
		case "maps":
			w.AddSys(fmt.Sprintf("maps = %v", u.cfg.Maps))
		case "hover":
			w.AddSys(fmt.Sprintf("hover = %v", u.cfg.Hover))
		case "images":
			w.AddSys(i18n.T("set_images_effective", u.cfg.Images, u.images))
		case "images_hover":
			w.AddSys(fmt.Sprintf("images_hover = %v", u.cfg.ImagesHover))
		case "video":
			w.AddSys("video = " + u.cfg.Video)
		case "avatars":
			w.AddSys(i18n.T("set_avatars_effective", u.cfg.Avatars, u.avatarsOn()))
		case "auto_media_max_kb":
			w.AddSys(fmt.Sprintf("auto_media_max_kb = %d", u.cfg.AutoMediaMaxKB))
		case "download_dir":
			w.AddSys("download_dir = " + u.cfg.DownloadDir)
		case "bell":
			w.AddSys(fmt.Sprintf("bell = %v", u.cfg.Bell))
		case "notify":
			w.AddSys("notify = " + u.cfg.Notify)
		case "auto_open_days":
			w.AddSys(fmt.Sprintf("auto_open_days = %d", u.cfg.AutoOpenDays))
		case "aggregate":
			w.AddSys(fmt.Sprintf("aggregate = %v", u.aggregate))
		case "log":
			w.AddSys(fmt.Sprintf("log = %v", u.cfg.Log))
		case "log_dir":
			w.AddSys("log_dir = " + u.cfg.LogDir)
		case "separator":
			w.AddSys(fmt.Sprintf("separator = %v", u.cfg.Separator))
		case "redline":
			w.AddSys(fmt.Sprintf("redline = %v", u.cfg.Redline))
		case "spell":
			w.AddSys("spell = " + u.cfg.Spell)
		case "spell_quotes":
			w.AddSys(fmt.Sprintf("spell_quotes = %v", u.cfg.SpellQuotes))
		case "sidebar_sort":
			w.AddSys("sidebar_sort = " + u.cfg.SidebarSort)
		case "sidebar_width":
			w.AddSys(fmt.Sprintf("sidebar_width = %d", u.cfg.SidebarWidth))
		case "kitty_images":
			w.AddSys(fmt.Sprintf("kitty_images = %d", u.cfg.KittyImages))
		case "cache_messages":
			w.AddSys(i18n.T("set_cache_messages", u.cfg.CacheMessages))
		case "lang":
			w.AddSys("lang = " + i18n.Lang())
		default: // "/set foobar" alone was mute
			w.AddSys(i18n.T("unknown_key", k))
		}
	}
	if len(args) == 0 {
		for _, k := range setKeys {
			show(k)
		}
		return
	}
	if len(args) == 1 {
		show(args[0])
		return
	}
	k, v := args[0], strings.Join(args[1:], " ")
	switch k {
	case "timestamps":
		u.cfg.Timestamps = onOff(v)
		u.clear()
	case "timestamps_seconds":
		u.cfg.TimestampsSeconds = onOff(v)
		u.clear() // the prefix changes width: everything is wrapped again
	case "cycle_mode":
		if v != "next" && v != "last_unread" {
			w.AddSys(i18n.T("set_cycle_mode_values"))
			return
		}
		u.cfg.CycleMode = v
		u.cycleHome = nil
	case "multiline":
		u.cfg.Multiline = onOff(v)
		if !u.cfg.Multiline {
			u.multi = false
		}
	case "link_previews":
		u.cfg.LinkPreviews = onOff(v)
		u.clear() // the "│" block shows or goes: lines to draw again
	case "maps":
		u.cfg.Maps = onOff(v)
		u.reloadMedia() // turned on: the shown locations move to downloading
	case "hover":
		if v == "on" { // old alias: on = menu
			v = "menu"
		}
		if v != "menu" && v != "highlight" && v != "off" {
			w.AddSys(i18n.T("set_hover_values"))
			return
		}
		u.cfg.Hover = config.HoverMode(v)
		u.hover.Invalidate() // the drawing (background/help) depends on the mode
		// Zone dropped at once: "off" must leave nothing lit, and the mode just
		// changed under a pointer that is not moving. zoneAt recomputes it at
		// the next move.
		u.zone = zoneNone
		if u.cfg.Hover == config.HoverOff {
			u.hover = nil
		}
	case "images":
		if v != "auto" && v != "kitty" && v != "halfblock" && v != "off" {
			w.AddSys(i18n.T("set_images_values"))
			return
		}
		u.applyImages(v)
	case "images_hover":
		u.cfg.ImagesHover = onOff(v)
		u.clear() // the lines kept free show or go
	case "video":
		if v != "show" && v != "hidden" && v != "autoplay" {
			w.AddSys(i18n.T("set_video_values"))
			return
		}
		u.cfg.Video = v
		u.reloadMedia() // the lines kept free and the frames decoded both change
	case "avatars":
		u.cfg.Avatars = onOff(v)
		u.clear() // the gutter shows or goes: lines to draw again
	case "auto_media_max_kb":
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > config.MaxAutoMediaKB {
			w.AddSys(i18n.T("number_expected_kb", config.MaxAutoMediaKB))
			return
		}
		u.cfg.AutoMediaMaxKB = n
	case "download_dir":
		u.cfg.DownloadDir = v
	case "bell":
		u.cfg.Bell = onOff(v)
	case "notify":
		if v != "terminal" && v != "desktop" && v != "off" {
			w.AddSys(i18n.T("set_notify_values"))
			return
		}
		u.cfg.Notify = v
	case "auto_open_days":
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			w.AddSys(i18n.T("number_expected_days"))
			return
		}
		u.cfg.AutoOpenDays = n
	case "aggregate":
		u.setAggregate(onOff(v))
		w = u.view() // the view may have changed: show() writes where the user looks
	case "log":
		u.cfg.Log = onOff(v)
		u.ws.Log = u.cfg.Log
	case "log_dir":
		u.cfg.LogDir = v
	case "separator":
		u.cfg.Separator = onOff(v)
		u.clear() // the separator line shows or goes: the view changes height
	case "redline":
		u.cfg.Redline = onOff(v)
		u.clear() // the redline shows or goes: the view changes height
	case "spell":
		if err := u.applySpell(v); err != nil { // resolved against spell.DictDir
			return // refused mode: told there, nothing saved
		}
		u.cfg.Spell = v
	case "spell_quotes":
		u.cfg.SpellQuotes = onOff(v)
		u.spellDirty()
	case "sidebar_sort":
		if v != "recent" && v != "alpha" && v != "unread" {
			w.AddSys(i18n.T("set_sidebar_sort_values"))
			return
		}
		u.cfg.SidebarSort = v
		u.sideScroll = 0
		u.marquee = marqueeState{}
	case "sidebar_width":
		n, err := strconv.Atoi(v)
		if err != nil {
			w.AddSys(i18n.T("number_expected_width"))
			return
		}
		u.sideW = clampSideW(n, u.t.Cols)
		u.cfg.SidebarWidth = u.sideW
		u.clear() // the message area changes width: everything is wrapped again
	case "kitty_images":
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			w.AddSys(i18n.T("number_expected_positive"))
			return
		}
		u.cfg.KittyImages = n
	case "cache_messages":
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			w.AddSys(i18n.T("number_expected_positive"))
			return
		}
		u.cfg.CacheMessages = n
		u.setMaxItems(n) // the memory of the windows follows at once
	case "lang":
		langs := i18n.Langs()
		for _, l := range strings.Split(v, "+") { // "fr+en": fallback chain
			if !slices.Contains(langs, l) {
				w.AddSys(i18n.T("lang_unknown", l, strings.Join(langs, ", ")))
				return
			}
		}
		u.cfg.Lang = v
		u.relang(v)
		u.clear() // the drawn texts change language
	default:
		w.AddSys(i18n.T("unknown_key", k))
		return
	}
	u.saveCfg()
	show(k)
}

// presenceKeys : the presence texts with no argument — the ones relang can
// bring into the new language on its own.
var presenceKeys = []string{"presence_online", "presence_idle", "presence_dnd",
	"presence_recently", "presence_last_week", "presence_last_month"}

// relang switches the language and brings the i18n texts already stored back
// into it: the presence of each peer and the cache mark of the windows were
// translated when they were built, and nothing would refresh them before the
// next event of the network — the mark is worse than stale, it is looked up by
// its text and could never be dropped again. Each text is recognised by its
// own value in the language on its way out, read just before the switch: no
// key kept in the state, no reverse lookup in the tables.
// ponytail: "seen <when>" (presence_seen) is a format whose argument is
// translated too, so it is left as it is and waits for the next presence
// update; carry the raw status in model.EvPresence the day that matters.
func (u *UI) relang(lang string) {
	was := make(map[string]string, len(presenceKeys)) // text on its way out -> key
	for _, k := range presenceKeys {
		was[i18n.T(k)] = k
	}
	mark := cacheMark()

	i18n.Set(lang)

	for k, p := range u.presence {
		if key, ok := was[p]; ok {
			u.presence[k] = i18n.T(key)
		}
	}
	for _, w := range u.ws.List { // bindChat only marks the windows of the list
		for _, it := range w.Items {
			switch {
			case it.Sys == mark:
				it.Sys = cacheMark()
				it.Invalidate()
			case it.gap != [2]int{}: // hole marker, recognised by its boundaries
				it.Sys = i18n.T("history_gap")
				it.Invalidate()
			}
		}
	}
}
