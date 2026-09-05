package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/theme"
)

// checkBox : every line of an overlay box is exactly w cells wide, the first
// and the last carry the horizontal borders, the others the vertical ones.
// Shared by the drawing tests of the centred boxes.
func checkBox(t *testing.T, name string, lines []render.Line, w int) {
	t.Helper()
	if len(lines) < 2 {
		t.Fatalf("%s: %d lines", name, len(lines))
	}
	for i, l := range lines {
		s := render.LineText(l)
		if got := render.Width(s); got != w {
			t.Errorf("%s line %d: width %d, %d expected (%q)", name, i, got, w, s)
		}
		switch {
		case i == 0:
			if !strings.HasPrefix(s, "┌") || !strings.HasSuffix(s, "┐") {
				t.Errorf("%s: top border %q", name, s)
			}
		case i == len(lines)-1:
			if !strings.HasPrefix(s, "└") || !strings.HasSuffix(s, "┘") {
				t.Errorf("%s: bottom border %q", name, s)
			}
		default:
			if !strings.HasPrefix(s, "│") || !strings.HasSuffix(s, "│") {
				t.Errorf("%s line %d: edges %q", name, i, s)
			}
		}
	}
}

// menuEntries : "leave the room" in a group and in a channel, "close the
// chat" in a private chat, the rest the same.
func TestMenuEntries(t *testing.T) {
	for _, k := range []model.ChatKind{model.ChatUser, model.ChatGroup, model.ChatChannel} {
		es := menuEntries(k, false, model.AllCaps())
		if len(es) != 5 {
			t.Fatalf("kind %d: %d entries", k, len(es))
		}
		want := "partir du salon"
		if k == model.ChatUser {
			want = "fermer la conversation"
		}
		if es[0].label != want || es[0].key != "leave" {
			t.Fatalf("kind %d: 1st entry %+v, want %q", k, es[0], want)
		}
		if es[2].label != "supprimer la conversation" {
			t.Fatalf("kind %d: 3rd entry %q", k, es[2].label)
		}
		for i, key := range []string{"leave", "block", "delete", "info", "search"} {
			if es[i].key != key {
				t.Fatalf("kind %d: entry %d = %q, want %q", k, i, es[i].key, key)
			}
		}
	}
}

// menuEntries(_, true) : menu of a member of the member box.
func TestMenuEntriesMember(t *testing.T) {
	es := menuEntries(model.ChatUser, true, model.AllCaps())
	if len(es) != 3 {
		t.Fatalf("%d entries", len(es))
	}
	for i, want := range []menuEntry{{"message privé", "query"}, {"infos", "info"}, {"signaler / bloquer", "block"}} {
		if es[i] != want {
			t.Fatalf("entry %d = %+v, want %+v", i, es[i], want)
		}
	}
	// Kind ignored for a member: the same entries whatever kind is.
	if g := menuEntries(model.ChatGroup, true, model.AllCaps()); g[0] != es[0] {
		t.Fatalf("kind taken into account: %+v", g[0])
	}
}

// menuRect : anchored at the click when it fits, always on the screen.
func TestMenuRect(t *testing.T) {
	es := menuEntries(model.ChatGroup, false, model.AllCaps())
	w := len("supprimer la conversation") + 2                          // longest label + borders
	for _, c := range []struct{ x, y int }{{27, 3}, {5, 0}, {0, 12}} { // the click, even in the sidebar
		if r := menuRect(es, c.x, c.y, 100, 30); r.col != c.x || r.row != c.y || r.w != w || r.h != len(es)+2 {
			t.Fatalf("anchor (%d,%d): %+v", c.x, c.y, r)
		}
	}
	// Bottom right corner: the box moves up and sideways instead of going past the edge.
	for _, tc := range []struct{ x, y, cols, rows int }{
		{27, 3, 100, 30}, {27, 28, 100, 30}, {95, 25, 100, 30},
		{0, 0, 10, 3}, {27, 5, 30, 6}, {200, 200, 80, 24},
	} {
		r := menuRect(es, tc.x, tc.y, tc.cols, tc.rows)
		if r.col < 0 || r.row < 0 || r.col+r.w > tc.cols || r.row+r.h > tc.rows {
			t.Fatalf("off screen %dx%d at (%d,%d): %+v", tc.cols, tc.rows, tc.x, tc.y, r)
		}
		if r.w < 2 || r.h < 2 {
			t.Fatalf("box without borders: %+v", r)
		}
	}
}

