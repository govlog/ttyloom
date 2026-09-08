package render

import (
	"bytes"
	"fmt"
	"image/png"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/theme"
)

type Opts struct {
	sendEcho     bool // local outgoing prefix: [msg(target)] instead of <nick>
	Width        int
	Theme        theme.Theme
	Timestamps   bool
	Seconds      bool   // timestamps with the seconds (15:04:05)
	LinkPreviews bool   // link previews: a "│" block with title and description
	Images       string // kitty | halfblock | off
	ImagesHover  bool   // image on hover only (F5): no line kept free
	Video        string // inline video: show | hidden (label alone) | autoplay
	CellW, CellH int    // px per cell (halfblock: 1 and 2)
	MaxImgCols   int
	MaxImgRows   int
	// Self : my id on the network of the message — Own() reads it. A function
	// and not an integer: the aggregated view and the search results mix
	// networks, and each one has an identity of its own. nil = no identity
	// (id 0): only the messages sent from here are mine.
	Self      func(m *model.Msg) int64
	Avatars   bool       // gutter of 3 cells before the nick (kitty)
	ShowChat  bool       // aggregated view: put the chat title in front
	Selected  *model.Msg // selected message: marker, background and help line
	Hover     *model.Msg // message under the mouse: background, no marker
	HoverHelp bool       // help line shown on hover too (menu mode), not only on selection
	// TextSel : messages covered by a selection drag. Background only: no help
	// line, which would change the height of the whole range.
	TextSel map[*model.Msg]bool
	// ReadOutbox : last of my messages read by the chat of the message. nil: no
	// "read" tick (a function and not an integer: the aggregated view mixes
	// chats). The whole message is handed over and not its ChatID: the chat is
	// named by (Net, ChatID) on the UI side, and the aggregate mixes networks.
	ReadOutbox func(m *model.Msg) int
	// ReadInbox : last received message I read in the chat of the message. nil:
	// no tick on the messages of other people.
	ReadInbox func(m *model.Msg) int
	// Reactions : reactions usable in the chat of the window, in Telegram order.
	// nil = restriction unknown (aggregated view, tests); empty non-nil = none,
	// so no hover emoji.
	Reactions []string
	// ChatKind : kind of the chat of the message. It is the avatar/colour
	// fallback for an incoming message with no FromID in a private chat (cache
	// written before the 30/08 fix, or any future MTProto case with no from_id):
	// the TDLib id of the peer is then the one of the chat itself. nil = no fallback.
	ChatKind func(m *model.Msg) model.ChatKind
	// Jump : search window — the palette offers "g go" (join the message in its
	// chat) instead of "g quoted".
	Jump bool
	// Alias : local name of the chat of the message (/rename), "" with no alias.
	// It replaces the title of the [chat] prefix of the aggregate and, in a
	// private chat, the name of the peer. nil = no renaming.
	Alias func(m *model.Msg) string
	// Redline : /set redline. The last-read line (LineItems) shows only when
	// this is true.
	Redline bool
	// Caps : what the network of the message can do — a function and not a
	// value, the aggregated view mixes networks and each message carries its
	// own. nil = nothing allowed: the UI always poses it (opts()), and a
	// missing one must not turn a gone backend into a full-featured network.
	Caps func(m *model.Msg) model.Caps
}

// Stamp : the time prefix of a line ("15:04 ", with seconds on o.Seconds).
func (o Opts) Stamp(t time.Time) string {
	f := "15:04"
	if o.Seconds {
		f = "15:04:05"
	}
	return t.Format(f) + " "
}

// caps : capabilities of the network of m, nothing allowed with no Caps posed.
func (o Opts) caps(m *model.Msg) model.Caps {
	if o.Caps == nil {
		return model.Caps{}
	}
	return o.Caps(m)
}

// self : my id on the network of m, 0 when the caller gave no identity.
func (o Opts) self(m *model.Msg) int64 {
	if o.Self == nil {
		return 0
	}
	return o.Self(m)
}

// selMark : column of the selection marker.
const selMark = "▌"

// SendEcho uses the same body, wrapping and error display with an IRC-style
// outgoing label. The caller decides whether this is a history item or only
// a local transcript (which must not allocate media placements).
func SendEcho(m *model.Msg, o Opts) []Line {
	o.sendEcho, o.Avatars, o.ShowChat = true, false, false
	return Message(m, o)
}

