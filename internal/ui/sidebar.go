package ui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/clipperhouse/uax29/v2/graphemes"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// sideMode : content of the sidebar (F2 cycles them).
type sideMode int

const (
	sideHidden sideMode = iota
	sideChats
	sideWindows
)

const (
	sideMinW = 12 // floor of the drag: under that nothing can be read
	sideKeep = 20 // columns left to the messages, otherwise the sidebar would eat them
)

// clampSideW gives the width of the sidebar content, bounded to [12, cols/2].
func clampSideW(w, cols int) int { return max(sideMinW, min(w, cols/2)) }

// sideCol : first column of the message area (│ bar of the sidebar included).
func (u *UI) sideCol() int { return u.sideW + 1 }

// chatLess gives the comparator of a sort mode (F7, /set sidebar_sort) — the
// one ordering rule of the sidebar, shared by the chat mode (sortChats) and by
// the windows mode (sideWins).
// recent (default, unknown mode included): pinned first, then from the newest
// to the oldest — the old behaviour. alpha: pinned first (in entry order among
// them, not alphabetical), then by title (render.Fold, case and accents
// ignored). unread: unread first (count going down), then like recent (pinned
// first, then entry order). Ties give 0: with slices.SortStableFunc the caller
// keeps its own order.
func chatLess(mode string) func(a, b *model.Chat) int {
	less := func(a, b *model.Chat) int { // recent
		if a.Pinned != b.Pinned {
			if a.Pinned {
				return -1
			}
			return 1
		}
		return b.LastDate.Compare(a.LastDate)
	}
	switch mode {
	case "alpha":
		less = func(a, b *model.Chat) int {
			if a.Pinned != b.Pinned {
				if a.Pinned {
					return -1
				}
				return 1
			}
			if a.Pinned {
				return 0
			}
			return strings.Compare(render.Fold(a.Title), render.Fold(b.Title))
		}
	case "unread":
		less = func(a, b *model.Chat) int {
			if a.Unread != b.Unread {
				if a.Unread > b.Unread {
					return -1
				}
				return 1
			}
			if a.Pinned != b.Pinned { // tie: falls back to the recent order
				if a.Pinned {
					return -1
				}
				return 1
			}
			return 0
		}
	}
	return less
}

// sortChats gives a new slice sorted by mode. Whatever the mode, groupGuilds
// then brings the channels of one Discord guild back together.
func sortChats(list []*model.Chat, mode string) []*model.Chat {
	out := slices.Clone(list)
	slices.SortStableFunc(out, chatLess(mode))
	return groupGuilds(out)
}

// guildOf gives the guild of a Discord channel title ("Guild / #chan" ->
// "Guild"), "" for anything else — a DM, a group DM, another network.
func guildOf(c *model.Chat) string {
	if c.Net != model.NetDiscord {
		return ""
	}
	g, _, ok := strings.Cut(c.Title, " / #")
	if !ok {
		return ""
	}
	return g
}

// groupGuilds keeps the channels of one Discord guild next to each other
// whatever the primary sort: the guild takes the place of its best-ranked
// channel, and inside it the channels follow one another by title
// (render.Fold, like the alpha mode) — the same block in the same order at
// every sort, as an IRC client lists the channels of a server. Pinned
// channels make a group of their own: grouping never breaks the "pinned
// first" rule. Everything else keeps its place, so a list with no Discord
// guild in it comes out of the pass unchanged.
func groupGuilds(list []*model.Chat) []*model.Chat {
	type key struct {
		pinned bool
		guild  string
	}
	groups := map[key][]*model.Chat{}
	for _, c := range list {
		if g := guildOf(c); g != "" {
			k := key{c.Pinned, g}
			groups[k] = append(groups[k], c)
		}
	}
	if len(groups) == 0 { // no guild channel: nothing to move
		return list
	}
	for _, g := range groups {
		slices.SortStableFunc(g, func(a, b *model.Chat) int {
			return strings.Compare(render.Fold(a.Title), render.Fold(b.Title))
		})
	}
	out := make([]*model.Chat, 0, len(list))
	for _, c := range list {
		k := key{c.Pinned, guildOf(c)}
		if k.guild == "" {
			out = append(out, c)
			continue
		}
		if g := groups[k]; g != nil {
			out = append(out, g...)
			delete(groups, k) // the whole guild went in with its first channel
		}
	}
	return out
}

// sideRow : one line of the chat list of the sidebar — a section header
// (chat nil) or a chat. sec is the section key of BOTH: "" means the sidebar
// carries no section at all, and a chat row then draws exactly as it did
// before sections existed (network badge kept, guild prefix kept).
type sideRow struct {
	chat   *model.Chat // chat line; nil on a header line
	sec    string      // section key: "telegram", "discord", "discord:Gophers"
	name   string      // header: name shown ("telegram", "Gophers")
	unread int         // header, folded: unread of the chats it hides
	window bool        // header, folded: one of them has an open window
	folded bool        // header: its chats are hidden
}

// sectionOf gives the section key of a chat: its network, plus the guild for
// a Discord guild channel. Same key as the one written in sidebar.toml.
// Peer is opaque to the UI (protocols/dsc holds the guild id), so the name is
// what names a guild here.
func sectionOf(c *model.Chat) string {
	if g := guildOf(c); g != "" {
		return c.Net + ":" + g
	}
	return c.Net
}

