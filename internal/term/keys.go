package term

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Code int

const (
	None Code = iota // Rune holds the character
	Enter
	Backspace
	Delete
	Tab
	Esc
	Up
	Down
	Left
	Right
	Home
	End
	PgUp
	PgDn
	Paste // Text holds the paste
	Ctrl  // Rune = lower case letter
	Mouse // Mouse holds the event
	F1
	F2
	F3 // member box
	F4
	F5
	F6
	F7
	FocusIn  // the terminal takes the focus back (CSI ?1004h)
	FocusOut // the terminal loses the focus
)

type MouseEvent struct {
	Button int
	X, Y   int
	Press  bool
	Motion bool // motion (bit 32): drag with a button, or plain hover
}

type Key struct {
	Code  Code
	Rune  rune
	Alt   bool
	Shift bool // kitty protocol only (Shift+Enter…)
	Ctrl  bool // same; Ctrl+letter keeps the Code: Ctrl, Rune: letter form
	Text  string
	Mouse MouseEvent
}

// Parse decodes buf into keys. rest = bytes of a cut sequence, to keep for
// the next call. final=true: nothing more will come, a lone ESC becomes Esc.
func Parse(buf []byte, final bool) (keys []Key, rest []byte) {
	for len(buf) > 0 {
		k, n, ok := parseOne(buf, final)
		if !ok {
			return keys, buf
		}
		buf = buf[n:]
		if k.Code == None && k.Rune == 0 {
			continue
		}
		// One notch of the wheel: a terminal with a scroll multiplier (Ghostty,
		// 3 by default) writes it as that many identical events in one go.
		if n := len(keys); n > 0 && k.Code == Mouse && k.Mouse.Button >= 64 && keys[n-1] == k {
			continue
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func parseOne(b []byte, final bool) (Key, int, bool) {
	c := b[0]
	switch {
	case c == 0x1b:
		if len(b) == 1 {
			if final {
				return Key{Code: Esc}, 1, true
			}
			return Key{}, 0, false
		}
		switch b[1] {
		case '[':
			return parseCSI(b, final)
		case 'O':
			if len(b) < 3 {
				if final {
					return Key{Code: Esc}, 1, true
				}
				return Key{}, 0, false
			}
			switch b[2] {
			case 'A':
				return Key{Code: Up}, 3, true
			case 'B':
				return Key{Code: Down}, 3, true
			case 'C':
				return Key{Code: Right}, 3, true
			case 'D':
				return Key{Code: Left}, 3, true
			case 'H':
				return Key{Code: Home}, 3, true
			case 'F':
				return Key{Code: End}, 3, true
			case 'Q':
				return Key{Code: F2}, 3, true
			case 'R':
				return Key{Code: F3}, 3, true
			case 'S':
				return Key{Code: F4}, 3, true
			}
			return Key{Code: Esc}, 1, true
		default: // ESC x = Alt+x
			if b[1] == 0x1b {
				return Key{Code: Esc}, 1, true
			}
			k, n, ok := parseOne(b[1:], final)
			if !ok {
				return Key{}, 0, false
			}
			k.Alt = true
			return k, n + 1, true
		}
	case c == '\r':
		return Key{Code: Enter}, 1, true
	case c == '\n':
		// In raw mode a real Enter arrives as \r. A bare \n comes from a
		// terminal keybind such as Ghostty's `shift+enter=text:\n`: keep the
		// line-break meaning.
		return Key{Code: Enter, Shift: true}, 1, true
	case c == '\t':
		return Key{Code: Tab}, 1, true
	case c == 0x7f || c == 0x08:
		return Key{Code: Backspace}, 1, true
	case c < 0x20:
		return Key{Code: Ctrl, Rune: rune('a' + c - 1)}, 1, true
	}
	if !utf8.FullRune(b) {
		if final {
			return Key{}, 1, true
		}
		return Key{}, 0, false
	}
	r, n := utf8.DecodeRune(b)
	return Key{Rune: r}, n, true
}

// maxPasteCSI : bracketed paste kept waiting for its ESC[201~ at most.
const maxPasteCSI = 1 << 20

// pasteOpen : b starts a bracketed paste whose end has not come yet. readLoop
// keeps waiting on it rather than finalise after 30 ms of silence: over a slow
// link the terminal hands a paste over in pieces.
func pasteOpen(b []byte) bool { return bytes.HasPrefix(b, []byte("\x1b[200~")) }

func parseCSI(b []byte, final bool) (Key, int, bool) {
	i := 2
	for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
		i++
	}
	if i >= len(b) {
		if final {
			return Key{Code: Esc}, 1, true
		}
		return Key{}, 0, false
	}
	params, fin := string(b[2:i]), b[i]
	n := i + 1
	if len(params) > 0 && params[0] == '<' && (fin == 'M' || fin == 'm') { // SGR mouse
		parts := strings.Split(params[1:], ";")
		if len(parts) != 3 {
			return Key{}, n, true
		}
		btn, err1 := strconv.Atoi(parts[0])
		x, err2 := strconv.Atoi(parts[1])
		y, err3 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil || err3 != nil {
			return Key{}, n, true
		}
		px, py := x-1, y-1
		if px < 0 {
			px = 0
		}
		if py < 0 {
			py = 0
		}
		// bit 32 = motion (?1002 with a button, ?1003 without); 4/8/16 = shift/meta/ctrl.
		m := MouseEvent{Button: btn &^ (4 | 8 | 16 | 32), X: px, Y: py, Press: fin == 'M', Motion: btn&32 != 0}
		return Key{Code: Mouse, Mouse: m}, n, true
	}
	if params == "200" && fin == '~' { // bracketed paste
		end := bytes.Index(b[n:], []byte("\x1b[201~"))
		if end < 0 {
			// No end marker in sight: the buffer of readLoop would grow
			// without bound as long as the producer keeps writing. Dropped
			// above the cap — a paste is capped at 64 KB in the input line
			// anyway (ui.maxPasteBytes).
			if len(b)-n > maxPasteCSI {
				return Key{}, len(b), true
			}
			if final {
				// Never Esc + the text as keystrokes: each \r of the paste
				// would send a message, a line starting with "/" would run a
				// command. What is there goes out as the paste it is.
				return Key{Code: Paste, Text: string(b[n:])}, len(b), true
			}
			return Key{}, 0, false
		}
		return Key{Code: Paste, Text: string(b[n : n+end])}, n + end + 6, true
	}
	p1, mod := params, ""
	if a, m, ok := strings.Cut(params, ";"); ok {
		p1, mod = a, m
	}
	if fin == 'u' { // kitty keyboard protocol
		return kittyKey(p1, mod), n, true
	}
	// Modifiers of the legacy CSI form (CSI 1;<mods> C…): mods = 1 + bits,
	// the same coding as the kitty protocol (1 Shift, 2 Alt, 4 Ctrl).
	k := Key{}
	if m, err := strconv.Atoi(csiField(mod)); err == nil && m > 0 {
		m--
		k.Shift, k.Alt, k.Ctrl = m&1 != 0, m&2 != 0, m&4 != 0
	}
	if strings.IndexByte("PQRS", fin) >= 0 && !fnKeyCSI(p1, mod, fin) {
		return Key{}, n, true // answer of the terminal (cursor position…), not a key
	}
	switch fin {
	case 'A':
		k.Code = Up
	case 'B':
		k.Code = Down
	case 'C':
		k.Code = Right
	case 'D':
		k.Code = Left
	case 'H':
		k.Code = Home
	case 'F':
		k.Code = End
	case 'P':
		k.Code = F1
	case 'Q':
		k.Code = F2
	case 'R':
		k.Code = F3
	case 'S':
		k.Code = F4
	case 'I': // focus: CSI I / CSI O, with no parameter
		k.Code = FocusIn
	case 'O':
		k.Code = FocusOut
	case '~':
		switch p1 {
		case "1", "7":
			k.Code = Home
		case "4", "8":
			k.Code = End
		case "3":
			k.Code = Delete
		case "5":
			k.Code = PgUp
		case "6":
			k.Code = PgDn
		case "12":
			k.Code = F2
		case "13":
			k.Code = F3
		case "14":
			k.Code = F4
		case "15":
			k.Code = F5
		case "17": // 16 is not given out (xterm F6)
			k.Code = F6
		case "18":
			k.Code = F7
		default:
			return Key{}, n, true
		}
	default: // unknown sequence (answer of the terminal…): ignored
		return Key{}, n, true
	}
	return k, n, true
}

// fnKeyCSI tells whether CSI <p1> ; <mod> <fin> really is F1..F4 of the kitty
// keyboard protocol (CSI [1;mods] P/Q/R/S) and not an answer of the terminal.
// R above all: CSI <line>;<col> R is a cursor position report, which another
// program on the same tty leaves behind — read as F3 it opened the member
// box, so a channels.getParticipants call on the network. kitty sends a plain
// F3 as CSI 13~, precisely because of that clash, so CSI R is a key only with
// a real modifier; the report of the home position (CSI 1;1R) is refused too.
func fnKeyCSI(p1, mod string, fin byte) bool {
	if p1 != "" && p1 != "1" {
		return false
	}
	if fin == 'R' {
		return p1 == "1" && mod != "" && mod != "1"
	}
	return true
}

// csiField : first field of a CSI parameter; sub-parameters (":", shifted key,
// event type) and the fields after (";", text) are dropped.
func csiField(s string) string {
	if i := strings.IndexAny(s, ":;"); i >= 0 {
		return s[:i]
	}
	return s
}

// kpRunes : numeric keypad, codes 57399.. (the disambiguate mode keeps them
// apart from the digits of the main keyboard).
const kpRunes = "0123456789./*-+"

// kittyKey decodes CSI <code> ; <mods> u (kitty keyboard protocol). code =
// Unicode codepoint or functional key; mods = 1 + bits (1 Shift, 2 Alt,
// 4 Ctrl). Ctrl+letter keeps the old Key{Code: Ctrl} form; a key of the
// private area that is not handled (modifiers alone, direction pad) is
// ignored rather than put into the input.
func kittyKey(p1, mod string) Key {
	code, err := strconv.Atoi(csiField(p1))
	if err != nil {
		return Key{} // answer of the terminal (CSI ? 1 u…) or unknown sequence
	}
	m := 1
	if v, err := strconv.Atoi(csiField(mod)); err == nil {
		m = v
	}
	m--
	k := Key{Shift: m&1 != 0, Alt: m&2 != 0, Ctrl: m&4 != 0}
	switch {
	case code == 13 || code == 57414: // Enter, Enter of the numeric keypad
		k.Code = Enter
	case code == 27:
		k.Code = Esc
	case code == 9:
		k.Code = Tab
	case code == 127:
		k.Code = Backspace
	case code >= 57399 && code < 57399+len(kpRunes):
		k.Rune = rune(kpRunes[code-57399])
	case code >= 57344: // private area: functional key not handled
		return Key{}
	case k.Ctrl:
		r := rune(code)
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if r < 'a' || r > 'z' {
			return Key{} // Ctrl+[ , Ctrl+digit…: no action, and never text
		}
		return Key{Code: Ctrl, Rune: r, Alt: k.Alt}
	default:
		k.Rune = rune(code)
	}
	return k
}
