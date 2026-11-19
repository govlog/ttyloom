package ui

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
)

// Muted chats (/mute, /unmute): no bell, no notification, never hot. Their
// unread counts stay, dimmed. Local to ttyloom: the network is not told.
// A whole section of the sidebar (a Discord guild) can be muted by its key:
// its chats are muted, the ones that come later too, and it sorts last.

// mutedFile : TOML form of muted.toml — chats = ["<net>:<id>", …], keys
// written like aliases.toml; sections = ["discord:Gophers", …], the keys of
// sidebar.toml (sectionOf).
// ponytail: a section is known by its name, the only thing the UI has; a
// guild renamed loses its mute. Use the guild id if a module ever gives it.
type mutedFile struct {
	Chats    []string `toml:"chats"`
	Sections []string `toml:"sections"`
}

// chatMuted : c is muted by itself or with its section.
func chatMuted(chats map[model.ChatKey]bool, secs map[string]bool, c *model.Chat) bool {
	return c != nil && (chats[c.Key()] || secs[sectionOf(c)])
}

// isMuted : c is muted, by itself or with its section.
func (u *UI) isMuted(c *model.Chat) bool { return chatMuted(u.muted, u.mutedSecs, c) }

// mutedWin : w shows a muted chat.
func (u *UI) mutedWin(w *Window) bool { return u.isMuted(w.Chat) }

func mutedPath() string { return filepath.Join(config.Dir(), "muted.toml") }

// loadMuted gives the muted chats and the muted sections. A missing file
// mutes nothing; a key broken by hand is dropped. The maps come back non nil
// even on an error.
func loadMuted(path string) (map[model.ChatKey]bool, map[string]bool, error) {
	chats, secs := map[model.ChatKey]bool{}, map[string]bool{}
	var f mutedFile
	if err := loadTOML(path, &f); err != nil {
		return chats, secs, err
	}
	for _, s := range f.Chats {
		if k, ok := parseAliasKey(s); ok {
			chats[k] = true
		}
	}
	for _, s := range f.Sections {
		if s != "" {
			secs[s] = true
		}
	}
	return chats, secs, nil
}

func saveMuted(path string, muted map[model.ChatKey]bool, secs map[string]bool) error {
	f := mutedFile{Chats: []string{}, Sections: slices.Sorted(maps.Keys(secs))}
	for k := range muted {
		f.Chats = append(f.Chats, aliasKey(k))
	}
	slices.Sort(f.Chats) // a stable file: one mute moves one line
	return saveTOML(path, f)
}

// muteSections : the section keys /mute and /unmute take — the sections of
// the chats known, and the muted ones (their network may not be up yet).
func (u *UI) muteSections() []string {
	seen := maps.Clone(u.mutedSecs)
	if seen == nil {
		seen = map[string]bool{}
	}
	for _, c := range u.chatList {
		seen[sectionOf(c)] = true
	}
	return slices.Sorted(maps.Keys(seen))
}

// muteSection : /mute and /unmute of a whole section.
func (u *UI) muteSection(w *Window, sec string, on bool) {
	done, already := "unmute_done", "unmute_already"
	if on {
		done, already = "mute_done", "mute_already"
	}
	if u.mutedSecs[sec] == on {
		w.AddSys(i18n.T(already, i18n.T("mute_section", sec)))
		return
	}
	if u.mutedSecs == nil {
		u.mutedSecs = map[string]bool{}
	}
	if on {
		u.mutedSecs[sec] = true
		for _, x := range u.ws.List { // the pulses already on stop now
			if x.Chat != nil && sectionOf(x.Chat) == sec {
				x.Hot = false
			}
		}
	} else {
		delete(u.mutedSecs, sec)
	}
	if err := saveMuted(mutedPath(), u.muted, u.mutedSecs); err != nil {
		w.AddSys(i18n.T("mute_error", err))
	}
	w.AddSys(i18n.T(done, i18n.T("mute_section", sec)))
}

// muteCmd : /mute [name] and /unmute [name]. With no name, the chat of the
// window; /mute alone where there is none lists the muted chats and
// sections. An exact section key ("discord:Gophers", case and accents
// apart) names the section; anything else names a chat. A chat with that
// exact name comes first: /mute telegram stays the chat "Telegram".
func (u *UI) muteCmd(w *Window, name string, on bool) {
	c := w.Chat
	if exact, _, _ := matchNames(u.chatNames(), name); name != "" && len(exact) == 0 {
		n := render.Fold(strings.TrimSpace(name))
		for _, sec := range u.muteSections() {
			if render.Fold(sec) == n {
				u.muteSection(w, sec, on)
				return
			}
		}
	}
	if name != "" {
		var ambiguous bool
		if c, ambiguous = u.findChat(name, true); c == nil {
			if !ambiguous { // findChat already listed the candidates
				w.AddSys(i18n.T("mute_unknown", name))
			}
			return
		}
	}
	if c == nil {
		if !on {
			w.AddSys(i18n.T("usage_unmute"))
			return
		}
		names := slices.Collect(maps.Keys(u.mutedSecs))
		for k := range u.muted {
			if mc := u.chats[k]; mc != nil {
				names = append(names, u.title(mc))
			} else {
				names = append(names, aliasKey(k)) // its network is not up yet
			}
		}
		if len(names) == 0 {
			w.AddSys(i18n.T("mute_none"))
			return
		}
		slices.Sort(names)
		w.AddSys(i18n.T("mute_list", strings.Join(names, ", ")))
		return
	}
	k := c.Key()
	done, already := "unmute_done", "unmute_already"
	if on {
		done, already = "mute_done", "mute_already"
	}
	if sec := sectionOf(c); !on && !u.muted[k] && u.mutedSecs[sec] { // silent because of its section: say how to undo that
		w.AddSys(i18n.T("unmute_section", u.title(c), sec))
		return
	}
	if u.muted[k] == on {
		w.AddSys(i18n.T(already, u.title(c)))
		return
	}
	if u.muted == nil {
		u.muted = map[model.ChatKey]bool{}
	}
	if on {
		u.muted[k] = true
		if i := u.ws.ForChat(k); i >= 0 {
			u.ws.List[i].Hot = false // a pulse already on stops now
		}
	} else {
		delete(u.muted, k)
	}
	if err := saveMuted(mutedPath(), u.muted, u.mutedSecs); err != nil {
		w.AddSys(i18n.T("mute_error", err))
	}
	if sec := sectionOf(c); !on && u.mutedSecs[sec] { // its own mute is gone, the one of its section stays
		w.AddSys(i18n.T("unmute_done_section", u.title(c), sec))
		return
	}
	w.AddSys(i18n.T(done, u.title(c)))
}
