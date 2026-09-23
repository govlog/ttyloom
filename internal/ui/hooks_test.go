package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// hookUI : a UI with one network (me: id 1, "me"), the group chat "room" it
// gives back, and src as hooks.toml, read as at start.
func hookUI(t *testing.T, src string) (*UI, *fakeBackend, *model.Chat) {
	t.Helper()
	b := &fakeBackend{}
	u := &UI{ctx: context.Background(), ws: NewWindows(), agg: &Window{}, debug: &Window{}, cfg: &config.Config{},
		t: &term.Term{Cols: 80, Rows: 24}, nets: map[string]model.Backend{netTelegram: b}, conn: map[string]bool{},
		focused: true, events: make(chan model.Event, 64), chats: map[model.ChatKey]*model.Chat{},
		dirty: map[model.ChatKey]bool{}, self: map[string]selfInfo{netTelegram: {ID: 1, Name: "me"}}}
	writeHooks(t, src)
	u.loadHooks(u.status0, false)
	return u, b, &model.Chat{Net: netTelegram, ID: 2, Kind: model.ChatGroup, Title: "room"}
}

// writeHooks writes src as hooks.toml, removed at the end of the test.
func writeHooks(t *testing.T, src string) {
	t.Helper()
	if err := os.WriteFile(hookPath(), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(hookPath()) })
}

