package cache

import (
	"bytes"
	"encoding/gob"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/model"
	// The Telegram backend registers with gob the concrete types it
	// stores behind the model's opaque handles; the cache itself knows
	// none of them. Without this import the round-trip would fail here
	// only, never in the binary (main pulls in tgc).
	_ "github.com/govlog/ttyloom/protocols/tgc"
)

func TestNewCreatesDirs0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub")
	New(dir, 2000)
	for _, p := range []string{dir, filepath.Join(dir, "history")} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Fatalf("%s: perm %v, want 0700", p, fi.Mode().Perm())
		}
	}
}

func TestSaveLoadHistoryRoundTrip(t *testing.T) {
	c := New(t.TempDir(), 2000)

	entities := []model.Span{
		{Start: 0, End: 5, Kind: model.SpanBold},
		{Start: 6, End: 9, Kind: model.SpanURL, URL: "https://example.com"},
	}
	loc := &tg.InputPhotoFileLocation{ID: 42, AccessHash: 7, FileReference: []byte{1, 2, 3}, ThumbSize: "x"}
	msg := model.Msg{
		ID:       1,
		ChatID:   100,
		Text:     "hello foo",
		Entities: entities,
		Media: &model.Media{
			Kind:    model.MediaPhoto,
			Loc:     loc,
			Frames:  [][]byte{{1}},
			FrameW:  10,
			FrameH:  20,
			Delay:   time.Second,
			Frame:   2,
			Next:    time.Now(),
			KittyID: 5,
			State:   model.MediaReady,
			Err:     "erreur transitoire",
		},
		Pending: true,
		TmpID:   99,
		Err:     "boom",
	}

	if err := c.SaveHistory(100, []model.Msg{msg}); err != nil {
		t.Fatal(err)
	}
	got, err := c.LoadHistory(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	gm := got[0]

	if !reflect.DeepEqual(gm.Entities, entities) {
		t.Fatalf("entities read back: %#v", gm.Entities)
	}
	if !reflect.DeepEqual(gm.Media.Loc, loc) {
		t.Fatalf("loc read back: %#v", gm.Media.Loc)
	}
	if gm.Media.Frames != nil || gm.Media.FrameW != 0 || gm.Media.FrameH != 0 ||
		gm.Media.Delay != 0 || gm.Media.Frame != 0 || !gm.Media.Next.IsZero() ||
		gm.Media.KittyID != 0 || gm.Media.State != model.MediaNone || gm.Media.Err != "" {
		t.Fatalf("Media runtime fields not reset to zero: %+v", gm.Media)
	}
	if gm.Pending || gm.TmpID != 0 || gm.Err != "" {
		t.Fatalf("Msg runtime fields not reset to zero: %+v", gm)
	}

	// The copy of the caller must never be touched.
	if msg.Media.Frames == nil || msg.Media.State != model.MediaReady || msg.Media.KittyID != 5 {
		t.Fatalf("caller's Media mutated: %+v", msg.Media)
	}
	if !msg.Pending || msg.TmpID != 99 || msg.Err != "boom" {
		t.Fatalf("caller's Msg mutated: %+v", msg)
	}
}

func TestSaveHistoryKeepsLastMax(t *testing.T) {
	c := New(t.TempDir(), 200) // explicit cap: checks that SaveHistory follows the parameter, not a fixed constant

	var msgs []model.Msg
	for i := 1; i <= 250; i++ {
		msgs = append(msgs, model.Msg{ID: i, ChatID: 7})
	}
	if err := c.SaveHistory(7, msgs); err != nil {
		t.Fatal(err)
	}
	got, err := c.LoadHistory(7)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 200 {
		t.Fatalf("len=%d, want 200", len(got))
	}
	if got[0].ID != 51 || got[199].ID != 250 {
		t.Fatalf("ids: first=%d last=%d, want 51..250", got[0].ID, got[199].ID)
	}
}

func TestLoadHistoryAbsent(t *testing.T) {
	c := New(t.TempDir(), 2000)
	got, err := c.LoadHistory(999)
	if got != nil || err != nil {
		t.Fatalf("got=%v err=%v, want (nil, nil)", got, err)
	}
}

func TestLoadHistoryCorrupted(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, 2000)
	p := filepath.Join(dir, "history", "42.gob")
	if err := os.WriteFile(p, []byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01, 0x02}, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := c.LoadHistory(42)
	if got != nil || err != nil {
		t.Fatalf("corrupted file: got=%v err=%v, want (nil, nil)", got, err)
	}
}

func TestSaveLoadDialogsRoundTrip(t *testing.T) {
	c := New(t.TempDir(), 2000)

	peer := &tg.InputPeerChannel{ChannelID: 55, AccessHash: 66}
	chats := []model.Chat{
		{ID: 1, Kind: model.ChatChannel, Title: "chan", Peer: peer, Pinned: true},
		// "Saved Messages": peers.User.InputPeer() gives this type
		// when Self() is true (protocols/tgc/client.go).
		{ID: 2, Kind: model.ChatUser, Title: "Moi", Peer: &tg.InputPeerSelf{}},
	}
	if err := c.SaveDialogs(chats, 4242); err != nil {
		t.Fatal(err)
	}
	got, selfID, err := c.LoadDialogs()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || selfID != 4242 {
		t.Fatalf("len=%d selfID=%d", len(got), selfID)
	}
	if !got[0].Pinned {
		t.Fatalf("Pinned lost: %+v", got[0])
	}
	if !reflect.DeepEqual(got[0].Peer, peer) {
		t.Fatalf("peer read back: %#v", got[0].Peer)
	}
	if _, ok := got[1].Peer.(*tg.InputPeerSelf); !ok {
		t.Fatalf("self peer read back: %#v", got[1].Peer)
	}
}