// Message draws a whole message (prefix, body, media, reactions).
func Message(m *model.Msg, o Opts) []Line {
	th := o.Theme
	dim := th.Style(theme.Dim)
	if m.Service != "" { // never selectable
		return Plain("*** "+Clean(m.Service), th.Style(theme.System), o.Width)
	}
	sel := o.Selected == m
	// Hover: same background as the selection, without the marker. The selection
	// wins, otherwise the message would carry two help lines.
	hov := !sel && o.Hover == m
	// helpHov : help line on hover, only in menu mode (HoverHelp); on selection
	// it always shows, whatever the mode.
	helpHov := hov && o.HoverHelp
	tsel := !sel && !hov && o.TextSel[m]
	if sel {
		o.Width-- // column of the marker
	}
	caps := o.caps(m)
	own := Own(m, o.self(m))
	fromID := m.FromID
	if fromID == 0 && !m.Out && o.ChatKind != nil && o.ChatKind(m) == model.ChatUser {
		fromID = m.ChatID // private chat: the incoming peer, for lack of from_id
	}
	tick, tickStyle := msgTick(m, o, caps)
	prefix, indent, avCol := msgPrefix(m, o, fromID, own)
	quick := "" // hover/selection: quick reaction emoji, raw — sent back to the server as it is
	// Neither deleted nor still in flight: nothing to react to.
	if (sel || helpHov) && Actionable(m) && caps.Reactions {
		quick = quickEmoji(m, o.Reactions)
	}
	bw := o.Width - indent - Width(tick)
	if bw < 10 { // screen too narrow: no prefix
		bw, indent, prefix = o.Width-Width(tick), 0, nil
	}

	lines, mk, ra, quote := msgBody(m, o, bw)
	// --- image block ---
	if md := m.Media; md != nil && md.Kind == model.MediaWebPage {
		o.MaxImgRows = min(o.MaxImgRows, MaxLinkRows) // link thumbnail: never a big picture, inline or on hover
	}
	img, hoverMedia := msgImage(m, o, indent)
	lines = append(lines, img...)
	// --- help line ---
	var acts []act
	helpFrom := -1
	if sel || helpHov {
		acts = selActs(m, own, o.Jump, caps)
		if quick != "" { // quick reaction emoji at the head of the help line, clickable
			acts = append([]act{{KeyReact, Truncate(Clean(quick), 2, ""), quick, 0}}, acts...)
		}
		helpFrom = len(lines)
		lines = append(lines, Wrap([]Span{{helpText(acts), dim}}, bw)...)
	}
	// --- avatar, padding and tick ---
	if o.Avatars && len(prefix) > 0 { // screen not too narrow
		lines[0].Avatar, lines[0].AvatarCol = fromID, avCol
	}
	pad := Span{strings.Repeat(" ", indent), theme.Style{}}
	for i := range lines {
		switch {
		case i == 0:
			lines[i].Spans = append(append([]Span{}, prefix...), lines[i].Spans...)
		case indent > 0 && lines[i].Img == nil:
			lines[i].Spans = append([]Span{pad}, lines[i].Spans...)
		}
	}
	if tick != "" {
		lines[0].Spans = append(lines[0].Spans, Span{tick, tickStyle})
	}
	// --- selection mark ---
	if sel || hov || tsel {
		paintMark(lines, th, sel)
	}
	// --- actions ---
	// The tick of my messages is a hover zone (who read the message). After
	// the selection marker: the columns count it.
	if tick != "" && m.Out {
		w := lineWidth(lines[0])
		lines[0].Actions = append(lines[0].Actions, Action{Col0: w - Width(tick), Col1: w, Key: KeyTicks})
	}
	// The "│ …" line of a reply points back to the quoted message.
	if mk.reply >= 0 && m.Reply.ID != 0 {
		lines[mk.reply].Actions = actionsIn(lines[mk.reply], []act{{KeyJump, quote, "", m.Reply.ID}})
	}
	for i := mk.reactFirst; i < mk.reactLast; i++ {
		lines[i].Actions = actionsIn(lines[i], ra)
	}
	for i := helpFrom; i >= 0 && i < len(lines); i++ {
		lines[i].Actions = actionsIn(lines[i], acts)
	}
	// A click on the label of a photo, a video, a GIF or a map is the "v" of
	// the palette: the preview, never the desktop. A link preview keeps its
	// URL (the page) and a file has nothing to preview.
	if md := m.Media; mk.label >= 0 && md.Kind != model.MediaWebPage && md.Previewable() && Actionable(m) {
		lines[mk.label].Actions = append(lines[mk.label].Actions, actionsIn(lines[mk.label], []act{{KeyView, mk.labelText, "", 0}})...)
	}
	// After the indent and the marker: Col is the end of the label as it is
	// drawn.
	if hoverMedia != nil && mk.label >= 0 {
		if cols, rows := imgBox(hoverMedia, indent, o); cols > 0 && rows > 0 {
			lines[mk.label].Hover = &Img{Media: hoverMedia, Col: lineWidth(lines[mk.label]), Cols: cols, Rows: rows}
		}
	}
	return lines
}