// sectionName gives the header text of a section key — the guild name for
// "discord:Gophers", the network name otherwise.
func sectionName(sec string) string {
	if _, g, ok := strings.Cut(sec, ":"); ok {
		return g
	}
	return sec
}

// sectionRows turns the sorted, filtered chat list into the lines of the
// sidebar. Fewer than two sections (mono-Telegram with no guild, /net on one
// network) gives one row per chat and NO header: a single section is not a
// section. folded holds the folded keys; a nil map means "no section at all"
// — the state of every caller outside sideBlock (the tests, a UI built by
// hand with no fold state).
func sectionRows(chats []*model.Chat, ws []*Window, folded map[string]bool) []sideRow {
	plain := func() []sideRow {
		out := make([]sideRow, len(chats))
		for i, c := range chats {
			out[i] = sideRow{chat: c}
		}
		return out
	}
	if folded == nil {
		return plain()
	}
	type sect struct {
		key   string
		first int // rank of its first chat: guild order of groupGuilds
		chats []*model.Chat
	}
	seen := map[string]*sect{}
	var secs []*sect
	for i, c := range chats {
		k := sectionOf(c)
		s := seen[k]
		if s == nil {
			s = &sect{key: k, first: i}
			seen[k], secs = s, append(secs, s)
		}
		s.chats = append(s.chats, c)
	}
	if len(secs) < 2 {
		return plain()
	}
	slices.SortStableFunc(secs, func(a, b *sect) int {
		an, ag, _ := strings.Cut(a.key, ":")
		bn, bg, _ := strings.Cut(b.key, ":")
		if an != bn { // networks in name order, like the /net cycle
			return strings.Compare(an, bn)
		}
		if (ag == "") != (bg == "") { // the DMs of a network before its guilds
			if ag == "" {
				return -1
			}
			return 1
		}
		return a.first - b.first
	})
	out := make([]sideRow, 0, len(chats)+len(secs))
	for _, s := range secs {
		h := sideRow{sec: s.key, name: sectionName(s.key), folded: folded[s.key]}
		if h.folded { // the header answers for what it hides
			for _, c := range s.chats {
				h.unread += c.Unread
				h.window = h.window || hasWindow(ws, c.Key())
			}
		}
		out = append(out, h)
		if h.folded {
			continue
		}
		for _, c := range s.chats {
			out = append(out, sideRow{chat: c, sec: s.key})
		}
	}
	return out
}

// nextSort gives the next mode of the F7 cycle (recent -> alpha -> unread -> recent).
func nextSort(mode string) string {
	switch mode {
	case "alpha":
		return "unread"
	case "unread":
		return "recent"
	default:
		return "alpha"
	}
}

// sortLabel : mode label shown in the status bar ([sort:…]).
func sortLabel(mode string) string {
	switch mode {
	case "alpha":
		return i18n.T("sort_alpha")
	case "unread":
		return i18n.T("sort_unread")
	default:
		return i18n.T("sort_recent")
	}
}

// sortedChats gives u.chatList sorted by cfg.SidebarSort — the only sort point
// for the sidebar display (drawing, click, current line).
// ponytail: computed again at each call, no cache; ≤ 1000 chats, so the cost
// is nothing — cache it if it ever gets noticeable.
func (u *UI) sortedChats() []*model.Chat {
	return sortChats(u.chatList, u.cfg.SidebarSort)
}

// sideChats : list shown in the sidebar — sorted, then cut to the /net filter.
// The "new chat" overlay keeps sortedChats: it reaches every network whatever
// the filter.
func (u *UI) sideChats() []*model.Chat {
	sorted := u.sortedChats() // a fresh slice: DeleteFunc changes nothing shared
	if u.netFilter == "" {
		return sorted
	}
	return slices.DeleteFunc(sorted, func(c *model.Chat) bool { return c.Net != u.netFilter })
}

// sideRowList : the rows of the chat sidebar — the one source of the click, of
// the wheel and of sideReveal. sideBlock builds the same list from the chats it
// already sorted for the drawing.
// ponytail: computed again at each call like sortedChats, no cache; fine below
// 1000 chats, cache the rows of the frame if a profile ever says otherwise.
func (u *UI) sideRowList() []sideRow { return sectionRows(u.sideChats(), u.ws.List, u.folded) }

// foldSections gives the header rows of the sidebar, in the order it draws
// them — nothing at all when the panel carries no section (a single network
// with no guild, no fold state loaded). The list /fold works on.
func (u *UI) foldSections() []sideRow {
	var out []sideRow
	for _, r := range u.sideRowList() {
		if r.chat == nil {
			out = append(out, r)
		}
	}
	return out
}

// sectionKeys gives the keys of the section rows — what /fold takes and what
// Tab completes after it.
func sectionKeys(secs []sideRow) []string {
	var out []string
	for _, r := range secs {
		out = append(out, r.sec)
	}
	return out
}

