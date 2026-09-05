package ui

import (
	"cmp"
	"fmt"
	"image"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/theme"
)

// draw repaints everything in one write (messages, status, input, images).
func (u *UI) draw() {
	if u.viewer != nil { // full screen preview: the screen carries the image only
		u.drawViewer()
		return
	}
	w := u.view()
	rows := u.t.Rows
	x0, cols := u.layout() // x0 > 0: the sidebar takes the columns 0..x0-1
	view := u.viewRows()
	lines, items, _ := w.LineItems(u.opts())
	maxScroll := max(0, len(lines)-view)
	w.Scroll = max(0, min(w.Scroll, maxScroll))
	if u.selShow { // on a selection change only: the scroll stays free
		u.selShow = false
		if first, last := blockOf(items, w.Sel); last >= 0 {
			w.Scroll = showRows(w.Scroll, first, last, len(lines), view, maxScroll)
		}
	}
	// Same rule as the selection: it moves only on a hit change, never at
	// each repaint, otherwise the scroll would be blocked.
	if s := u.search; s != nil && s.show {
		s.show = false
		if s.cur >= 0 && s.cur < len(s.hits) {
			l := s.hits[s.cur].line
			w.Scroll = showRows(w.Scroll, l, l, len(lines), view, maxScroll)
		}
	}
	start, end := viewSlice(lines, w.Scroll, view)

	// The overlay is computed before the message area: an image placed under
	// it would be drawn over, by this repaint as well as by animate().
	ov := u.overlay()
	var avs []avPlace
	var hov *render.Img // media of the hovered message (images_hover mode)
	hovRow := 0
	var b strings.Builder
	bg := theme.Style{FG: u.th.FG, BG: u.th.BG}.SGR()
	b.WriteString("\x1b[?25l")
	u.placed = u.placed[:0]
	u.hits = make([]rowHit, max(0, view)) // map of the clicks, made again at each repaint
	for r := 0; r < view; r++ {
		fmt.Fprintf(&b, "\x1b[%d;%dH%s\x1b[K", r+1, x0+1, bg)
		i := start + r
		if i >= end {
			continue
		}
		u.hits[r] = rowHit{item: items[i], img: lines[i].Img, acts: lines[i].Actions}
		u.showItem(items[i]) // the line shows: its media loads (again) here
		line := lines[i]
		if u.search != nil { // highlight when shown only: never in the cache
			line = highlight(line, string(u.search.q), theme.Style{Reverse: true})
		}
		u.writeLine(&b, line, cols, &u.hits[r])
		if img := lines[i].Img; img != nil && img.Row == 0 && r+img.Rows <= view &&
			!ov.hits(r, x0+img.Col, img.Rows, img.Cols) {
			u.placed = append(u.placed, placed{row: r, pid: uint32(r + 1), img: img})
		}
		if h := lines[i].Hover; h != nil && u.hover != nil && items[i] == u.hover {
			hov, hovRow = h, r
		}
		if id := lines[i].Avatar; id != 0 {
			// The peer of the avatar is named by the net of the message that shows
			// it: the aggregate mixes networks, and ids collide between them.
			var peer model.ChatKey
			if it := items[i]; it != nil && it.Msg != nil {
				peer = model.ChatKey{Net: it.Msg.Net, ID: id}
				loc := it.Msg.FromPhoto
				if loc == nil { // cache older than the avatar fallback: photo of the chat
					if c := u.chats[peer]; c != nil {
						loc = c.PhotoLoc
					}
				}
				u.noteAvatar(peer, loc)
			}
			col := x0 + lines[i].AvatarCol
			if md := u.avatarAt(peer); md != nil && !ov.hits(r, col, 1, 2) {
				avs = append(avs, avPlace{row: r, col: col, md: md})
			}
		}
	}
	// The previews of the GIF box go with the images of the messages: same
	// placement, same animation, same end-of-frame diff.
	u.placed = append(u.placed, u.gifPlacements(x0)...)
	if u.cfg.Separator { // separator line, column of the bar included
		dim := theme.Style{FG: u.th.Color(theme.Dim), BG: u.th.BG}.SGR()
		fmt.Fprintf(&b, "\x1b[%d;%dH%s%s\x1b[K", view+1, x0+1, dim, strings.Repeat("─", u.t.Cols-x0))
	}
	if hov != nil {
		// Whole block of the hovered item (help line and reactions included),
		// in screen lines bounded to the view: the image on top must cover none
		// of its lines when another position is possible.
		first, last := blockOf(items, u.hover)
		msgTop := max(0, min(view-1, first-start))
		msgBottom := max(0, min(view-1, last-start))
		u.drawHover(&b, hov, msgTop, msgBottom, hovRow, x0, cols, view, ov)
	}
	u.drawScrollbar(&b, x0+cols, view, len(lines), w.Scroll)
	if x0 > 0 {
		sepRow := -1
		if u.cfg.Separator {
			sepRow = view - sideHdr // list line, the header is above
		}
		side, srows := u.sideBlock(sepRow)
		for i, l := range side {
			fmt.Fprintf(&b, "\x1b[%d;1H", i+1)
			u.writeLine(&b, l, x0, nil)
			if l.Avatar == 0 {
				continue
			}
			// Same rows as sidebarLines, so line i and the chat match: it gives
			// the net that a bare Line.Avatar cannot.
			c := u.sideChatIn(srows, i)
			if c == nil {
				continue
			}
			u.noteAvatar(c.Key(), c.PhotoLoc)
			if md := u.avatarAt(c.Key()); md != nil && !ov.hits(i, 0, 1, 2) {
				avs = append(avs, avPlace{row: i, col: 0, md: md})
			}
		}
	}
	u.drawStatus(&b, rows-u.inputRows(), x0, cols)
	curRow, curCol := u.drawInput(&b, rows, x0, cols)
	var cur map[kplace]bool
	if u.images == "kitty" {
		// The cap never goes under what is on the screen: an image that shows
		// is never dropped by another one of the same repaint.
		u.kittyLRU.cap = max(u.cfg.KittyImages, len(u.placed)+len(avs)+1)
		cur = make(map[kplace]bool, len(u.placed)+len(avs)+1)
		for _, p := range u.placed {
			md := p.img.Media
			if len(md.Frames) == 0 {
				continue
			}
			u.redecode(md) // zoom: decode again only the images on the screen
			fmt.Fprintf(&b, "\x1b[%d;%dH", p.row+1, x0+p.img.Col+1)
			u.placeKitty(&b, cur, p.pid, md, md.Frames[md.Frame%len(md.Frames)], p.img.Cols, p.img.Rows)
		}
		for _, a := range avs {
			u.redecode(a.md)
			fmt.Fprintf(&b, "\x1b[%d;%dH", a.row+1, a.col+1)
			u.placeKitty(&b, cur, avatarPID(a.row, a.col), a.md, a.md.Frames[0], 2, 1)
		}
		// The QR after all the rest: its overlay already covers the text, and
		// the placement (z=0) covers the white cells kept free.
		if row, col, cols, rows, ok := u.qrImage(); ok {
			fmt.Fprintf(&b, "\x1b[%d;%dH", row+1, col+1)
			u.placeKitty(&b, cur, qrPID, u.qr.md, u.qr.png, cols, rows)
		}
	}
	if u.t.Kitty {
		u.endFrameKitty(&b, cur) // after every placement: never a hole
	}
	for _, box := range ov {
		for i, l := range box.lines {
			fmt.Fprintf(&b, "\x1b[%d;%dH", box.row+i+1, box.col+1)
			u.writeLine(&b, l, u.t.Cols-box.col, nil)
		}
	}
	fmt.Fprintf(&b, "\x1b[%d;%dH", curRow, x0+curCol+1)
	if u.picker == nil && u.menu == nil && u.themePick == nil && u.gsearch == nil && u.newChat == nil && u.gifs == nil {
		b.WriteString("\x1b[?25h") // overlay open: the input cursor stays hidden
	}
	u.noteShown()
	u.t.WriteString(b.String())
	u.t.Flush()
}

