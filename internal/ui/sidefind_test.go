package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// findUI : chat sidebar sorted a→z with three private chats, none open, the
// pointer not over the panel yet.
func findUI() *UI {
	now := time.Now()
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, th: theme.Terminal(),
		cfg: &config.Config{SidebarSort: "alpha", Hover: config.HoverMenu},
		t:   &term.Term{Cols: 80, Rows: 24}, side: sideChats, sideW: testSideW,
		nets:    map[string]model.Backend{netTelegram: &queryBackend{}},
		chats:   map[model.ChatKey]*model.Chat{},
		aliases: map[model.ChatKey]string{}, gotContacts: true}
	for i, title := range []string{"christopher", "Fabien", "Stéfany"} {
		c := &model.Chat{Net: netTelegram, ID: int64(i + 1), Kind: model.ChatUser, Title: title, LastDate: now}
		u.chatList = append(u.chatList, c)
		u.chats[c.Key()] = c
	}
	return u
}

// typeIn sends s key by key, as the terminal does.
func typeIn(u *UI, s string) {
	for _, r := range s {
		u.key(term.Key{Rune: r})
	}
}

// overSide moves the pointer over the list of the panel (what Run() does on a
// motion), off puts it over the messages.
func overSide(u *UI)           { u.zoneAt(2, sideHdr) }
func overMsgs(u *UI)           { u.zoneAt(50, 5) }
func shiftTab(u *UI)           { u.key(term.Key{Code: term.Tab, Shift: true}) }
func press(u *UI, c term.Code) { u.key(term.Key{Code: c}) }

// TestSideFindEnterOpens : pointer over the panel, "chris" leaves christopher
// alone under the cursor; Enter opens its window and gives the keyboard back
// to the input line: "salut" + Enter goes to christopher. The list is whole
// again.
func TestSideFindEnterOpens(t *testing.T) {
	u := findUI()
	overSide(u)
	typeIn(u, "chris")
	if got := rowNames(u.sideRowList()); !slices.Equal(got, []string{"christopher"}) {
		t.Fatalf("filtered rows: %v", got)
	}
	if u.ed.String() != "" {
		t.Fatalf("the filter went to the input line: %q", u.ed.String())
	}
	press(u, term.Enter)
	if c := u.ws.Current().Chat; c == nil || c.Title != "christopher" {
		t.Fatalf("Enter opened %v, want christopher", c)
	}
	typeIn(u, "salut")
	press(u, term.Enter)
	if b := u.nets[netTelegram].(*queryBackend); !slices.Equal(b.text, []string{"salut"}) || !slices.Equal(b.sends, []model.ChatKey{tgk(1)}) {
		t.Fatalf("after Enter the keys must go to christopher: sent %q to %v", b.text, b.sends)
	}
	if n := len(u.sideRowList()); n != 3 {
		t.Fatalf("after Enter the filter must go: %d rows", n)
	}
}

// TestSideFindArrows : the filter folds case and accents ("FA" finds Fabien
// and Stéfany); the arrows walk the lines left with a clamp at both ends, and
// Enter opens the one under the cursor.
func TestSideFindArrows(t *testing.T) {
	u := findUI()
	overSide(u)
	typeIn(u, "FA")
	if got := rowNames(u.sideRowList()); !slices.Equal(got, []string{"Fabien", "Stéfany"}) {
		t.Fatalf("filtered rows: %v", got)
	}
	press(u, term.Up) // already on the first line: clamp
	press(u, term.Down)
	press(u, term.Down) // last line: clamp
	press(u, term.Enter)
	if c := u.ws.Current().Chat; c == nil || c.Title != "Stéfany" {
		t.Fatalf("Enter opened %v, want Stéfany", c)
	}
	if len(u.ws.List) != 2 {
		t.Fatalf("%d windows: the arrows must open nothing", len(u.ws.List))
	}
}

// TestSideFindEscape : Escape drops the filter, opens nothing and gives the
// keyboard back to the input line.
func TestSideFindEscape(t *testing.T) {
	u := findUI()
	overSide(u)
	typeIn(u, "fa")
	press(u, term.Esc)
	if n := len(u.sideRowList()); n != 3 {
		t.Fatalf("Escape must drop the filter: %d rows", n)
	}
	typeIn(u, "x")
	if u.ed.String() != "x" || len(u.ws.List) != 1 {
		t.Fatalf("after Escape: input %q, %d windows", u.ed.String(), len(u.ws.List))
	}
}