// shScript writes body as a shell script and gives the cmd of hooks.toml that
// runs it through sh: a file just written is never exec'd itself (ETXTBSY).
func shScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(p, []byte(body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return "sh " + p
}

// arrive : m comes in live in c — from bob (id 7), dated now, unless m says otherwise.
func arrive(u *UI, c *model.Chat, m model.Msg) {
	m.ChatID = c.ID
	if m.From == "" && m.FromID == 0 {
		m.From, m.FromID = "bob", 7
	}
	if m.Date.IsZero() {
		m.Date = time.Now()
	}
	u.dispatch(model.Envelope{Net: c.Net, Ev: model.EvNewMessage{Chat: c, Msg: m}})
}

// incoming : arrive, then the runs it started, waited for and handled.
func incoming(u *UI, c *model.Chat, m model.Msg) {
	arrive(u, c, m)
	settle(u)
}

// settle waits for the runs in flight and hands their ends to the UI.
func settle(u *UI) {
	u.hookWait.Wait()
	u.drain()
}

// sysLines : the system lines of w that hold s.
func sysLines(w *Window, s string) int {
	n := 0
	for _, it := range w.Items {
		if it.Sys != "" && strings.Contains(it.Sys, s) {
			n++
		}
	}
	return n
}

// fileLines : the lines of the file at p; 0 when there is none.
func fileLines(t *testing.T, p string) int {
	t.Helper()
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "\n")
}

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// At start hooks.toml is read: its count and each hook left out go to window 0.
func TestHooksAtStart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TTYLOOM_DIR", dir)
	src := "[[hook]]\nname = \"ok\"\ncmd = \"true\"\n\n[[hook]]\nname = \"bad\"\n"
	if err := os.WriteFile(filepath.Join(dir, "hooks.toml"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &cfgFake{}
	cfg, err := config.LoadFrom(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	u := newUI(ctx, cancel, term.NewOffscreen(io.Discard, 80, 24), cfg, theme.Terminal(), []module.Module{m})
	w0 := u.ws.List[0]
	if sysLines(w0, i18n.T("hooks_loaded_one", 1)) != 1 || sysLines(w0, i18n.T("hook_no_cmd", "bad")) != 1 {
		t.Fatalf("window 0: %+v", w0.Items)
	}
}

// A send reply goes to the chat of the message as a message of mine, by a
// path that leaves the reply being prepared and the input alone. The fake has
// no Typing: a typing signal would panic.
func TestHookSend(t *testing.T) {
	u, b, room := hookUI(t, fmt.Sprintf(`[[hook]]
name  = "ping"
match = '^!ping$'
cmd   = '%s'
reply = "send"`, shScript(t, `echo " pong "`)))
	incoming(u, room, model.Msg{ID: 1, Text: "hello"}) // no match: no run
	quoted := &Item{Msg: &model.Msg{Net: netTelegram, ChatID: room.ID, ID: 1}}
	u.reply = quoted
	u.ed.Set("my draft")
	incoming(u, room, model.Msg{ID: 2, Text: "!ping"})
	if !slices.Equal(b.sends, []string{"room: pong"}) {
		t.Fatalf("sends %q", b.sends)
	}
	if u.reply != quoted || u.ed.String() != "my draft" {
		t.Fatalf("reply %v, input %q: the send took them", u.reply, u.ed.String())
	}
	w := u.ws.List[u.ws.ForChat(room.Key())]
	if last := w.Items[len(w.Items)-1].Msg; last == nil || !last.Out || !last.Pending || last.Text != "pong" {
		t.Fatalf("window: %+v", last)
	}
}

// draft: the draft of a hidden window, taken back at the visit; the input of
// the window shown when it is empty; my text is never replaced, nor the reply
// I am preparing — the output then shows as display.
func TestHookDraft(t *testing.T) {
	u, _, room := hookUI(t, fmt.Sprintf(`[[hook]]
name  = "idea"
cmd   = '%s'
reply = "draft"`, shScript(t, "echo try this")))
	incoming(u, room, model.Msg{ID: 1, Text: "a"})
	i := u.ws.ForChat(room.Key())
	w := u.ws.List[i]
	if w.Draft != "try this" || w.Act == 0 {
		t.Fatalf("hidden window: draft %q, act %d", w.Draft, w.Act)
	}
	u.goTo(i)
	if u.ed.String() != "try this" {
		t.Fatalf("visit: input %q", u.ed.String())
	}
	u.ed.Set("")
	incoming(u, room, model.Msg{ID: 2, Text: "b"})
	if u.ed.String() != "try this" {
		t.Fatalf("shown window: input %q", u.ed.String())
	}
	u.ed.Set("mine")
	incoming(u, room, model.Msg{ID: 3, Text: "c"})
	if u.ed.String() != "mine" || sysLines(w, "[idea] try this") != 1 {
		t.Fatalf("busy input: %q, %d lines", u.ed.String(), sysLines(w, "[idea] try this"))
	}
	// A reply being prepared, input still empty: Enter would send the idea
	// quoting a message it was not written for.
	u.ed.Set("")
	u.reply = &Item{Msg: &model.Msg{Net: netTelegram, ChatID: room.ID, ID: 3}}
	incoming(u, room, model.Msg{ID: 4, Text: "d"})
	if u.ed.String() != "" || sysLines(w, "[idea] try this") != 2 {
		t.Fatalf("reply being prepared: input %q, %d lines", u.ed.String(), sysLines(w, "[idea] try this"))
	}
}

// display: one system line per line of the output, after the name; none and
// an empty output: nothing. The window of the chat closed while the run goes:
// the lines land in the window the chat gets again, counted as activity.
func TestHookDisplay(t *testing.T) {
	lines, blank := shScript(t, `printf 'one\ntwo\n'`), shScript(t, `printf '  \n'`)
	u, _, room := hookUI(t, fmt.Sprintf(`[[hook]]
name  = "show"
cmd   = '%[1]s'
reply = "display"

[[hook]]
name = "quiet"
cmd  = '%[1]s'

[[hook]]
name  = "blank"
cmd   = '%[2]s'
reply = "display"`, lines, blank))
	incoming(u, room, model.Msg{ID: 1, Text: "x"})
	w := u.ws.List[u.ws.ForChat(room.Key())]
	if sysLines(w, "[show] one") != 1 || sysLines(w, "[show] two") != 1 || sysLines(w, "[quiet]") != 0 || sysLines(w, "[blank]") != 0 {
		t.Fatalf("lines: %+v", w.Items)
	}
	arrive(u, room, model.Msg{ID: 2, Text: "y"})
	u.closeWindowAt(u.ws.ForChat(room.Key()))
	settle(u)
	i := u.ws.ForChat(room.Key())
	if i < 0 || sysLines(u.ws.List[i], "[show] one") != 1 || u.ws.List[i].Act == 0 {
		t.Fatal("the window closed during the run took the reply with it")
	}
}

// Only news fires a hook: never a message of mine (from here or from another
// device), a service line, a notice, a message caught up after a cut, nor the
// history.
func TestHookNotFired(t *testing.T) {
	mark := filepath.Join(t.TempDir(), "runs")
	u, _, room := hookUI(t, fmt.Sprintf(`[[hook]]
name = "count"
cmd  = '%s'`, shScript(t, `echo x >> "`+mark+`"`)))
	incoming(u, room, model.Msg{ID: 1, Text: "news"})
	incoming(u, room, model.Msg{ID: 2, Text: "mine", Out: true})
	incoming(u, room, model.Msg{ID: 3, Text: "other device", From: "me", FromID: 1})
	incoming(u, room, model.Msg{ID: 4, Service: "alice joined"})
	incoming(u, room, model.Msg{ID: 5, Text: "-srv- maintenance", Notice: true})
	incoming(u, room, model.Msg{ID: 6, Text: "late", Date: time.Now().Add(-3 * time.Minute)})
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvHistory{ChatID: room.ID,
		Msgs: []model.Msg{{ID: 7, ChatID: room.ID, Date: time.Now(), From: "bob", FromID: 7, Text: "old"}}}})
	settle(u)
	if n := fileLines(t, mark); n != 1 {
		t.Fatalf("%d runs, want 1", n)
	}
}

