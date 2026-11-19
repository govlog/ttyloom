package ui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// hideRig : the UI of cfg on a 100 x 24 offscreen terminal, the chat list open
// at 26 columns, three chats with a window each (Alice shown, 300 messages of
// different lengths, so that a width change wraps them again), and the screen
// its frames make.
type hideRig struct {
	u     *UI
	out   *bytes.Buffer // the frame being drawn
	all   bytes.Buffer  // every frame since the start
	scr   *vt
	frame func() // when set, runs after each frame
}

func hideCfg(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func newHideRig(t *testing.T, cfg *config.Config) *hideRig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	out := &bytes.Buffer{}
	u := newUI(ctx, cancel, term.NewOffscreen(out, 100, 24), cfg, theme.Terminal(), nil)
	u.closeOverlays() // the Networks box of a client with no network
	u.side = sideChats
	now := time.Now()
	for i, title := range []string{"Alice", "Bob", "Carol"} {
		c := &model.Chat{Net: netTelegram, ID: int64(i + 1), Kind: model.ChatUser, Title: title, LastDate: now.Add(-time.Duration(i) * time.Hour)}
		u.chats[c.Key()] = c
		u.chatList = append(u.chatList, c)
		w := u.ws.New(i > 0) // Alice's window is the one shown
		w.Chat, w.Loaded = c, true
	}
	w := u.ws.List[1]
	for i := 1; i <= 300; i++ { // every line of message i carries its tag m<i>; 1 to 3 lines
		w.Items = append(w.Items, &Item{Msg: &model.Msg{Net: netTelegram, ChatID: 1, ID: i, Date: now, From: "zed", FromID: 99,
			Text: strings.Repeat(fmt.Sprintf("m%d ", i), 10+i%25)}})
	}
	r := &hideRig{u: u, out: out, scr: newVT(100, 24)}
	r.draw()
	return r
}

// hideClock : a time after every real clock reading of the test, at the start
// of a minute — the clock of the status bar holds for the next 60 s.
func hideClock() time.Time { return time.Now().Truncate(time.Minute).Add(time.Minute) }

// hiddenRig : a rig with the auto-hide on, slid away after a move of the
// pointer over the messages; the time the slide ended.
func hiddenRig(t *testing.T) (*hideRig, time.Time) {
	t.Helper()
	r := newHideRig(t, hideCfg(t))
	r.u.setCmd([]string{"sidebar_autohide", "on"})
	at := hideClock()
	r.move(60, 3, at)
	if _, at = r.run(at, 2*time.Second); r.width() != 0 {
		t.Fatalf("setup: the panel did not slide away (%d columns)", r.width())
	}
	return r, at
}

// draw draws one frame as Run does and feeds it to the screen.
func (r *hideRig) draw() {
	r.out.Reset()
	r.u.draw()
	r.all.Write(r.out.Bytes())
	r.scr.feed(r.out.Bytes())
	if r.frame != nil {
		r.frame()
	}
}

// mouse hands a mouse event over the way Run does: every event goes by
// noteInput, a move with no drag to the hover and the zones, the rest to key().
func (r *hideRig) mouse(m term.MouseEvent, at time.Time) {
	k := term.Key{Code: term.Mouse, Mouse: m}
	r.u.noteInput(k, at)
	if (m.Motion || !m.Press) && r.u.drag == dragNone {
		r.u.hoverAt(m.X, m.Y)
		r.u.sideHoverAt(m.X, m.Y)
		r.u.zoneAt(m.X, m.Y)
	} else {
		r.u.key(k)
	}
	r.draw()
}

// move : the pointer moves to (x, y) with no button held (?1003).
func (r *hideRig) move(x, y int, at time.Time) {
	r.mouse(term.MouseEvent{X: x, Y: y, Button: 3, Press: true, Motion: true}, at)
}

// press, release : the left button at (x, y).
func (r *hideRig) press(x, y int, at time.Time) {
	r.mouse(term.MouseEvent{X: x, Y: y, Button: 0, Press: true}, at)
}

func (r *hideRig) release(x, y int, at time.Time) {
	r.mouse(term.MouseEvent{X: x, Y: y, Button: 0}, at)
}

// key : a key, then the frame Run draws after it.
func (r *hideRig) key(k term.Key) {
	r.u.key(k)
	r.draw()
}

