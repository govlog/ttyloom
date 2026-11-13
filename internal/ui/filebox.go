package ui

import (
	"cmp"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// File browser (Ctrl+D, /files): the files sent and received in every
// conversation that the disk cache and the open windows hold, by person.
// The people go on the left, grouped by network, or in one list in the
// aggregate view (F6), cut to the network of /net; me first among them for
// what I sent. The files of the person picked go on the right, newest
// first. What is typed filters the files by name (fileMatch). Enter or a
// click on a file opens the menu of its file (filemenu.go), with "go to the
// message" on top of the usual entries.
//
// The histories are read from the disk outside the UI loop, after the cache
// writes already asked (bgTail); the windows open win over the disk: they
// hold the newest messages, and their media carry the state of their
// download.
//
// ponytail: what the box shows is taken at its opening; a file that comes
// meanwhile shows at the next one.

// fileKind : what counts as a file someone sent — not a sticker, a GIF, a
// link preview nor a map.
func fileKind(md *model.Media) bool {
	if md == nil || md.Loc == nil {
		return false
	}
	switch md.Kind {
	case model.MediaFile, model.MediaPhoto, model.MediaVideo, model.MediaAudio, model.MediaVoice:
		return true
	}
	return false
}

// filePerson : a person of the left column and the files they sent.
type filePerson struct {
	net   string // "" for me in the aggregate view: every network
	name  string
	me    bool
	files []*Item // newest first; Msg.Media is the media of the line
	shown []*Item // the ones the filter keeps
}

// fileRow : a line of the left column — a network header, or a person.
type fileRow struct {
	head   string
	person *filePerson
}

type filesBox struct {
	all       []*filePerson // every person, in their order (sortPeople)
	split     bool          // by network, under headers
	query     []rune        // the filter typed
	rows      []fileRow
	cur, top  int  // person picked (never a header), first row shown
	file      int  // file picked in the list of the person
	ftop      int  // first file shown
	right     bool // the keys go to the files
	loading   bool // the disk is being read
	nFiles    int
	nPersons  int
	need      int // rows the longer list takes, both columns
	lastRows  int // rows of the lists at the last drawing, for PgUp/PgDn
	leftWidth int // columns of the left column at the last drawing
}

// evFiles : the histories of the disk cache, read for box.
type evFiles struct {
	box  *filesBox
	hist map[model.ChatKey][]model.Msg
}

// openFiles : Ctrl+D, /files.
func (u *UI) openFiles() {
	b := &filesBox{loading: true}
	u.files = b
	type job struct {
		key  model.ChatKey
		read func(int64) ([]model.Msg, error)
	}
	var jobs []job
	for _, c := range u.chatList {
		if cc := u.cacheFor(c.Net); cc != nil && u.fileScope(c.Net) {
			jobs = append(jobs, job{c.Key(), cc.LoadHistory})
		}
	}
	prev := u.bgTail
	u.fileRuns.Add(1)
	go func() {
		defer u.fileRuns.Done()
		ev := evFiles{box: b, hist: map[model.ChatKey][]model.Msg{}}
		defer func() {
			if r := recover(); r != nil { // a file the decoder chokes on: the windows still show
				ev.hist = nil
			}
			u.post(ev)
		}()
		if prev != nil {
			<-prev // after the cache writes already asked: they hold what the windows let go
		}
		for _, j := range jobs {
			if msgs, _ := j.read(j.key.ID); len(msgs) > 0 {
				ev.hist[j.key] = msgs
			}
		}
	}()
}

// fileScope : the network net shows in the box — every one, or the one of /net.
func (u *UI) fileScope(net string) bool { return u.netFilter == "" || net == u.netFilter }

// filesLoaded : the disk is read. The box gathers the files by person.
func (u *UI) filesLoaded(e evFiles) {
	b := u.files
	if b == nil || e.box != b {
		return
	}
	b.loading = false
	live := map[model.ChatKey]map[int]*model.Msg{}
	for _, w := range u.ws.List {
		if w.Chat == nil || w.Search != "" || !u.fileScope(w.Chat.Net) {
			continue
		}
		k := w.Chat.Key()
		for _, it := range w.Items {
			if it.part || it.Msg == nil || it.Msg.ID == 0 {
				continue
			}
			if live[k] == nil {
				live[k] = map[int]*model.Msg{}
			}
			live[k][it.Msg.ID] = it.Msg
		}
	}
	people := map[string]*filePerson{}
	add := func(k model.ChatKey, m *model.Msg) {
		if m.Deleted || m.ID == 0 || m.Service != "" {
			return
		}
		for _, md := range append([]*model.Media{m.Media}, m.More...) {
			if !fileKind(md) {
				continue
			}
			p := *m // More stays: the file of an attachment is named by it (fetch)
			p.Net, p.ChatID, p.Media = k.Net, k.ID, md
			id := fmt.Sprintf("%s\x00%d\x00%s", k.Net, m.FromID, m.From)
			if m.FromID != 0 {
				id = fmt.Sprintf("%s\x00%d", k.Net, m.FromID)
			}
			if m.Out {
				id = "me\x00" + k.Net
				if u.aggregate {
					id = "me"
				}
			}
			who := people[id]
			if who == nil {
				who = &filePerson{net: k.Net, name: m.From, me: m.Out}
				if m.Out && u.aggregate {
					who.net = ""
				}
				people[id] = who
			}
			who.files = append(who.files, &Item{Msg: &p})
		}
	}
	for k, msgs := range e.hist {
		for i := range msgs {
			if live[k][msgs[i].ID] == nil { // the window holds it, fresher
				add(k, &msgs[i])
			}
		}
	}
	for k, ms := range live {
		for _, m := range ms {
			add(k, m)
		}
	}
	// The documents downloaded under the names of before move to the folder
	// of their conversation (filePath), and the ones on the disk are known.
	for _, p := range people {
		for _, it := range p.files {
			if md := it.Msg.Media; md.Kind == model.MediaFile {
				if where := u.filePath(it.Msg, md, ""); fileThere(where) {
					md.Path = where
				}
			}
		}
	}
	b.all, b.split = sortPeople(people), !u.aggregate
	b.filter()
	b.need = len(b.rows) // the height of the box, the same whatever the filter
	for _, p := range b.all {
		b.need = max(b.need, len(p.files))
	}
}

// fileThere : a regular file is at name.
func fileThere(name string) bool {
	fi, err := os.Stat(name)
	return err == nil && fi.Mode().IsRegular()
}

// filter keeps the files whose name matches the query and the people who
// have some; the person picked stays picked while they show.
func (b *filesBox) filter() {
	q, was := string(b.query), b.person()
	b.nFiles, b.nPersons = 0, 0
	for _, p := range b.all {
		p.shown = nil
		for _, it := range p.files {
			if fileMatch(q, it.Msg.Media) {
				p.shown = append(p.shown, it)
			}
		}
		if len(p.shown) > 0 {
			b.nPersons++
			b.nFiles += len(p.shown)
		}
	}
	b.rows = fileRows(b.all, b.split)
	b.cur, b.top, b.file, b.ftop = 0, 0, 0, 0
	if i := slices.IndexFunc(b.rows, func(r fileRow) bool { return was != nil && r.person == was }); i >= 0 {
		b.cur = i
	} else {
		b.pick(0, 1)
	}
	b.right = b.right && b.hasFiles()
}

// fileMatch : the name of the file of md matches q — a pattern with * and ?
// on the whole name ("*.pdf", "*toto*.gif"), else a part of it; case and
// accents aside. A file with no name goes by its label.
func fileMatch(q string, md *model.Media) bool {
	if q == "" {
		return true
	}
	name, q := render.Fold(render.CleanLine(cmp.Or(md.Name, md.Label))), render.Fold(q)
	if strings.ContainsAny(q, "*?") {
		ok, _ := path.Match(strings.NewReplacer(`\`, `\\`, `[`, `\[`).Replace(q), name) // [ and \ are letters here
		return ok
	}
	return strings.Contains(name, q)
}

// sortPeople : each person's files newest first; the people by their
// newest file, me first.
func sortPeople(people map[string]*filePerson) []*filePerson {
	var all []*filePerson
	for _, p := range people {
		slices.SortFunc(p.files, func(a, b *Item) int {
			return cmp.Or(b.Msg.Date.Compare(a.Msg.Date), cmp.Compare(b.Msg.ID, a.Msg.ID))
		})
		all = append(all, p)
	}
	slices.SortFunc(all, func(a, b *filePerson) int {
		if a.me != b.me {
			if a.me {
				return -1
			}
			return 1
		}
		return cmp.Or(b.files[0].Msg.Date.Compare(a.files[0].Msg.Date), strings.Compare(a.name, b.name))
	})
	return all
}

// fileRows : the left column — the people with files that show, by
// network under its header when split and when more than one network has
// some.
func fileRows(people []*filePerson, split bool) []fileRow {
	var all []*filePerson
	nets := map[string]bool{}
	for _, p := range people {
		if len(p.shown) > 0 {
			all = append(all, p)
			nets[p.net] = true
		}
	}
	if !split || len(nets) < 2 {
		rows := make([]fileRow, len(all))
		for i, p := range all {
			rows[i] = fileRow{person: p}
		}
		return rows
	}
	names := make([]string, 0, len(nets))
	for n := range nets {
		names = append(names, n)
	}
	slices.Sort(names)
	var rows []fileRow
	for _, n := range names {
		rows = append(rows, fileRow{head: n})
		for _, p := range all {
			if p.net == n {
				rows = append(rows, fileRow{person: p})
			}
		}
	}
	return rows
}

// pick makes row i the person picked, or the next person from it in the
// direction dir when i is a header; past the end, the nearest person the
// other way.
func (b *filesBox) pick(i, dir int) {
	n := len(b.rows)
	if n == 0 {
		return
	}
	i = max(0, min(i, n-1))
	for j := i; j >= 0 && j < n; j += dir {
		if b.rows[j].person != nil {
			b.cur, b.file, b.ftop = j, 0, 0
			return
		}
	}
	for j := i; j >= 0 && j < n; j -= dir {
		if b.rows[j].person != nil {
			b.cur, b.file, b.ftop = j, 0, 0
			return
		}
	}
}

// person : the person picked, nil on an empty list.
func (b *filesBox) person() *filePerson {
	if b.cur >= len(b.rows) {
		return nil
	}
	return b.rows[b.cur].person
}

// picked : the file picked, nil with none.
func (b *filesBox) picked() *Item {
	p := b.person()
	if p == nil || b.file >= len(p.shown) {
		return nil
	}
	return p.shown[b.file]
}

// hasFiles : the person picked has files to go to.
func (b *filesBox) hasFiles() bool {
	p := b.person()
	return p != nil && len(p.shown) > 0
}

// move moves the cursor of the column that has the keys by d.
func (b *filesBox) move(d int) {
	if !b.right {
		if d != 0 {
			b.pick(b.cur+d, cmp.Compare(d, 0))
		}
		return
	}
	if p := b.person(); p != nil {
		b.file = max(0, min(b.file+d, len(p.shown)-1))
	}
}

func (u *UI) filesClose() { u.files = nil }

// filesRect : the box, centred, as wide as the screen lets it up to 120
// columns, as high as its longer list up to 30 rows.
func (u *UI) filesRect() rect {
	w, h := min(u.t.Cols-2, 120), min(u.t.Rows-2, 30, max(u.files.need, 4)+4)
	return centerRect(u.t.Cols, u.t.Rows, max(w, 20), max(h, 6))
}

// filesMenu opens the menu of the file picked, beside its line.
func (u *UI) filesMenu() {
	r := u.filesRect()
	u.filesMenuAt(r.col+u.files.leftWidth+3, r.row+2+u.files.file-u.files.ftop)
}

// filesMenuAt opens the menu of the file picked at (x, y): "go to the
// message" first, then the entries of any file.
func (u *UI) filesMenuAt(x, y int) {
	if it := u.files.picked(); it != nil {
		u.openFileMenu(it, x, y)
		if u.menu != nil {
			u.menu.entries = append([]menuEntry{{label: i18n.T("menu_file_jump"), key: "jump"}}, u.menu.entries...)
		}
	}
}

// filesJump closes the box and goes to the message of it.
func (u *UI) filesJump(it *Item) {
	c := u.chats[it.Msg.Key()]
	u.filesClose()
	if c == nil {
		u.flash(i18n.T("files_chat_gone"))
		return
	}
	u.jumpTo(c, it.Msg.ID)
}

// filesKey : the box takes every key.
func (u *UI) filesKey(k term.Key) {
	b := u.files
	rows := max(1, b.lastRows)
	switch {
	case k.Code == term.Esc && len(b.query) > 0:
		b.query = nil
		b.filter()
	case k.Code == term.Esc, k.Code == term.Ctrl && (k.Rune == 'c' || k.Rune == 'd'):
		u.filesClose()
	case k.Code == term.Backspace:
		if len(b.query) > 0 {
			b.query = b.query[:len(b.query)-1]
			b.filter()
		}
	case k.Code == term.None && k.Rune >= ' ' && !k.Alt:
		b.query = append(b.query, k.Rune)
		b.filter()
	case k.Code == term.Left:
		b.right = false
	case k.Code == term.Right, k.Code == term.Enter && !b.right:
		b.right = b.hasFiles()
	case k.Code == term.Tab:
		b.right = !b.right && b.hasFiles()
	case k.Code == term.Enter:
		u.filesMenu()
	case k.Code == term.Up:
		b.move(-1)
	case k.Code == term.Down:
		b.move(1)
	case k.Code == term.PgUp:
		b.move(-rows)
	case k.Code == term.PgDn:
		b.move(rows)
	case k.Code == term.Home:
		b.move(-len(b.rows) - b.nFiles)
	case k.Code == term.End:
		b.move(len(b.rows) + b.nFiles)
	}
}

// filesMouse : a click outside closes; on a person it picks them, on a file
// it opens the menu of its file; the wheel moves the column under it.
func (u *UI) filesMouse(e term.MouseEvent) {
	b, r := u.files, u.filesRect()
	if !r.hits(e.Y, e.X, 1, 1) {
		if e.Press && !e.Motion {
			u.filesClose()
		}
		return
	}
	y := e.Y - r.row - 2
	if !e.Press || e.Motion || y < 0 || y >= b.lastRows {
		return
	}
	b.right = e.X > r.col+b.leftWidth+1
	switch e.Button {
	case 64:
		b.move(-1)
	case 65:
		b.move(1)
	case 0:
		if !b.right {
			if i := b.top + y; i < len(b.rows) && b.rows[i].person != nil {
				b.pick(i, 1)
			}
			return
		}
		if p := b.person(); p != nil && b.ftop+y < len(p.shown) {
			b.file = b.ftop + y
			u.filesMenuAt(e.X, e.Y)
		}
	}
}

// filesHead : the title, the filter typed, the count, and where the
// reading is.
func (u *UI) filesHead() string {
	b := u.files
	head := i18n.T("files_title")
	if len(b.query) > 0 {
		head += "  " + i18n.T("files_filter", render.CleanLine(string(b.query)))
	}
	switch {
	case b.loading:
		return head + "  …"
	case b.nFiles == 0 && len(b.all) > 0:
		return head + "  " + i18n.T("no_result")
	case b.nFiles == 0:
		return head + "  " + i18n.T("files_none")
	}
	scope := i18n.T("files_all_nets")
	if u.netFilter != "" {
		scope = u.netFilter
	}
	return head + "  " + i18n.T("files_count", b.nPersons, b.nFiles, scope)
}

// filesLines draws the box: a header, the people and the files side by side,
// the keys.
func (u *UI) filesLines(r rect) []render.Line {
	b := u.files
	box, edge, sel := boxStyles(u.th)
	bd := boxDraw{edge: edge, fill: box, inner: r.w - 2}
	rows := max(0, r.h-4)
	lw := max(12, min(28, bd.inner/3))
	rw := max(0, bd.inner-lw-1)
	b.lastRows, b.leftWidth = rows, lw
	b.top = fileTop(b.top, b.cur, rows, b.rows)
	p := b.person()
	if p != nil {
		b.ftop = followTop(b.ftop, b.file, rows)
	}
	hot := sel  // the cursor of the column with the keys
	cold := box // the cursor of the other one
	cold.FG = u.th.Color(theme.Accent)
	cold.Bold = true
	head := theme.Style{FG: u.th.Color(theme.Sep), BG: box.BG}
	dim := box
	dim.FG = u.th.Color(theme.Dim)
	out := []render.Line{bd.bar("┌", "┐"), bd.text(" "+u.filesHead(), box)}
	for y := range rows {
		var sp []render.Span
		switch i := b.top + y; {
		case i >= len(b.rows):
			sp = append(sp, render.Span{Text: strings.Repeat(" ", lw), Style: box})
		case b.rows[i].head != "":
			h := render.Truncate("── "+render.CleanLine(b.rows[i].head)+" ", lw, "")
			sp = append(sp, render.Span{Text: padTo(h+strings.Repeat("─", max(0, lw-render.Width(h))), lw), Style: head})
		default:
			sp = append(sp, u.personCell(b.rows[i].person, lw, pickStyle(i == b.cur, !b.right, hot, cold, box)))
		}
		sp = append(sp, render.Span{Text: "│", Style: edge})
		if p != nil && b.ftop+y < len(p.shown) {
			f := b.ftop + y
			st, chatSt := pickStyle(f == b.file, b.right, hot, cold, box), dim
			if f == b.file {
				chatSt = st
			}
			sp = append(sp, u.fileCell(p.shown[f], rw, st, chatSt)...)
		} else {
			sp = append(sp, render.Span{Text: strings.Repeat(" ", rw), Style: box})
		}
		out = append(out, bd.row(sp...))
	}
	return append(out, bd.text(i18n.T("files_keys"), edge), bd.bar("└", "┘"))
}

// fileTop : the first row shown of the left column — cur in view, and the
// header right above it with it.
func fileTop(top, cur, rows int, list []fileRow) int {
	t := cur
	if cur > 0 && list[cur-1].head != "" {
		t = cur - 1
	}
	return max(0, min(max(top, cur-rows+1), t))
}

// pickStyle : the style of a line — the cursor of the column with the keys,
// the cursor of the other column, or none.
func pickStyle(cur, keys bool, hot, cold, plain theme.Style) theme.Style {
	switch {
	case cur && keys:
		return hot
	case cur:
		return cold
	}
	return plain
}

// personCell : " name ⁿ  12" on w columns — the network badge in the
// aggregate view, the count of files at the right.
func (u *UI) personCell(p *filePerson, w int, st theme.Style) render.Span {
	name := render.CleanLine(p.name)
	if p.me {
		name = i18n.T("files_me")
	}
	if u.aggregate && p.net != "" {
		name += netBadge(p.net)
	}
	n := fmt.Sprintf(" %d ", len(p.shown))
	return render.Span{Text: padTo(" "+render.Truncate(name, max(0, w-render.Width(n)-1), "…"), w-render.Width(n)) + n, Style: st}
}

// fileCell : "date  size  name  conversation" on w columns, the
// conversation in its own style.
func (u *UI) fileCell(it *Item, w int, st, chatSt theme.Style) []render.Span {
	m, md := it.Msg, it.Msg.Media
	left := fmt.Sprintf(" %s %8s  %s", i18n.LocalTime(m.Date), render.HumanSize(md.Size), render.CleanLine(cmp.Or(md.Name, md.Label)))
	chat := ""
	if c := u.chats[m.Key()]; c != nil {
		chat = "  " + render.CleanLine(u.title(c))
	}
	if u.aggregate {
		chat += netBadge(m.Net)
	}
	left = render.Truncate(left, w, "…")
	chat = padTo(render.Truncate(chat, max(0, w-render.Width(left)), "…"), max(0, w-render.Width(left)))
	return []render.Span{{Text: left, Style: st}, {Text: chat, Style: chatSt}}
}