// drawScrollbar : column kept free at the right of the message area. Always
// written, with a space when there is no bar: no 2J clears the screen.
func (u *UI) drawScrollbar(b *strings.Builder, col, view, total, scroll int) {
	top, length, ok := scrollbar(total, view, scroll)
	acc := theme.Style{FG: u.th.Color(theme.Accent), BG: u.th.BG}.SGR()
	track, cur := theme.Style{FG: u.th.Color(theme.Dim), BG: u.th.BG}.SGR()+"│", "┃"
	if u.zone == zoneMsgs || u.zone == zoneBar { // follow-mouse: the column of the pointed area lights up
		track = acc + "│"
	}
	if u.drag == dragBar || u.zone == zoneBar { // grabbed, or pointer on the column: thicker
		cur = "█"
	}
	thumb := acc + cur
	blank := theme.Style{FG: u.th.FG, BG: u.th.BG}.SGR() + " "
	for r := 0; r < view; r++ {
		fmt.Fprintf(b, "\x1b[%d;%dH", r+1, col+1)
		switch {
		case !ok:
			b.WriteString(blank)
		case r >= top && r < top+length:
			b.WriteString(thumb)
		default:
			b.WriteString(track)
		}
	}
	b.WriteString("\x1b[0m")
}

// placeKitty places the image of the media at the cursor: it sends it on
// demand when the terminal does not have it (any more), and only places it
// otherwise. Sending at load time is not enough: the terminal drops on its
// quota, and an image sent while the window was hidden can be lost.
// ponytail: when the terminal drops it anyway (quota), the placement fails in
// silence until the next change of display mode (F4) or of cell size; to see
// it, one would have to read the answer of an a=p without q=2.
// crop: source sub-rectangle to show (zoomed preview), empty = the whole image.
func (u *UI) placeKitty(b *strings.Builder, cur map[kplace]bool, pid uint32, md *model.Media, png []byte, cols, rows int, crop ...image.Rectangle) {
	if md.KittyID == 0 {
		u.kittyID++
		md.KittyID = u.kittyID
		b.WriteString(media.KittyDisplay(md.KittyID, pid, png, cols, rows, crop...))
	} else {
		b.WriteString(media.KittyPlace(md.KittyID, pid, cols, rows, crop...))
	}
	cur[kplace{id: md.KittyID, pid: pid}] = true
	// The cap covers everything on the screen, so a media already placed in
	// this frame should not be dropped by a placement that follows. If it
	// happened, it heals itself: its id goes back to 0 and the next frame
	// sends it again as a=T.
	for _, old := range u.kittyLRU.touch(md) {
		b.WriteString(u.kittyFree(old))
	}
}

