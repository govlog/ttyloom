package irc

import (
	"strconv"
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/module"
)

// The form of /irc add.

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

// openForm : the fields of a new network; the answer goes to submit. The
// hostname field cycles through ircPresets (← → or Space); a preset fills
// host, port, TLS, and the name when it is still empty or came from a preset.
func (m *Module) openForm(h module.Host) {
	f := &module.Form{Title: i18n.T("irc_add_title")}
	for _, l := range []struct {
		key, def string
		secret   bool
	}{{"irc_f_name", "", false}, {"irc_f_host", "", false}, {"irc_f_port", "6697", false}, {"irc_f_tls", "yes", false},
		{"irc_f_nick", "", false}, {"irc_f_user", "", false}, {"irc_f_real", "", false}, {"irc_f_pass", "", true}} {
		f.Fields = append(f.Fields, module.Field{Label: i18n.T(l.key), Value: l.def, Secret: l.secret, Sel: -1})
	}
	for _, p := range ircPresets {
		f.Fields[fHost].Choices = append(f.Fields[fHost].Choices, p.host+"  "+p.label)
	}
	f.Pick = func(f *module.Form, field, i int) {
		if field != fHost {
			return
		}
		p := ircPresets[i]
		f.Fields[fHost].Value = p.host
		f.Fields[fPort].Value = strconv.Itoa(p.port)
		f.Fields[fTLS].Value = "yes"
		if f.Fields[fName].Value == "" || f.Fields[fName].Sel >= 0 {
			f.Fields[fName].Value, f.Fields[fName].Sel = p.name, 0 // marked as filled by a preset: the next preset replaces it
		}
	}
	f.Submit = func(v []string) string { return m.submit(h, v) }
	h.OpenForm(f)
}

// yes : the TLS field — yes/no in either language, empty means yes.
func yes(v string) bool {
	switch strings.ToLower(v) {
	case "", "y", "yes", "o", "oui", "on", "true", "1":
		return true
	}
	return false
}

// submit checks the form, writes the [[irc]] table, and starts the network.
// The error text keeps the form open.
func (m *Module) submit(h module.Host, v []string) string {
	name := strings.ToLower(v[0])
	switch {
	case !ValidName(name):
		return i18n.T("irc_bad_name")
	case m.ByName(name) != nil:
		return i18n.T("irc_name_taken", name)
	case v[1] == "" || strings.ContainsAny(v[1], " /"):
		return i18n.T("irc_bad_host")
	case v[4] == "" || strings.ContainsAny(v[4], " ,"):
		return i18n.T("irc_bad_nick")
	case !yes(v[3]) && v[7] != "": // the password would go in clear
		return i18n.T("irc_tls_password")
	}
	port := 6697
	if v[2] != "" {
		p, err := strconv.Atoi(v[2])
		if err != nil || p < 1 || p > 65535 {
			return i18n.T("irc_bad_port")
		}
		port = p
	}
	m.Add(&NetConfig{Name: name, Host: v[1], Port: port, TLS: yes(v[3]), Nick: v[4], User: v[5], RealName: v[6], NickServPassword: v[7]})
	if !h.SaveConfig() {
		m.Remove(name)
		return i18n.T("irc_not_saved")
	}
	net := Net(name)
	h.Print(module.Win{}, i18n.T("irc_added", net))
	h.AddNetwork(net)
	return ""
}

// delete : /irc delete <name>. The [[irc]] table leaves config.toml first
// (nothing changes when the write fails), then the network goes.
func (m *Module) delete(h module.Host, w module.Win, net string) {
	restore := m.Remove(Name(net))
	if !h.SaveConfig() {
		restore()
		return
	}
	h.RemoveNetwork(net)
	h.Print(w, i18n.T("irc_deleted", net))
}
