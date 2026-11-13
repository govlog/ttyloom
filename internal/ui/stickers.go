package ui

import (
	"cmp"
	"path/filepath"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/emoji"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

// Stickers tab of the GIF box (Tab in Ctrl+G, /sticker): the sections the
// network gives (recents, favorites, packs, the server) laid out flat on the
// grid of the GIF box — a title cell, then its stickers. A pack comes empty and
// is asked for when the end of the list is on the screen, one at a time: a
// burst of requests brings a FLOOD_WAIT on Telegram. Typing searches: the
// network for the stickers of an emoji (Caps.StickerSearch), else a filter of
// what the box holds. Enter or a click sends the one chosen.

// stickerCell : a cell of the grid, a section title (s nil) or a sticker.
type stickerCell struct {
	title string
	sec   int // section of a title, -1 for the results
	s     *model.Sticker
}

type stickerBox struct {
	netQuery
	chat     *model.Chat // conversation the sticker goes to, pinned at opening
	sections []model.StickerSection
	results  []model.Sticker // answer of the network search; shown while the query is not empty
	answered string          // emoji of the search the results answer
	cells    []stickerCell
	loading  string // Key of the pack on its way, "" = none
	g        grid
}

func (b *stickerBox) grid() *grid { return &b.g }

func (b *stickerBox) thumb(i int) *model.Media {
	if s := b.cells[i].s; s != nil {
		return s.Preview
	}
	return nil
}

func (u *UI) stickerRect() rect { return u.gridRect(&u.stickers.g) }

// stickerQuery gives the emoji the Stickers tab asks the network for: q
// itself when it is an emoji of the table, else the best shortcode match
// (":joy:" for "joy"), else the first emoji whose name holds q. false: no
// emoji fits, nothing to ask.
func stickerQuery(q string) (string, bool) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", false
	}
	for _, e := range emoji.All() {
		if emoji.Base(e.Char) == emoji.Base(q) {
			return e.Char, true
		}
	}
	if hits := emoji.Lookup(q, nil); len(hits) > 0 {
		return hits[0].Char, true
	}
	if es := emoji.Search(q); len(es) > 0 {
		return es[0].Char, true
	}
	return "", false
}

// openStickers : /sticker. The conversation is the one the input goes to.
func (u *UI) openStickers(q string) {
	if c := u.boxChat(func(c model.Caps) bool { return c.Stickers }); c != nil {
		u.openStickersFor(c, q)
	}
}

func (u *UI) openStickersFor(c *model.Chat, q string) {
	b := u.net(c)
	if b == nil {
		u.netUnsupported(c.Net)
		return
	}
	u.stickers = &stickerBox{chat: c, netQuery: netQuery{query: []rune(render.CleanLine(q))}, g: grid{live: -1}}
	u.stickerRect() // sized before the first answer
	if q != "" {
		u.stickers.typed = time.Now()
	}
	b.LoadStickers(u.backendContext(b), c)
}

// stickerTab : Tab in the GIF box — its Stickers tab on the same chat.
func (u *UI) stickerTab() {
	c := u.gifs.chat
	if !u.caps(c).Stickers {
		u.netUnsupported(c.Net)
		return
	}
	u.gifClose()
	u.openStickersFor(c, "")
}

// gifTab : Tab in the Stickers tab — back to the GIF box on the same chat.
func (u *UI) gifTab() {
	c := u.stickers.chat
	u.stickerClose()
	u.openGifsFor(c, "")
}

// stickerMine : an answer for the box open now, not for one closed or moved
// to another chat meanwhile.
func (u *UI) stickerMine(chatID int64) *stickerBox {
	s := u.stickers
	if s == nil || s.chat.ID != chatID || u.dispatchNet != s.chat.Net {
		return nil
	}
	return s
}

func (u *UI) stickersLoaded(e model.EvStickers) {
	s := u.stickerMine(e.ChatID)
	if s == nil {
		return
	}
	s.sections, s.err = e.Sections, e.Err
	u.stickerCells()
	u.stickerMore()
}

func (u *UI) stickerPack(e model.EvStickerPack) {
	s := u.stickerMine(e.ChatID)
	if s == nil {
		return
	}
	if s.loading == e.Key {
		s.loading = ""
	}
	for i := range s.sections {
		if sec := &s.sections[i]; sec.Key == e.Key && sec.Stickers == nil {
			sec.Stickers = e.Stickers
			if sec.Stickers == nil {
				sec.Stickers = []model.Sticker{} // never nil again: a failed pack is not asked twice
			}
		}
	}
	if e.Err != "" {
		s.err = e.Err
	}
	u.stickerCells()
	u.stickerMore()
}