// matchSection resolves the argument of /fold against the sections drawn: the
// exact key first ("discord:Gophers"), then the name shown by prefix ("goph"
// -> "Gophers"), case and accents apart (render.Fold). It gives "" and the
// candidates when several names start the same way — a name that resolves to
// nothing must never be written to sidebar.toml.
func matchSection(secs []sideRow, name string) (string, []string) {
	n := render.Fold(strings.TrimSpace(name))
	var hits []string
	for _, r := range secs {
		if render.Fold(r.sec) == n {
			return r.sec, nil // an exact key wins over any prefix
		}
		if strings.HasPrefix(render.Fold(r.name), n) {
			hits = append(hits, r.sec)
		}
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	return "", hits
}

// sideWins : indices of the windows drawn in windows mode — the /net filter
// cuts the ones bound to a chat of another network. A window bound to no chat
// (window 0, unbound) stays whatever the filter: it belongs to no network.
// The order follows the sort of the chat mode (F7, /set sidebar_sort) on the
// chat of each window: window 0 first, then the bound windows in that order,
// then the windows bound to nothing in index order. The numbers drawn stay the
// real window indices — only their order moves.
func (u *UI) sideWins() []int {
	wins := make([]int, 0, len(u.ws.List))
	for i, w := range u.ws.List {
		if u.netFilter == "" || w.Chat == nil || w.Chat.Net == u.netFilter {
			wins = append(wins, i)
		}
	}
	less := chatLess(u.cfg.SidebarSort)
	slices.SortStableFunc(wins, func(a, b int) int {
		if a == 0 || b == 0 { // window 0 stays at the head
			if a == 0 {
				return -1
			}
			return 1
		}
		ca, cb := u.ws.List[a].Chat, u.ws.List[b].Chat
		if ca == nil || cb == nil { // bound to nothing: at the end, index order
			if ca == nil && cb == nil {
				return 0
			}
			if ca == nil {
				return 1
			}
			return -1
		}
		return less(ca, cb)
	})
	return wins
}

// nextSide : F2 — hidden → chats → windows → hidden. In windows mode with
// several networks the /net filter comes first: windows of every network, then
// of each one in name order, and the wrap of that cycle hides the panel again.
// With a single network, the three plain stops.
func (u *UI) nextSide() {
	if u.side == sideWindows && u.multiNet() {
		if net := nextNet(u.netNames(), u.netFilter); net != "" {
			u.setNetFilter(net)
			return
		}
		u.setNetFilter("")
	}
	u.side, u.sideScroll = (u.side+1)%3, 0
}

// netSup : superscript small letters, a→z. "q" has no modifier letter, so it
// keeps its plain form.
var netSup = []rune("ᵃᵇᶜᵈᵉᶠᵍʰⁱʲᵏˡᵐⁿᵒᵖqʳˢᵗᵘᵛʷˣʸᶻ")

// netBadge : the network of a chat in one cell — its first letter in
// superscript ("telegram" → "ᵗ"). A space when the network is unknown: the
// column keeps its width.
func netBadge(net string) string {
	if net == "" {
		return " "
	}
	c := strings.ToLower(net)[0]
	if c < 'a' || c > 'z' {
		return " "
	}
	return string(netSup[c-'a'])
}

// sideOffset gives the first entry shown, a plain bound of the scroll asked
// for. The current line is brought back into the view by sideReveal, at a
// window change only: otherwise the wheel would be reset at each repaint.
func sideOffset(n, height, scroll int) int {
	if height < 1 || n <= height {
		return 0
	}
	return max(0, min(scroll, n-height))
}

// kindPrefix gives an ircii style marker for the kind of chat, in front of the
// title — # group/supergroup, & channel (broadcast), @ private chat (name).
func kindPrefix(k model.ChatKind) string {
	switch k {
	case model.ChatGroup:
		return "#"
	case model.ChatChannel:
		return "&"
	}
	return "@"
}

// sideRows gives the list lines of the sidebar — the last one of the chat mode
// is taken by "+ new message", outside the scroll.
func (u *UI) sideRows() int {
	if u.side == sideChats {
		return max(0, u.t.Rows-1-sideHdr)
	}
	return max(0, u.t.Rows-sideHdr)
}

// sideHdr : lines taken by the sidebar header (title + rule), above the list.
const sideHdr = 2

// cycleSort : F7 and the click on the header — next sort mode, scroll and
// marquee reset.
func (u *UI) cycleSort() {
	u.cfg.SidebarSort = nextSort(u.cfg.SidebarSort)
	u.sideScroll = 0
	u.marquee = marqueeState{}
	u.saveCfg()
}

// foldFile : TOML form of sidebar.toml — [folded] "<section key>" = true.
type foldFile struct {
	Folded map[string]bool `toml:"folded"`
}

// sidebarPath gives sidebar.toml, beside aliases.toml and config.toml
// (TTYLOOM_DIR included).
func sidebarPath() string { return filepath.Join(config.Dir(), "sidebar.toml") }

// loadFolds gives the folded sections. A missing file = nothing folded, not an
// error; a key at false (file edited by hand) is dropped, unfolded being the
// default. The map comes back non nil even on an error: the interface starts
// with everything unfolded, and a click can still fold.
func loadFolds(path string) (map[string]bool, error) {
	out := map[string]bool{}
	var f foldFile
	if _, err := toml.DecodeFile(path, &f); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return out, err
	}
	for k, v := range f.Folded {
		if v && k != "" {
			out[k] = true
		}
	}
	return out, nil
}

