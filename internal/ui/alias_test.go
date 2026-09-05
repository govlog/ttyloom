package ui

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/theme"
)

// aliases.toml: bare "<id>" = telegram (older files), "<net>:<id>" otherwise.
func TestAliasKeyRoundTrip(t *testing.T) {
	for _, c := range []struct {
		k model.ChatKey
		s string
	}{
		{model.ChatKey{Net: "telegram", ID: 42}, "42"},
		{model.ChatKey{Net: "discord", ID: 42}, "discord:42"},
	} {
		if got := aliasKey(c.k); got != c.s {
			t.Fatalf("aliasKey(%+v) = %q", c.k, got)
		}
		if k, ok := parseAliasKey(c.s); !ok || k != c.k {
			t.Fatalf("parseAliasKey(%q) = %+v %v", c.s, k, ok)
		}
	}
	if _, ok := parseAliasKey("nope:x"); ok {
		t.Fatal("unreadable key accepted")
	}
	// Hand-edited file: an empty net would build the forbidden key {"", id}.
	if k, ok := parseAliasKey(":42"); ok {
		t.Fatalf("empty net accepted: %+v", k)
	}
}

func TestAliasRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aliases.toml")
	got, err := loadAliases(path) // missing file: no alias, no error
	if err != nil || len(got) != 0 {
		t.Fatalf("missing file: %v, %v", got, err)
	}
	tg := func(id int64) model.ChatKey { return model.ChatKey{Net: model.NetTelegram, ID: id} }
	want := map[model.ChatKey]string{tg(7): "maman", tg(-1001234): "le canal"}
	if err := saveAliases(path, want); err != nil {
		t.Fatal(err)
	}
	if got, err = loadAliases(path); err != nil || !maps.Equal(got, want) {
		t.Fatalf("reread: %v, %v", got, err)
	}
	// /unrename : an alias dropped from the map goes away from the file.
	if err := saveAliases(path, map[model.ChatKey]string{tg(7): "maman"}); err != nil {
		t.Fatal(err)
	}
	if got, _ = loadAliases(path); len(got) != 1 || got[tg(7)] != "maman" {
		t.Fatalf("after removal: %v", got)
	}
	// File edited by hand: control neutralised, a value too long or an
	// unreadable key ignored — never an error.
	raw := "[aliases]\n\"7\" = \"ma\\u001Bman\"\n\"8\" = \"" + strings.Repeat("x", aliasMax+1) + "\"\n\"zz\" = \"x\"\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err = loadAliases(path); err != nil || len(got) != 1 || got[tg(7)] != "ma man" {
		t.Fatalf("hand-edited file: %v, %v", got, err)
	}
}

// TestFindChatCollision : a local name that copies the title of another chat
// is no longer resolved in silence.
func TestFindChatCollision(t *testing.T) {
	a := &model.Chat{Net: model.NetTelegram, ID: 1, Title: "Alice"}
	b := &model.Chat{Net: model.NetTelegram, ID: 2, Title: "Bob"}
	u := &UI{ws: NewWindows(), agg: &Window{}, chats: map[model.ChatKey]*model.Chat{a.Key(): a, b.Key(): b},
		chatList: []*model.Chat{a, b}, aliases: map[model.ChatKey]string{b.Key(): "Alice"}}
	if got, ambiguous := u.findChat("Alice"); got != nil || !ambiguous {
		t.Fatalf("exact collision: %v, %v", got, ambiguous)
	}
	if got, _ := u.findChat("Bob"); got != b { // unique exact name: always resolved
		t.Fatal("unique title not resolved")
	}
}

// TestTitleAlias : /rename replaces the title in the sidebar and the status
// bar, /unrename brings it back.
func TestTitleAlias(t *testing.T) {
	t.Setenv("TTYLOOM_DIR", t.TempDir()) // setAlias writes aliases.toml
	th := theme.Terminal()
	c := &model.Chat{Net: model.NetTelegram, ID: 7, Kind: model.ChatUser, Title: "Alice Dupont"}
	u := &UI{ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{}, th: th,
		chats: map[model.ChatKey]*model.Chat{c.Key(): c}, chatList: []*model.Chat{c}, aliases: map[model.ChatKey]string{}}
	w := u.ws.New(false)
	w.Chat = c

	u.command("rename", []string{"maman"}, "maman")
	if u.aliases[c.Key()] != "maman" || u.title(c) != "maman" {
		t.Fatalf("alias set: %v", u.aliases)
	}
	line := body(sidebarLines(sideChats, u.chatList, u.ws.List, nil, u.ws.Cur, th, testSideW, 1, 0, false, false, 0, -1, false, u.title)[0])
	if !strings.Contains(line, "maman") || strings.Contains(line, "Alice") {
		t.Fatalf("sidebar: %q", line)
	}
	var b strings.Builder
	u.drawStatus(&b, 1, 0, 80)
	if !strings.Contains(b.String(), "maman") || strings.Contains(b.String(), "Alice") {
		t.Fatalf("status: %q", b.String())
	}
	// Drawing: [chat] label of the aggregate and peer name in a private chat
	// (the peer carries the id of the chat).
	m := &model.Msg{Net: model.NetTelegram, ID: 1, ChatID: 7, FromID: 7, From: "Alice Dupont", ChatLabel: "Alice Dupont",
		Text: "salut", Date: time.Now()}
	if got := render.LineText(render.Message(m, render.Opts{Width: 60, Theme: th, ShowChat: true, Alias: u.aliasOf})[0]); !strings.Contains(got, "[maman]") || !strings.Contains(got, "<maman>") {
		t.Fatalf("rendered: %q", got)
	}
	// Unknown target: refusal, never a silent rename of the current window
	// to "Friends - Blabla".
	u.command("rename", []string{"Friends", "-", "Blabla"}, "Friends - Blabla")
	if sys := w.Items[len(w.Items)-1].Sys; u.title(c) != "maman" || !strings.Contains(sys, "inconnu") {
		t.Fatalf("unknown target: %q, %q", u.title(c), sys)
	}
	// Quotes: a name of several words on the current window.
	u.command("rename", nil, `"maman de Paris"`)
	if u.title(c) != "maman de Paris" {
		t.Fatalf("quoted name: %q", u.title(c))
	}
	// findChat resolves the local name like a title.
	if got, _ := u.findChat("maman"); got != c {
		t.Fatal("findChat on the local name")
	}

	u.command("unrename", nil, "")
	if len(u.aliases) != 0 || u.title(c) != "Alice Dupont" {
		t.Fatalf("alias removed: %v", u.aliases)
	}
	b.Reset()
	u.drawStatus(&b, 1, 0, 80)
	if !strings.Contains(b.String(), "Alice Dupont") {
		t.Fatalf("status restored: %q", b.String())
	}
	// The file follows: nothing to load again at the next start.
	if got, _ := loadAliases(aliasPath()); len(got) != 0 {
		t.Fatalf("file: %v", got)
	}
}
