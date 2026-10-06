package ui

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// Muted chats (/mute, /unmute): no bell, no notification, never hot. Their
// unread counts stay, dimmed. Local to ttyloom: the network is not told.

// mutedFile : TOML form of muted.toml — chats = ["<net>:<id>", …], keys
// written like aliases.toml.
type mutedFile struct {
	Chats []string `toml:"chats"`
}

// mutedWin : w shows a muted chat.
func (u *UI) mutedWin(w *Window) bool { return w.Chat != nil && u.muted[w.Chat.Key()] }

func mutedPath() string { return filepath.Join(config.Dir(), "muted.toml") }

// loadMuted gives the muted chats. A missing file mutes nothing; a key
// broken by hand is dropped. The map comes back non nil even on an error.
func loadMuted(path string) (map[model.ChatKey]bool, error) {
	out := map[model.ChatKey]bool{}
	var f mutedFile
	if _, err := toml.DecodeFile(path, &f); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return out, err
	}
	for _, s := range f.Chats {
		if k, ok := parseAliasKey(s); ok {
			out[k] = true
		}
	}
	return out, nil
}

func saveMuted(path string, muted map[model.ChatKey]bool) error {
	f := mutedFile{Chats: []string{}}
	for k := range muted {
		f.Chats = append(f.Chats, aliasKey(k))
	}
	slices.Sort(f.Chats) // a stable file: one mute moves one line
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(f); err != nil {
		return err
	}
	return config.WriteAtomic(path, buf.Bytes(), 0o600)
}

// muteCmd : /mute [name] and /unmute [name]. With no name, the chat of the
// window; /mute alone where there is none lists the muted chats.
func (u *UI) muteCmd(w *Window, name string, on bool) {
	c := w.Chat
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
		var names []string
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
	if err := saveMuted(mutedPath(), u.muted); err != nil {
		w.AddSys(i18n.T("mute_error", err))
	}
	w.AddSys(i18n.T(done, u.title(c)))
}
