package ui

import (
	"strings"
	"time"

	"rsc.io/qr"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/theme"
)

// QR code login: centred overlay, shown while the backend waits for Enter on the
// authentication prompt. The image goes out in kitty when the terminal can
// carry it, in half blocks otherwise (two modules per screen line); in both
// cases black on white is forced — a QR in the theme colours cannot be read by
// a phone.
//
// The overlay takes no key: the prompt (u.prompt) is in charge, Enter switches
// to the phone flow and Ctrl+C quits, as everywhere.

const (
	qrQuiet  = 2       // modules of quiet zone around the code
	qrChrome = 5       // lines of the box outside the QR: 2 borders, title, help, foot
	qrPID    = 1 << 22 // kitty placement: outside the line, hover (1<<23) and avatar (1<<24) ranges
)

// qrHint, qrKeys : text lines of the box, per network (where the phone finds
// the scanner, what Enter does), read at each drawing — a /set lang changes
// them with no restart.
func qrHint(net string) string { return i18n.T("qr_hint_" + net) }
func qrKeys(net string) string { return i18n.T("qr_keys_" + net) }

var (
	qrFG = theme.Color{Kind: 2}                                            // black
	qrBG = theme.Color{Kind: 2, RGB: theme.RGB{R: 0xff, G: 0xff, B: 0xff}} // white
)

type qrBox struct {
	net     string // network that asked: its own hint and key lines
	expires time.Time
	bits    [][]bool     // modules, quiet zone included
	png     []byte       // the same code, for kitty
	md      *model.Media // carrier of the kitty id; it survives a token renewal
	shown   int          // seconds shown at the last repaint (rate of the tick)
}

// newQRBox encodes the login URL. It is not kept: the token is worth an open
// session, and nothing must be able to log it after the fact. nil when the
// encoding fails (wild URL).
func newQRBox(url string, expires time.Time) *qrBox {
	c, err := qr.Encode(url, qr.M)
	if err != nil {
		return nil
	}
	return &qrBox{expires: expires, bits: qrBits(c, qrQuiet), png: c.PNG(),
		md: &model.Media{Kind: model.MediaPhoto}}
}

// qrBits gives the modules of the code, white quiet zone included.
func qrBits(c *qr.Code, quiet int) [][]bool {
	n := c.Size + 2*quiet
	out := make([][]bool, n)
	for y := range out {
		out[y] = make([]bool, n)
		for x := range out[y] {
			out[y][x] = c.Black(x-quiet, y-quiet) // outside the code = white
		}
	}
	return out
}

