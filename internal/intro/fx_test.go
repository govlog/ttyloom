package intro

import (
	"slices"
	"testing"

	"github.com/govlog/ttyloom/internal/theme"
)

// rgbKnown : every colour resolves, as with an RGB theme or a terminal that
// answered the colour queries.
func rgbKnown(c theme.Color, fg bool) (theme.RGB, bool) {
	switch c.Kind {
	case 2:
		return c.RGB, true
	case 1:
		return theme.RGB{R: 40 * c.Idx, G: 100, B: 200}, true
	}
	if fg {
		return theme.RGB{R: 0xcd, G: 0xd6, B: 0xf4}, true
	}
	return theme.RGB{R: 0x1e, G: 0x1e, B: 0x2e}, true
}

const sampleFrame = "\x1b[1;1H\x1b[0;38;2;205;214;244;48;2;30;30;46mhello 😀 world\x1b[K" +
	"\x1b[2;1H\x1b[0;1;31;44mbold\x1b[0;7mrev\x1b[K" +
	"\x1b[3;5H\x1b[0;3;4:3;38;2;1;2;3mcurly" +
	"\x1b[4;1H\x1b[0mplain"

func TestCompositeNothingIsIdentity(t *testing.T) {
	s := Parse(sampleFrame, 20, 4)
	out := Composite(s, NewLayer(20, 4), 0, rgbKnown)
	if !slices.Equal(out.Cells, s.Cells) {
		t.Fatalf("no effect, no dark: the chat must be unchanged\n%+v\n%+v", s.Cells, out.Cells)
	}
}

func TestCompositeGlyphSplitsWide(t *testing.T) {
	s := Parse("\x1b[1;1H"+theme.Style{}.SGR()+"a😀b", 4, 1)
	l := NewLayer(4, 1)
	*l.At(2, 0) = FX{Ch: '#', FG: theme.RGB{R: 255}}
	out := Composite(s, l, 0, rgbKnown)
	if out.At(1, 0).Text != " " || out.At(2, 0).Text != "#" || out.At(3, 0).Text != "b" {
		t.Fatalf("%+v", out.Cells)
	}
	if fg := out.At(2, 0).Style.FG; fg.Kind != 2 || fg.RGB != (theme.RGB{R: 255}) {
		t.Fatalf("glyph colour: %+v", fg)
	}
}

func TestCompositeReverseUsesFG(t *testing.T) {
	fg := theme.Color{Kind: 2, RGB: theme.RGB{R: 200, G: 200, B: 200}}
	s := Parse("\x1b[1;1H"+theme.Style{FG: fg, BG: theme.Color{Kind: 2}, Reverse: true}.SGR()+"x", 1, 1)
	l := NewLayer(1, 1)
	l.Cells[0] = FX{A: 0.1} // a light tint toward black
	c := Composite(s, l, 0, rgbKnown).At(0, 0)
	if c.Style.Reverse || c.Style.BG.Kind != 2 || c.Style.BG.RGB.R < 100 {
		t.Fatalf("the visible background was the light FG: %+v", c)
	}
}

// With no known background (a terminal that did not answer OSC 11), the
// backdrop comes at once, with no fade: the fills of the effects and of the
// logo show on it. The colour of the text stays the terminal's.
func TestCompositeUnknownBackgroundTakesBackdrop(t *testing.T) {
	unknown := func(theme.Color, bool) (theme.RGB, bool) { return theme.RGB{}, false }
	s := Parse("\x1b[1;1H"+theme.Style{}.SGR()+"xy", 2, 1)
	l := NewLayer(2, 1)
	l.Cells[0] = FX{Ch: ' ', Tint: theme.RGB{R: 255}, A: 1} // a letter of the logo: a solid fill
	out := Composite(s, l, 1, unknown)
	if bg := out.At(0, 0).Style.BG; bg.Kind != 2 || bg.RGB != (theme.RGB{R: 255}) {
		t.Fatalf("the fill must show: %+v", bg)
	}
	if c := out.At(1, 0); c.Style.BG.Kind != 2 || c.Style.BG.RGB != Backdrop || c.Style.FG.Kind != 0 || c.Text != "y" {
		t.Fatalf("dark = 1, background unknown: the backdrop at once, the text colour kept: %+v", c)
	}
	if out := Composite(s, NewLayer(2, 1), 0, unknown); !slices.Equal(out.Cells, s.Cells) {
		t.Fatal("nothing to do: unchanged")
	}
}

// Dark text on a light background stays readable once the background goes
// dark: it turns light instead of going dark with it.
func TestCompositeKeepsDarkTextReadable(t *testing.T) {
	lum := func(c theme.RGB) int { return (int(c.R)*299 + int(c.G)*587 + int(c.B)*114) / 1000 }
	black, white := theme.Color{Kind: 2}, theme.Color{Kind: 2, RGB: theme.RGB{R: 255, G: 255, B: 255}}
	s := Parse("\x1b[1;1H"+theme.Style{FG: black, BG: white}.SGR()+"x", 1, 1)
	c := Composite(s, NewLayer(1, 1), 1, rgbKnown).At(0, 0)
	if fg, bg := lum(c.Style.FG.RGB), lum(c.Style.BG.RGB); fg-bg < 60 {
		t.Fatalf("text %d on background %d: unreadable", fg, bg)
	}
}

// The backdrop darkens a known background; the effect then tints it.
func TestCompositeDarkensThenTints(t *testing.T) {
	s := Parse("\x1b[1;1H"+theme.Style{BG: theme.Color{Kind: 2, RGB: theme.RGB{R: 200, G: 200, B: 200}}}.SGR()+"x", 1, 1)
	dark := Composite(s, NewLayer(1, 1), 1, rgbKnown).At(0, 0).Style.BG.RGB
	if dark.R >= 100 {
		t.Fatalf("dark = 1 must darken toward the backdrop: %+v", dark)
	}
	l := NewLayer(1, 1)
	l.Cells[0] = FX{Tint: theme.RGB{G: 255}, A: 1}
	if bg := Composite(s, l, 1, rgbKnown).At(0, 0).Style.BG.RGB; bg != (theme.RGB{G: 255}) {
		t.Fatalf("A = 1 gives the tint: %+v", bg)
	}
}

// A glyph of the effect on a reverse cell whose colours are unknown keeps the
// background the cell shows (the text colour of the terminal).
func TestCompositeGlyphOnUnknownReverse(t *testing.T) {
	unknown := func(theme.Color, bool) (theme.RGB, bool) { return theme.RGB{}, false }
	s := Parse("\x1b[1;1H"+theme.Style{Reverse: true}.SGR()+"x", 1, 1)
	l := NewLayer(1, 1)
	red := theme.RGB{R: 255}
	l.Cells[0] = FX{Ch: '#', FG: red}
	c := Composite(s, l, 1, unknown).At(0, 0)
	if c.Text != "#" || !c.Style.Reverse || c.Style.FG.Kind != 0 || c.Style.BG != (theme.Color{Kind: 2, RGB: red}) {
		t.Fatalf("%+v", c)
	}
}
