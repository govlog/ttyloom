package irc

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/model"
)

var _ model.Backend = (*Client)(nil)

// fakeServer : the other end of a net.Pipe, read line by line. The client
// under test sees a real registration; the test drives the rest by hand.
type fakeServer struct {
	t     *testing.T
	conn  net.Conn
	lines chan string
	mu    sync.Mutex
	sasl  bool // advertise the sasl cap
}

func (s *fakeServer) send(format string, args ...any) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintf(s.conn, format+"\r\n", args...); err != nil {
		s.t.Errorf("server write: %v", err)
	}
}

// expect waits for the first line starting with prefix; the others are dropped.
func (s *fakeServer) expect(prefix string) string {
	s.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case l := <-s.lines:
			if strings.HasPrefix(l, prefix) {
				return l
			}
		case <-deadline:
			s.t.Fatalf("no %q line from the client", prefix)
			return ""
		}
	}
}

// never fails when a line with prefix shows up within d.
func (s *fakeServer) never(prefix string, d time.Duration) {
	s.t.Helper()
	deadline := time.After(d)
	for {
		select {
		case l := <-s.lines:
			if strings.HasPrefix(l, prefix) {
				s.t.Fatalf("unexpected %q line: %s", prefix, l)
			}
		case <-deadline:
			return
		}
	}
}

// register answers the handshake until 001: CAP LS, the CAP REQs, SASL when
// advertised, then the welcome once CAP END (or NICK/USER without caps) is in.
func (s *fakeServer) register() {
	for l := range s.lines {
		f := strings.Fields(l)
		switch {
		case strings.HasPrefix(l, "CAP LS"):
			caps := "server-time"
			if s.sasl {
				caps += " sasl"
			}
			s.send(":srv CAP * LS :%s", caps)
		case strings.HasPrefix(l, "CAP REQ"):
			c := strings.TrimPrefix(f[2], ":")
			if c == "sasl" && !s.sasl {
				s.send(":srv CAP * NAK :%s", c)
			} else {
				s.send(":srv CAP * ACK :%s", c)
			}
		case l == "AUTHENTICATE PLAIN":
			s.send("AUTHENTICATE +")
		case strings.HasPrefix(l, "AUTHENTICATE "):
			raw, _ := base64.StdEncoding.DecodeString(f[1])
			if string(raw) != "me\x00me\x00secret" {
				s.send(":srv 904 me :SASL authentication failed")
			} else {
				s.send(":srv 903 me :SASL authentication successful")
			}
		case strings.HasPrefix(l, "CAP END"):
			s.send(":srv 001 me :Welcome")
			s.send(":srv 376 me :End of MOTD")
			return
		}
	}
}

// savedLists : the channel lists SaveChannels was given, in order.
type savedLists struct {
	mu    sync.Mutex
	lists []string
}

func (s *savedLists) add(l []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lists = append(s.lists, strings.Join(l, ","))
	return nil
}

func (s *savedLists) get() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lists...)
}

// start builds a client on a pipe to a fake server, runs it, and waits for
// the registration. SaveChannels records its calls in saved.
func start(t *testing.T, cfg Config, sasl bool) (*Client, *fakeServer, chan model.Event, *savedLists) {
	t.Helper()
	cs, ss := net.Pipe()
	s := &fakeServer{t: t, conn: ss, lines: make(chan string, 64), sasl: sasl}
	go func() {
		sc := bufio.NewScanner(ss)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "QUIT") { // a real server closes on QUIT
				ss.Close()
				break
			}
			s.lines <- sc.Text()
		}
		close(s.lines)
	}()
	var saved savedLists
	cfg.Name, cfg.Host, cfg.Port, cfg.Nick = "test", "irc.example", 6667, "me"
	cfg.Dial = func(context.Context, string, string) (net.Conn, error) { return cs, nil }
	cfg.SaveChannels = saved.add
	events := make(chan model.Event, 256)
	c := New(cfg, events)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	s.register()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not end")
		}
		cs.Close()
		ss.Close()
	})
	waitFor[model.EvReady](t, events)
	waitFor[model.EvConnected](t, events)
	return c, s, events, &saved
}

// waitFor gives the first event of type T, the others dropped.
func waitFor[T any](t *testing.T, events chan model.Event) T {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			if v, ok := ev.(T); ok {
				return v
			}
		case <-deadline:
			var zero T
			t.Fatalf("no %T event", zero)
			return zero
		}
	}
}

