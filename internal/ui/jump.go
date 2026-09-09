package ui

import (
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/theme"
)

// Scrolled up in the history, a pill at the bottom right of the message area
// (" ↓ last message ") brings back to the end on a click. It is drawn as an
// overlay box after the images, and steps aside for one placed over its
// cells; the wheel over it still scrolls.

func (u *UI) jumpLabel() string { return " " + i18n.T("jump_last") + " " }

// jumpRect : the box of the pill; ok false when the view sits at the end.
func (u *UI) jumpRect() (rect, bool) {
	if u.view().Scroll <= 0 {
		return rect{}, false
	}
	x0, cols := u.layout()
	w := render.Width(u.jumpLabel())
	return rect{row: u.viewRows() - 1, col: x0 + cols - w, h: 1, w: w}, true
}

func (u *UI) jumpLines() []render.Line {
	st := theme.Style{FG: u.th.BG, BG: u.th.Color(theme.Accent), Bold: true}
	return []render.Line{{Spans: []render.Span{{Text: u.jumpLabel(), Style: st}}}}
}