// kplace : one live kitty placement on the screen. The pids come from the
// position, so they are stable from one frame to the next: placing the same
// image again at the same pid replaces it in place, with no hole.
//
//	message image (and preview image): pid = screen line + 1 (1..rows)
//	avatar                          : pid = 1<<24 | line<<12 | column
//
// Two avatars can share a line (sidebar + message), hence the column in the
// pid; two message images cannot (a line of an image block carries nothing
// else).
type kplace struct{ id, pid uint32 }

func avatarPID(row, col int) uint32 {
	return 1<<24 | uint32(row&0xfff)<<12 | uint32(col&0xfff)
}

// placeDiff gives the placements of prev missing from cur, to drop one by one.
// Stable order so that the frame written is reproducible.
func placeDiff(prev, cur map[kplace]bool) []kplace {
	var out []kplace
	for k := range prev {
		if !cur[k] {
			out = append(out, k)
		}
	}
	slices.SortFunc(out, func(a, b kplace) int {
		if a.id != b.id {
			return cmp.Compare(a.id, b.id)
		}
		return cmp.Compare(a.pid, b.pid)
	})
	return out
}

// endFrameKitty closes a frame: the placements of the frame before that this
// one did not make again are dropped (never a=d,d=a at the head of a frame,
// which made every image blink at each repaint), then the images made stale by
// a new decoding are freed — after the new ones are placed, never before.
func (u *UI) endFrameKitty(b *strings.Builder, cur map[kplace]bool) {
	for _, k := range placeDiff(u.kplaced, cur) {
		b.WriteString(media.KittyDeletePlacement(k.id, k.pid))
	}
	for _, id := range u.kittyOld {
		b.WriteString(media.KittyFree(id))
	}
	u.kittyOld, u.kplaced = nil, cur
}

