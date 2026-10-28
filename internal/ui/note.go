package ui

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

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

// noteFile : TOML form of notes.toml — [notes] "<net>:<id>" = "text", keys
// written like aliases.toml.
type noteFile struct {
	Notes map[string]string `toml:"notes"`
}

func notesPath() string { return filepath.Join(config.Dir(), "notes.toml") }

// loadNotes gives the notes by chat. A missing file = no note, not an error;
// an unreadable line (file edited by hand) is ignored.
func loadNotes(path string) (map[model.ChatKey]string, error) {
	out := map[model.ChatKey]string{}
	var f noteFile
	if _, err := toml.DecodeFile(path, &f); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return out, err
	}
	for k, v := range f.Notes {
		key, ok := parseAliasKey(k)
		v = strings.TrimSpace(render.CleanLine(v))
		if !ok || v == "" || render.Width(v) > noteMax {
			continue
		}
		out[key] = v
	}
	return out, nil
}

// saveNotes writes the whole file again: a note dropped from the map goes away.
func saveNotes(path string, notes map[model.ChatKey]string) error {
	f := noteFile{Notes: make(map[string]string, len(notes))}
	for k, v := range notes {
		f.Notes[aliasKey(k)] = v
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(f); err != nil {
		return err
	}
	return config.WriteAtomic(path, buf.Bytes(), 0o600)
}

// noteCmd : /note <text> on the chat of the window; the text replaces the
// note already there. /note alone shows the note.
func (u *UI) noteCmd(w *Window, text string) {
	if w.Chat == nil {
		w.AddSys(i18n.T("window_unbound"))
		return
	}
	k := w.Chat.Key()
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
		u.notes = map[model.ChatKey]string{}
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
	k := w.Chat.Key()
	if u.notes[k] == "" {
		w.AddSys(i18n.T("note_none"))
		return
	}
	delete(u.notes, k)
	if err := saveNotes(notesPath(), u.notes); err != nil {
		w.AddSys(i18n.T("note_error", err))
	}
	w.AddSys(i18n.T("note_cleared"))
}
