package ui

import (
	"slices"
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// inputChat is the recipient shown by the input mode, without creating a
// window or emitting diagnostics. Replies and edits belong to their message;
// otherwise a pending lookup blocks the old target until it resolves.
func (u *UI) inputChat(w *Window) *model.Chat {
	if it := u.edit; it != nil && it.Msg != nil {
		return u.chatOf(it.Msg)
	}
	if it := u.reply; it != nil && it.Msg != nil {
		return u.chatOf(it.Msg)
	}
	if u.queryPending(w) != "" {
		return nil
	}
	if w.Target != nil {
		return w.Target
	}
	if w == u.agg {
		return u.aggTarget()
	}
	return w.Chat
}

// query changes the send target of this view. Its feed and base conversation
// stay in place, including the aggregate, status and search windows.
func (u *UI) query(w *Window, name string, join bool) {
	name = unquote(strings.TrimSpace(name))
	if name == "" {
		closing := u.queryPending(w)
		if w.Target != nil {
			closing = u.title(w.Target)
		}
		u.cancelQuery(w)
		w.Target = nil
		u.cancelMode()
		if closing != "" {
			w.AddEvent(i18n.T("query_closed", closing))
		}
		return
	}
	if c, ambiguous := u.findChat(name); c != nil {
		u.cancelQuery(w)
		u.setQuery(w, c)
		return
	} else if ambiguous {
		return
	}
	resolvers := u.resolversFor(w, name)
	if len(resolvers) == 0 {
		w.AddSys(i18n.T("net_unsupported", strings.Join(u.netNames(), ", ")))
		return
	}
	u.cancelQuery(w)
	if u.queryWait == nil {
		u.queryWait = map[string][]*Window{}
	}
	_, waiting := u.queryWait[name]
	u.queryWait[name] = append(u.queryWait[name], w)
	u.cancelMode()
	w.AddSys(i18n.T("resolving", name))
	if !waiting {
		for _, b := range resolvers {
			b.Resolve(u.ctx, name, join)
		}
	}
}

func (u *UI) setQuery(w *Window, c *model.Chat) {
	if w == u.view() {
		u.cancelMode() // a reply or edit still aimed at the old chat must end
	}
	if w.Target != nil {
		if w.Target.Key() == c.Key() {
			return
		}
		w.AddEvent(i18n.T("query_closed", u.title(w.Target)))
	}
	w.Target = c
	u.listChat(c)
	u.winFor(c) // receipts and incoming messages have a real chat window
	w.AddEvent(i18n.T("query_target", u.title(c)))
}

func (u *UI) cancelQuery(w *Window) {
	for name, windows := range u.queryWait {
		// Keep an empty entry until the answer arrives: it belongs to a
		// cancelled target and must never fall through to the old bind path.
		u.queryWait[name] = slices.DeleteFunc(windows, func(x *Window) bool { return x == w })
	}
}

func (u *UI) queryPending(w *Window) string {
	for name, windows := range u.queryWait {
		if slices.Contains(windows, w) {
			return name
		}
	}
	return ""
}

func (u *UI) queryResolved(e model.EvChat) bool {
	windows, ok := u.queryWait[e.Query]
	if !ok {
		return false
	}
	delete(u.queryWait, e.Query)
	windows = slices.DeleteFunc(windows, func(w *Window) bool {
		return w != u.agg && w != u.debug && !slices.Contains(u.ws.List, w)
	})
	if len(windows) == 0 {
		return true
	}
	if e.Err != "" || e.Chat == nil {
		for _, w := range windows {
			w.AddSys(i18n.T("resolve_failed", e.Query, e.Err))
		}
		return true
	}
	c := u.remember(e.Chat)
	for _, w := range windows {
		u.setQuery(w, c)
	}
	return true
}

func (u *UI) dropTargets(stale func(*model.Chat) bool) {
	for _, w := range append(u.views(), u.debug) {
		if w != nil && w.Target != nil && stale(w.Target) {
			w.Target = nil
		}
	}
}
