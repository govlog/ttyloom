package intro

import (
	"math"
	"sort"

	"github.com/govlog/ttyloom/internal/theme"
)

func init() { parts = append(parts, newCube) }

// cube : a cube of glass turning behind the logo. Its faces are tinted, and
// hatched with '#' where the chat has no text: its text, and the faces at the
// back, show through the ones at the front, and a picture of the chat, which
// hides the tints, still shows the face. Its edges are lines of ASCII.
type cube struct{}

func newCube() part { return cube{} }

func (cube) Name() string { return "cube" }

type vec struct{ x, y, z float64 }

var (
	cubeVerts = [8]vec{{-1, -1, -1}, {1, -1, -1}, {1, 1, -1}, {-1, 1, -1}, {-1, -1, 1}, {1, -1, 1}, {1, 1, 1}, {-1, 1, 1}}
	cubeFaces = [6][4]int{{0, 1, 2, 3}, {5, 4, 7, 6}, {4, 0, 3, 7}, {1, 5, 6, 2}, {4, 5, 1, 0}, {3, 2, 6, 7}}
	cubeEdges = [12][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 0}, {4, 5}, {5, 6}, {6, 7}, {7, 4}, {0, 4}, {1, 5}, {2, 6}, {3, 7}}
	edgeFront = colTTY
	edgeBack  = theme.RGB{R: 0x58, G: 0x5b, B: 0x70}
)

// rotate turns v about x, then y, then z.
func rotate(v vec, ax, ay, az float64) vec {
	s, c := math.Sincos(ax)
	v = vec{v.x, v.y*c - v.z*s, v.y*s + v.z*c}
	s, c = math.Sincos(ay)
	v = vec{v.x*c + v.z*s, v.y, -v.x*s + v.z*c}
	s, c = math.Sincos(az)
	return vec{v.x*c - v.y*s, v.x*s + v.y*c, v.z}
}

func (cube) Draw(l *Layer, c Canvas, t float64) {
	if c.Cols < 1 || c.Rows < 1 {
		return
	}
	const dist = 4.5
	scale := math.Min(float64(c.Cols), float64(c.Rows)*c.Aspect) * 0.3 * dist // a unit: 30 % of the screen
	cx, cy := float64(c.Cols)/2, float64(c.Rows)/2
	var w [8]vec        // turned
	var p [8][2]float64 // on the screen, in cells
	for i, v := range cubeVerts {
		w[i] = rotate(v, t*0.71, t*0.43, t*0.29)
		k := scale / (w[i].z + dist)
		p[i] = [2]float64{cx + w[i].x*k, cy + w[i].y*k/c.Aspect}
	}
	// Faces, far to near, each one laid over what is under it.
	order := []int{0, 1, 2, 3, 4, 5}
	depth := func(f int) float64 {
		z := 0.0
		for _, i := range cubeFaces[f] {
			z += w[i].z
		}
		return z
	}
	sort.Slice(order, func(a, b int) bool { return depth(order[a]) > depth(order[b]) })
	light := vec{-0.4, -0.6, -0.7}
	for _, fi := range order {
		q := cubeFaces[fi]
		a, b, d := w[q[0]], w[q[1]], w[q[3]]
		n := cross(vec{b.x - a.x, b.y - a.y, b.z - a.z}, vec{d.x - a.x, d.y - a.y, d.z - a.z})
		shade := 0.35 + 0.65*math.Abs(dot(n, light))/(norm(n)*norm(light))
		col := mix(Backdrop, palette6[fi], float32(shade))
		alpha := float32(0.5)
		if n.z > 0 { // a face turned away: seen through the front ones
			alpha = 0.3
		}
		minX, maxX, minY, maxY := c.Cols, -1, c.Rows, -1
		for _, i := range q {
			minX, maxX = min(minX, int(math.Floor(p[i][0]))), max(maxX, int(math.Ceil(p[i][0])))
			minY, maxY = min(minY, int(math.Floor(p[i][1]))), max(maxY, int(math.Ceil(p[i][1])))
		}
		for y := max(minY, 0); y <= min(maxY, c.Rows-1); y++ {
			for x := max(minX, 0); x <= min(maxX, c.Cols-1); x++ {
				if !inQuad(float64(x)+0.5, float64(y)+0.5, p[q[0]], p[q[1]], p[q[2]], p[q[3]]) {
					continue
				}
				f := l.At(x, y)
				if f.A == 0 {
					f.Tint = Backdrop
				}
				f.Tint = mix(f.Tint, col, alpha/max(f.A+alpha*(1-f.A), 0.01))
				f.A += alpha * (1 - f.A)
				f.Fill, f.FG = '#', col // far to near: the front face gives its own
			}
		}
	}
	// Edges: the ones at the back first, dim.
	edges := cubeEdges
	sort.Slice(edges[:], func(a, b int) bool {
		e, f := edges[a], edges[b]
		return w[e[0]].z+w[e[1]].z > w[f[0]].z+w[f[1]].z
	})
	for _, e := range edges {
		col := edgeFront
		if w[e[0]].z+w[e[1]].z > 0.3 {
			col = edgeBack
		}
		line(l, p[e[0]], p[e[1]], c.Aspect, col)
	}
	for i := range p {
		if f := l.At(int(p[i][0]), int(p[i][1])); f != nil {
			f.Ch, f.FG = '+', edgeFront
		}
	}
	g := logoFor(c)
	occ := drawLogo(l, c, g, t, func(lc logoCell) (int, int, bool) {
		return 0, int(math.Round(1.5 * math.Sin(t*3+float64(lc.Letter)*0.7))), true
	})
	drawThreads(l, c, g, t, occ)
}

