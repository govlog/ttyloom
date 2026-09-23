package irc

import (
	"os"
	"strings"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/module"
)

// dccLister : a backend that keeps DCC offers and transfers (*Client, and
// the fake of the tests).
type dccLister interface{ DCC() []string }

// dccCmd : /dcc (offers and transfers), /dcc send <nick> <path>, /dcc get
// [nick] (the last offer of the window, or of the private chat of nick).
func (m *Module) dccCmd(h module.Host, w module.Win, args []string, text string) {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	switch sub {
	case "":
		n := 0
		for _, net := range m.Networks() {
			if l, ok := h.Backend(net).(dccLister); ok {
				for _, s := range l.DCC() {
					h.Print(w, net+": "+s)
					n++
				}
			}
		}
		if n == 0 {
			h.Print(w, i18n.T("dcc_none"))
		}
	case "send":
		if len(args) < 3 {
			h.Print(w, i18n.T("usage_dcc"))
			return
		}
		nick := args[1]
		path := config.Expand(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(text, args[0])), nick)))
		if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
			h.Print(w, i18n.T("send_not_a_file", path))
			return
		}
		net := h.ContextNet(w, Prefix)
		if net == "" || h.Backend(net) == nil {
			h.Print(w, i18n.T("dcc_which_net"))
			return
		}
		if c := h.ChatByTitle(net, nick); c != nil {
			h.SendFile(c, path)
			return
		}
		// The private chat does not exist yet: the network makes it, and the
		// send goes at the answer.
		h.Resolve(w, nick, net, path)
	case "get":
		win := w
		if len(args) > 1 {
			c := h.ChatByTitle(h.ContextNet(w, Prefix), args[1])
			if c == nil {
				h.Print(w, i18n.T("dcc_no_offer", args[1]))
				return
			}
			win = module.Win{Chat: c} // the window of that chat, when it has one
		}
		msg := h.LastIncomingFile(win)
		if msg == nil {
			h.Print(w, i18n.T("dcc_no_offer", strings.Join(args[1:], " ")))
			return
		}
		h.Download(msg)
	default:
		h.Print(w, i18n.T("usage_dcc"))
	}
}
