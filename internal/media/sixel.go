package media

import (
	"image"
	"image/draw"
	"math"
	"slices"
	"strconv"
)

const (
	// sixelColours : the registers one image may use.
	// ponytail: assumes the 256 registers most sixel terminals have; a real
	// VT340 has 16. Read the count with XTSMGRAPHICS (CSI ? 1 ; 1 S) and pass
	// it in if a smaller terminal ever matters.
	sixelColours = 256
	// sixelOpaque : the alpha from which a pixel is painted. No blend with
	// the terminal background, which is not known: a soft edge shows hard.
	sixelOpaque = 128
	sixelNone   = 0xffff // a pixel never painted, in the plane of indexes
)

// Sixel encodes img as a sixel sequence, DCS to ST, ready to write at the
// cursor. P2=1: a pixel never painted keeps what the screen had under it, so
// a transparent pixel is simply left out. The raster attributes give the
// size at 1:1, the registers of the colours in use follow (0..100 a
// channel), then the bands of 6 rows.
func Sixel(img image.Image) string {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	pix := sixelPixels(img)
	idx := make([]uint16, w*h)
	pal, ok := sixelExact(pix, idx)
	if !ok {
		pal = sixelQuantise(pix)
		sixelDither(pix, w, h, pal, idx)
	}
	return sixelEncode(w, h, pal, idx)
}

// sixelPixels gives the pixels of img row after row as straight RGBA: a soft
// edge keeps its colour instead of the darker premultiplied one.
func sixelPixels(img image.Image) []uint8 {
	b := img.Bounds()
	switch p := img.(type) {
	case *image.NRGBA:
		if p.Stride == 4*b.Dx() {
			return p.Pix[p.PixOffset(b.Min.X, b.Min.Y):][:4*b.Dx()*b.Dy()]
		}
	case *image.RGBA: // what the scaler gives; premultiplied is straight when opaque
		if p.Stride == 4*b.Dx() && p.Opaque() {
			return p.Pix[p.PixOffset(b.Min.X, b.Min.Y):][:4*b.Dx()*b.Dy()]
		}
	}
	p := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(p, p.Rect, img, b.Min, draw.Src)
	return p.Pix
}

// sixelExact indexes pix on its own colours when 256 opaque ones or fewer
// make it: a screenshot or a flat drawing keeps them exactly, with no
// dithering noise. false as soon as a 257th shows.
func sixelExact(pix []uint8, idx []uint16) ([][3]uint8, bool) {
	var pal [][3]uint8
	seen := make(map[uint32]uint16, sixelColours)
	last, cur := uint32(1<<24), uint16(0) // no colour yet; runs skip the map
	for i := range idx {
		p := pix[4*i : 4*i+4 : 4*i+4]
		if p[3] < sixelOpaque {
			idx[i] = sixelNone
			continue
		}
		if k := uint32(p[0])<<16 | uint32(p[1])<<8 | uint32(p[2]); k != last {
			j, ok := seen[k]
			if !ok {
				if len(pal) == sixelColours {
					return nil, false
				}
				j = uint16(len(pal))
				seen[k] = j
				pal = append(pal, [3]uint8{p[0], p[1], p[2]})
			}
			last, cur = k, j
		}
		idx[i] = cur
	}
	return pal, true
}

// sixelMoments : what a set of pixels weighs in a cut. q, the sum over its
// cells of |sum|²/count, gives the squared error around the mean as
// q - |s|²/n without a pass over the pixels.
type sixelMoments struct {
	n, q float64
	s    [3]float64 // sum of each channel
}

func (m *sixelMoments) add(o *sixelMoments, sign float64) {
	m.n += sign * o.n
	m.q += sign * o.q
	for c := range m.s {
		m.s[c] += sign * o.s[c]
	}
}

// err leaves out the spread inside a cell of the grid (8 levels wide).
func (m *sixelMoments) err() float64 { return m.q - sixelSq(m.s)/m.n }

