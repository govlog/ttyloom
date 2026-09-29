package ui

import (
	"bytes"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
)

// The chat shown at exit comes back at the next start: last.toml keeps its
// key, and the chat opens again once its network has listed its chats.

// lastFile : TOML form of last.toml — the key of the chat, in the form of
// aliases.toml ("<net>:<id>").
type lastFile struct {
	Chat string `toml:"chat"`
}

// lastPath gives last.toml, beside config.toml (TTYLOOM_DIR included).
func lastPath() string { return filepath.Join(config.Dir(), "last.toml") }

// loadLast gives the chat of last.toml. A missing, unreadable or hand-broken
// file gives none: the client then starts on window 0, as before.
func loadLast(path string) model.ChatKey {
	var f lastFile
	if _, err := toml.DecodeFile(path, &f); err != nil {
		return model.ChatKey{}
	}
	k, _ := parseAliasKey(f.Chat)
	return k
}

// saveLast writes the chat of the current window, at exit. On window 0 the
// chat still waiting for its network is kept: a restart before the network
// came up must not forget it.
func (u *UI) saveLast() {
	k := u.lastChat
	if c := u.ws.Current().Chat; c != nil {
		k = c.Key()
	}
	var f lastFile
	if k.Net != "" {
		f.Chat = aliasKey(k)
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(f); err != nil {
		return
	}
	_ = config.WriteAtomic(lastPath(), buf.Bytes(), 0o600) // exit: the screen is gone, nobody to tell
}

// restoreLast : first chat list of net — the chat left last time opens again
// when it belongs to net, and only while the user is still on window 0.
func (u *UI) restoreLast(net string) {
	k := u.lastChat
	if k.Net != net {
		return
	}
	u.lastChat = model.ChatKey{}
	if c := u.chats[k]; c != nil && u.ws.Cur == 0 {
		u.openChat(c)
	}
}
