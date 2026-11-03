package intro

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/theme"
)

// Size of the frames docs/screenshots/ansi2svg.py draws.
const shotCols, shotRows = 138, 34

// mocha : the colours of the screenshots (Catppuccin Mocha), the ones
// ansi2svg.py paints with.
var mocha = [16]theme.RGB{
	rgb(0x45475a), rgb(0xf38ba8), rgb(0xa6e3a1), rgb(0xf9e2af), rgb(0x89b4fa), rgb(0xf5c2e7), rgb(0x94e2d5), rgb(0xbac2de),
	rgb(0x585b70), rgb(0xf38ba8), rgb(0xa6e3a1), rgb(0xf9e2af), rgb(0x89b4fa), rgb(0xf5c2e7), rgb(0x94e2d5), rgb(0xa6adc8),
}

func rgb(v uint32) theme.RGB { return theme.RGB{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v)} }

func mochaRGB(c theme.Color, fg bool) (theme.RGB, bool) {
	switch {
	case c.Kind == 2:
		return c.RGB, true
	case c.Kind == 1 && c.Idx < 16:
		return mocha[c.Idx], true
	case fg:
		return theme.RGB{R: 0xcd, G: 0xd6, B: 0xf4}, true
	}
	return theme.RGB{R: 0x1e, G: 0x1e, B: 0x2e}, true
}

// TestShots writes frames of the demo for a look, not a check: skipped unless
// TTYLOOM_INTRO_SHOTS names the directory they go to. The chat behind is the
// frame TTYLOOM_INTRO_BG names (an .ansi of TestScreenshots), or a blank
// screen. Then, per frame:
//
//	python3 docs/screenshots/ansi2svg.py <f>.ansi <f>.svg intro && magick <f>.svg <f>.png
func TestShots(t *testing.T) {
	dir := os.Getenv("TTYLOOM_INTRO_SHOTS")
	if dir == "" {
		t.Skip("TTYLOOM_INTRO_SHOTS unset")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var bg string
	var widths []byte
	if src := os.Getenv("TTYLOOM_INTRO_BG"); src != "" {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		bg = string(b)
		widths, _ = os.ReadFile(strings.TrimSuffix(src, ".ansi") + ".widths")
	}
	write := func(name string, l *Layer, dark float32) {
		chat := Parse(bg, shotCols, shotRows)
		out := Composite(chat, l, dark, mochaRGB).Emit()
		base := filepath.Join(dir, name)
		if err := os.WriteFile(base+".ansi", []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
		if widths != nil {
			os.WriteFile(base+".widths", widths, 0o644)
		}
	}
	// The logo alone, in both fonts (the small one in a 60 × 20 corner).
	for _, sz := range [][2]int{{shotCols, shotRows}, {60, 20}} {
		c := Canvas{Cols: sz[0], Rows: sz[1], Aspect: 17.0 / 8}
		l := NewLayer(c.Cols, c.Rows)
		g := logoFor(c)
		occ := drawLogo(l, c, g, 0.2, nil)
		drawThreads(l, c, g, 0.2, occ)
		big := NewLayer(shotCols, shotRows)
		for y := 0; y < c.Rows; y++ {
			copy(big.Cells[y*shotCols:], l.Cells[y*c.Cols:(y+1)*c.Cols])
		}
		write(fmt.Sprintf("logo-%dx%d", sz[0], sz[1]), big, 1)
	}
	c := Canvas{Cols: shotCols, Rows: shotRows, Aspect: 17.0 / 8, Version: "v0.17-beta"}
	for _, mk := range parts {
		for _, ts := range []float64{0.5, 3, 7.5, 12} {
			p := mk()
			l := NewLayer(c.Cols, c.Rows)
			for step := 0.0; step < ts; step += 1.0 / 60 { // the parts with a state run up to ts
				l = NewLayer(c.Cols, c.Rows)
				p.Draw(l, c, step)
			}
			p.Draw(l, c, ts)
			write(fmt.Sprintf("%s-%04.1f", p.Name(), ts), l, 1)
		}
	}
	// Across the end of a slot: the transitions.
	s := NewShow(3, time.Unix(0, 0), "press any key", "v0.17-beta")
	for _, ts := range []float64{14.2, 14.5, 14.8, 29.5, 44.5} {
		l, dark := s.Frame(time.Unix(0, 0).Add(time.Duration(ts*float64(time.Second))), c)
		write(fmt.Sprintf("show-%04.1f", ts), l, dark)
	}
}
