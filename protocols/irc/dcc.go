package irc

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
)

// DCC SEND, both ways, straight between the two clients over TCP:
//   CTCP DCC SEND <name> <ip> <port> <size> [token]
// ip is the IPv4 as a decimal integer (or an IPv6 literal); port 0 with a
// token is a reverse offer — the receiver listens and sends the line back
// with its own ip and port. The receiver acks the bytes got so far as a
// 4-byte big-endian counter; the sender is done once the last one is in.
//
// ponytail: no RESUME/ACCEPT, no DCC CHAT, no SDCC, no reverse send. A
// transfer that fails starts over.

const (
	dccWait  = 2 * time.Minute // the peer has that long to connect, and its last ack that long to come
	dccBlock = 64 * 1024
)

// parseOffer reads the arguments of a DCC SEND. A name with spaces comes
// between double quotes. The address of a direct offer must be one a peer
// can be at: not unspecified, multicast or link-local, and loopback only
// when our own dcc_ip is (loopback) — a line from the network must not make
// the client connect to a service of the machine.
func parseOffer(nick, args string, loopback bool) (dccOffer, error) {
	args = strings.TrimSpace(args)
	var name string
	if strings.HasPrefix(args, `"`) {
		end := strings.Index(args[1:], `"`)
		if end < 0 {
			return dccOffer{}, errors.New("unterminated file name")
		}
		name, args = args[1:end+1], strings.TrimSpace(args[end+2:])
	} else {
		name, args, _ = strings.Cut(args, " ")
	}
	f := strings.Fields(args)
	if len(f) < 3 {
		return dccOffer{}, errors.New("malformed DCC SEND")
	}
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	if name == "" || name == "." || name == ".." || name == "/" {
		return dccOffer{}, errors.New("bad file name")
	}
	port, err := strconv.Atoi(f[1])
	if err != nil || port < 0 || port > 65535 {
		return dccOffer{}, errors.New("bad port")
	}
	size, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil || size <= 0 {
		return dccOffer{}, errors.New("bad size")
	}
	off := dccOffer{Nick: nick, Name: name, IP: parseIP(f[0]), Port: port, Size: size}
	if len(f) > 3 {
		off.Token = f[3]
	}
	if port == 0 && off.Token == "" {
		return dccOffer{}, errors.New("port 0 without a token")
	}
	if port != 0 {
		a, err := netip.ParseAddr(off.IP)
		if err != nil || a.IsUnspecified() || a.IsMulticast() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
			(a.IsLoopback() && !loopback) {
			return dccOffer{}, errors.New("bad address")
		}
	}
	return off, nil
}

// loopbackDCC : our own dcc_ip is a loopback address (a test, a bouncer on
// the machine): a loopback offer is then plausible.
func (c *Client) loopbackDCC() bool {
	a, err := netip.ParseAddr(c.cfg.DCCIP)
	return err == nil && a.IsLoopback()
}

// parseIP : an IPv4 as a decimal integer (what every client sends) or a
// literal (IPv6 goes as one).
func parseIP(s string) string {
	if ip := net.ParseIP(s); ip != nil {
		return ip.String()
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return ""
	}
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n)).String()
}

// ipArg : the ip of a DCC line — IPv4 as an integer, IPv6 as a literal.
func ipArg(ip string) string {
	if v4 := net.ParseIP(ip).To4(); v4 != nil {
		return strconv.FormatUint(uint64(binary.BigEndian.Uint32(v4)), 10)
	}
	return ip
}

// media : the file offer as the UI shows it, the download key of the message.
// The label carries the address the download would connect to: a LAN or a
// loopback offer is allowed, the user sees where it points before taking it.
func (o dccOffer) media() *model.Media {
	label := i18n.T("dcc_offer_passive", o.Name, render.HumanSize(o.Size))
	if o.Port != 0 {
		label = i18n.T("dcc_offer", o.Name, render.HumanSize(o.Size), net.JoinHostPort(o.IP, strconv.Itoa(o.Port)))
	}
	return &model.Media{Kind: model.MediaFile, Name: o.Name, Size: o.Size, Ext: media.SafeExtension(o.Name, ""),
		Label: label, Loc: o}
}

// dccName : the CTCP argument of a file name.
func dccName(name string) string {
	if strings.ContainsAny(name, " \"") {
		return `"` + strings.ReplaceAll(name, `"`, "'") + `"`
	}
	return name
}