// run moves the clock from at to at+d in ticks of 10 ms and draws each frame a
// tick asks for, as Run does. It gives the widths of the panel drawn, in
// order, each one once, and the time reached.
func (r *hideRig) run(at time.Time, d time.Duration) ([]int, time.Time) {
	widths := []int{r.width()}
	for end := at.Add(d); !at.After(end); at = at.Add(10 * time.Millisecond) {
		if r.u.tickAt(at) {
			r.draw()
		}
		if w := r.width(); w != widths[len(widths)-1] {
			widths = append(widths, w)
		}
	}
	return widths, at
}

// tick : one tick at at, and its frame.
func (r *hideRig) tick(at time.Time) {
	if r.u.tickAt(at) {
		r.draw()
	}
}

// width : the width of the panel on the screen — the column of its bar, the
// one column with │ or ├ on every row (but under a box on top, a menu); 0
// when no panel is drawn. A row of the panel wider than the panel pushes its
// bar out of that column.
func (r *hideRig) width() int {
	ov := r.u.overlay()
	for c := 0; c < r.scr.cols; c++ {
		all := true
		for row := 0; row < r.scr.rows && all; row++ {
			cell := r.scr.cell[row][c]
			all = strings.HasSuffix(cell, "│") || strings.HasSuffix(cell, "├") || ov.hits(row, c, 1, 1)
		}
		if all {
			return c
		}
	}
	return 0
}

// rowText : the text of a screen row from the column c on, colours dropped.
func (r *hideRig) rowText(row, c int) string {
	var b strings.Builder
	for _, cell := range r.scr.cell[row][c:] {
		if strings.HasPrefix(cell, "\x1b[") {
			cell = cell[strings.IndexByte(cell, 'm')+1:]
		}
		b.WriteString(cell)
	}
	return b.String()
}

var tagRe = regexp.MustCompile(`m\d+`)

// topTag : the tag of the message on the top row of the message area.
func (r *hideRig) topTag() string {
	c := 0
	if w := r.width(); w > 0 {
		c = w + 1
	}
	return tagRe.FindString(r.rowText(0, c))
}

func (r *hideRig) resize(cols, rows int) {
	r.u.t.Cols, r.u.t.Rows = cols, rows
	r.u.sideW = clampSideW(r.u.cfg.SidebarWidth, cols) // what Run does on a resize
	r.u.clear()
	r.scr = newVT(cols, rows)
	r.draw()
}

// Off by default: the setting reads off, and a pointer off the panel for ten
// seconds changes nothing — no slide, no timer.
func TestAutohideOffByDefault(t *testing.T) {
	r := newHideRig(t, hideCfg(t))
	r.u.setCmd([]string{"sidebar_autohide"})
	if got := lastSys(r.u.view()); got != "sidebar_autohide = false" {
		t.Fatalf("/set sidebar_autohide: %q, want it off by default", got)
	}
	at := hideClock()
	r.move(60, 3, at)
	if widths, _ := r.run(at, 10*time.Second); !slices.Equal(widths, []int{testSideW}) || r.u.hideC() != nil {
		t.Fatalf("auto-hide off: widths %v, timer %v; want the panel kept at %d and no timer", widths, r.u.hideC() != nil, testSideW)
	}
}

// On, the pointer off the panel for the delay: the panel slides out in steps
// of a few columns, and the message area takes its columns.
func TestAutohideSlidesOut(t *testing.T) {
	r := newHideRig(t, hideCfg(t))
	r.u.setCmd([]string{"sidebar_autohide", "on"})
	at := hideClock()
	r.move(60, 3, at)
	if widths, _ := r.run(at, 990*time.Millisecond); !slices.Equal(widths, []int{testSideW}) {
		t.Fatalf("before the delay of 1 s: widths %v, want the panel kept", widths)
	}
	widths, _ := r.run(at.Add(time.Second), time.Second)
	if want := []int{26, 20, 15, 10, 5, 0}; !slices.Equal(widths, want) {
		t.Fatalf("widths drawn %v, want %v", widths, want)
	}
	if got := r.rowText(22, 0); !strings.HasPrefix(got, "[") {
		t.Fatalf("status line of the hidden panel: %q, want it from the first column", got)
	}
}

