// Package theme holds the colours, the SGR styles and the Ghostty theme files.
package theme

import (
	"bufio"
	"fmt"
	"github.com/govlog/ttyloom/internal/i18n"
	"io"
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

func (c Color) sgr(fg bool) string {
	switch c.Kind {
	case 1:
		base := 30
		if !fg {
			base = 40
		}
		if c.Idx >= 8 {
			return strconv.Itoa(base + 60 + int(c.Idx) - 8)
		}
		return strconv.Itoa(base + int(c.Idx))
	case 2:
		p := "38"
		if !fg {
			p = "48"
		}
		return fmt.Sprintf("%s;2;%d;%d;%d", p, c.RGB.R, c.RGB.G, c.RGB.B)
	}
	return ""
}

// underlineSGR : colour of the undercurl (58;2;r;g;b). Nothing for Kind 0
// (terminal default) and Kind 1 (Ansi): in this package a Color never holds a
// real 256 index, only the 16-colour ANSI palette of Terminal().
// ponytail: wire 58;5;n if a 256-indexed Kind ever shows up.
func (c Color) underlineSGR() string {
	if c.Kind == 2 {
		return fmt.Sprintf("58;2;%d;%d;%d", c.RGB.R, c.RGB.G, c.RGB.B)
	}
	return ""
}

type Style struct {
	FG, BG                                               Color
	Bold, Italic, Underline, Curly, Strike, Dim, Reverse bool   // Curly : wavy underline, ignored if !Underline
	URL                                                  string // OSC 8 link
}

// Curly : undercurl available on this terminal (Ghostty/kitty/WezTerm/foot) —
// set once at start by term.Open(), read by Theme.Style(Link) and by the
// URL/TextURL/Email entities.
var Curly bool

// SGR gives the full sequence; it always starts with a reset.
func (s Style) SGR() string {
	var b strings.Builder
	b.WriteString("\x1b[0")
	if s.Bold {
		b.WriteString(";1")
	}
	if s.Dim {
		b.WriteString(";2")
	}
	if s.Italic {
		b.WriteString(";3")
	}
	if s.Underline {
		if s.Curly {
			b.WriteString(";4:3")
		} else {
			b.WriteString(";4")
		}
	}
	if s.Reverse {
		b.WriteString(";7")
	}
	if s.Strike {
		b.WriteString(";9")
	}
	if p := s.FG.sgr(true); p != "" {
		b.WriteString(";" + p)
	}
	if p := s.BG.sgr(false); p != "" {
		b.WriteString(";" + p)
	}
	if s.Underline && s.Curly {
		if p := s.FG.underlineSGR(); p != "" {
			b.WriteString(";" + p)
		}
	}
	b.WriteString("m")
	return b.String()
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
	first := strings.TrimPrefix(strings.TrimPrefix(parts[0], "light:"), "dark:")
	for _, p := range parts {
		if d, ok := strings.CutPrefix(p, "dark:"); ok {
			return d
		}
	}
	return first
}
