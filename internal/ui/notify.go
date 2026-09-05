package ui

import (
	"os/exec"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
)

// notifySendBin : path of notify-send, resolved once at start ("" when
// missing: /set notify desktop then stays silent).
var notifySendBin, _ = exec.LookPath("notify-send")

// notifyArgs gives the title and body cleaned to be shown outside the program
// (OSC 777 or notify-send): Clean (controls → space, but '\n' kept for the
// message drawing) then a replacement of '\n' (a notification holds on one
// line) and of ';' by ',' (it separates the fields of the OSC 777 sequence),
// body cut to 200 cells.
func notifyArgs(title, body string) (string, string) {
	clean := func(s string) string {
		s = strings.ReplaceAll(render.Clean(s), "\n", " ")
		return strings.ReplaceAll(s, ";", ",")
	}
	return clean(title), runewidth.Truncate(clean(body), 200, "")
}

// notify sends a desktop notification on a new message (same conditions as
// the bell, see newMessage). Title = title of the chat ("<title> · <nick>" in
// a group), body = text or media label.
func (u *UI) notify(chat *model.Chat, m *model.Msg) {
	title := u.title(chat)
	if chat.Kind != model.ChatUser {
		title += " · " + m.From
	}
	body := m.Text
	if body == "" && m.Media != nil {
		body = m.Media.Label
	}
	switch u.cfg.Notify {
	case "terminal":
		t, b := notifyArgs(title, body)
		u.t.WriteString("\x1b]777;notify;" + t + ";" + b + "\x1b\\") // flushed at the next draw()
	case "desktop":
		if notifySendBin == "" {
			return
		}
		t, b := notifyArgs(title, body)
		cmd := exec.Command(notifySendBin, "-a", "ttyloom", "-i", "dialog-information", "--", t, b)
		if cmd.Start() == nil { // error ignored: desktop notification, best effort
			go cmd.Wait()
		}
	}
}
