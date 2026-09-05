package cache

import (
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