// saveFolds writes the whole file again: only the folded sections land in it,
// so an unfolded section (sideToggle drops its key) leaves the file instead of
// piling up in it.
func saveFolds(path string, folded map[string]bool) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(foldFile{Folded: folded}); err != nil {
		return err
	}
	return config.WriteAtomic(path, buf.Bytes(), 0o600)
}

// sideToggle folds or unfolds the section sec — click on its header, /fold.
// Single entry point of the fold: the scroll comes back inside the new length
// and the marquee is reset, like cycleSort.
func (u *UI) sideToggle(sec string) {
	if u.folded == nil {
		return // no fold state: the sidebar carries no section to fold
	}
	if u.folded[sec] {
		delete(u.folded, sec) // unfolded is the default: only the folded keys are kept
	} else {
		u.folded[sec] = true
	}
	u.sideWheel(0) // new length: the scroll bounds itself again
	u.marquee = marqueeState{}
	if err := saveFolds(sidebarPath(), u.folded); err != nil {
		u.sys(i18n.T("sidebar_error", err)) // the fold holds on the screen anyway
	}
}

// sideHeader gives the two header lines of the sidebar: the title of the mode
// with the sort between brackets — the sort rules both modes, and the line
// answers the click (cycleSort) in both — then a ─ rule. Same width+│ build as
// sidebarLines.
func sideHeader(mode sideMode, sort string, th theme.Theme, width int, hot bool) []render.Line {
	acc := theme.Style{FG: th.Color(theme.Accent), Bold: true}
	sep := render.Span{Text: "│", Style: th.Style(theme.Sep)}
	if hot {
		sep.Style = acc
	}
	title := i18n.T("sidebar_title_windows")
	if mode == sideChats {
		title = i18n.T("sidebar_title_chats")
	}
	title += " [" + sortLabel(sort) + "]"
	rule := render.Span{Text: strings.Repeat("─", max(0, width)), Style: th.Style(theme.Sep)}
	return []render.Line{
		{Spans: []render.Span{{Text: fit(title, width), Style: acc}, sep}},
		{Spans: []render.Span{rule, sep}},
	}
}

// sideNewLine : fixed "+ new message" line, last line of the sidebar in chat
// mode. It comes after the message separator (sepRow), which always falls
// higher: the sidebar runs over the whole height of the screen, and cutting it
// would cost three chat lines.
// ponytail: written by draw() rather than by sidebarLines — a fourteenth
// parameter on that signature is not worth it.
func sideNewLine(th theme.Theme, width int, hot bool) render.Line {
	acc := theme.Style{FG: th.Color(theme.Accent), Bold: true}
	sep := render.Span{Text: "│", Style: th.Style(theme.Sep)}
	if hot { // bar grabbed: same highlight as the rest of the sidebar
		sep.Style = acc
	}
	label := i18n.T("sidebar_new_message")
	if render.Width(label) > width { // narrow sidebar (floor of 12 columns)
		label = i18n.T("sidebar_new_message_short")
	}
	return render.Line{Spans: []render.Span{{Text: fit(label, width), Style: acc}, sep}}
}

// sideMenuChat : chat whose sidebar line stays highlighted while its context
// menu is open.
// ponytail: a package variable because sidebarLines is a pure function called
// by draw() with no access to the UI; only one goroutine touches the state of
// the UI. To be made a parameter the day the signature can change.
var sideMenuChat *model.Chat

// sideSections : folded sections handed to sidebarLines out of band. sideBlock
// posts u.folded here before each frame; it is nil everywhere else, and a nil
// map means "no section header at all" — what every direct caller of
// sidebarLines wants (the tests, and any UI with no fold state loaded).
// ponytail: package-level state keeps sidebarLines' signature; pass rows as a
// parameter if it grows.
var sideSections map[string]bool

// sideSecLine : spans of a section header — "── name ───────[-]" laid over the
// same mark and unread columns as the chat lines, so the titles stay aligned;
// folded, those two columns carry what the section hides ([·] and the unread
// total in red).
func sideSecLine(r sideRow, th theme.Theme, width, gut int) []render.Span {
	dim := th.Style(theme.Dim)
	mark, unread := "   ", "  "
	if r.window {
		mark = "[·]"
	}
	if r.unread > 0 {
		unread = fmt.Sprintf("%2d", min(r.unread, 99))
	}
	marker := "[-]"
	if r.folded {
		marker = "[+]"
	}
	textW := width - 5 - gut
	// The name is cut first: the marker and at least one ─ of rule always stay.
	head := render.Truncate("── "+render.CleanLine(r.name)+" ", max(0, textW-4), "")
	text := fit(head+strings.Repeat("─", max(1, textW-render.Width(head)-3))+marker, textW)
	var sp []render.Span
	if gut > 0 {
		sp = append(sp, render.Span{Text: strings.Repeat(" ", gut)})
	}
	return append(sp, render.Span{Text: mark, Style: dim},
		render.Span{Text: unread, Style: theme.Style{FG: th.Color(theme.Error), Bold: true}},
		render.Span{Text: text, Style: th.Style(theme.Sep)})
}

