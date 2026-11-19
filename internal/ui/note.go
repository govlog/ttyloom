package ui

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
)

// Local note of a chat (/note): one line shown in the red banner at the top
// of its window, like a pinned message, and it wins over one (pin.go). Local
// to ttyloom: the network is not told. /unnote removes it.

// noteMax : longest note, in cells.
const noteMax = 200

// noteFile : TOML form of notes.toml — [notes] "<account>@<net>:<id>" =
// "text" (noteKey). A note of no account has the key of aliases.toml: an IRC
// note, or an entry of a file older than the accounts.
type noteFile struct {
	Notes map[string]string `toml:"notes"`
}

// noteKey : a note belongs to a chat and to the account that wrote it.
// Scope rule (noteScope): a network with server history (Caps.History:
// Telegram, Discord) has a stable user id, so a note belongs to the account;
// on IRC the self id is a hash of the nick of the moment (/nick changes it),
// so a note belongs to the network itself, acct 0, as its cache does. On a
// network with accounts, acct 0 is an entry of a file older than the
// accounts, never shown until attachNotes gives it an account.
type noteKey struct {
	acct int64
	chat model.ChatKey
}

// String : the key in notes.toml.
func (k noteKey) String() string {
	if k.acct == 0 {
		return aliasKey(k.chat)
	}
	return strconv.FormatInt(k.acct, 10) + "@" + k.chat.Net + ":" + strconv.FormatInt(k.chat.ID, 10)
}

// parseNoteKey reads a key of notes.toml. A "<net>" part whose head is not
// "<number>@" stays the network, as an older version reads it: such a key
// is kept and written back, never dropped.
func parseNoteKey(s string) (noteKey, bool) {
	k, ok := parseAliasKey(s)
	if !ok {
		return noteKey{}, false
	}
	if a, net, found := strings.Cut(k.Net, "@"); found && net != "" {
		if acct, err := strconv.ParseInt(a, 10, 64); err == nil && acct != 0 {
			return noteKey{acct, model.ChatKey{Net: net, ID: k.ID}}, true
		}
	}
	return noteKey{chat: k}, true
}

func notesPath() string { return filepath.Join(config.Dir(), "notes.toml") }

// loadNotes gives the notes of every account. A missing file = no note, not
// an error; an unreadable line (file edited by hand) is ignored.
func loadNotes(path string) (map[noteKey]string, error) {
	out := map[noteKey]string{}
	var f noteFile
	if err := loadTOML(path, &f); err != nil {
		return out, err
	}
	for k, v := range f.Notes {
		key, ok := parseNoteKey(k)
		v = strings.TrimSpace(render.CleanLine(v))
		if !ok || v == "" || render.Width(v) > noteMax {
			continue
		}
		out[key] = v
	}
	return out, nil
}

// saveNotes writes the whole file again: a note dropped from the map goes
// away. The map holds the notes of every account, shown or not.
func saveNotes(path string, notes map[noteKey]string) error {
	f := noteFile{Notes: make(map[string]string, len(notes))}
	for k, v := range notes {
		f.Notes[k.String()] = v
	}
	return saveTOML(path, f)
}

// noteScope : the account whose notes show on net, when self is our id
// there (EvReady, or the account that wrote the cache). ok false: not known
// (no backend to tell the rule, id 0), and no note of net shows.
func (u *UI) noteScope(net string, self int64) (acct int64, ok bool) {
	b := u.netOf(net)
	switch {
	case b == nil:
		return 0, false
	case !b.Caps().History:
		return 0, true
	}
	return self, self != 0
}

// attachNotes : self is our id on net now — from EvReady, or before it the
// account that wrote the cache, whose chats show. It stays after the network
// stops: its chats stay on the screen until the next EvReady.
//
// The notes of a file older than the accounts (no account in their key) go
// to the first account known on their network after the update: the one
// that wrote them, in nearly every case. They are written back under it at
// once. A note this account already has on the chat wins: the old entry then
// stays as it is in the file.
func (u *UI) attachNotes(net string, self int64) {
	acct, ok := u.noteScope(net, self)
	if !ok {
		delete(u.noteAcct, net)
		return
	}
	if u.noteAcct == nil {
		u.noteAcct = map[string]int64{}
	}
	u.noteAcct[net] = acct
	if acct == 0 {
		return
	}
	moved := false
	for k, v := range u.notes {
		if k.acct != 0 || k.chat.Net != net {
			continue
		}
		mine := noteKey{acct, k.chat}
		if _, taken := u.notes[mine]; !taken {
			u.notes[mine] = v
			delete(u.notes, k)
			moved = true
		}
	}
	if moved {
		if err := saveNotes(notesPath(), u.notes); err != nil {
			u.status0(i18n.T("note_error", err))
		}
	}
}

// noteKeyOf : the key of the note of chat k for the account shown now; ok
// false when that account is not known yet.
func (u *UI) noteKeyOf(k model.ChatKey) (noteKey, bool) {
	acct, ok := u.noteAcct[k.Net]
	return noteKey{acct, k}, ok
}

// noteOf : the note of chat k for the account shown now, "" with none.
func (u *UI) noteOf(k model.ChatKey) string {
	if nk, ok := u.noteKeyOf(k); ok {
		return u.notes[nk]
	}
	return ""
}

// noteCmd : /note <text> on the chat of the window; the text replaces the
// note already there. /note alone shows the note.
func (u *UI) noteCmd(w *Window, text string) {
	if w.Chat == nil {
		w.AddSys(i18n.T("window_unbound"))
		return
	}
	k, ok := u.noteKeyOf(w.Chat.Key())
	if !ok {
		w.AddSys(i18n.T("note_no_account"))
		return
	}
	text = strings.TrimSpace(render.CleanLine(text))
	if text == "" {
		if n := u.notes[k]; n != "" {
			w.AddSys(i18n.T("note_current", n))
		} else {
			w.AddSys(i18n.T("usage_note"))
		}
		return
	}
	if render.Width(text) > noteMax {
		w.AddSys(i18n.T("note_too_long", noteMax))
		return
	}
	if u.notes == nil {
		u.notes = map[noteKey]string{}
	}
	u.notes[k] = text
	if err := saveNotes(notesPath(), u.notes); err != nil {
		w.AddSys(i18n.T("note_error", err))
	}
	w.AddSys(i18n.T("note_set", text))
}

// unnoteCmd : /unnote drops the note of the chat of the window.
func (u *UI) unnoteCmd(w *Window) {
	if w.Chat == nil {
		w.AddSys(i18n.T("window_unbound"))
		return
	}
	k, ok := u.noteKeyOf(w.Chat.Key())
	if !ok || u.notes[k] == "" {
		w.AddSys(i18n.T("note_none"))
		return
	}
	delete(u.notes, k)
	if err := saveNotes(notesPath(), u.notes); err != nil {
		w.AddSys(i18n.T("note_error", err))
	}
	w.AddSys(i18n.T("note_cleared"))
}
