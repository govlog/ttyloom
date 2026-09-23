package ui

import (
	"context"
	"strings"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
)

// fakeMod : a network module that exists only in the tests. It proves that
// the client reaches a network through the module boundary alone.
type fakeMod struct {
	nets     []string
	launched []string
	cmds     []module.Command
}

func (m *fakeMod) Name() string                    { return "fake" }
func (m *fakeMod) Load(module.ConfigSource) error  { return nil }
func (m *fakeMod) Save(module.ConfigSink)          {}
func (m *fakeMod) Template() string                { return "" }
func (m *fakeMod) Networks() []string              { return m.nets }
func (m *fakeMod) Cache(net string) (string, bool) { return net, true }
func (m *fakeMod) Commands() []module.Command      { return m.cmds }
func (m *fakeMod) Claims(name string) bool         { return strings.HasPrefix(name, "%") }
func (m *fakeMod) Launch(_ context.Context, _ module.Host, net string, _ chan<- model.Event) (model.Backend, error) {
	m.launched = append(m.launched, net)
	return &runBackend{}, nil
}

// runBackend : a fakeBackend whose Run lasts until its context ends, like a
// connected network.
type runBackend struct{ fakeBackend }

func (*runBackend) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
