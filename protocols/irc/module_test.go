package irc

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
)

// fakeHost : the Host of the module tests. Do runs f at once (the tests are
// the UI goroutine); saves counts SaveConfig.
type fakeHost struct {
	module.Host
	cfg     *config.Config
	saves   int
	form    *module.Form
	removed []string
	lines   []string
}

func (h *fakeHost) Do(f func())                  { f() }
func (h *fakeHost) SaveConfig() bool             { h.saves++; return h.cfg.Save() == nil }
func (h *fakeHost) OpenForm(f *module.Form)      { h.form = f }
func (h *fakeHost) RemoveNetwork(net string)     { h.removed = append(h.removed, net) }
func (h *fakeHost) Print(_ module.Win, l string) { h.lines = append(h.lines, l) }

func loadIRC(t *testing.T, body string) (*config.Config, *Module) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModule()
	cfg, err := config.LoadFrom(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, m
}

// Valid [[irc]] tables are networks; a bad name or a second table of the
// same name is reported unknown and left out.
func TestModuleNetworks(t *testing.T) {
	cfg, m := loadIRC(t, "[[irc]]\nname = \"libera\"\nhost = \"irc.libera.chat\"\nnick = \"me\"\n"+
		"[[irc]]\nname = \"Bad Name\"\n[[irc]]\nname = \"libera\"\n")
	if !slices.Equal(m.Networks(), []string{"irc:libera"}) {
		t.Fatalf("networks: %v", m.Networks())
	}
	if !slices.Contains(cfg.Unknown, "irc.Bad Name") || !slices.Contains(cfg.Unknown, "irc.libera") {
		t.Fatalf("unknown: %v", cfg.Unknown)
	}
	if sub, keep := m.Cache("irc:libera"); sub != "irc:libera" || !keep {
		t.Fatalf("cache: %q %v", sub, keep)
	}
}

// saveRooms : the backend is in the rooms of list, and asks for its list to
// be saved.
func saveRooms(b model.Backend, list ...string) {
	c := b.(*Client)
	c.mu.Lock()
	c.channels = list
	c.mu.Unlock()
	c.cfg.SaveChannels()
}

// The channels a backend saves go through Do, into the table, into the file.
func TestModuleSaveChannels(t *testing.T) {
	cfg, m := loadIRC(t, "[[irc]]\nname = \"oftc\"\nhost = \"irc.oftc.net\"\nnick = \"me\"\n")
	h := &fakeHost{cfg: cfg}
	b, err := m.Launch(context.Background(), h, "irc:oftc", make(chan model.Event, 8))
	if err != nil || b == nil {
		t.Fatalf("launch: %v", err)
	}
	saveRooms(b, "#debian")
	again := NewModule()
	if _, err := config.LoadFrom(filepath.Dir(cfg.Path()), again); err != nil || h.saves != 1 {
		t.Fatalf("saves %d, %v", h.saves, err)
	}
	if n := again.ByName("oftc"); n == nil || !slices.Equal(n.Channels, []string{"#debian"}) {
		t.Fatalf("channels in the file: %+v", n)
	}
}

// nickserv_password_cmd is read like the Discord token_cmd: the words are
// the argv (no shell), stdout trimmed, the first stderr line in the error;
// the plain key stays when there is no command.
func TestIRCPasswordCmd(t *testing.T) {
	n := &NetConfig{Name: "libera", NickServPassword: "plain"}
	if pw, err := n.Password(); err != nil || pw != "plain" {
		t.Fatalf("plain key: %q, %v", pw, err)
	}
	n.NickServPasswordCmd = "echo  s3cret "
	if pw, err := n.Password(); err != nil || pw != "s3cret" {
		t.Fatalf("command: %q, %v", pw, err)
	}
	n.NickServPasswordCmd = "cat /ttyloom-no-such-file"
	if _, err := n.Password(); err == nil || !strings.Contains(err.Error(), "No such file") || !strings.Contains(err.Error(), "nickserv_password_cmd") {
		t.Fatalf("stderr of the command missing: %v", err)
	}
	n.NickServPasswordCmd = "true"
	if pw, err := n.Password(); err == nil {
		t.Fatalf("empty output taken as a password: %q", pw)
	}
}

// The password command runs at Load, before the terminal goes raw (a
// pinentry on the tty), and the first launch takes that result; a later
// launch (/irc connect) runs it again.
func TestIRCPasswordCmdAtLoad(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	script := filepath.Join(dir, "pw.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho run >> "+runs+"\necho s3cret\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		b, _ := os.ReadFile(runs)
		return strings.Count(string(b), "run")
	}
	cfg, m := loadIRC(t, "[[irc]]\nname = \"libera\"\nhost = \"127.0.0.1\"\nnick = \"me\"\nnickserv_password_cmd = \""+script+"\"\n")
	if n := count(); n != 1 {
		t.Fatalf("%d runs at Load, want 1", n)
	}
	h := &fakeHost{cfg: cfg}
	b, err := m.Launch(context.Background(), h, "irc:libera", make(chan model.Event, 8))
	if err != nil || b.(*Client).cfg.Password != "s3cret" || count() != 1 {
		t.Fatalf("first launch: %v, %d runs; want the password read at Load", err, count())
	}
	if _, err := m.Launch(context.Background(), h, "irc:libera", make(chan model.Event, 8)); err != nil || count() != 2 {
		t.Fatalf("second launch: %v, %d runs; want the command run again", err, count())
	}
}