func sixelSq(v [3]float64) float64 { return v[0]*v[0] + v[1]*v[1] + v[2]*v[2] }

// sixelCell : the pixels of one cell of the 32x32x32 colour grid.
type sixelCell struct {
	sixelMoments
	k [3]uint8 // place in the grid
}

// sixelBox : cells that end as one colour of the palette.
type sixelBox struct {
	sixelMoments
	cells []sixelCell
}

// sixelQuantise gives a palette of 256 colours at most fitted to pix, sorted
// by green (sixelNearest needs it). The colours go in a 32-level grid; the
// box of cells with the largest squared error is cut in two, on the axis
// and at the plane that lower it most (the rule of Wu's quantiser, better
// than a cut at the median for the same work), until there are 256 boxes.
// A colour is the mean of its box, at full precision. A 64-level grid gave
// the same error on photos and gradients for twice the time.
func sixelQuantise(pix []uint8) [][3]uint8 {
	hist := make([][4]uint64, 1<<15) // count and sum of each channel, per cell
	for i := 0; i < len(pix); i += 4 {
		p := pix[i : i+4 : i+4]
		if p[3] < sixelOpaque {
			continue
		}
		c := &hist[int(p[0]>>3)<<10|int(p[1]>>3)<<5|int(p[2]>>3)]
		c[0]++
		c[1] += uint64(p[0])
		c[2] += uint64(p[1])
		c[3] += uint64(p[2])
	}
	var all sixelBox
	for k, c := range hist {
		if c[0] == 0 {
			continue
		}
		cell := sixelCell{k: [3]uint8{uint8(k >> 10), uint8(k >> 5 & 31), uint8(k & 31)}}
		cell.n, cell.s = float64(c[0]), [3]float64{float64(c[1]), float64(c[2]), float64(c[3])}
		cell.q = sixelSq(cell.s) / cell.n
		all.add(&cell.sixelMoments, 1)
		all.cells = append(all.cells, cell)
	}
	boxes := []sixelBox{all}
	for len(boxes) < sixelColours {
		worst, e := -1, 0.0
		for i := range boxes {
			if v := boxes[i].err(); v > e { // a box of one cell has none
				worst, e = i, v
			}
		}
		if worst < 0 {
			break
		}
		lo, hi, ok := boxes[worst].split()
		if !ok {
			break
		}
		boxes[worst] = lo
		boxes = append(boxes, hi)
	}
	pal := make([][3]uint8, len(boxes))
	for i, b := range boxes {
		for c := range 3 {
			pal[i][c] = sixelSnap(b.s[c] / b.n)
		}
	}
	slices.SortFunc(pal, func(a, b [3]uint8) int { return int(a[1]) - int(b[1]) })
	return pal
}

// split cuts b in two along one axis of the grid, at the plane that leaves
// the least squared error on both sides. q is the same for every cut, so the
// best one has the largest |s|²/n summed over the two halves.
func (b *sixelBox) split() (lo, hi sixelBox, ok bool) {
	var planes [3][32]sixelMoments
	for i := range b.cells {
		c := &b.cells[i]
		for a := range 3 {
			planes[a][c.k[a]].add(&c.sixelMoments, 1)
		}
	}
	best, axis, cut := -1.0, -1, 0
	for a := range 3 {
		var l sixelMoments
		for k := range 31 {
			l.add(&planes[a][k], 1)
			if l.n == 0 {
				continue
			}
			if l.n == b.n {
				break
			}
			r := b.sixelMoments
			r.add(&l, -1)
			if v := sixelSq(l.s)/l.n + sixelSq(r.s)/r.n; v > best {
				best, axis, cut, lo.sixelMoments = v, a, k, l
			}
		}
	}
	if axis < 0 {
		return lo, hi, false
	}
	n := 0
	for i := range b.cells {
		if int(b.cells[i].k[axis]) <= cut {
			b.cells[i], b.cells[n] = b.cells[n], b.cells[i]
			n++
		}
	}
	lo.cells = b.cells[:n]
	hi = sixelBox{b.sixelMoments, b.cells[n:]}
	hi.add(&lo.sixelMoments, -1)
	return lo, hi, true
}

