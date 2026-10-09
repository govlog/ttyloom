// Package term holds raw mode, size, keyboard and the kitty graphics probe.
package term

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"slices"
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
	sync  bool // a synchronized update is open until the next Flush
	state *xterm.State
	keys  chan Key
	winch chan os.Signal
	// wake : a byte written on wake[1] stops the read loop (Hand), which then
	// gives parked the channel it waits on before it reads again.
	wake   [2]int
	parked chan chan struct{}
	held   bool // another program has the terminal (Hand): writes dropped

	Cols, Rows   int
	CellW, CellH int    // pixels; 0 = unknown
	Kitty        bool   // kitty graphics protocol supported
	KittyShm     bool   // ... and read a shared memory object: a terminal on this machine
	Sixel        bool   // sixel graphics (attribute 4 of the DA1 answer)
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

// modesOn : bracketed paste, mouse (press, motion, SGR), focus, and the kitty
// keyboard protocol flag 1 (disambiguate) — sent blind, the terminals that
// ignore it simply do not answer. modesOff takes them back, in reverse order.
const (
	modesOn  = "\x1b[?2004h\x1b[?1000h\x1b[?1003h\x1b[?1006h\x1b[?1004h\x1b[>1u"
	modesOff = "\x1b[<u\x1b[?1004l\x1b[?1006l\x1b[?1003l\x1b[?1000l\x1b[?2004l"
)

func Open() (*Term, error) {
	t := &Term{in: os.Stdin, out: bufio.NewWriterSize(os.Stdout, 1<<20), keys: make(chan Key, 64), winch: make(chan os.Signal, 1),
		parked: make(chan chan struct{})}
	if err := unix.Pipe2(t.wake[:], unix.O_CLOEXEC); err != nil {
		return nil, err
	}
	st, err := xterm.MakeRaw(int(t.in.Fd()))
	if err != nil {
		return nil, err
	}
	t.state = st
	t.WriteString("\x1b[?1049h" + modesOn)
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
	t.held = false // a player killed by the end of the session: the terminal comes back all the same
	t.WriteString(modesOff + "\x1b[0m\x1b[?25h\x1b[?1049l")
	t.Flush()
	xterm.Restore(int(t.in.Fd()), t.state)
}

// WriteString queues s for the next Flush, inside a synchronized update
// (DEC mode 2026): the terminal holds its paint until Flush ends it, so a
// frame never shows half drawn — a row erased and not yet written again, an
// image placed twice. A terminal without the mode ignores it.
func (t *Term) WriteString(s string) {
	if t.held {
		return
	}
	if !t.sync {
		t.sync = true
		t.out.WriteString("\x1b[?2026h")
	}
	t.out.WriteString(s)
}

// Flush ends the synchronized update and writes everything queued.
func (t *Term) Flush() {
	if t.held {
		return
	}
	if t.sync {
		t.sync = false
		t.out.WriteString("\x1b[?2026l")
	}
	t.out.Flush()
}

// Hand gives the terminal to another program (mpv) until back is called. The
// read loop stops reading, so the keys go to the program; the modes of Open
// are off; every write is dropped. The alternate screen and raw mode stay:
// no flash of the shell between the two, and Ctrl+C or Ctrl+Z reach the
// program as bytes instead of signalling the whole process group. back turns
// the modes on again, wipes what the program left (text, kitty images) and
// lets the loop read again; the caller then draws a full frame.
func (t *Term) Hand() (back func()) {
	t.WriteString(modesOff + "\x1b[0m")
	t.Flush()
	t.held = true
	var resume chan struct{}
	if t.in != nil { // offscreen: no read loop
		unix.Write(t.wake[1], []byte{0})
		keys := t.keys
		for resume == nil && keys != nil {
			select {
			case resume = <-t.parked:
			case _, ok := <-keys: // the loop may wait on a full channel: what was typed before goes
				if !ok {
					keys = nil // the loop has ended: nothing reads the input any more
				}
			}
		}
	}
	return func() {
		t.held = false
		s := "\x1b[?1049h" + modesOn + "\x1b[2J" // the program may have left the alternate screen
		if t.Kitty {
			s += "\x1b_Ga=d,d=A,q=2\x1b\\" // every image, data included
		}
		t.WriteString(s)
		t.Flush()
		if resume != nil {
			close(resume)
		}
	}
}

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
var reDA1 = regexp.MustCompile(`\x1b\[\?([0-9;]*)c`)

