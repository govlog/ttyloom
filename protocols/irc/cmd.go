package irc

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ergochat/irc-go/ircevent"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// The IRC commands of the UI (/kick, /who…), their wire form, and the
// numerics that answer them. Every answer is an EvLines aimed at the window
// the command came from (c.asked remembers it per kind of command).

// banReq : a /ban or /kickban waiting for the USERHOST of its nick.
type banReq struct {
	room, nick, reason string
	kick               bool
	reply              int64
}

// usages : the syntax of each command, for the error line.
var usages = map[string]string{
	"part": "/part [#room] [reason]", "cycle": "/cycle [#room]", "topic": "/topic [#room] [text]",
	"nick": "/nick <nick>", "notice": "/notice <target> <text>", "invite": "/invite <nick> [#room]",
	"names": "/names [#room]", "mode": "/mode [target] [modes…]", "kick": "/kick [#room] <nick> [reason]",
	"ban": "/ban [#room] <nick | mask>", "kickban": "/kickban [#room] <nick> [reason]", "who": "/who [mask]",
	"whowas": "/whowas <nick>", "motd": "/motd", "ctcp": "/ctcp <nick> <command> [args]",
	"quote": "/quote <raw line>", "ignore": "/ignore [nick | mask]",
}

// Command runs one slash command from the UI. reply: chat of the window it
// was typed in (0 = window 0); room: the channel of that window, if any.
func (c *Client) Command(_ context.Context, reply int64, room, name string, args []string, text string) {
	go func() {
		defer c.Guard("Command", nil)
		if err := c.command(reply, room, name, args, text); err != nil {
			c.Post(model.EvLines{ChatID: reply, Lines: []string{err.Error()}})
		}
	}()
}

// roomArg : the room named first in args, else the room of the window; the
// rest of the arguments follow. ok false with neither.
func roomArg(args []string, room string) (string, []string, bool) {
	if len(args) > 0 && isChannel(args[0]) {
		return args[0], args[1:], true
	}
	return room, args, room != ""
}

// rest : what follows the first n words of text. The blanks are walked one
// word at a time — a prefix rebuilt with single spaces would not match a text
// where two words are set apart by more than one.
func rest(text string, n int) string {
	s := text
	for ; n > 0; n-- {
		s = strings.TrimLeftFunc(s, unicode.IsSpace)
		i := strings.IndexFunc(s, unicode.IsSpace)
		if i < 0 {
			return ""
		}
		s = s[i:]
	}
	return strings.TrimSpace(s)
}

