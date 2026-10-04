package ui

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// Media browser (Ctrl+M, /media): the photos and videos, the GIFs or the
// files of the conversation of the window, sent by anyone, newest first, on
// the picture grid (grid.go). The network searches (model.MediaSearcher), a
// page at a time; the next one goes out when the grid nears the end of the
// list. Enter or a click shows the media, j goes to its message, o opens it.

// mediaAuto : pages asked in a row that bring nothing to show (the network
// filters coarser than the tab): past it, the box waits for a move.
const mediaAuto = 5

// mediaTabs : the i18n key of each tab, in the order of model.MediaFilter.
var mediaTabs = [...]string{"media_tab_media", "media_tab_gifs", "media_tab_files"}

type mediaBox struct {
	chat    *model.Chat // conversation, pinned at opening
	tab     model.MediaFilter
	items   []model.MediaItem // newest first
	pics    []*model.Media    // picture of each cell, nil: its text
	seen    map[int]bool      // ids listed: two pages can overlap
	g       grid
	asked   int // page in flight (its "before"), -1: none
	next    int // "before" of the next page, 0: the list is over
	auto    int // pages in a row with nothing to show
	err     string
	tabCols [len(mediaTabs)][2]int // columns of each tab in the header, from the inner left
}

func (b *mediaBox) grid() *grid              { return &b.g }
func (b *mediaBox) thumb(i int) *model.Media { return b.pics[i] }

// openMediaBox : Ctrl+M, /media [media|gifs|files]. The conversation is the
// one the input goes to.
func (u *UI) openMediaBox(arg string) {
	w := u.sendWin()
	if w == nil {
		return
	}
	c := w.Chat
	if c == nil {
		w.AddSys(i18n.T("window_not_bound"))
		return
	}
	if u.selfOf(c.Net).Bot { // a bot account cannot search a history
		u.sys(i18n.T("bot_unavailable"))
		return
	}
	if _, ok := u.net(c).(model.MediaSearcher); !ok {
		u.netUnsupported(c.Net)
		return
	}
	tab := model.TabMedia
	switch arg {
	case "", "media":
	case "gifs":
		tab = model.TabGIFs
	case "files":
		tab = model.TabFiles
	default:
		u.sys(i18n.T("usage_media"))
		return
	}
	u.mbox = &mediaBox{chat: c}
	u.mboxTab(tab)
}

// mboxTab shows tab t from its newest media.
func (u *UI) mboxTab(t model.MediaFilter) {
	b := u.mbox
	u.gridFree(b)
	*b = mediaBox{chat: b.chat, tab: t, seen: map[int]bool{}, g: grid{live: -1}, asked: -1}
	u.gridRect(&b.g) // sized before the first answer
	u.mboxAsk(0)
}

func (u *UI) mboxAsk(before int) {
	b := u.mbox
	be := u.net(b.chat)
	s, ok := be.(model.MediaSearcher)
	if !ok {
		b.err = i18n.T("net_unsupported", b.chat.Net)
		return
	}
	b.asked, b.err = before, ""
	s.SearchMedia(u.backendContext(be), b.chat, b.tab, before)
}

// mediaResult : a page of the network. An answer for another tab or page is
// dropped; an error stops the asking until the next move.
func (u *UI) mediaResult(e model.EvMedia) {
	b := u.mbox
	if b == nil || u.dispatchNet != b.chat.Net || e.ChatID != b.chat.ID || e.Filter != b.tab || e.Before != b.asked {
		return
	}
	b.asked = -1
	if e.Err != "" {
		b.err = e.Err
		return
	}
	n := len(b.items)
	for _, it := range e.Items {
		if b.seen[it.Msg.ID] {
			continue
		}
		b.seen[it.Msg.ID] = true
		b.items = append(b.items, it)
		b.pics = append(b.pics, u.cellPicture(&b.items[len(b.items)-1]))
	}
	b.next, b.g.n = e.Next, len(b.items)
	if len(b.items) == n {
		b.auto++
	} else {
		b.auto = 0
	}
}

