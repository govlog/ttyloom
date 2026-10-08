package ui

import (
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

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
		return strings.ReplaceAll(render.CleanLine(s), ";", ",")
	}
	return clean(title), runewidth.Truncate(clean(body), 200, "")
}

// markupEscape : &, < and > only, the entities every daemon reads — GNOME
// Shell shows the &#39; of html.EscapeString as it is.
var markupEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// desktopArgs : notifyArgs for notify-send. The body is markup for the
// notification daemon (Desktop Notifications spec: <b>, <a href>, <img>):
// escaped, a remote text shows as it was typed. The summary is plain text.
func desktopArgs(title, body string) (string, string) {
	t, b := notifyArgs(title, body)
	return t, markupEscape.Replace(b)
}

// mentioned : m calls on me — a mention entity or my @name, and where the
// name is the identity (IRC) my name as a word too ("chris: hello"). The one
// rule of the bell, the notification, the hot window and the hooks.
func (u *UI) mentioned(c *model.Chat, m *model.Msg) bool {
	me := u.selfOf(c.Net)
	return mentionsMe(m, me.ID, me.Name) || (backendCaps(u.netOf(c.Net)).NameIsID && me.Name != "" && saidWord(m.Text, me.Name))
}

// wordRes : the regexps of saidWord, by word.
// ponytail: never emptied — one entry per name I had in the session, a few.
var wordRes sync.Map

// saidWord : word stands alone in text, case apart. Compiled once per word:
// it runs on every message that comes in.
func saidWord(text, word string) bool {
	re, ok := wordRes.Load(word)
	if !ok {
		re, _ = wordRes.LoadOrStore(word, regexp.MustCompile(`(?i)(?:^|\W)`+regexp.QuoteMeta(word)+`(?:$|\W)`))
	}
	return re.(*regexp.Regexp).MatchString(text)
}

// alertGap : one bell and one notification at most in that time; the hot
// messages in between merge into the last one, given once it is over.
const alertGap = 2 * time.Second

// alertNote : a hot message waiting for its bell and notification.
type alertNote struct {
	chat  *model.Chat
	msg   model.Msg
	quiet bool // a reaction: the notification, never the bell
}

// alert rings and notifies for a hot message: at once when the last alert is
// alertGap old, else at the end of that time (tick), for the last message
// only — a burst of fifty messages gives two alerts, not fifty.
func (u *UI) alert(chat *model.Chat, m *model.Msg) {
	u.alertNext = &alertNote{chat: chat, msg: *m}
	u.flushAlert(time.Now())
}

// flushAlert gives the alert waiting once its time has come; true when it
// did.
func (u *UI) flushAlert(now time.Time) bool {
	a := u.alertNext
	if a == nil || now.Sub(u.alertAt) < alertGap {
		return false
	}
	u.alertNext, u.alertAt = nil, now
	if u.muted[a.chat.Key()] || u.seenNow(a.chat, &a.msg) { // muted, or read, while it waited
		return false
	}
	if u.cfg.Bell && !a.quiet {
		u.t.WriteString("\a") // flushed at the next draw()
	}
	u.notify(a.chat, &a.msg)
	return true
}

// seenNow tells whether m of chat is on the screen of a focused terminal —
// its window, a window in query on it (/q echoes its replies), or the
// aggregate showing its network.
func (u *UI) seenNow(chat *model.Chat, m *model.Msg) bool {
	i := u.ws.ForChat(chat.Key())
	v := u.view()
	query := v.Target != nil && v.Target.Key() == chat.Key()
	return u.focused && i >= 0 && (v == u.ws.List[i] || query || (v == u.agg && u.netShown(m)))
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
		t, b := desktopArgs(title, body)
		cmd := exec.Command(notifySendBin, "-a", "ttyloom", "-i", "dialog-information", "--", t, b)
		if cmd.Start() == nil { // error ignored: desktop notification, best effort
			go cmd.Wait()
		}
	}
}
