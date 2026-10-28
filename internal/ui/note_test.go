package ui

import (
	"strings"
	"testing"
)

// TestNote : /note puts a local note, shown in the banner of the window and
// kept in notes.toml for the next start; /unnote removes both.
func TestNote(t *testing.T) {
	t.Setenv("TTYLOOM_DIR", t.TempDir())
	u, _, w := msgUI(t)
	u.noteCmd(w, "appeler le client")
	if text, jump, ok := u.pinBanner(w); !ok || jump != 0 || !strings.Contains(text, "appeler le client") {
		t.Fatalf("banner after /note: %q jump %d ok %v", text, jump, ok)
	}
	if n, _ := loadNotes(notesPath()); n[w.Chat.Key()] != "appeler le client" {
		t.Fatalf("notes.toml: %v", n)
	}
	u.unnoteCmd(w)
	if _, _, ok := u.pinBanner(w); ok {
		t.Fatal("the banner must go with /unnote")
	}
	if n, _ := loadNotes(notesPath()); len(n) != 0 {
		t.Fatalf("notes.toml after /unnote: %v", n)
	}
}