// TestSideFindWheel : a filter typed by mistake ("zz" leaves no line) does not
// block the wheel over the panel: the notch drops the filter, steps in the
// whole list and gives the keyboard back to the input line.
func TestSideFindWheel(t *testing.T) {
	u := findUI()
	u.ws.New(false).Chat = u.chatList[0]
	u.ws.New(false).Chat = u.chatList[1] // Fabien is the current line
	overSide(u)
	typeIn(u, "zz")
	u.mouse(term.MouseEvent{X: 2, Y: sideHdr, Button: 64, Press: true}) // wheel up
	if c := u.ws.Current().Chat; c == nil || c.Title != "christopher" || len(u.sideRowList()) != 3 {
		t.Fatalf("wheel up: chat %v, %d rows", c, len(u.sideRowList()))
	}
	typeIn(u, "x")
	if u.ed.String() != "x" {
		t.Fatalf("after the wheel the keys must go to the input line: %q", u.ed.String())
	}
}

// TestSideFindKeyboardFocus : the keyboard follows the pointer into the panel
// and out of it, Shift+Tab moves it both ways, and a click on a line hands it
// back to the input line. The filter stays until Escape, Enter or a click.
func TestSideFindKeyboardFocus(t *testing.T) {
	u := findUI()
	typeIn(u, "a") // pointer elsewhere: the input line
	overSide(u)
	typeIn(u, "f")
	overMsgs(u)
	typeIn(u, "b")
	if got := rowNames(u.sideRowList()); u.ed.String() != "ab" || !slices.Equal(got, []string{"Fabien", "Stéfany"}) {
		t.Fatalf("follow-mouse: input %q, rows %v", u.ed.String(), got)
	}
	shiftTab(u)
	typeIn(u, "a")
	shiftTab(u)
	typeIn(u, "c")
	if got := rowNames(u.sideRowList()); u.ed.String() != "abc" || !slices.Equal(got, []string{"Fabien", "Stéfany"}) {
		t.Fatalf("Shift+Tab: input %q, rows %v (the filter stays when the keyboard leaves)", u.ed.String(), got)
	}
	overSide(u)
	u.mouse(term.MouseEvent{X: 2, Y: sideHdr + 1, Press: true}) // second line: Stéfany
	typeIn(u, "d")
	if c := u.ws.Current().Chat; c == nil || c.Title != "Stéfany" || u.ed.String() != "d" || u.ws.List[0].Draft != "abc" || len(u.sideRowList()) != 3 {
		t.Fatalf("click: chat %v, input %q (the draft stays with the window left: %q), %d rows", c, u.ed.String(), u.ws.List[0].Draft, len(u.sideRowList()))
	}
}

// TestSideFindDraw : the hits are underlined in the title, the cursor line
// stands apart from the current line, the rule under the header shows the
// filter and the terminal cursor sits at its end.
func TestSideFindDraw(t *testing.T) {
	u := findUI()
	u.ws.New(false).Chat = u.chatList[1] // Fabien is the current line
	overSide(u)
	typeIn(u, "fa")
	press(u, term.Down) // cursor on Stéfany
	side, _ := u.sideBlock(-1)
	if rule := body(side[1]); !strings.Contains(rule, "fa") {
		t.Fatalf("rule line: %q", rule)
	}
	var fab, stef render.Line
	for _, l := range side[sideHdr:] {
		switch {
		case strings.Contains(render.LineText(l), "Fabien"):
			fab = l
		case strings.Contains(render.LineText(l), "Stéfany"):
			stef = l
		}
	}
	hit := func(l render.Line, text string) bool {
		return slices.ContainsFunc(l.Spans, func(sp render.Span) bool { return sp.Text == text && sp.Style.Underline })
	}
	if !hit(fab, "Fa") || !hit(stef, "fa") {
		t.Fatalf("hits not set apart: %+v / %+v", fab.Spans, stef.Spans)
	}
	if !hasReverse(fab) || !hasReverse(stef) || content(fab)[0].Style.FG == content(stef)[0].Style.FG {
		t.Fatal("the cursor line must be inverted in another colour than the current line")
	}
	var b strings.Builder
	u.t = term.NewOffscreen(&b, 80, 24)
	u.draw()
	if !strings.HasSuffix(b.String(), "\x1b[2;12H\x1b[?25h\x1b[?2026l") { // "filtre : fa" (tests run in fr) is 11 cells wide
		t.Fatalf("terminal cursor not at the end of the filter: %q", b.String()[max(0, b.Len()-40):])
	}
}