// viewSlice gives the lines drawn — [start, end) trimmed to view rows from
// start — for a drawing of len(lines) lines with scroll of them held back at
// the bottom. The bottom is the anchor: the last line drawn stays lines[end-1].
//
// An image block that the top of the view cuts is taken back whole (a cut
// block is never placed, so its lines would stay blank), but never at scroll
// 0: there the scroll is already at its floor, and the lines that the move
// pushes past the bottom cannot be reached at all. A tall block met by the
// top of the view would then take the end of the thread off the screen — the
// last image block with it, cut in turn, hence not placed: label alone and
// blank lines, while the images higher up keep showing.
func viewSlice(lines []render.Line, scroll, view int) (start, end int) {
	end = len(lines) - scroll
	start = max(0, end-view)
	if scroll > 0 && start < end && lines[start].Img != nil && lines[start].Img.Row > 0 {
		start = max(0, start-lines[start].Img.Row)
	}
	return start, end
}

// showRows gives the Scroll that shows the lines first..last of the drawing
// (total lines in all, view lines on the screen). A block taller than the view
// shows its start.
func showRows(scroll, first, last, total, view, maxScroll int) int {
	scroll = min(scroll, total-1-last)
	scroll = max(scroll, total-view-first)
	return max(0, min(scroll, maxScroll))
}

// avPlace : avatar to place (2 cells x 1 line), never animated.
type avPlace struct {
	row, col int
	md       *model.Media
}

// hoverPID : kitty pid of the hover image on top. Outside the ranges of the
// message images (line+1, 1..rows) and of the avatars (≥ 1<<24): the end of
// frame diff drops it as soon as the hover leaves the message.
const hoverPID = 1 << 23 // under the avatar bit (1<<24), above any line

// drawHover draws the image of the hovered message over the text already
// written (images_hover mode). In kitty it is a placement like any other,
// pushed into u.placed so that animate() animates the GIFs; in half blocks the
// cells are written again in place. msgTop/msgBottom: screen lines of the
// whole block of the hovered message (help line and reactions included);
// labelRow: line of the media label, where the image can sit at the right.
func (u *UI) drawHover(b *strings.Builder, hov *render.Img, msgTop, msgBottom, labelRow, x0, cols, view int, ov overlays) {
	col, row, ok := hoverImageBox(msgTop, msgBottom, labelRow, hov.Col, hov.Cols, hov.Rows, cols, view)
	if !ok || ov.hits(row, x0+col, hov.Rows, hov.Cols) {
		return
	}
	img := &render.Img{Media: hov.Media, Col: col, Cols: hov.Cols, Rows: hov.Rows}
	if u.images != "halfblock" { // kitty: animate() and the LRU read u.placed
		u.placed = append(u.placed, placed{row: row, pid: hoverPID, img: img})
		return
	}
	// ponytail: the PNG is decoded again at each repaint, for the hovered image
	// only; cache it if it ever shows.
	for k, l := range render.HalfblockFrame(hov.Media, hov.Cols, hov.Rows) {
		fmt.Fprintf(b, "\x1b[%d;%dH", row+k+1, x0+col+1)
		u.writeLine(b, l, cols-col, nil)
	}
}

