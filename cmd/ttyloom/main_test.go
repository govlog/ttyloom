package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A killed session leaves .part-* files at every depth a download uses:
// download_dir, its maps/ and paste/, and avatars/<net>/. A recent one may
// be the download of another running instance: it stays.
func TestCleanPartsAllDepths(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "avatars", "discord")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, p := range []string{filepath.Join(dir, ".part-1"), filepath.Join(dir, "maps", ".part-2"), filepath.Join(deep, ".part-3")} {
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, nil, 0o600)
		os.Chtimes(p, old, old)
	}
	keep := filepath.Join(deep, "7.jpg")
	os.WriteFile(keep, nil, 0o600)
	fresh := filepath.Join(dir, ".part-4")
	os.WriteFile(fresh, nil, 0o600)
	cleanParts(dir)
	for _, p := range []string{filepath.Join(dir, ".part-1"), filepath.Join(dir, "maps", ".part-2"), filepath.Join(deep, ".part-3")} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("left behind: %s", p)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("a real file was removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("the part file of a running download was removed")
	}
}