func TestWipe(t *testing.T) {
	c := New(t.TempDir(), 2000)
	if err := c.SaveDialogs([]model.Chat{{ID: 1}}, 7); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveHistory(1, []model.Msg{{ID: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Wipe(); err != nil {
		t.Fatal(err)
	}
	chats, selfID, _ := c.LoadDialogs()
	msgs, _ := c.LoadHistory(1)
	if chats != nil || selfID != 0 || msgs != nil {
		t.Fatalf("after Wipe: %v %d %v", chats, selfID, msgs)
	}
	// The history directory is made again: a write must go through.
	if err := c.SaveHistory(1, []model.Msg{{ID: 3}}); err != nil {
		t.Fatal(err)
	}
}

// The larger variant of a photo (Media.Full) is saved like the media of the
// line: handle and path kept, runtime state reset, the caller's copy untouched.
func TestSaveHistoryResetsFullVariant(t *testing.T) {
	c := New(t.TempDir(), 10)
	full := &model.Media{Kind: model.MediaPhoto, Loc: &tg.InputPhotoFileLocation{ID: 42, ThumbSize: "y"},
		Path: "/dl/full.jpg", State: model.MediaLoading, Frames: [][]byte{{1}}, KittyID: 3}
	msg := model.Msg{ID: 1, ChatID: 100, Media: &model.Media{Kind: model.MediaPhoto, Loc: &tg.InputPhotoFileLocation{ID: 42, ThumbSize: "x"}, Full: full}}
	if err := c.SaveHistory(100, []model.Msg{msg}); err != nil {
		t.Fatal(err)
	}
	got, err := c.LoadHistory(100)
	if err != nil || len(got) != 1 {
		t.Fatalf("%v, %d messages", err, len(got))
	}
	gf := got[0].Media.Full
	if gf == nil || gf.Path != "/dl/full.jpg" || gf.State != model.MediaNone || gf.Frames != nil || gf.KittyID != 0 {
		t.Fatalf("full read back: %+v", gf)
	}
	if loc, ok := gf.Loc.(*tg.InputPhotoFileLocation); !ok || loc.ThumbSize != "y" {
		t.Fatalf("full loc read back: %#v", gf.Loc)
	}
	if full.State != model.MediaLoading || full.Frames == nil || full.KittyID != 3 {
		t.Fatalf("caller's Full mutated: %+v", full)
	}
}

// A file of an older format that still decodes: read as a cache when the
// files are the only copy (KeepOld), read as no cache at all otherwise.
func TestLoadHistoryOlderVersion(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, 2000)
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(cacheHeader{Version: cacheVersion - 1}); err != nil {
		t.Fatal(err)
	}
	if err := enc.Encode([]model.Msg{{ID: 1, ChatID: 42, Text: "older format"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "history", "42.gob"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.LoadHistory(42); got != nil {
		t.Fatalf("older format read as a cache with KeepOld off: %+v", got)
	}
	c.KeepOld = true
	got, err := c.LoadHistory(42)
	if err != nil || len(got) != 1 || got[0].Text != "older format" {
		t.Fatalf("older format dropped with KeepOld: %+v, %v", got, err)
	}
}

// A file that does not decode is kept as a .bak copy when a write replaces it.
func TestSaveHistoryKeepsUnreadableCopy(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, 2000)
	p := filepath.Join(dir, "history", "42.gob")
	junk := []byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01, 0x02}
	if err := os.WriteFile(p, junk, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveHistory(42, []model.Msg{{ID: 1, ChatID: 42}}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(p + ".bak-0"); err != nil || !bytes.Equal(got, junk) {
		t.Fatalf("unreadable file not kept as .bak-0: %v %v", got, err)
	}
	if got, _ := c.LoadHistory(42); len(got) != 1 {
		t.Fatalf("new file not written: %+v", got)
	}
}

// Archive sets the directory aside with its files and makes an empty one;
// with no history file nothing moves.
func TestArchive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "net")
	c := New(dir, 2000)
	if err := c.Archive(); err != nil {
		t.Fatal(err)
	}
	if aside, _ := filepath.Glob(dir + ".old-*"); len(aside) != 0 {
		t.Fatalf("empty cache set aside: %v", aside)
	}
	if err := c.SaveHistory(1, []model.Msg{{ID: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Archive(); err != nil {
		t.Fatal(err)
	}
	aside, _ := filepath.Glob(dir + ".old-*")
	if len(aside) != 1 {
		t.Fatalf("cache not set aside: %v", aside)
	}
	if _, err := os.Stat(filepath.Join(aside[0], "history", "1.gob")); err != nil {
		t.Fatalf("history file not in the archive: %v", err)
	}
	if msgs, _ := c.LoadHistory(1); msgs != nil {
		t.Fatalf("after Archive: %v", msgs)
	}
	if err := c.SaveHistory(1, []model.Msg{{ID: 3}}); err != nil { // the directory is made again
		t.Fatal(err)
	}
}