// Two [[irc]] tables read back, one with a bad name dropped and reported;
// Save keeps the valid ones and their channel list.
func TestLoadIRCTables(t *testing.T) {
	c, m := loadIRC(t, "api_id = 1\n[[irc]]\nname = \"libera\"\nhost = \"irc.libera.chat\"\nport = 6697\ntls = true\nnick = \"me\"\nchannels = [\"#go-nuts\"]\n"+
		"[[irc]]\nname = \"Bad Name\"\nhost = \"x\"\n[[irc]]\nname = \"oftc\"\nhost = \"irc.oftc.net\"\nport = 6667\n")
	if !slices.Equal(m.Networks(), []string{"irc:libera", "irc:oftc"}) {
		t.Fatalf("irc tables: %v", m.Networks())
	}
	if n := m.ByName("libera"); n == nil || n.Port != 6697 || !n.TLS || len(n.Channels) != 1 || n.Channels[0] != "#go-nuts" {
		t.Fatalf("libera: %+v", n)
	}
	if m.ByName("nope") != nil || !strings.Contains(strings.Join(c.Unknown, ","), "irc.Bad Name") {
		t.Fatalf("unknown: %v", c.Unknown)
	}
	m.ByName("oftc").Channels = []string{"#debian"}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	again := NewModule()
	if _, err := config.LoadFrom(filepath.Dir(c.Path()), again); err != nil {
		t.Fatal(err)
	}
	if n := again.ByName("oftc"); n == nil || len(n.Channels) != 1 || n.Channels[0] != "#debian" || len(again.Networks()) != 2 {
		t.Fatalf("saved irc tables: %v %+v", again.Networks(), n)
	}
}

// The ignore list of a network is read back from the file.
func TestIgnoresFromFile(t *testing.T) {
	_, m := loadIRC(t, "tabs = true\n[[irc]]\nname = \"libera\"\nhost = \"irc.libera.chat\"\nnick = \"me\"\nignores = [\"spammer!*@*\"]\n")
	if n := m.ByName("libera"); n == nil || len(n.Ignores) != 1 || n.Ignores[0] != "spammer!*@*" {
		t.Fatalf("ignores: %+v", n)
	}
}

func TestValidName(t *testing.T) {
	for name, ok := range map[string]bool{"libera": true, "my-net_2": true, "": false, "Libera": false, "a b": false, strings.Repeat("a", 33): false} {
		if ValidName(name) != ok {
			t.Errorf("ValidName(%q) = %v, want %v", name, !ok, ok)
		}
	}
}

// A network deleted stays deleted through a Save — a refused duplicate of its
// name goes with it, or the next start would take it as the network again —
// and the keys of the file no one takes stay.
func TestModuleDeleteKeepsUnknown(t *testing.T) {
	c, m := loadIRC(t, "future = \"kept\"\n[[irc]]\nname = \"libera\"\nhost = \"irc.libera.chat\"\n[[irc]]\nname = \"libera\"\nhost = \"irc.other.example\"\n")
	m.removeTable("libera")
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	again := NewModule()
	c2, err := config.LoadFrom(filepath.Dir(c.Path()), again)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Networks()) != 0 || !slices.Contains(c2.Unknown, "future") {
		t.Fatalf("networks %v, unknown %v", again.Networks(), c2.Unknown)
	}
}

