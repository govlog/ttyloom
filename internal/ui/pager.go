package ui

import (
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

// pager : lines waiting to be shown in a window.
type pager struct {
	w    *Window
	rest []render.Line
}

// Next takes out and gives back up to n lines.
func (p *pager) Next(n int) []render.Line {
	if n > len(p.rest) {
		n = len(p.rest)
	}
	out := p.rest[:n]
	p.rest = p.rest[n:]
	return out
}

func (p *pager) Done() bool { return len(p.rest) == 0 }

// emit shows lines in w: everything when it fits in the view, else one page
// and the rest in u.pager (only one pager at a time; a new emit replaces the old).
// page/view are bounded to 1 at least: a tiny terminal (Rows <= 3) must
// neither panic on a negative slice nor block the pager on a page of 0 lines.
func (u *UI) emit(w *Window, lines []render.Line) {
	if len(lines) == 0 {
		return
	}
	page := max(u.viewRows()-1, 1)
	view := page + 1
	if len(lines) <= view {
		w.AddLines(lines)
		u.pager = nil
		return
	}
	w.AddLines(lines[:page])
	u.pager = &pager{w: w, rest: lines[page:]}
}

// pagerKey handles one key while a pager is active. true: key taken by the
// pager. false: to be handled as usual (mouse).
func (u *UI) pagerKey(k term.Key) bool {
	p := u.pager
	page := max(u.viewRows()-1, 1)
	switch {
	case k.Code == term.Mouse:
		p.w.AddLines(p.Next(len(p.rest)))
		u.pager = nil
		return k.Mouse.Button < 64 // click: stale hits, swallowed; wheel: goes through
	case k.Code == term.Enter, k.Code == term.None && k.Rune == ' ':
		p.w.AddLines(p.Next(page))
	case k.Code == term.None && k.Rune == 'q':
		p.w.AddLines(p.Next(len(p.rest)))
	case k.Code == term.Esc:
		u.pager = nil
		return true
	default:
		return true
	}
	if p.Done() {
		u.pager = nil
	}
	return true
}
