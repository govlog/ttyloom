package ui

import (
	"strings"

	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/theme"
)

// Frame of the centred overlays (emoji picker, theme picker, new chat, QR
// login, global search): the same ┌─┐ / │…│ / └─┘ box, drawn in one place.
// The context menu (menu.go) and the member box (parts.go) carry their title
// in the border and are not built here.

// boxStyles gives the three styles the boxes share: the body (background of
// the box), the borders and the inverted current line.
func boxStyles(th theme.Theme) (body, edge, sel theme.Style) {
	body = theme.Style{FG: th.FG, BG: th.Color(theme.CodeBG)}
	edge, sel = body, body
	edge.FG = th.Color(theme.Sep)
	sel.Reverse = true
	return body, edge, sel
}

// boxDraw draws the lines of a box: inner = columns between the two vertical
// borders, fill = style of the padding added to a row that does not reach
// inner (a box too narrow for its cells).
type boxDraw struct {
	edge, fill theme.Style
	inner      int
}

// bar : horizontal border, l and r being its two corners.
func (b boxDraw) bar(l, r string) render.Line {
	return render.Line{Spans: []render.Span{{Text: l + strings.Repeat("─", b.inner) + r, Style: b.edge}}}
}

// row : one line of the box, borders around spans that already make inner
// columns; a short row is padded with b.fill.
func (b boxDraw) row(sp ...render.Span) render.Line {
	out := []render.Span{{Text: "│", Style: b.edge}}
	w := 0
	for _, s := range sp {
		w += render.Width(s.Text)
		out = append(out, s)
	}
	if w < b.inner {
		out = append(out, render.Span{Text: strings.Repeat(" ", b.inner-w), Style: b.fill})
	}
	return render.Line{Spans: append(out, render.Span{Text: "│", Style: b.edge})}
}

// text : one line of text in st, cut or filled to inner.
func (b boxDraw) text(s string, st theme.Style) render.Line {
	return b.row(render.Span{Text: padTo(s, b.inner), Style: st})
}
