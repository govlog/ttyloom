package ui

import (
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
	u.cancelMode()
	w.AddSys(i18n.T("resolving", name))
	u.resolve(w, name, join, resolvers, false, "")
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
	for id, req := range u.lookups {
		if req.win == w && !req.bind && req.file == "" {
			delete(u.lookups, id)
		}
	}
}

func (u *UI) queryPending(w *Window) string {
	for _, req := range u.lookups {
		if req.win == w && !req.bind && req.file == "" {
			return req.query
		}
	}
	return ""
}

func (u *UI) dropTargets(stale func(*model.Chat) bool) {
	for _, w := range append(u.views(), u.debug) {
		if w != nil && w.Target != nil && stale(w.Target) {
			w.Target = nil
		}
	}
}
