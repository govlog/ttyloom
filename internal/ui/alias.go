package ui

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
)

// aliasMax : longest local name, in cells.
const aliasMax = 64

// aliasFile : TOML form of aliases.toml — [aliases] "<chatID>" = "name".
type aliasFile struct {
	Aliases map[string]string `toml:"aliases"`
}

// aliasKey encodes a ChatKey for aliases.toml: plain "<id>" for telegram
// (files written before phase 1 keep working), "<net>:<id>" otherwise.
func aliasKey(k model.ChatKey) string {
	if k.Net == model.NetTelegram {
		return strconv.FormatInt(k.ID, 10)
	}
	return k.Net + ":" + strconv.FormatInt(k.ID, 10)
}

func parseAliasKey(s string) (model.ChatKey, bool) {
	if net, id, ok := strings.Cut(s, ":"); ok {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || net == "" { // ":42" edited by hand: never build a key with no net
			return model.ChatKey{}, false
		}
		return model.ChatKey{Net: net, ID: n}, true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return model.ChatKey{}, false
	}
	return model.ChatKey{Net: model.NetTelegram, ID: n}, true
}

// aliasPath gives aliases.toml, beside config.toml (TTYLOOM_DIR included).
func aliasPath() string { return filepath.Join(config.Dir(), "aliases.toml") }

// loadAliases gives the local names by chat. A missing file = no alias,
// not an error; an unreadable line (file edited by hand) is ignored.
func loadAliases(path string) (map[model.ChatKey]string, error) {
	out := map[model.ChatKey]string{}
	var f aliasFile
	if _, err := toml.DecodeFile(path, &f); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return out, err
	}
	for k, v := range f.Aliases {
		key, ok := parseAliasKey(k)
		// Same boundary as at input time: the file can be edited by hand, so a
		// dirty or too long value is ignored, never fatal.
		v = strings.TrimSpace(render.CleanLine(v))
		if !ok || v == "" || render.Width(v) > aliasMax {
			continue
		}
		out[key] = v
	}
	return out, nil
}

// saveAliases writes the whole file again: an alias dropped from the map goes away.
func saveAliases(path string, al map[model.ChatKey]string) error {
	f := aliasFile{Aliases: make(map[string]string, len(al))}
	for k, name := range al {
		f.Aliases[aliasKey(k)] = name
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(f); err != nil {
		return err
	}
	return config.WriteAtomic(path, buf.Bytes(), 0o600)
}

// title gives the shown name of a chat — the local name (/rename), else the
// Telegram title. Single point of passage: sidebar, status bar, window name,
// [chat] prefix of the aggregate, completion, notifications, members, menu.
func (u *UI) title(c *model.Chat) string {
	if c == nil {
		return ""
	}
	if a := u.aliases[c.Key()]; a != "" {
		return a
	}
	return c.Title
}

// aliasOf gives the local name for the message drawing (render.Opts.Alias);
// "" with no alias, and the drawing then keeps ChatLabel and the first name.
// It takes the message and not a bare id: the aggregated view mixes networks.
func (u *UI) aliasOf(m *model.Msg) string { return u.aliases[m.Key()] }

// chatTitle gives title(c) when the caller hands one over, else the Telegram
// title (pure functions called with no UI, tests).
func chatTitle(c *model.Chat, title func(*model.Chat) string) string {
	if title != nil {
		return title(c)
	}
	return c.Title
}

// winName : Window.Name() with the local name. Window.Name() stays the
// Telegram title: the log file (/log) does not change name on a rename.
func winName(w *Window, title func(*model.Chat) string) string {
	if w.Chat == nil || w.Search != "" {
		return w.Name()
	}
	return chatTitle(w.Chat, title)
}

func (u *UI) winName(w *Window) string { return winName(w, u.title) }

// setAlias sets (name not empty) or drops the local name of c, saves it and
// invalidates the views: the titles are copied into the lines already drawn.
func (u *UI) setAlias(c *model.Chat, name string) {
	old := u.title(c)
	if name == "" {
		delete(u.aliases, c.Key())
	} else {
		u.aliases[c.Key()] = name
	}
	if err := saveAliases(aliasPath(), u.aliases); err != nil {
		u.sys(i18n.T("aliases_error", err))
	}
	u.clear()
	u.sys(i18n.T("renamed", render.CleanLine(old), render.CleanLine(u.title(c))))
}

// unquote drops the surrounding quotes of a string.
func unquote(s string) string {
	if len(s) > 1 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		return s[1 : len(s)-1]
	}
	return s
}

// cutTarget gives the target (first word, or a quoted string) and the rest —
// "\"Friends - Blabla\" Blabla" → target "Friends - Blabla", rest "Blabla".
func cutTarget(text string) (target, rest string) {
	if strings.HasPrefix(text, `"`) {
		if i := strings.Index(text[1:], `"`); i >= 0 {
			return text[1 : i+1], strings.TrimSpace(text[i+2:])
		}
	}
	target, rest, _ = strings.Cut(text, " ")
	return target, strings.TrimSpace(rest)
}

// rename : /rename <new name> on the current window, /rename <target> <new
// name> elsewhere. As soon as text follows the first word, that word is a
// target: when it is unknown the command refuses instead of renaming the
// current window with the whole line. A name (or a target) of several words
// goes between quotes.
func (u *UI) rename(w *Window, text string) {
	if text == "" {
		w.AddSys(i18n.T("usage_rename"))
		return
	}
	target, rest := cutTarget(text)
	c, name := w.Chat, target
	if rest != "" {
		t, ambiguous := u.findChat(target)
		if ambiguous {
			return // findChat has already listed the candidates
		}
		if t == nil {
			w.AddSys(i18n.T("rename_unknown_target", target))
			return
		}
		c, name = t, unquote(rest)
	}
	if c == nil {
		w.AddSys(i18n.T("rename_window_not_bound"))
		return
	}
	name = strings.TrimSpace(render.CleanLine(name))
	if name == "" {
		w.AddSys(i18n.T("usage_rename"))
		return
	}
	if render.Width(name) > aliasMax {
		w.AddSys(i18n.T("name_too_long", aliasMax))
		return
	}
	u.setAlias(c, name)
}

// unrename : /unrename [target] — with no target, the chat of the window.
func (u *UI) unrename(w *Window, target string) {
	c := w.Chat
	target = unquote(target)
	if target != "" {
		var ambiguous bool
		if c, ambiguous = u.findChat(target); c == nil {
			if !ambiguous {
				w.AddSys(i18n.T("unknown_name", target))
			}
			return
		}
	}
	if c == nil {
		w.AddSys(i18n.T("usage_unrename"))
		return
	}
	if u.aliases[c.Key()] == "" {
		w.AddSys(i18n.T("no_local_name", render.CleanLine(c.Title)))
		return
	}
	u.setAlias(c, "")
}