// msgTick : end of the first line — sent/read for my messages, read/unread for
// the others. "" when the network has no read receipts.
func msgTick(m *model.Msg, o Opts, caps model.Caps) (tick string, st theme.Style) {
	tick, st = "", o.Theme.Style(theme.Dim)
	switch { // end of the first line: sent/read (mine) or read/unread (someone else)
	case !caps.ReadReceipts: // network with no receipts: neither tick nor KeyTicks zone
	case m.Out && m.ID != 0:
		tick = " ✓"
		if o.ReadOutbox != nil && m.ID <= o.ReadOutbox(m) {
			tick = " ✓✓"
		}
	case !m.Out && m.ID != 0 && o.ReadInbox != nil:
		if m.ID <= o.ReadInbox(m) {
			tick = " ✓✓"
		} else {
			tick, st = " •", o.Theme.Style(theme.Accent)
		}
	}
	return tick, st
}

// msgPrefix : what comes before the body — time, [chat] label, avatar gutter
// and <nick>. indent is the width it takes (the columns the following lines are
// padded with), avCol the column of the avatar in it. fromID is the author
// already settled by Message (a private chat with no from_id falls back on the
// peer), own says the message is mine.
func msgPrefix(m *model.Msg, o Opts, fromID int64, own bool) (prefix []Span, indent, avCol int) {
	th := o.Theme
	dim := th.Style(theme.Dim)
	// Local name (/rename): it counts for the label of the aggregate and, in a
	// private chat (the peer has the id of the chat), for the peer name.
	label, from := m.ChatLabel, m.From
	if o.Alias != nil {
		if a := o.Alias(m); a != "" {
			label = a
			if !m.Out && fromID == m.ChatID {
				from = a
			}
		}
	}
	if o.Timestamps {
		prefix = append(prefix, Span{o.Stamp(m.Date), dim})
	}
	if o.sendEcho {
		prefix = append(prefix, Span{"[msg(", dim}, Span{CleanLine(label), th.Style(theme.Own)}, Span{")] ", dim})
		for _, s := range prefix {
			indent += Width(s.Text)
		}
		return prefix, indent, 0
	}
	if o.ShowChat && label != "" {
		prefix = append(prefix, Span{"[" + Clean(label) + "] ", theme.Style{FG: th.Nick(m.ChatID)}})
	}
	nick := theme.Style{FG: th.Nick(fromID)}
	if own {
		nick = th.Style(theme.Own)
	}
	if o.Avatars { // 3 image cells, before the nick
		for _, s := range prefix {
			avCol += Width(s.Text)
		}
		prefix = append(prefix, Span{"   ", theme.Style{}})
	}
	prefix = append(prefix, Span{"<", dim}, Span{Clean(from), nick}, Span{"> ", dim})
	for _, s := range prefix {
		indent += Width(s.Text)
	}
	return prefix, indent, avCol
}

// msgMarks : the lines the tail of Message has to find again. Every field is a
// rank in the lines given back, -1 when the section is not there.
type msgMarks struct {
	reply      int    // last line of the "│ …" quote: it links back to the message
	label      int    // last line of the media label: the hover image hangs there
	labelText  string // the label as drawn, the click zone of "v"
	reactFirst int    // first line of the reactions
	reactLast  int    // line after the last one of the reactions
}

