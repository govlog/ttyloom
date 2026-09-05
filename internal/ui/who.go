package ui

import (
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/theme"
)

// Hover popup of the tick (who read the message) and of a reaction (who
// reacted with what): a small box stuck under the pointer, filled by the
// backend through EvWho and cached for a short while.

const whoTTL = time.Minute

type whoKey struct {
	net    string // ids collide between networks: the chat is (net, chatID)
	chatID int64
	id     int
	react  bool
}

type whoEntry struct {
	text string
	at   time.Time
}

type whoBox struct {
	key  whoKey
	item *Item // the popup dies with its message (window change, /clear)
	x, y int   // screen cell of the pointer when the zone was entered
	text string
}

// whoZone : the act under the pointer that carries a popup — the tick, or a
// reaction already on the message. The quick-react button of the help line
// shares KeyReact: only an emoji found on the message counts.
func whoZone(h hit) *render.Action {
	if h.item == nil || h.item.Msg == nil || h.act == nil {
		return nil
	}
	switch h.act.Key {
	case render.KeyTicks:
		return h.act
	case render.KeyReact:
		for _, r := range h.item.Msg.Reactions {
			if r.Emoji == h.act.Emoji {
				return h.act
			}
		}
	}
	return nil
}

// whoAt updates the popup at pointer (x, y). true when a repaint is needed:
// the popup opened, moved to another zone, or closed.
func (u *UI) whoAt(x, y int) bool {
	var z *render.Action
	var h hit
	if u.hoverItem(x, y) != nil { // same gating as the hover (overlays, sidebar)
		x0, _ := u.layout()
		h = hitAt(u.hits, x-x0, y)
		z = whoZone(h)
	}
	if z == nil {
		if u.who == nil {
			return false
		}
		u.who = nil
		return true
	}
	m := h.item.Msg
	key := whoKey{net: m.Net, chatID: m.ChatID, id: m.ID, react: z.Key == render.KeyReact}
	if u.who != nil && u.who.key == key {
		return false // still on the same zone: the popup stays put
	}
	u.who = &whoBox{key: key, item: h.item, x: x, y: y, text: u.whoText(key, m)}
	return true
}

// whoText gives the cached line, or "loading…" while the backend is asked. The
// "loading" is cached too: hovering again during the request asks nothing. With
// nothing asked (chat unknown, or network without the capability) the popup
// says so instead of waiting for ever on an answer that will never come.
func (u *UI) whoText(key whoKey, m *model.Msg) string {
	if e, ok := u.whoCache[key]; ok && time.Since(e.at) < whoTTL {
		return e.text
	}
	// One guard for both: u.net covers the chat left unknown (it gives nil for
	// a nil chat) and takes over from the old nil-backend test.
	c := u.chatOf(m)
	caps := u.caps(c)
	asked := false
	if b := u.net(c); b != nil {
		// The zones themselves are gated in the drawing; the call is gated here
		// too, so nothing is ever asked of a network that has none of it.
		switch {
		case key.react && caps.Reactions:
			b.WhoReacted(u.ctx, c, m.ID, m.Reactions)
			asked = true
		case !key.react && caps.ReadReceipts:
			b.WhoRead(u.ctx, c, m.ID, c.ReadOutboxMaxID)
			asked = true
		}
	}
	if !asked {
		return i18n.T("net_unsupported", m.Net) // never cached: a backend can come back
	}
	u.whoCache[key] = whoEntry{text: i18n.T("loading"), at: time.Now()}
	return i18n.T("loading")
}

// whoEv : answer of the backend — cached, and the open popup follows. The text is
// remote: CleanLine once here.
func (u *UI) whoEv(e model.EvWho) {
	key := whoKey{net: u.dispatchNet, chatID: e.ChatID, id: e.ID, react: e.React}
	en := whoEntry{text: render.CleanLine(e.Text), at: time.Now()}
	u.whoCache[key] = en
	if u.who != nil && u.who.key == key {
		u.who.text = en.text
	}
}

// whoRect gives the popup box, under and right of the pointer, brought back
// on the screen near the edges.
func (u *UI) whoRect() rect {
	cols, rows := u.t.Cols, u.viewRows()
	w := min(render.Width(u.who.text)+2, min(50, max(4, cols)))
	h := min(len(render.Plain(u.who.text, theme.Style{}, max(1, w-2)))+2, max(3, rows))
	return rect{row: max(0, min(u.who.y+1, rows-h)), col: max(0, min(u.who.x+2, cols-w)), h: h, w: w}
}

// Lines gives the whole box, one render.Line per screen line.
func (w *whoBox) Lines(th theme.Theme, wd, h int) []render.Line {
	body, edge, _ := boxStyles(th)
	b := boxDraw{edge: edge, fill: body, inner: max(0, wd-2)}
	out := []render.Line{b.bar("┌", "┐")}
	for _, l := range render.Plain(w.text, body, b.inner) {
		out = append(out, b.row(l.Spans...))
	}
	out = append(out, b.bar("└", "┘"))
	if len(out) < h { // Plain gave fewer lines than the rect (never in practice)
		h = len(out)
	}
	return out[:h]
}
