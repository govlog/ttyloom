package intro

import (
	"math/rand/v2"
	"time"

	"github.com/govlog/ttyloom/internal/theme"
)

// Canvas : the screen a part draws on.
type Canvas struct {
	Cols, Rows int
	Aspect     float64 // cell height / cell width
	Version    string  // build version, for the parts that write it (set by the show)
}

// part : one effect of the demo, with the logo it plays with.
type part interface {
	Name() string
	Draw(l *Layer, c Canvas, t float64) // t: seconds since the part started
}

// parts : the constructors of every part; each part file adds its own.
var parts []func() part

// Timings of the show, in seconds.
const (
	fadeIn    = 0.5
	partLen   = 15.0 // slot of one part, its transition to the next included
	transLen  = 1.0
	fadeOut   = 0.3
	hintAfter = 4.0
	hintFade  = 0.5
)

var hintFG = theme.RGB{R: 0xa6, G: 0xad, B: 0xc8}

// Show : the demo, from its fade-in to its fade-out. Its clock is the wall
// clock less the paused spans.
type Show struct {
	makers   []func() part
	start    time.Time
	paused   time.Duration
	pausedAt time.Time // zero: running
	stopping bool
	stopAt   float64 // show time of Stop
	rng      *rand.Rand
	order    []int        // part of each slot
	trans    []int        // transition kind at the end of each slot
	live     map[int]part // the parts on screen, by slot: a part keeps its state over its slot
	hint     string
	version  string
}

// NewShow starts a show at now. seed picks the order of the parts and the
// transitions; hint is the line shown after a few seconds.
func NewShow(seed uint64, now time.Time, hint, version string) *Show {
	return &Show{makers: parts, start: now, rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)),
		live: map[int]part{}, hint: hint, version: version}
}

// clock : seconds of show at now.
func (s *Show) clock(now time.Time) float64 {
	d := now.Sub(s.start) - s.paused
	if !s.pausedAt.IsZero() {
		d -= now.Sub(s.pausedAt)
	}
	return max(d.Seconds(), 0)
}

func (s *Show) Pause(now time.Time) {
	if s.pausedAt.IsZero() {
		s.pausedAt = now
	}
}

func (s *Show) Resume(now time.Time) {
	if !s.pausedAt.IsZero() {
		s.paused += now.Sub(s.pausedAt)
		s.pausedAt = time.Time{}
	}
}

// Stop starts the fade-out; once stopping, it does nothing.
func (s *Show) Stop(now time.Time) {
	if !s.stopping {
		s.stopping, s.stopAt = true, s.clock(now)
	}
}

func (s *Show) Stopping() bool { return s.stopping }

// Done : the fade-out is over.
func (s *Show) Done(now time.Time) bool {
	return s.stopping && s.clock(now)-s.stopAt >= fadeOut
}

// Current gives the name of the part on screen ("" with no part).
func (s *Show) Current(now time.Time) string {
	if len(s.makers) == 0 {
		return ""
	}
	slot := int(s.clock(now) / partLen)
	s.fill(slot)
	return s.partAt(slot).Name()
}

// fill draws the order up to slot: a shuffle bag, so every part plays before
// one comes back, and a new bag never opens on the part that closed the last.
func (s *Show) fill(slot int) {
	n := len(s.makers)
	for len(s.order) <= slot {
		bag := s.rng.Perm(n)
		if k := len(s.order); k > 0 && n > 1 && bag[0] == s.order[k-1] {
			j := 1 + s.rng.IntN(n-1)
			bag[0], bag[j] = bag[j], bag[0]
		}
		s.order = append(s.order, bag...)
		for range bag {
			s.trans = append(s.trans, s.rng.IntN(transKinds))
		}
	}
}

// partAt gives the part of slot, made at its first frame.
func (s *Show) partAt(slot int) part {
	p, ok := s.live[slot]
	if !ok {
		p = s.makers[s.order[slot]]()
		s.live[slot] = p
	}
	return p
}

// partStart : show time at which the part of slot starts. Each part after the
// first one starts with the transition that brings it in.
func partStart(slot int) float64 {
	if slot == 0 {
		return 0
	}
	return float64(slot)*partLen - transLen
}

// Frame gives the layer at now and how far the chat goes to the backdrop.
func (s *Show) Frame(now time.Time, c Canvas) (*Layer, float32) {
	t := s.clock(now)
	c.Version = s.version
	l := NewLayer(c.Cols, c.Rows)
	if len(s.makers) > 0 {
		slot := int(t / partLen)
		s.fill(slot + 1)
		for k := range s.live {
			if k < slot {
				delete(s.live, k)
			}
		}
		s.partAt(slot).Draw(l, c, t-partStart(slot))
		if end := float64(slot+1) * partLen; t >= end-transLen {
			next := NewLayer(c.Cols, c.Rows)
			s.partAt(slot+1).Draw(next, c, t-partStart(slot+1))
			transition(l, next, float32((t-(end-transLen))/transLen), s.trans[slot])
		}
	}
	if s.hint != "" && t >= hintAfter {
		s.drawHint(l, c, float32(smooth((t-hintAfter)/hintFade)))
	}
	f := float32(smooth(t / fadeIn))
	if s.stopping {
		f = min(f, float32(1-smooth((t-s.stopAt)/fadeOut)))
	}
	if f < 1 {
		fade(l, f)
	}
	return l, f
}

