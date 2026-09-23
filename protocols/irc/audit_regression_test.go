package irc

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

func TestRegressionDCCExtension(t *testing.T) {
	off, err := parseOffer("alice", "invoice.desktop 2130706433 1234 10", true)
	if err != nil {
		t.Fatal(err)
	}
	if off.media().Ext != ".bin" {
		t.Fatalf("untrusted active extension retained: %q", off.media().Ext)
	}
}

func TestRegressionDCCSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "local-private.txt")
	path := filepath.Join(dir, "download.txt")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, ".part-download.txt")); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.Write([]byte("changed"))
		io.Copy(io.Discard, conn)
	}()
	events := make(chan model.Event, 10)
	c := New(Config{}, events)
	off := dccOffer{Nick: "alice", Name: "download.txt", IP: "127.0.0.1", Port: l.Addr().(*net.TCPAddr).Port, Size: 7}
	c.dccGet(context.Background(), off.media(), off, path)
	<-done
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("symlink target overwritten: %q", got)
	}
}

func TestRegressionDCCCancel(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := l.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New(Config{}, make(chan model.Event, 10))
	off := dccOffer{Nick: "alice", Name: "idle.txt", IP: "127.0.0.1", Port: l.Addr().(*net.TCPAddr).Port, Size: 1}
	done := make(chan struct{})
	go func() { defer close(done); c.dccGet(ctx, off.media(), off, filepath.Join(t.TempDir(), "idle.txt")) }()
	conn := <-accepted
	defer conn.Close()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(150 * time.Millisecond):
		conn.Close()
		<-done
		t.Fatal("DCC stayed blocked after cancellation; only peer close released it")
	}
}

func TestRegressionIRCCaseMapping(t *testing.T) {
	if chatID("alice[") != chatID("alice{") {
		t.Fatal("RFC1459 equivalent nicknames receive different chat identities")
	}
}

func TestNegotiatedCaseMapping(t *testing.T) {
	for _, mode := range []string{"ascii", "rfc1459", "strict-rfc1459"} {
		t.Run(mode, func(t *testing.T) {
			c, _, _, _ := start(t, Config{}, false, mode)
			if c.chatID("Alice") != c.chatID("alice") {
				t.Fatal("ASCII case differs")
			}
			if got := c.chatID("alice[") == c.chatID("alice{"); got != (mode != "ascii") {
				t.Fatal("bracket mapping differs from advertisement")
			}
			if got := c.chatID("alice~") == c.chatID("alice^"); got != (mode == "rfc1459") {
				t.Fatal("tilde mapping differs from advertisement")
			}
			if c.chatID("Éva") == c.chatID("éva") {
				t.Fatal("Unicode case was folded")
			}
		})
	}
}