func (c *Client) command(reply int64, room, name string, args []string, text string) error {
	usage := fmt.Errorf("%s", i18n.T("irc_usage", usages[name]))
	ask := func(kind string) { // the answer of this command goes to reply
		c.mu.Lock()
		c.asked[kind] = reply
		c.mu.Unlock()
	}
	switch name {
	case "part":
		ch, more, ok := roomArg(args, room)
		if !ok {
			return usage
		}
		c.part(ch, strings.Join(more, " "))
	case "cycle":
		ch, _, ok := roomArg(args, room)
		if !ok {
			return usage
		}
		if err := c.send("PART", ch, "cycling"); err != nil {
			return err
		}
		return c.send("JOIN", ch)
	case "topic":
		ch, more, ok := roomArg(args, room)
		if !ok {
			return usage
		}
		ask("topic")
		if len(more) == 0 {
			return c.send("TOPIC", ch)
		}
		return c.send("TOPIC", ch, strings.Join(more, " "))
	case "nick":
		if len(args) != 1 {
			return usage
		}
		return c.send("NICK", args[0])
	case "notice":
		if len(args) < 2 {
			return usage
		}
		if err := c.send("NOTICE", args[0], rest(text, 1)); err != nil {
			return err
		}
		c.Post(model.EvLines{ChatID: reply, Lines: []string{"-> -" + args[0] + "- " + rest(text, 1)}})
	case "invite":
		if len(args) == 0 {
			return usage
		}
		ch, _, ok := roomArg(args[1:], room)
		if !ok {
			return usage
		}
		ask("invite")
		return c.send("INVITE", args[0], ch)
	case "names":
		ch, _, ok := roomArg(args, room)
		if !ok {
			return usage
		}
		ask("names:" + c.casefold(ch))
		return c.send("NAMES", ch)
	case "mode":
		params := args
		if len(params) == 0 || strings.HasPrefix(params[0], "+") || strings.HasPrefix(params[0], "-") {
			if room == "" {
				return usage
			}
			params = append([]string{room}, params...)
		}
		ask("mode")
		return c.send("MODE", params...)
	case "kick":
		ch, more, ok := roomArg(args, room)
		if !ok || len(more) == 0 {
			return usage
		}
		return c.kick(ch, more[0], strings.Join(more[1:], " "))
	case "ban", "kickban":
		ch, more, ok := roomArg(args, room)
		if !ok || len(more) == 0 {
			return usage
		}
		req := banReq{room: ch, nick: more[0], reason: strings.Join(more[1:], " "), kick: name == "kickban", reply: reply}
		if strings.ContainsAny(req.nick, "!@") { // a mask as it is
			return c.ban(req, req.nick)
		}
		c.mu.Lock()
		c.bans[c.casefold(req.nick)] = req
		c.mu.Unlock()
		return c.send("USERHOST", req.nick)
	case "who":
		mask := room
		if len(args) > 0 {
			mask = args[0]
		}
		if mask == "" {
			return usage
		}
		ask("who")
		return c.send("WHO", mask)
	case "whowas":
		if len(args) != 1 {
			return usage
		}
		ask("whowas")
		return c.send("WHOWAS", args[0])
	case "motd":
		ask("motd")
		return c.send("MOTD")
	case "ctcp":
		if len(args) < 2 {
			return usage
		}
		nick, verb := args[0], strings.ToUpper(args[1])
		body := verb
		if verb == "PING" {
			c.mu.Lock()
			c.pings[c.casefold(nick)] = time.Now()
			c.mu.Unlock()
			body += " " + strconv.FormatInt(time.Now().UnixNano(), 10)
		} else if r := rest(text, 2); r != "" {
			body += " " + r
		}
		ask("ctcp:" + c.casefold(nick))
		return c.send("PRIVMSG", nick, "\x01"+body+"\x01")
	case "quote":
		if text == "" {
			return usage
		}
		return c.sendRaw(text)
	case "ignore":
		return c.ignore(reply, strings.Join(args, " "))
	default:
		return fmt.Errorf("%s: unknown command", name)
	}
	return nil
}

func (c *Client) kick(room, nick, reason string) error {
	if reason == "" {
		return c.send("KICK", room, nick)
	}
	return c.send("KICK", room, nick, reason)
}

// ban sets the mask on the room, and kicks after it for a /kickban.
func (c *Client) ban(req banReq, mask string) error {
	if err := c.send("MODE", req.room, "+b", mask); err != nil {
		return err
	}
	if req.kick {
		return c.kick(req.room, req.nick, req.reason)
	}
	return nil
}

// banMask : *!*@host from the user@host of a USERHOST answer (the ident and
// its ~ are dropped: the host is what identifies the connection).
func banMask(userhost string) string {
	_, host, _ := strings.Cut(userhost, "@")
	return "*!*@" + host
}