// waitMsg gives the first EvNewMessage matching f.
func waitMsg(t *testing.T, events chan model.Event, f func(model.EvNewMessage) bool) model.EvNewMessage {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			if m, ok := ev.(model.EvNewMessage); ok && f(m) {
				return m
			}
		case <-deadline:
			t.Fatal("no matching EvNewMessage")
		}
	}
}

func TestCaps(t *testing.T) {
	got := New(Config{}, nil).Caps()
	if got != (model.Caps{Whois: true, Resolve: true, Leave: true}) {
		t.Fatalf("caps: %+v", got)
	}
}

// SASL PLAIN with the password, no NickServ line, and the rooms of the list
// joined once registered; the JOIN echo becomes a service line of the room.
func TestRegisterSASLAndRejoin(t *testing.T) {
	_, s, events, _ := start(t, Config{Password: "secret", Channels: []string{"#go", "#go"}}, true)
	if l := s.expect("JOIN"); l != "JOIN #go" {
		t.Fatalf("join: %q", l)
	}
	s.send(":me!u@h JOIN #go")
	m := waitMsg(t, events, func(m model.EvNewMessage) bool { return m.Msg.Service != "" })
	if m.Chat.Kind != model.ChatGroup || m.Chat.Title != "#go" || !strings.Contains(m.Msg.Service, "#go") || m.Msg.ID == 0 {
		t.Fatalf("join service: %+v %+v", m.Chat, m.Msg)
	}
	s.never("PRIVMSG NickServ", 200*time.Millisecond)
}

// No sasl cap on the server: the password goes to NickServ after 001.
func TestNickServWithoutSASL(t *testing.T) {
	_, s, _, _ := start(t, Config{Password: "secret"}, false)
	if l := s.expect("PRIVMSG NickServ"); l != "PRIVMSG NickServ :IDENTIFY secret" {
		t.Fatalf("identify: %q", l)
	}
}

// A room message keeps its mIRC styles as spans; a private one opens the chat
// of the sender; an ACTION reads "* nick does" in italics.
func TestIncomingMessages(t *testing.T) {
	_, s, events, _ := start(t, Config{}, false)
	s.send("@time=2026-09-11T10:00:00.000Z :alice!a@h PRIVMSG #go :\x02hi\x02 there")
	m := waitFor[model.EvNewMessage](t, events)
	if m.Msg.Text != "hi there" || m.Msg.From != "alice" || m.Msg.Out || m.Chat.Title != "#go" ||
		m.Msg.Date.Year() != 2026 || len(m.Msg.Entities) != 1 || m.Msg.Entities[0] != (model.Span{Start: 0, End: 2, Kind: model.SpanBold}) {
		t.Fatalf("room message: %+v", m.Msg)
	}
	s.send(":bob!b@h PRIVMSG me :yo")
	m = waitFor[model.EvNewMessage](t, events)
	if m.Chat.Kind != model.ChatUser || m.Chat.Title != "bob" || m.Msg.Text != "yo" || m.Chat.ID != chatID("Bob") {
		t.Fatalf("private message: %+v %+v", m.Chat, m.Msg)
	}
	s.send(":alice!a@h PRIVMSG #go :\x01ACTION waves\x01")
	m = waitFor[model.EvNewMessage](t, events)
	if m.Msg.Text != "* alice waves" || len(m.Msg.Entities) != 1 || m.Msg.Entities[0].Kind != model.SpanItalic {
		t.Fatalf("action: %+v", m.Msg)
	}
	s.send(":NickServ!s@h NOTICE me :You are now identified")
	if l := waitFor[model.EvLog](t, events); !strings.Contains(l.Msg, "-NickServ- You are now identified") {
		t.Fatalf("notice: %+v", l)
	}
}

// A send goes out one PRIVMSG per line piece, then the receipt carries an id.
func TestSendSplitsAndAcks(t *testing.T) {
	c, s, events, _ := start(t, Config{}, false)
	c.Send(context.Background(), chatOf("#go"), strings.Repeat("word ", 100)+"\nsecond", 7)
	first := s.expect("PRIVMSG #go :")
	second := s.expect("PRIVMSG #go :")
	third := s.expect("PRIVMSG #go ")
	if len(first) > 400+len("PRIVMSG #go :") || !strings.HasPrefix(second, "PRIVMSG #go :word") || strings.TrimPrefix(third, "PRIVMSG #go ") != "second" {
		t.Fatalf("pieces: %q %q %q", first, second, third)
	}
	e := waitFor[model.EvSent](t, events)
	if e.TmpID != 7 || e.ID == 0 || e.Err != "" || e.ChatID != chatID("#go") {
		t.Fatalf("receipt: %+v", e)
	}
	c.SendStyled(context.Background(), chatOf("bob"), []model.Seg{{Text: "b", Bold: true}, {Text: " plain"}}, 8)
	if l := s.expect("PRIVMSG bob :"); l != "PRIVMSG bob :\x02b\x0f plain" {
		t.Fatalf("styled: %q", l)
	}
}

