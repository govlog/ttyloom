// Package irc is the IRC backend: one Client per network, on ircevent
// (ergochat/irc-go). No bouncer and no helper program — the channel list of
// the configuration is the only memory across sessions, and the disk cache
// of the UI is the only history. It talks to the UI through model.Event
// only, like tgc and dsc.
package irc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ergochat/irc-go/ircevent"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// Config : one [[irc]] table, plus what the backend needs from the client.
type Config struct {
	Name     string // network key without the prefix (irc:<Name>)
	Host     string
	Port     int
	TLS      bool
	Nick     string
	User     string
	RealName string
	Password string // NickServ / SASL PLAIN password; empty = none
	Channels []string
	DCCIP    string
	DCCPorts string
	Ignores  []string // nick!user@host masks whose lines are dropped (/ignore)
	// SaveChannels writes the channel list back to the configuration — after
	// a join or a part, so that the next start finds the same rooms.
	SaveChannels func([]string) error
	// Dial : the tests plug a pipe here; nil = net.Dialer.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

type Client struct {
	model.Poster
	cfg     Config
	conn    *ircevent.Connection
	ids     idGen
	casemap atomic.Value

	mu       sync.Mutex
	channels []string                  // rooms to be in: the config list, joined at each connection
	members  map[string][]string       // folded channel -> nicks seen (NAMES, JOIN, PART…)
	names    map[string][]string       // NAMES in progress, folded channel -> nicks
	queries  map[string]string         // folded nick -> nick, private chats open
	joining  map[string][]model.EvChat // folded channel -> Resolve query waiting for the JOIN
	naming   map[string]*model.Chat
	whois    map[string]*whoisReq
	offers   map[string]dccOffer  // folded nick -> last DCC offer received
	xfers    []string             // DCC transfers running, one label each
	asked    map[string]int64     // kind of command ("who", "motd", "ctcp:<nick>"…) -> chat its answer goes to
	bans     []banReq             // bans waiting for their USERHOST answer, in the order asked
	motd     []string             // MOTD lines gathered until 376/422
	ignores  []string             // /ignore masks, nick!user@host with * and ?
	pings    map[string]time.Time // folded nick -> CTCP PING sent at
	saslOK   bool
	ready    bool // EvReady posted (once per Run)
	sock     net.Conn
}

// whoisReq : one WHOIS in flight, the numerics gathered until 318.
type whoisReq struct {
	chatID int64
	raw    []ircmsg.Message
}

// New builds the client; nothing connects before Run.
func New(cfg Config, events chan<- model.Event) *Client {
	c := &Client{Poster: model.Poster{Events: events}, cfg: cfg,
		members: map[string][]string{}, names: map[string][]string{}, queries: map[string]string{},
		joining: map[string][]model.EvChat{}, naming: map[string]*model.Chat{}, whois: map[string]*whoisReq{},
		offers: map[string]dccOffer{}, asked: map[string]int64{}, pings: map[string]time.Time{}}
	for _, m := range cfg.Ignores { // a hand-written "bob" is the mask bob!*@*
		c.ignores = append(c.ignores, ignoreMask(m))
	}
	for _, ch := range cfg.Channels {
		if isChannel(ch) && !slices.Contains(c.channels, ch) {
			c.channels = append(c.channels, ch)
		}
	}
	return c
}

// net : the network key, in every log line — with several IRC networks the
// bare text says nothing about which one speaks.
func (c *Client) net() string { return model.IRCNet(c.cfg.Name) }

// Caps : whois (WHOIS), name resolution (/join, /query) and leaving a room.
// No history, no edit, no reaction, no read receipt, no search: IRC has none.
func (c *Client) Caps() model.Caps { return model.Caps{Whois: true, Resolve: true, Leave: true} }

// logWriter : the ircevent log goes to /debug, never to the terminal.
type logWriter struct{ c *Client }

func (w logWriter) Write(p []byte) (int, error) {
	w.c.PostNB(model.EvLog{Level: "DEBUG", Msg: w.c.net() + ": " + strings.TrimSpace(string(p))})
	return len(p), nil
}

// build makes the ircevent connection of the configuration.
func (c *Client) build() *ircevent.Connection {
	cfg := c.cfg
	user := cfg.User
	if user == "" {
		user = cfg.Nick
	}
	real := cfg.RealName
	if real == "" {
		real = cfg.Nick
	}
	dial := cfg.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second}).DialContext
	}
	conn := &ircevent.Connection{
		Server:        net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Nick:          cfg.Nick,
		User:          user,
		RealName:      real,
		UseTLS:        cfg.TLS,
		TLSConfig:     &tls.Config{ServerName: cfg.Host},
		RequestCaps:   []string{"server-time"},
		SASLOptional:  true, // a network without SASL identifies to NickServ after 001
		EnableCTCP:    true, // VERSION/PING answered by the library, ACTION and DCC rewritten
		Version:       "ttyloom",
		QuitMessage:   "ttyloom",
		ReconnectFreq: 20 * time.Second,
		Log:           log.New(logWriter{c}, "", 0),
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			s, err := dial(ctx, network, addr)
			if err == nil {
				c.mu.Lock()
				c.sock = s
				c.mu.Unlock()
			}
			return s, err
		},
	}
	if cfg.Password != "" {
		conn.SASLLogin, conn.SASLPassword = cfg.Nick, cfg.Password
	}
	return conn
}