// qrHalfblocks gives two lines of modules per screen line (▀ the top, ▄ the
// bottom, █ both). One style only, so one line = one span.
func qrHalfblocks(bits [][]bool) []render.Line {
	if len(bits) == 0 {
		return nil
	}
	st := theme.Style{FG: qrFG, BG: qrBG}
	at := func(y, x int) bool { return y < len(bits) && bits[y][x] }
	out := make([]render.Line, (len(bits)+1)/2)
	for i := range out {
		var b strings.Builder
		for x := range bits[0] {
			switch top, bot := at(2*i, x), at(2*i+1, x); {
			case top && bot:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bot:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		out[i] = render.Line{Spans: []render.Span{{Text: b.String(), Style: st}}}
	}
	return out
}

func (q *qrBox) cols() int {
	if len(q.bits) == 0 {
		return 0
	}
	return len(q.bits[0])
}

// rows gives the screen lines of the code — the same footprint in kitty and in
// half blocks. A cell is about twice as tall as it is wide, so cols x cols/2
// cells draw a square, which is what the reader expects.
func (q *qrBox) rows() int { return (len(q.bits) + 1) / 2 }

func (q *qrBox) inner() int {
	return max(q.cols(), render.Width(qrHint(q.net)), render.Width(qrKeys(q.net)))
}

func (q *qrBox) width() int { return q.inner() + 2 }

// height : the code is replaced by an apology line when the whole box does not
// fit — a cut QR would be unreadable, and going past the edge would scroll the
// terminal.
func (q *qrBox) height(rows int) int {
	if h := q.rows() + qrChrome; h <= rows {
		return h
	}
	return 1 + qrChrome
}

func (q *qrBox) fits(cols, rows int) bool { return q.rows()+qrChrome <= rows && q.width() <= cols }

// left gives the seconds before the token expires, never negative.
func (q *qrBox) left(now time.Time) int {
	return max(0, int(q.expires.Sub(now)/time.Second))
}

// Lines : the whole box. kitty: the lines of the code stay white, the
// placement of the image covers them (see draw).
func (q *qrBox) Lines(th theme.Theme, cols, rows int, kitty bool) []render.Line {
	box, edge, _ := boxStyles(th)
	acc, white := box, theme.Style{FG: qrFG, BG: qrBG}
	acc.FG, acc.Bold = th.Color(theme.Accent), true
	b := boxDraw{edge: edge, fill: box, inner: q.inner()}
	// The countdown fits in the title: one line less, and the box still fits
	// on a terminal of 24 lines.
	title := i18n.T("qr_title", q.left(time.Now()))
	if !q.expires.After(time.Now()) {
		title = i18n.T("qr_title_renew")
	}
	out := []render.Line{b.bar("┌", "┐"), b.text(title, acc), b.text(qrHint(q.net), box)}
	switch pad := (b.inner - q.cols()) / 2; {
	case !q.fits(cols, rows):
		out = append(out, b.text(i18n.T("qr_too_small"), box))
	case kitty:
		for range q.rows() {
			out = append(out, b.text("", white))
		}
	default:
		for _, l := range qrHalfblocks(q.bits) {
			out = append(out, b.row(render.Span{Text: strings.Repeat(" ", pad), Style: white},
				l.Spans[0], render.Span{Text: strings.Repeat(" ", b.inner-pad-q.cols()), Style: white}))
		}
	}
	return append(out, b.text(qrKeys(q.net), box), b.bar("└", "┘"))
}

// centerRect : w x h rectangle centred in a cols x rows screen.
func centerRect(cols, rows, w, h int) rect {
	return rect{row: max(0, (rows-h)/2), col: max(0, (cols-w)/2), h: h, w: w}
}

func (u *UI) qrRect() rect {
	h, rows := u.qr.height(u.t.Rows), u.t.Rows
	if h+2 <= rows {
		rows -= 2 // the status bar and the input line stay visible
	}
	return centerRect(u.t.Cols, rows, u.qr.width(), h)
}

// qrImage gives the top left corner (origin 0) and the size in cells of the
// kitty image of the code, false when it has no reason to be.
func (u *UI) qrImage() (row, col, cols, rows int, ok bool) {
	q := u.qr
	if q == nil || u.images != "kitty" || !q.fits(u.t.Cols, u.t.Rows) {
		return 0, 0, 0, 0, false
	}
	r := u.qrRect()
	return r.row + 3, r.col + 1 + (q.inner()-q.cols())/2, q.cols(), q.rows(), true
}

// setQR : new token (first display or renewal). The old kitty image is freed
// only at the end of the frame, after the new one is placed: no hole on the
// screen.
func (u *UI) setQR(e model.EvQR) {
	q := newQRBox(e.URL, e.Expires)
	if q == nil {
		u.status0(i18n.T("qr_encode_failed"))
		return
	}
	q.net = u.dispatchNet
	if old := u.qr; old != nil {
		q.md = old.md
		u.retireKitty(q.md)
	}
	u.qr = q
	u.clear()
}

// closeQR : QR scanned, given up or failed.
func (u *UI) closeQR() {
	q := u.qr
	if q == nil {
		return
	}
	u.qr = nil
	s := u.kittyFree(q.md) // ids and LRU cleaned even when the mode has changed
	if u.t.Kitty {
		u.t.WriteString(s)
	}
	u.clear()
}