// A frame slower than a step (a window of 10000 messages wraps again in about
// 90 ms) takes the steps due at once: the slide ends on time in fewer frames,
// and the loop is free between them.
func TestAutohideSlowFramesSkipSteps(t *testing.T) {
	r := newHideRig(t, hideCfg(t))
	r.u.setCmd([]string{"sidebar_autohide", "on"})
	at := hideClock()
	r.move(60, 3, at)
	at = at.Add(time.Second)
	var widths []int
	for _, d := range []time.Duration{0, 90, 180} { // one frame every 90 ms
		r.tick(at.Add(d * time.Millisecond))
		widths = append(widths, r.width())
	}
	if want := []int{20, 5, 0}; !slices.Equal(widths, want) || r.u.hideC() != nil {
		t.Fatalf("frames 90 ms apart: widths %v, timer %v; want %v and the slide over", widths, r.u.hideC() != nil, want)
	}
}

// Hidden, the pointer touches the first column of the screen: the panel
// slides back in steps. The second column belongs to the messages: it brings
// nothing back.
func TestAutohideRevealAtLeftEdge(t *testing.T) {
	r, at := hiddenRig(t)
	r.move(1, 5, at)
	if widths, _ := r.run(at, 500*time.Millisecond); !slices.Equal(widths, []int{0}) {
		t.Fatalf("pointer on the second column: widths %v, want the panel kept hidden", widths)
	}
	at = at.Add(time.Second)
	r.move(0, 5, at)
	widths, _ := r.run(at, 500*time.Millisecond)
	if want := []int{0, 5, 10, 15, 20, 26}; !slices.Equal(widths, want) {
		t.Fatalf("pointer on the first column: widths %v, want %v", widths, want)
	}
}

// The panel stays out while it is in use, ten seconds after the pointer was
// last seen off it: the pointer over it, the menu of one of its lines open, a
// filter typed in it, its bar being dragged.
func TestAutohideKeptWhileInUse(t *testing.T) {
	for _, c := range []struct {
		name string
		use  func(r *hideRig, at time.Time)
		want int
	}{
		{"pointer over it, hover off", func(r *hideRig, at time.Time) { // no follow-mouse keyboard to hold it
			r.u.setCmd([]string{"hover", "off"})
			r.move(5, 3, at)
		}, testSideW},
		{"menu of a line", func(r *hideRig, at time.Time) {
			r.mouse(term.MouseEvent{X: 3, Y: sideHdr, Button: 2, Press: true}, at)
			r.move(60, 3, at)
		}, testSideW},
		{"filter typed", func(r *hideRig, at time.Time) {
			r.move(5, 3, at) // the keyboard follows the pointer into the panel
			r.key(term.Key{Rune: 'a'})
			r.move(60, 3, at)
		}, testSideW},
		{"bar dragged", func(r *hideRig, at time.Time) {
			r.press(testSideW, 3, at)
			r.mouse(term.MouseEvent{X: 80, Y: 3, Button: 0, Press: true, Motion: true}, at) // the bar stops at half the screen
		}, 50},
	} {
		r := newHideRig(t, hideCfg(t))
		r.u.setCmd([]string{"sidebar_autohide", "on"})
		at := hideClock()
		r.move(60, 3, at) // a move seen: the delay counts from here
		c.use(r, at)
		if widths, _ := r.run(at, 10*time.Second); !slices.Equal(widths, []int{c.want}) {
			t.Fatalf("%s: widths %v, want the panel kept at %d", c.name, widths, c.want)
		}
	}
}

// A terminal that never reported a move with no button (ssh without mouse
// reporting, a tmux that does not pass it): the panel never slides away, a
// click off it included — the user cannot lose it.
func TestAutohideNeedsAPointer(t *testing.T) {
	r := newHideRig(t, hideCfg(t))
	r.u.setCmd([]string{"sidebar_autohide", "on"})
	at := hideClock()
	r.press(60, 3, at)
	r.release(60, 3, at)
	if widths, _ := r.run(at, 10*time.Second); !slices.Equal(widths, []int{testSideW}) {
		t.Fatalf("no move seen: widths %v, want the panel kept", widths)
	}
}

