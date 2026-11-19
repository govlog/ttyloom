package main

import (
	"os"
	"path/filepath"
	"strings"
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
	cleanFiles(dir, t.TempDir())
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

// A received file that a version before media.OriginalName renamed them kept
// as ".part-notes.pdf" has the date of its message: it is not a temporary
// file of os.CreateTemp (".part-" and digits), it stays.
func TestCleanFilesKeepsReceivedPartName(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "irc_libera", "alice", ".part-notes.pdf")
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte("only copy"), 0o600)
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(p, old, old)
	cleanFiles(dir, t.TempDir())
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("received file removed: %v", err)
	}
}

// A download_dir or a cache that is empty or relative makes the patterns
// start at / or at the working directory: the files of other programs.
// Nothing is removed there.
func TestCleanFilesNeedsAbsoluteDirs(t *testing.T) {
	t.Chdir(t.TempDir())
	old := time.Now().Add(-8 * 24 * time.Hour)
	files := []string{filepath.Join("dl", ".part-1"), filepath.Join("dl", "x", "y", ".part-2"),
		filepath.Join("dl", "paste", "20260901-120000-1.png"), filepath.Join("cache", "gifs", "telegram", "a.mp4")}
	for _, p := range files {
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, nil, 0o600)
		os.Chtimes(p, old, old)
	}
	cleanFiles("dl", "cache")
	for _, p := range files {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("relative directory: %s removed", p)
		}
	}
}

// The pictures of the boxes — GIF previews, media thumbnails, custom emojis —
// leave the cache after a week unused, and pasted images never sent leave
// download_dir after a day: nothing else ever removed them. The history of a
// network, beside them in the cache, stays.
func TestCleanFilesOldPictures(t *testing.T) {
	dl, cache := t.TempDir(), t.TempDir()
	old := time.Now().Add(-8 * 24 * time.Hour)
	gone := []string{filepath.Join(cache, "gifs", "telegram", "a.mp4"), filepath.Join(cache, "thumbs", "discord", "b.jpg"),
		filepath.Join(cache, "emoji", "discord", "c.png"), filepath.Join(dl, "paste", "20260901-120000-1.png")}
	kept := []string{filepath.Join(cache, "telegram", "history", "1.gob"), filepath.Join(cache, "gifs", "telegram", "fresh.mp4"),
		filepath.Join(dl, "20260901-120000_telegram_1_x_7.jpg"), filepath.Join(dl, "paste", "notes.txt")}
	for _, p := range append(gone, kept...) {
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, nil, 0o600)
		if !strings.Contains(p, "fresh") {
			os.Chtimes(p, old, old)
		}
	}
	cleanFiles(dl, cache)
	for _, p := range gone {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("left behind: %s", p)
		}
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("removed: %s", p)
		}
	}
}
