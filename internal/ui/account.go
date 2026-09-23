package ui

import (
	"path/filepath"
	"slices"

	"github.com/govlog/ttyloom/internal/model"
)

// clearNetWork drops work whose replies can no longer arrive from this session.
func (u *UI) clearNetWork(net string) {
	for id, req := range u.lookups {
		owned := req.wait[net]
		for _, c := range req.chats {
			owned = owned || c.Net == net
		}
		if owned {
			delete(u.lookups, id)
		}
	}
	u.syncQueue = slices.DeleteFunc(u.syncQueue, func(c *model.Chat) bool { return c.Net == net })
	if u.syncCur != nil && u.syncCur.Net == net {
		u.syncCur = nil
	}
	for _, w := range u.ws.List {
		if w.Chat != nil && w.Chat.Net == net {
			w.Loading, w.Loaded = false, false
			for _, it := range w.Items {
				if it.Msg != nil && it.Msg.Media != nil && it.Msg.Media.State == model.MediaLoading && it.Msg.Media.Path == "" {
					it.Msg.Media.State = model.MediaNone
				}
			}
		}
	}
	u.contacts = slices.DeleteFunc(u.contacts, func(c *model.Chat) bool { return c.Net == net })
	u.gotContacts = false
	u.gsearch, u.newChat = nil, nil
	if u.gifs != nil && u.gifs.chat.Net == net {
		u.gifClose()
	}
	for k := range u.partsCache {
		if k.Net == net {
			delete(u.partsCache, k)
		}
	}
	for k := range u.typing {
		if k.Net == net {
			delete(u.typing, k)
		}
	}
	for k := range u.lastTyping {
		if k.Net == net {
			delete(u.lastTyping, k)
		}
	}
	u.syncNext()
}

// clearAccount also removes runtime data, including messages shared by views.
func (u *UI) clearAccount(net string) {
	u.clearNetWork(net)
	u.closeViewer()
	u.cancelMode()
	u.picker, u.menu, u.mention, u.spellFix, u.pager = nil, nil, nil, nil, nil
	u.parts, u.who, u.completion = nil, nil, nil
	u.hits, u.placed = nil, nil
	u.cycleHome = nil
	if u.authDraft != nil {
		u.authDraft = &Editor{}
	}
	if u.prompt == nil {
		u.ed = Editor{}
	}
	for _, w := range append(u.views(), u.debug) {
		if w == nil {
			continue
		}
		if (w.Chat != nil && w.Chat.Net == net) || (w.Target != nil && w.Target.Net == net) {
			w.Draft = ""
		}
		w.Items = slices.DeleteFunc(w.Items, func(it *Item) bool {
			if it.Echo != nil && it.Echo.Net == net {
				return true
			}
			if it.Msg == nil || it.Msg.Net != net {
				return false
			}
			if md := it.Msg.Media; md != nil {
				delete(u.openNext, md)
				u.dropFrames(md)
			}
			return true
		})
		if w.Sel != nil && w.Sel.Msg != nil && w.Sel.Msg.Net == net {
			w.Sel = nil
		}
	}
	for k, md := range u.avatars {
		if k.Net == net {
			if md != nil {
				u.dropFrames(md)
			}
			delete(u.avatars, k)
		}
	}
	for k := range u.presence {
		if k.Net == net {
			delete(u.presence, k)
		}
	}
	for k := range u.whoCache {
		if k.net == net {
			delete(u.whoCache, k)
		}
	}
	changed := false
	for k := range u.aliases {
		if k.Net == net {
			delete(u.aliases, k)
			changed = true
		}
	}
	if changed && u.cfg != nil && filepath.Dir(u.cfg.Path()) != "." {
		_ = saveAliases(aliasPath(), u.aliases)
	}
	for _, md := range u.customs {
		u.dropFrames(md)
	}
	u.customs = nil
	delete(u.reactList, net)
	delete(u.dialogsSeen, net)
	u.dialogsDirty = true
}