// The keys that act on the panel bring it back, and it slides away again once
// the delay has run with the pointer off it: F7, Shift+F2, F2 and Shift+Tab —
// that one gives the panel the keyboard, which holds it until Escape. F2 on a
// panel slid away brings it back and keeps its mode; once the panel is out,
// F2 cycles the mode as before.
func TestAutohideKeysReveal(t *testing.T) {
	for _, c := range []struct {
		name string
		key  term.Key
	}{
		{"F7", term.Key{Code: term.F7}},
		{"Shift+F2", term.Key{Code: term.F2, Shift: true}},
		{"F2", term.Key{Code: term.F2}},
		{"Shift+Tab", term.Key{Code: term.Tab, Shift: true}},
	} {
		r, _ := hiddenRig(t)
		saved := r.u.cfg.Sidebar
		r.key(c.key)
		from := time.Now() // the keys read the real clock: the delay counts from here
		widths, at := r.run(from, 300*time.Millisecond)
		if want := []int{0, 5, 10, 15, 20, 26}; !slices.Equal(widths, want) {
			t.Fatalf("%s: widths %v, want %v", c.name, widths, want)
		}
		if r.u.side != sideChats || r.u.cfg.Sidebar != saved {
			t.Fatalf("%s: mode %v, saved %q; want the chat list kept, %q saved", c.name, r.u.side, r.u.cfg.Sidebar, saved)
		}
		if c.name == "Shift+Tab" {
			if widths, at = r.run(at, 3*time.Second); !slices.Equal(widths, []int{testSideW}) {
				t.Fatalf("Shift+Tab: widths %v, want the panel kept while it has the keyboard", widths)
			}
			r.key(term.Key{Code: term.Esc})
			from = at // the keyboard back to the input line: the delay counts from here
		}
		if widths, _ := r.run(at, from.Add(900*time.Millisecond).Sub(at)); !slices.Equal(widths, []int{testSideW}) {
			t.Fatalf("%s: widths %v before the delay, want the panel kept", c.name, widths)
		}
		if widths, _ := r.run(from.Add(time.Second), time.Second); widths[len(widths)-1] != 0 {
			t.Fatalf("%s: widths %v after the delay, want the panel slid away again", c.name, widths)
		}
		if c.name == "F2" {
			r.key(term.Key{Code: term.F2}) // slid away again: back first
			r.run(time.Now(), 300*time.Millisecond)
			r.key(term.Key{Code: term.F2}) // out: the next mode
			if r.u.side != sideWindows {
				t.Fatalf("F2 on the panel out: mode %v, want the windows list", r.u.side)
			}
		}
	}
}

// Every width of a slide draws the whole frame with the bar of the panel at
// that width — no row of the panel wider than the panel, no panic — in both
// modes, avatars on and off, for each panel width from 12 to 40 columns. The
// avatars of the panel stay placed while the width is not 0 (no blink at
// each step), and nothing of them is left once it is 0.
func TestAutohideEveryWidthDrawsWhole(t *testing.T) {
	for _, avatars := range []bool{false, true} {
		for _, mode := range []sideMode{sideChats, sideWindows} {
			for sw := sideMinW; sw <= 40; sw++ {
				ctx := fmt.Sprintf("avatars %v, mode %v, width %d", avatars, mode, sw)
				r := newHideRig(t, hideCfg(t))
				r.u.ws.List[1].Items = r.u.ws.List[1].Items[270:] // 30 messages: enough to fill the screen
				if avatars {
					r.u.t.Kitty, r.u.t.CellW, r.u.t.CellH = true, 8, 17
					r.u.images, r.u.cfg.Avatars = "kitty", true
					for _, c := range r.u.chatList {
						r.u.avatars[c.Key()] = &model.Media{Kind: model.MediaAvatar, State: model.MediaReady, Frames: [][]byte{[]byte("png")}}
					}
				}
				r.u.side = mode
				r.u.setCmd([]string{"sidebar_width", strconv.Itoa(sw)})
				r.u.setCmd([]string{"sidebar_autohide", "on"})
				r.draw()
				placed := len(kittyShown(r.all.String()))
				deleted := 0
				r.frame = func() {
					if r.width() > 0 {
						deleted += strings.Count(r.out.String(), "a=d")
					}
				}
				var out, in []int
				func() {
					defer func() {
						if p := recover(); p != nil {
							t.Fatalf("%s: panic: %v", ctx, p)
						}
					}()
					at := hideClock()
					r.move(60, 3, at)
					out, at = r.run(at.Add(time.Second), time.Second)
					r.move(0, 3, at)
					in, _ = r.run(at, time.Second)
				}()
				var want []int
				for k := range hideSteps + 1 {
					want = append(want, sw*(hideSteps-k)/hideSteps)
				}
				if !slices.Equal(out, want) {
					t.Fatalf("%s: widths sliding out %v, want %v", ctx, out, want)
				}
				if slices.Reverse(want); !slices.Equal(in, want) {
					t.Fatalf("%s: widths sliding in %v, want %v", ctx, in, want)
				}
				if avatars && mode == sideChats && (placed != len(r.u.chatList) || deleted != 0) {
					t.Fatalf("%s: %d avatars placed at full width, %d deleted while the panel showed; want %d and 0",
						ctx, placed, deleted, len(r.u.chatList))
				}
			}
		}
	}
}

