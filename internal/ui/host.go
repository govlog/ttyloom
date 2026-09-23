package ui

import (
	"context"
	"slices"
	"strings"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
)

// host : the UI as the modules see it (module.Host).
type host struct{ u *UI }

// evDo : a function a backend goroutine sends to the goroutine of the UI.
type evDo struct{ f func() }

// win : the module view of a window.
func (u *UI) win(w *Window) module.Win {
	if w == nil {
		return module.Win{}
	}
	return module.Win{Chat: w.Chat, Target: w.Target, Ref: w}
}

// window : the UI window of a module.Win — its Ref, else the window of its
// chat, else nil.
func (u *UI) window(w module.Win) *Window {
	if win, ok := w.Ref.(*Window); ok && win != nil {
		return win
	}
	if w.Chat != nil {
		if i := u.ws.ForChat(w.Chat.Key()); i >= 0 {
			return u.ws.List[i]
		}
	}
	return nil
}

func (h host) Print(w module.Win, line string) {
	if win := h.u.window(w); win != nil {
		win.AddSys(line)
		return
	}
	h.u.sys(line)
}

func (h host) Status(line string) { h.u.status0(line) }

func (h host) NetAction(w module.Win, net, sub string) {
	win := h.u.window(w)
	if win == nil {
		win = h.u.view()
	}
	h.u.netAction(win, net, sub)
}

func (h host) AddNetwork(net string) {
	u := h.u
	if !slices.Contains(u.netList, net) {
		u.netList = append(u.netList, net)
		slices.Sort(u.netList)
	}
	if m := u.modOf(net); m != nil {
		u.addCache(m, net)
	}
	u.startNet(net)
}

// RemoveNetwork : the network stops, its chats and windows go, and the list
// forgets it. Its live maps empty at the EvStopped of its Run, like a
// disconnect.
// ponytail: the cache directory of the network stays on disk; remove it the
// day a stale chat list at a re-add under the same name bothers someone.
func (h host) RemoveNetwork(net string) {
	u := h.u
	if u.nets[net] != nil {
		u.stopNet(net, false)
	}
	u.netList = slices.DeleteFunc(u.netList, func(n string) bool { return n == net })
	if u.netFilter == net {
		u.setNetFilter("")
	}
	delete(u.caches, net)
	for _, c := range slices.Clone(u.chatList) { // dropChat edits u.chatList
		if c.Net == net {
			u.dropChat(c.Key())
		}
	}
	u.goTo(u.ws.Cur) // windows closed: the current one has moved
}

func (h host) Backend(net string) model.Backend   { return h.u.nets[net] }
func (h host) Context(net string) context.Context { return h.u.netContext(net) }

func (h host) ContextNet(w module.Win, mod string) string {
	for _, c := range []*model.Chat{w.Chat, w.Target} {
		if c != nil && model.NetModule(c.Net) == mod {
			return c.Net
		}
	}
	if h.u.netFilter != "" && model.NetModule(h.u.netFilter) == mod { // the tab (or /net) names the network
		return h.u.netFilter
	}
	var mine []string
	for _, n := range h.u.netList {
		if model.NetModule(n) == mod {
			mine = append(mine, n)
		}
	}
	if len(mine) == 1 {
		return mine[0]
	}
	return ""
}

func (h host) SaveConfig() bool { return h.u.saveCfg() }

func (h host) Chats(net string) []*model.Chat {
	var out []*model.Chat
	for _, c := range h.u.chatList {
		if c.Net == net {
			out = append(out, c)
		}
	}
	return out
}

// ChatByTitle : the chat of net whose title is name, case apart.
func (h host) ChatByTitle(net, name string) *model.Chat {
	u := h.u
	if b, ok := u.nets[net].(model.ChatIDer); ok {
		return u.chats[model.ChatKey{Net: net, ID: b.ChatID(name)}]
	}
	for _, c := range u.chatList {
		if c.Net == net && strings.EqualFold(c.Title, name) {
			return c
		}
	}
	return nil
}

func (h host) SendFile(c *model.Chat, path string) { h.u.sendFile(c, path, "") }

func (h host) Resolve(w module.Win, name, net, sendPath string) {
	win := h.u.window(w)
	if win == nil {
		win = h.u.view()
	}
	h.u.resolve(win, name, false, []model.Backend{h.u.nets[net]}, false, sendPath)
}

func (h host) Download(m *model.Msg) { h.u.download(m) }

// LastIncomingFile : the last incoming file of w not fetched yet.
func (h host) LastIncomingFile(w module.Win) *model.Msg {
	win := h.u.window(w)
	if win == nil {
		return nil
	}
	for i := len(win.Items) - 1; i >= 0; i-- {
		m := win.Items[i].Msg
		if m != nil && !m.Out && m.Media != nil && m.Media.Kind == model.MediaFile &&
			m.Media.State != model.MediaReady && m.Media.State != model.MediaLoading {
			return m
		}
	}
	return nil
}

func (h host) OpenForm(f *module.Form) { h.u.form = newFormBox(f) }

func (h host) Do(f func()) {
	select {
	case h.u.events <- evDo{f}:
	case <-h.u.ctx.Done():
	}
}

var _ module.Host = host{}
