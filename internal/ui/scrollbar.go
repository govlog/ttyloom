package ui

// Scrollbar: pure geometry, drawn by drawScrollbar (draw.go).
//
// Window.Scroll convention: physical lines from the BOTTOM (0 = stuck to the
// last messages, total-view = at the very top). The bar cursor follows the
// opposite visual way: top of the bar = oldest = highest Scroll.

// thumbLen : height of the cursor, in proportion to the part shown, ≥ 1.
func thumbLen(total, view int) int {
	return max(1, min(view, (view*view+total/2)/total))
}

// scrollbar gives the rank of the first line of the cursor and its height, in
// the 0..view-1 area. ok=false: everything fits on the screen, no bar.
func scrollbar(total, view, scroll int) (top, length int, ok bool) {
	if view <= 0 || total <= view {
		return 0, 0, false
	}
	maxScroll := total - view
	scroll = max(0, min(scroll, maxScroll))
	length = thumbLen(total, view)
	span := view - length       // travel of the cursor
	above := maxScroll - scroll // lines hidden above the view
	return min((above*span+maxScroll/2)/maxScroll, span), length, true
}

// scrollFromY : the reverse of scrollbar — the Scroll that brings the top of
// the cursor to line y. y=0: very top (highest Scroll); y=view-1: bottom (0).
func scrollFromY(y, view, total int) int {
	if view <= 0 || total <= view {
		return 0
	}
	maxScroll := total - view
	span := max(1, view-thumbLen(total, view))
	above := min((max(0, y)*maxScroll+span/2)/span, maxScroll)
	return maxScroll - above
}
