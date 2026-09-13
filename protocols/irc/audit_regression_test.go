package irc

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/model"
)

func TestRegressionDCCExtension(t *testing.T) {
	off, err := parseOffer("alice", "invoice.desktop 2130706433 1234 10")
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
