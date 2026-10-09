package ui

import (
	"slices"
	"strconv"
	"strings"
)

// Row diff. draw writes every piece of a frame right after a cursor move
// (\x1b[r;cH) and every piece sets its own colours, so the frame splits into
// screen rows that stand alone. Only the rows that changed since the last
// frame are sent: typing a letter sends the input line, not the screen.
// Before this, a frame that differed by one byte went out whole — with
// pictures drawn in the cells (sixel, Terminology), every one of them at each
// key.

// cellPic : a picture drawn in the cells (sixel, Terminology): text written
// over its rows erases it, so it goes again with them.
type cellPic struct {
	row, rows int    // screen rows it covers, 0-based
	seq       string // what draws it, cursor move included
}

// frame : what draw made, by layer.
type frame struct {
	text  string // pieces of text, each one after a cursor move
	gfx   string // kitty commands: sent whenever there are some (placeDiff already keeps them to what changed)
	pics  []cellPic
	trail string // cursor at its place, shown or not
}

// paint sends to the terminal what f changes on the screen.
func (u *UI) paint(f frame) {
	whole := u.prevRows == nil // after repaint: the frame goes as draw wrote it
	rows := splitRows(f.text)
	dirty := map[int]bool{}
	for r, s := range rows {
		if prev, ok := u.prevRows[r]; !ok || prev != s {
			dirty[r] = true
		}
	}
	// A picture gone or moved leaves its pixels: the rows it covered are
	// written again, which erases them.
	for _, p := range u.prevPics {
		if !slices.Contains(f.pics, p) {
			for r := p.row; r < p.row+p.rows; r++ {
				dirty[r] = true
			}
		}
	}
	var pics []string
	for _, p := range f.pics {
		hit := !slices.Contains(u.prevPics, p)
		for r := p.row; r < p.row+p.rows && !hit; r++ {
			hit = dirty[r]
		}
		if hit {
			pics = append(pics, p.seq)
		}
	}
	u.prevRows, u.prevPics = rows, f.pics
	if len(dirty) == 0 && f.gfx == "" && len(pics) == 0 && f.trail == u.prevTrail {
		u.t.Flush() // what other code queued still goes
		return
	}
	u.prevTrail = f.trail
	u.t.WriteString("\x1b[?25l") // hidden while the rows are written
	if whole {
		u.t.WriteString(f.text)
	} else {
		order := make([]int, 0, len(dirty))
		for r := range dirty {
			order = append(order, r)
		}
		slices.Sort(order)
		for _, r := range order {
			u.t.WriteString(rows[r])
		}
	}
	u.t.WriteString(f.gfx)
	for _, s := range pics {
		u.t.WriteString(s)
	}
	u.t.WriteString(f.trail)
	u.t.Flush()
}

// splitRows groups the pieces of s by the screen row of the cursor move
// that opens each one (0-based), in their order. Text before the first move
// goes under row -1.
func splitRows(s string) map[int]string {
	rows := map[int]string{}
	row, start := -1, 0
	for i := 0; i < len(s); {
		j := strings.Index(s[i:], "\x1b[")
		if j < 0 {
			break
		}
		i += j
		if r, n := cup(s[i:]); n > 0 {
			if i > start {
				rows[row] += s[start:i]
			}
			row, start = r, i
			i += n
			continue
		}
		i += 2
	}
	if start < len(s) {
		rows[row] += s[start:]
	}
	return rows
}

// cup reads a cursor move \x1b[<row>;<col>H at the start of s: the row,
// 0-based, and the length of the sequence; n = 0 when s opens with
// something else.
func cup(s string) (row, n int) {
	i := 2
	digits := func() int {
		k := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		return i - k
	}
	d := digits()
	if d == 0 || i >= len(s) || s[i] != ';' {
		return 0, 0
	}
	r, _ := strconv.Atoi(s[2 : 2+d])
	i++
	if digits() == 0 || i >= len(s) || s[i] != 'H' {
		return 0, 0
	}
	return r - 1, i + 1
}
