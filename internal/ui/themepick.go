package ui

import (
	"slices"
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// Theme picker (/theme with no argument): centred overlay, vertical list
// filtered by the typing. Each move applies the theme at once (live preview);
// Enter keeps it, Esc gives the first one back.

type themePicker struct {
	all   []string // every name
	names []string // names shown, filter applied
	query []rune
	cur   int
	w     int         // width of the box, borders included
	rows  int         // list lines shown
	orig  theme.Theme // theme at opening time, restored by Esc
	// load : injected by the tests, theme.Load for real.
	load func(string) (theme.Theme, error)
}

// newThemePicker lists names, with the theme cur preselected. The box is 40
// columns and at most rows-4 screen lines (borders, header and foot included).
func newThemePicker(names []string, cur string, th theme.Theme, cols, rows int) *themePicker {
	p := &themePicker{all: names, names: names, load: theme.Load, orig: th,
		w:    min(40, max(4, cols)),
		rows: max(1, min(len(names), rows-8))}
	p.cur = max(0, slices.IndexFunc(names, func(n string) bool { return strings.EqualFold(n, cur) }))
	return p
}

// themeFilter gives the names that hold q, accents and case ignored.
func themeFilter(names []string, q string) []string {
	q = render.Fold(q)
	if q == "" {
		return names
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if strings.Contains(render.Fold(n), q) {
			out = append(out, n)
		}
	}
	return out
}

// filter : list computed again; the selected theme is kept when it survives.
func (p *themePicker) filter() {
	sel := p.name()
	p.names = themeFilter(p.all, string(p.query))
	p.cur = max(0, slices.Index(p.names, sel))
}

func (p *themePicker) name() string {
	if p.cur < len(p.names) {
		return p.names[p.cur]
	}
	return ""
}

func (p *themePicker) move(d int) { p.cur = max(0, min(p.cur+d, len(p.names)-1)) }

func (p *themePicker) height() int { return p.rows + 4 }

// top : first name shown, the current one at mid height.
func (p *themePicker) top() int {
	return min(max(0, p.cur-p.rows/2), max(0, len(p.names)-p.rows))
}

// Lines : the whole box, one render.Line per screen line; the current name is
// inverted.
func (p *themePicker) Lines(th theme.Theme) []render.Line {
	box, edge, sel := boxStyles(th)
	b := boxDraw{edge: edge, fill: box, inner: p.w - 2}
	out := make([]render.Line, 0, p.rows+4)
	out = append(out, b.bar("┌", "┐"), b.text(i18n.T("theme_picker_head", string(p.query)), box))
	for i, top := 0, p.top(); i < p.rows; i++ {
		st, s := box, ""
		if j := top + i; j < len(p.names) {
			s = render.CleanLine(p.names[j]) // file name: never drawn raw
			if j == p.cur {
				st = sel
			}
		}
		out = append(out, b.text(s, st))
	}
	return append(out, b.text(i18n.T("theme_picker_keys"), edge), b.bar("└", "┘"))
}

// themeRect : box of the picker, centred.
func (u *UI) themeRect() rect {
	return centerRect(u.t.Cols, u.t.Rows, u.themePick.w, u.themePick.height())
}

// openThemePicker : /theme with no argument. "terminal" is not a file: it is
// added to the list of the themes found on the disk.
func (u *UI) openThemePicker() {
	names := theme.Names()
	if !slices.Contains(names, "terminal") {
		names = append([]string{"terminal"}, names...)
	}
	u.themePick = newThemePicker(names, u.th.Name, u.th, u.t.Cols, u.t.Rows)
}

// themeApply : preview of the selected theme. clear() is enough to repaint
// everything — the kitty images stay in place.
func (u *UI) themeApply() {
	n := u.themePick.name()
	if n == "" || strings.EqualFold(n, u.th.Name) {
		return
	}
	th, err := u.themePick.load(n)
	if err != nil {
		u.sys(err.Error())
		return
	}
	u.th = th
	u.clear()
}

// themeKeep : Enter — the theme shown becomes the one of the configuration.
func (u *UI) themeKeep() {
	u.themePick = nil
	u.cfg.Theme = u.th.Name
	if !u.saveCfg() {
		return
	}
	u.sys(i18n.T("theme_picker_head", u.th.Name))
}

// themeCancel : Esc — the first theme is given back, nothing is saved.
func (u *UI) themeCancel() {
	u.th, u.themePick = u.themePick.orig, nil
	u.clear()
}

// themeList : the theme picker seen as a list overlay. Every move applies the
// theme shown (preview), Enter keeps it, Esc and a click outside give the
// first one back.
func (u *UI) themeList() listOverlay {
	p := u.themePick
	return listOverlay{r: u.themeRect(), head: 2, rows: p.rows, top: p.top(), n: len(p.names),
		move:  func(d int) { p.move(d); u.themeApply() },
		click: func(i int) { p.cur = i; u.themeApply() },
		enter: u.themeKeep, close: u.themeCancel}
}

// themeKey : the shared navigation, then the filter typing; every move applies
// the theme.
func (u *UI) themeKey(k term.Key) {
	if u.themeList().key(k) {
		return
	}
	p := u.themePick
	switch {
	case k.Code == term.Backspace:
		if n := len(p.query); n > 0 {
			p.query = p.query[:n-1]
			p.filter()
		}
	case k.Code == term.None && k.Rune != 0 && !k.Alt:
		p.query = append(p.query, k.Rune)
		p.filter()
	default:
		return // no other key goes through
	}
	u.themeApply()
}

// themeMouse : wheel and click move (and so apply), a click outside the box
// cancels.
func (u *UI) themeMouse(e term.MouseEvent) { u.themeList().mouse(e) }
