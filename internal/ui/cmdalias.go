package ui

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
)

// Command aliases (/alias, /unalias): "/name rest" runs the text of the
// alias, rest appended after a space. A text that is no command goes to the
// current chat as a message. Not to be mixed up with the local names of
// /rename (alias.go, aliases.toml).

// commandsFile : TOML form of commands.toml — [alias] name = "text".
type commandsFile struct {
	Alias map[string]string `toml:"alias"`
}

// reCmdAlias : a name the input can type after "/" — lower case, as
// ParseCommand reads every command.
var reCmdAlias = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func cmdAliasPath() string { return filepath.Join(config.Dir(), "commands.toml") }

// loadCmdAliases gives the aliases of the file; a missing file holds none,
// and a name broken by hand is dropped. The map comes back non nil even on an
// error.
func loadCmdAliases(path string) (map[string]string, error) {
	out := map[string]string{}
	var f commandsFile
	if _, err := toml.DecodeFile(path, &f); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return out, err
	}
	for name, text := range f.Alias {
		if name = strings.ToLower(name); reCmdAlias.MatchString(name) && text != "" {
			out[name] = text
		}
	}
	return out, nil
}

// saveCmdAliases writes the file 0600: an alias can hold a password
// (/msg nickserv identify …).
func saveCmdAliases(path string, aliases map[string]string) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(commandsFile{Alias: aliases}); err != nil {
		return err
	}
	return config.WriteAtomic(path, buf.Bytes(), 0o600)
}

// reservedCommand : a name a command of the client or of a module already
// has (its short forms included); no alias may take it.
func (u *UI) reservedCommand(name string) bool {
	_, short := aliases[name]
	m, _ := u.modCommand(name)
	return short || slices.Contains(commandNames, "/"+name) || m != nil
}

// expandAlias gives the line an alias stands for: "/name rest" → its text,
// rest appended. Once only: the text runs the commands of the client, never
// another alias, so no alias can loop.
func (u *UI) expandAlias(line string) (string, bool) {
	if !strings.HasPrefix(line, "/") || strings.HasPrefix(line, "//") || strings.Contains(line, "\n") {
		return line, false
	}
	name, rest, _ := strings.Cut(line[1:], " ")
	name = strings.ToLower(name)
	text, ok := u.cmdAlias[name]
	if !ok || u.reservedCommand(name) { // a hand-written alias never hides a command
		return line, false
	}
	if rest = strings.TrimSpace(rest); rest != "" {
		text += " " + rest
	}
	return text, true
}

// aliasCmd : /alias lists the aliases, /alias name shows one, /alias name
// text sets it (quotes around the text dropped).
func (u *UI) aliasCmd(w *Window, text string) {
	name, body, _ := strings.Cut(text, " ")
	name = strings.ToLower(strings.TrimPrefix(name, "/"))
	body = unquote(strings.TrimSpace(body))
	switch {
	case name == "":
		if len(u.cmdAlias) == 0 {
			w.AddSys(i18n.T("alias_none"))
			return
		}
		names := make([]string, 0, len(u.cmdAlias))
		for n := range u.cmdAlias {
			names = append(names, n)
		}
		slices.Sort(names)
		for _, n := range names {
			w.AddSys(i18n.T("alias_line", n, u.cmdAlias[n]))
		}
	case !reCmdAlias.MatchString(name):
		w.AddSys(i18n.T("alias_bad_name", name))
	case body == "":
		if t, ok := u.cmdAlias[name]; ok {
			w.AddSys(i18n.T("alias_line", name, t))
		} else {
			w.AddSys(i18n.T("alias_unknown", name))
		}
	case u.reservedCommand(name):
		w.AddSys(i18n.T("alias_reserved", name))
	default:
		if u.cmdAlias == nil {
			u.cmdAlias = map[string]string{}
		}
		u.cmdAlias[name] = body
		if err := saveCmdAliases(cmdAliasPath(), u.cmdAlias); err != nil {
			w.AddSys(i18n.T("alias_error", err))
		}
		w.AddSys(i18n.T("alias_line", name, body))
	}
}

// unaliasCmd : /unalias name.
func (u *UI) unaliasCmd(w *Window, text string) {
	name := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(text), "/"))
	if name == "" {
		w.AddSys(i18n.T("usage_unalias"))
		return
	}
	if _, ok := u.cmdAlias[name]; !ok {
		w.AddSys(i18n.T("alias_unknown", name))
		return
	}
	delete(u.cmdAlias, name)
	if err := saveCmdAliases(cmdAliasPath(), u.cmdAlias); err != nil {
		w.AddSys(i18n.T("alias_error", err))
	}
	w.AddSys(i18n.T("alias_removed", name))
}

// aliasNames : the aliases as commands ("/name"), for the completion.
func (u *UI) aliasNames() []string {
	out := make([]string, 0, len(u.cmdAlias))
	for n := range u.cmdAlias {
		out = append(out, "/"+n)
	}
	slices.Sort(out)
	return out
}
