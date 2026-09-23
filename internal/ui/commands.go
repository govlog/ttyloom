package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/theme"
)

var aliases = map[string]string{
	"w": "window", "win": "window", "q": "query", "qu": "query", "j": "join", "m": "msg",
	"hist": "history", "t": "theme", "o": "open", "c": "clear", "h": "help", "exit": "quit",
}

var commandNames = []string{"/window", "/close", "/query", "/join", "/new", "/msg", "/me", "/away", "/chats", "/net", "/fold", "/history",
	"/search", "/whois", "/rename", "/unrename", "/open", "/view", "/send", "/theme", "/set", "/clear", "/log", "/debug", "/emoji", "/gif", "/help", "/quit",
	"/telegram", "/discord", "/irc", "/dcc"}

// cmdNames : the commands valid now. general resolve first by prefix, then
// context — the ones a network adds in its own windows (IRC's /kick), which
// must not take /i from /irc nor /wh from /whois. module names the commands
// a module runs, whatever their layer.
type cmdNames struct {
	general, context []string
	module           map[string]bool // "/fk"
}

func (n cmdNames) all() []string { return append(slices.Clone(n.general), n.context...) }

// ParseCommand : "/win new hide" → ("window", [new hide], "new hide", true).
// "//x" → text "/x"; with no slash → text as it is, ok=false.
func ParseCommand(line string, names cmdNames) (name string, args []string, text string, ok bool) {
	if strings.HasPrefix(line, "//") {
		return "", nil, line[1:], false
	}
	if !strings.HasPrefix(line, "/") {
		return "", nil, line, false
	}
	name, text, _ = strings.Cut(line[1:], " ")
	name = strings.ToLower(name)
	name = resolveCommand(name, names)
	text = strings.TrimSpace(text)
	return name, strings.Fields(text), text, true
}

// resolveCommand keeps aliases exact, then accepts a command prefix when it
// names one command only. /qu is an explicit alias: query and quit would
// otherwise both match it. Ambiguous prefixes stay available for Tab cycling.
//
// The prefix is read in layers: the generic commands first, the IRC names an
// IRC context adds to them second. Without that, entering a room would take
// /i (irc) away to invite and ignore, and /wh (whois) to who and whowas.
func resolveCommand(name string, names cmdNames) string {
	if a, ok := aliases[name]; ok {
		return a
	}
	if slices.Contains(names.all(), "/"+name) {
		return name
	}
	if m := uniquePrefix(name, names.general); m != "" {
		return m
	}
	return cmp.Or(uniquePrefix(name, names.context), name) // ambiguous: the original, for the error message
}

// uniquePrefix : the only command of names that name starts, "" when none or
// several do.
func uniquePrefix(name string, names []string) string {
	match := ""
	for _, command := range names {
		candidate := strings.TrimPrefix(command, "/")
		if !strings.HasPrefix(candidate, name) {
			continue
		}
		if match != "" {
			return ""
		}
		match = candidate
	}
	return match
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
	if u.runModCommand(w, name, args, text) {
		return
	}
	// The IRC commands need a network: the one of the window, of the tab, or
	// the only one configured. With none configured at all they stay unknown.
	if isIRCCommand(name) && len(u.ircNets()) > 0 {
		net := u.ircNetFor(w)
		if net == "" {
			w.AddSys(i18n.T("irc_which_net"))
			return
		}
		u.ircCommand(w, net, name, args, text)
		return
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
		c, _ := u.findChat(arg(0), true)
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
	case "away":
		nets := u.netNames()
		if u.netFilter != "" {
			nets = []string{u.netFilter}
		}
		var done []string
		for _, n := range nets {
			a, ok := u.nets[n].(awayer)
			if !ok {
				if u.netFilter != "" {
					u.netUnsupported(n)
				}
				continue
			}
			a.Away(u.netContext(n), text)
			u.setAway(n, text)
			done = append(done, n)
		}
		switch {
		case len(done) == 0 && u.netFilter == "":
			w.AddSys(i18n.T("net_unsupported", strings.Join(nets, ", ")))
		case len(done) > 0 && text == "":
			w.AddSys(i18n.T("away_back", strings.Join(done, ", ")))
		case len(done) > 0:
			w.AddSys(i18n.T("away_set", text, strings.Join(done, ", ")))
		}
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
	case "irc":
		u.ircCmd(w, args)
	case "dcc":
		u.dccCmd(w, args, text)
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
		b.LoadHistory(u.backendContext(b), w.Chat, w.OldestID(), n)
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
			b.Search(u.backendContext(b), w.Chat, text, searchLimit)
		}
	case "whois":
		// On IRC a nick needs no chat: the network answers on the token. The
		// context has to be anchored though — the window itself or its tab
		// names the network. Inferred from a single IRC network configured
		// (window 0, no IRC tab), the name is looked up as a chat first: it is
		// a contact of another network more often than an IRC nick.
		irc := u.ircNetFor(w)
		if arg(0) == "" || !winOn(w, irc) { // a window on another network: a chat
			irc = ""
		}
		toIRC := irc != "" && (w.Chat != nil || w.Target != nil || model.IRCName(u.netFilter) != "")
		c := w.Chat
		if !toIRC && arg(0) != "" {
			var ambiguous bool
			if c, ambiguous = u.findChat(arg(0), false); c == nil {
				toIRC = irc != "" && !ambiguous // no chat of that name: a nick
				if !toIRC {
					if !ambiguous {
						w.AddSys(i18n.T("unknown_name", arg(0)))
					}
					return
				}
			}
		}
		if toIRC {
			if b := u.nets[irc]; b != nil {
				b.WhoisMember(u.netContext(irc), strings.TrimPrefix(arg(0), "@"))
			}
			return
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
			b.Whois(u.backendContext(b), c)
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
			u.emit(w, helpLines(u.topics(), u.sections(), u.width()))
		} else {
			u.emit(w, helpTopic(u.topics(), arg(0), u.width()))
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
	if u.ws.Cur == 0 {
		u.sys(i18n.T("window0_no_close"))
		return
	}
	if u.closeWindowAt(u.ws.Cur) == nil {
		return
	}
	u.goTo(u.ws.Cur)
}

func (u *UI) closeWindowAt(i int) *Window {
	if i <= 0 || i >= len(u.ws.List) {
		return nil
	}
	w := u.ws.List[i]
	if w.Chat != nil && w.Search == "" && u.dirty[w.Chat.Key()] {
		u.bgWait.Wait()
		if cc := u.cacheFor(w.Chat.Net); cc != nil {
			if err := saveMerged(cc, w.Chat.ID, w.Msgs()); err != nil {
				u.status0(i18n.T("cache_error", err))
				return nil
			}
		}
		delete(u.dirty, w.Chat.Key())
	}
	u.freeImages(w)
	return u.ws.CloseAt(i)
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
	if c, ambiguous := u.findChat(name, false); c != nil {
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
	resolvers := u.resolversFor(w, name)
	if len(resolvers) == 0 {
		w.AddSys(i18n.T("net_unsupported", strings.Join(u.netNames(), ", ")))
		return
	}
	w.AddSys(i18n.T("resolving", name))
	u.resolve(w, name, join, resolvers, true, "")
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
		u.applyNet(nextNet(names, u.netFilter))
	case name == netAll:
		u.applyNet("")
	case slices.Contains(names, name):
		u.applyNet(name)
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
