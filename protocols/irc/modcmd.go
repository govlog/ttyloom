package irc

import (
	"context"
	"slices"
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
)

// The IRC commands (ircii names). /irc and /dcc exist whenever the module is
// there; the others only from an IRC context — a window on an IRC chat, the
// IRC tab, or a single IRC network configured — and go to the backend of
// that network; the answers come back as EvLines, in the window the command
// was typed in.

// contextNames : the ircii commands, in an IRC context only.
var contextNames = []string{"part", "cycle", "topic", "nick", "notice", "invite", "names", "mode",
	"kick", "ban", "kickban", "who", "whowas", "motd", "ctcp", "quote", "ignore"}

// ctcpNames : the CTCP requests /ctcp completes.
var ctcpNames = []string{"VERSION", "PING", "TIME", "USERINFO", "CLIENTINFO", "SOURCE", "FINGER"}

// commander, members : what the commands of the module ask of the backend of
// a network; *Client, and the fakes of the tests.
type commander interface {
	Command(ctx context.Context, reply int64, room, name string, args []string, text string)
}
type members interface {
	Members(chat *model.Chat) []string
}

func (m *Module) Commands() []module.Command {
	out := []module.Command{
		{Name: "irc", Help: module.Topic{Key: "help_irc", Section: "chats"}, Complete: m.completeIRC, Run: m.ircCmd},
		{Name: "dcc", Help: module.Topic{Key: "help_dcc", Section: "chats"}, Run: m.dccCmd},
	}
	for _, name := range contextNames {
		out = append(out, module.Command{Name: name, Context: true,
			Help:     module.Topic{Key: "help_" + name, Section: "irc"},
			Complete: m.completer(name),
			Run:      func(h module.Host, w module.Win, args []string, text string) { m.route(h, w, name, args, text) }})
	}
	return out
}

// ircCmd : /irc (status of each network), /irc add (the form), /irc connect
// <name>, /irc disconnect <name>, /irc delete <name>.
func (m *Module) ircCmd(h module.Host, w module.Win, args []string, _ string) {
	sub, name := "", ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	if len(args) > 1 {
		name = strings.ToLower(args[1])
	}
	switch sub {
	case "", "status":
		nets := m.Networks()
		if len(nets) == 0 {
			h.Print(w, i18n.T("irc_none"))
			return
		}
		for _, n := range nets {
			h.NetAction(w, n, "status")
		}
	case "add":
		m.openForm(h)
	case "connect", "disconnect", "delete":
		net := Net(name)
		if name == "" || !slices.Contains(m.Networks(), net) {
			h.Print(w, i18n.T("irc_unknown", name, strings.Join(m.Networks(), ", ")))
			return
		}
		switch sub {
		case "connect":
			h.NetAction(w, net, "login")
		case "disconnect":
			h.NetAction(w, net, "disconnect")
		default:
			m.delete(h, w, net)
		}
	default:
		h.Print(w, i18n.T("usage_irc"))
	}
}

// route sends one ircii command typed in w to the IRC network w means. The
// room of the window is the default target of /part, /mode, /kick…; a
// private window has none.
func (m *Module) route(h module.Host, w module.Win, name string, args []string, text string) {
	net := h.ContextNet(w, Prefix)
	b, ok := h.Backend(net).(commander)
	if !ok {
		h.Print(w, i18n.T("net_unsupported", net))
		return
	}
	room := ""
	for _, c := range []*model.Chat{w.Target, w.Chat} {
		if c != nil && c.Net == net && IsChannel(c.Title) {
			room = c.Title
			break
		}
	}
	var reply int64
	if w.Chat != nil && w.Chat.Net == net {
		reply = w.Chat.ID
	}
	b.Command(h.Context(net), reply, room, name, args, text)
}

// ircArg : the argument of an IRC command being typed in rest (everything
// after the command) — its index, a trailing space starting the next one,
// and its text.
func ircArg(rest string) (int, string) {
	done := strings.Fields(rest)
	if n := len(done); n > 0 && !strings.HasSuffix(rest, " ") {
		return n - 1, done[n-1] // the last word is the one being typed
	}
	return len(done), ""
}

// completeIRC : the sub-commands of /irc, then the names of the networks.
func (m *Module) completeIRC(_ module.Host, _ module.Win, rest string) ([]string, string) {
	if !strings.Contains(rest, " ") {
		return []string{"add", "connect", "disconnect", "delete"}, rest
	}
	var names []string
	for _, n := range m.Networks() {
		names = append(names, Name(n))
	}
	_, arg := ircArg(rest)
	return names, arg
}

// completer : the completion of the arguments of an ircii command — a
// member of the room, a room joined on the network, a nick or a room, a
// CTCP request, an ignore mask.
func (m *Module) completer(name string) func(module.Host, module.Win, string) ([]string, string) {
	return func(h module.Host, w module.Win, rest string) ([]string, string) {
		i, arg := ircArg(rest)
		nicks := func() []string { return m.nicks(h, w) }
		chans := func() []string { return m.chans(h, w) }
		target := func() []string {
			if IsChannel(arg) {
				return chans()
			}
			return nicks()
		}
		switch {
		case name == "whowas" && i == 0:
			return nicks(), arg
		case (name == "invite" || name == "ctcp") && i == 0:
			return nicks(), arg
		case name == "ctcp" && i == 1:
			return ctcpNames, arg
		case name == "invite" && i == 1:
			return chans(), arg
		case name == "ignore" && i == 0:
			return append(nicks(), m.ignores(h, w)...), arg
		case slices.Contains([]string{"part", "cycle", "names", "topic", "mode"}, name) && i == 0:
			return chans(), arg
		case slices.Contains([]string{"kick", "kickban", "ban"}, name):
			// <target> <nick> <reason…>: a room takes a nick after it, the rest is free text.
			switch {
			case i == 0:
				return target(), arg
			case i == 1 && IsChannel(strings.Fields(rest)[0]):
				return nicks(), arg
			}
		case (name == "who" || name == "notice") && i == 0:
			return target(), arg
		}
		return nil, arg
	}
}

// nicks : the members of the IRC room of w — its send target when it is a
// room, else its chat. None outside a room.
func (m *Module) nicks(h module.Host, w module.Win) []string {
	net := h.ContextNet(w, Prefix)
	l, ok := h.Backend(net).(members)
	if !ok {
		return nil
	}
	for _, c := range []*model.Chat{w.Target, w.Chat} {
		if c != nil && c.Net == net && IsChannel(c.Title) {
			return l.Members(c) // the room first: a query target must not hide it
		}
	}
	for _, c := range []*model.Chat{w.Target, w.Chat} {
		if c != nil && c.Net == net {
			return l.Members(c) // else a private chat, which has its peer
		}
	}
	return nil
}

// chans : the rooms joined on the IRC network of w.
func (m *Module) chans(h module.Host, w module.Win) []string {
	var out []string
	for _, c := range h.Chats(h.ContextNet(w, Prefix)) {
		if c.Kind != model.ChatUser {
			out = append(out, c.Title)
		}
	}
	return out
}

// ignores : the masks /ignore already holds on the network of w.
func (m *Module) ignores(h module.Host, w module.Win) []string {
	if n := m.ByName(Name(h.ContextNet(w, Prefix))); n != nil {
		return n.Ignores
	}
	return nil
}
