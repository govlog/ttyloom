// Package term holds raw mode, size, keyboard and the kitty graphics probe.
package term

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	xterm "golang.org/x/term"

	"github.com/govlog/ttyloom/internal/theme"
)

type Term struct {
	in    *os.File
	out   *bufio.Writer
	state *xterm.State
	keys  chan Key
	winch chan os.Signal

	Cols, Rows   int
	CellW, CellH int    // pixels; 0 = unknown
	Kitty        bool   // kitty graphics protocol supported
	Panic        string // panic of the read loop, shown by main after Close
	KittyKbd     bool   // kitty keyboard protocol on (answer to CSI ? u)
	Curly        bool   // undercurl (SGR 4:3) supported
}

// curlyTerms : a TERM that holds one of these names supports undercurl.
var curlyTerms = []string{"ghostty", "kitty", "wezterm", "foot"}

// curlySupported : undercurl (SGR 4:3) through TERM or TERM_PROGRAM, the same
// terminals as the kitty/OSC 777 detection (Ghostty, kitty, WezTerm, foot).
func curlySupported() bool {
	term := strings.ToLower(os.Getenv("TERM"))
	for _, s := range curlyTerms {
		if strings.Contains(term, s) {
			return true
		}
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "ghostty", "WezTerm":
		return true
	}
	return false
}

func Open() (*Term, error) {
	t := &Term{in: os.Stdin, out: bufio.NewWriterSize(os.Stdout, 1<<20), keys: make(chan Key, 64), winch: make(chan os.Signal, 1)}
	st, err := xterm.MakeRaw(int(t.in.Fd()))
	if err != nil {
		return nil, err
	}
	t.state = st
	// \x1b[>1u : kitty keyboard protocol, flag 1 (disambiguate) — sent blind,
	// the terminals that ignore it simply do not answer.
	t.WriteString("\x1b[?1049h\x1b[?2004h\x1b[?1000h\x1b[?1003h\x1b[?1006h\x1b[?1004h\x1b[>1u")
	t.probe()
	t.Size()
	t.Curly = curlySupported()
	theme.Curly = t.Curly
	signal.Notify(t.winch, syscall.SIGWINCH)
	go t.readLoop()
	return t, nil
}

// NewOffscreen gives a Term that draws into w and reads nothing: no probe, no
// raw mode. The screenshot generator (internal/ui/shot_test.go) draws whole
// frames through it.
func NewOffscreen(w io.Writer, cols, rows int) *Term {
	return &Term{out: bufio.NewWriterSize(w, 1<<20), keys: make(chan Key), winch: make(chan os.Signal, 1), Cols: cols, Rows: rows}
}

func (t *Term) Close() {
	t.WriteString("\x1b[<u\x1b[?1004l\x1b[?1006l\x1b[?1003l\x1b[?1000l\x1b[?2004l\x1b[0m\x1b[?25h\x1b[?1049l")
	t.Flush()
	xterm.Restore(int(t.in.Fd()), t.state)
}

func (t *Term) WriteString(s string)      { t.out.WriteString(s) }
func (t *Term) Flush()                    { t.out.Flush() }
func (t *Term) Keys() <-chan Key          { return t.keys }
func (t *Term) Resized() <-chan os.Signal { return t.winch }

// Size reads the size again in cells and, when known, in pixels.
func (t *Term) Size() {
	ws, err := unix.IoctlGetWinsize(int(t.in.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 || ws.Row == 0 {
		t.Cols, t.Rows = 80, 24
		return
	}
	t.Cols, t.Rows = int(ws.Col), int(ws.Row)
	if ws.Xpixel > 0 && ws.Ypixel > 0 {
		t.CellW, t.CellH = int(ws.Xpixel)/t.Cols, int(ws.Ypixel)/t.Rows
	}
}

// maxProbe : the answers to the probe are a few dozen bytes; above that the
// producer on the standard input is not a terminal answering us.
const maxProbe = 4 << 10

var reCell = regexp.MustCompile(`\x1b\[6;(\d+);(\d+)t`)

// reKbd : answer to CSI ? u (flags of the kitty keyboard protocol).
var reKbd = regexp.MustCompile(`\x1b\[\?\d+u`)

// reDA1 : answer to CSI c, which closes the probe. It is looked for anywhere
// in the buffer and no longer as a suffix: mouse tracking and focus reporting
// are on before the probe, so an event of the terminal can land right behind
// the answer — the suffix test then waited for the 500 ms timeout at every
// start, and dropped the answers it had already read.
var reDA1 = regexp.MustCompile(`\x1b\[\?[0-9;]*c`)

// probe asks for kitty (a=q), the cell size (CSI 16 t), the state of the
// keyboard protocol (CSI ? u), then DA1 (CSI c) which marks the end because
// the answers come in order.
func (t *Term) probe() {
	t.WriteString("\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\\x1b[16t\x1b[?u\x1b[c")
	t.Flush()
	var resp []byte
	buf := make([]byte, 256)
	for !reDA1.Match(resp) {
		if !t.wait(500 * time.Millisecond) {
			break
		}
		n, err := t.in.Read(buf)
		if err != nil || n == 0 {
			break
		}
		resp = append(resp, buf[:n]...)
		if len(resp) > maxProbe { // a terminal that talks without ever ending
			break
		}
	}
	t.Kitty = strings.Contains(string(resp), "_Gi=31;OK")
	// Only a terminal that speaks the keyboard protocol answers CSI ? u: safer
	// than the TERM list, a false positive would block Esc (no timeout left).
	t.KittyKbd = reKbd.Match(resp)
	if m := reCell.FindSubmatch(resp); m != nil {
		t.CellH, _ = strconv.Atoi(string(m[1]))
		t.CellW, _ = strconv.Atoi(string(m[2]))
	}
}

func (t *Term) wait(d time.Duration) bool {
	fds := []unix.PollFd{{Fd: int32(t.in.Fd()), Events: unix.POLLIN}}
	n, _ := unix.Poll(fds, int(d.Milliseconds()))
	return n > 0
}

func (t *Term) readLoop() {
	// A panic here would take the process down without the deferred Close of
	// main: the terminal would stay in raw mode, alternate screen on, mouse
	// tracking on. Closing the channel makes the interface leave the way it
	// does at the end of the input.
	defer func() {
		if r := recover(); r != nil {
			t.Panic = fmt.Sprintf("input loop panic: %v", r)
			close(t.keys)
		}
	}()
	var pending []byte
	buf := make([]byte, 4096)
	for {
		n, err := t.in.Read(buf)
		if err != nil {
			close(t.keys)
			return
		}
		pending = append(pending, buf[:n]...)
		keys, rest := Parse(pending, false)
		// With the keyboard protocol, Esc comes as CSI 27 u: nothing left to
		// guess, a pending ESC is a cut sequence for sure.
		if len(rest) > 0 && !t.KittyKbd && !pasteOpen(rest) && !t.wait(30*time.Millisecond) { // lone ESC or cut sequence
			more, _ := Parse(rest, true)
			keys, rest = append(keys, more...), nil
		}
		pending = rest
		for _, k := range keys {
			t.keys <- k
		}
	}
}
