package ui

import (
	"context"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/govlog/ttyloom/internal/cache"
	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// noteNet : the one network of the note tests.
const noteNet = "fake:a"

// noteMod : the fake module, with the backend newB gives at each login.
type noteMod struct {
	cfgFake
	newB func() model.Backend
}

func (m *noteMod) Launch(context.Context, module.Host, string, chan<- model.Event) (model.Backend, error) {
	return m.newB(), nil
}

// accountNet : a network that keeps the history on the server and knows its
// account by a stable id, as Telegram and Discord do.
func accountNet() model.Backend { return &runBackend{fakeBackend{caps: model.Caps{History: true}}} }

// nameNet : a network with no server history whose chat ids come from their
// names, as IRC: ids gives the id of each name.
type nameNet struct {
	runBackend
	ids map[string]int64
}

func (b *nameNet) ChatID(name string) int64 { return b.ids[name] }

// noteStart gives a start function for a client with the network noteNet,
// all its files in one directory: each call is a new session on them.
func noteStart(t *testing.T, newB func() model.Backend) func() *UI {
	dir := t.TempDir()
	t.Setenv("TTYLOOM_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[fakenet]\nnets = [\""+noteNet+"\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return func() *UI {
		m := &noteMod{newB: newB}
		cfg, err := config.LoadFrom(dir, m)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		u := newUI(ctx, cancel, term.NewOffscreen(io.Discard, 80, 24), cfg, theme.Terminal(), []module.Module{m})
		t.Cleanup(u.bgWait.Wait) // the cache writes end before their directory goes
		return u
	}
}

// noteReady : the network says which account is connected.
func noteReady(u *UI, self int64) {
	u.dispatch(model.Envelope{Net: noteNet, Ev: model.EvReady{SelfID: self, SelfName: "me"}})
}

// noteRelogin : logout, then a login as account self.
func noteRelogin(u *UI, self int64) {
	u.dispatch(model.Envelope{Net: noteNet, Ev: model.EvStopped{}})
	u.startNet(noteNet)
	noteReady(u, self)
}

// noteOpen : the network lists the chat, and its window becomes the current one.
func noteOpen(u *UI, c model.Chat) {
	c.Net = noteNet
	u.dispatch(model.Envelope{Net: noteNet, Ev: model.EvDialogs{Chats: []*model.Chat{&c}}})
	u.openChat(u.chats[c.Key()])
}

// noteBanner : the banner the current window shows.
func noteBanner(u *UI) string {
	text, _, _ := u.pinBanner(u.view())
	return text
}

// noteEntries : the [notes] table of notes.toml as a hand editor reads it.
func noteEntries(t *testing.T) map[string]string {
	t.Helper()
	var f struct {
		Notes map[string]string `toml:"notes"`
	}
	if _, err := toml.DecodeFile(notesPath(), &f); err != nil {
		t.Fatalf("notes.toml: %v", err)
	}
	return f.Notes
}

var alice = model.Chat{ID: 2, Kind: model.ChatUser, Title: "alice"}

// TestNote : /note puts a local note, shown in the banner of the window and
// kept in notes.toml for the next start; /unnote removes both.
func TestNote(t *testing.T) {
	u := noteStart(t, accountNet)()
	noteReady(u, 111)
	noteOpen(u, alice)
	input(u, "/note appeler le client")
	if text, jump, ok := u.pinBanner(u.view()); !ok || jump != 0 || !strings.Contains(text, "appeler le client") {
		t.Fatalf("banner after /note: %q jump %d ok %v", text, jump, ok)
	}
	if n := noteEntries(t); len(n) != 1 || !strings.Contains(strings.Join(slices.Collect(maps.Values(n)), ""), "appeler le client") {
		t.Fatalf("notes.toml after /note: %v", n)
	}
	input(u, "/unnote")
	if _, _, ok := u.pinBanner(u.view()); ok {
		t.Fatal("the banner must go with /unnote")
	}
	if n := noteEntries(t); len(n) != 0 {
		t.Fatalf("notes.toml after /unnote: %v", n)
	}
}

// A note belongs to the account that wrote it. On Telegram a private chat has
// the id of the peer on every account: another account on the same network
// does not see the note on its own chat with that peer, the first account
// finds it again when it comes back, and the file keeps the notes of both.
func TestNoteBelongsToItsAccount(t *testing.T) {
	u := noteStart(t, accountNet)()
	noteReady(u, 111)
	noteOpen(u, alice)
	input(u, "/note first account: owes us money")
	noteRelogin(u, 222)
	noteOpen(u, alice)
	if got := noteBanner(u); got != "" {
		t.Fatalf("second account shows the banner %q of the first account", got)
	}
	input(u, "/note second account: met at the fair")
	noteRelogin(u, 111)
	noteOpen(u, alice)
	if got := noteBanner(u); !strings.Contains(got, "owes us money") {
		t.Fatalf("first account back: banner %q, want its own note", got)
	}
	all := strings.Join(slices.Collect(maps.Values(noteEntries(t))), "\n")
	for _, want := range []string{"owes us money", "met at the fair"} {
		if !strings.Contains(all, want) {
			t.Fatalf("notes.toml lost %q: %q", want, all)
		}
	}
}

// A notes.toml written before the notes had an account is read: its notes go
// to the first account known on their network, which writes them back under
// its id at once — the account of the cache, whose chats show at start, else
// the first one that connects. The entries of a network that never connects
// stay as they are, and a save keeps every entry.
func TestNoteOldFileAttached(t *testing.T) {
	for _, cached := range []bool{true, false} {
		start := noteStart(t, accountNet)
		old := "[notes]\n\"" + noteNet + ":2\" = \"written before the update\"\n\"discord:9\" = \"a network not connected here\"\n"
		if err := os.WriteFile(notesPath(), []byte(old), 0o600); err != nil {
			t.Fatal(err)
		}
		if cached { // the cache of the last session, written by account 111
			cc := cache.New(filepath.Join(config.CacheDir(), noteNet), 2000)
			if err := cc.SaveDialogs([]model.Chat{{ID: 2, Kind: model.ChatUser, Title: "alice", LastDate: time.Now()}}, 111); err != nil {
				t.Fatal(err)
			}
		}
		u := start()
		if cached {
			u.openChat(u.chats[model.ChatKey{Net: noteNet, ID: 2}]) // before EvReady
		} else {
			noteReady(u, 111)
			noteOpen(u, alice)
		}
		if got := noteBanner(u); !strings.Contains(got, "written before the update") {
			t.Fatalf("cache %v, account 111 known: banner %q, want the old note", cached, got)
		}
		if n := noteEntries(t); n["111@"+noteNet+":2"] != "written before the update" || n[noteNet+":2"] != "" {
			t.Fatalf("cache %v, account 111 known: notes.toml %v, want the old note under 111@%s:2", cached, n, noteNet)
		}
		noteOpen(u, model.Chat{ID: 3, Kind: model.ChatUser, Title: "bob"})
		input(u, "/note a new note") // a save of the whole file
		n := noteEntries(t)
		want := map[string]string{"111@" + noteNet + ":2": "written before the update", "111@" + noteNet + ":3": "a new note",
			"discord:9": "a network not connected here"}
		if len(n) != len(want) {
			t.Fatalf("cache %v: notes.toml %v, want %v", cached, n, want)
		}
		for k, v := range want {
			if n[k] != v {
				t.Fatalf("cache %v: notes.toml %q = %q, want %q (file %v)", cached, k, n[k], v, n)
			}
		}
	}
}

// Before the network answers, the client shows the chats of its cache: the
// notes shown on them are those of the account that wrote the cache.
func TestNoteOfCachedChatBeforeReady(t *testing.T) {
	start := noteStart(t, accountNet)
	file := "[notes]\n\"111@" + noteNet + ":2\" = \"note of account 111\"\n"
	if err := os.WriteFile(notesPath(), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	cc := cache.New(filepath.Join(config.CacheDir(), noteNet), 2000)
	for _, c := range []struct {
		self int64
		want string
	}{{111, "note of account 111"}, {222, ""}} {
		if err := cc.SaveDialogs([]model.Chat{{ID: 2, Kind: model.ChatUser, Title: "alice", LastDate: time.Now()}}, c.self); err != nil {
			t.Fatal(err)
		}
		u := start()
		u.openChat(u.chats[model.ChatKey{Net: noteNet, ID: 2}])
		if got := noteBanner(u); (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Fatalf("cache of account %d, before EvReady: banner %q, want %q", c.self, got, c.want)
		}
	}
}

// On IRC the self id comes from the nick: a note on a room belongs to the
// network and stays through a nick change. When the server negotiates
// another case mapping the room gets another id, and its note follows it.
func TestNoteOnIRCSurvivesNickAndRekey(t *testing.T) {
	ids := map[string]int64{"#go": 42}
	u := noteStart(t, func() model.Backend { return &nameNet{ids: ids} })()
	noteReady(u, 1001)
	noteOpen(u, model.Chat{ID: 42, Kind: model.ChatGroup, Title: "#go"})
	input(u, "/note the room of the project")
	noteReady(u, 2002) // reconnected as "me_": the nick was taken
	if got := noteBanner(u); !strings.Contains(got, "the room of the project") {
		t.Fatalf("after a nick change: banner %q", got)
	}
	ids["#go"] = 43
	noteReady(u, 2002)
	if c := u.view().Chat; c == nil || c.ID != 43 {
		t.Fatalf("setup: the window shows %+v, want the room at id 43", c)
	}
	if got := noteBanner(u); !strings.Contains(got, "the room of the project") {
		t.Fatalf("after the rekey: banner %q", got)
	}
	if n := noteEntries(t); len(n) != 1 || n[noteNet+":43"] != "the room of the project" {
		t.Fatalf("notes.toml after the rekey: %v, want the note under %s:43", n, noteNet)
	}
}
