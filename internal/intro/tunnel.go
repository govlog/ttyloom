package intro

import (
	"math"

	"github.com/govlog/ttyloom/internal/theme"
)

func init() { parts = append(parts, newTunnel) }

// tunnel : a flight down a tunnel lined with the three threads of the logo,
// braided; the far end wanders on a Lissajous path. The logo comes out of
// the far end.
type tunnel struct{}

func newTunnel() part { return tunnel{} }

func (tunnel) Name() string { return "tunnel" }

// ramp : glyphs from light to dense.
const ramp = " .:-=+*#%@"

var (
	tileDark  = theme.RGB{R: 0x1e, G: 0x1e, B: 0x2e}
	tileLight = theme.RGB{R: 0x45, G: 0x47, B: 0x5a}
)

func (tunnel) Draw(l *Layer, c Canvas, t float64) {
	if c.Cols < 1 || c.Rows < 1 {
		return
	}
	cx := float64(c.Cols)/2 + float64(c.Cols)*0.16*math.Sin(t*2*math.Pi/7)
	cy := float64(c.Rows)/2 + float64(c.Rows)*0.14*math.Sin(t*2*math.Pi/11)
	scale := math.Max(math.Min(float64(c.Cols), float64(c.Rows)*c.Aspect)/2, 1)
	for y := 0; y < c.Rows; y++ {
		dy := (float64(y) - cy) * c.Aspect
		for x := 0; x < c.Cols; x++ {
			dx := float64(x) - cx
			dist := math.Hypot(dx, dy) / scale
			if dist < 0.02 {
				continue
			}
			depth := 0.35/dist + t*1.1
			ang := math.Atan2(dy, dx)/(2*math.Pi) + 0.5 + t*0.04
			fog := math.Min(dist*1.5, 1) // the far end is dark
			// The walls: a checker of tiles, the texture of the demo tunnels.
			tile := (int(math.Floor(ang*16))+int(math.Floor(depth*3)))%2 == 0
			col, b := tileDark, 0.25
			if tile {
				col, b = tileLight, 0.45
			}
			// Over it, the three threads, braided along the depth.
			for k := range 3 {
				centre := float64(k)/3 + 0.09*math.Sin(2*math.Pi*depth*0.5+float64(k)*2*math.Pi/3)
				d := math.Abs(math.Mod(ang-centre+1.5, 1) - 0.5)
				if w := 1 - d/0.045; w > 0 {
					col, b = mix(col, threadCol[k], float32(math.Min(w*1.6, 1))), math.Max(b, 0.55+0.45*w)
				}
			}
			bb := float32(b * fog)
			f := l.At(x, y)
			f.Tint, f.A = mix(Backdrop, col, 0.35+0.65*float32(fog)), 0.75*float32(fog)
			if bb > 0.08 {
				f.Ch = ramp[min(int(bb*float32(len(ramp)-1)+0.5), len(ramp)-1)]
				f.FG = mix(col, white, 0.3*bb)
			}
		}
	}
	g := logoFor(c)
	k := smooth(t / 2) // out of the far end, over 2 s
	if k < 0.05 {
		return
	}
	at := func(lc logoCell) (int, int, bool) {
		dx := int(math.Round((float64(lc.X) - float64(g.W)/2) * (k - 1)))
		dy := int(math.Round((float64(lc.Y) - float64(g.H)/2) * (k - 1)))
		return dx, dy, true
	}
	occ := drawLogo(l, c, g, t, at)
	if k >= 1 {
		drawThreads(l, c, g, t, occ)
	}
}
