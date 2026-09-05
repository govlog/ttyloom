// Package cache keeps the dialogs and the history on disk (gob), for a
// faster start without waiting for Telegram.
package cache

import (
	"bytes"
	"cmp"
	"encoding/gob"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
)

// The concrete types behind the opaque handles of the model (Chat.Peer,
// PhotoLoc, Media.Loc, Msg.FromPhoto) are gob.Register-ed by the backend that
// owns them — see protocols/tgc/gob.go. The cache knows no protocol.

// 2: model.Span entities + opaque any handles — a v1 file no longer decodes.
const cacheVersion = 2

// defaultMaxHistory : messages kept per chat when New gets max <= 0 (older
// tests, callers that do not come from the config).
const defaultMaxHistory = 200

type cacheHeader struct {
	Version int
}

type Cache struct {
	dir        string
	historyDir string
	max        int // messages kept per chat (config cache_messages)
}

// New makes dir and dir/history (0700) and gives the cache. The creation
// errors are ignored here: they come back on their own at the first Save
// (ponytail: no error return in the signature asked for). max is the number
// of messages kept per chat (config cache_messages); max <= 0 falls back to
// defaultMaxHistory.
func New(dir string, max int) *Cache {
	if max <= 0 {
		max = defaultMaxHistory
	}
	historyDir := filepath.Join(dir, "history")
	_ = os.MkdirAll(historyDir, 0o700)
	return &Cache{dir: dir, historyDir: historyDir, max: max}
}

func (c *Cache) dialogsPath() string { return filepath.Join(c.dir, "dialogs.gob") }

func (c *Cache) historyPath(chatID int64) string {
	return filepath.Join(c.historyDir, strconv.FormatInt(chatID, 10)+".gob")
}

// readCache decodes the header then v from path. Missing file, bad header,
// other version or broken decoding: false, and the caller reads that as no
// cache at all (the file is written again at the next Save).
func readCache(path string, v any) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	dec := gob.NewDecoder(f)
	var hdr cacheHeader
	if err := dec.Decode(&hdr); err != nil || hdr.Version != cacheVersion {
		return false
	}
	return dec.Decode(v) == nil
}

// writeCache encodes the header then v, and writes it atomically
// (config.WriteAtomic: temporary file + rename in the directory of path).
func writeCache(path string, v any) error {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(cacheHeader{Version: cacheVersion}); err != nil {
		return err
	}
	if err := enc.Encode(v); err != nil {
		return err
	}
	return config.WriteAtomic(path, buf.Bytes(), 0o600)
}

// dialogsFile : payload of dialogs.gob. SelfID names the account: a cache
// written by another account is seen at start.
type dialogsFile struct {
	SelfID int64
	Chats  []model.Chat
}

// LoadDialogs gives the chats and the account that wrote them (0 = unknown,
// old format — the decoding fails and the file is written again).
func (c *Cache) LoadDialogs() ([]model.Chat, int64, error) {
	var f dialogsFile
	if !readCache(c.dialogsPath(), &f) {
		return nil, 0, nil
	}
	return f.Chats, f.SelfID, nil
}

func (c *Cache) SaveDialogs(chats []model.Chat, selfID int64) error {
	return writeCache(c.dialogsPath(), dialogsFile{SelfID: selfID, Chats: chats})
}

// Wipe erases the cache (dialogs and all the history) and makes the directory again.
func (c *Cache) Wipe() error {
	_ = os.Remove(c.dialogsPath())
	if err := os.RemoveAll(c.historyDir); err != nil {
		return err
	}
	return os.MkdirAll(c.historyDir, 0o700)
}

// RemoveHistory erases the history of a chat (left, blocked, emptied).
func (c *Cache) RemoveHistory(chatID int64) error {
	if err := os.Remove(c.historyPath(chatID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (c *Cache) LoadHistory(chatID int64) ([]model.Msg, error) {
	var msgs []model.Msg
	if !readCache(c.historyPath(chatID), &msgs) {
		return nil, nil
	}
	return msgs, nil
}

// SaveHistory keeps the last c.max messages by rising ID and writes a clean
// copy: the runtime state fields are reset without ever touching the data of
// the caller (Media is a shared pointer, so it is copied before any change).
func (c *Cache) SaveHistory(chatID int64, msgs []model.Msg) error {
	sorted := append([]model.Msg(nil), msgs...)
	slices.SortStableFunc(sorted, func(a, b model.Msg) int { return cmp.Compare(a.ID, b.ID) })
	if len(sorted) > c.max {
		sorted = sorted[len(sorted)-c.max:]
	}

	clean := make([]model.Msg, len(sorted))
	for i, m := range sorted {
		m.Pending = false
		m.TmpID = 0
		m.Err = ""
		if m.Media != nil {
			mc := *m.Media
			mc.State = model.MediaNone
			mc.Err = ""
			mc.Frames = nil
			mc.FrameW = 0
			mc.FrameH = 0
			mc.Delay = 0
			mc.Frame = 0
			mc.Next = time.Time{}
			mc.KittyID = 0
			mc.KittyAlt = 0
			m.Media = &mc
		}
		clean[i] = m
	}
	return writeCache(c.historyPath(chatID), clean)
}
