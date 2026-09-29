package ui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/module"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// cfgFake : fakeMod with a section of its own, [fakenet] nets = […], and a
// /fk command.
type cfgFake struct {
	fakeMod
	Sec struct {
		Nets []string `toml:"nets"`
	}
}

func (m *cfgFake) Load(src module.ConfigSource) error {
	_, err := src.Decode("fakenet", &m.Sec)
	return err
}
func (m *cfgFake) Save(dst module.ConfigSink) { dst.Set("fakenet", m.Sec) }
func (m *cfgFake) Networks() []string         { return m.Sec.Nets }
func (m *cfgFake) Commands() []module.Command {
	return []module.Command{{Name: "fk", Help: module.Topic{Key: "help_fk", Section: "chats"},
		Run: func(h module.Host, w module.Win, _ []string, text string) { h.Print(w, "fk:"+text) }}}
}

// The fake module goes the whole way the real ones go: its section of
// config.toml, its network started at start, its command, its help, and its
// section written back.
func TestFakeNetEndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TTYLOOM_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[fakenet]\nnets = [\"fake:a\"]\n"), 0o600); err != nil {
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
	if !slices.Contains(u.netList, "fake:a") || !slices.Equal(m.launched, []string{"fake:a"}) {
		t.Fatalf("start: %v %v", u.netList, m.launched)
	}
	u.command("fk", []string{"hi"}, "hi")
	if last := u.view().Items[len(u.view().Items)-1].Sys; !strings.HasSuffix(last, "fk:hi") {
		t.Fatalf("command: %q", last)
	}
	if !slices.Contains(helpCandidates(u.topics()), "fk") {
		t.Fatal("help misses /fk")
	}
	m.Sec.Nets = append(m.Sec.Nets, "fake:b")
	if !u.saveCfg() {
		t.Fatal("save")
	}
	m2 := &cfgFake{}
	if _, err := config.LoadFrom(dir, m2); err != nil || !slices.Equal(m2.Networks(), []string{"fake:a", "fake:b"}) {
		t.Fatalf("section written back: %v %v", m2.Networks(), err)
	}
}

// With no network, the client starts on the hub, its welcome shown.
func TestStartWithoutNetwork(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TTYLOOM_DIR", dir)
	m := &cfgFake{}
	m.canAdd = true
	cfg, err := config.LoadFrom(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	u := newUI(ctx, cancel, term.NewOffscreen(io.Discard, 80, 24), cfg, theme.Terminal(), []module.Module{m})
	if u.hub == nil || !u.hub.welcome {
		t.Fatalf("hub at start: %+v", u.hub)
	}
	u.hubKey(term.Key{Code: term.Esc})
	if u.hub != nil || !strings.Contains(u.ws.List[0].Items[len(u.ws.List[0].Items)-1].Sys, i18n.T("hub_none")) {
		t.Fatal("closing with no network must say /networks")
	}
}

// restartable gives a start function for a client with the fake network
// fake:a, all its files in one directory: each call is a new session on them.
func restartable(t *testing.T) func() *UI {
	dir := t.TempDir()
	t.Setenv("TTYLOOM_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[fakenet]\nnets = [\"fake:a\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return func() *UI {
		m := &cfgFake{}
		cfg, err := config.LoadFrom(dir, m)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		return newUI(ctx, cancel, term.NewOffscreen(io.Discard, 80, 24), cfg, theme.Terminal(), []module.Module{m})
	}
}

// F2 and F3 are kept in config.toml: the next start opens the sidebar in the
// mode it was left in, and the member box with it.
func TestPanelsKeptAcrossRestart(t *testing.T) {
	start := restartable(t)
	u := start()
	if u.side != sideHidden || u.partsOn {
		t.Fatalf("first start: side %d, member box %v", u.side, u.partsOn)
	}
	u.key(term.Key{Code: term.F2})
	u.key(term.Key{Code: term.F2}) // windows mode
	u.key(term.Key{Code: term.F3})
	if u = start(); u.side != sideWindows || !u.partsOn {
		t.Fatalf("after restart: side %d, member box %v", u.side, u.partsOn)
	}
	u.key(term.Key{Code: term.F2}) // hidden again
	u.key(term.Key{Code: term.F3})
	if u = start(); u.side != sideHidden || u.partsOn {
		t.Fatalf("after the second restart: side %d, member box %v", u.side, u.partsOn)
	}
}