// cellPicture : what the cell of it shows. A small GIF itself — it plays
// under the pointer; else the picture the network gave; else the media
// itself when small enough (a video: its first frame); else nothing, the
// cell shows text.
func (u *UI) cellPicture(it *model.MediaItem) *model.Media {
	md := it.Msg.Media
	if u.images == "off" {
		return nil
	}
	small := u.cfg.AutoMediaMaxKB > 0 && md.Size <= int64(u.cfg.AutoMediaMaxKB)*1024
	decodable := !strings.HasPrefix(md.Mime, "video/") || media.HaveFFmpeg
	switch {
	case md.Kind == model.MediaGIF && small && decodable:
		return md
	case it.Thumb != nil:
		return it.Thumb
	case md.Previewable() && small && decodable:
		return md
	}
	return nil
}

// mboxLoad starts the downloads of the pictures on the screen, lets the
// frames far from the view go, and asks the next page when the grid nears
// the end of the list. Called at each drawing.
func (u *UI) mboxLoad() {
	b := u.mbox
	be := u.net(b.chat)
	if be == nil {
		return
	}
	u.gridRelease(b)
	lo, hi := b.g.visible()
	for i := lo; i < hi; i++ {
		md := b.pics[i]
		if md == nil || md.State != model.MediaNone || !md.Previewable() {
			continue
		}
		if md == b.items[i].Msg.Media { // the file of the message, where the chat would put it
			u.download(&b.items[i].Msg)
			continue
		}
		md.State = model.MediaLoading
		be.Download(u.backendContext(be), md, filepath.Join(config.CacheDir(), "thumbs", b.chat.Net, thumbName(md)))
	}
	if b.asked < 0 && b.next != 0 && b.err == "" && b.auto < mediaAuto && hi+b.g.perRow*b.g.rows >= len(b.items) {
		u.mboxAsk(b.next)
	}
}

// mboxRetry : a move asks again what an error or a run of empty pages
// stopped (mboxLoad); a first page that failed is asked again here.
func (u *UI) mboxRetry() {
	b := u.mbox
	failed := b.err != "" && len(b.items) == 0 && b.asked < 0
	b.auto, b.err = 0, ""
	if failed {
		u.mboxAsk(0)
	}
}

func (u *UI) mboxClose() {
	u.gridFree(u.mbox)
	u.mbox = nil
}

// mboxPick : the message of the current cell, nil on an empty list.
func (u *UI) mboxPick() *model.Msg {
	b := u.mbox
	if b.g.cur >= len(b.items) {
		return nil
	}
	return &b.items[b.g.cur].Msg
}

// mboxView shows the media of the current cell in the preview, above the
// box — closing it comes back here. A file opens with its program.
func (u *UI) mboxView() {
	m := u.mboxPick()
	switch {
	case m == nil:
	case m.Media.Previewable() && u.images != "off":
		u.viewMsg(m)
		if u.viewer != nil {
			u.viewer.nav = u.mboxNav
		}
	default:
		u.openItemMedia(u.view(), &Item{Msg: m})
	}
}

// mboxNav : the arrows of a preview opened from the browser step through its
// list in the order of the grid; the current cell follows, and the next page
// is asked before the step reaches the end of what is loaded.
func (u *UI) mboxNav(cur *model.Msg, d int) *model.Msg {
	b := u.mbox
	if b == nil {
		return nil
	}
	i := slices.IndexFunc(b.items, func(it model.MediaItem) bool { return it.Msg.ID == cur.ID })
	if i < 0 {
		return nil
	}
	for j := i + d; j >= 0 && j < len(b.items); j += d {
		if m := &b.items[j].Msg; u.viewable(m.Media) {
			b.g.move(j - b.g.cur)
			if j >= len(b.items)-b.g.perRow*b.g.rows && b.asked < 0 && b.next != 0 && b.err == "" {
				u.mboxAsk(b.next)
			}
			return m
		}
	}
	return nil
}

// mboxJump closes the box and goes to the message of the current cell.
func (u *UI) mboxJump() {
	if m := u.mboxPick(); m != nil {
		c := u.mbox.chat
		u.mboxClose()
		u.jumpTo(c, m.ID)
	}
}

