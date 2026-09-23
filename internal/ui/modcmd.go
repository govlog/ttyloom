package ui

import (
	"slices"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
)

// modCommand : the module command called name (no slash), and its module.
func (u *UI) modCommand(name string) (module.Module, *module.Command) {
	for _, m := range u.mods {
		cs := m.Commands()
		for i := range cs {
			if cs[i].Name == name {
				return m, &cs[i]
			}
		}
	}
	return nil, nil
}

// modNets : the networks of m in the list of the UI.
func (u *UI) modNets(m module.Module) []string {
	var out []string
	for _, n := range u.netList {
		if model.NetModule(n) == m.Name() {
			out = append(out, n)
		}
	}
	return out
}

// runModCommand runs a module command typed in w; false when name is none.
// A context command needs a network of its module: with none configured it
// is unknown (false), with several and no context it asks which one.
func (u *UI) runModCommand(w *Window, name string, args []string, text string) bool {
	m, c := u.modCommand(name)
	if c == nil {
		return false
	}
	win := u.win(w)
	if c.Context {
		if len(u.modNets(m)) == 0 {
			return false
		}
		if (host{u}).ContextNet(win, m.Name()) == "" {
			w.AddSys(i18n.T("which_net", m.Name()))
			return true
		}
	}
	c.Run(host{u}, win, args, text)
	return true
}

// modComplete : the candidates a module gives for the command being typed.
func (u *UI) modComplete(name, tail, word string) []string {
	_, c := u.modCommand(name)
	if c == nil || c.Complete == nil {
		return nil
	}
	names, typing := c.Complete(host{u}, u.win(u.view()), tail)
	return multiWord(word, typing, names)
}

// topics : the help entries — the client's, then the commands of each module.
func (u *UI) topics() []topic {
	out := slices.Clone(helpTopics)
	for _, m := range u.mods {
		for _, c := range m.Commands() {
			out = append(out, topic{"/" + c.Name, c.Help.Key, c.Help.Section})
		}
	}
	return out
}

// sections : the sections of /help — the client's, with the ones the modules
// add right after "chats", in module order.
func (u *UI) sections() []string {
	var extra []string
	for _, t := range u.topics() {
		if !slices.Contains(helpSections, t.section) && !slices.Contains(extra, t.section) {
			extra = append(extra, t.section)
		}
	}
	i := slices.Index(helpSections, "chats") + 1
	return slices.Concat(helpSections[:i], extra, helpSections[i:])
}