// msgBody : the sections of a message (quote, forward, text or "deleted",
// media label, error, reactions) wrapped to bw columns, with the marks the
// actions need, the clickable reactions and the raw quote, which is the label
// of its link back.
func msgBody(m *model.Msg, o Opts, bw int) (lines []Line, mk msgMarks, ra []act, quote string) {
	th := o.Theme
	dim := th.Style(theme.Dim)
	var sections [][]Span
	labelSec := -1 // section of the media label (hover mode)
	replySec := -1
	if m.Reply != nil {
		q := Clean(m.Reply.Text)
		if m.Reply.From != "" {
			q = Clean(m.Reply.From) + ": " + q
		}
		if q == "" {
			q = "…"
		}
		quote = "│ " + truncate(q, bw-2)
		replySec = len(sections)
		sections = append(sections, []Span{{quote, dim}})
	}
	if m.FwdFrom != "" {
		sections = append(sections, []Span{{i18n.T("fwd_from", Clean(m.FwdFrom)), dim}})
	}
	switch {
	case m.Deleted:
		sections = append(sections, []Span{{i18n.T("msg_deleted"), dim}})
	case m.Text != "":
		body := Runs(m.Text, m.Entities, theme.Style{}, th)
		if m.Pending {
			body = append([]Span{{"… ", dim}}, body...)
		}
		if m.Edited {
			body = append(body, Span{i18n.T("msg_edited"), dim})
		}
		sections = append(sections, body)
	}
	if md := m.Media; md != nil && !m.Deleted {
		label := Clean(md.Label)
		switch md.State {
		case model.MediaLoading:
			label += i18n.T("media_loading")
		case model.MediaReady:
			if md.Kind == model.MediaVideo && md.Want > len(md.Frames) {
				label += i18n.T("media_decoding_pct", 100*len(md.Frames)/md.Want)
			}
		case model.MediaFailed:
			label += i18n.T("media_error", Clean(md.Err))
		}
		labelSec = len(sections)
		st := linkStyle(md, dim)
		if md.Kind == model.MediaWebPage && o.LinkPreviews && bw >= 10 { // too narrow: label only
			sections = append(sections, linkSections(label, Clean(md.Name), bw, dim, st)...)
		} else {
			sections = append(sections, []Span{{label, st}})
		}
		if md.Kind == model.MediaMap {
			credit := theme.Style{FG: th.FG, Underline: true, URL: media.MapCopyrightURL}
			sections = append(sections, []Span{{media.MapAttribution, credit}})
		}
	}
	if m.Err != "" {
		sections = append(sections, []Span{{i18n.T("msg_failed", Clean(m.Err)), th.Style(theme.Error)}})
	}
	// ra : clickable reactions, placed after the columns are made.
	reactSec := -1
	if len(m.Reactions) > 0 && !m.Deleted {
		ra = reactActs(m.Reactions)
		accent := th.Style(theme.Accent)
		var spans []Span
		for i, a := range ra {
			if i > 0 {
				spans = append(spans, Span{"  ", dim})
			}
			st := dim
			if m.Reactions[i].Mine {
				st = accent
			}
			spans = append(spans, Span{a.label, st})
		}
		reactSec = len(sections)
		sections = append(sections, spans)
	}
	if len(sections) == 0 {
		sections = [][]Span{{}}
	}

	mk = msgMarks{reply: -1, label: -1, reactFirst: -1, reactLast: -1}
	for i, sec := range sections {
		if i == reactSec {
			mk.reactFirst = len(lines)
		}
		lines = append(lines, Wrap(sec, bw)...)
		if i == reactSec {
			mk.reactLast = len(lines)
		}
		if i == labelSec {
			mk.label = len(lines) - 1
			mk.labelText = Clean(m.Media.Label)
		}
		if i == replySec {
			mk.reply = len(lines) - 1
		}
	}
	return lines, mk, ra, quote
}

// msgImage : the media block of a message — the lines to add under the body, or
// the media to paint on top on hover (images_hover, no line kept free). Both are
// empty when nothing is to be shown (no media, images off, hidden video, cut
// link preview).
func msgImage(m *model.Msg, o Opts, indent int) (lines []Line, hover *model.Media) {
	if md := m.Media; md != nil && !m.Deleted && md.State == model.MediaReady && len(md.Frames) > 0 && o.Images != "off" &&
		(md.Kind != model.MediaWebPage || o.LinkPreviews) && // cut previews: the thumbnail goes with the block
		(md.Kind != model.MediaVideo || o.Video != "hidden") { // video = hidden: the label alone, "v" and "o" still work
		if o.ImagesHover {
			hover = md // painted on top on hover: nothing is kept free here
		} else {
			lines = imageLines(md, indent, o)
		}
	}
	return lines, hover
}