// sideTitle gives the title drawn on a chat row. Under the header of its own
// guild, a channel drops the "Guild / #" the header already names
// ("Gophers / #general" -> "general", the # of kindPrefix taking its place);
// with no section (r.sec == "") the title is the one of before, untouched.
func sideTitle(r sideRow, name func(*model.Chat) string) string {
	t := render.CleanLine(chatTitle(r.chat, name))
	if strings.Contains(r.sec, ":") { // guild section: its name is on the header
		if _, chn, ok := strings.Cut(t, " / #"); ok {
			return chn
		}
	}
	return t
}

// sidebarLines draws the sidebar: height lines of width columns followed by
// the separator. cur = index of the current window in ws. avatars: gutter of 3
// cells at the head of the chat lines, with Line.Avatar carrying the chat id.
// wins: indices of ws drawn in windows mode (the /net filter cuts the others),
// nil = every window.
// sepRow: line (0-based) of the message separator line, -1 when it is missing;
// on that line the vertical bar of the sidebar becomes ├. hot: bar grabbed
// with the mouse (resize running) — it goes to the accent colour. title: shown
// name of a chat (u.title, local names included); nil = Telegram title. nets:
// one badge cell for the network in front of each chat title (multi-network only).
func sidebarLines(mode sideMode, chats []*model.Chat, ws []*Window, wins []int, cur int, th theme.Theme,
	width, height, scroll int, avatars, nets bool, step int, sepRow int, hot bool, title func(*model.Chat) string) []render.Line {
	dim := th.Style(theme.Dim)
	acc := theme.Style{FG: th.Color(theme.Accent), Bold: true}
	red := theme.Style{FG: th.Color(theme.Error), Bold: true} // unread badge
	sep := render.Span{Text: "│", Style: th.Style(theme.Sep)}
	if hot { // bar grabbed: highlight while the drag lasts
		sep.Style = acc
	}
	// Current line: one single style on every column, otherwise each span
	// inverts on a different background and the bar looks patchy.
	on := theme.Style{FG: th.FG, Reverse: true}

	gut := 0
	if avatars {
		gut = 3
	}
	// Only the height rows on the screen are formatted: with hundreds of chats
	// the sidebar cost more than the messages, for lines nobody saw.
	var n int
	var row func(k int) ([]render.Span, int64)
	switch mode {
	case sideChats:
		var curChat *model.Chat
		if cur >= 0 && cur < len(ws) {
			curChat = ws[cur].Chat
		}
		// Rows, not chats: sectionRows adds the headers when sections are on
		// (sideSections not nil, two sections at least) and gives one row per
		// chat otherwise — the drawing of before, untouched.
		secs := sectionRows(chats, ws, sideSections)
		n = len(secs)
		row = func(k int) ([]render.Span, int64) {
			r := secs[k]
			if r.chat == nil { // section header
				return sideSecLine(r, th, width, gut), 0
			}
			c := r.chat
			mark, unread := "   ", "  "
			if hasWindow(ws, c.Key()) {
				mark = "[·]"
			}
			if c.Unread > 0 {
				unread = fmt.Sprintf("%2d", min(c.Unread, 99))
			}
			pfx := kindPrefix(c.Kind)
			label := " " + sideTitle(r, title)
			m, u, t := dim, red, theme.Style{}
			isCur := c == curChat
			if isCur || c == sideMenuChat { // current line, or line of the open menu
				m, u, t = on, on, on
				u.Bold = true
			}
			var sp []render.Span
			id := int64(0)
			if avatars { // same style as the rest of the line: the bar stays in one piece
				sp, id = append(sp, render.Span{Text: "   ", Style: t}), c.ID
			}
			badge := ""
			if nets && r.sec == "" { // under a header the network is already named
				badge = netBadge(c.Net)
			}
			// pfx and badge are outside the scrolling string: they stay fixed, only
			// the title scrolls.
			textW := width - 5 - gut - render.Width(pfx) - render.Width(badge)
			text := fit(label, textW)
			if isCur { // only the current line scrolls
				text = marquee(label, textW, step)
			}
			sp = append(sp, render.Span{Text: mark, Style: m}, render.Span{Text: unread, Style: u}, render.Span{Text: pfx, Style: m})
			if badge != "" {
				sp = append(sp, render.Span{Text: badge, Style: m})
			}
			return append(sp, render.Span{Text: text, Style: t}), id
		}
	case sideWindows:
		if wins == nil {
			for i := range ws {
				wins = append(wins, i)
			}
		}
		n = len(wins)
		row = func(k int) ([]render.Span, int64) {
			i := wins[k]
			w := ws[i]
			pfx := ""
			if w.Chat == nil && w.Search == "" { // status window: ircii style marker
				pfx = "*"
			} else if w.Chat != nil && w.Search == "" { // no prefix on "?search"
				pfx = kindPrefix(w.Chat.Kind)
			}
			s := fmt.Sprintf("%d: %s", i, render.CleanLine(winName(w, title)))
			if w.Act > 0 {
				s += fmt.Sprintf(" (%d)", w.Act)
			}
			textW := width - render.Width(pfx)
			st, p, text := theme.Style{}, dim, fit(s, textW)
			switch {
			case i == cur:
				st, p, text = on, on, marquee(s, textW, step)
			case w.Chat != nil && w.Chat == sideMenuChat: // line of the open menu
				st, p = on, on
			}
			return []render.Span{{Text: pfx, Style: p}, {Text: text, Style: st}}, 0
		}
	}

	off := sideOffset(n, height, scroll)
	blank := strings.Repeat(" ", max(0, width))
	out := make([]render.Line, 0, max(0, height))
	for i := 0; i < height; i++ {
		spans, id := []render.Span{{Text: blank}}, int64(0)
		if k := off + i; k < n {
			spans, id = row(k)
		}
		s := sep
		if i == sepRow {
			s = render.Span{Text: "├", Style: sep.Style}
		}
		out = append(out, render.Line{Spans: append(spans, s), Avatar: id})
	}
	return out
}

