package ui

import (
	"slices"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/theme"
)

// The newest pinned message of a chat shows in a banner on the first row of
// its window, in the error colour; a click jumps to the message. The local
// note (/note, note.go) takes the same banner and wins over the pin.

// pinInfo : the newest pinned message known of a chat — what the banner shows.
// From and Text stay empty when the message was never loaded (a live pin of an
// old message): the banner then shows a generic label, the jump still works.
type pinInfo struct {
	ID   int
	From string
	Text string
}

// pinned : EvPinned — the pin flags of the loaded copies and the banner of
// the chat follow the server.
func (u *UI) pinned(e model.EvPinned) {
	key := u.evKey(e.ChatID)
	if e.Fetched { // whole answer: the banner is replaced, never merged
		if e.Top == nil {
			delete(u.pins, key)
			return
		}
		u.setPin(key, e.Top.ID, e.Top)
		u.flagPinned(key, []int{e.Top.ID}, true)
		return
	}
	if len(e.IDs) == 0 {
		return
	}
	u.flagPinned(key, e.IDs, e.Pinned)
	if e.Pinned {
		if id := slices.Max(e.IDs); id >= u.pins[key].ID {
			u.setPin(key, id, u.loadedMsg(key, id))
		}
		return
	}
	// The shown pin went: the newest loaded pin takes its place, else nothing
	// shows — the server is not asked for the one before.
	if p, ok := u.pins[key]; ok && slices.Contains(e.IDs, p.ID) {
		delete(u.pins, key)
		if m := u.newestLoadedPin(key); m != nil {
			u.setPin(key, m.ID, m)
		}
	}
}

// setPin : the banner of the chat shows message id; m fills it when loaded.
func (u *UI) setPin(key model.ChatKey, id int, m *model.Msg) {
	if u.pins == nil {
		u.pins = map[model.ChatKey]pinInfo{}
	}
	p := pinInfo{ID: id}
	if m != nil {
		p.From, p.Text = render.CleanLine(m.From), render.CleanLine(m.Summary())
	}
	u.pins[key] = p
}

// flagPinned moves Msg.Pinned on every loaded copy of the messages (the menu
// label reads it), and the cache keeps the change.
func (u *UI) flagPinned(key model.ChatKey, ids []int, pinned bool) {
	found := false
	for _, w := range u.viewsOf(key) {
		for _, it := range w.Items {
			if it.Msg != nil && it.Msg.Key() == key && slices.Contains(ids, it.Msg.ID) && it.Msg.Pinned != pinned {
				it.Msg.Pinned = pinned
				found = true
			}
		}
	}
	if found {
		u.markDirty(key)
	}
}

// loadedMsg gives a loaded copy of the message, nil when no view holds it.
func (u *UI) loadedMsg(key model.ChatKey, id int) *model.Msg {
	for _, w := range u.viewsOf(key) {
		for _, it := range w.Items {
			if it.Msg != nil && it.Msg.ID == id && it.Msg.Key() == key {
				return it.Msg
			}
		}
	}
	return nil
}

// newestLoadedPin gives the loaded pinned message with the highest id, nil
// with none.
func (u *UI) newestLoadedPin(key model.ChatKey) *model.Msg {
	var top *model.Msg
	for _, w := range u.viewsOf(key) {
		for _, it := range w.Items {
			if it.Msg != nil && it.Msg.Pinned && it.Msg.Key() == key && (top == nil || it.Msg.ID > top.ID) {
				top = it.Msg
			}
		}
	}
	return top
}

// pinBanner : the banner of the window — the local note (/note) first, else
// the newest pinned message. jump is the message a click goes to, 0 for a
// note; ok false = no banner.
func (u *UI) pinBanner(w *Window) (text string, jump int, ok bool) {
	if w == nil || w.Chat == nil || w.Search != "" {
		return "", 0, false
	}
	k := w.Chat.Key()
	if n := u.notes[k]; n != "" {
		return i18n.T("note_banner", n), 0, true
	}
	if p, found := u.pins[k]; found {
		body := p.Text
		if p.From != "" {
			body = p.From + ": " + body
		}
		if body == "" {
			body = i18n.T("pinned_message")
		}
		return "📌 " + body, p.ID, true
	}
	return "", 0, false
}

// pinRect : the box of the banner, the whole first row of the message area;
// ok false when the window shows none.
func (u *UI) pinRect() (rect, bool) {
	if _, _, ok := u.pinBanner(u.view()); !ok {
		return rect{}, false
	}
	x0, cols := u.layout()
	return rect{row: 0, col: x0, h: 1, w: cols}, true
}

// pinLines : the banner drawn — one line over the whole width, in the error
// colour, inverted like the pill of jump.go.
func (u *UI) pinLines(cols int) []render.Line {
	text, _, _ := u.pinBanner(u.view())
	st := theme.Style{FG: u.th.BG, BG: u.th.Color(theme.Error), Bold: true}
	return []render.Line{{Spans: []render.Span{{Text: padTo(render.Truncate(" "+text, cols, "…"), cols), Style: st}}}}
}
