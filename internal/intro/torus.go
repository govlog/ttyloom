package intro

import (
	"math"

	"github.com/govlog/ttyloom/internal/theme"
)

func init() { parts = append(parts, newTorus) }

// torus : the donut of donut.c, lit and z-buffered, that changes its shape:
// its tube breathes, it shrinks to almost a ball, and its tube twists.
type torus struct {
	depth []float64 // 1/z per cell, reused from frame to frame
}

func newTorus() part { return &torus{} }

func (*torus) Name() string { return "torus" }

// lumRamp : the luminance ramp of donut.c, dark to bright.
const lumRamp = ".,-~:;=!*#$@"

func (r *torus) Draw(l *Layer, c Canvas, t float64) {
	if c.Cols < 1 || c.Rows < 1 {
		return
	}
	cycle := 2 * math.Pi * t / partLen
	r1 := 1 + 0.3*math.Sin(cycle*2)                // the tube breathes
	r2 := 2.2 - 0.95*(0.5-0.5*math.Cos(cycle))     // down to almost a ball, then back
	twist := 0.7 * (0.5 - 0.5*math.Cos(cycle*1.5)) // the tube twists, then untwists
	const k2 = 6.0
	k1 := math.Min(float64(c.Cols), float64(c.Rows)*c.Aspect) * k2 * 0.36 / (r1 + r2)
	cx, cy := float64(c.Cols)/2, float64(c.Rows)/2
	if len(r.depth) != len(l.Cells) {
		r.depth = make([]float64, len(l.Cells))
	}
	clear(r.depth)
	sa, ca := math.Sincos(t * 0.9)
	sb, cb := math.Sincos(t * 0.53)
	// About one sample per cell on the screen: steps from the size the
	// torus takes there.
	dTheta := math.Min(0.9/(k1/k2*r1), 0.3)
	dPhi := math.Min(0.9/(k1/k2*(r1+r2)), 0.3)
	for phi := 0.0; phi < 2*math.Pi; phi += dPhi {
		sp, cp := math.Sincos(phi)
		tw := twist * math.Sin(3*phi+t*1.3)
		for theta := 0.0; theta < 2*math.Pi; theta += dTheta {
			st, ct := math.Sincos(theta + tw)
			// The circle of the tube, turned about y by phi.
			ox, oy := r2+r1*ct, r1*st
			x := ox*(cb*cp+sa*sb*sp) - oy*ca*sb
			y := ox*(sb*cp-sa*cb*sp) + oy*ca*cb
			z := k2 + ca*ox*sp + oy*sa
			ooz := 1 / z
			sx := int(cx + k1*ooz*x)
			sy := int(cy - k1*ooz*y/c.Aspect)
			if sx < 0 || sx >= c.Cols || sy < 0 || sy >= c.Rows {
				continue
			}
			i := sy*c.Cols + sx
			if ooz <= r.depth[i] {
				continue
			}
			r.depth[i] = ooz
			// Light from above and behind the viewer, as in donut.c.
			lum := cp*ct*sb - ca*ct*sp - sa*st + cb*(ca*st-ct*sa*sp)
			f := &l.Cells[i]
			if lum <= 0 {
				f.Ch, f.FG, f.Tint, f.A = '.', theme.RGB{R: 0x45, G: 0x47, B: 0x5a}, Backdrop, 0.6
				continue
			}
			v := float32(math.Min(lum/math.Sqrt2, 1))
			col := mix(colBlue, colPink, v)
			f.Ch = lumRamp[min(int(v*float32(len(lumRamp)-1)+0.5), len(lumRamp)-1)]
			f.FG = mix(col, white, 0.35*v*v)
			f.Tint, f.A = mix(Backdrop, col, 0.35*v), 0.7
		}
	}
	g := logoFor(c)
	occ := drawLogo(l, c, g, t, func(lc logoCell) (int, int, bool) {
		return 0, -int(math.Round(2 * math.Abs(math.Sin(t*2.6+float64(lc.Letter)*0.45)))), true
	})
	drawThreads(l, c, g, t, occ)
}
