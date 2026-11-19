// Package theme holds the colours, the SGR styles and the Ghostty theme files.
package theme

import (
	"bufio"
	"fmt"
	"github.com/govlog/ttyloom/internal/i18n"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type RGB struct{ R, G, B uint8 }

// Color : Kind 0 = terminal default, 1 = ANSI index, 2 = RGB.
type Color struct {
	Kind uint8
	Idx  uint8
	RGB  RGB
}

func Ansi(i uint8) Color { return Color{Kind: 1, Idx: i} }

func Hex(s string) (Color, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return Color{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return Color{}, false
	}
	return Color{Kind: 2, RGB: RGB{uint8(v >> 16), uint8(v >> 8), uint8(v)}}, true
}

// Mix : the colour a fraction f (0 to 1) of the way from a to b. Both must be
// RGB: there is nothing to mix in the 16 ANSI colours.
func Mix(a, b Color, f float64) Color {
	m := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*f)) }
	return Color{Kind: 2, RGB: RGB{m(a.RGB.R, b.RGB.R), m(a.RGB.G, b.RGB.G), m(a.RGB.B, b.RGB.B)}}
}

type Style struct {
	FG, BG                                               Color
	Bold, Italic, Underline, Curly, Strike, Dim, Reverse bool   // Curly : wavy underline, ignored if !Underline
	URL                                                  string // OSC 8 link
	Masked                                               bool   // the text shown is not URL: a click asks first
}

// Curly : undercurl available on this terminal (Ghostty/kitty/WezTerm/foot) —
// set once at start by term.Open(), read by Theme.Style(Link) and by the
// URL/TextURL/Email entities.
var Curly bool

// SGR gives the full sequence; it always starts with a reset.
func (s Style) SGR() string { return string(s.AppendSGR(make([]byte, 0, 64))) }

// AppendSGR appends the SGR parameters of c, each after a ';': ";31",
// ";38;2;r;g;b". fg picks the text code, else the background one. Nothing
// for the terminal default.
func (c Color) AppendSGR(b []byte, fg bool) []byte {
	switch c.Kind {
	case 1:
		base := 30
		if !fg {
			base = 40
		}
		b = append(b, ';')
		if c.Idx >= 8 {
			return strconv.AppendInt(b, int64(base+60+int(c.Idx)-8), 10)
		}
		return strconv.AppendInt(b, int64(base+int(c.Idx)), 10)
	case 2:
		p := ";38;2;"
		if !fg {
			p = ";48;2;"
		}
		return appendRGB(append(b, p...), c.RGB)
	}
	return b
}

// appendRGB appends r;g;b.
func appendRGB(b []byte, c RGB) []byte {
	b = strconv.AppendInt(b, int64(c.R), 10)
	b = append(b, ';')
	b = strconv.AppendInt(b, int64(c.G), 10)
	b = append(b, ';')
	return strconv.AppendInt(b, int64(c.B), 10)
}

// AppendSGR appends the sequence of SGR to b, with no allocation of its own:
// the intro writes one at almost every cell of a frame.
func (s Style) AppendSGR(b []byte) []byte {
	b = append(b, "\x1b[0"...)
	if s.Bold {
		b = append(b, ";1"...)
	}
	if s.Dim {
		b = append(b, ";2"...)
	}
	if s.Italic {
		b = append(b, ";3"...)
	}
	if s.Underline {
		if s.Curly {
			b = append(b, ";4:3"...)
		} else {
			b = append(b, ";4"...)
		}
	}
	if s.Reverse {
		b = append(b, ";7"...)
	}
	if s.Strike {
		b = append(b, ";9"...)
	}
	b = s.FG.AppendSGR(b, true)
	b = s.BG.AppendSGR(b, false)
	// Colour of the undercurl (58;2;r;g;b). Nothing for Kind 0 (terminal
	// default) and Kind 1 (Ansi): in this package a Color never holds a real
	// 256 index, only the 16-colour ANSI palette of Terminal().
	// ponytail: wire 58;5;n if a 256-indexed Kind ever shows up.
	if s.Underline && s.Curly && s.FG.Kind == 2 {
		b = appendRGB(append(b, ";58;2;"...), s.FG.RGB)
	}
	return append(b, 'm')
}

type Role int

const (
	Text Role = iota
	Dim
	Own
	Link
	Mention
	Code
	CodeBG
	Accent
	Act
	System
	Error
	StatusBG
	Sep
)

type Theme struct {
	Name    string
	Palette [16]Color
	FG, BG  Color
}