// sideBlock : the whole sidebar of a frame — header, list, "+ new message"
// line — and the rows it was drawn from, headers included, which the avatar
// loop of draw() reads again line by line. sepRow: line of the message
// separator, -1 when there is none.
func (u *UI) sideBlock(sepRow int) ([]render.Line, []sideRow) {
	sorted := u.sideChats() // sorted and filtered once: one sort per frame
	hot := u.sideHot()
	side := sideHeader(u.side, u.cfg.SidebarSort, u.th, u.sideW, hot)
	sideSections = u.folded // sections of this frame only: nil again right after
	side = append(side, sidebarLines(u.side, sorted, u.ws.List, u.sideWins(), u.ws.Cur, u.th, u.sideW, u.sideRows(),
		u.sideScroll, u.avatarsOn(), u.multiNet(), u.marquee.step, sepRow, hot, u.title)...)
	sideSections = nil
	if u.side == sideChats { // last line, outside the scroll
		side = append(side, sideNewLine(u.th, u.sideW, hot))
	}
	return side, sectionRows(sorted, u.ws.List, u.folded)
}

// sideHot : the │ bar of the panel goes to the accent colour — while it is
// dragged to resize, and while the pointer sits over the panel (follow-mouse).
func (u *UI) sideHot() bool { return u.drag == dragSide || u.zone == zoneSide }

// fit cuts or fills s to w columns.
func fit(s string, w int) string {
	s = render.Truncate(s, w, "")
	return s + strings.Repeat(" ", max(0, w-render.Width(s)))
}

// marqueeClusters gives title cut into graphemes. Unit of the marquee shift
// and cut: never a cluster (ZWJ emoji, vs16…) cut in two.
func marqueeClusters(title string) []string {
	var cl []string
	g := graphemes.FromString(title)
	for g.Next() {
		cl = append(cl, g.Value())
	}
	return cl
}

// marqueeMax gives the last useful shift of marquee(title, width, ·). Above
// that, the window already shows the whole end of the title: going further
// would change only the filling, never the content shown.
func marqueeMax(title string, width int) int {
	if render.Width(title) <= width {
		return 0
	}
	cl := marqueeClusters(title)
	w := 0
	for i := len(cl) - 1; i >= 0; i-- {
		w += render.Width(cl[i])
		if w > width {
			return i + 1
		}
	}
	return 0
}

// marquee gives the scrolling title, a window of width cells from the grapheme
// step. title already fits in width → unchanged (fit). Otherwise: never a cut
// in the middle of a cluster, and a cluster that no longer fits whole leaves
// filling rather than being cut; step capped at marqueeMax.
func marquee(title string, width, step int) string {
	if render.Width(title) <= width {
		return fit(title, width)
	}
	if m := marqueeMax(title, width); step > m {
		step = m
	}
	if step < 0 {
		step = 0
	}
	cl := marqueeClusters(title)
	var b strings.Builder
	w := 0
	for _, c := range cl[step:] {
		cw := render.Width(c)
		if w+cw > width {
			break
		}
		b.WriteString(c)
		w += cw
	}
	b.WriteString(strings.Repeat(" ", width-w))
	return b.String()
}

// curRowTitle gives the title and the width available of the line currently
// selected in the sidebar (current chat in chat mode, window cur in window
// mode) — same build as the matching line in sidebarLines. ok=false outside the
// chat/window sidebar, with no current window (window 0 with no bound chat), or
// when a folded section hides the line of the current chat.
func curRowTitle(mode sideMode, rows []sideRow, ws []*Window, cur, w int, avatars, nets bool, name func(*model.Chat) string) (title string, width int, ok bool) {
	if cur < 0 || cur >= len(ws) {
		return "", 0, false
	}
	gut := 0
	if avatars {
		gut = 3
	}
	switch mode {
	case sideChats:
		c := ws[cur].Chat
		if c == nil {
			return "", 0, false
		}
		// The row of that chat, or nothing: a fold can hide it, and the marquee
		// would then scroll a string nobody draws.
		i := slices.IndexFunc(rows, func(x sideRow) bool { return x.chat == c })
		if i < 0 {
			return "", 0, false
		}
		badge := 0
		if nets && rows[i].sec == "" {
			badge = render.Width(netBadge(c.Net))
		}
		return " " + sideTitle(rows[i], name), w - 5 - gut - render.Width(kindPrefix(c.Kind)) - badge, true
	case sideWindows:
		s := fmt.Sprintf("%d: %s", cur, render.CleanLine(winName(ws[cur], name)))
		if ws[cur].Act > 0 {
			s += fmt.Sprintf(" (%d)", ws[cur].Act)
		}
		pfx := ""
		if ws[cur].Chat == nil && ws[cur].Search == "" { // status window: * marker, like sidebarLines
			pfx = "*"
		} else if ws[cur].Chat != nil && ws[cur].Search == "" {
			pfx = kindPrefix(ws[cur].Chat.Kind)
		}
		return s, w - render.Width(pfx), true
	}
	return "", 0, false
}

