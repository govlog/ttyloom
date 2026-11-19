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
	"unicode/utf8"

	"github.com/ergochat/irc-go/ircmsg"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
	"github.com/govlog/ttyloom/internal/render"
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
	// rfc8265 (Ergo, PRECIS) folds no bracket: two people bob[m] and bob{m}.
	for _, mode := range []string{"ascii", "rfc1459", "strict-rfc1459", "rfc8265"} {
		t.Run(mode, func(t *testing.T) {
			c, _, _, _ := start(t, Config{}, false, mode)
			if c.chatID("Alice") != c.chatID("alice") {
				t.Fatal("ASCII case differs")
			}
			ascii := mode == "ascii" || mode == "rfc8265"
			if got := c.chatID("alice[") == c.chatID("alice{"); got != !ascii {
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
// NickServ, who may be anyone on a network without services — and the
// refusal shows in window 0, the one trace of a wrong password.
func TestRegressionNoNickServAfterSASLFailure(t *testing.T) {
	_, s, events, _ := connect(t, Config{Password: "wrong", PasswordWithoutTLS: true}, true)
	s.never("PRIVMSG NickServ", 300*time.Millisecond)
	for {
		select {
		case ev := <-events:
			if l, ok := ev.(model.EvLines); ok && l.ChatID == 0 && strings.Contains(strings.Join(l.Lines, " "), "SASL authentication failed") {
				return
			}
		default:
			t.Fatal("no window 0 line for the refused password")
		}
	}
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
			if l, ok := ev.(model.EvLines); ok && l.ChatID == 0 { // window 0, not /debug
				if len(l.Lines) != 1 || l.Lines[0] != i18n.T("irc_password_withheld", "irc:plain") {
					t.Fatalf("warning: %q", l.Lines)
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

// IRC-1: a message sent while a paste is paced goes out after the whole
// paste. Other users must not see the paste cut in two.
func TestRegressionSendAfterPacedPaste(t *testing.T) {
	was := outFill
	outFill = 50 * time.Millisecond
	t.Cleanup(func() { outFill = was })
	c, s, events, _ := start(t, Config{}, false)
	c.Send(context.Background(), chatOf("#go"), "a1\na2\na3\na4\na5\na6\na7\na8", 1)
	for range outBurst { // the burst is out, a5 to a8 wait for their tokens
		s.expect("PRIVMSG #go")
	}
	c.Send(context.Background(), chatOf("#go"), "B", 2)
	var got []string
	for range 5 {
		got = append(got, strings.TrimPrefix(s.expect("PRIVMSG #go"), "PRIVMSG #go "))
	}
	if want := []string{"a5", "a6", "a7", "a8", "B"}; !slices.Equal(got, want) {
		t.Fatalf("wire order after the burst: %v, want %v (B inside the paste)", got, want)
	}
	for _, tmp := range []int64{1, 2} {
		if e := waitFor[model.EvSent](t, events); e.TmpID != tmp || e.Err != "" || e.ID == 0 {
			t.Fatalf("receipt: %+v, want tmp %d with an id", e, tmp)
		}
	}
}

// IRC-1: messages and commands typed in quick succession go out in the order
// they were typed, past the burst of the pacer too.
func TestRegressionQuickSendsKeepOrder(t *testing.T) {
	was := outFill
	outFill = 5 * time.Millisecond
	t.Cleanup(func() { outFill = was })
	c, s, _, _ := start(t, Config{}, false)
	var want []string
	for i := range 12 {
		text := string(rune('a' + i))
		if i%3 == 2 { // a command among the messages
			cmd(c, 0, "#go", "notice", "#go "+text)
			want = append(want, "NOTICE #go "+text)
		} else {
			c.Send(context.Background(), chatOf("#go"), text, int64(i+1))
			want = append(want, "PRIVMSG #go "+text)
		}
	}
	var got []string
	for range want {
		got = append(got, s.lineWithin(5*time.Second, "PRIVMSG #go ", "NOTICE #go "))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("wire order: %q, want %q", got, want)
	}
}

// IRC-1: a send that fails in the middle posts its error once and drops the
// rest of its lines; the send after it still goes out.
func TestRegressionFailedSendFreesQueue(t *testing.T) {
	c, s, events, _ := start(t, Config{}, false)
	c.Send(context.Background(), chatOf("#go"), "ok\nbad\x00\nlost", 1) // NUL: the line cannot be written
	c.Send(context.Background(), chatOf("#go"), "next", 2)
	got := []string{s.expect("PRIVMSG #go"), s.expect("PRIVMSG #go")}
	slices.Sort(got)
	if want := []string{"PRIVMSG #go next", "PRIVMSG #go ok"}; !slices.Equal(got, want) {
		t.Fatalf("lines: %q, want %q", got, want)
	}
	s.never("PRIVMSG #go lost", 100*time.Millisecond)
	sent := map[int64]model.EvSent{}
	for range 2 {
		e := waitFor[model.EvSent](t, events)
		if _, twice := sent[e.TmpID]; twice {
			t.Fatalf("second receipt for tmp %d: %+v", e.TmpID, e)
		}
		sent[e.TmpID] = e
	}
	if e := sent[1]; e.Err == "" || e.ID != 0 {
		t.Fatalf("failed send: %+v, want an error and no id", e)
	}
	if e := sent[2]; e.Err != "" || e.ID == 0 {
		t.Fatalf("send after the failed one: %+v, want an id", e)
	}
}

// A send given up while it waits behind a paste (its context ends: the chat
// closed, the network stopped) never goes out and answers with that error.
func TestRegressionCancelledSendDropped(t *testing.T) {
	was := outFill
	outFill = 50 * time.Millisecond
	t.Cleanup(func() { outFill = was })
	c, s, events, _ := start(t, Config{}, false)
	c.Send(context.Background(), chatOf("#go"), "1\n2\n3\n4\n5\n6", 1)
	ctx, cancel := context.WithCancel(context.Background())
	c.Send(ctx, chatOf("#go"), "gone", 2)
	cancel()
	e := waitFor[model.EvSent](t, events)
	for e.TmpID != 2 {
		e = waitFor[model.EvSent](t, events)
	}
	if e.Err != context.Canceled.Error() {
		t.Fatalf("cancelled send: %+v, want the error %q", e, context.Canceled)
	}
	s.never("PRIVMSG #go gone", 100*time.Millisecond)
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

// --- audit of 2026-11-19, P2 ---

// roomHost : the Host of a window on chat, a chat of the network of the
// fake server.
type roomHost struct {
	fakeHost
	b    *Client
	chat *model.Chat
}

func (h *roomHost) ContextNet(module.Win, string) string { return h.chat.Net }
func (h *roomHost) Backend(string) model.Backend         { return h.b }
func (h *roomHost) Context(string) context.Context       { return context.Background() }
func (h *roomHost) Chats(string) []*model.Chat           { return []*model.Chat{h.chat} }

// IRC-5: the UI shows a bidi or control character of a room name as a space.
// The room completion and /part typed in the window of the room use the name
// as the server spells it.
func TestRegressionRoomCommandsUseWireName(t *testing.T) {
	c, s, _, _ := start(t, Config{}, false)
	wire := "#a\u202eb"
	chat := chatOf(wire)
	chat.Net, chat.Title = Net("test"), render.CleanLine(chat.Title) // the chat as the UI holds it
	h := &roomHost{b: c, chat: chat}
	w := module.Win{Chat: chat}
	var part module.Command
	for _, x := range NewModule().Commands() {
		if x.Name == "part" {
			part = x
		}
	}
	if got, _ := part.Complete(h, w, ""); !slices.Equal(got, []string{wire}) {
		t.Fatalf("rooms offered by the completion: %q, want %q", got, wire)
	}
	part.Run(h, w, nil, "")
	if l := s.expect("PART"); l != "PART "+wire {
		t.Fatalf("/part in the window of the room: %q, want %q", l, "PART "+wire)
	}
}

// IRC-3: the server relays ":nick!user@host PRIVMSG target :text\r\n" to the
// others in 512 bytes and cuts the end in silence. With a long nick, the
// longest user and host and a long room name, each piece of a message and of
// an action still fits; no rune is cut in two and no byte is lost.
func TestRegressionPiecesFitRelayed(t *testing.T) {
	was := outFill
	outFill = 5 * time.Millisecond
	t.Cleanup(func() { outFill = was })
	c, s, events, _ := start(t, Config{}, false)
	nick := strings.Repeat("n", 30)
	s.send(":me!u@h NICK " + nick)
	waitFor[model.EvSelfName](t, events)
	room := "#" + strings.Repeat("r", 49)
	prefix := ":" + nick + "!" + strings.Repeat("u", 10) + "@" + strings.Repeat("h", 63)
	text := strings.Repeat("aé😀", 150) // runes of 1, 2 and 4 bytes, no blank to cut on
	for _, tc := range []struct {
		name      string
		send      func()
		open, end string
	}{
		{"message", func() { c.Send(context.Background(), chatOf(room), text, 1) }, "", ""},
		{"action", func() { c.SendAction(context.Background(), chatOf(room), []model.Seg{{Text: text}}, 2) }, "\x01ACTION ", "\x01"},
	} {
		tc.send()
		var got string
		for len(got) < len(text) {
			l := s.lineWithin(2*time.Second, "PRIVMSG "+room)
			if l == "" {
				t.Fatalf("%s: the pieces end after %d of %d bytes", tc.name, len(got), len(text))
			}
			m, err := ircmsg.ParseLine(l)
			if err != nil || len(m.Params) != 2 {
				t.Fatalf("%s: line %q: %v", tc.name, l, err)
			}
			if n := len(prefix + " PRIVMSG " + room + " :" + m.Params[1] + "\r\n"); n > 512 {
				t.Fatalf("%s: a piece of %d bytes is relayed in %d bytes, over 512", tc.name, len(m.Params[1]), n)
			}
			piece := strings.TrimSuffix(strings.TrimPrefix(m.Params[1], tc.open), tc.end)
			if !utf8.ValidString(piece) {
				t.Fatalf("%s: a piece cut inside a rune: %q", tc.name, piece)
			}
			got += piece
		}
		if got != text {
			t.Fatalf("%s: the pieces put together differ from the text", tc.name)
		}
	}
}

// IRC-4: the JOIN refusals of Solanum (479 bad name, 480 throttle), InspIRCd
// (489 TLS only, 520 opers only, 926 forbidden) and UnrealIRCd (448
// forbidden, 489, 500 join flood) answer the /join that waits for them.
func TestRegressionJoinRefusalsAnswered(t *testing.T) {
	c, s, events, _ := start(t, Config{}, false)
	for i, code := range []string{"448", "479", "480", "489", "500", "520", "926"} {
		room, req := "#r"+code, uint64(i+1)
		c.Resolve(context.Background(), room, true, req)
		s.expect("JOIN " + room)
		s.send(":srv %s me %s :Cannot join channel (%s)", code, room, code)
		if e := waitFor[model.EvChat](t, events); e.Request != req || !strings.Contains(e.Err, "("+code+")") {
			t.Fatalf("%s: %+v, want request %d with the reason of the server", code, e, req)
		}
	}
}

// IRC-4: a JOIN that gets no answer this client knows leaves "resolving…"
// for ever: after joinWait the /join gets an error. A /join answered in time
// gets no second answer, and a late JOIN echo does not answer the one given
// up.
func TestRegressionJoinTimeout(t *testing.T) {
	was := joinWait
	joinWait = 100 * time.Millisecond
	t.Cleanup(func() { joinWait = was })
	c, s, events, _ := start(t, Config{}, false)
	c.Resolve(context.Background(), "#silent", true, 1)
	s.expect("JOIN #silent")
	c.Resolve(context.Background(), "#ok", true, 2)
	s.expect("JOIN #ok")
	s.send(":me!u@h JOIN #ok")
	got := map[uint64]model.EvChat{}
	for len(got) < 2 {
		e := waitFor[model.EvChat](t, events)
		if _, twice := got[e.Request]; twice {
			t.Fatalf("second answer to request %d: %+v", e.Request, e)
		}
		got[e.Request] = e
	}
	if e := got[1]; e.Err != i18n.T("irc_timeout") {
		t.Fatalf("/join with no answer: %+v, want the error %q", e, i18n.T("irc_timeout"))
	}
	if e := got[2]; e.Err != "" || e.Chat == nil {
		t.Fatalf("/join answered by the server: %+v, want the chat", e)
	}
	s.send(":me!u@h JOIN #silent") // the server answers after all
	deadline := time.After(3 * joinWait)
	for {
		select {
		case ev := <-events:
			if e, ok := ev.(model.EvChat); ok {
				t.Fatalf("answer after the timeout: %+v", e)
			}
		case <-deadline:
			return
		}
	}
}

// IRC-2: with no SASL the rooms of the list wait for services. A room that
// needs a registered nick (+r) refuses a JOIN that comes before 900. 900
// sends the rooms at once, and only once.
func TestRegressionJoinWaitsForLogin(t *testing.T) {
	was := identifyWait
	identifyWait = 400 * time.Millisecond
	t.Cleanup(func() { identifyWait = was })
	_, s, _, _ := start(t, Config{Password: "secret", PasswordWithoutTLS: true, Channels: []string{"#reg"}}, false)
	s.expect("PRIVMSG NickServ")
	s.never("JOIN", 150*time.Millisecond)
	s.send(":srv 900 me me!u@h me :You are now logged in as me")
	if l := s.expect("JOIN"); l != "JOIN #reg" {
		t.Fatalf("join after 900: %q", l)
	}
	s.never("JOIN", 2*identifyWait) // the end of the wait sends nothing more
}

// IRC-2: services that answer IDENTIFY with a NOTICE and no 900: the rooms
// go out when identifyWait ends.
func TestRegressionJoinAfterIdentifyWait(t *testing.T) {
	was := identifyWait
	identifyWait = 200 * time.Millisecond
	t.Cleanup(func() { identifyWait = was })
	_, s, _, _ := start(t, Config{Password: "secret", PasswordWithoutTLS: true, Channels: []string{"#reg"}}, false)
	s.expect("PRIVMSG NickServ")
	from := time.Now()
	s.send(":NickServ!s@services. NOTICE me :You are now identified for me.")
	if l := s.expect("JOIN"); l != "JOIN #reg" {
		t.Fatalf("join without 900: %q", l)
	}
	if d := time.Since(from); d < identifyWait/2 {
		t.Fatalf("rooms joined %v after IDENTIFY, want about %v", d, identifyWait)
	}
}

// IRC-2: the connection drops while its rooms wait for services. The next
// connection joins them once, on its own 900: the wait of the first one
// joins nothing on it.
func TestRegressionHeldJoinsFollowConnection(t *testing.T) {
	was := identifyWait
	identifyWait = 300 * time.Millisecond
	t.Cleanup(func() { identifyWait = was })
	c1, s1 := pipeServer(t)
	c2, s2 := pipeServer(t)
	dials := make(chan net.Conn, 2)
	dials <- c1
	dials <- c2
	c := New(Config{Name: "test", Host: "irc.example", Port: 6667, Nick: "me", Password: "secret", PasswordWithoutTLS: true,
		Channels: []string{"#reg"}, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			select {
			case conn := <-dials:
				return conn, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}}, make(chan model.Event, 256))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not end")
		}
		c1.Close()
		c2.Close()
	})
	s1.register()
	s1.expect("PRIVMSG NickServ")
	s1.conn.Close() // the connection drops before services answer
	s2.register()
	s2.expect("PRIVMSG NickServ")
	s2.send(":srv 900 me me!u@h me :You are now logged in as me")
	if l := s2.expect("JOIN"); l != "JOIN #reg" {
		t.Fatalf("join after 900 on the new connection: %q", l)
	}
	s2.never("JOIN", 2*identifyWait)
}
