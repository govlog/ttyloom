package ui

import (
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// Ctrl+F search in the shown window: the typing filters live, the hits are
// highlighted when drawn and the view moves to the current one.

// hl : one hit — line of the window drawing, visual columns.
type hl struct{ line, col0, col1 int }

type searchState struct {
	q    []rune
	hits []hl
	cur  int  // current hit, -1 when there is none
	show bool // next draw: move the view to hits[cur]
}

// foldRunes gives the text folded rune by rune, with for each folded rune the
// index of the first rune. Folding changes the number of runes (a lone accent
// is dropped): without this table, the positions found no longer point at the
// text shown.
func foldRunes(s string) (folded []rune, orig []int) {
	for i, r := range []rune(s) {
		if r < utf8.RuneSelf { // ASCII: neither diacritic nor composed case
			folded, orig = append(folded, unicode.ToLower(r)), append(orig, i)
			continue
		}
		for _, f := range render.Fold(string(r)) {
			folded = append(folded, f)
			orig = append(orig, i)
		}
	}
	return folded, orig
}

// occurrences gives the ranges [start, end) in runes of each hit of q in s,
// folded comparison, with no overlap.
func occurrences(s, q string) [][2]int {
	fq := []rune(render.Fold(q))
	if len(fq) == 0 {
		return nil
	}
	fs, orig := foldRunes(s)
	n := len([]rune(s))
	var out [][2]int
	for i := 0; i+len(fq) <= len(fs); i++ {
		if !slices.Equal(fs[i:i+len(fq)], fq) {
			continue
		}
		end := n
		if j := i + len(fq); j < len(orig) {
			end = orig[j]
		}
		out = append(out, [2]int{orig[i], end})
		i += len(fq) - 1
	}
	return out
}

// searchScan : lines swept by matchLines, the newest ones. A message often
// draws several lines (wrap, images, previews), so the cap is well above
// the default item cap; hard so that cache_messages cannot raise the cost
// of a keystroke (~135 ms worst case, only while the search prompt is open).
const searchScan = 10000

// matchLines gives the hits of q in the drawn lines, from top to bottom.
// ponytail: full sweep at each keystroke, ~27 ms on searchScan lines; index it
// if that cap goes up.
func matchLines(lines []render.Line, q string) []hl {
	var out []hl
	off := max(0, len(lines)-searchScan)
	for i, l := range lines[off:] {
		text := render.LineText(l)
		runes := []rune(text)
		for _, o := range occurrences(text, q) {
			out = append(out, hl{line: off + i, col0: render.Width(string(runes[:o[0]])),
				col1: render.Width(string(runes[:o[1]]))})
		}
	}
	return out
}

// highlight gives a copy of l where each hit of q carries st. Never in place:
// the lines come from the drawing cache of the items.
// ponytail: membership tested hit by hit for each rune; a line is at most as
// wide as the terminal.
func highlight(l render.Line, q string, st theme.Style) render.Line {
	occ := occurrences(render.LineText(l), q)
	if len(occ) == 0 {
		return l
	}
	inside := func(i int) bool {
		for _, o := range occ {
			if i >= o[0] && i < o[1] {
				return true
			}
		}
		return false
	}
	out := make([]render.Span, 0, len(l.Spans)+2*len(occ))
	i := 0
	for _, sp := range l.Spans {
		for _, r := range sp.Text {
			style := sp.Style
			if inside(i) {
				style = st
				style.URL = sp.Style.URL // the OSC 8 link survives the highlight
			}
			if n := len(out); n > 0 && out[n-1].Style == style {
				out[n-1].Text += string(r)
			} else {
				out = append(out, render.Span{Text: string(r), Style: style})
			}
			i++
		}
	}
	return render.Line{Spans: out, Img: l.Img}
}

// searchFind computes the hits again on the current drawing of the view and
// goes to the last one (the newest).
// ponytail: computed again only on a keystroke; a message that came since
// shifts the lines until the next keystroke, and the view stays readable.
func (u *UI) searchFind() {
	s := u.search
	s.hits = matchLines(u.view().Lines(u.opts()), string(s.q))
	s.cur = len(s.hits) - 1
	s.show = s.cur >= 0
}

// searchStep : previous hit (dir < 0) or next one, in a loop.
func (u *UI) searchStep(dir int) {
	s := u.search
	if len(s.hits) == 0 {
		return
	}
	s.cur = (s.cur + dir + len(s.hits)) % len(s.hits)
	s.show = true
}

// searchKey handles one key during the search. false: to be handled as usual
// (mouse: wheel and click stay free).
func (u *UI) searchKey(k term.Key) bool {
	s := u.search
	switch {
	case k.Code == term.Mouse:
		return false
	case k.Code == term.Esc, k.Code == term.Ctrl && k.Rune == 'c':
		u.search = nil
	case k.Code == term.Enter:
		u.searchStep(-1)
	case k.Code == term.Ctrl && k.Rune == 'f': // 2nd Ctrl+F: the same query, on the server side
		u.searchGlobalOpen()
	case k.Code == term.Ctrl && k.Rune == 'n':
		u.searchStep(1)
	case k.Code == term.Backspace:
		if len(s.q) > 0 {
			s.q = s.q[:len(s.q)-1]
			u.searchFind()
		}
	case k.Code == term.None && k.Rune != 0 && !k.Alt:
		s.q = append(s.q, k.Rune)
		u.searchFind()
	}
	return true
}

// --- server search (/search) ---

// searchLimit : results brought back at most by /search.
const searchLimit = 50

// reuseMsgs gives the messages of the result, using again the *model.Msg of
// the first window when it already has that id. Edit, reaction and delete then
// act on the same message in both views.
func reuseMsgs(origin *Window, msgs []model.Msg) []*model.Msg {
	have := make(map[int]*model.Msg, len(origin.Items))
	for _, it := range origin.Items {
		if it.Msg != nil && it.Msg.ID != 0 {
			have[it.Msg.ID] = it.Msg
		}
	}
	out := make([]*model.Msg, len(msgs))
	for i := range msgs {
		if m := have[msgs[i].ID]; m != nil {
			out[i] = m
			continue
		}
		out[i] = &msgs[i]
	}
	return out
}

// searchResult : answer of /search. The result goes into a "?text" window
// bound to the same chat, already complete (Loaded, Full): no network history,
// no cache replay and no read receipt.
func (u *UI) searchResult(e model.EvSearch) {
	i := u.ws.ForChat(u.evKey(e.ChatID))
	if i < 0 {
		return // window closed meanwhile
	}
	origin := u.ws.List[i]
	switch {
	case e.Err != "":
		origin.AddSys(i18n.T("search_error", e.Err))
		return
	case len(e.Msgs) == 0:
		origin.AddSys(i18n.T("no_result"))
		return
	}
	w := u.ws.New(true)
	w.Search, w.Loaded, w.Full = e.Query, true, true
	u.bindChat(w, origin.Chat)
	msgs := reuseMsgs(origin, e.Msgs)
	w.Merge(msgs)
	for _, m := range msgs {
		u.autoMedia(m, false)
	}
	u.goTo(len(u.ws.List) - 1)
}

// --- global search (Ctrl+F twice) ---

// Second Ctrl+F: the local query goes to the server (messages.searchGlobal)
// and the results show in a centred overlay, one line per message. Enter opens
// the chat and settles there; a 3rd Ctrl+F comes back to the local one.

const (
	gsLimit = 50                     // results brought back at most
	gsDelay = 300 * time.Millisecond // typing settled before the query starts again
	gsChatW = 20                     // columns of the [#room] cell
	gsFromW = 14                     // columns of the <author> cell
)

type globalSearch struct {
	query    []rune
	hits     []model.SearchHit
	cur      int
	scroll   int               // first result shown
	inflight string            // query of the call running; "" = none (only one at a time)
	pending  int               // networks still to answer the query in flight
	acc      []model.SearchHit // their hits, shown once they are all in
	errs     []string          // their failures, shown when nothing answered
	sent     string            // query of the results shown
	typed    time.Time         // last keystroke; zero = nothing to start again
	err      string
}

// move : bounded index, the scroll follows the current one.
func (g *globalSearch) move(d, rows int) {
	g.cur = max(0, min(g.cur+d, len(g.hits)-1))
	g.scroll = min(g.scroll, g.cur)
	if g.cur >= g.scroll+rows {
		g.scroll = g.cur - rows + 1
	}
	g.scroll = max(0, min(g.scroll, max(0, len(g.hits)-rows)))
}

// gsRect : box of the overlay, wide and centred.
func (u *UI) gsRect() rect {
	w := max(24, min(u.t.Cols-8, 100))
	h := max(6, min(u.t.Rows-6, 24))
	return centerRect(u.t.Cols, u.t.Rows, w, h)
}

// gsRows : result lines shown (borders, header and foot taken away).
func gsRows(h int) int { return max(1, h-4) }

// snippet gives a window of w columns in s, framed on the first hit of q (with
// no hit: the start). The drawn width is exactly w as soon as s is wider;
// "…" marks each cut end.
func snippet(s, q string, w int) string {
	s = render.CleanLine(s)
	if w <= 0 {
		return ""
	}
	if render.Width(s) <= w {
		return s
	}
	r := []rune(s)
	start := 0
	if occ := occurrences(s, q); len(occ) > 0 {
		start = max(0, occ[0][0]-w/4) // a quarter of context before the hit
	}
	// The end of the text would fit in the window: go back rather than leave a blank.
	for start > 0 && render.Width(string(r[start:])) < w {
		start--
	}
	out := string(r[start:])
	if start > 0 {
		out = "…" + out
	}
	return render.Truncate(out, w, "…")
}

// Lines : the whole box, one render.Line per screen line; w and h are its
// size, borders included. title gives the local name of a chat (/rename).
func (g *globalSearch) Lines(th theme.Theme, w, h int, title func(*model.Chat) string) []render.Line {
	box, edge, sel := boxStyles(th)
	dim, acc := box, box
	dim.FG, acc.FG = th.Color(theme.Dim), th.Color(theme.Accent)
	inner, rows := max(1, w-2), gsRows(h)
	b := boxDraw{edge: edge, fill: box, inner: inner}
	head := i18n.T("gsearch_head", render.CleanLine(string(g.query)))
	switch {
	case g.err != "":
		head += "  (" + g.err + ")"
	case g.inflight != "":
		head += "  …"
	default:
		head += i18n.T(i18n.Plural(len(g.hits), "gsearch_count"), len(g.hits))
	}
	out := make([]render.Line, 0, rows+4)
	out = append(out, b.bar("┌", "┐"), b.text(head, box))
	// Widths of the cells, three separators of two spaces. The sum is inner as
	// long as inner ≥ 22, which gsRect makes sure of (box ≥ 24); under that, the
	// author then the snippet give way and wrap fills up.
	dateW := len("00/00 00:00")
	chatW := min(gsChatW, max(4, inner/4))
	fromW := min(gsFromW, max(0, inner-chatW-dateW-6))
	snipW := max(0, inner-chatW-fromW-dateW-6)
	q := string(g.query)
	for i := 0; i < rows; i++ {
		j := g.scroll + i
		if j >= len(g.hits) { // the header already carries the count: no duplicate here
			out = append(out, b.text("", dim))
			continue
		}
		hit := g.hits[j]
		st, hi, cell := box, acc, dim
		if j == g.cur { // current line: inverted, hit included
			st, cell = sel, sel
			hi.Reverse = true
		}
		spans := []render.Span{
			{Text: padTo("["+render.Truncate(kindPrefix(hit.Chat.Kind)+bareTitle(hit.Chat, render.CleanLine(chatTitle(hit.Chat, title))), max(1, chatW-2), "…")+"]", chatW), Style: cell},
			{Text: "  " + hit.Date.Format("02/01 15:04") + "  ", Style: cell},
			{Text: padTo("<"+render.CleanLine(hit.From)+">", fromW) + "  ", Style: st},
			{Text: padTo(snippet(hit.Text, q, snipW), snipW), Style: st},
		}
		line := boxDraw{edge: edge, fill: st, inner: inner} // narrow box: the padding follows the line
		out = append(out, line.row(highlight(render.Line{Spans: spans}, q, hi).Spans...))
	}
	return append(out, b.text(i18n.T("gsearch_keys"), edge), b.bar("└", "┘"))
}

// searchGlobalOpen : 2nd Ctrl+F — the local query goes to the server.
func (u *UI) searchGlobalOpen() {
	if u.botOnly() {
		u.sys(i18n.T("gsearch_bot_unavailable"))
		return
	}
	u.gsearch = &globalSearch{query: slices.Clone(u.search.q)}
	u.gsSend() // query already typed: no useless wait
}

// gsSend starts the query when it changed. Only one at a time — the next one
// goes out when the one in flight comes back (searchGlobalResult).
func (u *UI) gsSend() {
	g := u.gsearch
	q := strings.TrimSpace(string(g.query))
	if q == "" { // query erased: nothing left to show, and everything is to be done again
		g.hits, g.cur, g.scroll, g.sent, g.err = nil, 0, 0, "", ""
		return
	}
	if g.inflight != "" || q == g.sent {
		return
	}
	g.inflight, g.err, g.acc, g.errs, g.pending = q, "", nil, nil, 0
	// Every network that searches, or the one of the /net filter alone (F2 in
	// windows mode cycles it): the answers are merged once they are all in.
	for name, b := range u.nets {
		if !b.Caps().GlobalSearch || (u.netFilter != "" && name != u.netFilter) {
			continue
		}
		b.SearchGlobal(u.ctx, q, gsLimit)
		g.pending++
	}
	if g.pending == 0 {
		// No network searches: the query is marked sent, otherwise every key
		// pressed would ask again for the same nothing.
		g.inflight, g.sent = "", q
		g.err = i18n.T("net_unsupported", strings.Join(u.netNames(), ", "))
	}
}

// gsChat : shared chat pointer — the one already known when it exists.
// remember() cannot be used here: the search answer does not carry the read
// counters, it would wipe the ones of the sidebar. The sidebar does not grow
// either (listChat): a plain result, or a contact of the address book, is not
// a chat — attach() puts it there at opening time, newMessage() at the first
// message.
func (u *UI) gsChat(c *model.Chat) *model.Chat {
	if old := u.chats[c.Key()]; old != nil {
		return old
	}
	u.chats[c.Key()] = c
	return c
}

// searchGlobalResult : answer of one network. The hits pile up until every
// network asked has answered; then the list shows, newest first.
func (u *UI) searchGlobalResult(e model.EvSearchGlobal) {
	g := u.gsearch
	if g == nil || e.Query != g.inflight {
		return // overlay closed, or answer of a query given up
	}
	for _, h := range e.Hits {
		h.Chat = u.gsChat(h.Chat)
		g.acc = append(g.acc, h)
	}
	if e.Err != "" {
		g.errs = append(g.errs, render.CleanLine(e.Err))
	}
	if g.pending--; g.pending > 0 {
		return
	}
	g.inflight = ""
	if strings.TrimSpace(string(g.query)) != e.Query {
		u.gsSend() // the query moved during the round trip
		return
	}
	slices.SortStableFunc(g.acc, func(a, b model.SearchHit) int { return b.Date.Compare(a.Date) })
	g.hits, g.acc, g.err, g.sent, g.cur, g.scroll = g.acc, nil, "", e.Query, 0, 0
	if len(g.hits) == 0 && len(g.errs) > 0 {
		g.err, g.sent = strings.Join(g.errs, "; "), "" // failure: the same query must be able to go out again
	}
}

// itemByID gives the item of the window that carries this message, nil else.
func itemByID(w *Window, id int) *Item {
	for _, it := range w.Items {
		if it.Msg != nil && it.Msg.ID == id {
			return it
		}
	}
	return nil
}

// gsOpen : Enter or click — the chat of the result, settled on the message.
func (u *UI) gsOpen() {
	g := u.gsearch
	if g.cur < 0 || g.cur >= len(g.hits) {
		return
	}
	hit := g.hits[g.cur]
	// The local search keeps the keyboard while it is open: it goes with the
	// overlay, even when the jump stays in the current window.
	u.gsearch, u.search = nil, nil
	u.jumpTo(hit.Chat, hit.MsgID)
}

// gsList : the global search seen as a list overlay. close is given by the
// caller: Esc gives the input line back to the local search too, a click
// outside the box only closes the overlay.
func (u *UI) gsList(close func()) listOverlay {
	g, r := u.gsearch, u.gsRect()
	rows := gsRows(r.h)
	return listOverlay{r: r, head: 2, rows: rows, top: g.scroll, n: len(g.hits),
		move:  func(d int) { g.move(d, rows) },
		click: func(i int) { g.cur = i; u.gsOpen() },
		enter: u.gsOpen, close: close}
}

// gsKey : the shared navigation, the 3rd Ctrl+F, then the typing.
func (u *UI) gsKey(k term.Key) {
	// Esc gives the input line back to the local search, which a click outside
	// the box does not do: the close differs between the two paths.
	if u.gsList(func() { u.gsearch, u.search = nil, nil }).key(k) {
		return
	}
	g := u.gsearch
	switch {
	case k.Code == term.Ctrl && k.Rune == 'f':
		u.gsearch = nil // 3rd Ctrl+F: back to the local search, query kept
	case k.Code == term.Backspace:
		if n := len(g.query); n > 0 {
			g.query, g.typed = g.query[:n-1], time.Now()
		}
	case k.Code == term.None && k.Rune != 0 && !k.Alt:
		g.query, g.typed = append(g.query, k.Rune), time.Now()
	}
}

// gsMouse : wheel and click in the list; a click outside the box closes it
// alone, the local search keeps the input line.
func (u *UI) gsMouse(e term.MouseEvent) { u.gsList(func() { u.gsearch = nil }).mouse(e) }