// marqueeState : scrolling of the title of the current sidebar line. next:
// time of the next step (rate 300 ms). pause: 300 ms ticks left to wait at the
// ends before going on. title/width: last line seen, to catch a change
// (window, sidebar mode, avatars on/off during a session…) without listing
// every caller that touches ws.Cur or u.side — one shift would slip through
// otherwise (attach() opens a window without going through goTo).
type marqueeState struct {
	step, pause int
	next        time.Time
	title       string
	width       int
}

// marqueeTick moves the title of the current line on or pauses it, at a rate
// of 300 ms — apart from the real period of the ticker of tick() (100 ms, not
// 150: a counter was dropped for a timestamp, which does not care about that
// setting). true when the shift shown changed (repaint worth it).
func (u *UI) marqueeTick(now time.Time) bool {
	var rows []sideRow
	// ponytail: one sort per 100 ms tick with the chat sidebar open (≤ 1000
	// chats); cache the rows of the frame if a profile ever shows it.
	if u.side == sideChats {
		rows = u.sideRowList()
	}
	title, width, ok := curRowTitle(u.side, rows, u.ws.List, u.ws.Cur, u.sideW, u.avatarsOn(), u.multiNet(), u.title)
	if !ok {
		title, width = "", 0
	}
	if title != u.marquee.title || width != u.marquee.width {
		u.marquee = marqueeState{title: title, width: width}
	}
	if !ok || render.Width(title) <= width || now.Before(u.marquee.next) {
		return false
	}
	u.marquee.next = now.Add(300 * time.Millisecond)
	if u.marquee.pause > 0 {
		u.marquee.pause--
		return false
	}
	maxStep := marqueeMax(title, width)
	prev := u.marquee.step
	if u.marquee.step >= maxStep {
		u.marquee.step = 0
	} else {
		u.marquee.step++
	}
	if u.marquee.step == 0 || u.marquee.step == maxStep {
		u.marquee.pause = 5 // 5 * 300 ms = 1.5 s at the ends
	}
	return u.marquee.step != prev
}

func hasWindow(ws []*Window, k model.ChatKey) bool {
	for _, w := range ws {
		if w.Chat != nil && w.Chat.Key() == k {
			return true
		}
	}
	return false
}

// layout gives the first column of the message area (0 = no sidebar) and its
// width. The last column of the screen is kept for the scrollbar, bar shown or
// not: the wrap width thus never changes and nothing is to be wrapped again
// when the bar shows up.
func (u *UI) layout() (x0, width int) {
	if u.side == sideHidden || u.t.Cols < u.sideCol()+sideKeep {
		return 0, max(1, u.t.Cols-1)
	}
	return u.sideCol(), max(1, u.t.Cols-u.sideCol()-1)
}

func (u *UI) width() int { _, w := u.layout(); return w }

// viewRows gives the height of the message area — one line less with the
// separator line above the status bar (/set separator), and less again when
// the expanded input zone is open.
func (u *UI) viewRows() int {
	v := u.t.Rows - 1 - u.inputRows()
	if u.cfg.Separator {
		v--
	}
	return v
}

// inputRows : height of the input zone. One line, or in the expanded editor
// one screen line per line of the draft, between 3 and half the screen. The
// search takes the line for its query: one line again.
func (u *UI) inputRows() int {
	if !u.multi || u.search != nil {
		return 1
	}
	n := strings.Count(u.ed.String(), "\n") + 1
	return max(min(max(n, 3), u.t.Rows/2), 1)
}

// sideDragTo : the width of the sidebar follows the mouse (x = column aimed at
// for the bar). The reflow is at once: the message area changes width.
func (u *UI) sideDragTo(x int) {
	if w := clampSideW(x, u.t.Cols); w != u.sideW {
		u.sideW = w
		u.clear()
	}
}

// sideDragEnd : release — the width reached becomes the one of the config.
func (u *UI) sideDragEnd() {
	u.drag = dragNone
	if u.sideW == u.cfg.SidebarWidth {
		return // click with no move: nothing to save
	}
	u.cfg.SidebarWidth = u.sideW
	u.saveCfg()
}

// sideMouse : wheel and click in the sidebar.
func (u *UI) sideMouse(m term.MouseEvent) {
	switch m.Button {
	case 64, 65:
		d := 1
		if m.Button == 64 {
			d = -1
		}
		// Zone read at the event, never u.zone: that one is what the last move
		// painted and a clear() may have dropped it. Off the follow-mouse zone
		// (hover off, overlay on top), the wheel scrolls the list as before.
		if u.zoneOf(m.X, m.Y) != zoneSide {
			u.sideWheel(d) // one row a notch: term already folds the bursts of the terminal
			return
		}
		u.sideStep(d)
	case 0:
		u.sideClick(m.Y)
	case 2:
		u.openMenu(m.X, m.Y) // context menu of the line, anchored at the click
	}
}