// ctxMenu.Lines : one line per entry between the borders, the current entry
// inverted, never more lines than the rect.
func TestMenuLines(t *testing.T) {
	es := menuEntries(model.ChatGroup, false, model.AllCaps())
	r := menuRect(es, 0, 0, 100, 30)
	m := &ctxMenu{entries: es, cur: 2}
	lines := m.Lines(theme.Terminal(), r.w, r.h)
	if len(lines) != len(es)+2 {
		t.Fatalf("%d lines for %d entries", len(lines), len(es))
	}
	checkBox(t, "menu", lines, r.w)
	for i := range es {
		if got := lines[1+i].Spans[1].Style.Reverse; got != (i == m.cur) {
			t.Errorf("entry %d: reversed = %v", i, got)
		}
	}
	if got := lines[1+m.cur].Spans[1].Text; !strings.HasPrefix(got, es[m.cur].label) {
		t.Errorf("current label: %q", got)
	}
	// Rect shorter than the menu: the drawing is cut, never longer.
	if short := m.Lines(theme.Terminal(), r.w, 3); len(short) != 3 {
		t.Fatalf("height bounded: %d lines", len(short))
	}
}

// Clicking a member of a network that cannot resolve a name says so instead
// of opening a window that would wait forever on "resolving…".
func TestMenuMemberQueryUnsupported(t *testing.T) {
	dc := &fakeBackend{} // zero Caps: no Resolve
	u := &UI{ws: NewWindows(), agg: &Window{},
		nets:  map[string]model.Backend{"discord": dc},
		parts: &partsBox{mark: -1}}
	w := u.ws.New(true)
	w.Chat = &model.Chat{Net: "discord", ID: 7, Kind: model.ChatGroup}
	u.ws.Cur = len(u.ws.List) - 1
	n := len(u.ws.List)
	u.openMemberMenu("@alice", 0, 0, 0)
	u.menuDo(u.menu, "query")

	if len(u.ws.List) != n {
		t.Fatalf("window opened on a network with no Resolve: %d windows", len(u.ws.List))
	}
	items := w.Items
	if len(items) != 1 || !strings.Contains(items[0].Sys, i18n.T("net_unsupported", "discord")) {
		t.Fatalf("sys line: %+v", items)
	}
}

// A member's menu keeps the chat of its box: an event that brings back
// window 0 (EvAuthPrompt, EvFatal) while it is open must not send the token
// to the network of the window shown afterward.
func TestMenuMemberPinsChat(t *testing.T) {
	tg, dc := &fakeBackend{caps: model.AllCaps()}, &fakeBackend{caps: model.AllCaps()}
	u := &UI{ws: NewWindows(), agg: &Window{},
		nets:  map[string]model.Backend{model.NetTelegram: tg, "discord": dc},
		parts: &partsBox{mark: -1}}
	w := u.ws.New(true)
	w.Chat = &model.Chat{Net: "discord", ID: 7, Kind: model.ChatUser}
	u.ws.Cur = len(u.ws.List) - 1 // the member box follows the displayed window
	u.openMemberMenu("@alice", 0, 0, 0)

	u.ws.Cur = 0 // what goTo(0) leaves behind: window 0 is bound to nothing
	u.menuDo(u.menu, "info")

	if len(dc.members) != 1 || dc.members[0] != "@alice" {
		t.Fatalf("menu network: %v", dc.members)
	}
	if len(tg.members) != 0 {
		t.Fatalf("token sent to the wrong network: %v", tg.members)
	}
}

// The menu offers nothing the network cannot do: an entry answered by a
// confirmation and then by nothing at all is worse than no entry.
func TestMenuEntriesCaps(t *testing.T) {
	keys := func(es []menuEntry) []string {
		out := make([]string, 0, len(es))
		for _, e := range es {
			out = append(out, e.key)
		}
		return out
	}
	for _, tc := range []struct {
		name   string
		kind   model.ChatKind
		member bool
		caps   model.Caps
		want   []string
	}{
		{"room, nothing allowed", model.ChatGroup, false, model.Caps{},
			[]string{"delete", "info", "search"}},
		{"room, everything allowed", model.ChatGroup, false, model.AllCaps(),
			[]string{"leave", "block", "delete", "info", "search"}},
		// A private chat is never left: the entry only closes its window, so
		// it stays whatever the network can do.
		{"private chat, nothing allowed", model.ChatUser, false, model.Caps{},
			[]string{"leave", "delete", "info", "search"}},
		{"member, nothing allowed", model.ChatUser, true, model.Caps{},
			[]string{"query", "info"}},
		{"member, everything allowed", model.ChatUser, true, model.AllCaps(),
			[]string{"query", "info", "block"}},
	} {
		if got := keys(menuEntries(tc.kind, tc.member, tc.caps)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
	// Wired to the network of the chat, not to a Caps of the UI's own.
	u := &UI{ws: NewWindows(), agg: &Window{}, parts: &partsBox{mark: -1},
		nets: map[string]model.Backend{"discord": &fakeBackend{}}} // zero Caps
	w := u.ws.New(true)
	w.Chat = &model.Chat{Net: "discord", ID: 7, Kind: model.ChatUser}
	u.ws.Cur = len(u.ws.List) - 1
	u.openMemberMenu("@alice", 0, 0, 0)
	if got := keys(u.menu.entries); slices.Contains(got, "block") {
		t.Fatalf("member menu of a network with no Block: %v", got)
	}
}