// The avatars of the panel go with it: none is left on the screen once the
// panel has slid away, and they come back with it.
func TestAutohideAvatarsGoWithThePanel(t *testing.T) {
	r := newHideRig(t, hideCfg(t))
	r.u.t.Kitty, r.u.t.CellW, r.u.t.CellH = true, 8, 17
	r.u.images, r.u.cfg.Avatars = "kitty", true
	for _, c := range r.u.chatList {
		r.u.avatars[c.Key()] = &model.Media{Kind: model.MediaAvatar, State: model.MediaReady, Frames: [][]byte{[]byte("png")}}
	}
	r.u.setCmd([]string{"sidebar_autohide", "on"})
	r.draw()
	at := hideClock()
	r.move(60, 3, at)
	_, at = r.run(at.Add(time.Second), time.Second)
	if n := len(kittyShown(r.all.String())); r.width() != 0 || n != 0 {
		t.Fatalf("panel slid away (%d columns): %d placements left on the screen, want none", r.width(), n)
	}
	r.move(0, 3, at)
	r.run(at, time.Second)
	if n := len(kittyShown(r.all.String())); n != len(r.u.chatList) {
		t.Fatalf("panel back: %d avatars placed, want %d", n, len(r.u.chatList))
	}
}

// Under the preview on the whole screen nothing slides — each step would draw
// the preview whole again — and the delay waits for it to close.
func TestAutohideWaitsForTheViewer(t *testing.T) {
	r := newHideRig(t, hideCfg(t))
	r.u.setCmd([]string{"sidebar_autohide", "on"})
	at := hideClock()
	r.tick(at) // the clock of the status bar settles
	r.move(60, 3, at)
	r.u.viewMsg(&model.Msg{Media: &model.Media{Kind: model.MediaPhoto, State: model.MediaLoading, Loc: 1}})
	if r.u.viewer == nil {
		t.Fatal("setup: no preview")
	}
	frames := 0
	for i := range 300 {
		if r.u.tickAt(at.Add(time.Duration(i) * 10 * time.Millisecond)) {
			frames++
			r.draw()
		}
	}
	if frames != 0 || r.u.hideC() != nil {
		t.Fatalf("3 s under the preview: %d frames asked, timer %v; want none", frames, r.u.hideC() != nil)
	}
	r.key(term.Key{Code: term.Esc}) // the preview closes
	at = at.Add(3 * time.Second)
	if widths, _ := r.run(at, 900*time.Millisecond); !slices.Equal(widths, []int{testSideW}) {
		t.Fatalf("preview closed: widths %v before the delay, want the panel kept", widths)
	}
	if widths, _ := r.run(at.Add(time.Second), time.Second); widths[len(widths)-1] != 0 {
		t.Fatalf("preview closed: widths %v after the delay, want the panel slid away", widths)
	}
}

