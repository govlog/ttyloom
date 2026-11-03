package ui

import (
	"bytes"
	"context"
	"slices"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/intro"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// introUI : the interface with the default configuration (intro = true) on
// an offscreen terminal of cols × rows, and what it draws.
func introUI(t *testing.T, cols, rows int) (*UI, *bytes.Buffer) {
	t.Helper()
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Intro {
		t.Fatal("intro must be on by default")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	buf := &bytes.Buffer{}
	u := newUI(ctx, cancel, term.NewOffscreen(buf, cols, rows), cfg, theme.Terminal(), nil)
	return u, buf
}

// The key that stops the demo is the demo's: it never reaches the input. A
// second key during the fade-out ends it at once, swallowed too.
func TestIntroKeyIsSwallowed(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	u.command("demo", nil, "")
	if u.intro == nil {
		t.Fatal("/demo starts the demo")
	}
	t0 := time.Now()
	u.introStep(t0)
	if !u.introKey(term.Key{Rune: 'a'}, t0) || u.ed.String() != "" || u.intro == nil {
		t.Fatalf("first key: swallowed, fade-out started (input %q)", u.ed.String())
	}
	if !u.introKey(term.Key{Rune: 'b'}, t0.Add(100*time.Millisecond)) || u.intro != nil || u.ed.String() != "" {
		t.Fatal("second key during the fade: ends at once, swallowed")
	}
}

// Once the demo is over, the screen is exactly the one of a session that
// never had it.
func TestIntroLeavesTheScreenAsWithout(t *testing.T) {
	plain, pbuf := introUI(t, 80, 24)
	plain.draw()
	u, buf := introUI(t, 80, 24)
	t0 := time.Now()
	u.introStart(t0)
	for i := range 30 {
		u.introStep(t0.Add(time.Duration(i) * 50 * time.Millisecond))
		u.draw()
	}
	u.introKey(term.Key{Rune: 'x'}, t0.Add(1500*time.Millisecond))
	u.introStep(t0.Add(1900 * time.Millisecond)) // the fade-out is over: the end
	u.draw()
	if u.intro != nil {
		t.Fatal("the demo must be over")
	}
	a, b := intro.Parse(pbuf.String(), 80, 24), intro.Parse(buf.String(), 80, 24)
	if !slices.Equal(a.Cells, b.Cells) {
		for i := range a.Cells {
			if a.Cells[i] != b.Cells[i] {
				t.Fatalf("cell %d, %d: %+v, want %+v", i%80, i/80, b.Cells[i], a.Cells[i])
			}
		}
	}
}

// An authentication prompt must never be hidden under the demo.
func TestIntroEndsOnAuthPrompt(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	u.introStart(time.Now())
	u.authStart("fake:a", model.EvAuthPrompt{Question: "code?"})
	u.introStep(time.Now())
	if u.intro != nil {
		t.Fatal("an auth prompt must end the demo")
	}
}

// A prompt that opens while the demo waits for the focus ends it too: the
// screen still shows, the prompt must not be under the demo.
func TestIntroEndsOnAuthPromptWhilePaused(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	u.introStart(time.Now())
	u.introKey(term.Key{Code: term.FocusOut}, time.Now())
	u.authStart("fake:a", model.EvAuthPrompt{Question: "code?"})
	u.draw()
	if u.intro != nil {
		t.Fatal("a prompt drawn under a paused demo")
	}
}

// Forgotten in a tab that never says it lost the focus, the demo ends by
// itself after ten idle minutes, with its fade-out.
func TestIntroEndsAfterTenIdleMinutes(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	t0 := time.Now()
	u.introStart(t0)
	u.introStep(t0.Add(9 * time.Minute))
	if u.intro == nil || u.intro.show.Stopping() {
		t.Fatal("9 minutes: still playing")
	}
	u.introStep(t0.Add(10*time.Minute + time.Second))
	u.introStep(t0.Add(10*time.Minute + time.Second + 400*time.Millisecond))
	if u.intro != nil {
		t.Fatal("10 idle minutes: fade-out, then end")
	}
}

// Without the focus, no frame is sent; the demo waits.
func TestIntroPausesWithoutFocus(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	u.introStart(time.Now())
	if u.introC() == nil {
		t.Fatal("playing: a frame timer")
	}
	if u.introKey(term.Key{Code: term.FocusOut}, time.Now()) {
		t.Fatal("the focus is not the demo's: the UI sees it too")
	}
	if u.intro == nil || u.introC() != nil {
		t.Fatal("unfocused: no frame timer, demo kept")
	}
	u.introKey(term.Key{Code: term.FocusIn}, time.Now())
	if u.introC() == nil {
		t.Fatal("focus back: the frames again")
	}
}

// A mouse move is not a key: the demo goes on. A click stops it.
func TestIntroMouse(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	u.introStart(time.Now())
	if u.introKey(term.Key{Code: term.Mouse, Mouse: term.MouseEvent{X: 3, Y: 3, Motion: true}}, time.Now()) || u.intro.show.Stopping() {
		t.Fatal("a move must not stop the demo")
	}
	if !u.introKey(term.Key{Code: term.Mouse, Mouse: term.MouseEvent{X: 3, Y: 3, Press: true}}, time.Now()) || !u.intro.show.Stopping() {
		t.Fatal("a click stops the demo and is swallowed")
	}
}

func TestIntroNeedsRoom(t *testing.T) {
	u, _ := introUI(t, 39, 24)
	if u.introStart(time.Now()) || u.intro != nil {
		t.Fatal("under 40 columns: no demo")
	}
}

// The screen shrinks under the minimum while the demo plays: no panic.
func TestIntroSurvivesResize(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	t0 := time.Now()
	u.introStart(t0)
	u.t.Cols, u.t.Rows = 20, 6
	u.introStep(t0.Add(time.Second))
	u.draw()
}

// The colours the terminal gave blend; the ones it did not stay unknown.
func TestIntroColours(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	if _, ok := u.rgbOf(theme.Color{}, false); ok {
		t.Fatal("default background, no answer: unknown")
	}
	u.t.Colors.BG, u.t.Colors.HasBG = theme.RGB{R: 1, G: 2, B: 3}, true
	if c, ok := u.rgbOf(theme.Color{}, false); !ok || c != (theme.RGB{R: 1, G: 2, B: 3}) {
		t.Fatalf("default background from OSC 11: %v %v", c, ok)
	}
	if c, ok := u.rgbOf(theme.Color{Kind: 2, RGB: theme.RGB{R: 9}}, true); !ok || c.R != 9 {
		t.Fatal("RGB colour: itself")
	}
}

// /set screensaver takes a Go duration (60m, 1h, 3600s) or 0 for off.
func TestSetScreensaver(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	if u.cfg.Screensaver != "0" || u.saverDelay() != 0 {
		t.Fatalf("off by default: %q", u.cfg.Screensaver)
	}
	for _, v := range []string{"60m", "1h", "3600s"} {
		u.setCmd([]string{"screensaver", v})
		if u.cfg.Screensaver != v || u.saverDelay() != time.Hour {
			t.Fatalf("%s: %q %v", v, u.cfg.Screensaver, u.saverDelay())
		}
	}
	for _, v := range []string{"abc", "-5m", "60"} {
		u.setCmd([]string{"screensaver", v})
		if u.cfg.Screensaver != "3600s" {
			t.Fatalf("%q must be refused: %q", v, u.cfg.Screensaver)
		}
	}
	u.setCmd([]string{"screensaver", "0"})
	if u.saverDelay() != 0 {
		t.Fatal("0: off")
	}
}

// After the delay with no key nor click, the demo plays; a mouse move is not
// activity.
func TestScreensaverStartsAfterIdle(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	u.setCmd([]string{"screensaver", "1h"})
	t0 := time.Now()
	u.noteInput(term.Key{Rune: 'a'}, t0)
	u.noteInput(term.Key{Code: term.Mouse, Mouse: term.MouseEvent{Motion: true}}, t0.Add(30*time.Minute))
	if u.saverCheck(t0.Add(59*time.Minute)) || u.intro != nil {
		t.Fatal("59 minutes: not yet")
	}
	if !u.saverCheck(t0.Add(time.Hour)) || u.intro == nil {
		t.Fatal("one hour idle: the demo")
	}
}

func TestScreensaverOff(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	t0 := time.Now()
	u.noteInput(term.Key{Rune: 'a'}, t0)
	if u.saverCheck(t0.Add(100*time.Hour)) || u.intro != nil {
		t.Fatal("screensaver 0: never")
	}
}

// Once over, it comes back only after some activity.
func TestScreensaverComesBackAfterActivity(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	u.setCmd([]string{"screensaver", "1h"})
	t0 := time.Now()
	u.noteInput(term.Key{Rune: 'a'}, t0)
	u.saverCheck(t0.Add(time.Hour))
	u.introEnd()
	if u.saverCheck(t0.Add(3 * time.Hour)) {
		t.Fatal("no activity since: it stays off")
	}
	u.noteInput(term.Key{Rune: 'b'}, t0.Add(3*time.Hour))
	if !u.saverCheck(t0.Add(4 * time.Hour)) {
		t.Fatal("an hour after the next key: again")
	}
}

// A prompt waiting for the user, or mpv holding the terminal, keeps it off.
func TestScreensaverWaitsForThePrompt(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	u.setCmd([]string{"screensaver", "1h"})
	t0 := time.Now()
	u.noteInput(term.Key{Rune: 'a'}, t0)
	u.authStart("fake:a", model.EvAuthPrompt{Question: "code?"})
	if u.saverCheck(t0.Add(2 * time.Hour)) {
		t.Fatal("a prompt is open: no screensaver")
	}
}

// A terminal that reports the focus pauses the demo when hidden: the 10
// minute limit is only for the ones that never report it.
func TestIntroIdleLimitOnlyWithoutFocusReports(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	t0 := time.Now()
	u.introStart(t0)
	u.noteInput(term.Key{Code: term.FocusIn}, t0)
	u.introStep(t0.Add(11 * time.Minute))
	if u.intro == nil || u.intro.show.Stopping() {
		t.Fatal("focus reported: the demo plays until a key")
	}
}

// Started while the terminal has no focus, the screensaver waits paused: no
// frame for a hidden window.
func TestScreensaverHiddenStartsPaused(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	u.setCmd([]string{"screensaver", "1h"})
	t0 := time.Now()
	u.noteInput(term.Key{Rune: 'a'}, t0)
	u.noteInput(term.Key{Code: term.FocusOut}, t0.Add(time.Minute))
	if !u.saverCheck(t0.Add(time.Hour)) || u.introC() != nil {
		t.Fatal("hidden: the demo waits for the focus")
	}
	u.introKey(term.Key{Code: term.FocusIn}, t0.Add(2*time.Hour))
	if u.introC() == nil {
		t.Fatal("focus back: it plays")
	}
}

// Resized while it waits for the focus, the demo is laid out again for the
// new screen at once, not at its next frame.
func TestIntroFollowsResizeWhilePaused(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	t0 := time.Now()
	u.introStart(t0)
	u.introStep(t0.Add(time.Second))
	u.introKey(term.Key{Code: term.FocusOut}, t0.Add(time.Second))
	u.t.Cols, u.t.Rows = 100, 30
	u.draw()
	if l := u.intro.layer; l.Cols != 100 || l.Rows != 30 {
		t.Fatalf("layer %dx%d after a resize to 100x30", l.Cols, l.Rows)
	}
}

// /demo takes no prefix from the commands that were there before it.
func TestDemoKeepsDebugPrefix(t *testing.T) {
	u, _ := introUI(t, 80, 24)
	if n, _, _, _ := ParseCommand("/de", u.commandNames()); n != "debug" {
		t.Fatalf("/de: %q", n)
	}
}
