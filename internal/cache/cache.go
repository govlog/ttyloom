// Package cache keeps the dialogs and the history on disk (gob), for a
// faster start without waiting for Telegram.
package cache

import (
	"bytes"
	"cmp"
	"encoding/gob"
	"errors"
	"io/fs"
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
// 3 : Media.Full — a history written before it has no larger variant for its
// photos, and the start-up sync (messages newer than the cache only) would
// never bring one back: the old files are read as no cache at all, unless
// KeepOld says the files are the only copy there is.
const cacheVersion = 3

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
	// KeepOld : the files are the only copy of the history (IRC, no server
	// history): a history of an older format that still decodes is read as a
	// cache, never as no cache at all. Off, a network with server history
	// fetches again what the new format holds.
	KeepOld bool
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

// readCache decodes the header then v from path. version is the format of
// the file (0 without a readable header); err is fs.ErrNotExist with no file,
// else the decoding error. A file of another format that still decodes (gob
// leaves a missing field at zero) comes back with no error: the caller reads
// version and decides whether it is a cache.
func readCache(path string, v any) (version int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	dec := gob.NewDecoder(f)
	var hdr cacheHeader
	if err := dec.Decode(&hdr); err != nil {
		return 0, err
	}
	return hdr.Version, dec.Decode(v)
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

// LoadDialogs gives the chats and the account that wrote them (0 = unknown:
// no file, or one that does not decode — it is written again at the next
// Save). A file of an older format that still decodes is read: the list is
// refreshed by the network anyway.
func (c *Cache) LoadDialogs() ([]model.Chat, int64, error) {
	var f dialogsFile
	if _, err := readCache(c.dialogsPath(), &f); err != nil {
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

// Archive sets the whole cache aside — the directory renamed to
// <dir>.old-<time>, then made again empty — so that a history dropped for a
// wrong reason (account change, list of the chats unreadable) can still be
// recovered by hand. With no history file there is nothing to keep: no move.
func (c *Cache) Archive() error {
	if entries, _ := os.ReadDir(c.historyDir); len(entries) == 0 {
		return nil
	}
	if err := os.Rename(c.dir, c.dir+".old-"+time.Now().Format("20060102-150405")); err != nil {
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

// LoadHistory gives the cached messages of the chat: nil with no file, with
// a file that does not decode, or — unless KeepOld — with one of another
// format.
func (c *Cache) LoadHistory(chatID int64) ([]model.Msg, error) {
	var msgs []model.Msg
	ver, err := readCache(c.historyPath(chatID), &msgs)
	if err != nil || (ver != cacheVersion && !c.KeepOld) {
		return nil, nil
	}
	return msgs, nil
}

// keepUnreadable sets a history file that does not decode aside, as
// <name>.bak-<version> (one copy per format, 0: no readable header), before
// a write replaces it: a truncated file, or one in a format the current types
// cannot read, may be the only copy of that history.
func keepUnreadable(path string) {
	var probe []model.Msg
	ver, err := readCache(path, &probe)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return
	}
	_ = os.Rename(path, path+".bak-"+strconv.Itoa(ver))
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
		m.LiveAt = time.Time{}
		m.TmpID = 0
		m.Err = ""
		if m.Media != nil {
			m.Media = stripped(m.Media)
		}
		clean[i] = m
	}
	path := c.historyPath(chatID)
	keepUnreadable(path)
	return writeCache(path, clean)
}

// stripped : a copy of md (and of its Full variant) without the runtime state
// of the UI — download state, frames, kitty ids. Handle and path stay.
func stripped(md *model.Media) *model.Media {
	mc := *md
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
	if mc.Full != nil {
		mc.Full = stripped(mc.Full)
	}
	return &mc
}
