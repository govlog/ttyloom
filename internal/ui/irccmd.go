package ui

import (
	"context"
	"slices"
	"strings"

	"github.com/govlog/ttyloom/internal/model"
)

// The IRC commands (ircii names). They exist only from an IRC context — a
// window on an IRC chat, the IRC tab, or a single IRC network configured —
// and go to the backend through the optional ircCommander interface; the
// answers come back as EvLines, in the window the command was typed in.

var ircCommandNames = []string{"/part", "/cycle", "/topic", "/nick", "/notice", "/invite", "/names", "/mode",
	"/kick", "/ban", "/kickban", "/who", "/whowas", "/motd", "/ctcp", "/quote", "/ignore"}

// ircCommander : a backend that takes the IRC commands (protocols/irc).
// reply is the chat of the window the command came from (0 = window 0);
// room the channel of that window when it is one, the default target of
// /part, /mode, /kick…
type ircCommander interface {
	Command(ctx context.Context, reply int64, room, name string, args []string, text string)
}

// commandNames : the commands that resolve and complete right now — the
// generic ones, plus the IRC ones from an IRC context.
func (u *UI) commandNames() []string {
	if u.ircNetFor(u.view()) == "" {
		return commandNames
	}
	return append(slices.Clone(commandNames), ircCommandNames...)
}

// isIRCCommand : name (without /) is one of ircCommandNames.
func isIRCCommand(name string) bool { return slices.Contains(ircCommandNames, "/"+name) }

// winOn : w belongs to net, or to no network at all (window 0). The IRC
// commands go to the network whatever the window, their names mean nothing
// elsewhere; a shared command (/whois) needs this.
func winOn(w *Window, net string) bool {
	for _, c := range []*model.Chat{w.Chat, w.Target} {
		if c != nil && c.Net != net {
			return false
		}
	}
	return true
}

// memberLister : a backend that knows who is in a chat (protocols/irc), for
// the Tab completion of the nick arguments.
type memberLister interface {
	Members(chat *model.Chat) []string
}

// ctcpNames : the CTCP requests /ctcp completes.
var ctcpNames = []string{"VERSION", "PING", "TIME", "USERINFO", "CLIENTINFO", "SOURCE", "FINGER"}

// ircNicks : the members of the IRC room of the shown window — its send
// target when it is a room, else its chat. None outside a room.
func (u *UI) ircNicks() []string {
	w := u.view()
	net := u.ircNetFor(w)
	l, ok := u.nets[net].(memberLister)
	if !ok {
		return nil
	}
	for _, c := range []*model.Chat{w.Target, w.Chat} {
		if c != nil && c.Net == net { // a private chat has its peer
			return l.Members(c)
		}
	}
	return nil
}

// isIRCRoom : the four channel prefixes of RFC 2811.
func isIRCRoom(s string) bool { return s != "" && strings.ContainsRune("#&!+", rune(s[0])) }

// ircChans : the rooms joined on the IRC network of the shown window.
func (u *UI) ircChans() []string {
	net := u.ircNetFor(u.view())
	if net == "" {
		return nil
	}
	var out []string
	for _, c := range u.chatList {
		if c.Net == net && c.Kind != model.ChatUser {
			out = append(out, c.Title)
		}
	}
	return out
}

// ircIgnores : the masks /ignore already holds on that network.
func (u *UI) ircIgnores() []string {
	if n := u.cfg.IRCByName(model.IRCName(u.ircNetFor(u.view()))); n != nil {
		return n.Ignores
	}
	return nil
}

// ircCommand routes one IRC command typed in w to the network net.
func (u *UI) ircCommand(w *Window, net, name string, args []string, text string) {
	b, ok := u.nets[net].(ircCommander)
	if !ok {
		u.netUnsupported(net)
		return
	}
	room := ""
	for _, c := range []*model.Chat{w.Target, w.Chat} {
		if c != nil && c.Net == net && isIRCRoom(c.Title) { // a private window has no default room
			room = c.Title
			break
		}
	}
	var reply int64
	if w.Chat != nil && w.Chat.Net == net {
		reply = w.Chat.ID
	}
	b.Command(u.netContext(net), reply, room, name, args, text)
}