// onUserhost : 302 "<me> :nick=+user@host nick2=-user@host" — the bans
// waiting for these nicks go out.
func (c *Client) onUserhost(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	if strings.TrimSpace(e.Params[1]) == "" {
		// An unknown nick gets an empty answer, naming nobody: the bans waiting
		// would sit there for ever and a moderation command would look done.
		c.mu.Lock()
		pending := make([]banReq, 0, len(c.bans))
		for _, req := range c.bans {
			pending = append(pending, req)
		}
		clear(c.bans)
		c.mu.Unlock()
		for _, req := range pending {
			c.Post(model.EvLines{ChatID: req.reply, Lines: []string{req.nick + ": " + i18n.T("irc_no_such_nick")}})
		}
		return
	}
	for _, f := range strings.Fields(e.Params[1]) {
		nick, uh, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		nick = strings.TrimSuffix(nick, "*") // an operator is "nick*"
		uh = strings.TrimLeft(uh, "+-")      // away flag
		c.mu.Lock()
		req, waiting := c.bans[c.casefold(nick)]
		delete(c.bans, c.casefold(nick))
		c.mu.Unlock()
		if !waiting {
			continue
		}
		if _, host, _ := strings.Cut(uh, "@"); host == "" {
			// No host in the answer: "*!*@" is a ban the server widens to
			// everyone. Nothing goes out.
			c.Post(model.EvLines{ChatID: req.reply, Lines: []string{nick + ": " + i18n.T("irc_no_such_nick")}})
			continue
		}
		if err := c.ban(req, banMask(uh)); err != nil {
			c.Post(model.EvLines{ChatID: req.reply, Lines: []string{err.Error()}})
		}
	}
}

// replyTo : the window that asked for kind, then forgotten (0 = window 0).
func (c *Client) replyTo(kind string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.asked[kind]
	delete(c.asked, kind)
	return r
}

// peek : same, kept (a list answer spans several numerics).
func (c *Client) peek(kind string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.asked[kind]
}

// lines posts text to the window that asked for kind.
func (c *Client) lines(kind string, text ...string) {
	c.Post(model.EvLines{ChatID: c.peek(kind), Lines: text})
}

// reply : a one-line answer of a command (331, 341, 305, 306).
func (c *Client) reply(kind string) func(ircmsg.Message) {
	return func(e ircmsg.Message) {
		if len(e.Params) < 2 {
			return
		}
		c.Post(model.EvLines{ChatID: c.replyTo(kind), Lines: []string{strings.Join(e.Params[1:], " ")}})
	}
}

// onError : a refused command (421, 461, 482…) — "<thing> <text>" in window 0.
func (c *Client) onError(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	c.Post(model.EvLines{Lines: []string{strings.Join(e.Params[1:], " ")}})
}

func (c *Client) onModeReply(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	var text string
	switch e.Command {
	case ircevent.RPL_CHANNELMODEIS: // me #chan +modes [params]
		text = "mode/" + e.Params[1] + " [" + strings.Join(e.Params[2:], " ") + "]"
	case ircevent.RPL_CREATIONTIME: // me #chan <unix>
		if len(e.Params) >= 3 {
			if s, err := strconv.ParseInt(e.Params[2], 10, 64); err == nil {
				text = e.Params[1] + ": created " + i18n.LocalTime(time.Unix(s, 0))
			}
		}
	case ircevent.RPL_UMODEIS: // me +modes
		text = "your user mode is " + strings.Join(e.Params[1:], " ")
	default: // the lists (367, 346, 348) and their ends
		text = strings.Join(e.Params[1:], " ")
	}
	if text == "" {
		return
	}
	// The answer is over on a list end (368, 347, 349), on the 329 that follows
	// the 324 of a channel, and on the 221 of a user mode: the asking window is
	// forgotten there, kept on every numeric before it.
	switch e.Command {
	case ircevent.RPL_ENDOFBANLIST, ircevent.RPL_ENDOFINVITELIST, ircevent.RPL_ENDOFEXCEPTLIST,
		ircevent.RPL_CREATIONTIME, ircevent.RPL_UMODEIS:
		c.Post(model.EvLines{ChatID: c.replyTo("mode"), Lines: []string{text}})
	default:
		c.lines("mode", text)
	}
}

// onWho : 352 "me #chan user host server nick flags :hops realname" — one
// line per user, "nick user@host (realname) flags #chan server".
func (c *Client) onWho(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	if e.Command == ircevent.RPL_WHOSPCRPL || len(e.Params) < 8 {
		c.lines("who", strings.Join(e.Params[1:], " "))
		return
	}
	real := e.Params[7]
	if _, r, ok := strings.Cut(real, " "); ok { // hop count first
		real = r
	}
	c.lines("who", fmt.Sprintf("%s %s@%s (%s) %s %s %s", e.Params[5], e.Params[2], e.Params[3], real, e.Params[6], e.Params[1], e.Params[4]))
}