// A click during the slide hits what the frame drew. Hidden, a press on the
// first column is one on the messages, and it brings nothing back; half way
// in, the column after the bar is message area, the bar column grabs the
// bar, and a row of the panel opens its chat.
func TestAutohideClickHitsWhatIsDrawn(t *testing.T) {
	r, at := hiddenRig(t)
	r.press(0, 2, at)
	if r.u.drag != dragText {
		t.Fatalf("hidden, press on the first column: drag %v, want a selection on the messages", r.u.drag)
	}
	r.release(0, 2, at)
	if r.tick(at); r.width() != 0 {
		t.Fatalf("hidden, click on the first column: the panel came back (%d columns)", r.width())
	}
	at = at.Add(time.Second)
	r.move(0, 5, at)
	r.tick(at)
	r.tick(at.Add(hideEvery)) // two steps in
	w := r.width()
	if w != 10 {
		t.Fatalf("setup: two steps in, the panel is %d columns, want 10", w)
	}
	r.press(w+1, 6, at) // another message: on the first one it would be a double click
	if r.u.drag != dragText {
		t.Fatalf("press on the column after the bar: drag %v, want a selection on the messages", r.u.drag)
	}
	r.release(w+1, 6, at)
	r.press(w, 2, at)
	if r.u.drag != dragSide {
		t.Fatalf("press on the bar drawn: drag %v, want the bar grabbed", r.u.drag)
	}
	r.release(w, 2, at)
	r.press(3, sideHdr+1, at) // Bob, second in the recent order
	r.release(3, sideHdr+1, at)
	if c := r.u.ws.Current().Chat; c == nil || c.Title != "Bob" {
		t.Fatalf("click on the second row of the panel: window of %v, want Bob", c)
	}
}

// The settings are saved and read back; a delay that is no duration is
// refused with a message. A UI started from that file has the panel out, and
// slides it away once the pointer has been off it for the delay read back.
func TestAutohideSettingsSaved(t *testing.T) {
	cfg := hideCfg(t)
	r := newHideRig(t, cfg)
	r.u.setCmd([]string{"sidebar_autohide", "on"})
	r.u.setCmd([]string{"sidebar_autohide_delay", "2s"})
	for _, bad := range []string{"abc", "-1s", "2"} {
		r.u.setCmd([]string{"sidebar_autohide_delay", bad})
		if got := lastSys(r.u.view()); got != i18n.T("sidebar_autohide_delay_bad") || r.u.cfg.SidebarHideDelay != "2s" {
			t.Fatalf("delay %q: message %q, delay %q; want it refused with %q", bad, got, r.u.cfg.SidebarHideDelay, i18n.T("sidebar_autohide_delay_bad"))
		}
	}
	back, err := config.LoadFrom(filepath.Dir(cfg.Path()))
	if err != nil || !back.SidebarAutohide || back.SidebarHideDelay != "2s" {
		t.Fatalf("read back: %v, sidebar_autohide %v, delay %q", err, back.SidebarAutohide, back.SidebarHideDelay)
	}
	r = newHideRig(t, back)
	at := hideClock()
	r.move(60, 3, at)
	if widths, _ := r.run(at, 1990*time.Millisecond); !slices.Equal(widths, []int{testSideW}) {
		t.Fatalf("delay of 2 s read back: widths %v before it, want the panel kept", widths)
	}
	if widths, _ := r.run(at.Add(2*time.Second), time.Second); widths[len(widths)-1] != 0 {
		t.Fatalf("delay of 2 s read back: widths %v after it, want the panel slid away", widths)
	}
}

// An idle client stays idle: no timer is armed and no tick asks for a frame,
// at rest out with the pointer over the panel and at rest slid away.
func TestAutohideIdleArmsNothing(t *testing.T) {
	r := newHideRig(t, hideCfg(t))
	r.u.setCmd([]string{"sidebar_autohide", "on"})
	at := hideClock()
	r.tick(at) // the clock of the status bar settles
	r.move(5, 3, at)
	idle := func(what string) {
		t.Helper()
		if r.u.hideC() != nil {
			t.Fatalf("%s: a timer is armed", what)
		}
		for i := range 100 {
			at = at.Add(100 * time.Millisecond)
			if r.u.tickAt(at) {
				t.Fatalf("%s: tick %d asks for a frame", what, i)
			}
		}
	}
	idle("out, pointer over it")
	r.move(60, 3, at)
	if _, at = r.run(at, 2*time.Second); r.width() != 0 {
		t.Fatalf("setup: the panel did not slide away (%d columns)", r.width())
	}
	idle("slid away")
}