// sixelSnap rounds a channel to the nearest value a register holds (0..100
// on the wire): the dithering then works against the colour the terminal
// shows.
func sixelSnap(v float64) uint8 {
	return uint8(math.Round(math.Round(v*100/255) * 255 / 100))
}

// sixelDither maps pix on pal with Floyd–Steinberg error diffusion.
// image/draw.FloydSteinberg scans the whole palette at every pixel: 215 ms
// at 800x600 for 256 colours. Here the nearest colour is looked up once per
// cell of a 32-level grid and kept.
// ponytail: the colour kept is the nearest to the centre of the cell, up to
// ~14 levels off the true nearest in a corner, and the diffusion carries
// that error on. An exact search per pixel lowers the squared error by 5 to
// 10 % on photos for 2 to 4 times the time, if a flat area ever needs it.
func sixelDither(pix []uint8, w, h int, pal [][3]uint8, idx []uint16) {
	p := make([][3]int32, len(pal))
	for i, c := range pal {
		p[i] = [3]int32{int32(c[0]), int32(c[1]), int32(c[2])}
	}
	near := make([]uint16, 1<<15) // 1 + index in pal, 0 for not looked up
	// Errors in 1/16 of a level, 3 a pixel, for the current and the next
	// row; one pixel of margin on each side spares the tests at the edges.
	cur, next := make([]int32, 3*(w+2)), make([]int32, 3*(w+2))
	last := 0 // the colour of the pixel before, a good seed for the search
	for y := range h {
		row, out := pix[4*y*w:][:4*w], idx[y*w:][:w]
		for x := range out {
			s := row[4*x : 4*x+4 : 4*x+4]
			if s[3] < sixelOpaque {
				out[x] = sixelNone
				continue
			}
			e := cur[3*x+3 : 3*x+9 : 3*x+9] // this pixel, then the one on its right
			r := min(max(int32(s[0])+(e[0]+8)>>4, 0), 255)
			g := min(max(int32(s[1])+(e[1]+8)>>4, 0), 255)
			b := min(max(int32(s[2])+(e[2]+8)>>4, 0), 255)
			k := r>>3<<10 | g>>3<<5 | b>>3
			j := near[k]
			if j == 0 {
				j = uint16(sixelNearest(p, r&^7|4, g&^7|4, b&^7|4, last)) + 1
				near[k] = j
			}
			last = int(j - 1)
			out[x] = j - 1
			c := &p[last]
			dr, dg, db := r-c[0], g-c[1], b-c[2]
			e[3] += 7 * dr
			e[4] += 7 * dg
			e[5] += 7 * db
			n := next[3*x : 3*x+9 : 3*x+9] // below left, below, below right
			n[0] += 3 * dr
			n[1] += 3 * dg
			n[2] += 3 * db
			n[3] += 5 * dr
			n[4] += 5 * dg
			n[5] += 5 * db
			n[6] += dr
			n[7] += dg
			n[8] += db
		}
		cur, next = next, cur
		clear(next)
	}
}