func (c *Client) onWhoEnd(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	c.Post(model.EvLines{ChatID: c.replyTo("who"), Lines: []string{strings.Join(e.Params[1:], " ")}})
}

// onWhowas : 314 "me nick user host * :realname".
func (c *Client) onWhowas(e ircmsg.Message) {
	if len(e.Params) < 6 {
		return
	}
	c.lines("whowas", fmt.Sprintf("%s was %s@%s (%s)", e.Params[1], e.Params[2], e.Params[3], e.Params[5]))
}

func (c *Client) onWhowasEnd(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	c.Post(model.EvLines{ChatID: c.replyTo("whowas"), Lines: []string{strings.Join(e.Params[1:], " ")}})
}

// MOTD: gathered from 375 to 376 (or 422 alone), posted once — to the
// window of /motd, else window 0 (the MOTD of every connection).
func (c *Client) onMotdLine(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	c.mu.Lock()
	c.motd = append(c.motd, strings.TrimPrefix(e.Params[len(e.Params)-1], "- "))
	c.mu.Unlock()
}

func (c *Client) onMotdEnd(e ircmsg.Message) {
	c.mu.Lock()
	lines := c.motd
	c.motd = nil
	c.mu.Unlock()
	if len(e.Params) >= 2 {
		lines = append(lines, e.Params[len(e.Params)-1])
	}
	c.Post(model.EvLines{ChatID: c.replyTo("motd"), Lines: lines})
}

// onCTCPReply : a NOTICE "\x01CMD text\x01" from nick — the answer of /ctcp.
func (c *Client) onCTCPReply(nick, body string) {
	verb, text, _ := strings.Cut(body, " ")
	if strings.EqualFold(verb, "PING") {
		c.mu.Lock()
		at, ok := c.pings[c.casefold(nick)]
		delete(c.pings, c.casefold(nick))
		c.mu.Unlock()
		if ok {
			text = fmt.Sprintf("%.3f s", time.Since(at).Seconds())
		}
	}
	clean, _ := spansOf(text)
	c.Post(model.EvLines{ChatID: c.replyTo("ctcp:" + c.casefold(nick)),
		Lines: []string{"[ctcp(" + nick + ")] " + strings.ToUpper(verb) + " " + clean}})
}

// whoisLabels : the numerics of a WHOIS and the label of their line.
var whoisLabels = map[string]string{
	ircevent.RPL_WHOISOPERATOR: "oper", ircevent.RPL_WHOISCHANNELS: "channels", ircevent.RPL_AWAY: "away",
	ircevent.RPL_WHOISSECURE: "secure", ircevent.RPL_WHOISACTUALLY: "actually", ircevent.RPL_WHOISBOT: "bot",
	ircevent.RPL_WHOISMODES: "modes", "320": "special",
}

// formatWhois lays the numerics of a WHOIS out like irssi: a header with
// nick and user@host, one labelled line per numeric, the 318 text last.
func formatWhois(nick string, raw []ircmsg.Message) []string {
	out := []string{"┌ " + nick}
	line := func(label, text string) { out = append(out, fmt.Sprintf("│ %-8s : %s", label, text)) }
	for _, e := range raw {
		p := e.Params
		switch e.Command {
		case ircevent.RPL_WHOISUSER: // nick user host * :realname
			if len(p) >= 6 {
				out[0] = "┌ " + p[1] + " (" + p[2] + "@" + p[3] + ")"
				line("ircname", p[5])
			}
		case ircevent.RPL_WHOISIDLE: // nick idle signon :text
			if len(p) >= 3 {
				if s, err := strconv.Atoi(p[2]); err == nil {
					text := (time.Duration(s) * time.Second).String()
					if len(p) >= 4 {
						if on, err := strconv.ParseInt(p[3], 10, 64); err == nil {
							text += ", signon " + i18n.LocalTime(time.Unix(on, 0))
						}
					}
					line("idle", text)
				}
			}
		case ircevent.RPL_WHOISSERVER: // nick server :info
			if len(p) >= 4 {
				line("server", p[2]+" ("+p[3]+")")
			} else if len(p) >= 3 {
				line("server", p[2])
			}
		case ircevent.RPL_WHOISACCOUNT: // nick account :text
			if len(p) >= 4 {
				line("loggedin", p[3]+" "+p[2])
			}
		case ircevent.RPL_ENDOFWHOIS:
			if len(p) >= 2 {
				out = append(out, "└ "+p[len(p)-1])
			}
		default:
			if label, ok := whoisLabels[e.Command]; ok && len(p) >= 3 {
				line(label, strings.Join(p[2:], " "))
			}
		}
	}
	return out
}