func TestDCCBlockedWriteCancelled(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := watchDCC(ctx, a)
	defer conn.Close()
	done := make(chan error, 1)
	go func() { _, err := conn.Write([]byte("unread bytes")); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("write to an idle peer succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("write remained blocked after cancellation")
	}
}

// --- IRC protocol (audit of 2026-09-22) ---

// ctcpReplies : the CTCP answers (NOTICE wrapped in \x01) sent to nick within d.
func ctcpReplies(s *fakeServer, nick string, d time.Duration) []string {
	var got []string
	deadline := time.After(d)
	for {
		select {
		case l, ok := <-s.lines:
			if !ok {
				return got
			}
			if strings.HasPrefix(l, "NOTICE "+nick+" :\x01") {
				got = append(got, l)
			}
		case <-deadline:
			return got
		}
	}
}

// CTCP VERSION from an /ignore'd nick: no answer at all.
func TestRegressionCTCPIgnored(t *testing.T) {
	_, s, _, _ := start(t, Config{Ignores: []string{"evil"}}, false)
	for i := 0; i < 30; i++ {
		s.send(":evil!u@h PRIVMSG me :\x01VERSION\x01")
	}
	if got := ctcpReplies(s, "evil", 500*time.Millisecond); len(got) > 0 {
		t.Fatalf("%d CTCP replies sent to an ignored nick", len(got))
	}
}

// A burst of CTCP requests from anyone else is answered three times, then
// one every two seconds (none more within the second); a request through a
// room gets no answer at all.
func TestRegressionCTCPThrottled(t *testing.T) {
	_, s, _, _ := start(t, Config{}, false)
	s.send(":bob!b@h PRIVMSG #go :\x01VERSION\x01")
	for i := 0; i < 30; i++ {
		s.send(":bob!b@h PRIVMSG me :\x01VERSION\x01")
	}
	got := ctcpReplies(s, "bob", time.Second)
	if len(got) != 3 || got[0] != "NOTICE bob :\x01VERSION ttyloom\x01" {
		t.Fatalf("CTCP replies within a second: %q, want 3 VERSION lines", got)
	}
}

// SASL offered and refused (904): the password is not sent again to
// NickServ, who may be anyone on a network without services.
func TestRegressionNoNickServAfterSASLFailure(t *testing.T) {
	_, s, _, _ := start(t, Config{Password: "wrong", PasswordWithoutTLS: true}, true)
	s.never("PRIVMSG NickServ", 300*time.Millisecond)
}

// Without TLS the password stays home unless the configuration says so: no
// SASL, no IDENTIFY, and a warning line says why.
func TestRegressionPasswordWithheldWithoutTLS(t *testing.T) {
	_, s, _, _ := start(t, Config{Password: "secret"}, true)
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case l := <-s.lines:
			if strings.HasPrefix(l, "AUTHENTICATE") || strings.Contains(l, "secret") {
				t.Fatalf("password on a connection without TLS: %q", l)
			}
			continue
		case <-deadline:
		}
		break
	}
	events := make(chan model.Event, 64)
	c := New(Config{Name: "plain", Host: "h", Port: 1, Nick: "me", Password: "secret",
		Dial: func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("down") }}, events)
	if err := c.Run(context.Background()); err == nil {
		t.Fatal("Run with a dead dial succeeded")
	}
	for {
		select {
		case ev := <-events:
			if l, ok := ev.(model.EvLog); ok && l.Level == "WARN" {
				if l.Msg != i18n.T("irc_password_withheld", "irc:plain") {
					t.Fatalf("warning: %q", l.Msg)
				}
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no warning line for the withheld password")
		}
	}
}

// A DCC SEND is done when the peer acked the last byte: a receiver that took
// the bytes but closed after a partial ack leaves the line in error.
func TestRegressionDCCSendWaitsForAck(t *testing.T) {
	c, s, events, _ := start(t, Config{DCCIP: "127.0.0.1"}, false)
	path := filepath.Join(t.TempDir(), "f.bin")
	payload := strings.Repeat("z", 3000)
	os.WriteFile(path, []byte(payload), 0o600)
	c.SendFile(context.Background(), chatOf("bob"), path, "", false, 5)
	line := s.expect("PRIVMSG bob :\x01DCC SEND ")
	f := strings.Fields(strings.Trim(strings.TrimPrefix(line, "PRIVMSG bob :"), "\x01"))
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", f[4]))
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8192)
	for got := 0; got < len(payload); {
		n, err := conn.Read(buf)
		got += n
		if err != nil {
			break
		}
	}
	conn.Write([]byte{0, 0, 0, 100}) // 100 bytes acked, not the whole file
	conn.Close()
	if e := waitFor[model.EvSent](t, events); e.Err == "" || e.TmpID != 5 {
		t.Fatalf("receipt after a partial ack: %+v", e)
	}
}

// The listener of a DCC SEND binds the address announced, not every interface.
func TestRegressionDCCListenerBindsAnnouncedIP(t *testing.T) {
	c := New(Config{DCCIP: "127.0.0.1"}, nil)
	l, err := c.listen()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if a := l.Addr().(*net.TCPAddr); !a.IP.IsLoopback() {
		t.Fatalf("listener on %s, want 127.0.0.1", a)
	}
}

// /nick goes through SetNick: the preferred nick follows, so the library
// does not ask the old one back at the next keepalive.
func TestRegressionNickSticks(t *testing.T) {
	c, s, _, _ := start(t, Config{}, false)
	cmd(c, 0, "", "nick", "other")
	if l := s.expect("NICK"); l != "NICK other" {
		t.Fatalf("nick: %q", l)
	}
	s.send(":me!u@h NICK other")
	for deadline := time.Now().Add(2 * time.Second); c.conn.CurrentNick() != "other" && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if cur, pref := c.conn.CurrentNick(), c.conn.PreferredNick(); cur != "other" || pref != "other" {
		t.Fatalf("current %q, preferred %q, want other", cur, pref)
	}
}

