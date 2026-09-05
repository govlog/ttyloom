package theme

import (
	"strings"
	"testing"
)

const sample = `palette = 0=#45475a
palette = 1=#f38ba8
palette = 4=#89b4fa
background = #1e1e2e
foreground = #cdd6f4
cursor-color = #f5e0dc
`

func TestParse(t *testing.T) {
	th, err := Parse(strings.NewReader(sample), "Test")
	if err != nil {
		t.Fatal(err)
	}
	if th.Name != "Test" || th.Palette[1] != (Color{Kind: 2, RGB: RGB{0xf3, 0x8b, 0xa8}}) {
		t.Fatalf("palette: %+v", th.Palette[1])
	}
	if th.BG != (Color{Kind: 2, RGB: RGB{0x1e, 0x1e, 0x2e}}) || th.FG.Kind != 2 {
		t.Fatalf("bg/fg: %+v %+v", th.BG, th.FG)
	}
	if th.Palette[2] != Ansi(2) { // missing → ANSI
		t.Fatalf("palette[2]: %+v", th.Palette[2])
	}
	if th.Style(Link).FG != th.Palette[4] || !th.Style(Link).Underline {
		t.Fatalf("link style: %+v", th.Style(Link))
	}
}

func TestSGR(t *testing.T) {
	c, _ := Hex("#ff0000")
	if got := (Style{FG: c, Bold: true}).SGR(); got != "\x1b[0;1;38;2;255;0;0m" {
		t.Fatalf("rgb: %q", got)
	}
	if got := (Style{FG: Ansi(9), BG: Ansi(0)}).SGR(); got != "\x1b[0;91;40m" {
		t.Fatalf("ansi: %q", got)
	}
	if got := (Style{}).SGR(); got != "\x1b[0m" {
		t.Fatalf("default: %q", got)
	}
}

func TestSGRCurly(t *testing.T) {
	c, _ := Hex("#ff0000")
	got := (Style{FG: c, Underline: true, Curly: true}).SGR()
	if !strings.Contains(got, "4:3") || !strings.Contains(got, "58;2;255;0;0") {
		t.Fatalf("curly: %q", got)
	}
}

func TestSGRUnderlineFallback(t *testing.T) {
	c, _ := Hex("#ff0000")
	got := (Style{FG: c, Underline: true}).SGR()
	if strings.Contains(got, "4:3") || strings.Contains(got, "58;") || !strings.Contains(got, ";4;") {
		t.Fatalf("fallback: %q", got)
	}
}

func TestGhosttyThemeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Bluloco Dark", "Bluloco Dark"},
		{"dark:rose-pine,light:rose-pine-dawn", "rose-pine"},
		{"light:rose-pine-dawn,dark:rose-pine", "rose-pine"},
		{"light:only", "only"},
	}
	for _, c := range cases {
		if got := ghosttyThemeName(c.in); got != c.want {
			t.Errorf("ghosttyThemeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