// drawHint writes the hint, centred on hintRow, on a
// band of backdrop that keeps it readable over the chat.
func (s *Show) drawHint(l *Layer, c Canvas, a float32) {
	text := toASCII("  " + s.hint + "  ")
	y, x0 := hintRow(c), (c.Cols-len(text))/2
	for i := 0; i < len(text); i++ {
		f := l.At(x0+i, y)
		if f == nil {
			continue
		}
		ch := text[i]
		*f = FX{Tint: Backdrop, A: 0.85 * a}
		if hash(x0+i, y) < a {
			f.Ch, f.FG = ch, hintFG
		}
	}
}

// latin1 : the letter of each rune from U+00C0 to U+00FF, accent dropped.
const latin1 = "AAAAAAACEEEEIIIIDNOOOOOxOUUUUYTsaaaaaaaceeeeiiiidnooooo/ouuuuyty"

// toASCII : s in printable ASCII, one byte a letter: the accents of the
// Latin letters folded, any other rune a '?'.
func toASCII(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 0x20 && r <= 0x7e:
			b = append(b, byte(r))
		case r >= 0xc0 && r <= 0xff:
			b = append(b, latin1[r-0xc0])
		case r == 0x152: // the oe ligature
			b = append(b, 'O', 'E')
		case r == 0x153:
			b = append(b, 'o', 'e')
		case r == 0x2019: // typographic apostrophe
			b = append(b, '\'')
		case r == 0xab || r == 0xbb: // guillemets
			b = append(b, '"')
		default:
			b = append(b, '?')
		}
	}
	return string(b)
}

// hintRow : the row of the hint, the second from the bottom: under the
// scrollers.
func hintRow(c Canvas) int { return c.Rows - 2 }

// fade scales the whole layer to f (0..1): tints by f, glyphs by a threshold
// of their own, so they come and go one by one instead of all at once.
func fade(l *Layer, f float32) {
	for y := 0; y < l.Rows; y++ {
		for x := 0; x < l.Cols; x++ {
			c := &l.Cells[y*l.Cols+x]
			c.A *= max(f, 0)
			if c.Ch != 0 && hash(x, y) >= f {
				c.Ch = 0
			}
		}
	}
}

// smooth : smoothstep of x clamped to 0..1.
func smooth(x float64) float64 {
	x = min(max(x, 0), 1)
	return x * x * (3 - 2*x)
}

// hash gives a fixed value in [0, 1) per cell: the order in which cells come
// and go in a fade or a dissolve, the same at every frame (no flicker).
func hash(x, y int) float32 {
	h := uint32(x)*0x9E3779B1 ^ uint32(y)*0x85EBCA77
	h ^= h >> 15
	h *= 0x2C1B3C6D
	h ^= h >> 12
	h *= 0x297A2D39
	h ^= h >> 15
	return float32(h>>8) / (1 << 24)
}

// Transitions from one part to the next.
const (
	transCross    = iota // both parts mixed
	transDissolve        // cell by cell
	transWipe            // a soft edge going left to right
	transKinds
)

const wipeEdge = 6.0 // columns of the soft edge of a wipe

// transition puts into a the way from a to b at w (0..1).
func transition(a, b *Layer, w float32, kind int) {
	w = float32(smooth(float64(w)))
	for y := 0; y < a.Rows; y++ {
		for x := 0; x < a.Cols; x++ {
			u := w
			switch kind {
			case transDissolve:
				u = 0
				if hash(x, y) < w {
					u = 1
				}
			case transWipe:
				edge := w*float32(a.Cols+2*wipeEdge) - wipeEdge
				u = min(max((edge-float32(x))/wipeEdge+0.5, 0), 1)
			}
			i := y*a.Cols + x
			a.Cells[i] = mixFX(a.Cells[i], b.Cells[i], u, hash(x, y))
		}
	}
}

// mixFX : the cell from a to b at u; h, the threshold of the cell, picks the
// glyph.
func mixFX(a, b FX, u, h float32) FX {
	if u <= 0 {
		return a
	}
	if u >= 1 {
		return b
	}
	out := FX{A: a.A*(1-u) + b.A*u}
	if wa, wb := a.A*(1-u), b.A*u; wa+wb > 0 {
		out.Tint = mix(a.Tint, b.Tint, wb/(wa+wb))
	}
	g := a
	if h < u {
		g = b
	}
	out.Ch, out.FG = g.Ch, g.FG
	return out
}
