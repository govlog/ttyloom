package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/govlog/ttyloom/internal/cache"
	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// /irc and /dcc — the IRC networks (as many as configured, added live by
// the form of /irc add) and the DCC transfers.

// ircNets : the IRC networks configured, in name order.
func (u *UI) ircNets() []string {
	var out []string
	for _, n := range u.netList {
		if model.IRCName(n) != "" {
			out = append(out, n)
		}
	}
	return out
}

// ircCmd : /irc (status of each network), /irc add (the form), /irc connect
// <name>, /irc disconnect <name>.
func (u *UI) ircCmd(w *Window, args []string) {
	sub, name := "", ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	if len(args) > 1 {
		name = strings.ToLower(args[1])
	}
	switch sub {
	case "", "status":
		nets := u.ircNets()
		if len(nets) == 0 {
			w.AddSys(i18n.T("irc_none"))
			return
		}
		for _, n := range nets {
			u.netStatus(w, n)
		}
	case "add":
		u.openIRCForm()
	case "connect", "disconnect":
		net := model.IRCNet(name)
		if name == "" || !slices.Contains(u.netList, net) {
			w.AddSys(i18n.T("irc_unknown", name, strings.Join(u.ircNets(), ", ")))
			return
		}
		if sub == "connect" {
			u.startNet(net)
		} else {
			u.stopNet(net, false)
		}
	default:
		w.AddSys(i18n.T("usage_irc"))
	}
}

// ircPreset : one well-known network, offered by the hostname field of the
// form. Round-robin hosts and the TLS port of each, checked in September
// 2026 against the networks' own pages (Libera, OFTC, EFnet, hackint) and
// the ircbits/irchelp overviews for the others.
type ircPreset struct {
	name, label, host string
	port              int
}

var ircPresets = []ircPreset{
	{"libera", "Libera.Chat", "irc.libera.chat", 6697},
	{"oftc", "OFTC", "irc.oftc.net", 6697},
	{"efnet", "EFnet", "irc.efnet.org", 6697},
	{"dalnet", "DALnet", "irc.dal.net", 6697},
	{"undernet", "Undernet", "irc.undernet.org", 6697},
	{"ircnet", "IRCnet", "open.ircnet.net", 6697},
	{"quakenet", "QuakeNet", "irc.quakenet.org", 6697},
	{"rizon", "Rizon", "irc.rizon.net", 6697},
	{"hackint", "hackint", "irc.hackint.org", 6697},
	{"gamesurge", "GameSurge", "irc.gamesurge.net", 6697},
	{"espernet", "EsperNet", "irc.esper.net", 6697},
	{"snoonet", "Snoonet", "irc.snoonet.org", 6697},
	{"tilde", "tilde.chat", "irc.tilde.chat", 6697},
}

// Field indexes of the /irc add form.
const (
	fName = iota
	fHost
	fPort
	fTLS
	fNick
	fUser
	fReal
	fPass
)

// openIRCForm : the fields of a new network; the answer goes to ircAddSubmit.
// The hostname field cycles through ircPresets (← → or Space); a preset
// fills host, port, TLS, and the name when it is still empty.
func (u *UI) openIRCForm() {
	f := &formBox{title: i18n.T("irc_add_title"), submit: u.ircAddSubmit}
	for _, l := range []struct {
		key, def string
		secret   bool
	}{{"irc_f_name", "", false}, {"irc_f_host", "", false}, {"irc_f_port", "6697", false}, {"irc_f_tls", "yes", false},
		{"irc_f_nick", "", false}, {"irc_f_user", "", false}, {"irc_f_real", "", false}, {"irc_f_pass", "", true}} {
		f.fields = append(f.fields, formField{label: i18n.T(l.key), val: []rune(l.def), secret: l.secret, sel: -1})
	}
	for _, p := range ircPresets {
		f.fields[fHost].choices = append(f.fields[fHost].choices, p.host+"  "+p.label)
	}
	f.pick = func(field, i int) {
		if field != fHost {
			return
		}
		p := ircPresets[i]
		f.fields[fHost].val = []rune(p.host)
		f.fields[fPort].val = []rune(strconv.Itoa(p.port))
		f.fields[fTLS].val = []rune("yes")
		if len(f.fields[fName].val) == 0 || f.fields[fName].sel >= 0 {
			f.fields[fName].val, f.fields[fName].sel = []rune(p.name), 0 // marked as filled by a preset: the next preset replaces it
		}
	}
	u.form = f
}

// yes : the TLS field — yes/no in either language, empty means yes.
func yes(v string) bool {
	switch strings.ToLower(v) {
	case "", "y", "yes", "o", "oui", "on", "true", "1":
		return true
	}
	return false
}

// ircAddSubmit checks the form, writes the [[irc]] table, and starts the
// network. The error text keeps the form open.
func (u *UI) ircAddSubmit(v []string) string {
	name := strings.ToLower(v[0])
	switch {
	case !config.ValidIRCName(name):
		return i18n.T("irc_bad_name")
	case u.cfg.IRCByName(name) != nil:
		return i18n.T("irc_name_taken", name)
	case v[1] == "" || strings.ContainsAny(v[1], " /"):
		return i18n.T("irc_bad_host")
	case v[4] == "" || strings.ContainsAny(v[4], " ,"):
		return i18n.T("irc_bad_nick")
	}
	port := 6697
	if v[2] != "" {
		p, err := strconv.Atoi(v[2])
		if err != nil || p < 1 || p > 65535 {
			return i18n.T("irc_bad_port")
		}
		port = p
	}
	n := &config.IRCConfig{Name: name, Host: v[1], Port: port, TLS: yes(v[3]), Nick: v[4], User: v[5], RealName: v[6], NickServPassword: v[7]}
	u.cfg.IRC = append(u.cfg.IRC, n)
	if !u.saveCfg() {
		u.cfg.IRC = u.cfg.IRC[:len(u.cfg.IRC)-1]
		return i18n.T("irc_not_saved")
	}
	net := model.IRCNet(name)
	u.netList = append(u.netList, net)
	slices.Sort(u.netList)
	if u.cfg.Cache && u.caches != nil && u.caches[net] == nil {
		u.caches[net] = cache.New(filepath.Join(config.CacheDir(), net), u.cfg.CacheMessages)
	}
	u.sys(i18n.T("irc_added", net))
	u.startNet(net)
	return ""
}