// paintMark : background of a selected, hovered or dragged-over message, and the
// ▌ column of the selected one alone. The image blocks and the avatars move with
// it. Half block spans keep their own background: it carries the bottom pixel.
func paintMark(lines []Line, th theme.Theme, sel bool) {
	mark := Span{selMark, theme.Style{FG: th.Color(theme.Accent), BG: th.Color(theme.CodeBG)}}
	for i := range lines {
		for k := range lines[i].Spans {
			// Kind 0 = no explicit background; never the one of the half
			// blocks, which carries the bottom pixel.
			if lines[i].Spans[k].Style.BG.Kind == 0 {
				lines[i].Spans[k].Style.BG = mark.Style.BG
			}
		}
		if !sel {
			continue // hover: the background is enough, the marker stays for the selection
		}
		lines[i].Spans = append([]Span{mark}, lines[i].Spans...)
		if lines[i].Img != nil {
			lines[i].Img.Col++ // the image block moves with the rest
		}
		if lines[i].Avatar != 0 {
			lines[i].AvatarCol++
		}
	}
}

const (
	maxLinkDesc = 4 // description lines of a link preview
	// MaxLinkRows : lines of the image block of a link preview. Exported: the UI
	// decodes the thumbnail in that box.
	MaxLinkRows = 20
)

// linkStyle : style of the label of a link preview — it carries the URL, and
// so the click (and the OSC 8 link). SafeURL filters: the URL goes out as it
// is in the terminal sequence as well as under xdg-open.
func linkStyle(md *model.Media, st theme.Style) theme.Style {
	if md.Kind == model.MediaWebPage && SafeURL(md.URL) {
		st.URL = md.URL
	}
	return st
}

// linkSections : "│" block of a link preview — label (clickable: it carries
// the URL) then description, maxLinkDesc lines at most. The lines are wrapped
// here and joined with '\n' so that each keeps its bar: the Wrap of the
// sections then only cuts where it is told to.
func linkSections(label, desc string, w int, st, head theme.Style) [][]Span {
	dw := max(w-2, 1)
	quote := func(ls []Line) string {
		out := make([]string, len(ls))
		for i, l := range ls {
			out[i] = "│ " + LineText(l)
		}
		return strings.Join(out, "\n")
	}
	secs := [][]Span{{{quote(Wrap([]Span{{label, head}}, dw)), head}}}
	if desc == "" {
		return secs
	}
	ls := Wrap([]Span{{desc, st}}, dw)
	if len(ls) > maxLinkDesc {
		ls = ls[:maxLinkDesc]
		last := &ls[maxLinkDesc-1]
		last.Spans = []Span{{truncate(LineText(*last)+"…", dw), st}}
	}
	return append(secs, []Span{{quote(ls), st}})
}

// Own tells whether m is my message (it can be edited and deleted).
func Own(m *model.Msg, myID int64) bool {
	return m.Out || (myID != 0 && m.FromID == myID)
}

// Actionable tells whether m takes the actions of the palette. A message
// still in flight (no id) or deleted cannot be edited, deleted nor quoted.
func Actionable(m *model.Msg) bool { return m.ID != 0 && !m.Deleted }

// Openable tells whether "o" has a target — a file to download, or the page
// of a link preview.
func Openable(md *model.Media) bool { return md != nil && (md.Loc != nil || SafeURL(md.URL)) }

// Playable : downloaded video, playable inline (key l).
func Playable(m *model.Msg) bool {
	md := m.Media
	return md != nil && md.Kind == model.MediaVideo && md.Path != ""
}

// Playing : inline play started — frames decoded or decoding running.
func Playing(md *model.Media) bool {
	return md != nil && md.Kind == model.MediaVideo && (len(md.Frames) > 1 || md.Want > 1)
}

// Stoppable : the play can be stopped ("s") — running, or paused somewhere
// other than the first frame. After "s" the video is at rest: "l" plays it
// again from the start and "s" has nothing left to do.
func Stoppable(md *model.Media) bool {
	return Playing(md) && !(md.Paused && md.Frame == 0)
}

// act : action before the columns are made; label is the text shown.
type act struct {
	key   rune
	label string
	emoji string
	id    int // KeyJump : message aimed at
}

