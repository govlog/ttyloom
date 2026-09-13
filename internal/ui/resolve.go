package ui

import (
	"slices"
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

type lookup struct {
	query  string
	win    *Window
	bind   bool
	file   string
	wait   map[string]bool
	chats  []*model.Chat
	errors []string
}

func (u *UI) resolve(w *Window, name string, join bool, backends []model.Backend, bind bool, file string) {
	u.lookupID++
	id := u.lookupID
	req := &lookup{query: name, win: w, bind: bind, file: file, wait: map[string]bool{}}
	if u.lookups == nil {
		u.lookups = map[uint64]*lookup{}
	}
	u.lookups[id] = req
	for _, b := range backends {
		for net, live := range u.nets {
			if live == b {
				req.wait[net] = true
			}
		}
	}
	for net := range req.wait {
		u.nets[net].Resolve(u.netContext(net), name, join, id)
	}
}

// displayQuery : what a query may be shown as. "#room key" joins a channel
// with its key: the key has nothing to do on the screen, nor in the log of
// the window.
func displayQuery(q string) string {
	if strings.HasPrefix(q, "#") {
		name, _, _ := strings.Cut(q, " ")
		return name
	}
	return q
}

func (u *UI) chatResolved(e model.EvChat) {
	req := u.lookups[e.Request]
	if req == nil || !req.wait[u.dispatchNet] || req.query != e.Query {
		return
	}
	delete(req.wait, u.dispatchNet)
	if e.Err == "" && e.Chat != nil {
		req.chats = append(req.chats, e.Chat)
	} else {
		req.errors = append(req.errors, e.Err)
	}
	if len(req.wait) != 0 {
		return
	}
	delete(u.lookups, e.Request)
	w := req.win
	if w != u.agg && w != u.debug && !slices.Contains(u.ws.List, w) {
		return
	}
	if len(req.chats) == 0 {
		w.AddSys(i18n.T("resolve_failed", displayQuery(req.query), strings.Join(req.errors, "; ")))
		return
	}
	if len(req.chats) > 1 {
		var names []string
		for _, c := range req.chats {
			names = append(names, c.Net+": "+u.title(c))
		}
		w.AddSys(i18n.T("ambiguous", strings.Join(names, ", ")))
		return
	}
	c := u.remember(req.chats[0])
	u.listChat(c)
	switch {
	case req.file != "":
		u.sendFile(c, req.file, "")
	case req.bind:
		u.attach(w, c)
	default:
		u.setQuery(w, c)
	}
}
