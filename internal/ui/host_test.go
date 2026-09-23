package ui

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/cache"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
)

// modUI : launchUI with the fake module and a chan for the envelopes.
func modUI(nets ...string) (*UI, *fakeMod) {
	u, _ := launchUI(nil)
	m := &fakeMod{nets: nets}
	u.mods, u.envs, u.events = []module.Module{m}, make(chan model.Envelope, 8), make(chan model.Event, 8)
	u.launch = u.launchModule
	return u, m
}

// A network of a module starts through Launch, and the end of its Run
// reaches the UI as an EvStopped stamped with the network.
func TestLaunchModule(t *testing.T) {
	u, m := modUI("fake:a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // runBackend.Run gives back at once
	if b, err := u.launchModule(ctx, "fake:a"); err != nil || b == nil || !slices.Equal(m.launched, []string{"fake:a"}) {
		t.Fatalf("launch: %v %v %v", b, err, m.launched)
	}
	select {
	case env := <-u.envs:
		if _, ok := env.Ev.(model.EvStopped); !ok || env.Net != "fake:a" {
			t.Fatalf("event: %+v", env)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no EvStopped")
	}
}

// AddNetwork lists the network, makes its cache as the module says and
// starts it; RemoveNetwork takes all of it away.
func TestHostAddRemoveNetwork(t *testing.T) {
	u, m := modUI()
	u.caches = map[string]*cache.Cache{}
	u.cfg.Cache, u.cfg.CacheMessages = true, 100
	t.Setenv("TTYLOOM_DIR", t.TempDir()) // config.CacheDir() is $TTYLOOM_DIR/cache
	h := host{u}
	h.AddNetwork("fake:b")
	if !slices.Contains(u.netList, "fake:b") || u.nets["fake:b"] == nil || !slices.Equal(m.launched, []string{"fake:b"}) {
		t.Fatalf("add: list %v nets %v launched %v", u.netList, u.nets, m.launched)
	}
	if c := u.caches["fake:b"]; c == nil || !c.KeepOld {
		t.Fatalf("cache: %+v", c)
	}
	c := &model.Chat{Net: "fake:b", ID: 1, Title: "room"}
	u.chats, u.chatList = map[model.ChatKey]*model.Chat{c.Key(): c}, []*model.Chat{c}
	u.winFor(c)
	h.RemoveNetwork("fake:b")
	if slices.Contains(u.netList, "fake:b") || u.chats[c.Key()] != nil || u.ws.ForChat(c.Key()) >= 0 || u.caches["fake:b"] != nil {
		t.Fatalf("remove: list %v chats %v", u.netList, u.chats)
	}
}

// ContextNet : the network of the window's chat, else the /net filter, else
// the only network of the module.
func TestHostContextNet(t *testing.T) {
	u, _ := modUI("fake:a", "fake:b")
	u.netList = append(u.netList, "fake:a", "fake:b")
	h := host{u}
	if n := h.ContextNet(module.Win{}, "fake"); n != "" {
		t.Fatalf("two networks, no context: %q", n)
	}
	if n := h.ContextNet(module.Win{Chat: &model.Chat{Net: "fake:b"}}, "fake"); n != "fake:b" {
		t.Fatalf("chat: %q", n)
	}
	u.netFilter = "fake:a"
	if n := h.ContextNet(module.Win{}, "fake"); n != "fake:a" {
		t.Fatalf("filter: %q", n)
	}
	u.netFilter, u.netList = "", []string{"telegram", "fake:a"}
	if n := h.ContextNet(module.Win{}, "fake"); n != "fake:a" {
		t.Fatalf("single network: %q", n)
	}
}

// Do from another goroutine runs f on the UI's.
func TestHostDo(t *testing.T) {
	u, _ := modUI()
	ran := false
	go host{u}.Do(func() { ran = true })
	u.event(<-u.events)
	if !ran {
		t.Fatal("f not run")
	}
}

// A name a module claims goes to its networks alone: fakeMod claims "%x".
func TestResolversClaimed(t *testing.T) {
	u, _ := modUI("fake:a")
	u.nets["fake:a"], u.nets["telegram"] = &fakeBackend{caps: model.Caps{Resolve: true}}, &fakeBackend{caps: model.Caps{Resolve: true}}
	if got := u.resolversFor(nil, "%room"); len(got) != 1 || got[0] != u.nets["fake:a"] {
		t.Fatalf("claimed: %v", got)
	}
	if got := u.resolversFor(nil, "bob"); len(got) != 2 {
		t.Fatalf("free name: %v", got)
	}
}