// listen opens a port of the configured range (any free one when empty) on
// the address announced — never on every interface, where the first comer
// of the two-minute window would take the file. Behind a NAT dcc_ip is not
// an address of the machine: the one of the IRC socket then, which is the
// side the peer reaches.
func (c *Client) listen() (net.Listener, error) {
	var ips []string
	if c.cfg.DCCIP != "" {
		ips = append(ips, c.cfg.DCCIP)
	}
	if ip := c.sockIP(); ip != "" && ip != c.cfg.DCCIP {
		ips = append(ips, ip)
	}
	a, b := 0, 0
	if c.cfg.DCCPorts != "" {
		lo, hi, ok := strings.Cut(c.cfg.DCCPorts, "-")
		var err1, err2 error
		a, err1 = strconv.Atoi(strings.TrimSpace(lo))
		b, err2 = strconv.Atoi(strings.TrimSpace(hi))
		if !ok {
			b, err2 = a, err1
		}
		if err1 != nil || err2 != nil || a < 1 || b > 65535 || a > b {
			return nil, fmt.Errorf("dcc_ports %q", c.cfg.DCCPorts)
		}
	}
	lc := net.ListenConfig{}
	ctx := context.Background()
	for _, ip := range ips {
		for p := a; p <= b; p++ {
			if l, err := lc.Listen(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(p))); err == nil {
				return l, nil
			}
		}
	}
	if c.cfg.DCCPorts == "" {
		return nil, errors.New(i18n.T("dcc_no_ip"))
	}
	return nil, errors.New(i18n.T("dcc_no_port", c.cfg.DCCPorts))
}

// track adds a transfer label for /dcc, and gives the way to drop it.
func (c *Client) track(label string) func() {
	c.mu.Lock()
	c.xfers = append(c.xfers, label)
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		if i := slices.Index(c.xfers, label); i >= 0 {
			c.xfers = slices.Delete(c.xfers, i, i+1)
		}
		c.mu.Unlock()
	}
}

// DCC : the offers waiting and the transfers running, for /dcc.
func (c *Client) DCC() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, o := range c.offers {
		out = append(out, i18n.T("dcc_pending", o.Nick, o.Name, render.HumanSize(o.Size)))
	}
	slices.Sort(out)
	return append(out, c.xfers...)
}

// SendFile : DCC SEND to the person of chat. The local line stays pending
// until the peer took the whole file (EvSent), or failed (EvSent.Err).
func (c *Client) SendFile(ctx context.Context, chat *model.Chat, path, _ string, removeAfter bool, tmpID int64) {
	snapshot := *chat
	chat = &snapshot
	go func() {
		fail := func(err string) {
			c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID, Err: i18n.T("upload_error", filepath.Base(path), err)})
		}
		defer c.Guard("SendFile", fail)
		if err := c.dccSend(ctx, nameOf(chat), path); err != nil {
			fail(err.Error())
			return
		}
		if removeAfter {
			os.Remove(path)
		}
		c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID, ID: c.ids.next()})
	}()
}