// probe asks for kitty (a=q, also through shared memory), the cell size
// (CSI 16 t), the state of the keyboard protocol (CSI ? u), then DA1 (CSI c)
// which marks the end because the answers come in order.
func (t *Term) probe() {
	q := "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\"
	// The same query read from a shared memory object, which only a terminal
	// on this machine reaches (not through ssh). The terminal unlinks it once
	// read; the Remove is for the one that does not.
	if shm := shmProbe(); shm != "" {
		q += "\x1b_Gi=32,s=1,v=1,a=q,t=s,f=24;" + base64.StdEncoding.EncodeToString([]byte(shm)) + "\x1b\\"
		defer os.Remove("/dev/shm" + shm)
	}
	t.WriteString(q + "\x1b[16t\x1b[?u\x1b[c")
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
	t.KittyShm = strings.Contains(string(resp), "_Gi=32;OK")
	if m := reDA1.FindSubmatch(resp); m != nil {
		t.Sixel = slices.Contains(strings.Split(string(m[1]), ";")[1:], "4") // the first one is the class
	}
	// Only a terminal that speaks the keyboard protocol answers CSI ? u: safer
	// than the TERM list, a false positive would block Esc (no timeout left).
	t.KittyKbd = reKbd.Match(resp)
	if m := reCell.FindSubmatch(resp); m != nil {
		t.CellH, _ = strconv.Atoi(string(m[1]))
		t.CellW, _ = strconv.Atoi(string(m[2]))
	}
}

// shmProbe writes one RGB pixel to a new POSIX shared memory object and gives
// its name; "" with no /dev/shm (not Linux: mpv then sends its frames as
// escape codes). O_EXCL: never through a link someone else left there.
func shmProbe() string {
	name := fmt.Sprintf("/ttyloom-%d", os.Getpid())
	f, err := os.OpenFile("/dev/shm"+name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.Write([]byte{0, 0, 0}); err != nil {
		return ""
	}
	return name
}

func (t *Term) wait(d time.Duration) bool {
	fds := []unix.PollFd{{Fd: int32(t.in.Fd()), Events: unix.POLLIN}}
	n, _ := unix.Poll(fds, int(d.Milliseconds()))
	return n > 0
}

// ready waits for input. false: Hand asks the loop to stop reading.
func (t *Term) ready() bool {
	fds := []unix.PollFd{{Fd: int32(t.in.Fd()), Events: unix.POLLIN}, {Fd: int32(t.wake[0]), Events: unix.POLLIN}}
	for {
		if _, err := unix.Poll(fds, -1); err == unix.EINTR {
			continue
		}
		if fds[1].Revents != 0 {
			unix.Read(t.wake[0], make([]byte, 1))
			return false
		}
		return true // input, hang-up or poll error: the Read says which
	}
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
	var decoder Decoder
	buf := make([]byte, 4096)
	for {
		if !t.ready() {
			resume := make(chan struct{})
			t.parked <- resume
			<-resume
			continue
		}
		n, err := t.in.Read(buf)
		if err != nil {
			close(t.keys)
			return
		}
		keys := decoder.Feed(buf[:n], false)
		// With the keyboard protocol, Esc comes as CSI 27 u: nothing left to
		// guess, a pending ESC is a cut sequence for sure.
		if len(decoder.pending) > 0 && !decoder.discardPaste && !t.KittyKbd && !pasteOpen(decoder.pending) && !t.wait(30*time.Millisecond) {
			keys = append(keys, decoder.Feed(nil, true)...)
		}
		for _, k := range keys {
			t.keys <- k
		}
	}
}