// Run connects (blocking until 001, the error of a first connection is the
// error of Run), then reconnects on its own until ctx ends.
func (c *Client) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil // a launch already given up (a logout during the token read)
	}
	c.conn = c.build()
	c.wire(c.conn)
	if err := c.conn.Connect(); err != nil {
		return fmt.Errorf("%s: %w", c.net(), err)
	}
	done := make(chan struct{})
	go func() {
		defer c.Guard("Loop", nil)
		defer close(done)
		c.conn.Loop()
	}()
	select {
	case <-ctx.Done():
		c.conn.Quit()
		// The server answers a QUIT by closing; one that does not would keep
		// Loop waiting a whole ping cycle — the socket goes down by hand.
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			c.mu.Lock()
			s := c.sock
			c.mu.Unlock()
			if s != nil {
				s.Close()
			}
			<-done
		}
	case <-done:
	}
	return nil
}

// wire hangs the callbacks of the protocol on conn. Every callback runs on
// the read goroutine of ircevent, one at a time: the maps are locked all the
// same, the Backend methods reach them from other goroutines.
func (c *Client) wire(conn *ircevent.Connection) {
	conn.AddConnectCallback(func(ircmsg.Message) { c.connected() })
	conn.AddDisconnectCallback(func(ircmsg.Message) { c.Post(model.EvDisconnected{}) })
	conn.AddCallback(ircevent.RPL_SASLSUCCESS, func(ircmsg.Message) {
		c.mu.Lock()
		c.saslOK = true
		c.mu.Unlock()
	})
	conn.AddCallback("PRIVMSG", c.onPrivmsg)
	conn.AddCallback("NOTICE", c.onNotice)
	conn.AddCallback("CTCP_ACTION", c.onAction)
	conn.AddCallback("CTCP", c.onCTCP)
	conn.AddCallback("JOIN", c.onJoin)
	conn.AddCallback("PART", c.onPart)
	conn.AddCallback("KICK", c.onKick)
	conn.AddCallback("QUIT", c.onQuit)
	conn.AddCallback("NICK", c.onNick)
	conn.AddCallback("TOPIC", c.onTopic)
	conn.AddCallback("MODE", c.onMode)
	conn.AddCallback("INVITE", func(e ircmsg.Message) {
		if len(e.Params) > 1 {
			c.Post(model.EvLog{Level: "INFO", Msg: i18n.T("irc_invited", c.net(), e.Nick(), e.Params[1])})
		}
	})
	conn.AddCallback(ircevent.RPL_TOPIC, func(e ircmsg.Message) { // 332 on join, and answer of /topic
		if len(e.Params) < 3 {
			return
		}
		c.service(e.Params[1], i18n.T("irc_topic_is", e.Params[2]), e)
		c.mu.Lock()
		reply, pending := c.asked["topic"]
		delete(c.asked, "topic")
		c.mu.Unlock()
		if pending { // the room line alone would not show in the asking window
			c.Post(model.EvLines{ChatID: reply, Lines: []string{i18n.T("irc_topic_is", e.Params[2]) + " (" + e.Params[1] + ")"}})
		}
	})
	conn.AddCallback(ircevent.RPL_NAMREPLY, c.onNames)
	conn.AddCallback(ircevent.RPL_ENDOFNAMES, c.onEndOfNames)
	for _, code := range []string{ircevent.RPL_WHOISUSER, ircevent.RPL_WHOISSERVER, ircevent.RPL_WHOISOPERATOR,
		ircevent.RPL_WHOISIDLE, ircevent.RPL_WHOISCHANNELS, ircevent.RPL_AWAY, ircevent.RPL_WHOISACCOUNT,
		ircevent.RPL_WHOISSECURE, ircevent.RPL_WHOISACTUALLY, ircevent.RPL_WHOISBOT, "320"} {
		conn.AddCallback(code, c.onWhoisLine)
	}
	conn.AddCallback(ircevent.RPL_ENDOFWHOIS, c.onEndOfWhois)
	conn.AddCallback(ircevent.ERR_NOSUCHNICK, c.onNoSuchNick)
	for _, code := range []string{ircevent.ERR_NOSUCHCHANNEL, ircevent.ERR_TOOMANYCHANNELS, ircevent.ERR_CHANNELISFULL,
		ircevent.ERR_INVITEONLYCHAN, ircevent.ERR_BANNEDFROMCHAN, ircevent.ERR_BADCHANNELKEY, ircevent.ERR_BADCHANMASK,
		ircevent.ERR_NEEDREGGEDNICK, ircevent.ERR_CANNOTSENDTOCHAN} {
		conn.AddCallback(code, c.onChannelError)
	}
	conn.AddCallback("ERROR", func(e ircmsg.Message) {
		c.Post(model.EvLog{Level: "ERROR", Msg: c.net() + ": " + strings.Join(e.Params, " ")})
	})
	conn.AddCallback("KILL", func(e ircmsg.Message) {
		c.Post(model.EvLog{Level: "ERROR", Msg: c.net() + ": KILL " + strings.Join(e.Params, " ")})
	})
	// The numerics that answer the slash commands of cmd.go.
	for _, code := range []string{ircevent.RPL_MOTDSTART, ircevent.RPL_MOTD} {
		conn.AddCallback(code, c.onMotdLine)
	}
	conn.AddCallback(ircevent.RPL_ENDOFMOTD, c.onMotdEnd)
	conn.AddCallback(ircevent.ERR_NOMOTD, c.onMotdEnd)
	conn.AddCallback(ircevent.RPL_WHOREPLY, c.onWho)
	conn.AddCallback(ircevent.RPL_WHOSPCRPL, c.onWho)
	conn.AddCallback(ircevent.RPL_ENDOFWHO, c.onWhoEnd)
	conn.AddCallback(ircevent.RPL_WHOWASUSER, c.onWhowas)
	conn.AddCallback(ircevent.RPL_ENDOFWHOWAS, c.onWhowasEnd)
	conn.AddCallback(ircevent.ERR_WASNOSUCHNICK, c.onWhowasEnd)
	conn.AddCallback(ircevent.RPL_USERHOST, c.onUserhost)
	for _, code := range []string{ircevent.RPL_CHANNELMODEIS, ircevent.RPL_CREATIONTIME, ircevent.RPL_UMODEIS,
		ircevent.RPL_BANLIST, ircevent.RPL_ENDOFBANLIST, ircevent.RPL_INVITELIST, ircevent.RPL_ENDOFINVITELIST,
		ircevent.RPL_EXCEPTLIST, ircevent.RPL_ENDOFEXCEPTLIST} {
		conn.AddCallback(code, c.onModeReply)
	}
	conn.AddCallback(ircevent.RPL_NOTOPIC, c.reply("topic"))
	conn.AddCallback(ircevent.RPL_INVITING, c.reply("invite"))
	conn.AddCallback(ircevent.RPL_NOWAWAY, c.reply("away"))
	conn.AddCallback(ircevent.RPL_UNAWAY, c.reply("away"))
	for _, code := range []string{ircevent.ERR_UNKNOWNCOMMAND, ircevent.ERR_NEEDMOREPARAMS, ircevent.ERR_CHANOPRIVSNEEDED,
		ircevent.ERR_USERNOTINCHANNEL, ircevent.ERR_NOTONCHANNEL, ircevent.ERR_NICKNAMEINUSE, ircevent.ERR_ERRONEUSNICKNAME,
		ircevent.ERR_NOSUCHSERVER, ircevent.ERR_NOTEXTTOSEND, ircevent.ERR_USERONCHANNEL, ircevent.ERR_UNKNOWNMODE,
		ircevent.ERR_NOPRIVILEGES, ircevent.ERR_UMODEUNKNOWNFLAG, ircevent.ERR_USERSDONTMATCH} {
		conn.AddCallback(code, c.onError)
	}
}

