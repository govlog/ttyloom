package term

import (
	"bytes"
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want []Key
	}{
		{"a", []Key{{Rune: 'a'}}},
		{"é", []Key{{Rune: 'é'}}},
		{"\x1b[A", []Key{{Code: Up}}},
		{"\x1b[1;3D", []Key{{Code: Left, Alt: true}}},
		{"\x1b[1;5D", []Key{{Code: Left, Ctrl: true}}},
		{"\x1b[1;5C", []Key{{Code: Right, Ctrl: true}}},
		{"\x1b[1;2A", []Key{{Code: Up, Shift: true}}},
		{"\x1b[1;2Q", []Key{{Code: F2, Shift: true}}},  // Shift+F2 (/net cycle)
		{"\x1b[12;2~", []Key{{Code: F2, Shift: true}}}, // the other form of the same key
		{"\x1b[5~", []Key{{Code: PgUp}}},
		{"\x1b[3~", []Key{{Code: Delete}}},
		{"\x1bOH", []Key{{Code: Home}}},
		{"\x1b3", []Key{{Rune: '3', Alt: true}}},
		{"\x1b\x7f", []Key{{Code: Backspace, Alt: true}}},
		{"\x18", []Key{{Code: Ctrl, Rune: 'x'}}},
		{"\r", []Key{{Code: Enter}}},
		{"\t", []Key{{Code: Tab}}},
		{"\x7f", []Key{{Code: Backspace}}},
		{"ab\x1b[B", []Key{{Rune: 'a'}, {Rune: 'b'}, {Code: Down}}},
		{"\x1b[200~ab\ncd\x1b[201~", []Key{{Code: Paste, Text: "ab\ncd"}}},
		{"\x1b[?62;22c", nil}, // DA1 answer: ignored
		{"\x1b[I", []Key{{Code: FocusIn}}},
		{"\x1b[O", []Key{{Code: FocusOut}}},
		{"a\x1b[Ob", []Key{{Rune: 'a'}, {Code: FocusOut}, {Rune: 'b'}}},
		{"\x1b\x1b[A", []Key{{Code: Esc}, {Code: Up}}},
		{"\x1bé", []Key{{Rune: 'é', Alt: true}}},
	}
	for _, c := range cases {
		got, rest := Parse([]byte(c.in), false)
		if !reflect.DeepEqual(got, c.want) || rest != nil {
			t.Errorf("%q: got %+v rest %q", c.in, got, rest)
		}
	}
	if k, rest := Parse([]byte("\x1b["), false); k != nil || string(rest) != "\x1b[" {
		t.Errorf("incomplete: %+v %q", k, rest)
	}
	if k, _ := Parse([]byte("\x1b"), true); len(k) != 1 || k[0].Code != Esc {
		t.Errorf("esc final: %+v", k)
	}
	if k, rest := Parse([]byte("\x1b\xc3"), false); k != nil || string(rest) != "\x1b\xc3" {
		t.Errorf("incomplete alt utf8: %+v %q", k, rest)
	}
}

func TestParseMouse(t *testing.T) {
	cases := []struct {
		in   string
		want Key
	}{
		{"\x1b[<0;12;5M", Key{Code: Mouse, Mouse: MouseEvent{Button: 0, X: 11, Y: 4, Press: true}}},
		{"\x1b[<0;12;5m", Key{Code: Mouse, Mouse: MouseEvent{Button: 0, X: 11, Y: 4, Press: false}}},
		{"\x1b[<64;1;1M", Key{Code: Mouse, Mouse: MouseEvent{Button: 64, X: 0, Y: 0, Press: true}}},
		{"\x1b[<65;80;24M", Key{Code: Mouse, Mouse: MouseEvent{Button: 65, X: 79, Y: 23, Press: true}}},
		{"\x1b[<8;3;3M", Key{Code: Mouse, Mouse: MouseEvent{Button: 0, X: 2, Y: 2, Press: true}}}, // Alt+click: hidden modifier
	}
	for _, c := range cases {
		got, rest := Parse([]byte(c.in), false)
		if len(got) != 1 || got[0] != c.want || rest != nil {
			t.Errorf("%q: got %+v rest %q", c.in, got, rest)
		}
	}
	if k, rest := Parse([]byte("\x1b[<0;12"), false); k != nil || string(rest) != "\x1b[<0;12" {
		t.Errorf("incomplete: %+v %q", k, rest)
	}
}

func TestParseMouseMotion(t *testing.T) {
	want := Key{Code: Mouse, Mouse: MouseEvent{Button: 0, X: 9, Y: 4, Press: true, Motion: true}}
	if got, rest := Parse([]byte("\x1b[<32;10;5M"), false); len(got) != 1 || got[0] != want || rest != nil {
		t.Errorf("left-button drag: %+v rest %q", got, rest)
	}
	want = Key{Code: Mouse, Mouse: MouseEvent{Button: 3, X: 9, Y: 4, Press: true, Motion: true}}
	if got, _ := Parse([]byte("\x1b[<35;10;5M"), false); len(got) != 1 || got[0] != want { // hover with no button (?1003)
		t.Errorf("hover: %+v", got)
	}
}