// /join: JOIN sent, the chat comes with the echo; the room list is saved.
// A refused room answers with the error. /leave: PART, the list shrinks.
func TestResolveJoinAndLeave(t *testing.T) {
	c, s, events, saved := start(t, Config{Channels: []string{"#go"}}, false)
	s.expect("JOIN #go")
	c.Resolve(context.Background(), "#new", true)
	s.expect("JOIN #new")
	s.send(":me!u@h JOIN #new")
	ch := waitFor[model.EvChat](t, events)
	if ch.Query != "#new" || ch.Chat == nil || ch.Chat.Title != "#new" || ch.Err != "" {
		t.Fatalf("resolved: %+v", ch)
	}
	c.Resolve(context.Background(), "#bad", true)
	s.expect("JOIN #bad")
	s.send(":srv 473 me #bad :Cannot join channel (+i)")
	if ch = waitFor[model.EvChat](t, events); ch.Query != "#bad" || !strings.Contains(ch.Err, "+i") {
		t.Fatalf("refused: %+v", ch)
	}
	c.Resolve(context.Background(), "carol", false)
	if ch = waitFor[model.EvChat](t, events); ch.Chat == nil || ch.Chat.Kind != model.ChatUser || ch.Chat.Title != "carol" {
		t.Fatalf("query: %+v", ch)
	}
	c.Leave(context.Background(), chatOf("#go"))
	s.expect("PART #go")
	if g := waitFor[model.EvChatGone](t, events); g.ChatID != chatID("#go") {
		t.Fatalf("gone: %+v", g)
	}
	time.Sleep(50 * time.Millisecond)
	if got := saved.get(); strings.Join(got, " ") != "#go,#new #new" {
		t.Fatalf("saved lists: %v", got)
	}
}

// WHOIS lines gathered until 318; NAMES answers the member box; a QUIT of a
// member lands in the rooms it was in.
func TestWhoisNamesQuit(t *testing.T) {
	c, s, events, _ := start(t, Config{}, false)
	c.Whois(context.Background(), chatOf("alice"))
	s.expect("WHOIS alice")
	s.send(":srv 311 me alice al host.example * :Alice A")
	s.send(":srv 319 me alice :@#go #dev")
	s.send(":srv 317 me alice 120 1700000000 :seconds idle, signon time")
	s.send(":srv 318 me alice :End of WHOIS")
	w := waitFor[model.EvWhois](t, events)
	if w.ChatID != chatID("alice") || len(w.Lines) != 3 || w.Lines[0] != "alice!al@host.example · Alice A" || w.Lines[1] != "@#go #dev" || !strings.Contains(w.Lines[2], "2m0s") {
		t.Fatalf("whois: %+v", w)
	}
	c.WhoisMember(context.Background(), "ghost")
	s.expect("WHOIS ghost")
	s.send(":srv 401 me ghost :No such nick")
	if w = waitFor[model.EvWhois](t, events); w.Err == "" || w.ChatID != chatID("ghost") {
		t.Fatalf("whois error: %+v", w)
	}
	c.Participants(context.Background(), chatOf("#go"))
	s.expect("NAMES #go")
	s.send(":srv 353 me = #go :@alice +bob me")
	s.send(":srv 366 me #go :End of NAMES")
	p := waitFor[model.EvParticipants](t, events)
	if len(p.Lines) != 3 || p.Lines[0].Text != "@alice" || p.Lines[0].Query != "alice" || p.Lines[1].Query != "bob" {
		t.Fatalf("participants: %+v", p)
	}
	s.send(":bob!b@h QUIT :bye")
	m := waitMsg(t, events, func(m model.EvNewMessage) bool { return m.Msg.Service != "" })
	if m.Chat.Title != "#go" || !strings.Contains(m.Msg.Service, "bob") || !strings.Contains(m.Msg.Service, "bye") {
		t.Fatalf("quit line: %+v %+v", m.Chat, m.Msg)
	}
	s.send(":alice!a@h NICK alicia")
	m = waitMsg(t, events, func(m model.EvNewMessage) bool { return m.Msg.Service != "" })
	if !strings.Contains(m.Msg.Service, "alicia") {
		t.Fatalf("nick line: %+v", m.Msg)
	}
}

