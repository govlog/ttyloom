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
	"log"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	// PasswordWithoutTLS sends Password on a connection without TLS, where
	// SASL PLAIN and IDENTIFY both carry it in clear; off, it is withheld.
	PasswordWithoutTLS bool
	Channels           []string
	DCCIP              string
	DCCPorts           string
	Ignores            []string // nick!user@host masks whose lines are dropped (/ignore)
	// SaveChannels asks for the room list (Channels) to be written back to
	// the configuration — after a join or a part, so that the next start
	// finds the same rooms. It may wait for the UI: never called with c.mu
	// held. The list is read when it is written, so two calls in either order
	// leave the latest one.
	SaveChannels func()
	// SaveIgnores : the same for the ignore masks (Ignores), after a /ignore,
	// so that the next start drops the same lines.
	SaveIgnores func()
	// Dial : the tests plug a pipe here; nil = net.Dialer.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

type Client struct {
	model.Poster
	cfg     Config
	conn    *ircevent.Connection
	ids     idGen
	casemap atomic.Value

	out  *pacer // user-initiated lines: burst outBurst, then one every outFill
	ctcp *pacer // CTCP answers: burst 3, one every 2 s

	mu       sync.Mutex
	channels []string                     // rooms to be in, "#room" or "#room key": the config list, joined at each connection
	members  map[string]map[string]string // folded channel -> folded nick -> nick as seen (NAMES, JOIN, PART…)
	names    map[string][]string          // NAMES in progress, folded channel -> nicks
	queries  map[string]string            // folded nick -> nick, private chats open
	joining  map[string][]model.EvChat    // folded channel -> Resolve query waiting for the JOIN
	naming   map[string]*model.Chat
	whois    map[string]*whoisReq
	offers   map[string]dccOffer  // folded nick -> last DCC offer received
	xfers    []string             // DCC transfers running, one label each
	asked    map[string]int64     // kind of command ("who", "motd", "ctcp:<nick>"…) -> chat its answer goes to
	bans     []banReq             // bans waiting for their USERHOST answer, in the order asked
	motd     []string             // MOTD lines gathered until 376/422
	ignores  []string             // /ignore masks, nick!user@host with * and ?
	pings    map[string]time.Time // folded nick -> CTCP PING sent at
	ready    bool                 // EvReady posted (once per Run)
	up       bool                 // connected() ran on this connection: a /motd ends with 376 too
	sock     net.Conn
	stopped  bool            // Run asked to end during the first connection: a socket dialed after is closed at once
	runCtx   context.Context // the context of Run: a dial still running when it ends gives up
}

// whoisReq : one WHOIS in flight, the numerics gathered until 318.
type whoisReq struct {
	chatID int64
	raw    []ircmsg.Message
}

// New builds the client and its connection, callbacks wired; nothing
// connects before Run. The UI may call a method as soon as New returns: the
// connection is there for it, never a nil one Run has not set yet.
func New(cfg Config, events chan<- model.Event) *Client {
	c := &Client{Poster: model.Poster{Events: events}, cfg: cfg,
		out: newPacer(outBurst, outFill), ctcp: newPacer(3, 2*time.Second),
		members: map[string]map[string]string{}, names: map[string][]string{}, queries: map[string]string{},
		joining: map[string][]model.EvChat{}, naming: map[string]*model.Chat{}, whois: map[string]*whoisReq{},
		offers: map[string]dccOffer{}, asked: map[string]int64{}, pings: map[string]time.Time{}}
	for _, m := range cfg.Ignores { // a hand-written "bob" is the mask bob!*@*
		c.ignores = append(c.ignores, ignoreMask(m))
	}
	for _, ch := range cfg.Channels {
		if IsChannel(ch) && !slices.ContainsFunc(c.channels, func(x string) bool { return roomName(x) == roomName(ch) }) {
			c.channels = append(c.channels, ch)
		}
	}
	c.conn = c.build()
	c.wire(c.conn)
	return c
}

// net : the network key, in every log line — with several IRC networks the
// bare text says nothing about which one speaks.
func (c *Client) net() string { return Net(c.cfg.Name) }

