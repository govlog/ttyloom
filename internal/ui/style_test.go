package ui

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// Ctrl+B/I/U leave IRC toggles in the draft: the send splits it into runs,
// the styles stack (underline+italic), and the echo carries the spans.
func TestParseStyle(t *testing.T) {
	if parseStyle("plain") != nil {
		t.Fatal("no marker: nil")
	}
	segs := parseStyle("a\x02b\x1fc\x1dd\x1de\x1ff")
	want := []model.Seg{{Text: "a"}, {Text: "b", Bold: true}, {Text: "c", Bold: true, Underline: true},
		{Text: "d", Bold: true, Underline: true, Italic: true}, {Text: "e", Bold: true, Underline: true}, {Text: "f", Bold: true}}
	if len(segs) != len(want) {
		t.Fatalf("segments: %+v", segs)
	}
	for i := range want {
		if segs[i] != want[i] {
			t.Fatalf("segment %d: %+v, want %+v", i, segs[i], want[i])
		}
	}
	if got := fenceText(segs); got != "abcdef" { // inline runs: no line break between them
		t.Fatalf("text: %q", got)
	}
	ents := fenceEntities(segs)
	// bold over b..f, underline over c..e, italic over d: one span per run and style
	n := map[model.SpanKind]int{}
	for _, e := range ents {
		n[e.Kind]++
	}
	if n[model.SpanBold] != 5 || n[model.SpanUnderline] != 3 || n[model.SpanItalic] != 1 {
		t.Fatalf("entities: %+v", ents)
	}
	if e := ents[len(ents)-1]; e.Start != 5 || e.End != 6 || e.Kind != model.SpanBold {
		t.Fatalf("last entity: %+v", e)
	}
}

// styleAt : the styles active at the cursor, for the status bar.
func TestStyleAt(t *testing.T) {
	if got := styleLabel([]rune("ab")); got != "" {
		t.Fatalf("plain: %q", got)
	}
	if got := styleLabel([]rune("\x02a\x1fb\x1dc\x1d")); got != "bold+underline" {
		t.Fatalf("label: %q", got)
	}
}

type styledBackend struct {
	queryBackend
	segs []model.Seg
}

func (b *styledBackend) SendStyled(_ context.Context, _ *model.Chat, segs []model.Seg, _ int64) {
	b.segs = segs
}

// Ctrl+B, typed text, Ctrl+B again, more text, Enter: the network gets the
// runs, the local echo has the plain text and the bold span.
func TestStyleKeysSend(t *testing.T) {
	u, _, room, _ := queryUI()
	b := &styledBackend{}
	b.caps = model.AllCaps()
	u.nets[model.NetTelegram] = b
	u.ws.Cur = 1
	w := u.view()
	if w.Chat != room {
		t.Fatalf("window 1 bound to %v", w.Chat)
	}
	u.key(term.Key{Code: term.Ctrl, Rune: 'b'})
	u.key(term.Key{Rune: 'x'})
	u.key(term.Key{Code: term.Ctrl, Rune: 'b'})
	u.key(term.Key{Rune: 'y'})
	u.key(term.Key{Code: term.Enter})
	want := []model.Seg{{Text: "x", Bold: true}, {Text: "y"}}
	if !slices.Equal(b.segs, want) {
		t.Fatalf("segments sent: %+v", b.segs)
	}
	var echo *model.Msg
	for _, it := range w.Items {
		if it.Msg != nil {
			echo = it.Msg
		}
	}
	if echo == nil || echo.Text != "xy" || !slices.Equal(echo.Entities, []model.Span{{Start: 0, End: 1, Kind: model.SpanBold}}) {
		t.Fatalf("local echo: %+v", echo)
	}
}

// The input line hides the markers and shows the style; the status bar
// names the styles active at the cursor.
func TestStyleInputAndStatus(t *testing.T) {
	u := netUI(model.NetTelegram)
	u.th = theme.Terminal()
	u.ws.New(false).Chat = u.chatList[0]
	u.ed.Insert("a\x02b\x1fc")
	var b strings.Builder
	u.drawInput(&b, 24, 0, 80)
	out := b.String()
	if strings.ContainsAny(out, marks) {
		t.Fatalf("marker written to the terminal: %q", out)
	}
	bold := theme.Style{FG: u.th.FG, BG: u.th.BG, Bold: true}.SGR()
	both := theme.Style{FG: u.th.FG, BG: u.th.BG, Bold: true, Underline: true}.SGR()
	if !strings.Contains(out, bold+"b") || !strings.Contains(out, both+"c") {
		t.Fatalf("styles of the runs: %q", out)
	}
	b.Reset()
	u.drawStatus(&b, 23, 0, 80)
	if !strings.Contains(b.String(), "[bold+underline]") {
		t.Fatalf("status: %q", b.String())
	}
	u.ed.Home()
	b.Reset()
	u.drawStatus(&b, 23, 0, 80)
	if strings.Contains(b.String(), "[bold") {
		t.Fatalf("status at the start of the line: %q", b.String())
	}
}