// me : the nick the server knows us by.
func (c *Client) me() string { return c.conn.CurrentNick() }

// isMe : e comes from us (our own JOIN, NICK…).
func (c *Client) isMe(nick string) bool { return c.casefold(nick) == c.casefold(c.me()) }

// connected : end of the registration, on every connection. EvReady once,
// then the rooms of the list joined again and NickServ told when SASL did not
// do it.
func (c *Client) connected() {
	mode := c.conn.ISupport()["CASEMAPPING"]
	switch mode {
	case "ascii", "rfc1459", "rfc1459-strict", "strict-rfc1459":
	default:
		mode = "rfc1459"
	}
	c.casemap.Store(mode)
	c.mu.Lock()
	first := !c.ready
	c.ready = true
	chans := slices.Clone(c.channels)
	sasl := c.saslOK
	c.mu.Unlock()
	if first {
		c.Post(model.EvReady{SelfID: chatID(c.me(), "ascii"), SelfName: c.me()})
	}
	c.Post(model.EvConnected{})
	if c.cfg.Password != "" && !sasl {
		c.conn.Send("PRIVMSG", "NickServ", "IDENTIFY "+c.cfg.Password)
	}
	for _, ch := range chans {
		c.conn.Join(ch)
	}
}

// msgOf : the message of a PRIVMSG or NOTICE line in chat.
func (c *Client) msgOf(e ircmsg.Message, chat *model.Chat, text string) model.Msg {
	nick := e.Nick()
	clean, spans := spansOf(text)
	return model.Msg{ID: c.ids.next(), ChatID: chat.ID, ChatLabel: chat.Title, Date: serverTime(e.GetTag("time")),
		From: nick, FromID: c.chatID(nick), Out: c.isMe(nick), Text: clean, Entities: spans}
}