func TestParseF2(t *testing.T) {
	for _, in := range []string{"\x1b[12~", "\x1bOQ"} {
		got, rest := Parse([]byte(in), false)
		if len(got) != 1 || got[0].Code != F2 || rest != nil {
			t.Errorf("%q: got %+v rest %q", in, got, rest)
		}
	}
}

func TestParseF3F4(t *testing.T) {
	cases := []struct {
		in   string
		code Code
	}{
		{"\x1b[13~", F3}, {"\x1bOR", F3},
		{"\x1b[14~", F4}, {"\x1bOS", F4},
		{"\x1b[15~", F5},
		{"\x1b[17~", F6},
	}
	for _, c := range cases {
		got, rest := Parse([]byte(c.in), false)
		if len(got) != 1 || got[0].Code != c.code || rest != nil {
			t.Errorf("%q: got %+v rest %q", c.in, got, rest)
		}
	}
}

func TestParseF7(t *testing.T) {
	got, rest := Parse([]byte("\x1b[18~"), false)
	if len(got) != 1 || got[0].Code != F7 || rest != nil {
		t.Errorf("got %+v rest %q", got, rest)
	}
}

func TestParseKittyKeys(t *testing.T) {
	cases := []struct {
		in   string
		want []Key
	}{
		{"\x1b[13;2u", []Key{{Code: Enter, Shift: true}}},
		{"\x1b[13;5u", []Key{{Code: Enter, Ctrl: true}}},
		{"\x1b[13u", []Key{{Code: Enter}}},
		{"\x1b[57414;2u", []Key{{Code: Enter, Shift: true}}}, // Enter of the numeric keypad
		{"\x1b[27u", []Key{{Code: Esc}}},
		{"\x1b[97;3u", []Key{{Rune: 'a', Alt: true}}},
		{"\x1b[120;5u", []Key{{Code: Ctrl, Rune: 'x'}}}, // same key as \x18
		{"\x1b[9;2u", []Key{{Code: Tab, Shift: true}}},
		{"\x1b[127u", []Key{{Code: Backspace}}},
		{"\x1b[57400u", []Key{{Rune: '1'}}},             // numeric keypad
		{"\x1b[91;5u", nil},                             // Ctrl+[ : no action, and above all not text
		{"\x1b[57441;2u", nil},                          // left Shift alone
		{"\x1b[?1u", nil},                               // answer of the terminal
		{"\x1b[97;3:1u", []Key{{Rune: 'a', Alt: true}}}, // sub-parameter (event type)
	}
	for _, c := range cases {
		got, rest := Parse([]byte(c.in), false)
		if !reflect.DeepEqual(got, c.want) || rest != nil {
			t.Errorf("%q: got %+v rest %q", c.in, got, rest)
		}
	}
	legacy, _ := Parse([]byte("\x18"), false)
	kitty, _ := Parse([]byte("\x1b[120;5u"), false)
	if !reflect.DeepEqual(legacy, kitty) {
		t.Errorf("Ctrl+X : %+v ≠ %+v", legacy, kitty)
	}
}

// With the kitty keyboard protocol, F1..F4 arrive as CSI P/Q/S (with an
// optional "1;mods" prefix), not as SS3 or CSI 12~. F3 is the exception: it
// comes as CSI 13~, CSI R being the cursor position report (see TestCPRNotF3).
func TestParseKittyFKeys(t *testing.T) {
	for in, want := range map[string]Code{"\x1b[P": F1, "\x1b[Q": F2, "\x1b[1;1Q": F2, "\x1b[1;5R": F3, "\x1b[S": F4, "\x1bOQ": F2, "\x1b[12~": F2} {
		keys, rest := Parse([]byte(in), true)
		if len(keys) != 1 || keys[0].Code != want || len(rest) != 0 {
			t.Fatalf("%q: got %+v rest %q, want code %d", in, keys, rest, want)
		}
	}
}

// TestPasteWithoutEnd : a bracketed paste with no ESC[201~ made the pending
// buffer of readLoop grow as long as the producer wrote. Under the cap it
// still waits for the rest; above it, everything is dropped.
func TestPasteWithoutEnd(t *testing.T) {
	small := append([]byte("\x1b[200~"), bytes.Repeat([]byte("a"), 32)...)
	if keys, rest := Parse(small, false); len(keys) != 0 || len(rest) != len(small) {
		t.Fatalf("short paste: %d keys, %d bytes pending", len(keys), len(rest))
	}
	big := append([]byte("\x1b[200~"), bytes.Repeat([]byte("a"), maxPasteCSI+1)...)
	if keys, rest := Parse(big, false); len(keys) != 0 || len(rest) != 0 {
		t.Fatalf("paste without end: %d keys, %d bytes pending", len(keys), len(rest))
	}
	// A normal paste still goes through whole.
	ok := []byte("\x1b[200~bonjour\x1b[201~")
	if keys, rest := Parse(ok, false); len(keys) != 1 || keys[0].Code != Paste || keys[0].Text != "bonjour" || len(rest) != 0 {
		t.Fatalf("normal paste: %+v rest %d", keys, len(rest))
	}
}