// ircNetFor : the IRC network a command from w means — the one of its chat
// or send target, else the only one configured, else "".
func (u *UI) ircNetFor(w *Window) string {
	for _, c := range []*model.Chat{w.Chat, w.Target} {
		if c != nil && model.IRCName(c.Net) != "" {
			return c.Net
		}
	}
	if nets := u.ircNets(); len(nets) == 1 {
		return nets[0]
	}
	return ""
}

// resolversFor : the backends a lookup of name from w goes to. The network
// of the window when it resolves (two IRC networks would both join the
// room), else the IRC networks for a "#room", else every resolving one.
func (u *UI) resolversFor(w *Window, name string) []model.Backend {
	if w != nil {
		for _, c := range []*model.Chat{w.Chat, w.Target} {
			if b := u.net(c); b != nil && b.Caps().Resolve {
				return []model.Backend{b}
			}
		}
	}
	var out, irc []model.Backend
	for _, n := range u.netNames() {
		b := u.nets[n]
		if !b.Caps().Resolve {
			continue
		}
		out = append(out, b)
		if model.IRCName(n) != "" {
			irc = append(irc, b)
		}
	}
	if strings.HasPrefix(name, "#") && len(irc) > 0 {
		return irc
	}
	return out
}

// dccLister : a backend that keeps DCC offers and transfers (protocols/irc).
type dccLister interface{ DCC() []string }

// dccCmd : /dcc (offers and transfers), /dcc send <nick> <path>, /dcc get
// [nick] (the last offer of the window, or of the private chat of nick).
func (u *UI) dccCmd(w *Window, args []string, text string) {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	switch sub {
	case "":
		n := 0
		for _, net := range u.ircNets() {
			if l, ok := u.nets[net].(dccLister); ok {
				for _, s := range l.DCC() {
					w.AddSys(net + ": " + s)
					n++
				}
			}
		}
		if n == 0 {
			w.AddSys(i18n.T("dcc_none"))
		}
	case "send":
		if len(args) < 3 {
			w.AddSys(i18n.T("usage_dcc"))
			return
		}
		nick := args[1]
		path := config.Expand(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(text, args[0])), nick)))
		if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
			w.AddSys(i18n.T("send_not_a_file", path))
			return
		}
		net := u.ircNetFor(w)
		if net == "" || u.nets[net] == nil {
			w.AddSys(i18n.T("dcc_which_net"))
			return
		}
		if c := u.chatByTitle(net, nick); c != nil {
			u.sendFile(c, path, "")
			return
		}
		// The private chat does not exist yet: the network makes it (Resolve),
		// and the send goes at the answer (chatResolved).
		if u.dccAfter == nil {
			u.dccAfter = map[string]string{}
		}
		u.dccAfter[nick] = path
		u.nets[net].Resolve(u.ctx, nick, false)
	case "get":
		win := w
		if len(args) > 1 {
			net := u.ircNetFor(w)
			c := u.chatByTitle(net, args[1])
			if c == nil {
				w.AddSys(i18n.T("dcc_no_offer", args[1]))
				return
			}
			if i := u.ws.ForChat(c.Key()); i >= 0 {
				win = u.ws.List[i]
			} else {
				win = nil
			}
		}
		m := lastOffer(win)
		if m == nil {
			w.AddSys(i18n.T("dcc_no_offer", strings.Join(args[1:], " ")))
			return
		}
		u.download(m)
	default:
		w.AddSys(i18n.T("usage_dcc"))
	}
}

// chatByTitle : the chat of net whose title is name, case apart.
func (u *UI) chatByTitle(net, name string) *model.Chat {
	for _, c := range u.chatList {
		if c.Net == net && strings.EqualFold(c.Title, name) {
			return c
		}
	}
	return nil
}

// lastOffer : the last incoming file of w not fetched yet.
func lastOffer(w *Window) *model.Msg {
	if w == nil {
		return nil
	}
	for i := len(w.Items) - 1; i >= 0; i-- {
		m := w.Items[i].Msg
		if m != nil && !m.Out && m.Media != nil && m.Media.Kind == model.MediaFile &&
			m.Media.State != model.MediaReady && m.Media.State != model.MediaLoading {
			return m
		}
	}
	return nil
}

// dccResolved : the answer of the lookup /dcc send started; true when it was
// one. The file goes out as soon as the chat exists.
func (u *UI) dccResolved(e model.EvChat) bool {
	path, ok := u.dccAfter[e.Query]
	if !ok {
		return false
	}
	delete(u.dccAfter, e.Query)
	if e.Err != "" || e.Chat == nil {
		u.sys(i18n.T("resolve_failed", e.Query, e.Err))
		return true
	}
	c := u.remember(e.Chat)
	u.listChat(c)
	u.sendFile(c, path, "")
	return true
}