// target : the chat a message to target from nick lands in — the channel, or
// the private chat of the sender (of the recipient for our own echo).
func (c *Client) target(e ircmsg.Message) *model.Chat {
	to := e.Params[0]
	if isChannel(to) {
		return c.chatOf(to)
	}
	nick := e.Nick()
	if c.isMe(nick) {
		nick = to
	}
	c.mu.Lock()
	c.queries[c.casefold(nick)] = nick
	c.mu.Unlock()
	return c.chatOf(nick)
}

func (c *Client) onPrivmsg(e ircmsg.Message) {
	if c.ignored(e) || len(e.Params) < 2 {
		return
	}
	chat := c.target(e)
	c.Post(model.EvNewMessage{Msg: c.msgOf(e, chat, e.Params[1]), Chat: chat})
}

// onNotice : a notice to a channel shows there as "-nick- text"; one to us
// from a person opens no window — it goes to the status window, as ircii
// does — and one from the server too.
func (c *Client) onNotice(e ircmsg.Message) {
	if len(e.Params) < 2 || (e.Nick() != "" && c.ignored(e)) {
		return
	}
	// A NOTICE wrapped in \x01 is the answer of a /ctcp, not a message.
	if t := e.Params[1]; len(t) > 2 && t[0] == 1 && t[len(t)-1] == 1 && e.Nick() != "" {
		c.onCTCPReply(e.Nick(), t[1:len(t)-1])
		return
	}
	if isChannel(e.Params[0]) {
		chat := c.chatOf(e.Params[0])
		m := c.msgOf(e, chat, e.Params[1])
		m.Text = "-" + e.Nick() + "- " + m.Text
		c.Post(model.EvNewMessage{Msg: m, Chat: chat})
		return
	}
	from := e.Nick()
	if from == "" {
		from = e.Source
	}
	clean, _ := spansOf(e.Params[1])
	c.Post(model.EvLog{Level: "INFO", Msg: c.net() + ": -" + from + "- " + clean})
}