// Where the name is the identity, my name as a word is a mention; elsewhere
// it takes an @ (or a mention entity).
func TestHookMentionByName(t *testing.T) {
	mark := filepath.Join(t.TempDir(), "runs")
	u, _, room := hookUI(t, fmt.Sprintf(`[[hook]]
name    = "call"
mention = true
cmd     = '%s'`, shScript(t, `echo "$TTYLOOM_TEXT" >> "`+mark+`"`)))
	u.nets["names"] = &fakeBackend{caps: model.Caps{NameIsID: true}}
	u.self["names"] = selfInfo{ID: 5, Name: "me"}
	channel := &model.Chat{Net: "names", ID: 3, Kind: model.ChatGroup, Title: "#go"}
	incoming(u, room, model.Msg{ID: 1, Text: "me: hi"})
	incoming(u, room, model.Msg{ID: 2, Text: "@me hi"})
	incoming(u, channel, model.Msg{ID: 3, Text: "me: hi"})
	incoming(u, channel, model.Msg{ID: 4, Text: "meme"})
	b, _ := os.ReadFile(mark)
	if got := string(b); got != "@me hi\nme: hi\n" {
		t.Fatalf("runs on %q", got)
	}
}

// At most 4 runs of a hook at a time: a 5th message is skipped. At most 10
// send replies of a hook a minute: an 11th is dropped.
func TestHookGuards(t *testing.T) {
	dir := t.TempDir()
	gate, mark := filepath.Join(dir, "gate"), filepath.Join(dir, "runs")
	slow := shScript(t, `while [ ! -f "`+gate+`" ]; do sleep 0.02; done; echo x >> "`+mark+`"`)
	u, b, room := hookUI(t, fmt.Sprintf(`[[hook]]
name    = "slow"
match   = '^slow$'
cmd     = '%s'
timeout = "10s"

[[hook]]
name  = "echo"
match = '^echo$'
cmd   = '%s'
reply = "send"`, slow, shScript(t, "echo pong")))
	for i := 1; i <= 5; i++ {
		arrive(u, room, model.Msg{ID: i, Text: "slow"})
	}
	touch(t, gate)
	settle(u)
	if n := fileLines(t, mark); n != 4 {
		t.Fatalf("%d runs of slow, want 4", n)
	}
	for i := 10; i <= 20; i++ {
		incoming(u, room, model.Msg{ID: i, Text: "echo"})
	}
	if len(b.sends) != 10 {
		t.Fatalf("%d sends, want 10", len(b.sends))
	}
}

// The first failure of a hook writes one line in window 0, with the first
// line of stderr; the next ones only go to the log, until a success. Every
// failure is in the log.
func TestHookFailures(t *testing.T) {
	ok := filepath.Join(t.TempDir(), "ok")
	u, _, room := hookUI(t, fmt.Sprintf(`[[hook]]
name = "flaky"
cmd  = '%s'`, shScript(t, `[ -f "`+ok+`" ] && exit 0
echo boom >&2
exit 3`)))
	w0 := u.ws.List[0]
	incoming(u, room, model.Msg{ID: 1, Text: "a"})
	incoming(u, room, model.Msg{ID: 2, Text: "b"})
	if n := sysLines(w0, "boom"); n != 1 {
		t.Fatalf("two failures: %d lines, want 1", n)
	}
	touch(t, ok)
	incoming(u, room, model.Msg{ID: 3, Text: "c"})
	if err := os.Remove(ok); err != nil {
		t.Fatal(err)
	}
	incoming(u, room, model.Msg{ID: 4, Text: "d"})
	if n := sysLines(w0, "boom"); n != 2 {
		t.Fatalf("failure after a success: %d lines, want 2", n)
	}
	if n := sysLines(u.debug, "boom"); n != 3 {
		t.Fatalf("log: %d failures, want 3", n)
	}
}