// --- ignore ---

// ignoreMask : the mask of what was typed — a nick alone is nick!*@*, a
// user@host is *!user@host, a full mask stays.
func ignoreMask(s string) string {
	switch {
	case strings.Contains(s, "!"):
		return s
	case strings.Contains(s, "@"):
		return "*!" + s
	}
	return s + "!*@*"
}

// globMatch : * and ? wildcards, case-insensitive (ASCII fold, IRC masks
// are ASCII).
func globMatch(pattern, s string) bool {
	p, t := strings.ToLower(pattern), strings.ToLower(s)
	for len(p) > 0 {
		switch p[0] {
		case '*':
			for len(p) > 0 && p[0] == '*' {
				p = p[1:]
			}
			if p == "" {
				return true
			}
			for i := 0; i <= len(t); i++ {
				if globMatch(p, t[i:]) {
					return true
				}
			}
			return false
		case '?':
			if t == "" {
				return false
			}
		default:
			if t == "" || p[0] != t[0] {
				return false
			}
		}
		p, t = p[1:], t[1:]
	}
	return t == ""
}

// ignored : the source of e (nick!user@host) matches an ignore mask.
func (c *Client) ignored(e ircmsg.Message) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.ignores {
		if globMatch(m, e.Source) {
			return true
		}
	}
	return false
}

// ignore : /ignore — lists, or toggles a mask; the list is saved through
// EvIRCIgnores.
func (c *Client) ignore(reply int64, arg string) error {
	if arg == "" {
		c.mu.Lock()
		text := i18n.T("irc_ignore_none")
		if len(c.ignores) > 0 {
			text = i18n.T("irc_ignore_list", strings.Join(c.ignores, ", "))
		}
		c.mu.Unlock()
		c.Post(model.EvLines{ChatID: reply, Lines: []string{text}})
		return nil
	}
	mask := ignoreMask(arg)
	c.mu.Lock()
	var text string
	if i := slices.IndexFunc(c.ignores, func(m string) bool { return strings.EqualFold(m, mask) }); i >= 0 {
		c.ignores = slices.Delete(c.ignores, i, i+1)
		text = i18n.T("irc_ignore_removed", mask)
	} else {
		c.ignores = append(c.ignores, mask)
		text = i18n.T("irc_ignore_added", mask)
	}
	list := slices.Clone(c.ignores)
	c.mu.Unlock()
	c.Post(model.EvLines{ChatID: reply, Lines: []string{text}})
	c.Post(model.EvIRCIgnores{Ignores: list})
	return nil
}

// --- away ---

// Away : AWAY with the message, AWAY alone to come back; 305/306 answer in
// window 0.
func (c *Client) Away(_ context.Context, msg string) {
	go func() {
		defer c.Guard("Away", nil)
		c.mu.Lock()
		c.asked["away"] = 0
		c.mu.Unlock()
		var err error
		if msg == "" {
			err = c.send("AWAY")
		} else {
			err = c.send("AWAY", msg)
		}
		if err != nil {
			c.Post(model.EvLines{Lines: []string{err.Error()}})
		}
	}()
}