// selActs : palette of the selected message. jump: search window, the result
// points back to its chat rather than to the message it quotes. caps: what the
// network of the message can do — an entry it has not is left out.
func selActs(m *model.Msg, own, jump bool, caps model.Caps) []act {
	var out []act
	switch {
	case jump && m.ID != 0:
		out = append(out, act{KeyJump, i18n.T("act_jump"), "", m.ID})
	case m.Reply != nil && m.Reply.ID != 0:
		out = append(out, act{KeyJump, i18n.T("act_quoted"), "", m.Reply.ID})
	}
	if Actionable(m) {
		if own {
			if caps.Edit {
				out = append(out, act{'e', i18n.T("act_edit"), "", 0})
			}
			out = append(out, act{'d', i18n.T("act_delete"), "", 0})
		}
		out = append(out, act{'p', i18n.T("act_reply"), "", 0})
		if caps.Reactions {
			out = append(out, act{'r', i18n.T("act_react"), "", 0})
		}
		// "i info" stays whatever the network: every backend answers what it can.
		out = append(out, act{'c', i18n.T("act_copy"), "", 0}, act{'i', i18n.T("act_info"), "", 0})
		if md := m.Media; Openable(md) {
			out = append(out, act{'o', i18n.T("act_open"), "", 0})
			if md.Previewable() {
				out = append(out, act{'v', i18n.T("act_view"), "", 0})
			}
			switch {
			case !Playable(m):
			case !Stoppable(md): // at rest
				out = append(out, act{'l', i18n.T("act_play"), "", 0})
			case md.Paused:
				out = append(out, act{'l', i18n.T("act_resume"), "", 0}, act{'s', i18n.T("act_stop"), "", 0})
			default:
				out = append(out, act{'l', i18n.T("act_pause"), "", 0}, act{'s', i18n.T("act_stop"), "", 0})
			}
		}
	}
	return append(out, act{KeyEsc, "Esc", "", 0})
}

func helpText(acts []act) string {
	labels := make([]string, len(acts))
	for i, a := range acts {
		labels[i] = a.label
	}
	return strings.Join(labels, " · ")
}

// reactActs : one action per reaction; raw emoji (the one sent back to the
// server), clean label (the one drawn).
func reactActs(rs []model.Reaction) []act {
	out := make([]act, 0, len(rs))
	for _, r := range rs {
		out = append(out, act{KeyReact, fmt.Sprintf("%s %d", Clean(r.Emoji), r.Count), r.Emoji, 0})
	}
	return out
}

// quickEmoji : quick reaction emoji offered on hover, brought back to the
// reactions the chat allows (allowed) — otherwise the server would refuse the
// click. allowed nil = restriction unknown, the guess goes through as it is.
func quickEmoji(m *model.Msg, allowed []string) string {
	e := guessEmoji(m)
	switch {
	case allowed == nil || slices.Contains(allowed, e):
		return e
	case len(allowed) == 0:
		return "" // chat with no reaction: nothing to offer
	}
	return allowed[0]
}

// guessEmoji gives my reaction when I have one (a click removes it), else the
// most frequent one, else a guess from the content. The words looked for below
// are read in the message text, in French and in English: they never follow
// the interface language.
func guessEmoji(m *model.Msg) string {
	best := -1
	for i, r := range m.Reactions {
		if r.Mine {
			return r.Emoji
		}
		if best < 0 || r.Count > m.Reactions[best].Count {
			best = i
		}
	}
	if best >= 0 {
		return m.Reactions[best].Emoji
	}
	t := " " + Fold(m.Text) + " " // spaces: "gg" is looked for as a whole word
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(t, s) {
				return true
			}
		}
		return false
	}
	switch {
	case strings.HasSuffix(strings.TrimSpace(t), "?"):
		return "🤔"
	case has("merci", "thanks"):
		return "🙏"
	case has("bravo", " gg ", "felicit"):
		return "👏"
	case has("lol", "mdr", "😂"):
		return "🤣" // 😂 is detected but never posted: not in the Telegram list
	case has("triste", "desole", "rip"):
		return "😢"
	}
	if md := m.Media; md != nil {
		switch md.Kind {
		case model.MediaPhoto, model.MediaVideo, model.MediaGIF:
			return "🔥"
		case model.MediaSticker:
			return "❤" // no variation selector: Telegram refuses ❤️
		}
	}
	return "👍"
}