// Terminal gives the ANSI colours of the terminal, with no 24-bit colour.
func Terminal() Theme {
	t := Theme{Name: "terminal"}
	for i := range t.Palette {
		t.Palette[i] = Ansi(uint8(i))
	}
	return t
}

func (t Theme) Color(r Role) Color {
	switch r {
	case Dim, Sep:
		return t.Palette[8]
	case Own:
		return t.Palette[15]
	case Link, Accent:
		return t.Palette[4]
	case Mention:
		return t.Palette[5]
	case Code, Act:
		return t.Palette[3]
	case CodeBG, StatusBG:
		return t.Palette[0]
	case System:
		return t.Palette[2]
	case Error:
		return t.Palette[1]
	}
	return t.FG
}

var nickColors = [...]int{1, 2, 3, 4, 5, 6, 9, 10, 11, 12, 13, 14}

func (t Theme) Nick(id int64) Color {
	if id < 0 {
		id = -id
	}
	return t.Palette[nickColors[id%int64(len(nickColors))]]
}

func (t Theme) Style(r Role) Style {
	s := Style{FG: t.Color(r)}
	switch r {
	case Link:
		s.Underline, s.Curly = true, Curly
	case Own:
		s.Bold = true
	case Code:
		s.BG = t.Color(CodeBG)
	case StatusBG:
		s.FG, s.BG = t.FG, t.Color(StatusBG)
	}
	return s
}

// Parse reads a Ghostty theme file (palette = N=#hex, background, foreground).
func Parse(r io.Reader, name string) (Theme, error) {
	t := Terminal()
	t.Name = name
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "palette":
			is, hex, ok := strings.Cut(v, "=")
			i, err := strconv.Atoi(strings.TrimSpace(is))
			c, okc := Hex(hex)
			if ok && err == nil && okc && i >= 0 && i < 16 {
				t.Palette[i] = c
			}
		case "background":
			if c, ok := Hex(v); ok {
				t.BG = c
			}
		case "foreground":
			if c, ok := Hex(v); ok {
				t.FG = c
			}
		}
	}
	return t, sc.Err()
}

func Dirs() []string {
	home, _ := os.UserHomeDir()
	d := []string{filepath.Join(home, ".config/ttyloom/themes"), filepath.Join(home, ".config/ghostty/themes")}
	if r := os.Getenv("GHOSTTY_RESOURCES_DIR"); r != "" {
		d = append(d, filepath.Join(r, "themes"))
	}
	return append(d, "/usr/share/ghostty/themes")
}

// Names gives the theme names available, sorted, with no duplicate.
func Names() []string {
	seen := map[string]bool{}
	for _, d := range Dirs() {
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			if !e.IsDir() {
				seen[e.Name()] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// Load reads a theme by name (case insensitive). "" or "terminal" = Terminal().
func Load(name string) (Theme, error) {
	if name == "" || strings.EqualFold(name, "terminal") {
		return Terminal(), nil
	}
	for _, n := range Names() {
		if !strings.EqualFold(n, name) {
			continue
		}
		for _, d := range Dirs() {
			f, err := os.Open(filepath.Join(d, n))
			if err != nil {
				continue
			}
			defer f.Close()
			return Parse(f, n)
		}
	}
	return Theme{}, fmt.Errorf(i18n.T("theme_not_found"), name)
}

// GhosttyDefault gives the value of `theme =` in ~/.config/ghostty/config ("" when missing).
func GhosttyDefault() string {
	home, _ := os.UserHomeDir()
	b, err := os.ReadFile(filepath.Join(home, ".config/ghostty/config"))
	if err != nil {
		return ""
	}
	name := ""
	for _, l := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(l, "=")
		if ok && strings.TrimSpace(k) == "theme" {
			name = strings.TrimSpace(v)
		}
	}
	return ghosttyThemeName(name)
}

// ghosttyThemeName : theme name from the value of `theme =`.
// It handles "X" and "light:X,dark:Y" / "dark:Y,light:X" (any order, dark: wins).
func ghosttyThemeName(v string) string {
	v = strings.Trim(strings.TrimSpace(v), `"`)
	if !strings.Contains(v, ",") && !strings.HasPrefix(v, "light:") && !strings.HasPrefix(v, "dark:") {
		return v
	}
	parts := strings.Split(v, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	first := strings.TrimPrefix(strings.TrimPrefix(parts[0], "light:"), "dark:")
	for _, p := range parts {
		if d, ok := strings.CutPrefix(p, "dark:"); ok {
			return strings.TrimSpace(d)
		}
	}
	return strings.TrimSpace(first)
}
