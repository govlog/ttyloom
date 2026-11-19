package ui

import (
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

// normalizePaste : the clipboard is untrusted input, drawn again by drawInput.
// A tab becomes four spaces: the draft holds no raw tab, and Clean would make
// it one space.
func normalizePaste(raw string) string {
	return render.Clean(strings.NewReplacer("\r\n", "\n", "\r", "\n", "\t", "    ").Replace(raw))
}

// pasteNeedsPrompt tells whether a multiline paste needs a decision (as is /
// code block / cancel) rather than a plain insert.
func pasteNeedsPrompt(s string) bool {
	return strings.Contains(s, "\n")
}

// maxPasteBytes : hard cap of a paste into the input line. Telegram sends
// 4096 characters at most anyway; a clipboard text is read up to twice that
// (clip.go), and every repaint has to walk what sits on the line.
const maxPasteBytes = 64 << 10

// pasteText inserts a paste, from the terminal (bracketed paste) as well as
// from the clipboard read by Ctrl+V.
func (u *UI) pasteText(w *Window, text string) {
	if len(text) > maxPasteBytes {
		w.AddSys(i18n.T("paste_too_big", render.HumanSize(int64(len(text))), render.HumanSize(maxPasteBytes)))
		return
	}
	switch {
	case u.prompt != nil: // login answer: always one line
		u.ed.Insert(strings.ReplaceAll(text, "\n", " "))
	case u.multi && pasteNeedsPrompt(text):
		// Expanded editor: the choice inserts into the draft (as is / fenced),
		// it never sends.
		u.pasteAsk, u.pasteIns, u.pasteAt = text, true, time.Now()
	case u.edit != nil: // while editing, a paste fills the text, it does not send it
		u.ed.Insert(text)
		u.sendTyping(w)
	case pasteNeedsPrompt(text):
		u.pasteAsk, u.pasteAt = text, time.Now()
	default:
		u.ed.Insert(text)
		u.sendTyping(w)
	}
}

// insertFenced puts text into the draft between ``` fences, each fence alone
// on its line so the send parser sees them.
func (u *UI) insertFenced(text string) {
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	ins := "```\n" + text + "```"
	r, c := []rune(u.ed.String()), u.ed.Cursor()
	if c > 0 && c <= len(r) && r[c-1] != '\n' {
		ins = "\n" + ins
	}
	if c < len(r) && r[c] != '\n' {
		ins += "\n"
	}
	u.ed.Insert(ins)
}

// promptGuard : a prompt that acts on one key (a confirmation, the paste and
// the image prompts) ignores that key for this long after it opens. Ctrl+V
// reads the clipboard in the background, so its prompt can open between two
// typed keys: a key that comes this soon was typed before the prompt showed.
const promptGuard = 300 * time.Millisecond

// typedAhead : k is one of the answer keys of a prompt opened at at, and
// comes within promptGuard: typing, not an answer. The caller swallows it and
// keeps the prompt.
func typedAhead(k term.Key, at time.Time, keys string) bool {
	return k.Code == term.None && strings.ContainsRune(keys, k.Rune) && time.Since(at) < promptGuard
}

// pasteKey handles the keys while u.pasteAsk waits for a decision. Called at
// the head of key(), before the pager: it swallows every key.
func (u *UI) pasteKey(k term.Key) bool {
	text := u.pasteAsk
	switch {
	case typedAhead(k, u.pasteAt, "ec"): // swallowed, the prompt stays
	case k.Code == term.Paste:
		if next := normalizePaste(k.Text); pasteNeedsPrompt(next) {
			u.pasteAsk, u.pasteAt = next, time.Now() // a new multiline paste replaces the one waiting
		} // otherwise: single line, ignored (no "Paste of 1 lines")
	case k.Code == term.None && k.Rune == 'e':
		u.pasteAsk = ""
		if u.pasteIns {
			u.pasteIns = false
			u.ed.Insert(text)
			break
		}
		u.send(u.sendWin(), text)
	case k.Code == term.None && k.Rune == 'c':
		u.pasteAsk = ""
		if u.pasteIns {
			u.pasteIns = false
			u.insertFenced(text)
			break
		}
		u.sendWith(u.sendWin(), text, true)
	case k.Code == term.Esc, k.Code == term.None && k.Rune == 'a':
		u.pasteAsk, u.pasteIns = "", false
	}
	return true
}
