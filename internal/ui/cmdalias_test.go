package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/model"
)

// An alias runs its text: a command with the typed arguments appended, or a
// message to the current chat when the text is no command.
func TestCmdAliasRuns(t *testing.T) {
	u, b, room, peer := queryUI()
	u.goTo(u.ws.ForChat(room.Key()))
	input(u, `/alias blop "Blop !!!"`)
	input(u, "/alias hi /msg blop")
	input(u, "/blop")
	input(u, "/HI how are you")
	if want := []model.ChatKey{room.Key(), peer.Key()}; !slices.Equal(b.sends, want) {
		t.Fatalf("sent to %v, want %v", b.sends, want)
	}
	if want := []string{"Blop !!!", "how are you"}; !slices.Equal(b.text, want) {
		t.Fatalf("texts %q, want %q", b.text, want)
	}
}

// A command of the client keeps its name: an alias may not take it.
func TestCmdAliasRefusesACommandName(t *testing.T) {
	u, _, _, _ := queryUI()
	w := u.view()
	for _, name := range []string{"msg", "m", "alias"} {
		input(u, "/alias "+name+" /quit")
		if got := lastSys(w); !strings.Contains(got, name) || u.cmdAlias[name] != "" {
			t.Errorf("/alias %s: %q, alias %q; want a refusal", name, got, u.cmdAlias[name])
		}
	}
}

// The aliases live in commands.toml and /unalias takes one out of the file
// and of the input.
func TestCmdAliasSavedAndRemoved(t *testing.T) {
	u, b, room, _ := queryUI()
	u.goTo(u.ws.ForChat(room.Key()))
	input(u, "/alias ident /msg blop identify pw")
	if m, err := loadCmdAliases(cmdAliasPath()); err != nil || m["ident"] != "/msg blop identify pw" {
		t.Fatalf("commands.toml: %v %v", m, err)
	}
	input(u, "/unalias ident")
	if m, _ := loadCmdAliases(cmdAliasPath()); len(m) != 0 {
		t.Fatalf("after /unalias: %v", m)
	}
	input(u, "/ident")
	if len(b.sends) != 0 {
		t.Fatalf("a removed alias still sends: %q", b.text)
	}
}