// Every server page is empty and final: the scrollback is the disk cache.
func TestHistoryEmpty(t *testing.T) {
	c, _, events, _ := start(t, Config{}, false)
	c.LoadHistory(context.Background(), chatOf("#go"), 0, 50)
	if h := waitFor[model.EvHistory](t, events); !h.Done || len(h.Msgs) != 0 || h.ChatID != chatID("#go") {
		t.Fatalf("history: %+v", h)
	}
}

// --- DCC ---

func TestParseOffer(t *testing.T) {
	off, err := parseOffer("bob", `"my file.txt" 2130706433 5000 42`)
	if err != nil || off != (dccOffer{Nick: "bob", Name: "my file.txt", IP: "127.0.0.1", Port: 5000, Size: 42}) {
		t.Fatalf("quoted: %+v %v", off, err)
	}
	off, err = parseOffer("bob", "../../etc/passwd 127.0.0.1 5000 42")
	if err != nil || off.Name != "passwd" {
		t.Fatalf("path stripped: %+v %v", off, err)
	}
	off, err = parseOffer("bob", "x 0 0 42 token1")
	if err != nil || off.Port != 0 || off.Token != "token1" {
		t.Fatalf("reverse: %+v %v", off, err)
	}
	for _, bad := range []string{"x 127.0.0.1 5000 0", "x 127.0.0.1 5000", ".. 127.0.0.1 5000 42", "x 0 0 42", "x 999999999999 5000 42"} {
		if _, err := parseOffer("bob", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// An incoming DCC SEND is a file offer in the private chat; the download
// fetches it from the peer, acks on the way, and lands in the download dir.
func TestDCCGet(t *testing.T) {
	c, s, events, _ := start(t, Config{DCCIP: "127.0.0.1"}, false)
	payload := strings.Repeat("x", 100*1024)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	acks := make(chan int, 8)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.Write([]byte(payload))
		buf := make([]byte, 4)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
			acks <- int(buf[0])<<24 | int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3])
		}
	}()
	port := l.Addr().(*net.TCPAddr).Port
	s.send(":bob!b@h PRIVMSG me :\x01DCC SEND notes.txt 2130706433 %d %d\x01", port, len(payload))
	m := waitFor[model.EvNewMessage](t, events)
	if m.Chat.Title != "bob" || m.Msg.Media == nil || m.Msg.Media.Kind != model.MediaFile || m.Msg.Media.Size != int64(len(payload)) || !strings.Contains(m.Msg.Media.Label, "notes.txt") {
		t.Fatalf("offer: %+v", m.Msg)
	}
	if got := c.DCC(); len(got) != 1 || !strings.Contains(got[0], "notes.txt") {
		t.Fatalf("pending list: %v", got)
	}
	path := filepath.Join(t.TempDir(), "notes.txt")
	c.Download(context.Background(), m.Msg.Media, path)
	d := waitFor[model.EvDownloaded](t, events)
	if d.Err != "" || d.Path != path {
		t.Fatalf("downloaded: %+v", d)
	}
	if b, _ := os.ReadFile(path); string(b) != payload {
		t.Fatalf("content: %d bytes", len(b))
	}
	var last int
	for last != len(payload) {
		select {
		case last = <-acks:
		case <-time.After(2 * time.Second):
			t.Fatalf("final ack missing, last %d", last)
		}
	}
	if got := c.DCC(); len(got) != 0 {
		t.Fatalf("list after: %v", got)
	}
}

// A reverse offer (port 0, token): we listen and send the line back with
// our port, the peer connects.
func TestDCCGetReverse(t *testing.T) {
	c, s, events, _ := start(t, Config{DCCIP: "127.0.0.1"}, false)
	s.send(":bob!b@h PRIVMSG me :\x01DCC SEND r.bin 0 0 5 tok9\x01")
	m := waitFor[model.EvNewMessage](t, events)
	path := filepath.Join(t.TempDir(), "r.bin")
	c.Download(context.Background(), m.Msg.Media, path)
	line := s.expect("PRIVMSG bob :\x01DCC SEND r.bin ")
	f := strings.Fields(strings.Trim(strings.TrimPrefix(line, "PRIVMSG bob :"), "\x01"))
	if len(f) != 7 || f[3] != "2130706433" || f[5] != "5" || f[6] != "tok9" {
		t.Fatalf("reverse line: %q", line)
	}
	port, _ := strconv.Atoi(f[4])
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	conn.Write([]byte("hello"))
	conn.Close()
	if d := waitFor[model.EvDownloaded](t, events); d.Err != "" {
		t.Fatalf("reverse download: %+v", d)
	}
	if b, _ := os.ReadFile(path); string(b) != "hello" {
		t.Fatalf("content %q", b)
	}
}

