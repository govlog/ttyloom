package intro

import (
	"math/rand/v2"

	"github.com/govlog/ttyloom/internal/theme"
)

func init() { parts = append(parts, newFire) }

// fire : the fire of the demos, two heat rows per screen row, fed by the
// bottom of the screen and by the letters of the logo, which burn.
type fire struct {
	heat       []float32
	w, h       int
	steps      int     // simulation steps done
	last       float64 // t of the last frame: going back starts again
	rng        *rand.Rand
	sourceMask []bool
}

func newFire() part { return &fire{} }

func (*fire) Name() string { return "fire" }

const (
	fireHz     = 60  // simulation steps per second, whatever the frame rate
	fireMaxRun = 120 // steps run at most per frame: a jump in time does not stall
)

var firePalette = []theme.RGB{
	{R: 0x00, G: 0x00, B: 0x00},
	{R: 0x45, G: 0x00, B: 0x0a},
	{R: 0xd2, G: 0x32, B: 0x1e},
	{R: 0xf5, G: 0xa0, B: 0x3c},
	{R: 0xfa, G: 0xe6, B: 0x8c},
	{R: 0xff, G: 0xff, B: 0xff},
}

// fireColour : the colour of heat h (0..1).
func fireColour(h float32) theme.RGB {
	h = min(max(h, 0), 1) * float32(len(firePalette)-1)
	i := min(int(h), len(firePalette)-2)
	return mix(firePalette[i], firePalette[i+1], h-float32(i))
}

func (f *fire) Draw(l *Layer, c Canvas, t float64) {
	if c.Cols < 1 || c.Rows < 1 {
		return
	}
	g := logoFor(c)
	if f.w != c.Cols || f.h != c.Rows*2 || t < f.last {
		f.reset(c, g)
	}
	f.last = t
	n := int(t*fireHz) - f.steps
	if n > fireMaxRun {
		f.steps += n - fireMaxRun
		n = fireMaxRun
	}
	for range n {
		f.step()
	}
	for y := 0; y < c.Rows; y++ {
		for x := 0; x < c.Cols; x++ {
			h := max(f.heat[(2*y)*f.w+x], f.heat[(2*y+1)*f.w+x])
			if h < 0.06 {
				continue
			}
			cell := l.At(x, y)
			cell.Tint, cell.A = fireColour(h*0.7), min(h*1.3, 0.9)
			if h > 0.18 {
				cell.Ch = ramp[min(int(h*float32(len(ramp)-1)+0.5), len(ramp)-1)]
				cell.FG = fireColour(min(h*1.15, 1))
			}
		}
	}
	occ := drawLogo(l, c, g, t, nil)
	drawThreads(l, c, g, t, occ)
}

// reset starts the fire again for the size of c: cold, with its sources.
func (f *fire) reset(c Canvas, g logo) {
	f.w, f.h = c.Cols, c.Rows*2
	f.heat = make([]float32, f.w*f.h)
	f.steps = 0
	f.rng = rand.New(rand.NewPCG(1, 2))
	f.sourceMask = make([]bool, f.w*f.h)
	x0, y0 := g.origin(c)
	for _, lc := range g.Cells {
		x, y := x0+lc.X, y0+lc.Y
		if x >= 0 && x < f.w && y >= 0 && 2*y+1 < f.h {
			f.sourceMask[(2*y)*f.w+x] = true
		}
	}
}

// step : one step of the fire, the way of the Doom fire: each cell rises one
// row, drifts a column left or right at random and cools at random. The
// drift and the uneven cooling make the tongues.
func (f *fire) step() {
	f.steps++
	w, h := f.w, f.h
	for x := 0; x < w; x++ {
		f.heat[(h-1)*w+x] = 0.85 + 0.15*f.rng.Float32()
	}
	for i, src := range f.sourceMask {
		if src && f.rng.Float32() < 0.5 {
			f.heat[i] = max(f.heat[i], 0.35+0.35*f.rng.Float32())
		}
	}
	cool := 2.6 / float32(h) // a flame of the bottom row dies at about half the screen
	for y := 1; y < h; y++ {
		for x := 0; x < w; x++ {
			r := f.rng.IntN(4)
			dx := min(max(x-r+1, 0), w-1)
			v := f.heat[y*w+x] - float32(r&1)*cool*2
			if i := (y-1)*w + dx; !f.sourceMask[i] || v > f.heat[i] {
				f.heat[i] = max(v, 0)
			}
		}
	}
}