// line draws a line of glyphs from a to b, the glyph by its slope on the
// screen (cells are taller than wide).
func line(l *Layer, a, b [2]float64, aspect float64, col theme.RGB) {
	dx, dy := b[0]-a[0], b[1]-a[1]
	ch := byte('-')
	switch ang := math.Abs(math.Atan2(dy*aspect, dx)); {
	case ang > math.Pi*3/8 && ang < math.Pi*5/8:
		ch = '|'
	case ang >= math.Pi/8 && ang <= math.Pi*3/8:
		ch = '\\'
		if dy*dx < 0 {
			ch = '/'
		}
	case ang >= math.Pi*5/8 && ang <= math.Pi*7/8:
		ch = '/'
		if dy*dx < 0 {
			ch = '\\'
		}
	}
	n := int(math.Max(math.Abs(dx), math.Abs(dy))) + 1
	for i := 0; i <= n; i++ {
		x := a[0] + dx*float64(i)/float64(n)
		y := a[1] + dy*float64(i)/float64(n)
		if f := l.At(int(math.Floor(x)), int(math.Floor(y))); f != nil {
			f.Ch, f.FG = ch, col
		}
	}
}

// inQuad : the point x, y is inside the convex quad a b c d (either winding).
func inQuad(x, y float64, a, b, c, d [2]float64) bool {
	side := func(p, q [2]float64) float64 { return (q[0]-p[0])*(y-p[1]) - (q[1]-p[1])*(x-p[0]) }
	s1, s2, s3, s4 := side(a, b), side(b, c), side(c, d), side(d, a)
	return (s1 >= 0 && s2 >= 0 && s3 >= 0 && s4 >= 0) || (s1 <= 0 && s2 <= 0 && s3 <= 0 && s4 <= 0)
}

func cross(a, b vec) vec   { return vec{a.y*b.z - a.z*b.y, a.z*b.x - a.x*b.z, a.x*b.y - a.y*b.x} }
func dot(a, b vec) float64 { return a.x*b.x + a.y*b.y + a.z*b.z }
func norm(a vec) float64   { return math.Sqrt(dot(a, a)) }
