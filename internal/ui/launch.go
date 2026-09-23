package ui

import (
	"context"
	"path/filepath"

	"github.com/govlog/ttyloom/internal/cache"
	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
)

// modOf : the module of a network, nil when no module has that name.
func (u *UI) modOf(net string) module.Module {
	name := model.NetModule(net)
	for _, m := range u.mods {
		if m.Name() == name {
			return m
		}
	}
	return nil
}

// addCache makes the disk cache of net where its module says, when the
// cache is on and net has none yet.
func (u *UI) addCache(m module.Module, net string) {
	if !u.cfg.Cache || u.caches == nil || u.caches[net] != nil {
		return
	}
	sub, keepOld := m.Cache(net)
	c := cache.New(filepath.Join(config.CacheDir(), sub), u.cfg.CacheMessages)
	c.KeepOld = keepOld
	u.caches[net] = c
}

// launchModule builds the backend of net through its module and runs it: its
// own chan, the forwarder that stamps the network on every event, and the
// goroutine of its Run — an error or a panic comes back as an event rather
// than killing the terminal. At start for each network, then at /<net> login.
func (u *UI) launchModule(nctx context.Context, net string) (model.Backend, error) {
	raw := make(chan model.Event, 256)
	b, err := u.modOf(net).Launch(nctx, host{u}, net, raw)
	if err != nil {
		return nil, err
	}
	if p, ok := b.(interface{ SetContext(context.Context) }); ok {
		p.SetContext(nctx)
	}
	go func() {
		for {
			select {
			case <-nctx.Done():
				return
			case ev := <-raw:
				select {
				case u.envs <- model.Envelope{Net: net, Ev: ev, Session: nctx}:
				case <-nctx.Done():
					return
				}
			}
		}
	}()
	go func() {
		var stopped model.EvStopped
		// Straight into the UI chan, not through raw: when the context that
		// ended Run is the one of a logout, the forwarder is already gone.
		defer func() {
			if r := recover(); r != nil {
				stopped.Err = i18n.T("panic", r)
			}
			select {
			case u.envs <- model.Envelope{Net: net, Ev: stopped, Session: nctx}:
			case <-u.ctx.Done():
			}
		}()
		if err := b.Run(nctx); err != nil && nctx.Err() == nil {
			stopped.Err = err.Error()
		}
	}()
	return b, nil
}
