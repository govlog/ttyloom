package ui

import (
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/intro"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// The demo of the start (internal/intro): ASCII effects and the logo over the
// chat, which lives on behind them, until the first key.

const (
	introMinCols, introMinRows = 40, 12
	// introIdle : with no key nor click for that long, the demo ends by itself
	// on a terminal that never reported the focus (tmux without focus-events,
	// screen): forgotten in a hidden tab, it must not play forever. Where the
	// focus is reported, a hidden demo is paused and plays until a key.
	introIdle = 10 * time.Minute
)

type introRun struct {
	show   *intro.Show
	layer  *intro.Layer
	dark   float32
	pace   intro.Pacer
	timer  *time.Timer
	paused bool
	input  time.Time // last key or click (the start at first)
}

// introStart starts the demo; false, and nothing starts, on a screen under
// 40 × 12.
func (u *UI) introStart(now time.Time) bool {
	if u.t.Cols < introMinCols || u.t.Rows < introMinRows {
		return false
	}
	if u.intro != nil {
		u.intro.timer.Stop()
	}
	u.intro = &introRun{show: intro.NewShow(uint64(now.UnixNano()), now, i18n.T("intro_hint"), u.build.Version),
		timer: time.NewTimer(0), input: now}
	if u.focusOut { // a hidden terminal: the demo waits for the focus
		u.intro.show.Pause(now)
		u.intro.paused = true
		u.intro.timer.Stop()
	}
	return true
}

// introKey : the demo looks at an event before the UI. true: the event was
// the demo's (a key, a paste, a click, the wheel), swallowed: the first one
// starts the fade-out, the next one ends it at once. The focus and the mouse
// moves go on to the UI.
func (u *UI) introKey(k term.Key, now time.Time) bool {
	r := u.intro
	switch {
	case k.Code == term.FocusOut:
		r.show.Pause(now)
		r.paused = true
		r.timer.Stop()
		return false
	case k.Code == term.FocusIn:
		u.introResume(now)
		return false
	case k.Code == term.Mouse && (k.Mouse.Motion || !k.Mouse.Press):
		return false
	}
	r.input = now
	if r.show.Stopping() {
		u.introEnd()
		return true
	}
	u.introResume(now) // a key with no focus event first: the fade must run
	r.show.Stop(now)
	return true
}

func (u *UI) introResume(now time.Time) {
	if r := u.intro; r.paused {
		r.show.Resume(now)
		r.paused = false
		r.timer.Reset(0)
	}
}

// noteInput : every event of the terminal goes by here first. A key, a
// click, the wheel or a paste is activity, for the screensaver; a mouse move
// is not. A focus event tells that the terminal reports the focus.
func (u *UI) noteInput(k term.Key, now time.Time) {
	switch {
	case k.Code == term.FocusIn || k.Code == term.FocusOut:
		u.focusSeen, u.focusOut = true, k.Code == term.FocusOut
	case k.Code == term.Mouse && k.Mouse.Motion:
	default:
		u.lastInput = now
	}
}

// saverDelay : the idle time before the screensaver; 0: off.
func (u *UI) saverDelay() time.Duration {
	d, err := time.ParseDuration(u.cfg.Screensaver)
	if err != nil || d < 0 {
		return 0
	}
	return d
}

// saverCheck starts the demo as a screensaver after saverDelay with no
// activity — once per idle spell, and never over a prompt, the viewer or mpv.
// true: it started.
func (u *UI) saverCheck(now time.Time) bool {
	d := u.saverDelay()
	if d == 0 || u.intro != nil || now.Sub(u.lastInput) < d || u.saverFrom.Equal(u.lastInput) ||
		u.introBlocked() || u.viewer != nil || u.t.Held() {
		return false
	}
	if !u.introStart(now) {
		return false
	}
	u.saverFrom = u.lastInput
	return true
}

// introStep : the next frame of the demo, or its end — when the UI needs the
// user (a login prompt, a QR code, a form must never be hidden), at the end
// of its fade-out, or after introIdle with no key.
func (u *UI) introStep(now time.Time) {
	r := u.intro
	if r == nil {
		return
	}
	if u.introBlocked() || r.show.Done(now) {
		u.introEnd()
		return
	}
	if !u.focusSeen && now.Sub(r.input) >= introIdle {
		r.show.Stop(now)
	}
	r.layer, r.dark = r.show.Frame(now, u.introCanvas())
}

// introBlocked : the UI waits for the user — a login prompt, a QR code, a
// form — and the demo must not hide it. Checked by every frame of the demo
// and by every draw, a paused demo included.
func (u *UI) introBlocked() bool { return u.prompt != nil || u.qr != nil || u.form != nil }

// introPace sets the next frame from what this one cost; a link too slow for
// the demo ends it, so that keys and messages never wait behind it.
func (u *UI) introPace(cost time.Duration) {
	r := u.intro
	if r == nil || r.paused {
		return
	}
	wait, giveUp := r.pace.Next(cost)
	if giveUp {
		u.introEnd()
		return
	}
	r.timer.Reset(wait)
}

// introEnd ends the demo at once; the next frame is the whole interface.
func (u *UI) introEnd() {
	if u.intro == nil {
		return
	}
	u.intro.timer.Stop()
	u.intro = nil
	u.freeVeils()
	u.repaint()
}

// introC : the frame timer of the demo; nil (never ready) with no demo or
// while it is paused.
func (u *UI) introC() <-chan time.Time {
	if u.intro == nil || u.intro.paused {
		return nil
	}
	return u.intro.timer.C
}

func (u *UI) introCanvas() intro.Canvas {
	aspect := 2.0
	if u.t.CellW > 0 && u.t.CellH > 0 {
		aspect = float64(u.t.CellH) / float64(u.t.CellW)
	}
	return intro.Canvas{Cols: u.t.Cols, Rows: u.t.Rows, Aspect: aspect}
}

// introLayer : the layer of the demo and its dark. A layer of another size (a
// resize, paused or between two frames) is made again for this screen first.
func (u *UI) introLayer() (*intro.Layer, float32) {
	r := u.intro
	if r.layer == nil || r.layer.Cols != u.t.Cols || r.layer.Rows != u.t.Rows {
		r.layer, r.dark = r.show.Frame(time.Now(), u.introCanvas())
	}
	return r.layer, r.dark
}

// introCompose lays the demo over the frame text draw made.
func (u *UI) introCompose(text string) string {
	l, dark := u.introLayer()
	return intro.Composite(intro.Parse(text, u.t.Cols, u.t.Rows), l, dark, u.rgbOf).Emit()
}

// rgbOf : the colour c shows on the screen. A default colour is the
// terminal's own, known when it answered OSC 10/11; an ANSI one, when it
// answered OSC 4.
func (u *UI) rgbOf(c theme.Color, fg bool) (theme.RGB, bool) {
	p := u.t.Colors
	switch {
	case c.Kind == 2:
		return c.RGB, true
	case c.Kind == 1:
		if c.Idx < 16 && p.HasANSI[c.Idx] {
			return p.ANSI[c.Idx], true
		}
		return theme.RGB{}, false
	case fg:
		return p.FG, p.HasFG
	}
	return p.BG, p.HasBG
}