// Caps : whois (WHOIS), name resolution (/join, /query) and leaving a room;
// the nick is the identity of a person (NameIsID). No history, no edit, no
// reaction, no read receipt, no search: IRC has none.
func (c *Client) Caps() model.Caps {
	return model.Caps{Whois: true, Resolve: true, Leave: true, NickWhois: true, NameIsID: true}
}

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
	port := cfg.Port
	if port == 0 { // a table written by hand with no port: the usual one
		port = 6667
		if cfg.TLS {
			port = 6697
		}
	}
	dial := cfg.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second}).DialContext
	}
	conn := &ircevent.Connection{
		Server:        net.JoinHostPort(cfg.Host, strconv.Itoa(port)),
		Nick:          cfg.Nick,
		User:          user,
		RealName:      real,
		UseTLS:        cfg.TLS,
		TLSConfig:     &tls.Config{ServerName: cfg.Host},
		RequestCaps:   []string{"server-time"},
		SASLOptional:  true,  // a network without SASL identifies to NickServ after 001
		EnableCTCP:    false, // CTCP parsed by onCTCP: ignore list and rate limit on the answers
		QuitMessage:   "ttyloom",
		ReconnectFreq: 20 * time.Second,
		Log:           log.New(logWriter{c}, "", 0),
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c.mu.Lock()
			run := c.runCtx
			c.mu.Unlock()
			if run != nil { // a stop of Run ends the dial too, not its timeout
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				stop := context.AfterFunc(run, cancel)
				defer stop()
			}
			s, err := dial(ctx, network, addr)
			if err == nil {
				c.mu.Lock()
				c.sock = s
				if c.stopped {
					s.Close()
				}
				c.mu.Unlock()
			}
			return s, err
		},
	}
	if pw := c.password(); pw != "" {
		conn.SASLLogin, conn.SASLPassword = cfg.Nick, pw
	}
	return conn
}

// password : the one that may go on the wire — none without TLS unless the
// configuration says so, SASL PLAIN and IDENTIFY both carrying it in clear.
func (c *Client) password() string {
	if c.cfg.TLS || c.cfg.PasswordWithoutTLS {
		return c.cfg.Password
	}
	return ""
}

// Run connects (blocking until 001, the error of a first connection is the
// error of Run), then reconnects on its own until ctx ends.
func (c *Client) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil // a launch already given up (a logout during the token read)
	}
	if c.cfg.Password != "" && c.password() == "" {
		c.status(i18n.T("irc_password_withheld", c.net()))
	}
	c.mu.Lock()
	c.runCtx = ctx
	c.mu.Unlock()
	// A stop while the first connection waits for a silent server: the
	// socket goes at once — the handshake would wait a minute, and the
	// network would look up meanwhile.
	stop := context.AfterFunc(ctx, func() {
		c.mu.Lock()
		c.stopped = true
		c.mu.Unlock()
		c.conn.Quit()
		c.closeSock()
	})
	err := c.conn.Connect()
	stop()
	if err != nil {
		if ctx.Err() != nil {
			return nil // the stop asked for, not a failure
		}
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
			c.closeSock()
			c.conn.Reconnect() // wakes Loop when it waits between two connections
			<-done
		}
	case <-done:
	}
	return nil
}

// closeSock closes the socket by hand: a server that does not close on QUIT,
// or one that never answers.
func (c *Client) closeSock() {
	c.mu.Lock()
	s := c.sock
	c.mu.Unlock()
	if s != nil {
		s.Close()
	}
}

