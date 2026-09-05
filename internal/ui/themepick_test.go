package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

var themeNames = []string{"terminal", "Catppuccin Mocha", "Gruvbox Dark", "Solarized Light"}

// fakeLoad : fake themes, no file to read.
func fakeLoad(name string) (theme.Theme, error) {
	if name == "Gruvbox Dark" {
		return theme.Theme{}, fmt.Errorf("theme not found: %s", name)
	}
	t := theme.Terminal()
	t.Name = name
	return t, nil
}

// Navigation bounded, filter deaf to case and accents, index of the current
// theme at opening time.
func TestThemePickerNav(t *testing.T) {
	p := newThemePicker(themeNames, "gruvbox dark", theme.Terminal(), 80, 24)
	if p.cur != 2 { // the current one is preselected, case ignored
		t.Fatalf("current: %d", p.cur)
	}
	if newThemePicker(themeNames, "absent", theme.Terminal(), 80, 24).cur != 0 {
		t.Fatal("unknown theme: selection should fall back to 0")
	}
	p.move(-10)
	if p.cur != 0 {
		t.Fatalf("lower bound: %d", p.cur)
	}
	p.move(100)
	if p.cur != len(themeNames)-1 || p.name() != "Solarized Light" {
		t.Fatalf("upper bound: %d %q", p.cur, p.name())
	}
	// The filter keeps the selected theme while it survives.
	p.query = []rune("Sol")
	p.filter()
	if len(p.names) != 1 || p.name() != "Solarized Light" {
		t.Fatalf("filter \"Sol\": %v, cur=%d", p.names, p.cur)
	}
	p.query = []rune("gruv")
	p.filter()
	if p.name() != "Gruvbox Dark" || p.cur != 0 {
		t.Fatalf("filter \"gruv\": %v, cur=%d", p.names, p.cur)
	}
	if got := themeFilter(themeNames, "MOCHA"); len(got) != 1 || got[0] != "Catppuccin Mocha" {
		t.Fatalf("case ignored: %v", got)
	}
	if got := themeFilter(themeNames, "zzz"); len(got) != 0 {
		t.Fatalf("no match: %v", got)
	}
	if got := themeFilter(themeNames, ""); len(got) != len(themeNames) {
		t.Fatalf("empty filter: %v", got)
	}
	// The box fits on the screen, header and foot included.
	if small := newThemePicker(themeNames, "", theme.Terminal(), 80, 10); small.height() > 10 {
		t.Fatalf("box too tall: %d", small.height())
	}
}

// Esc gives the first theme back with nothing saved; each move applies it.
func TestThemePickerCancelRestores(t *testing.T) {
	orig := theme.Terminal()
	orig.Name = "terminal"
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{},
		t: &term.Term{Cols: 80, Rows: 24}, th: orig}
	u.themePick = newThemePicker(themeNames, orig.Name, orig, u.t.Cols, u.t.Rows)
	u.themePick.load = fakeLoad
	u.themeKey(term.Key{Code: term.Down})
	if u.th.Name != "Catppuccin Mocha" {
		t.Fatalf("preview: %q", u.th.Name)
	}
	// Filter: the selection moves, the theme follows.
	for _, r := range "sol" {
		u.themeKey(term.Key{Rune: r})
	}
	if u.th.Name != "Solarized Light" {
		t.Fatalf("preview after filter: %q", u.th.Name)
	}
	// Load failed: the theme shown does not move.
	u.themeKey(term.Key{Code: term.Backspace})
	u.themeKey(term.Key{Code: term.Backspace})
	u.themeKey(term.Key{Code: term.Backspace})
	u.themePick.cur = 2
	u.themeApply()
	if u.th.Name != "Solarized Light" {
		t.Fatalf("load error: %q", u.th.Name)
	}
	u.themeKey(term.Key{Code: term.Esc})
	if u.themePick != nil {
		t.Fatal("picker still open")
	}
	if u.th.Name != orig.Name || u.cfg.Theme != "" {
		t.Fatalf("cancel: theme %q, cfg %q", u.th.Name, u.cfg.Theme)
	}
}

// themePicker.Lines : header, p.rows names, foot, the current name inverted.
func TestThemePickerLines(t *testing.T) {
	p := newThemePicker(themeNames, "Gruvbox Dark", theme.Terminal(), 80, 24)
	lines := p.Lines(theme.Terminal())
	if len(lines) != p.height() {
		t.Fatalf("%d lines, %d expected", len(lines), p.height())
	}
	checkBox(t, "themepick", lines, p.w)
	cur := 2 + p.cur - p.top()
	for i := 0; i < p.rows; i++ {
		if got := lines[2+i].Spans[1].Style.Reverse; got != (2+i == cur) {
			t.Errorf("line %d: reversed = %v", i, got)
		}
	}
	if got := lines[cur].Spans[1].Text; !strings.HasPrefix(got, "Gruvbox Dark") {
		t.Errorf("current name: %q", got)
	}
	// Empty filter lines: no name, no inversion, and the width holds.
	p.query = []rune("zzz")
	p.filter()
	checkBox(t, "themepick empty", p.Lines(theme.Terminal()), p.w)
}
