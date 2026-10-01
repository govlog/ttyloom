package ui

import (
	"slices"
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// Typing in the sidebar: while the panel has the keyboard — the pointer came
// over it (follow-mouse), or Shift+Tab — the letters filter its list, the
// arrows move a cursor and Enter opens the line under it, then gives the
// keyboard back to the input line.

// sideFind : state of the typing in the sidebar.
type sideFind struct {
	keys bool        // the sidebar has the keyboard
	q    string      // filter; "" = every line
	chat *model.Chat // cursor of the chat mode; nil = on the current line
	win  *Window     // cursor of the windows mode; nil = on the current line
}

// sideHasKeys : the keys go to the sidebar — it has the keyboard, it is drawn,
// and no mode holds the input line (login answer, Ctrl+F, y/n question, paste
// or send confirmation): those keep the keys, the cursor and the highlight.
func (u *UI) sideHasKeys() bool {
	if !u.find.keys || u.prompt != nil || u.search != nil || u.ask != nil || u.pasteAsk != "" || u.sendAsk != nil {
		return false
	}
	x0, _ := u.layout()
	return x0 > 0
}

// sideFocus : Shift+Tab — the keyboard goes to the sidebar, or back to the
// input line. The filter stays until Escape, Enter, a click or the wheel.
func (u *UI) sideFocus() {
	if x0, _ := u.layout(); x0 == 0 {
		return // no sidebar drawn: nothing to give the keyboard to
	}
	u.find.keys = !u.find.keys
}

// sideKey : a key while the sidebar has the keyboard. false for the keys it
// leaves to their usual path (F keys, Ctrl, Alt, Tab, paste, mouse).
func (u *UI) sideKey(k term.Key) bool {
	if k.Alt || k.Ctrl {
		return false
	}
	switch {
	case k.Code == term.Esc: // the filter goes, the keyboard goes back to the input line
		u.sideDone()
	case k.Code == term.Up:
		u.sideMove(-1)
	case k.Code == term.Down:
		u.sideMove(1)
	case k.Code == term.Enter && !k.Shift:
		u.sideOpen()
	case k.Code == term.Backspace:
		if r := []rune(u.find.q); len(r) > 0 {
			u.sideFilter(string(r[:len(r)-1]))
		}
	case k.Code == term.None && k.Rune != 0:
		u.sideFilter(u.find.q + string(k.Rune))
	default:
		return false
	}
	return true
}

// sideFilter sets the filter: the list starts again from its top, the cursor
// on its first line — the one Enter opens. An empty filter puts the cursor
// back on the current line.
func (u *UI) sideFilter(q string) {
	u.find.q, u.find.chat, u.find.win = q, nil, nil
	u.sideScroll = 0
	if q == "" {
		u.sideReveal()
		return
	}
	if ls, _ := u.sideLines(); len(ls) > 0 {
		u.sideSel(ls[0])
	}
}

// sideDone : the typing in the sidebar is over — Escape, Enter, a click on a
// line, the wheel. Filter, cursor and keyboard go back to rest; the scroll
// bounds itself to the whole list again.
func (u *UI) sideDone() {
	u.find = sideFind{}
	u.sideWheel(0)
}

// sideLine : a line of the sidebar list the cursor can stand on — a chat or a
// window, never a section header.
type sideLine struct {
	row  int         // rank in the list drawn, headers included
	chat *model.Chat // chat mode
	win  *Window     // windows mode
}

// sideLines gives the lines the cursor can stand on, in the drawn order, and
// the rank among them of the cursor — of the current line while the cursor has
// not moved; -1 when it is not in the list.
func (u *UI) sideLines() (out []sideLine, at int) {
	at = -1
	switch u.side {
	case sideChats:
		cur := u.find.chat
		if cur == nil {
			cur = u.ws.Current().Chat
		}
		for i, r := range u.sideRowList() {
			if r.chat == nil {
				continue // section header
			}
			if r.chat == cur {
				at = len(out)
			}
			out = append(out, sideLine{row: i, chat: r.chat})
		}
	case sideWindows:
		cur := u.find.win
		if cur == nil {
			cur = u.ws.Current()
		}
		for i, wi := range u.sideWins() {
			if wi < 0 {
				continue // header of the split list
			}
			if w := u.ws.List[wi]; w == cur {
				at = len(out)
			}
			out = append(out, sideLine{row: i, win: u.ws.List[wi]})
		}
	}
	return out, at
}

// sideMove : Up and Down — the cursor goes to the previous or next line, with
// a clamp at both ends like the wheel. It opens nothing.
func (u *UI) sideMove(d int) {
	ls, at := u.sideLines()
	if j := stepIdx(at, d, len(ls)); j >= 0 {
		u.sideSel(ls[j])
	}
}

// sideSel puts the cursor on l and scrolls the list to it.
func (u *UI) sideSel(l sideLine) {
	u.find.chat, u.find.win = l.chat, l.win
	u.sideShow(l.row)
}

// sideOpen : Enter — the line under the cursor opens and the keyboard goes back
// to the input line: what is typed next goes to that chat. A cursor that never
// moved (no filter, no arrow) opens nothing.
func (u *UI) sideOpen() {
	ls, at := u.sideLines()
	moved := u.find.chat != nil || u.find.win != nil
	u.sideDone()
	if !moved || at < 0 {
		return
	}
	if l := ls[at]; l.chat != nil {
		u.openChat(l.chat)
	} else if i := slices.Index(u.ws.List, l.win); i >= 0 {
		u.goTo(i)
	}
}

// foldState : the folded sections as the list draws them — none while a
// filter runs: a hit must never hide under a folded header.
func (u *UI) foldState() map[string]bool {
	if u.folded != nil && u.find.q != "" {
		return map[string]bool{}
	}
	return u.folded
}

// winMatch : the window w answers the folded filter q of the sidebar — by its
// chat, as in the chat mode, or by the name drawn.
func (u *UI) winMatch(w *Window, q string) bool {
	if q == "" || strings.Contains(render.Fold(winName(w, u.title)), q) {
		return true
	}
	return w.Chat != nil && w.Search == "" && ncMatch(w.Chat, q, u.title)
}

// sideFindText gives the filter as the rule under the sidebar header shows
// it, cut from the left so that its end — what is being typed — stays in view,
// and the column of that end: the terminal cursor goes there.
func (u *UI) sideFindText() (string, int) {
	text := []rune(i18n.T("sidebar_filter", render.CleanLine(u.find.q)))
	shown, w, _ := hwindow(text, len(text), max(1, u.sideW-1))
	return shown, w
}

// hitSpans gives the title span of a sidebar line with the hits of the filter
// q set apart, bold and underlined — in the accent colour, or in the colours
// of the line when it is inverted (current line, cursor).
func hitSpans(sp render.Span, q string, th theme.Theme) []render.Span {
	if q == "" {
		return []render.Span{sp}
	}
	st := sp.Style
	st.Bold, st.Underline = true, true
	if !st.Reverse {
		st.FG = th.Color(theme.Accent)
	}
	return highlight(render.Line{Spans: []render.Span{sp}}, q, st).Spans
}
