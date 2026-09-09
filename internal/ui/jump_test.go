package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/term"
)

// Scrolled up, a pill at the bottom right of the message area offers the
// way back; a click on it goes to the last line. Nothing at the bottom.
func TestJumpLastPill(t *testing.T) {
	u := hoverUI() // x0 = 27, message area 27..78, bar 79, 8 message lines
	w := u.view()
	for i := 0; i < 40; i++ {
		w.AddSys("line")
	}
	if _, ok := u.jumpRect(); ok {
		t.Fatal("pill shown at the bottom")
	}
	w.Scroll = 10
	r, ok := u.jumpRect()
	if !ok || r.row != u.viewRows()-1 || r.col+r.w != 79 || r.h != 1 {
		t.Fatalf("pill rect: %+v %v", r, ok)
	}
	var out bytes.Buffer
	u.t = term.NewOffscreen(&out, 80, 10)
	u.draw()
	if !u.jumpShown || !strings.Contains(out.String(), i18n.T("jump_last")) {
		t.Fatalf("pill drawn %v: %q", u.jumpShown, out.String())
	}
	u.mouse(term.MouseEvent{X: r.col + 1, Y: r.row, Button: 0, Press: true})
	if w.Scroll != 0 {
		t.Fatalf("click: scroll %d", w.Scroll)
	}
}
