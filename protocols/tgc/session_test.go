package tgc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/session"
)

func TestSessionWriteReplacesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	s := &sessionFile{FileStorage: session.FileStorage{Path: path}}
	if err := s.StoreSession(context.Background(), []byte("new")); err != nil {
		t.Fatal(err)
	}
	data, err := s.LoadSession(context.Background())
	if err != nil || string(data) != "new" {
		t.Fatalf("session: %q, %v", data, err)
	}
	buf := make([]byte, 3)
	if _, err := old.Read(buf); err != nil || string(buf) != "old" {
		t.Fatalf("previous session was truncated: %q, %v", buf, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("session permissions are not private")
	}
}