// TestSideFindFolded : a hit in a folded section shows while the filter runs.
func TestSideFindFolded(t *testing.T) {
	u := wheelUI()
	u.folded[netTelegram] = true
	overSide(u)
	typeIn(u, "none")
	if got := rowNames(u.sideRowList()); !slices.Equal(got, []string{"telegram-none"}) {
		t.Fatalf("rows: %v", got)
	}
}

// TestSideFindWindows : in windows mode the filter cuts the windows by name
// and Enter goes to the one under the cursor.
func TestSideFindWindows(t *testing.T) {
	u := winSortUI()
	u.cfg.Hover = config.HoverMenu
	overSide(u)
	typeIn(u, "bo")
	if got := u.sideWins(); !slices.Equal(got, []int{3}) {
		t.Fatalf("windows drawn: %v", got)
	}
	press(u, term.Enter)
	if u.ws.Cur != 3 {
		t.Fatalf("Enter went to window %d, want 3", u.ws.Cur)
	}
}

// The pointer gave the keyboard to the panel, then left the terminal over it
// (no event says so): back with Alt+Tab and no motion, the typing goes to the
// input line. A keyboard given by Shift+Tab stays with the panel.
func TestRegressionFocusGivesPointerKeyboardBack(t *testing.T) {
	u := findUI()
	overSide(u)
	press(u, term.FocusOut)
	press(u, term.FocusIn)
	typeIn(u, "hello")
	if u.ed.String() != "hello" || u.find.q != "" {
		t.Fatalf("after a focus round trip: input %q, panel filter %q", u.ed.String(), u.find.q)
	}
	overMsgs(u)
	shiftTab(u)
	press(u, term.FocusOut)
	press(u, term.FocusIn)
	if !u.sideHasKeys() {
		t.Fatal("Shift+Tab gave the keyboard to the panel: a focus round trip keeps it there")
	}
}

// TestSideHover : the sidebar line under the pointer takes the background of
// a hovered message, and a repaint is asked only when the line changes. The
// current line keeps its inversion; the │ bar, the messages and hover = off
// leave no line lit.
func TestSideHover(t *testing.T) {
	u := findUI()
	u.openChat(u.chatList[0]) // christopher: the current line
	bg := u.th.Color(theme.CodeBG)
	lit := func() (out []string) {
		lines, _ := u.sideBlock(-1)
		for _, l := range lines {
			if slices.ContainsFunc(l.Spans, func(s render.Span) bool { return s.Style.BG == bg }) {
				out = append(out, render.LineText(l))
			}
		}
		return out
	}
	if !u.sideHoverAt(2, sideHdr+1) {
		t.Fatal("pointer onto Fabien: no repaint")
	}
	if got := lit(); len(got) != 1 || !strings.Contains(got[0], "Fabien") {
		t.Fatalf("over Fabien, lit lines: %q", got)
	}
	if u.sideHoverAt(5, sideHdr+1) {
		t.Fatal("same line: no repaint")
	}
	for _, p := range [][2]int{{2, sideHdr}, {u.sideW, sideHdr + 1}, {50, 5}} { // current line, │ bar, messages
		u.sideHoverAt(2, sideHdr+1)
		if !u.sideHoverAt(p[0], p[1]) {
			t.Fatalf("%v: no repaint", p)
		}
		if got := lit(); len(got) != 0 {
			t.Fatalf("%v, lit lines: %q", p, got)
		}
	}
	u.cfg.Hover = config.HoverOff
	if u.sideHoverAt(2, sideHdr+1) || len(lit()) != 0 {
		t.Fatalf("hover off, lit lines: %q", lit())
	}
}