// actionsIn gives the columns of the labels of acts in the drawn line. A
// label cut by the line break simply is not clickable.
func actionsIn(l Line, acts []act) []Action {
	text := ""
	for _, sp := range l.Spans {
		text += sp.Text
	}
	var out []Action
	from := 0
	for _, a := range acts {
		i := strings.Index(text[from:], a.label)
		if i < 0 {
			continue
		}
		i += from
		from = i + len(a.label)
		col := Width(text[:i])
		out = append(out, Action{Col0: col, Col1: col + Width(a.label), Key: a.key, Emoji: a.emoji, ID: a.id})
	}
	return out
}

// LineText : text of a line, styles flattened.
func LineText(l Line) string {
	var b strings.Builder
	for _, sp := range l.Spans {
		b.WriteString(sp.Text)
	}
	return b.String()
}

// lineWidth : drawn width of a line, in cells.
func lineWidth(l Line) int { return Width(LineText(l)) }

// imgBox : size in cells of the image block of a message indented by indent.
func imgBox(md *model.Media, indent int, o Opts) (cols, rows int) {
	return Box(md.FrameW, md.FrameH, min(o.MaxImgCols, o.Width-indent), o.MaxImgRows, o.CellW, o.CellH)
}

// HalfblockFrame : current frame of the media in half blocks, nil when the
// decoding fails. Used inline as well as on top (hover).
func HalfblockFrame(md *model.Media, cols, rows int) []Line {
	if len(md.Frames) == 0 {
		return nil
	}
	img, err := png.Decode(bytes.NewReader(md.Frames[md.Frame%len(md.Frames)]))
	if err != nil {
		return nil
	}
	return Halfblocks(img, cols, rows)
}

func imageLines(md *model.Media, indent int, o Opts) []Line {
	cols, rows := imgBox(md, indent, o)
	if cols < 1 || rows < 1 {
		return nil
	}
	if o.Images == "halfblock" {
		return HalfblockFrame(md, cols, rows)
	}
	// Img on each line of the block: the view can go back to its first line, and
	// no line of the block gets the indent of the prefix.
	lines := make([]Line, rows)
	for k := range lines {
		lines[k].Img = &Img{Media: md, Col: indent, Cols: cols, Rows: rows, Row: k}
	}
	return lines
}

// Box : size in cells of a w x h px image in maxCols x maxRows, aspect kept,
// never made bigger.
func Box(w, h, maxCols, maxRows, cellW, cellH int) (cols, rows int) {
	if w <= 0 || h <= 0 || cellW <= 0 || cellH <= 0 || maxCols <= 0 || maxRows <= 0 {
		return 0, 0
	}
	s := 1.0
	if f := float64(maxCols*cellW) / float64(w); f < s {
		s = f
	}
	if f := float64(maxRows*cellH) / float64(h); f < s {
		s = f
	}
	cols = int(math.Ceil(float64(w) * s / float64(cellW)))
	rows = int(math.Ceil(float64(h) * s / float64(cellH)))
	return min(max(cols, 1), maxCols), min(max(rows, 1), maxRows)
}

func Plain(text string, st theme.Style, width int) []Line {
	return Wrap([]Span{{Clean(text), st}}, width)
}

// Separator : ──── text ────
func Separator(text string, o Opts) Line {
	return sepLine(text, o.Theme.Style(theme.Sep), o.Width)
}

// Redline : last-read line (/set redline), red (theme.Error = palette[1])
// rather than grey, full width like the other separators.
func Redline(o Opts) Line {
	return sepLine(i18n.T("redline_label"), o.Theme.Style(theme.Error), o.Width)
}

func sepLine(text string, st theme.Style, width int) Line {
	w := width - Width(text) - 2
	if w < 2 {
		return Line{Spans: []Span{{text, st}}}
	}
	left := w / 2
	return Line{Spans: []Span{{strings.Repeat("─", left) + " " + text + " " + strings.Repeat("─", w-left), st}}}
}

func truncate(s string, w int) string {
	if w < 1 {
		w = 1
	}
	return Truncate(s, w, "…")
}

// LongDate gives the long form of a date, in the language in use.
func LongDate(t time.Time) string {
	months := strings.Split(i18n.T("months"), ",")
	return i18n.T("long_date", t.Day(), months[t.Month()-1], t.Year())
}

// HumanSize gives a readable size ("240 KB"), used by the backends and the UI.
func HumanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return strings.Replace(i18n.T("size_mb", float64(n)/(1<<20)), ".", i18n.T("decimal_sep"), 1)
	case n >= 1<<10:
		return i18n.T("size_kb", n>>10)
	}
	return i18n.T("size_b", n)
}