// A reader scrolled up keeps the message on the top row while the panel
// slides out and back, at each step: the messages wrap again under it.
func TestAutohideKeepsTheTopMessage(t *testing.T) {
	r := newHideRig(t, hideCfg(t))
	r.u.setCmd([]string{"sidebar_autohide", "on"})
	r.key(term.Key{Code: term.PgUp})
	r.key(term.Key{Code: term.PgUp})
	top := r.topTag()
	if top == "" || top == "m300" {
		t.Fatalf("setup: top row %q, want a message above the last one", top)
	}
	r.frame = func() {
		if got := r.topTag(); got != top {
			t.Fatalf("panel at %d columns: top row shows %q, want %q", r.width(), got, top)
		}
	}
	at := hideClock()
	r.move(60, 3, at)
	if _, at = r.run(at, 2*time.Second); r.width() != 0 {
		t.Fatalf("setup: the panel did not slide away (%d columns)", r.width())
	}
	r.move(0, 3, at)
	if r.run(at, time.Second); r.width() != testSideW {
		t.Fatalf("setup: the panel did not come back (%d columns)", r.width())
	}
}

// Changes during a slide: the auto-hide turned off brings the panel back at
// full width at once; a new sidebar_width goes on with the slide at the new
// scale; a terminal with no room for the panel drops the slide, and once the
// room is back the panel is out at full width, then slides away after the
// delay.
func TestAutohideChangesDuringTheSlide(t *testing.T) {
	start := func(t *testing.T) (*hideRig, time.Time) {
		t.Helper()
		r := newHideRig(t, hideCfg(t))
		r.u.setCmd([]string{"sidebar_autohide", "on"})
		at := hideClock()
		r.move(60, 3, at)
		at = at.Add(time.Second)
		r.tick(at)
		at = at.Add(hideEvery)
		if r.tick(at); r.width() != 15 {
			t.Fatalf("setup: two steps out, the panel is %d columns, want 15", r.width())
		}
		return r, at
	}
	t.Run("off", func(t *testing.T) {
		r, _ := start(t)
		r.u.setCmd([]string{"sidebar_autohide", "off"})
		if r.draw(); r.width() != testSideW || r.u.hideC() != nil {
			t.Fatalf("turned off mid-slide: %d columns, timer %v; want %d at once and no timer", r.width(), r.u.hideC() != nil, testSideW)
		}
	})
	t.Run("sidebar_width", func(t *testing.T) {
		r, at := start(t)
		r.u.setCmd([]string{"sidebar_width", "40"})
		r.draw()
		if widths, _ := r.run(at.Add(hideEvery), time.Second); !slices.Equal(widths, []int{24, 16, 8, 0}) {
			t.Fatalf("sidebar_width 40 mid-slide: widths %v, want [24 16 8 0]", widths)
		}
	})
	t.Run("resize", func(t *testing.T) {
		r, at := start(t)
		r.resize(40, 24) // 40 < 20 + 1 + 20: no room
		widths, at := r.run(at, 200*time.Millisecond)
		if !slices.Equal(widths, []int{0}) || r.u.hideC() != nil {
			t.Fatalf("no room mid-slide: widths %v, timer %v; want no panel and no timer", widths, r.u.hideC() != nil)
		}
		r.resize(100, 24)
		if r.tick(at); r.width() != testSideW {
			t.Fatalf("room back: %d columns, want the panel out at %d", r.width(), testSideW)
		}
		if widths, _ := r.run(at, 2*time.Second); widths[len(widths)-1] != 0 {
			t.Fatalf("room back: widths %v, want the panel slid away after the delay", widths)
		}
	})
}

// BenchmarkAutohideStep : one step of a slide — the panel changes width, the
// shown window wraps again whole, one frame is drawn — for a window of 50,
// 2000 and 10000 messages on a 120 x 40 terminal.
func BenchmarkAutohideStep(b *testing.B) {
	for _, n := range []int{50, 2000, 10000} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			u := benchUI()
			u.t = term.NewOffscreen(io.Discard, 120, 40)
			w := u.view()
			for i := len(w.Items); i < n; i++ {
				w.Items = append(w.Items, &Item{Msg: &model.Msg{Net: netTelegram, ChatID: 1, ID: i + 1, Date: time.Now(), From: "alice", FromID: int64(i % 5),
					Text: fmt.Sprintf("message %d — some text that wraps over a couple of lines when the window is narrow enough", i)}})
			}
			w.Items = w.Items[:n]
			u.draw()
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				u.hide.step = 1 + i%2 // two widths in turn: each frame wraps again
				u.clear()
				u.draw()
			}
		})
	}
}
