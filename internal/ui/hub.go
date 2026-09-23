package ui

import (
	"slices"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// Hub "Networks" (/networks, and at the first start): the networks and their
// state, the page of a module to add one, the basic actions on one. A page is
// the form of the module, drawn in place of the list; a network started from
// the hub brings it back at its EvReady or EvStopped.

type hubView int

const (
	hubList    hubView = iota // networks and add lines
	hubActions                // what can be done on hub.net
	hubConfirm                // remove hub.net? y/n
)

type hubRow struct {
	net string // a configured network, "" for an add line
	mod module.Module
}

type hubAction struct {
	label string
	do    func()
}

type hubBox struct {
	view    hubView
	cur     int
	net     string // actions and confirm: the network
	askAt   time.Time
	welcome bool // no network at the opening: the two lines of welcome
	// rows, acts, texts, title, help : what the view shows, computed by the
	// UI (hubRefresh) before each drawing and each key — Lines cannot read
	// the UI, and the states change under an open box.
	rows  []hubRow
	acts  []hubAction
	texts []string
	title string
	help  string
}

// openHub : /networks, the first start, and the way back after a network
// started from the hub.
func (u *UI) openHub() {
	u.hub = &hubBox{welcome: len(u.netList) == 0}
	if u.hubReturn == nil {
		u.hubReturn = map[string]bool{}
	}
}

// closeHub : Esc on the list. With no network, window 0 says how to come back.
func (u *UI) closeHub() {
	u.hub = nil
	if len(u.netList) == 0 {
		u.status0(i18n.T("hub_none"))
	}
}

// hubRows : for each module, its configured networks, then its add line when
// one can be added now.
func (u *UI) hubRows() []hubRow {
	var out []hubRow
	for _, m := range u.mods {
		for _, net := range m.Networks() {
			if slices.Contains(u.netList, net) {
				out = append(out, hubRow{net: net, mod: m})
			}
		}
		if m.CanAdd() {
			out = append(out, hubRow{mod: m})
		}
	}
	return out
}

// hubName : "IRC libera", "Telegram" — the label and the instance.
func (u *UI) hubName(net string) string {
	name := net
	if m := u.modOf(net); m != nil {
		name = m.Label()
	}
	if _, inst, ok := strings.Cut(net, ":"); ok {
		name += " " + inst
	}
	return render.CleanLine(name)
}

// hubState : stopped, connecting (started, no account yet), or connected as
// the name of the account.
func (u *UI) hubState(net string) string {
	switch {
	case u.nets[net] == nil:
		return i18n.T("hub_stopped")
	case u.self[net].Name != "":
		return i18n.T("hub_up", render.CleanLine(u.self[net].Name)) // the name comes from the network
	default:
		return i18n.T("hub_connecting")
	}
}

func (u *UI) hubRowText(r hubRow) string {
	if r.net == "" {
		return i18n.T("hub_add", r.mod.Label())
	}
	return padTo(u.hubName(r.net), 16) + " " + u.hubState(r.net)
}

// hubActs : what can be done on net now, in this order: connect or
// disconnect, log out of the account (a backend that knows how), remove (a
// module that can). The answers go to window 0.
func (u *UI) hubActs(net string) []hubAction {
	w := u.ws.List[0]
	var acts []hubAction
	if b := u.nets[net]; b == nil {
		acts = append(acts, hubAction{i18n.T("hub_act_connect"), func() {
			u.hubReturn[net] = true
			u.hub = nil
			u.netAction(w, net, "login")
			u.hubLaunched(net)
		}})
	} else {
		acts = append(acts, hubAction{i18n.T("hub_act_disconnect"), func() {
			u.netAction(w, net, "disconnect")
			u.hub.view, u.hub.cur = hubList, 0
		}})
		if _, ok := b.(model.Logouter); ok {
			acts = append(acts, hubAction{i18n.T("hub_act_logout"), func() {
				u.netAction(w, net, "logout")
				u.hub.view, u.hub.cur = hubList, 0
			}})
		}
	}
	if _, ok := u.modOf(net).(module.Remover); ok {
		acts = append(acts, hubAction{i18n.T("hub_act_remove"), func() {
			u.hub.view, u.hub.askAt = hubConfirm, time.Now()
		}})
	}
	return acts
}

// hubLaunched : after a start asked from the hub. A start that failed at
// once (a token_cmd that fails) leaves no backend: the hub comes back now,
// its EvStopped will never come.
func (u *UI) hubLaunched(net string) {
	if u.nets[net] == nil {
		delete(u.hubReturn, net)
		u.openHub()
	}
}

// hubBack : EvReady or EvStopped of net. A network started from the hub
// brings it back, once.
func (u *UI) hubBack(net string) {
	if u.hubReturn[net] {
		delete(u.hubReturn, net)
		u.openHub()
	}
}

func (h *hubBox) actionLabels() []string {
	out := make([]string, len(h.acts))
	for i, a := range h.acts {
		out[i] = a.label
	}
	return out
}

// hubRefresh computes what the view shows from the state of now. A network
// gone (removed) sends the actions back to the list.
func (u *UI) hubRefresh() {
	h := u.hub
	if h.view != hubList && !slices.Contains(u.netList, h.net) {
		h.view, h.cur = hubList, 0
	}
	h.title, h.help = i18n.T("hub_title"), i18n.T("hub_keys")
	h.texts, h.rows, h.acts = h.texts[:0], nil, nil
	switch h.view {
	case hubList:
		h.rows = u.hubRows()
		for _, r := range h.rows {
			h.texts = append(h.texts, u.hubRowText(r))
		}
	case hubActions:
		h.title += " › " + u.hubName(h.net)
		h.acts = u.hubActs(h.net)
		h.texts, h.help = h.actionLabels(), i18n.T("hub_actions_keys")
	case hubConfirm:
		h.title += " › " + u.hubName(h.net)
		h.help = i18n.T("hub_confirm_remove", u.hubName(h.net))
	}
	h.cur = min(max(h.cur, 0), max(0, len(h.texts)-1))
}

// head : lines between the top border and the first row.
func (h *hubBox) head() int {
	if h.view == hubList && h.welcome {
		return 4
	}
	return 2
}

// Lines draws the box from what hubRefresh set; w is its width, borders
// included.
func (h *hubBox) Lines(th theme.Theme, w int) []render.Line {
	box, edge, sel := boxStyles(th)
	dim := box
	dim.FG = th.Color(theme.Dim)
	b := boxDraw{edge: edge, fill: box, inner: max(1, w-2)}
	out := make([]render.Line, 0, len(h.texts)+6)
	out = append(out, b.bar("┌", "┐"), b.text(h.title, box))
	if h.head() == 4 {
		out = append(out, b.text(i18n.T("hub_welcome1"), box), b.text(i18n.T("hub_welcome2"), dim))
	}
	for i, s := range h.texts {
		st := box
		if i == h.cur {
			st = sel
		}
		out = append(out, b.text(s, st))
	}
	return append(out, b.text(h.help, dim), b.bar("└", "┘"))
}

// hubRect : box of the hub, centred.
// ponytail: no scroll — a list taller than the screen is cut; add one (top,
// rows of listOverlay) the day someone has that many networks.
func (u *UI) hubRect() rect {
	w := max(30, min(u.t.Cols-4, 56))
	h := min(u.t.Rows, u.hub.head()+len(u.hub.texts)+2)
	return centerRect(u.t.Cols, u.t.Rows, w, h)
}

// hubEnter : on the list, an add line opens the page of its module (a form
// the hub shows in its place), a network its actions; on the actions, the
// current one runs.
func (u *UI) hubEnter() {
	h := u.hub
	if h.cur >= len(h.texts) {
		return
	}
	switch h.view {
	case hubList:
		r := h.rows[h.cur]
		if r.net == "" {
			r.mod.OpenSetup(host{u})
			return
		}
		h.view, h.net, h.cur, h.acts = hubActions, r.net, 0, u.hubActs(r.net)
	case hubActions:
		h.acts[h.cur].do()
	}
}

// hubEsc : the actions go back to the list, the list closes the hub.
func (u *UI) hubEsc() {
	if u.hub.view == hubList {
		u.closeHub()
		return
	}
	u.hub.view, u.hub.cur = hubList, 0
}

// hubListOverlay : the list and the actions seen as a list overlay; a click
// on a row does what Enter does.
func (u *UI) hubListOverlay() listOverlay {
	h := u.hub
	return listOverlay{r: u.hubRect(), head: h.head(), rows: len(h.texts), n: len(h.texts),
		move:  func(d int) { h.cur = min(max(h.cur+d, 0), max(0, len(h.texts)-1)) },
		click: func(i int) { h.cur = i; u.hubEnter() },
		enter: u.hubEnter, close: u.hubEsc}
}

// hubKey : the question of a removal takes one key — y 300 ms or more after
// it removes, any other goes back to the actions; otherwise the list keys.
func (u *UI) hubKey(k term.Key) {
	u.hubRefresh()
	h := u.hub
	if h.view != hubConfirm {
		u.hubListOverlay().key(k)
		return
	}
	if !answeredYes(k, h.askAt) {
		h.view = hubActions
		return
	}
	if r, ok := u.modOf(h.net).(module.Remover); ok {
		r.Remove(host{u}, u.win(u.view()), h.net)
	}
	h.view, h.cur = hubList, 0
}

// hubMouse : the list and the actions take the mouse; the question ignores it.
func (u *UI) hubMouse(e term.MouseEvent) {
	u.hubRefresh()
	if u.hub.view != hubConfirm {
		u.hubListOverlay().mouse(e)
	}
}