// mboxKey : the box takes every key.
func (u *UI) mboxKey(k term.Key) {
	b := u.mbox
	n := model.MediaFilter(len(mediaTabs))
	switch {
	case k.Code == term.Esc, k.Code == term.Ctrl && (k.Rune == 'c' || k.Rune == 'm'):
		u.mboxClose()
	case k.Code == term.Tab && k.Shift:
		u.mboxTab((b.tab + n - 1) % n)
	case k.Code == term.Tab:
		u.mboxTab((b.tab + 1) % n)
	case k.Code == term.Enter:
		u.mboxView()
	case b.g.key(k):
		u.mboxRetry()
	case k.Code == term.None && k.Rune == 'j':
		u.mboxJump()
	case k.Code == term.None && k.Rune == 'o':
		if m := u.mboxPick(); m != nil {
			if render.SafeURL(m.Media.URL) { // a page asks first, on the input line the box would hold
				u.mboxClose()
			}
			u.openItemMedia(u.view(), &Item{Msg: m})
		}
	}
}

// mboxMouse : a click outside closes; on the header a click picks a tab;
// the grid takes the rest, a click on a cell shows its media.
func (u *UI) mboxMouse(e term.MouseEvent) {
	b, r := u.mbox, u.gridRect(&u.mbox.g)
	click := e.Press && !e.Motion
	switch {
	case u.drag != dragGrid && !r.hits(e.Y, e.X, 1, 1): // a drag of the thumb may leave the box
		if click {
			u.mboxClose()
		} else {
			b.g.live = -1
		}
	case u.drag != dragGrid && e.Y == r.row+1:
		for t, c := range b.tabCols {
			if x := e.X - r.col - 1; click && e.Button == 0 && x >= c[0] && x < c[1] {
				u.mboxTab(model.MediaFilter(t))
			}
		}
	default:
		if click { // a press or the wheel, not the pointer going by
			u.mboxRetry()
		}
		if u.gridMouse(&b.g, e, r.row+2, r.col+1) >= 0 {
			u.mboxView()
		}
	}
}

// mediaText : the lines of a cell with no picture — name, size, who sent it
// and when.
func mediaText(m *model.Msg) []string {
	md := m.Media
	return []string{render.CleanLine(cmp.Or(md.Name, md.Label)), render.HumanSize(md.Size), m.From, i18n.LocalTime(m.Date)}
}

// mboxHead : the tabs, the current one lit, then the conversation and the
// count. It records where each tab is, for the clicks.
func (u *UI) mboxHead(inner int, box theme.Style) []render.Span {
	b := u.mbox
	dim, on := box, box
	dim.FG = u.th.Color(theme.Dim)
	on.FG, on.Bold, on.Reverse = u.th.Color(theme.Accent), true, true
	var sp []render.Span
	col := 0
	for t, key := range mediaTabs {
		name := " " + i18n.T(key) + " "
		st := dim
		if model.MediaFilter(t) == b.tab {
			st = on
		}
		b.tabCols[t] = [2]int{col, col + render.Width(name)}
		sp = append(sp, render.Span{Text: name, Style: st})
		col += render.Width(name)
	}
	status := ""
	switch {
	case b.err != "":
		status = "(" + b.err + ")"
	case b.asked >= 0:
		status = "…"
	case len(b.items) == 0:
		status = i18n.T("no_result")
	default:
		status = fmt.Sprintf("%d/%d", b.g.cur+1, len(b.items))
		if b.next != 0 {
			status += "+"
		}
	}
	rest := "  " + u.title(b.chat) + "  " + status
	return append(sp, render.Span{Text: padTo(render.Truncate(rest, max(0, inner-col), "…"), max(0, inner-col)), Style: box})
}

// mboxLines draws the box: the tabs, the grid, the keys. It also starts the
// downloads of the cells on the screen and the next page.
func (u *UI) mboxLines(r rect) []render.Line {
	b := u.mbox
	u.mboxLoad()
	box, edge, _ := boxStyles(u.th)
	bd := boxDraw{edge: edge, fill: box, inner: r.w - 2}
	out := []render.Line{bd.bar("┌", "┐"), bd.row(u.mboxHead(bd.inner, box)...)}
	out = append(out, b.g.lines(bd, u.th, box, u.gridCells(b, box, func(i int) []string { return mediaText(&b.items[i].Msg) }))...)
	return append(out, bd.text(i18n.T("media_keys"), edge), bd.bar("└", "┘"))
}