func (u *UI) stickerSearch(e model.EvStickerSearch) {
	s := u.stickerMine(e.ChatID)
	if s == nil || e.Query != s.inflight {
		return
	}
	s.inflight = ""
	if want, ok := stickerQuery(string(s.query)); ok && want != e.Query {
		u.stickerAsk() // the query moved during the round trip
		return
	}
	s.results, s.answered, s.err = e.Stickers, e.Query, e.Err
	u.stickerCells()
}

// stickerAsk sends the search of the query (Telegram), once the typing has
// rested; a word no emoji has asks nothing. The local filter needs no network.
func (u *UI) stickerAsk() {
	s := u.stickers
	q := strings.TrimSpace(string(s.query))
	if q == "" || !u.caps(s.chat).StickerSearch {
		u.stickerCells()
		return
	}
	e, ok := stickerQuery(q)
	if !ok {
		s.results, s.answered, s.err = nil, "", ""
		u.stickerCells()
		return
	}
	if s.inflight != "" || e == s.answered {
		u.stickerCells() // the results of that emoji are already there
		return
	}
	b := u.net(s.chat)
	if b == nil {
		return
	}
	s.inflight = e
	b.SearchStickers(u.backendContext(b), s.chat, e)
}

// stickerMore asks for the first pack not loaded yet whose title is on the
// screen, when none is on its way — never while a search shows.
func (u *UI) stickerMore() {
	s := u.stickers
	if s == nil || s.loading != "" || strings.TrimSpace(string(s.query)) != "" {
		return
	}
	lo, hi := s.g.visible()
	for i := lo; i < min(hi, len(s.cells)); i++ {
		c := s.cells[i]
		if c.s != nil || c.sec < 0 {
			continue
		}
		if sec := s.sections[c.sec]; sec.Key != "" && sec.Stickers == nil {
			b := u.net(s.chat)
			if b == nil {
				return
			}
			s.loading = sec.Key
			b.LoadStickerPack(u.backendContext(b), s.chat, sec.Key)
			return
		}
	}
}

// stickerCells lays the cells out again: the search results, or the sections
// (filtered by the query when the network does not search). The current cell
// stays on the same title or sticker when it is still there.
func (u *UI) stickerCells() {
	s := u.stickers
	var keep stickerCell
	if s.g.cur < len(s.cells) {
		keep = s.cells[s.g.cur]
	}
	q := strings.TrimSpace(string(s.query))
	s.cells = s.cells[:0]
	match := func(st *model.Sticker) bool { // the local filter
		f := render.Fold(q)
		return f == "" || strings.Contains(render.Fold(st.Name+" "+st.Tags+" "+st.Emoji), f)
	}
	if q != "" && u.caps(s.chat).StickerSearch {
		e, ok := stickerQuery(q)
		if !ok {
			match = func(*model.Sticker) bool { return false }
		} else {
			match = func(st *model.Sticker) bool { return emoji.Base(st.Emoji) == emoji.Base(e) }
		}
		if ok && s.answered == e && len(s.results) > 0 {
			s.cells = append(s.cells, stickerCell{title: i18n.T("stickers_results"), sec: -1})
			for i := range s.results {
				s.cells = append(s.cells, stickerCell{s: &s.results[i]})
			}
		}
	}
	for i := range s.sections {
		sec := &s.sections[i]
		var hits []stickerCell
		for j := range sec.Stickers {
			if st := &sec.Stickers[j]; match(st) {
				hits = append(hits, stickerCell{s: st})
			}
		}
		if q == "" || len(hits) > 0 {
			s.cells = append(append(s.cells, stickerCell{title: sec.Title, sec: i}), hits...)
		}
	}
	s.g.n, s.g.cur = len(s.cells), 0
	for i, c := range s.cells {
		if c == keep {
			s.g.cur = i
			break
		}
	}
	if s.g.perRow > 0 {
		s.g.move(0) // keeps cur on the screen
	}
}

func (u *UI) stickerLoad() {
	s := u.stickers
	b := u.net(s.chat)
	if b == nil {
		return
	}
	u.gridRelease(s)
	u.gridLoad(s, func(_ int, md *model.Media) {
		md.State = model.MediaLoading
		b.Download(u.backendContext(b), md, filepath.Join(config.CacheDir(), "stickers", s.chat.Net, thumbName(md)))
	})
}

