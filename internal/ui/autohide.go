package ui

import (
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

// Auto-hide of the sidebar (sidebar_autohide): the panel slides out to the
// left once the pointer has been off it for sidebar_autohide_delay, and comes
// back as soon as the pointer touches the first column of the screen. It
// pushes the message area as F2 does, so the messages wrap again at each step.
// The panel stays "on" for F2 and for the saved sidebar mode the whole time:
// only its width moves (sideNow).

// ponytail: the narrowest panel drawn is sideMinW/hideSteps = 2 columns, where
// the 2-cell avatars of the panel still fit before its bar. Above 6 steps a
// 1-column panel appears: keep the avatars out of it in draw() then.
const (
	hideSteps = 5                     // widths of a slide after the first one: about 5 columns each at 26
	hideEvery = 30 * time.Millisecond // one step: a slide takes 150 ms
)

// sideHide : state of the auto-hide.
type sideHide struct {
	step  int       // 0: full width … hideSteps: slid away
	out   bool      // where the slide goes, or where it rests: true = away
	next  time.Time // time of the next step; zero at rest
	since time.Time // last time the panel was in use: the delay counts from it
	x     int       // column of the pointer at its last mouse event
	seen  bool      // a move with no button came: the terminal reports the pointer
	timer *time.Timer
}

// hideDelay : sidebar_autohide_delay; 1 s when the file holds no valid duration.
func (u *UI) hideDelay() time.Duration {
	d, err := time.ParseDuration(u.cfg.SidebarHideDelay)
	if err != nil || d < 0 {
		return time.Second
	}
	return d
}

// hideNote reads a mouse event for the auto-hide (noteInput): the column of
// the pointer, and a move with no button held on the first column of the
// screen, or on the part of the panel drawn, brings the panel back at once.
// A press never does: a click there stays a click on what is drawn.
func (u *UI) hideNote(k term.Key, now time.Time) {
	if k.Code != term.Mouse {
		return
	}
	h := &u.hide
	h.x = k.Mouse.X
	if !k.Mouse.Motion || k.Mouse.Button != 3 {
		return
	}
	if !h.seen {
		h.seen, h.since = true, now // the terminal reports the pointer: the delay starts here
	}
	if x0, _ := u.layout(); h.x < max(1, x0) {
		u.hideShow(now)
	}
}

// hideShow : the panel is in use — the pointer came to it, a key acts on it.
// The delay starts again, and a panel going or gone comes back.
func (u *UI) hideShow(now time.Time) {
	h := &u.hide
	h.since = now
	if h.out {
		h.out, h.next = false, now
		u.hideArm(now)
	}
}

// hideKeep : the panel is in use and must stay — the pointer over it, the
// menu of one of its lines open, its filter or its keyboard on, its bar
// dragged.
func (u *UI) hideKeep() bool {
	x0, _ := u.layout()
	m := u.menu
	return (u.hide.seen && u.hide.x < x0) || (m != nil && m.chat != nil && m.member == "") ||
		u.find.keys || u.find.q != "" || u.drag == dragSide
}

// hideTick moves the auto-hide on, from tickAt and from the step timer: a
// slide starts once the pointer has been off the panel for the delay, and the
// steps that are due are taken — several at once when a frame took longer
// than a step, so a big window slides in fewer frames but on time. Without a
// pointer ever seen, nothing slides. true when the width changed.
func (u *UI) hideTick(now time.Time) bool {
	h := &u.hide
	if !u.cfg.SidebarAutohide || !u.sideOn() {
		moved := h.step != 0
		u.hideReset(now) // off, closed by F2 or no room: at rest out, the delay counts from now
		return moved
	}
	if u.viewer != nil { // the preview covers the screen: a step would draw it whole again
		h.since = now
		return false
	}
	switch {
	case u.hideKeep():
		u.hideShow(now)
	case !h.out && h.step == 0 && h.seen && now.Sub(h.since) >= u.hideDelay():
		h.out, h.next = true, now
	}
	if h.next.IsZero() || now.Before(h.next) {
		return false
	}
	n := 1 + int(now.Sub(h.next)/hideEvery)
	if h.out {
		h.step = min(hideSteps, h.step+n)
	} else {
		h.step = max(0, h.step-n)
	}
	h.next = h.next.Add(time.Duration(n) * hideEvery)
	if h.step == 0 || h.step == hideSteps {
		h.next = time.Time{}
	}
	u.hideArm(now)
	u.clear() // the message area changes width: everything is wrapped again
	return true
}

// hideReset puts the panel back out at full width at once, at rest.
func (u *UI) hideReset(now time.Time) {
	h := &u.hide
	if h.step != 0 {
		u.clear()
	}
	h.step, h.out, h.next, h.since = 0, false, time.Time{}, now
	u.hideArm(now)
}

// hideArm sets the step timer on the next step, or stops it at rest: an idle
// client has no timer of the auto-hide armed. The delay before a slide needs
// none: tickAt checks it.
func (u *UI) hideArm(now time.Time) {
	h := &u.hide
	switch {
	case h.next.IsZero():
		if h.timer != nil {
			h.timer.Stop()
		}
	case h.timer == nil:
		h.timer = time.NewTimer(h.next.Sub(now))
	default:
		h.timer.Reset(h.next.Sub(now))
	}
}

// hideC : the step timer of a slide; nil (never ready) at rest.
func (u *UI) hideC() <-chan time.Time {
	if u.hide.next.IsZero() || u.hide.timer == nil {
		return nil
	}
	return u.hide.timer.C
}

// sideCut cuts the lines of the panel, drawn at full width, to the width it
// has now: their first columns, then their bar. Nothing to do at full width.
func (u *UI) sideCut(lines []render.Line) []render.Line {
	w := u.sideNow()
	if w >= u.sideW {
		return lines
	}
	for i, l := range lines {
		n := len(l.Spans) - 1
		sp, got := truncSpans(l.Spans[:n:n], w)
		if got < w { // a wide cluster across the cut: a blank in its place
			sp = append(sp, render.Span{Text: strings.Repeat(" ", w-got)})
		}
		lines[i].Spans = append(sp, l.Spans[n])
	}
	return lines
}