// onAction : CTCP ACTION, "* nick does" in italics.
func (c *Client) onAction(e ircmsg.Message) {
	if c.ignored(e) || len(e.Params) < 2 {
		return
	}
	chat := c.target(e)
	m := c.msgOf(e, chat, e.Params[1])
	m.Text = "* " + e.Nick() + " " + m.Text
	for i := range m.Entities {
		m.Entities[i].Start += len([]rune("* " + e.Nick() + " "))
		m.Entities[i].End += len([]rune("* " + e.Nick() + " "))
	}
	m.Entities = append([]model.Span{{Start: 0, End: len([]rune(m.Text)), Kind: model.SpanItalic}}, m.Entities...)
	c.Post(model.EvNewMessage{Msg: m, Chat: chat})
}

// onCTCP : every CTCP the library does not answer itself. DCC SEND becomes a
// file offer in the private chat of the sender; the rest is dropped.
func (c *Client) onCTCP(e ircmsg.Message) {
	if c.ignored(e) || len(e.Params) < 2 || !strings.HasPrefix(strings.ToUpper(e.Params[1]), "DCC SEND ") {
		return
	}
	off, err := parseOffer(e.Nick(), e.Params[1][len("DCC SEND "):])
	if err != nil {
		c.Post(model.EvLog{Level: "WARN", Msg: c.net() + ": DCC " + e.Nick() + ": " + err.Error()})
		return
	}
	c.mu.Lock()
	c.offers[c.casefold(off.Nick)] = off
	c.queries[c.casefold(off.Nick)] = off.Nick
	c.mu.Unlock()
	chat := c.chatOf(off.Nick)
	m := c.msgOf(e, chat, "")
	m.Media = off.media()
	c.Post(model.EvNewMessage{Msg: m, Chat: chat})
}

// service posts a service line ("alice joined") in channel.
func (c *Client) service(channel, text string, e ircmsg.Message) {
	chat := c.chatOf(channel)
	m := model.Msg{ID: c.ids.next(), ChatID: chat.ID, ChatLabel: chat.Title,
		Date: serverTime(e.GetTag("time")), Service: text}
	c.Post(model.EvNewMessage{Msg: m, Chat: chat})
}

// member bookkeeping: NAMES fills the list, JOIN/PART/KICK/QUIT/NICK keep it
// right. QUIT and NICK carry no channel: the lists say where the person was.
func (c *Client) addMember(channel, nick string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := c.casefold(channel)
	if !slices.ContainsFunc(c.members[k], func(n string) bool { return c.casefold(n) == c.casefold(nick) }) {
		c.members[k] = append(c.members[k], nick)
	}
}

func (c *Client) dropMember(channel, nick string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := c.casefold(channel)
	c.members[k] = slices.DeleteFunc(c.members[k], func(n string) bool { return c.casefold(n) == c.casefold(nick) })
}

// channelsOf : the channels nick is seen in.
func (c *Client) channelsOf(nick string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for ch, ns := range c.members {
		if slices.ContainsFunc(ns, func(n string) bool { return c.casefold(n) == c.casefold(nick) }) {
			out = append(out, ch)
		}
	}
	slices.Sort(out)
	return out
}

func (c *Client) onJoin(e ircmsg.Message) {
	if len(e.Params) < 1 {
		return
	}
	ch, nick := e.Params[0], e.Nick()
	if c.isMe(nick) {
		c.mu.Lock()
		if !slices.ContainsFunc(c.channels, func(x string) bool { return c.casefold(x) == c.casefold(ch) }) {
			c.channels = append(c.channels, ch)
			c.saveChannelsLocked()
		}
		c.members[c.casefold(ch)] = nil
		q, waiting := c.joining[c.casefold(ch)]
		delete(c.joining, c.casefold(ch))
		c.mu.Unlock()
		chat := c.chatOf(ch)
		if waiting {
			for _, ev := range q {
				ev.Chat = chat
				c.Post(ev)
			}
		}
		c.service(ch, i18n.T("irc_you_joined", ch), e)
		return
	}
	c.addMember(ch, nick)
	c.service(ch, i18n.T("irc_joined", nick), e)
}