// sideStep goes to the previous (d < 0) or the next (d > 0) window of the
// sidebar as it is drawn — the wheel over the panel (follow-mouse). It never
// opens a window: a notch must cost no network. In chat mode the steps are
// therefore only the rows whose chat already has a window; a section header
// carries no chat, a folded row is not drawn any more, and a chat never opened
// is walked past; a search window on that chat is not a step either (ForChat
// skips them, as openChat does). In windows mode the steps are the drawn
// windows (/net filter, F7 sort). The ends clamp, and a current window that is not one of
// the steps has the wheel enter the list by the end it comes from. goTo calls
// sideReveal: the list follows on its own.
func (u *UI) sideStep(d int) {
	var wins []int
	if u.side == sideWindows {
		wins = u.sideWins()
	} else {
		for _, r := range u.sideRowList() {
			if r.chat == nil {
				continue // section header: no chat, no step
			}
			if i := u.ws.ForChat(r.chat.Key()); i >= 0 {
				wins = append(wins, i)
			}
		}
	}
	if j := stepIdx(slices.Index(wins, u.ws.Cur), d, len(wins)); j >= 0 && wins[j] != u.ws.Cur {
		u.goTo(wins[j])
	}
}

// stepIdx : rank i moved by d, bounded to [0, n-1] — clamp, never a wrap.
// i < 0 (nothing current, or the current row not in the list) enters the list
// by the end the wheel comes from. n == 0 gives -1: nothing to go to.
func stepIdx(i, d, n int) int {
	if n == 0 {
		return -1
	}
	if i < 0 {
		if d < 0 {
			return n - 1
		}
		return 0
	}
	return min(max(i+d, 0), n-1)
}

func (u *UI) sideWheel(d int) {
	n := len(u.sideRowList()) // rows, not chats: a header scrolls, a folded line is gone
	if u.side == sideWindows {
		n = len(u.sideWins())
	}
	u.sideScroll = max(0, min(u.sideScroll+d, max(0, n-u.sideRows())))
}

// sideReveal brings the line of the current window back into the view of the
// sidebar: called at window changes, never at a repaint.
func (u *UI) sideReveal() {
	i := -1
	switch u.side {
	case sideChats:
		if c := u.ws.Current().Chat; c != nil { // no chat: no line, never the first header
			i = slices.IndexFunc(u.sideRowList(), func(r sideRow) bool { return r.chat == c })
		}
	case sideWindows:
		i = slices.Index(u.sideWins(), u.ws.Cur) // filtered out: no line to bring back
	}
	if i < 0 {
		return
	}
	if i < u.sideScroll {
		u.sideScroll = i
	}
	if i >= u.sideScroll+u.sideRows() {
		u.sideScroll = i - u.sideRows() + 1
	}
}

// sideClick opens the window of the line y of the sidebar; the last line of
// the chat mode opens "new chat".
func (u *UI) sideClick(y int) {
	if y < 0 || y >= u.t.Rows {
		return
	}
	if u.side == sideChats && y == u.t.Rows-1 {
		u.openNewChat()
		return
	}
	if y < sideHdr { // header: the title line cycles the sort, in both modes
		if y == 0 {
			u.cycleSort()
		}
		return
	}
	switch u.side {
	case sideChats:
		switch r, ok := u.sideRowIn(u.sideRowList(), y); {
		case !ok:
		case r.chat != nil:
			u.openChat(r.chat)
		default:
			u.sideToggle(r.sec) // header line: it folds its section, it opens nothing
		}
	case sideWindows:
		wins := u.sideWins()
		if i := sideOffset(len(wins), u.sideRows(), u.sideScroll) + y - sideHdr; i < len(wins) {
			u.goTo(wins[i])
		}
	}
}

// sideChatAt gives the chat carried by the line y of the sidebar, nil when it
// carries none (empty line, "+ new message" line, unbound window, hidden sidebar).
func (u *UI) sideChatAt(y int) *model.Chat {
	return u.sideChatIn(u.sideRowList(), y)
}

// sideRowIn gives the row drawn on the screen line y, ok=false when the line
// carries none (panel header, empty line, "+ new message" line, window mode).
func (u *UI) sideRowIn(rows []sideRow, y int) (sideRow, bool) {
	y -= sideHdr // screen row -> list line
	if u.side != sideChats || y < 0 || y >= u.sideRows() {
		return sideRow{}, false
	}
	if i := sideOffset(len(rows), u.sideRows(), u.sideScroll) + y; i < len(rows) {
		return rows[i], true
	}
	return sideRow{}, false
}

// sideChatIn : same, on the rows of the sidebar already built. draw() holds
// them and would otherwise sort the whole list again on every avatar line. A
// section header carries no chat.
func (u *UI) sideChatIn(rows []sideRow, y int) *model.Chat {
	if u.side == sideChats {
		r, ok := u.sideRowIn(rows, y)
		if !ok {
			return nil
		}
		return r.chat
	}
	y -= sideHdr // screen row -> list line
	if u.side != sideWindows || y < 0 || y >= u.sideRows() {
		return nil
	}
	wins := u.sideWins()
	if i := sideOffset(len(wins), u.sideRows(), u.sideScroll) + y; i < len(wins) {
		return u.ws.List[wins[i]].Chat
	}
	return nil
}