// TestCPRNotF3 : a cursor position report (CSI <line>;<col> R) left on the
// tty by another program must not become F3 — that opened the member box, so
// a channels.getParticipants call on the network.
func TestCPRNotF3(t *testing.T) {
	for _, in := range []string{"\x1b[R", "\x1b[24;80R", "\x1b[1;1R", "\x1b[6;2R", "\x1b[2;3P"} {
		if keys, rest := Parse([]byte(in), true); len(keys) != 0 || len(rest) != 0 {
			t.Errorf("%q → %+v rest %q", in, keys, rest)
		}
	}
	// The two real forms of F3, and the modified form of the kitty protocol.
	for _, in := range []string{"\x1b[13~", "\x1bOR", "\x1b[1;5R"} {
		keys, _ := Parse([]byte(in), true)
		if len(keys) != 1 || keys[0].Code != F3 {
			t.Errorf("%q → %+v", in, keys)
		}
	}
}

// TestProbeAnswers : the probe's answers are recognized in the order the
// terminal sends them, and the end (DA1) is found even when a mouse or
// focus event follows it — mouse tracking and focus are armed before the
// probe, and the old suffix test used to wait the 500 ms delay on every
// startup.
func TestProbeAnswers(t *testing.T) {
	resp := []byte("\x1b_Gi=31;OK\x1b\\\x1b[6;36;18t\x1b[?1u\x1b[?62;22c")
	for _, suffix := range []string{"", "\x1b[<35;10;5M", "\x1b[I"} {
		buf := append(append([]byte(nil), resp...), suffix...)
		if !reDA1.Match(buf) {
			t.Errorf("DA1 not found with suffix %q", suffix)
		}
		if !reKbd.Match(buf) {
			t.Errorf("keyboard response not found with suffix %q", suffix)
		}
		m := reCell.FindSubmatch(buf)
		if m == nil || string(m[1]) != "36" || string(m[2]) != "18" {
			t.Errorf("cell: %q", m)
		}
	}
	// A terminal silent on the keyboard protocol: no false positive, and the
	// cell response must not pass for a DA1.
	quiet := []byte("\x1b[6;36;18t\x1b[?62;22c")
	if reKbd.Match(quiet) {
		t.Error("false positive on keyboard protocol")
	}
	if reDA1.Match([]byte("\x1b[6;36;18t")) {
		t.Error("CSI 16 t response mistaken for a DA1")
	}
}

// A bare \n (terminal keybind like Ghostty shift+enter=text:\n) must keep
// the Shift+Enter meaning; \r stays a plain Enter.
func TestLFIsShiftEnter(t *testing.T) {
	keys, _ := Parse([]byte("\n"), true)
	if len(keys) != 1 || keys[0].Code != Enter || !keys[0].Shift {
		t.Fatalf("\\n: got %+v", keys)
	}
	keys, _ = Parse([]byte("\r"), true)
	if len(keys) != 1 || keys[0].Code != Enter || keys[0].Shift {
		t.Fatalf("\\r: got %+v", keys)
	}
}

// TestPasteCutNeverTypesKeys : a paste cut before its ESC[201~ and then
// finalised (30 ms of silence on a terminal without the kitty protocol) must
// never be replayed as keystrokes — each \r would send a message, and a line
// starting with "/" would run a command.
func TestPasteCutNeverTypesKeys(t *testing.T) {
	keys, _ := Parse([]byte("\x1b[200~/quit\rb"), true)
	if len(keys) != 1 || keys[0].Code != Paste || keys[0].Text != "/quit\rb" {
		t.Fatalf("cut paste: %+v, want one Paste", keys)
	}
	// readLoop keeps waiting while a paste is open rather than finalise it.
	if !pasteOpen([]byte("\x1b[200~abc")) || pasteOpen([]byte("\x1b[A")) {
		t.Fatal("pasteOpen")
	}
}

// A terminal with a scroll multiplier (Ghostty: 3) writes one notch as three
// identical wheel events in one go; they count for one, otherwise the panel
// moved nine rows a notch and a zoom step was three.
func TestWheelBurstCollapsed(t *testing.T) {
	keys, _ := Parse([]byte("\x1b[<64;5;5M\x1b[<64;5;5M\x1b[<64;5;5M"), false)
	if len(keys) != 1 || keys[0].Mouse.Button != 64 {
		t.Fatalf("burst: %+v", keys)
	}
	keys, _ = Parse([]byte("\x1b[<64;5;5M\x1b[<65;5;5M\x1b[<0;5;5M\x1b[<0;5;5M"), false)
	if len(keys) != 4 {
		t.Fatalf("events that are not one notch kept apart: %+v", keys)
	}
}