// sixelNearest gives the index of the colour of pal (sorted by green)
// closest to r, g, b. The search walks out from g both ways and stops on
// each side once the gap in green alone exceeds the best distance; seed, a
// colour likely near, gives that bound from the start.
func sixelNearest(pal [][3]int32, r, g, b int32, seed int) int {
	lo, hi := 0, len(pal)
	for lo < hi { // the first colour with green >= g
		if m := (lo + hi) / 2; pal[m][1] < g {
			lo = m + 1
		} else {
			hi = m
		}
	}
	lo--
	best, bestD := 0, int32(math.MaxInt32)
	try := func(i int) {
		dr, dg, db := pal[i][0]-r, pal[i][1]-g, pal[i][2]-b
		if d := dr*dr + dg*dg + db*db; d < bestD {
			best, bestD = i, d
		}
	}
	try(seed)
	for lo >= 0 || hi < len(pal) {
		if hi < len(pal) {
			if d := pal[hi][1] - g; d*d >= bestD {
				hi = len(pal)
			} else {
				try(hi)
				hi++
			}
		}
		if lo >= 0 {
			if d := g - pal[lo][1]; d*d >= bestD {
				lo = -1
			} else {
				try(lo)
				lo--
			}
		}
	}
	return best
}

// sixelEncode writes the sequence of the w x h plane idx coloured by pal.
// One pass over the pixels of a band, column after column, gives each colour
// its row of sixels and the columns it touches, in order: writing a colour
// then walks those columns, never the whole width.
func sixelEncode(w, h int, pal [][3]uint8, idx []uint16) string {
	out := make([]byte, 0, 64+w*h) // a dithered photo takes up to a byte a pixel
	out = append(out, "\x1bP0;1;0q\"1;1;"...)
	out = strconv.AppendInt(out, int64(w), 10)
	out = append(out, ';')
	out = strconv.AppendInt(out, int64(h), 10)
	// Registers for the colours in use only, numbered from 0.
	reg := make([]int, len(pal))
	for i := range reg {
		reg[i] = -1
	}
	for _, c := range idx {
		if c != sixelNone {
			reg[c] = 0
		}
	}
	n := 0
	for i, c := range pal {
		if reg[i] < 0 {
			continue
		}
		reg[i] = n
		out = append(out, '#')
		out = strconv.AppendInt(out, int64(n), 10)
		out = append(out, ";2"...)
		for _, v := range c {
			out = append(out, ';')
			out = strconv.AppendInt(out, (int64(v)*100+127)/255, 10)
		}
		n++
	}
	rows := make([]byte, len(pal)*w) // sixels of each colour over the band
	cols := make([][]int, len(pal))  // columns each colour touches, in order
	var band []uint16                // colours of the band, as they show
	for y0 := 0; y0 < h; y0 += 6 {
		if y0 > 0 {
			out = append(out, '-')
		}
		band = band[:0]
		for x := range w {
			for dy := range min(6, h-y0) {
				c := idx[(y0+dy)*w+x]
				if c == sixelNone {
					continue
				}
				at := int(c)*w + x
				if rows[at] == 0 {
					if len(cols[c]) == 0 {
						band = append(band, c)
					}
					cols[c] = append(cols[c], x)
				}
				rows[at] |= 1 << dy
			}
		}
		for i, c := range band {
			if i > 0 {
				out = append(out, '$')
			}
			out = append(out, '#')
			out = strconv.AppendInt(out, int64(reg[c]), 10)
			row, xs := rows[int(c)*w:][:w], cols[c]
			at := 0 // column under the sixel cursor
			for j := 0; j < len(xs); {
				x, v := xs[j], row[xs[j]]
				k := j + 1
				for k < len(xs) && xs[k] == x+k-j && row[xs[k]] == v {
					k++
				}
				out = sixelRun(out, '?', x-at)
				out = sixelRun(out, 63+v, k-j)
				clear(row[x : x+k-j]) // ready for the next band
				at, j = x+k-j, k
			}
			cols[c] = xs[:0] // the empty sixels after the last column are left out
		}
	}
	return string(append(out, "\x1b\\"...))
}

// sixelRun appends n times the sixel ch, as !n<ch> from 4 on, where it is
// shorter.
func sixelRun(out []byte, ch byte, n int) []byte {
	if n >= 4 {
		out = append(out, '!')
		out = strconv.AppendInt(out, int64(n), 10)
		return append(out, ch)
	}
	for range n {
		out = append(out, ch)
	}
	return out
}
