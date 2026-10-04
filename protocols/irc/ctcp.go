package irc

import (
	"fmt"
	"strings"
	"time"

	"github.com/ergochat/irc-go/ircmsg"

	"github.com/govlog/ttyloom/internal/model"
)

// onCTCP : a PRIVMSG wrapped in \x01, parsed here rather than by the library,
// which answered every VERSION or TIME on its own — ignore list or not, at
// any rate (an easy "Excess Flood"). ACTION and DCC SEND are messages;
// VERSION, PING, TIME and CLIENTINFO get an answer when asked directly (not
// through a room), from someone not ignored, and while the bucket has a
// token. Everything else, USERINFO included, is dropped.
func (c *Client) onCTCP(e ircmsg.Message, body string) {
	verb, arg, _ := strings.Cut(body, " ")
	switch verb = strings.ToUpper(verb); verb {
	case "ACTION":
		c.onAction(e, arg)
	case "DCC":
		c.onDCC(e, arg)
	case "VERSION", "PING", "TIME", "CLIENTINFO":
		if e.Nick() == "" || !c.isMe(e.Params[0]) || !c.ctcp.allow() {
			return
		}
		reply := verb
		switch verb {
		case "VERSION":
			reply += " ttyloom"
		case "PING":
			if arg != "" {
				reply += " " + arg
			}
		case "TIME":
			reply += " " + time.Now().UTC().Format(time.RFC1123)
		case "CLIENTINFO":
			reply += " ACTION CLIENTINFO DCC PING TIME VERSION"
		}
		// Straight out, past the output pacer: a callback never waits, and
		// the bucket above is the limit of these lines.
		c.conn.Send("NOTICE", e.Nick(), "\x01"+reply+"\x01")
	}
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