// /ignore silences the JOIN, PART, QUIT, NICK, TOPIC and MODE lines of a
// matching source; the member list still follows the person.
func TestRegressionIgnoreServiceLines(t *testing.T) {
	c, s, events, _ := start(t, Config{Ignores: []string{"b@h"}}, false) // *!b@h: the nick may change
	s.send(":me!u@h JOIN #go")
	waitMsg(t, events, func(m model.EvNewMessage) bool { return m.Msg.Service != "" })
	for _, l := range []string{":bob!b@h JOIN #go", ":bob!b@h TOPIC #go :new topic", ":bob!b@h MODE #go +m",
		":bob!b@h PART #go :bye", ":bob!b@h JOIN #go", ":bob!b@h NICK bobby", ":carol!c@h JOIN #go"} {
		s.send(l)
	}
	m := waitMsg(t, events, func(m model.EvNewMessage) bool { return m.Msg.Service != "" })
	if !strings.Contains(m.Msg.Service, "carol") {
		t.Fatalf("line of an ignored source shown: %q", m.Msg.Service)
	}
	if got := c.Members(chatOf("#go")); !slices.Equal(got, []string{"bobby", "carol"}) {
		t.Fatalf("members after the ignored JOIN and NICK: %v", got)
	}
	s.send(":bobby!b@h QUIT :gone")
	s.send(":carol!c@h PART #go")
	if m = waitMsg(t, events, func(m model.EvNewMessage) bool { return m.Msg.Service != "" }); !strings.Contains(m.Msg.Service, "carol") {
		t.Fatalf("quit of an ignored source shown: %q", m.Msg.Service)
	}
}

// Output pacing: four lines at once, then one a period — a paste of six
// lines does not go out in one go (the servers count that as a flood).
func TestRegressionOutputPaced(t *testing.T) {
	was := outFill
	outFill = 200 * time.Millisecond
	t.Cleanup(func() { outFill = was })
	c, s, events, _ := start(t, Config{}, false)
	c.Send(context.Background(), chatOf("#go"), "1\n2\n3\n4\n5\n6", 9)
	for i := 0; i < 4; i++ {
		s.expect("PRIVMSG #go")
	}
	s.never("PRIVMSG #go", 100*time.Millisecond) // the fifth waits for its token
	from := time.Now()
	s.expect("PRIVMSG #go")
	s.expect("PRIVMSG #go")
	if e := waitFor[model.EvSent](t, events); e.Err != "" || e.TmpID != 9 {
		t.Fatalf("receipt: %+v", e)
	}
	if d := time.Since(from); d > 2*time.Second {
		t.Fatalf("lines 5 and 6 took %v", d)
	}
}

// A MOTD that never ends is kept to maxGather lines.
func TestRegressionMotdBounded(t *testing.T) {
	_, s, events, _ := start(t, Config{}, false)
	s.send(":srv 375 me :- start")
	for i := 0; i < maxGather+100; i++ {
		s.send(":srv 372 me :- line")
	}
	s.send(":srv 376 me :End of MOTD")
	if l := waitLines(t, events, "End of MOTD"); len(l.Lines) > maxGather+1 {
		t.Fatalf("%d MOTD lines kept", len(l.Lines))
	}
}

// Members : the list is sorted, case apart, whatever the order of NAMES.
func TestRegressionMembersSorted(t *testing.T) {
	c, s, events, _ := start(t, Config{}, false)
	s.send(":me!u@h JOIN #go")
	c.Participants(context.Background(), chatOf("#go"))
	s.expect("NAMES #go")
	s.send(":srv 353 me = #go :zed @alice +Bob")
	s.send(":srv 366 me #go :End of NAMES")
	waitFor[model.EvParticipants](t, events)
	if got := c.Members(chatOf("#go")); !slices.Equal(got, []string{"alice", "Bob", "zed"}) {
		t.Fatalf("members: %v", got)
	}
}