// SendFile announces the port, streams once the peer connected, then acks
// the local line.
func TestDCCSend(t *testing.T) {
	c, s, events, _ := start(t, Config{DCCIP: "127.0.0.1", DCCPorts: "40000-40100"}, false)
	dir := t.TempDir()
	path := filepath.Join(dir, "a file.txt")
	payload := strings.Repeat("y", 70*1024)
	os.WriteFile(path, []byte(payload), 0o600)
	c.SendFile(context.Background(), chatOf("bob"), path, "", true, 3)
	line := s.expect("PRIVMSG bob :\x01DCC SEND ")
	f := strings.Fields(strings.Trim(strings.TrimPrefix(line, "PRIVMSG bob :"), "\x01"))
	// "DCC SEND "a file.txt" 2130706433 <port> <size>"
	if len(f) != 7 || f[2]+" "+f[3] != `"a file.txt"` || f[4] != "2130706433" || f[6] != strconv.Itoa(len(payload)) {
		t.Fatalf("send line: %q", line)
	}
	port, _ := strconv.Atoi(f[5])
	if port < 40000 || port > 40100 {
		t.Fatalf("port out of range: %d", port)
	}
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	got := make([]byte, 0, len(payload))
	buf := make([]byte, 8192)
	for len(got) < len(payload) {
		n, err := conn.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			break
		}
		conn.Write([]byte{0, 0, 0, byte(n)}) // an ack, ignored by the sender
	}
	if string(got) != payload {
		t.Fatalf("received %d bytes", len(got))
	}
	e := waitFor[model.EvSent](t, events)
	if e.Err != "" || e.TmpID != 3 || e.ID == 0 {
		t.Fatalf("receipt: %+v", e)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("removeAfter: file still there")
	}
	c.SendFile(context.Background(), chatOf("#go"), path, "", false, 4)
	if e = waitFor[model.EvSent](t, events); e.Err == "" {
		t.Fatal("a room must refuse a DCC SEND")
	}
}

// --- pure helpers ---

func TestSplitLines(t *testing.T) {
	long := strings.Repeat("abcdefghij ", 50) // 550 bytes
	got := splitLines(long+"\n\nend", 400)
	if len(got) != 3 || len(got[0]) > 400 || !strings.HasSuffix(got[0], "abcdefghij") || got[2] != "end" {
		t.Fatalf("split: %d pieces, %q…", len(got), got[0][:20])
	}
	if got := splitLines(strings.Repeat("é", 250), 400); len(got) != 2 || len(got[0])%2 != 0 {
		t.Fatalf("rune boundary: %d pieces, %d bytes", len(got), len(got[0]))
	}
	if got := splitLines("", 400); len(got) != 0 {
		t.Fatalf("empty: %v", got)
	}
}

func TestSpansOf(t *testing.T) {
	text, spans := spansOf("\x0304red\x03 \x1dit\x1d \x1fu\x1f")
	if text != "red it u" || len(spans) != 2 || spans[0] != (model.Span{Start: 4, End: 6, Kind: model.SpanItalic}) || spans[1] != (model.Span{Start: 7, End: 8, Kind: model.SpanUnderline}) {
		t.Fatalf("spans: %q %+v", text, spans)
	}
	if text, spans := spansOf("plain"); text != "plain" || spans != nil {
		t.Fatalf("plain: %q %v", text, spans)
	}
}

func TestStyled(t *testing.T) {
	got := styled([]model.Seg{{Text: "a", Bold: true, Italic: true}, {Text: " b"}, {Text: "code", Kind: model.SegPre}, {Text: "c", Underline: true}})
	if got != "\x02\x1da\x0f b\ncode\n\x1fc\x0f" {
		t.Fatalf("styled: %q", got)
	}
}

func TestChatID(t *testing.T) {
	if chatID("#Go") != chatID("#go") || chatID("#go") == chatID("#dev") || chatID("#go") <= 0 {
		t.Fatal("chatID must be case-insensitive, distinct and positive")
	}
}