// hoverImageBox gives the top left corner of the hover image on top. Screen
// coordinates of the message area, 0-based. msgTop..msgBottom: lines of the
// block of the hovered message (help line and reactions included). Order of
// preference, each covering no line of the block (but labelRow, where the
// label leaves room at the right on purpose):
//
//	(a) at the right of the label, line labelRow — only when moving it up to
//	    stay in the area does not push the image onto another line of the
//	    block (above as well as under labelRow);
//	(b) under the whole block;
//	(c) above the whole block;
//	(d) last resort: at the right and trimmed, even if it covers.
//
// Each position is brought back into the area (moved up when it goes past the
// bottom). ok = false: empty area or empty image, nothing to draw.
func hoverImageBox(msgTop, msgBottom, labelRow, labelEndCol, imgCols, imgRows, areaCols, areaRows int) (col, row int, ok bool) {
	if imgCols < 1 || imgRows < 1 || areaCols < 1 || areaRows < 1 {
		return 0, 0, false
	}
	clampRow := func(r int) int { return max(0, min(r, areaRows-imgRows)) }
	clampCol := func(c int) int { return max(0, min(c, areaCols-imgCols)) }
	// safe : no line [r, r+imgRows) falls inside the block, but labelRow.
	safe := func(r int) bool {
		for i := r; i < r+imgRows; i++ {
			if i != labelRow && i >= msgTop && i <= msgBottom {
				return false
			}
		}
		return true
	}
	if r := clampRow(labelRow); labelEndCol+1+imgCols <= areaCols && safe(r) {
		return labelEndCol + 1, r, true
	}
	if msgBottom+1+imgRows <= areaRows {
		return 0, msgBottom + 1, true
	}
	if msgTop-imgRows >= 0 {
		return 0, msgTop - imgRows, true
	}
	return clampCol(areaCols - imgCols), clampRow(labelRow), true
}

// rect : screen rectangle in cells, origin 0.
type rect struct{ row, col, h, w int }

// hits : the given rectangle crosses another one that is not empty.
func (r rect) hits(row, col, h, w int) bool {
	return r.h > 0 && row < r.row+r.h && r.row < row+h && col < r.col+r.w && r.col < col+w
}

// overlayBox : one box on top and its content already drawn.
type overlayBox struct {
	rect
	lines []render.Line
}

// overlays : the boxes of the frame, in drawing order.
type overlays []overlayBox

// hits : the given rectangle crosses one of them.
func (o overlays) hits(row, col, h, w int) bool {
	for _, b := range o {
		if b.rect.hits(row, col, h, w) {
			return true
		}
	}
	return false
}

// overlay gives the boxes on top in drawing order: members (F3), emoji
// picker, context menu, theme picker; the last one wins when two overlap.
func (u *UI) overlay() overlays {
	var out overlays
	// Hover popup (who read, who reacted): its message must still be shown.
	if u.who != nil && slices.Contains(u.view().Items, u.who.item) {
		r := u.whoRect()
		out = append(out, overlayBox{rect: r, lines: u.who.Lines(u.th, r.w, r.h)})
	}
	if r, ok := u.partsRect(); ok {
		out = append(out, overlayBox{rect: r, lines: u.parts.Lines(u.th, r.w, r.h)})
	}
	if u.picker != nil {
		r := u.pickerRect()
		out = append(out, overlayBox{rect: r, lines: u.picker.Lines(u.th)})
	}
	if u.mention != nil { // @… box, above the input
		r := u.mentionRect()
		out = append(out, overlayBox{rect: r, lines: u.mention.Lines(u.th, r.w, r.h)})
	}
	if u.spellFix != nil { // correction box, above the input
		r := u.spellFixRect()
		out = append(out, overlayBox{rect: r, lines: u.spellFix.Lines(u.th, r.w, r.h, u.spellFixHelp())})
	}
	if u.menu != nil { // above the members and the emoji picker
		r := u.menuBox()
		out = append(out, overlayBox{rect: r, lines: u.menu.Lines(u.th, r.w, r.h)})
	}
	if u.themePick != nil { // above everything: it takes everything
		out = append(out, overlayBox{rect: u.themeRect(), lines: u.themePick.Lines(u.th)})
	}
	if u.gsearch != nil { // global search: it takes everything too
		r := u.gsRect()
		out = append(out, overlayBox{rect: r, lines: u.gsearch.Lines(u.th, r.w, r.h, u.title)})
	}
	if u.newChat != nil { // new chat: it takes everything too
		r := u.ncRect()
		out = append(out, overlayBox{rect: r, lines: u.newChat.Lines(u.th, r.w, r.h, u.title, u.online)})
	}
	if u.gifs != nil { // GIF box: it takes everything too
		r := u.gifRect()
		out = append(out, overlayBox{rect: r, lines: u.gifLines(r)})
	}
	if u.qr != nil { // login running: nothing else counts
		r := u.qrRect()
		out = append(out, overlayBox{rect: r, lines: u.qr.Lines(u.th, u.t.Cols, u.t.Rows, u.images == "kitty")})
	}
	return out
}