// wire hangs the callbacks of the protocol on conn. Every callback runs on
// the read goroutine of ircevent, one at a time: the maps are locked all the
// same, the Backend methods reach them from other goroutines.
func (c *Client) wire(conn *ircevent.Connection) {
	conn.AddConnectCallback(func(ircmsg.Message) { c.connected() })
	conn.AddDisconnectCallback(func(ircmsg.Message) { c.resetConn() })
	conn.AddCallback("PRIVMSG", c.onPrivmsg)
	conn.AddCallback("NOTICE", c.onNotice)
	conn.AddCallback("JOIN", c.onJoin)
	conn.AddCallback("PART", c.onPart)
	conn.AddCallback("KICK", c.onKick)
	conn.AddCallback("QUIT", c.onQuit)
	conn.AddCallback("NICK", c.onNick)
	conn.AddCallback("TOPIC", c.onTopic)
	conn.AddCallback("MODE", c.onMode)
	conn.AddCallback("INVITE", func(e ircmsg.Message) {
		if len(e.Params) > 1 {
			c.status(i18n.T("irc_invited", c.net(), e.Nick(), e.Params[1]))
		}
	})
	// The answers of SASL: a refused password must show (no IDENTIFY follows
	// it), and the success names the account.
	for _, code := range []string{ircevent.RPL_LOGGEDIN, ircevent.ERR_NICKLOCKED, ircevent.ERR_SASLFAIL} {
		conn.AddCallback(code, func(e ircmsg.Message) {
			if len(e.Params) > 1 {
				c.status(c.net() + ": " + e.Params[len(e.Params)-1])
			}
		})
	}
	conn.AddCallback(ircevent.RPL_TOPIC, func(e ircmsg.Message) { // 332 on join, and answer of /topic
		if len(e.Params) < 3 {
			return
		}
		c.mu.Lock()
		_, joined := c.members[c.casefold(e.Params[1])]
		reply, pending := c.asked["topic"]
		delete(c.asked, "topic")
		c.mu.Unlock()
		if joined { // the line of a room we are not in (/topic #other) would make a chat of it
			c.service(e.Params[1], i18n.T("irc_topic_is", e.Params[2]), e)
		}
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
	// 470 is a forward (+f, a ban to ##fix_your_connection): its text names
	// the room the server sends us to. 437 is a room held during a split
	// (and a nick refused, which no /join waits for).
	for _, code := range []string{ircevent.ERR_NOSUCHCHANNEL, ircevent.ERR_TOOMANYCHANNELS, ircevent.ERR_CHANNELISFULL,
		ircevent.ERR_INVITEONLYCHAN, ircevent.ERR_BANNEDFROMCHAN, ircevent.ERR_BADCHANNELKEY, ircevent.ERR_BADCHANMASK,
		ircevent.ERR_NEEDREGGEDNICK, ircevent.ERR_CANNOTSENDTOCHAN, ircevent.ERR_LINKCHANNEL, ircevent.ERR_UNAVAILRESOURCE} {
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
// then the rooms of the list joined again and NickServ told when the server
// has no SASL at all. One that offered SASL and refused the password is not
// told a second time, and on a network without services (EFnet, IRCnet)
// anyone may sit on the nick NickServ.
func (c *Client) connected() {
	c.mu.Lock()
	again := c.up // a /motd after registration: nothing is sent twice
	c.up = true
	c.mu.Unlock()
	if again {
		return
	}
	mode := c.conn.ISupport()["CASEMAPPING"]
	switch mode {
	case "ascii", "rfc1459", "rfc1459-strict", "strict-rfc1459":
	case "rfc8265", "rfc7613": // Ergo: [ ] \ ~ are letters of their own there
		// ponytail: PRECIS also folds non-ASCII letters (Éva = éva), which
		// casefold does not; ascii is the closest mapping it has.
		mode = "ascii"
	default:
		mode = "rfc1459"
	}
	c.casemap.Store(mode)
	c.mu.Lock()
	first := !c.ready
	c.ready = true
	chans := slices.Clone(c.channels)
	c.mu.Unlock()
	if first {
		c.Post(model.EvReady{SelfID: chatID(c.me(), "ascii"), SelfName: c.me()})
	} else {
		c.Post(model.EvSelfName{Name: c.me()}) // a reconnection, maybe under another nick
	}
	c.Post(model.EvConnected{})
	if _, sasl := c.conn.AcknowledgedCaps()["sasl"]; !sasl && c.password() != "" {
		c.conn.Send("PRIVMSG", "NickServ", "IDENTIFY "+c.password())
	}
	// The rooms in as few lines as fit (JOIN #a,#b,…): a line per room in a
	// burst is what an "Excess Flood" kill counts.
	var line string
	for _, ch := range chans {
		switch {
		case strings.Contains(ch, " "): // "#room key": a line of its own
			c.conn.Send("JOIN", strings.Fields(ch)...)
		case line == "":
			line = ch
		case len(line)+1+len(ch) > maxLine:
			c.conn.Send("JOIN", line)
			line = ch
		default:
			line += "," + ch
		}
	}
	if line != "" {
		c.conn.Send("JOIN", line)
	}
}

// roomName : the room of an entry of the list, its key left out.
// roomKey : the key saved with room ch, "" with none.
func (c *Client) roomKey(ch string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, x := range c.channels {
		if name, key, ok := strings.Cut(x, " "); ok && c.casefold(name) == c.casefold(ch) {
			return strings.TrimSpace(key)
		}
	}
	return ""
}

// remember puts room ch in the list. A room joined with a key keeps it, a new
// key replaces the old one: every later connection needs it, or the server
// answers 475. c.mu held; true when the list changed.
func (c *Client) remember(ch, key string) bool {
	key = strings.TrimSpace(key)
	i := slices.IndexFunc(c.channels, func(x string) bool { return c.casefold(roomName(x)) == c.casefold(ch) })
	entry := ch
	if i >= 0 {
		entry = roomName(c.channels[i])
	}
	if key != "" {
		entry += " " + key
	}
	switch {
	case i < 0:
		c.channels = append(c.channels, entry)
	case key != "" && c.channels[i] != entry:
		c.channels[i] = entry
	default:
		return false
	}
	return true
}

func roomName(entry string) string {
	n, _, _ := strings.Cut(entry, " ")
	return n
}

// resetConn forgets what the connection that dropped owned — the member
// lists, the answers being gathered — and answers the requests that wait for
// it with an error: the next connection rebuilds the lists (its JOINs and
// NAMES), and its answers must not be taken for the ones of the old requests.
func (c *Client) resetConn() {
	c.mu.Lock()
	c.up = false
	c.members, c.names = map[string]map[string]string{}, map[string][]string{}
	c.motd, c.bans = nil, nil
	clear(c.asked)
	clear(c.pings)
	joining, whois, naming := c.joining, c.whois, c.naming
	c.joining, c.whois, c.naming = map[string][]model.EvChat{}, map[string]*whoisReq{}, map[string]*model.Chat{}
	c.mu.Unlock()
	err := i18n.T("irc_not_connected", c.net())
	for _, q := range joining {
		for _, ev := range q {
			ev.Err = err
			c.Post(ev)
		}
	}
	for _, r := range whois {
		c.Post(model.EvWhois{ChatID: r.chatID, Err: err})
	}
	for _, chat := range naming {
		c.Post(model.EvParticipants{ChatID: chat.ID, Err: err})
	}
	c.Post(model.EvDisconnected{})
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
	to, _ := c.statusRoom(e.Params[0])
	if IsChannel(to) {
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

// statusRoom : for a message to the ops or the voices of a room (@#room,
// +#room: the prefixes of ISUPPORT STATUSMSG), the room and the prefix;
// otherwise to itself and "".
func (c *Client) statusRoom(to string) (string, string) {
	pre := c.conn.ISupport()["STATUSMSG"]
	if pre == "" {
		pre = "@+"
	}
	if room := strings.TrimLeft(to, pre); room != to && IsChannel(room) {
		return room, to[:len(to)-len(room)]
	}
	return to, ""
}

// prefix puts p before the text of m, its spans moved along.
func prefix(m *model.Msg, p string) {
	n := utf8.RuneCountInString(p)
	m.Text = p + m.Text
	for i := range m.Entities {
		m.Entities[i].Start += n
		m.Entities[i].End += n
	}
}

func (c *Client) onPrivmsg(e ircmsg.Message) {
	if c.ignored(e) || len(e.Params) < 2 {
		return
	}
	if t := e.Params[1]; len(t) > 1 && t[0] == 1 {
		c.onCTCP(e, strings.TrimSuffix(t[1:], "\x01"))
		return
	}
	chat := c.target(e)
	m := c.msgOf(e, chat, e.Params[1])
	if room, pre := c.statusRoom(e.Params[0]); pre != "" { // the room line says who it went to
		prefix(&m, "["+pre+room+"] ")
	}
	c.Post(model.EvNewMessage{Msg: m, Chat: chat})
}

// onNotice : a notice to a channel shows there as "-nick- text"; one to us
// from a person (NickServ's answers) belongs to the private chat with them —
// the UI keeps it in window 0 while that chat has no window, as ircii does —
// and one from the server goes to window 0.
func (c *Client) onNotice(e ircmsg.Message) {
	if len(e.Params) < 2 || (e.Nick() != "" && c.ignored(e)) {
		return
	}
	// A NOTICE wrapped in \x01 is the answer of a /ctcp, not a message.
	if t := e.Params[1]; len(t) > 2 && t[0] == 1 && t[len(t)-1] == 1 && e.Nick() != "" {
		c.onCTCPReply(e.Nick(), t[1:len(t)-1])
		return
	}
	if room, pre := c.statusRoom(e.Params[0]); IsChannel(room) {
		chat := c.chatOf(room)
		m := c.msgOf(e, chat, e.Params[1])
		prefix(&m, "-"+e.Nick()+"- ")
		if pre != "" {
			prefix(&m, "["+pre+room+"] ")
		}
		m.Notice = true // no hook answers a notice (IRC convention)
		c.Post(model.EvNewMessage{Msg: m, Chat: chat})
		return
	}
	from := e.Nick()
	if strings.Contains(e.Source, "!") { // nick!user@host: a person, never a server
		chat := c.chatOf(from)
		m := c.msgOf(e, chat, e.Params[1])
		prefix(&m, "-"+from+"- ")
		m.Notice = true
		c.Post(model.EvNewMessage{Msg: m, Chat: chat})
		return
	}
	if from == "" {
		from = e.Source
	}
	clean, _ := spansOf(e.Params[1])
	c.status(c.net() + ": -" + from + "- " + clean)
}

// status : a line of window 0, one the user has to see (a notice, an invite,
// a refused password) — an EvLog under ERROR stays in /debug.
func (c *Client) status(text string) { c.Post(model.EvLines{Lines: []string{text}}) }

// onAction : CTCP ACTION, "* nick does" in italics.
func (c *Client) onAction(e ircmsg.Message, text string) {
	chat := c.target(e)
	m := c.msgOf(e, chat, text)
	prefix(&m, "* "+e.Nick()+" ")
	m.Entities = append([]model.Span{{Start: 0, End: len([]rune(m.Text)), Kind: model.SpanItalic}}, m.Entities...)
	m.Action = true
	c.Post(model.EvNewMessage{Msg: m, Chat: chat})
}

// onDCC : DCC SEND becomes a file offer in the private chat of the sender;
// any other DCC (CHAT, RESUME…) is dropped.
func (c *Client) onDCC(e ircmsg.Message, args string) {
	kind, rest, _ := strings.Cut(args, " ")
	if !strings.EqualFold(kind, "SEND") {
		return
	}
	off, err := parseOffer(e.Nick(), rest, c.loopbackDCC())
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

func (c *Client) onJoin(e ircmsg.Message) {
	if len(e.Params) < 1 {
		return
	}
	ch, nick := e.Params[0], e.Nick()
	if c.isMe(nick) {
		c.mu.Lock()
		q, waiting := c.joining[c.casefold(ch)]
		delete(c.joining, c.casefold(ch))
		key := ""
		for _, ev := range q {
			if _, k, _ := strings.Cut(ev.Query, " "); strings.TrimSpace(k) != "" {
				key = k
			}
		}
		changed := c.remember(ch, key)
		c.members[c.casefold(ch)] = map[string]string{}
		c.mu.Unlock()
		if changed {
			c.saveChannels()
		}
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
	// An ignored person is still counted (the member list must stay right);
	// only the line is dropped — same below for PART, QUIT, NICK, TOPIC, MODE.
	c.addMember(ch, nick)
	if c.ignored(e) {
		return
	}
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
	if c.ignored(e) {
		return
	}
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
	if c.isMe(victim) {
		// Kicked: out of the room, so /join sends the JOIN again; the room
		// stays in the list, the next connection tries it again — the line
		// says so.
		c.mu.Lock()
		delete(c.members, c.casefold(ch))
		c.mu.Unlock()
		c.service(ch, i18n.T("irc_you_kicked", e.Nick())+reason, e)
		return
	}
	c.dropMember(ch, victim)
	c.service(ch, i18n.T("irc_kicked", victim, e.Nick())+reason, e)
}

func (c *Client) onQuit(e ircmsg.Message) {
	nick := e.Nick()
	reason := ""
	if len(e.Params) > 0 {
		reason = " (" + e.Params[0] + ")"
	}
	quiet := c.ignored(e)
	for _, ch := range c.channelsOf(nick) {
		c.dropMember(ch, nick)
		if !quiet {
			c.service(ch, i18n.T("irc_quit", nick)+reason, e)
		}
	}
}

func (c *Client) onNick(e ircmsg.Message) {
	if len(e.Params) < 1 {
		return
	}
	old, now := e.Nick(), e.Params[0]
	if c.isMe(now) { // ours: the library has switched to the new nick already
		c.Post(model.EvSelfName{Name: now})
	}
	quiet := c.ignored(e)
	for _, ch := range c.channelsOf(old) {
		c.dropMember(ch, old)
		c.addMember(ch, now)
		if !quiet {
			c.service(ch, i18n.T("irc_renamed", old, now), e)
		}
	}
	if c.casefold(old) == c.casefold(now) {
		return
	}
	// A private chat follows the person: the new nick opens its own window at
	// its next line. The old one stays, with its history (on IRC the disk
	// cache is the only copy) and one line that says where the person went.
	c.mu.Lock()
	_, open := c.queries[c.casefold(old)]
	delete(c.queries, c.casefold(old))
	if open {
		c.queries[c.casefold(now)] = now
	}
	c.mu.Unlock()
	if open && !quiet {
		c.service(old, i18n.T("irc_renamed", old, now), e)
	}
}

func (c *Client) onTopic(e ircmsg.Message) {
	if len(e.Params) < 2 || c.ignored(e) {
		return
	}
	c.service(e.Params[0], i18n.T("irc_topic_set", e.Nick(), e.Params[1]), e)
}

func (c *Client) onMode(e ircmsg.Message) {
	if len(e.Params) < 2 || !IsChannel(e.Params[0]) || c.ignored(e) {
		return
	}
	c.service(e.Params[0], i18n.T("irc_mode", e.Nick(), strings.Join(e.Params[1:], " ")), e)
}

// maxGather : lines kept of a NAMES, WHOIS or MOTD answer. A server that
// never sends the end numeric must not grow the heap without bound.
const maxGather = 10000

// onWhoisLine : one numeric of a WHOIS, kept raw — formatWhois lays the whole
// lot out on 318.
func (c *Client) onWhoisLine(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	c.mu.Lock()
	r := c.whois[c.casefold(e.Params[1])]
	if r != nil && len(r.raw) < maxGather {
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

// saveChannels asks for the room list to be written back; c.mu not held.
func (c *Client) saveChannels() {
	if c.cfg.SaveChannels != nil {
		c.cfg.SaveChannels()
	}
}

// Channels : the rooms to be in, as the configuration keeps them ("#room",
// "#room key").
func (c *Client) Channels() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.channels)
}

// Ignores : the /ignore masks.
func (c *Client) Ignores() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.ignores)
}

// send writes one raw command; a connection that is down is the error.
func (c *Client) send(cmd string, params ...string) error {
	if !c.conn.Connected() {
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
	if !c.conn.Connected() {
		return errors.New(i18n.T("irc_not_connected", c.net()))
	}
	return c.conn.SendRaw(line)
}

// localIP : the address DCC SEND announces — dcc_ip, else the one of the
// IRC socket.
func (c *Client) localIP() string {
	if c.cfg.DCCIP != "" {
		return c.cfg.DCCIP
	}
	return c.sockIP()
}

// sockIP : the local address of the IRC socket, "" while there is none (or
// when it is not TCP: the pipe of the tests).
func (c *Client) sockIP() string {
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