func (c *Client) dccSend(ctx context.Context, nick, path string) error {
	if isChannel(nick) {
		return errors.New(i18n.T("dcc_private_only"))
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() == 0 {
		return errors.New(i18n.T("dcc_empty"))
	}
	ip := c.localIP()
	if ip == "" {
		return errors.New(i18n.T("dcc_no_ip"))
	}
	l, err := c.listen()
	if err != nil {
		return err
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	name := filepath.Base(path)
	untrack := c.track(i18n.T("dcc_sending", name, nick))
	defer untrack()
	line := fmt.Sprintf("\x01DCC SEND %s %s %d %d\x01", dccName(name), ipArg(ip), port, st.Size())
	if err := c.pace(ctx); err != nil {
		return err
	}
	if err := c.send("PRIVMSG", nick, line); err != nil {
		return err
	}
	// The peer has dccWait to connect; ctx (a logout, a quit) cuts it short.
	wctx, cancel := context.WithTimeout(ctx, dccWait)
	defer cancel()
	go func() {
		<-wctx.Done()
		l.Close() // a closed listener makes Accept come back
	}()
	conn, err := l.Accept()
	if err != nil {
		if wctx.Err() != nil {
			return errors.New(i18n.T("dcc_no_answer", nick))
		}
		return err
	}
	conn = watchDCC(ctx, conn)
	defer conn.Close()
	acks := acked(conn, st.Size())
	if err := c.stream(ctx, f, conn, st.Size(), func(pct int) {
		c.PostNB(model.EvUpload{Text: i18n.T("dcc_progress", name, pct)})
	}); err != nil {
		return err
	}
	// The peer has the file once it acked the last byte: a close with bytes
	// still in the send queue would drop them (RST). The read carries the
	// dccWait idle deadline of dccConn, ctx cuts it.
	return <-acks
}

// acked reads the 4-byte counters of the peer through the whole transfer —
// unread, they would block a receiver that writes them in line with its
// reads — and gives nil once the peer acked size bytes, or the read error
// (a peer gone or silent past the deadline) before that. The counter of a
// DCC ack wraps at 4 GiB: the compare is modulo 2^32.
func acked(r io.Reader, size int64) <-chan error {
	ch := make(chan error, 1)
	go func() {
		var buf [4]byte
		done := false
		for {
			if _, err := io.ReadFull(r, buf[:]); err != nil {
				if !done {
					ch <- fmt.Errorf("ack: %w", err)
				}
				return
			}
			if !done && binary.BigEndian.Uint32(buf[:]) == uint32(size) {
				done = true
				ch <- nil
			}
		}
	}()
	return ch
}

// stream copies size bytes from src to dst, progress every 5 %; ctx cuts it.
func (c *Client) stream(ctx context.Context, src io.Reader, dst io.Writer, size int64, progress func(int)) error {
	buf := make([]byte, dccBlock)
	var done int64
	last := -1
	for done < size {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			if pct := int(done * 100 / size); pct/5 != last/5 {
				last = pct
				progress(pct)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if done < size {
		return io.ErrUnexpectedEOF
	}
	return nil
}

// dccGet fetches an offer into path — a ".part-" file next to it first, the
// name once whole (a killed session leaves a part file cleanParts removes).
func (c *Client) dccGet(ctx context.Context, m *model.Media, off dccOffer, path string) {
	result := model.EvDownloaded{Media: m}
	defer func() { c.Post(result) }() // Report completion after resource cleanup.
	fail := func(err string) { result.Err = err }
	defer c.Guard("dccGet", fail)
	untrack := c.track(i18n.T("dcc_receiving", off.Name, off.Nick))
	defer untrack()
	conn, err := c.dccConnect(ctx, off)
	if err != nil {
		fail(err.Error())
		return
	}
	conn = watchDCC(ctx, conn)
	defer conn.Close()
	c.mu.Lock()
	if o, ok := c.offers[c.casefold(off.Nick)]; ok && o == off {
		delete(c.offers, c.casefold(off.Nick))
	}
	c.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fail(err.Error())
		return
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".part-*")
	if err != nil {
		fail(err.Error())
		return
	}
	part := f.Name()
	defer f.Close()
	defer os.Remove(part) // no-op once renamed
	ack := make([]byte, 4)
	w := &ackWriter{w: f, ack: func(n int64) {
		// ponytail: 32-bit counter, wraps past 4 GB; the sender ignores it anyway.
		binary.BigEndian.PutUint32(ack, uint32(n))
		conn.Write(ack)
	}}
	err = c.stream(ctx, io.LimitReader(conn, off.Size), w, off.Size, func(pct int) {
		c.PostNB(model.EvUpload{Text: i18n.T("dcc_progress", off.Name, pct)})
	})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil || w.n != off.Size {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		fail(err.Error())
		return
	}
	if err := os.Rename(part, path); err != nil {
		fail(err.Error())
		return
	}
	result.Path = path
}

type dccConn struct {
	net.Conn
	stop func() bool
}

func watchDCC(ctx context.Context, conn net.Conn) net.Conn {
	return &dccConn{Conn: conn, stop: context.AfterFunc(ctx, func() { conn.Close() })}
}

func (c *dccConn) Read(p []byte) (int, error) {
	c.SetReadDeadline(time.Now().Add(dccWait))
	return c.Conn.Read(p)
}

func (c *dccConn) Write(p []byte) (int, error) {
	c.SetWriteDeadline(time.Now().Add(dccWait))
	return c.Conn.Write(p)
}

func (c *dccConn) Close() error {
	c.stop()
	return c.Conn.Close()
}

// dccConnect reaches the sender: its port, or ours when the offer is a
// reverse one (port 0) — we listen, send the line back, and wait.
func (c *Client) dccConnect(ctx context.Context, off dccOffer) (net.Conn, error) {
	if off.Port != 0 {
		d := net.Dialer{Timeout: 30 * time.Second}
		return d.DialContext(ctx, "tcp", net.JoinHostPort(off.IP, strconv.Itoa(off.Port)))
	}
	ip := c.localIP()
	if ip == "" {
		return nil, errors.New(i18n.T("dcc_no_ip"))
	}
	l, err := c.listen()
	if err != nil {
		return nil, err
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	line := fmt.Sprintf("\x01DCC SEND %s %s %d %d %s\x01", dccName(off.Name), ipArg(ip), port, off.Size, off.Token)
	if err := c.pace(ctx); err != nil {
		return nil, err
	}
	if err := c.send("PRIVMSG", off.Nick, line); err != nil {
		return nil, err
	}
	wctx, cancel := context.WithTimeout(ctx, dccWait)
	defer cancel()
	go func() {
		<-wctx.Done()
		l.Close()
	}()
	conn, err := l.Accept()
	if err != nil && wctx.Err() != nil {
		return nil, errors.New(i18n.T("dcc_no_answer", off.Nick))
	}
	return conn, err
}

// ackWriter counts the bytes written and tells the sender after each block.
type ackWriter struct {
	w    io.Writer
	n    int64
	ack  func(int64)
	last int64
}

func (a *ackWriter) Write(p []byte) (int, error) {
	n, err := a.w.Write(p)
	a.n += int64(n)
	if a.n-a.last >= dccBlock || err != nil || len(p) < dccBlock {
		a.last = a.n
		a.ack(a.n)
	}
	return n, err
}