// stickerClose frees the pictures of every sticker the box held, the ones the
// filter hides included.
func (u *UI) stickerClose() {
	s := u.stickers
	s.cells = s.cells[:0]
	for i := range s.sections {
		for j := range s.sections[i].Stickers {
			s.cells = append(s.cells, stickerCell{s: &s.sections[i].Stickers[j]})
		}
	}
	for i := range s.results {
		s.cells = append(s.cells, stickerCell{s: &s.results[i]})
	}
	s.g.n = len(s.cells)
	u.gridFree(s)
	u.stickers = nil
}

func (u *UI) stickerSend() {
	s := u.stickers
	if s.g.cur >= len(s.cells) || s.cells[s.g.cur].s == nil {
		return // a title
	}
	pick, c := *s.cells[s.g.cur].s, s.chat
	u.stickerClose()
	b := u.net(c)
	if b == nil {
		return
	}
	w := u.winFor(c)
	m := u.pendingMsg(c, "")
	m.Media = &model.Media{Kind: model.MediaSticker, State: model.MediaLoading,
		Label: "[sticker " + cmp.Or(pick.Emoji, pick.Name) + "]"}
	u.insertPending(w, m)
	b.SendSticker(u.backendContext(b), c, pick, m.TmpID)
	u.flash(i18n.T("sending"))
}

func (u *UI) stickerKey(k term.Key) {
	s := u.stickers
	switch {
	case k.Code == term.Esc, k.Code == term.Ctrl && k.Rune == 'c':
		u.stickerClose()
	case k.Code == term.Tab:
		u.gifTab()
	case k.Code == term.Enter:
		u.stickerSend()
	case s.g.key(k):
		u.stickerMore()
	case s.edit(k):
		if !u.caps(s.chat).StickerSearch {
			u.stickerCells() // the filter needs no network: at once
		}
	}
}

func (u *UI) stickerMouse(e term.MouseEvent) {
	r := u.stickerRect()
	if u.drag != dragGrid && !r.hits(e.Y, e.X, 1, 1) { // a drag of the thumb may leave the box
		if e.Press && !e.Motion {
			u.stickerClose()
		} else {
			u.stickers.g.live = -1
		}
		return
	}
	if e.Press && !e.Motion && e.Button == 0 && e.Y == r.row+1 {
		if tab, ok := tabAt(e.X-r.col-1, false); ok && tab == "gif" {
			u.gifTab()
		}
		return
	}
	if u.gridMouse(&u.stickers.g, e, r.row+2, r.col+1) >= 0 {
		u.stickerSend()
		return
	}
	u.stickerMore()
}

// boxTabs : the tabs at the head of the GIF box, the open one in brackets.
func boxTabs(stickers bool) string {
	if stickers {
		return "GIF · [Stickers]"
	}
	return "[GIF] · Stickers"
}

// tabAt : the tab under column x of the head line (x counted from the first
// column inside the border).
func tabAt(x int, stickers bool) (string, bool) {
	head := boxTabs(stickers)
	gifEnd := strings.Index(head, "·") - 1
	switch {
	case x >= 0 && x < gifEnd:
		return "gif", true
	case x > gifEnd+1 && x < render.Width(head):
		return "stickers", true
	}
	return "", false
}

// stickerLines draws the box: the tabs and the search, the grid, the keys. It
// also starts the downloads of the cells on the screen.
func (u *UI) stickerLines(r rect) []render.Line {
	s := u.stickers
	u.stickerLoad()
	box, edge, _ := boxStyles(u.th)
	b := boxDraw{edge: edge, fill: box, inner: r.w - 2}
	head := boxTabs(true) + "  " + i18n.T("gif_head", render.CleanLine(string(s.query)))
	switch {
	case s.err != "":
		head += "  (" + s.err + ")"
	case s.inflight != "" || s.loading != "":
		head += "  …"
	case len(s.cells) == 0:
		head += "  " + i18n.T("no_result")
	}
	out := []render.Line{b.bar("┌", "┐"), b.text(head, box)}
	out = append(out, s.g.lines(b, u.th, box, u.gridCells(s, box, func(i int) []string {
		return []string{s.cells[i].title}
	}))...)
	return append(out, b.text(i18n.T("sticker_keys"), edge), b.bar("└", "┘"))
}