// pickerRect gives the box of the picker, centred, without drawing its content
// (the mouse only needs its position).
func (u *UI) pickerRect() rect {
	return centerRect(u.t.Cols, u.t.Rows, u.picker.width(), u.picker.height())
}

// blockOf gives the first and last line of it in items, -1 when it is missing.
func blockOf(items []*Item, it *Item) (first, last int) {
	first, last = -1, -1
	if it == nil {
		return first, last
	}
	for i, x := range items {
		if x != it {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	return first, last
}

// writeLine writes one line; a non-nil rh: the link ranges are noted in it.
func (u *UI) writeLine(b *strings.Builder, l render.Line, cols int, rh *rowHit) {
	w := 0
	for _, sp := range l.Spans {
		st := sp.Style
		if st.FG.Kind == 0 {
			st.FG = u.th.FG
		}
		if st.BG.Kind == 0 {
			st.BG = u.th.BG
		}
		text := sp.Text
		tw := render.Width(text)
		if w+tw > cols {
			text = render.Truncate(text, cols-w, "")
			tw = render.Width(text)
		}
		if rh != nil && st.URL != "" {
			rh.urls = append(rh.urls, urlSpan{col0: w, col1: w + tw, url: st.URL})
		}
		b.WriteString(st.SGR())
		if st.URL != "" {
			b.WriteString("\x1b]8;;" + st.URL + "\x1b\\")
		}
		b.WriteString(text)
		if st.URL != "" {
			b.WriteString("\x1b]8;;\x1b\\")
		}
		w += tw
		if w >= cols {
			break
		}
	}
	b.WriteString("\x1b[0m")
}

func (u *UI) actList() string {
	var parts []string
	for i, w := range u.ws.List {
		if w.Act > 0 {
			parts = append(parts, fmt.Sprintf("%d(%d)", i, w.Act))
		}
	}
	return strings.Join(parts, ",")
}

func (u *UI) drawStatus(b *strings.Builder, row, x0, cols int) {
	st := u.th.Style(theme.StatusBG)
	acc, act, errS := st, st, st
	acc.FG, acc.Bold = u.th.Color(theme.Accent), true
	act.FG = u.th.Color(theme.Act)
	errS.FG = u.th.Color(theme.Error)
	fmt.Fprintf(b, "\x1b[%d;%dH%s\x1b[K", row, x0+1, st.SGR())
	if u.pager != nil {
		msg := i18n.T("pager_more", len(u.pager.rest))
		u.writeLine(b, render.Line{Spans: []render.Span{{Text: msg, Style: acc}}}, cols, nil)
		return
	}
	w := u.view()
	var spans []render.Span
	add := func(s string, style theme.Style) { spans = append(spans, render.Span{Text: s, Style: style}) }
	add(time.Now().Format("[15:04]"), st)
	if who := u.selfName(w.Chat); who != "" { // account of the network in front
		add(" [@"+who+"]", st)
	}
	add(fmt.Sprintf(" [%d:", u.ws.Cur), st)
	name := u.winName(w)
	switch w {
	case u.agg:
		name = i18n.T("status_aggregated")
	case u.debug:
		name = "debug"
	}
	add(render.CleanLine(name), acc) // remote title: never a raw sequence, only one line
	// Presence: only in a private chat, and bounded so as not to eat the bar.
	if w.Chat != nil && w.Chat.Kind == model.ChatUser {
		if p := u.presence[w.Chat.Key()]; p != "" {
			add(" · "+render.Truncate(p, 20, "…"), st)
		}
	}
	add("]", st)
	if u.multiNet() && w.Chat != nil { // network of the current chat, silent with one backend
		add(" ["+w.Chat.Net+"]", st)
	}
	if w.Log {
		add(" [log]", st)
	}
	mode := u.images // current mode, cycles with F4; ·hover = images_hover (F5)
	if u.cfg.ImagesHover && u.images != "off" {
		mode += i18n.T("status_hover_suffix")
	}
	add(" [img:"+mode+"]", st)
	if l := u.actList(); l != "" {
		add(" [Act: ", st)
		add(l, act)
		add("]", st)
	}
	if s := u.search; s != nil {
		add(i18n.T("status_search", s.cur+1, len(s.hits)), acc)
	}
	if w.Chat != nil {
		if t, ok := u.typing[w.Chat.Key()]; ok {
			who := t.who
			if a := u.aliases[w.Chat.Key()]; a != "" && w.Chat.Kind == model.ChatUser {
				who = a // in a private chat the one who types is the peer: their local name
			}
			add(i18n.T("status_typing", who), st)
		}
	}
	if !u.focused {
		dim := st
		dim.FG = u.th.Color(theme.Dim)
		add(i18n.T("status_away"), dim)
	}
	if s := u.connStatus(); s != "" {
		add(s, errS)
	}
	if u.flashMsg != "" { // last: it is temporary, so it does not move the rest
		add(" ["+u.flashMsg+"]", acc)
	}
	u.writeLine(b, render.Line{Spans: spans}, cols, nil)
}

// inputWindow : runes measured per available cell around the cursor. A real
// grapheme cluster stays far under it (a ZWJ family emoji is 7 runes for 2
// cells), so the visible window is always full.
// ponytail: a text made only of combining marks (many runes, one cell) would
// show a shorter window — cosmetic, and the line stays linear, which is the
// point.
const inputWindow = 8

// hwindow shows the runes around cur that fit into avail columns, and gives
// the width of what sits before the cursor. Only a window of runes around the
// cursor is measured: shifting one rune at a time and calling render.Width on
// the whole rest each time was quadratic, and it was done again at every
// repaint, so at every key press: a pasted single line of 16 000 characters
// gave 2.3 s per frame. Width is monotonic on a shrinking suffix, hence the
// binary search.
func hwindow(runes []rune, cur, avail int) (shown string, curW, first int) {
	cur = min(cur, len(runes))
	lo, hi := max(cur-inputWindow*avail, 0), min(cur+inputWindow*avail, len(runes))
	seg := runes[lo:cur]
	off := lo + sort.Search(len(seg), func(i int) bool { return render.Width(string(seg[i:])) <= avail })
	return render.Truncate(string(runes[off:hi]), avail, ""), render.Width(string(runes[off:cur])), off
}

// drawInput draws the input zone, whose last line is row, and gives back the
// screen row of the cursor and its column relative to the message area.
func (u *UI) drawInput(b *strings.Builder, row, x0, cols int) (curRow, curCol int) {
	prompt := "[status] "
	switch {
	case u.pasteAsk != "":
		n := strings.Count(u.pasteAsk, "\n") + 1
		key := "paste_ask"
		if u.pasteIns {
			key = "paste_ask_insert"
		}
		prompt = i18n.T(key, n)
	case u.ask != nil:
		prompt = u.ask.q + " (y/n) "
	case u.sendAsk != nil:
		prompt = u.sendAsk.prompt()
	case u.prompt != nil:
		prompt = u.prompt.Question + " "
	case u.search != nil:
		prompt = i18n.T("search_prompt")
	case u.edit != nil:
		prompt = fmt.Sprintf("✎ #%d › ", u.edit.Msg.ID)
	case u.reply != nil:
		prompt = fmt.Sprintf("↩ #%d › ", u.reply.Msg.ID)
	case u.view() == u.agg:
		if c := u.aggTarget(); c != nil { // the input goes to the chat of the last message
			prompt = i18n.T("reply_to_prompt", render.CleanLine(u.title(c)))
		}
	case u.view().Chat != nil:
		prompt = "[" + render.CleanLine(u.title(u.view().Chat)) + "] "
	}
	if u.inputRows() > 1 {
		return u.drawInputMulti(b, row, x0, cols, prompt)
	}
	text := strings.ReplaceAll(u.ed.String(), "\n", "⏎")
	cursor := u.ed.Cursor()
	switch {
	case u.pasteAsk != "" || u.ask != nil || (u.sendAsk != nil && !u.sendAsk.caption):
		text = ""
	case u.search != nil: // the input line carries the query, not the message
		text, cursor = string(u.search.q), len(u.search.q)
	}
	if u.prompt != nil && u.prompt.Secret {
		text = strings.Repeat("*", len([]rune(text)))
	}
	runes := []rune(text)
	pw := render.Width(prompt)
	avail := max(cols-pw-1, 1)
	shown, curW, first := hwindow(runes, cursor, avail)
	acc := theme.Style{FG: u.th.Color(theme.Accent), Bold: true}
	fmt.Fprintf(b, "\x1b[%d;%dH%s\x1b[K", row, x0+1, theme.Style{FG: u.th.FG, BG: u.th.BG}.SGR())
	spans := []render.Span{{Text: shown}}
	if u.spellActive() {
		spans = styleRanges([]rune(shown), first, u.spellBadRanges(), theme.Style{}, u.spellStyle())
	}
	u.inMap = inputMap{top: row, pw: pw, avail: avail, lo: first}
	u.writeLine(b, render.Line{Spans: append([]render.Span{{Text: prompt, Style: acc}}, spans...)}, cols, nil)
	return row, pw + curW
}

// drawInputMulti : expanded editor (/set multiline). One screen line per line
// of the draft, the prompt on the first one, the following lines aligned
// under the text; a vertical window keeps the cursor visible when the draft
// is taller than the zone.
func (u *UI) drawInputMulti(b *strings.Builder, row, x0, cols int, prompt string) (curRow, curCol int) {
	ir := u.inputRows()
	top := row - ir + 1
	lines := strings.Split(u.ed.String(), "\n")
	// Line and column of the cursor, in runes.
	ci, ccol, left := 0, 0, u.ed.Cursor()
	for i, l := range lines {
		n := len([]rune(l))
		if left <= n {
			ci, ccol = i, left
			break
		}
		left -= n + 1 // the \n
	}
	start := 0
	if ci >= ir {
		start = ci - ir + 1
	}
	if s := len(lines) - ir; start > s && s >= 0 {
		start = s
	}
	pw := render.Width(prompt)
	avail := max(cols-pw-1, 1)
	acc := theme.Style{FG: u.th.Color(theme.Accent), Bold: true}
	bg := theme.Style{FG: u.th.FG, BG: u.th.BG}.SGR()
	bad := u.spellBadRanges()
	// Rune offset of each draft line in the whole buffer, for the ranges.
	offs := make([]int, len(lines))
	for i := 1; i < len(lines); i++ {
		offs[i] = offs[i-1] + len([]rune(lines[i-1])) + 1
	}
	curW, cliLo := 0, 0
	for r := 0; r < ir; r++ {
		fmt.Fprintf(b, "\x1b[%d;%dH%s\x1b[K", top+r, x0+1, bg)
		li := start + r
		if li >= len(lines) {
			continue
		}
		var shown string
		lo := 0
		if li == ci {
			shown, curW, lo = hwindow([]rune(lines[li]), ccol, avail)
			cliLo = lo
		} else {
			shown = render.Truncate(lines[li], avail, "")
		}
		spans := []render.Span{{Text: shown}}
		if u.spellActive() {
			spans = styleRanges([]rune(shown), offs[li]+lo, bad, theme.Style{}, u.spellStyle())
		}
		lead := render.Span{Text: strings.Repeat(" ", pw)}
		if r == 0 {
			lead = render.Span{Text: prompt, Style: acc}
		}
		u.writeLine(b, render.Line{Spans: append([]render.Span{lead}, spans...)}, cols, nil)
	}
	u.inMap = inputMap{top: top, pw: pw, avail: avail, multi: true, start: start, cli: ci, cliLo: cliLo}
	return top + ci - start, pw + curW
}

// freeImages frees the frames and the kitty images of a window (closed, rebound).
// ponytail: a search window shares its media with the window of the chat:
// freeing them would break the other view, so nothing is freed here. The
// images of the messages seen in the search window only stay in the terminal,
// like the ones dropped by trim; the terminal clears them, framesBudget the frames.
func (u *UI) freeImages(w *Window) {
	if w.Search != "" {
		return
	}
	for _, it := range w.Items {
		if it.Msg != nil && it.Msg.Media != nil {
			u.dropFrames(it.Msg.Media)
		}
	}
}
