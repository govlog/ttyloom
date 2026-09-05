package ui

import (
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

// normalizePaste : the clipboard is untrusted input, drawn again by drawInput.
func normalizePaste(raw string) string {
	return render.Clean(strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n"))
}

// pasteNeedsPrompt tells whether a multiline paste needs a decision (as is /
// code block / cancel) rather than a plain insert.
func pasteNeedsPrompt(s string) bool {
	return strings.Contains(s, "\n")
}

// maxPasteBytes : hard cap of a paste into the input line. Telegram sends
// 4096 characters at most anyway; the clipboard is read up to 64 MB
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
		u.pasteAsk, u.pasteIns = text, true
	case u.edit != nil: // while editing, a paste fills the text, it does not send it
		u.ed.Insert(text)
		u.sendTyping(w)
	case pasteNeedsPrompt(text):
		u.pasteAsk = text
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
	if r, c := []rune(u.ed.String()), u.ed.Cursor(); c > 0 && c <= len(r) && r[c-1] != '\n' {
		ins = "\n" + ins
	}
	u.ed.Insert(ins)
}

// pasteKey handles the keys while u.pasteAsk waits for a decision. Called at
// the head of key(), before the pager: it swallows every key.
func (u *UI) pasteKey(k term.Key) bool {
	text := u.pasteAsk
	switch {
	case k.Code == term.Paste:
		if next := normalizePaste(k.Text); pasteNeedsPrompt(next) {
			u.pasteAsk = next // a new multiline paste replaces the one waiting
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