func (c *Client) onPart(e ircmsg.Message) {
	if len(e.Params) < 1 {
		return
	}
	ch, nick := e.Params[0], e.Nick()
	if c.isMe(nick) {
		c.mu.Lock()
		delete(c.members, c.casefold(ch))
		c.mu.Unlock()
		return // Leave already told the UI (EvChatGone)
	}
	c.dropMember(ch, nick)
	reason := ""
	if len(e.Params) > 1 {
		reason = " (" + e.Params[1] + ")"
	}
	c.service(ch, i18n.T("irc_left", nick)+reason, e)
}

func (c *Client) onKick(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	ch, victim := e.Params[0], e.Params[1]
	reason := ""
	if len(e.Params) > 2 {
		reason = " (" + e.Params[2] + ")"
	}
	c.dropMember(ch, victim)
	if c.isMe(victim) {
		// Kicked: the room stays in the list, the next connection tries it
		// again — the line says so.
		c.service(ch, i18n.T("irc_you_kicked", e.Nick())+reason, e)
		return
	}
	c.service(ch, i18n.T("irc_kicked", victim, e.Nick())+reason, e)
}

func (c *Client) onQuit(e ircmsg.Message) {
	nick := e.Nick()
	reason := ""
	if len(e.Params) > 0 {
		reason = " (" + e.Params[0] + ")"
	}
	for _, ch := range c.channelsOf(nick) {
		c.dropMember(ch, nick)
		c.service(ch, i18n.T("irc_quit", nick)+reason, e)
	}
}

func (c *Client) onNick(e ircmsg.Message) {
	if len(e.Params) < 1 {
		return
	}
	old, now := e.Nick(), e.Params[0]
	for _, ch := range c.channelsOf(old) {
		c.dropMember(ch, old)
		c.addMember(ch, now)
		c.service(ch, i18n.T("irc_renamed", old, now), e)
	}
	if c.casefold(old) == c.casefold(now) {
		return
	}
	// A private chat follows the person: the old window is gone, the new
	// one opens at the next line. ponytail: no move of the history.
	c.mu.Lock()
	_, open := c.queries[c.casefold(old)]
	delete(c.queries, c.casefold(old))
	c.mu.Unlock()
	if open {
		c.Post(model.EvChatGone{ChatID: c.chatID(old)})
	}
}

func (c *Client) onTopic(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	c.service(e.Params[0], i18n.T("irc_topic_set", e.Nick(), e.Params[1]), e)
}

func (c *Client) onMode(e ircmsg.Message) {
	if len(e.Params) < 2 || !isChannel(e.Params[0]) {
		return
	}
	c.service(e.Params[0], i18n.T("irc_mode", e.Nick(), strings.Join(e.Params[1:], " ")), e)
}

// onNames : 353 "<me> <=|*|@> <channel> :nick nick…", gathered until 366.
func (c *Client) onNames(e ircmsg.Message) {
	if len(e.Params) < 4 {
		return
	}
	k := c.casefold(e.Params[2])
	c.mu.Lock()
	c.names[k] = append(c.names[k], strings.Fields(e.Params[3])...)
	c.mu.Unlock()
}

// onEndOfNames : the list is whole — it becomes the member list, and answers
// the Participants call waiting for it.
func (c *Client) onEndOfNames(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	k := c.casefold(e.Params[1])
	c.mu.Lock()
	names := c.names[k]
	delete(c.names, k)
	// Only a room we are in has a member list: the key is set by the self
	// JOIN. A 366 for any other room (/names #other) must not create one, or
	// Resolve would read it as "already joined" and skip the JOIN.
	if _, joined := c.members[k]; joined {
		c.members[k] = nil
		for _, n := range names {
			c.members[k] = append(c.members[k], strings.TrimLeft(n, "~&@%+"))
		}
	}
	chat := c.naming[k]
	delete(c.naming, k)
	reply, asked := c.asked["names:"+k]
	delete(c.asked, "names:"+k)
	c.mu.Unlock()
	if asked { // /names: the list as it comes, prefixes and all
		c.Post(model.EvLines{ChatID: reply, Lines: []string{i18n.T("irc_names", e.Params[1], strings.Join(names, " "))}})
	}
	if chat == nil {
		return
	}
	ev := model.EvParticipants{ChatID: chat.ID}
	for _, n := range names {
		bare := strings.TrimLeft(n, "~&@%+")
		ev.Lines = append(ev.Lines, model.Participant{Text: n, Query: bare})
	}
	c.Post(ev)
}

