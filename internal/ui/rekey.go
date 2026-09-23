package ui

import (
	"maps"
	"path/filepath"
	"slices"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// rekeyChats moves the cache keys of a network whose chat ids come from their
// titles (model.ChatIDer) to the mapping the server negotiated — IRC, whose
// old keys folded ASCII only.
func (u *UI) rekeyChats(net string, idFor func(string) int64) {
	u.bgWait.Wait()
	aliasesChanged := false
	for _, c := range slices.Collect(maps.Values(u.chats)) {
		if c.Net != net {
			continue
		}
		old := c.Key()
		key := model.ChatKey{Net: net, ID: idFor(c.Title)}
		if old == key {
			continue
		}
		if cc := u.cacheFor(net); cc != nil {
			msgs, err := cc.LoadHistory(old.ID)
			if err != nil {
				u.status0(i18n.T("cache_error", err))
				continue
			}
			other, err := cc.LoadHistory(key.ID)
			if err != nil {
				u.status0(i18n.T("cache_error", err))
				continue
			}
			for i := range msgs {
				msgs[i].ChatID = key.ID
			}
			if err := cc.SaveHistory(key.ID, mergeHistory(other, msgs)); err != nil {
				u.status0(i18n.T("cache_error", err))
				continue
			}
			// Keep the old file as a recovery copy if migration is interrupted.
		}
		target := u.chats[key]
		if target == nil {
			target = c
			target.ID = key.ID
		}
		delete(u.chats, old)
		u.chats[key] = target
		for i, entry := range u.chatList {
			if entry == c {
				u.chatList[i] = target
			}
		}
		for _, w := range u.views() {
			if w.Chat == c {
				w.Chat = target
			}
			if w.Target == c {
				w.Target = target
			}
			for _, it := range w.Items {
				for _, m := range []*model.Msg{it.Msg, it.Echo} {
					if m != nil && m.Net == net {
						if m.ChatID == old.ID {
							m.ChatID = key.ID
						}
						if m.From != "" {
							m.FromID = idFor(m.From)
						}
					}
				}
			}
			w.Invalidate()
		}
		if u.cached[old] {
			delete(u.cached, old)
			u.cached[key] = true
		}
		if u.dirty[old] {
			delete(u.dirty, old)
			u.dirty[key] = true
		}
		if alias := u.aliases[old]; alias != "" {
			if u.aliases[key] == "" {
				u.aliases[key] = alias
			}
			delete(u.aliases, old)
			aliasesChanged = true
		}
		u.dialogsDirty = true
	}
	listed := map[model.ChatKey]bool{}
	u.chatList = slices.DeleteFunc(u.chatList, func(c *model.Chat) bool {
		key := c.Key()
		if listed[key] {
			return true
		}
		listed[key] = true
		return false
	})
	seen := map[model.ChatKey]*Window{}
	for i := 1; i < len(u.ws.List); {
		w := u.ws.List[i]
		if w.Chat == nil || w.Chat.Net != net || w.Search != "" {
			i++
			continue
		}
		key := w.Chat.Key()
		if first := seen[key]; first != nil {
			msgs := w.Msgs()
			for j := range msgs {
				first.Merge([]*model.Msg{&msgs[j]})
			}
			u.ws.CloseAt(i)
			u.markDirty(key)
		} else {
			seen[key] = w
			i++
		}
	}
	if aliasesChanged && u.cfg != nil && filepath.Dir(u.cfg.Path()) != "." {
		_ = saveAliases(aliasPath(), u.aliases)
	}
}
