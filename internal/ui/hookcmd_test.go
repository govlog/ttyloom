package ui

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/govlog/ttyloom/internal/hook"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
	"github.com/govlog/ttyloom/protocols/dsc"
)

// hookCmd types line in the window shown.
func hookCmd(u *UI, line string) {
	name, args, text, _ := ParseCommand(line, u.commandNames())
	u.command(name, args, text)
}

// /hooks lists each hook with its counters. /hooks reload reads the file
// again: an invalid file keeps the hooks of before; a hook that keeps its
// name keeps its counters; the late end of a run of a hook gone does nothing.
func TestHooksReload(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "gate")
	a, slow := shScript(t, "echo a"), shScript(t, `while [ ! -f "`+gate+`" ]; do sleep 0.02; done; echo late`)
	u, b, room := hookUI(t, fmt.Sprintf(`[[hook]]
name  = "a"
chats = ["room"]
cmd   = '%s'
reply = "send"

[[hook]]
name  = "gone"
cmd   = '%s'
reply = "send"`, a, slow))
	w0 := u.ws.List[0]
	arrive(u, room, model.Msg{ID: 1, Text: "x"}) // a ends at once, gone waits for the gate
	writeHooks(t, "[[hook]\nname =")
	hookCmd(u, "/hooks reload")
	hookCmd(u, "/hooks")
	goneRow := i18n.T("hook_row", "gone", "*", hook.ReplySend, 1, 0, 0, 0, i18n.T("hook_never"))
	if sysLines(w0, "hooks.toml") != 1 || sysLines(w0, goneRow) != 1 {
		t.Fatalf("invalid file: %+v", w0.Items)
	}
	writeHooks(t, fmt.Sprintf(`[[hook]]
name  = "a"
chats = ["room"]
cmd   = '%s'
reply = "send"

[[hook]]
name = "b"
cmd  = "true"`, a))
	hookCmd(u, "/hooks reload")
	touch(t, gate)
	settle(u)
	hookCmd(u, "/hooks")
	aRow := i18n.T("hook_row", "a", "room", hook.ReplySend, 1, 0, 0, 0, i18n.T("hook_ok"))
	bRow := i18n.T("hook_row", "b", "*", hook.ReplyNone, 0, 0, 0, 0, i18n.T("hook_never"))
	// "2 hooks loaded" twice: at start (a, gone), then at the reload (a, b).
	if sysLines(w0, i18n.T("hooks_loaded_many", 2)) != 2 || sysLines(w0, aRow) != 1 || sysLines(w0, bRow) != 1 {
		t.Fatalf("reload: %+v", w0.Items)
	}
	if !slices.Equal(b.sends, []string{"room: a"}) {
		t.Fatalf("sends %q: the late end of gone did something", b.sends)
	}
}

// /hooks test runs a hook on a text of mine in the chat of the window: only
// match counts (from would refuse me), its groups are filled, and the output
// shows, never sent.
func TestHooksTest(t *testing.T) {
	u, b, room := hookUI(t, fmt.Sprintf(`[[hook]]
name  = "meteo"
from  = ["42"]
match = '^!meteo (?P<ville>\w+)$'
cmd   = '%s'
reply = "send"`, shScript(t, `echo "$TTYLOOM_MATCH_VILLE for $TTYLOOM_FROM"`)))
	incoming(u, room, model.Msg{ID: 1, Text: "hi"}) // the chat gets its window
	u.goTo(u.ws.ForChat(room.Key()))
	w := u.view()
	hookCmd(u, "/hooks test meteo !meteo Paris")
	settle(u)
	hookCmd(u, "/hooks test meteo hello")
	if sysLines(w, "[meteo] Paris for me") != 1 || sysLines(w, i18n.T("hook_test_nomatch", "meteo")) != 1 || len(b.sends) != 0 {
		t.Fatalf("lines %+v, sends %q", w.Items, b.sends)
	}
}

// A send hook that can reach a configured network of a module with a warning
// (Discord: self-bots) gets that warning at load; one kept to another network
// does not, nor a hook that sends nothing.
func TestHookModuleWarning(t *testing.T) {
	u, _, _ := hookUI(t, "")
	u.mods = []module.Module{dsc.NewModule()}
	u.netList = []string{dsc.Net, "other"}
	writeHooks(t, `[[hook]]
name  = "wide"
cmd   = "true"
reply = "send"

[[hook]]
name  = "narrow"
net   = "other"
cmd   = "true"
reply = "send"

[[hook]]
name = "quiet"
cmd  = "true"`)
	hookCmd(u, "/hooks reload")
	w0, warn := u.ws.List[0], i18n.T("dsc_autoreply_warn")
	if sysLines(w0, warn) != 1 || sysLines(w0, i18n.T("hook_note", "wide", warn)) != 1 {
		t.Fatalf("window 0: %+v", w0.Items)
	}
}