// Launch builds the backend of a configured network and refuses an unknown
// one; the rooms the backend saves wait for Do, never touching the table
// from its goroutine.
func TestModuleLaunch(t *testing.T) {
	cfg, m := loadIRC(t, "[[irc]]\nname = \"libera\"\nhost = \"127.0.0.1\"\nport = 1\nnick = \"me\"\n[[irc]]\nname = \"oftc\"\nhost = \"127.0.0.1\"\nport = 1\nnick = \"me\"\n")
	queued := &queueHost{fakeHost: fakeHost{cfg: cfg}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b, err := m.Launch(ctx, queued, "irc:oftc", make(chan model.Event, 8))
	if err != nil || b == nil {
		t.Fatalf("launch: %v %v", b, err)
	}
	if _, err := m.Launch(ctx, queued, "irc:nope", make(chan model.Event, 8)); err == nil {
		t.Fatal("an unknown IRC network must not launch")
	}
	saveRooms(b, "#debian")
	if len(m.ByName("oftc").Channels) != 0 || len(queued.fns) != 1 {
		t.Fatalf("backend mutated the configuration: %v, %d queued", m.ByName("oftc").Channels, len(queued.fns))
	}
	queued.fns[0]()
	if !slices.Equal(m.ByName("oftc").Channels, []string{"#debian"}) {
		t.Fatalf("channels after Do: %v", m.ByName("oftc").Channels)
	}
}

// queueHost : Do keeps f for the test to run, like the UI loop would later.
type queueHost struct {
	fakeHost
	fns []func()
}

func (h *queueHost) Do(f func()) { h.fns = append(h.fns, f) }

// Two channel saves, run by the UI in the other order (the backend no longer
// holds its lock while it waits for the UI, so nothing orders them): the file
// keeps the latest list all the same.
func TestIRCChannelsPersistInOrder(t *testing.T) {
	cfg, m := loadIRC(t, "[[irc]]\nname = \"test\"\nhost = \"irc.example\"\nnick = \"me\"\n")
	queued := &queueHost{fakeHost: fakeHost{cfg: cfg}}
	b, err := m.Launch(context.Background(), queued, "irc:test", make(chan model.Event, 8))
	if err != nil {
		t.Fatal(err)
	}
	saveRooms(b, "#first")
	saveRooms(b, "#first", "#second")
	queued.fns[1]()
	queued.fns[0]()
	saved := NewModule()
	if _, err := config.LoadFrom(filepath.Dir(cfg.Path()), saved); err != nil {
		t.Fatal(err)
	}
	if got := saved.ByName("test").Channels; !slices.Equal(got, []string{"#first", "#second"}) {
		t.Fatalf("saved channels: %v, want the latest list", got)
	}
}

// The ignore list of a network goes back to its [[irc]] table, like the
// channels: the masks of /ignore survive a restart.
func TestIRCIgnoresPersist(t *testing.T) {
	cfg, m := loadIRC(t, "[[irc]]\nname = \"test\"\nhost = \"irc.example\"\nnick = \"me\"\n")
	b, err := m.Launch(context.Background(), &fakeHost{cfg: cfg}, "irc:test", make(chan model.Event, 8))
	if err != nil {
		t.Fatal(err)
	}
	cl := b.(*Client)
	cl.ignores = []string{"spammer!*@*"} // /ignore spammer
	cl.cfg.SaveIgnores()
	saved := NewModule()
	if _, err := config.LoadFrom(filepath.Dir(cfg.Path()), saved); err != nil {
		t.Fatal(err)
	}
	if got := saved.ByName("test").Ignores; len(got) != 1 || got[0] != "spammer!*@*" {
		t.Fatalf("saved ignores: %v", got)
	}
}

// The backend saves from its goroutines while the UI writes config.toml:
// Do brings every change to the one goroutine that owns the file (-race).
func TestIRCConfigCallbackRace(t *testing.T) {
	cfg, m := loadIRC(t, "[[irc]]\nname = \"audit\"\nhost = \"localhost\"\nnick = \"me\"\n")
	ch := make(chan func(), 100)
	h := &chanHost{fakeHost: fakeHost{cfg: cfg}, ch: ch}
	b, err := m.Launch(context.Background(), h, "irc:audit", make(chan model.Event, 100))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			saveRooms(b, "#one")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			saveRooms(b, "#two")
		}
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	for {
		select {
		case f := <-ch:
			cfg.Images = "off"
			f()
		case <-done:
			for len(ch) > 0 {
				(<-ch)()
			}
			return
		}
	}
}

// chanHost : Do sends f to the goroutine of the test, the UI of this test.
type chanHost struct {
	fakeHost
	ch chan func()
}

func (h *chanHost) Do(f func()) { h.ch <- f }

func TestNetName(t *testing.T) {
	if Net("libera") != "irc:libera" || Name("irc:libera") != "libera" {
		t.Fatal("irc:libera round trip")
	}
	if Name("irc") != "" || Name("irc:") != "" || Name("discord") != "" {
		t.Fatal("not an IRC network key")
	}
}

// IRC always adds a network: its page is the form of /irc add with a line of
// guide; Remove takes a table out of config.toml and the network away.
func TestIRCSetupAndRemove(t *testing.T) {
	cfg, m := loadIRC(t, "[[irc]]\nname = \"libera\"\nhost = \"irc.libera.chat\"\nnick = \"me\"\n")
	h := &fakeHost{cfg: cfg}
	if m.Label() != "IRC" || !m.CanAdd() {
		t.Fatalf("label %q, can add %v", m.Label(), m.CanAdd())
	}
	m.OpenSetup(h)
	if h.form == nil || len(h.form.Intro) != 1 || len(h.form.Fields) != 8 {
		t.Fatalf("form: %+v", h.form)
	}
	var r module.Remover = m
	r.Remove(h, module.Win{}, "irc:libera")
	again := NewModule()
	if _, err := config.LoadFrom(filepath.Dir(cfg.Path()), again); err != nil || len(again.Networks()) != 0 || !slices.Equal(h.removed, []string{"irc:libera"}) {
		t.Fatalf("after remove: %v %v %v", again.Networks(), h.removed, err)
	}
}
