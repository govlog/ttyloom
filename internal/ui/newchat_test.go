package ui

import (
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// TestNewChatFilter : local filter (title, alias, name, accents and case
// ignored), local before the server results, duplicates dropped by id.
func TestNewChatFilter(t *testing.T) {
	alice := &model.Chat{ID: 1, Kind: model.ChatUser, Title: "Alice Martin", Username: "alicem"}
	salon := &model.Chat{ID: 2, Kind: model.ChatGroup, Title: "Équipe Réseau"}
	bob := &model.Chat{ID: 3, Kind: model.ChatUser, Title: "Bob", Username: "bobby"} // contact with no chat
	local := []*model.Chat{alice, salon, bob}
	dupe := &model.Chat{ID: 1, Kind: model.ChatUser, Title: "Alice M."} // same id, come from the server
	far := &model.Chat{ID: 4, Kind: model.ChatChannel, Title: "Alicante Info"}

	rows := newChatFilter(local, nil, "", nil)
	if len(rows) != 3 || rows[0].chat != alice || rows[2].chat != bob {
		t.Fatalf("empty filter: %+v", rows)
	}
	if rows := newChatFilter(local, nil, "EQUIPE", nil); len(rows) != 1 || rows[0].chat != salon {
		t.Fatalf("accents and case ignored: %+v", rows)
	}
	if rows := newChatFilter(local, nil, "@bobby", nil); len(rows) != 1 || rows[0].chat != bob {
		t.Fatalf("filter by username: %+v", rows)
	}
	// Local name (/rename): it counts like the Telegram title.
	title := func(c *model.Chat) string {
		if c == salon {
			return "Copains"
		}
		return c.Title
	}
	if rows := newChatFilter(local, nil, "copains", title); len(rows) != 1 || rows[0].chat != salon {
		t.Fatalf("filter by alias: %+v", rows)
	}

	// Server: after the local ones, under the section header, with no duplicate id.
	rows = newChatFilter(local, []*model.Chat{dupe, far}, "ali", nil)
	if len(rows) != 3 || rows[0].chat != alice || !rows[1].sep || rows[2].chat != far {
		t.Fatalf("local then server: %+v", rows)
	}
	// Every server result already known: no orphan header.
	if rows := newChatFilter(local, []*model.Chat{dupe}, "alice", nil); len(rows) != 1 || rows[0].sep {
		t.Fatalf("empty section: %+v", rows)
	}

	// The header is never selectable: move steps over it.
	n := &newChatBox{rows: newChatFilter(local, []*model.Chat{far}, "ali", nil)}
	n.cur = ncFirst(n.rows, nil)
	n.move(1)
	if n.chat() != far {
		t.Fatalf("header selected: cur=%d", n.cur)
	}
	n.move(1) // end of the list: the selection does not move
	if n.chat() != far {
		t.Fatalf("upper bound: cur=%d", n.cur)
	}
}

// TestNewChatRect : box centred of about 50 columns, bounded to the screen,
// and lines drawn exactly at its width.
func TestNewChatRect(t *testing.T) {
	u := &UI{t: &term.Term{Cols: 80, Rows: 24}}
	r := u.ncRect()
	if r.w != ncW || r.col != (80-ncW)/2 {
		t.Fatalf("width and centering: %+v", r)
	}
	if r.h != 18 || r.row != (24-18)/2 {
		t.Fatalf("height and centering: %+v", r)
	}
	small := (&UI{t: &term.Term{Cols: 40, Rows: 10}}).ncRect()
	if small.w > 40 || small.h > 10 || small.col < 0 || small.row < 0 {
		t.Fatalf("narrow screen: %+v", small)
	}

	// Drawing: one line per screen line, all at the width of the box.
	alice := &model.Chat{ID: 1, Kind: model.ChatUser, Title: "Alice", Username: "alicem"}
	n := &newChatBox{rows: newChatFilter([]*model.Chat{alice}, []*model.Chat{{ID: 2, Title: "Canal\x1bx"}}, "ali", nil)}
	lines := n.Lines(theme.Terminal(), r.w, r.h, nil, func(c *model.Chat) bool { return c.ID == 1 })
	if len(lines) != r.h {
		t.Fatalf("%d lines for a box of %d", len(lines), r.h)
	}
	for i, l := range lines {
		if got := render.Width(render.LineText(l)); got != r.w {
			t.Fatalf("line %d wide %d: %q", i, got, render.LineText(l))
		}
	}
	if !strings.Contains(render.LineText(lines[2]), "@ Alice (@alicem)") || !strings.Contains(render.LineText(lines[2]), "en ligne") {
		t.Fatalf("contact line: %q", render.LineText(lines[2]))
	}
	if !strings.Contains(render.LineText(lines[3]), "sur Telegram") {
		t.Fatalf("section header: %q", render.LineText(lines[3]))
	}
	if strings.ContainsRune(render.LineText(lines[4]), 0x1b) { // remote title: never raw
		t.Fatalf("Clean not applied: %q", render.LineText(lines[4]))
	}
}