// onWhoisLine : one numeric of a WHOIS, kept raw — formatWhois lays the whole
// lot out on 318.
func (c *Client) onWhoisLine(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	c.mu.Lock()
	r := c.whois[c.casefold(e.Params[1])]
	if r != nil {
		r.raw = append(r.raw, e)
	}
	_, whowas := c.asked["whowas"]
	c.mu.Unlock()
	// 312 also answers a WHOWAS: the server the gone nick was last on.
	if r == nil && whowas && e.Command == ircevent.RPL_WHOISSERVER {
		c.lines("whowas", i18n.T("irc_whowas_server", e.Params[1], strings.Join(e.Params[2:], " ")))
	}
}

func (c *Client) onEndOfWhois(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	c.mu.Lock()
	r := c.whois[c.casefold(e.Params[1])]
	delete(c.whois, c.casefold(e.Params[1]))
	c.mu.Unlock()
	if r != nil {
		c.Post(model.EvWhois{ChatID: r.chatID, Lines: formatWhois(e.Params[1], append(r.raw, e))})
	}
}

// onNoSuchNick : 401 answers a WHOIS of a nick that is not there — and a
// PRIVMSG to one, which the UI sees as a plain error line.
func (c *Client) onNoSuchNick(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	nick := e.Params[1]
	c.mu.Lock()
	r := c.whois[c.casefold(nick)]
	delete(c.whois, c.casefold(nick))
	c.mu.Unlock()
	if r != nil {
		c.Post(model.EvWhois{ChatID: r.chatID, Err: i18n.T("irc_no_such_nick")})
		return
	}
	c.Post(model.EvLog{Level: "ERROR", Msg: c.net() + ": " + nick + ": " + i18n.T("irc_no_such_nick")})
}

// onChannelError : a JOIN refused (or a send to a room that refuses it). The
// Resolve waiting for the room gets the answer; otherwise the line shows.
func (c *Client) onChannelError(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	ch := e.Params[1]
	reason := strings.Join(e.Params[2:], " ")
	c.mu.Lock()
	q, waiting := c.joining[c.casefold(ch)]
	delete(c.joining, c.casefold(ch))
	c.mu.Unlock()
	if waiting {
		for _, ev := range q {
			ev.Err = reason
			c.Post(ev)
		}
		return
	}
	c.Post(model.EvLog{Level: "ERROR", Msg: c.net() + ": " + ch + ": " + reason})
}

// saveChannelsLocked writes the room list back; c.mu held by the caller.
func (c *Client) saveChannelsLocked() {
	if c.cfg.SaveChannels == nil {
		return
	}
	list := slices.Clone(c.channels)
	if err := c.cfg.SaveChannels(list); err != nil {
		c.Post(model.EvLog{Level: "ERROR", Msg: c.net() + ": " + err.Error()})
	}
}

// send writes one raw command; a connection that is down is the error.
func (c *Client) send(cmd string, params ...string) error {
	if c.conn == nil || !c.conn.Connected() {
		return errors.New(i18n.T("irc_not_connected", c.net()))
	}
	return c.conn.Send(cmd, params...)
}

// sendRaw writes one line as it was typed (/quote); same guard as send, plus
// the CR/LF/NUL check the built commands get from ircmsg: a raw line carrying
// one of those would smuggle a second command onto the wire.
func (c *Client) sendRaw(line string) error {
	if strings.ContainsAny(line, "\r\n\x00") {
		return errors.New(i18n.T("irc_usage", usages["quote"]))
	}
	if c.conn == nil || !c.conn.Connected() {
		return errors.New(i18n.T("irc_not_connected", c.net()))
	}
	return c.conn.SendRaw(line)
}

// localIP : the address of the IRC socket, what DCC SEND announces when the
// configuration names none.
func (c *Client) localIP() string {
	if c.cfg.DCCIP != "" {
		return c.cfg.DCCIP
	}
	c.mu.Lock()
	s := c.sock
	c.mu.Unlock()
	if s == nil {
		return ""
	}
	if a, ok := s.LocalAddr().(*net.TCPAddr); ok {
		return a.IP.String()
	}
	return ""
}

// discard drains r: the acks of a DCC SEND, read and forgotten.
func discard(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		if _, err := r.Read(buf); err != nil {
			return
		}
	}
}
