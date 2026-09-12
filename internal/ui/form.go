package ui

import (
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// Form overlay: a centred box of labelled fields (/irc add). Tab, Down and
// Up move between the fields, Enter submits, Esc and Ctrl+C close. A secret
// field shows dots. The submit callback answers with an error text that
// keeps the box open, or "" to close it.

const (
	formW      = 56 // width of the box, borders included
	formLabelW = 18 // label column, before ": "
)

type formField struct {
	label  string
	val    []rune
	secret bool
	// choices : presets the field cycles through with ← → or Space; sel is
	// the one shown (-1 = free text). Typing goes back to free text.
	choices []string
	sel     int
}

type formBox struct {
	title  string
	fields []formField
	cur    int
	err    string
	submit func(vals []string) string
	// pick : a preset chosen on field f (choice i) — the caller fills the
	// other fields from it.
	pick func(f, i int)
}

// values gives the text of every field, in order, trimmed.
func (f *formBox) values() []string {
	out := make([]string, len(f.fields))
	for i, fd := range f.fields {
		out[i] = strings.TrimSpace(string(fd.val))
	}
	return out
}

// height : lines of the box, borders included — title, the fields, one line
// of error or help.
func (f *formBox) height() int { return len(f.fields) + 4 }

// move : field cur+d, bounded.
func (f *formBox) move(d int) { f.cur = min(max(f.cur+d, 0), len(f.fields)-1) }

// key handles one key; true when the box is done (submitted or closed).
func (f *formBox) key(k term.Key) bool {
	switch {
	case k.Code == term.Esc, k.Code == term.Ctrl && k.Rune == 'c':
		return true
	case k.Code == term.Enter:
		f.err = f.submit(f.values())
		return f.err == ""
	case k.Code == term.Tab && k.Shift, k.Code == term.Up:
		f.move(-1)
	case k.Code == term.Tab, k.Code == term.Down:
		f.move(1)
	case len(f.fields[f.cur].choices) > 0 && (k.Code == term.Right || k.Code == term.Left || k.Code == term.None && k.Rune == ' '):
		d := 1
		if k.Code == term.Left {
			d = -1
		}
		f.cycle(d)
	case k.Code == term.Backspace:
		fd := &f.fields[f.cur]
		fd.sel = -1
		if len(fd.val) > 0 {
			fd.val = fd.val[:len(fd.val)-1]
		}
	case k.Code == term.Ctrl && k.Rune == 'u':
		f.fields[f.cur].val, f.fields[f.cur].sel = nil, -1
	case k.Code == term.None && k.Rune != 0 && !k.Alt:
		fd := &f.fields[f.cur]
		if fd.sel >= 0 { // a preset was shown: the typing starts a free text
			fd.val, fd.sel = nil, -1
		}
		fd.val = append(fd.val, k.Rune)
	case k.Code == term.Paste:
		f.fields[f.cur].val = append(f.fields[f.cur].val, []rune(render.CleanLine(k.Text))...)
	}
	return false
}

// cycle shows the next (d=1) or previous preset of the current field, the
// free text being one stop of the ring, and tells pick.
func (f *formBox) cycle(d int) {
	fd := &f.fields[f.cur]
	n := len(fd.choices)
	fd.sel = ((fd.sel+1+d)%(n+1)+(n+1))%(n+1) - 1 // -1 .. n-1
	if fd.sel < 0 {
		fd.val = nil
		return
	}
	fd.val = []rune(fd.choices[fd.sel])
	if f.pick != nil {
		f.pick(f.cur, fd.sel)
	}
}

// Lines draws the box; w is its width, borders included.
func (f *formBox) Lines(th theme.Theme, w int) []render.Line {
	box, edge, sel := boxStyles(th)
	dim, errSt := box, box
	dim.FG, errSt.FG = th.Color(theme.Dim), th.Color(theme.Error)
	inner := max(1, w-2)
	b := boxDraw{edge: edge, fill: box, inner: inner}
	out := make([]render.Line, 0, f.height())
	out = append(out, b.bar("┌", "┐"), b.text(f.title, box))
	for i, fd := range f.fields {
		v := string(fd.val)
		if fd.secret {
			v = strings.Repeat("•", len(fd.val))
		}
		st := box
		if i == f.cur {
			st = sel
		}
		label := padTo(fd.label, formLabelW) + ": "
		if len(fd.choices) > 0 { // a choice field: the arrows say it cycles
			if fd.sel >= 0 {
				v = fd.choices[fd.sel] // the preset with its label, the value being the host alone
			}
			v = "‹ " + v + " ›"
		}
		out = append(out, b.row(
			render.Span{Text: label, Style: st},
			render.Span{Text: padTo(render.CleanLine(v), max(0, inner-render.Width(label))), Style: st},
		))
	}
	switch {
	case f.err != "":
		out = append(out, b.text(f.err, errSt))
	default:
		out = append(out, b.text(i18n.T("form_help"), dim))
	}
	return append(out, b.bar("└", "┘"))
}

// formRect : box of the overlay, centred.
func (u *UI) formRect() rect {
	w := max(30, min(u.t.Cols-4, formW))
	h := min(u.t.Rows, u.form.height())
	return centerRect(u.t.Cols, u.t.Rows, w, h)
}

func (u *UI) formKey(k term.Key) {
	if u.form.key(k) {
		u.form = nil
	}
}

// formMouse : a click outside closes, one on a field row selects it.
func (u *UI) formMouse(e term.MouseEvent) {
	r := u.formRect()
	switch {
	case !r.hits(e.Y, e.X, 1, 1):
		u.form = nil
	case e.Button == 0:
		if i := e.Y - r.row - 2; i >= 0 && i < len(u.form.fields) {
			u.form.cur = i
		}
	}
}
