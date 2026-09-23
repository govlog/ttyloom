package irc

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
)

// The IRC module: its [[irc]] tables, the launch of each network.

// Prefix : the IRC networks — there can be several, each one keyed
// "irc:<name>" (Net). The prefix alone is never a network.
const Prefix = "irc"

// Net gives the network key of the IRC network name.
func Net(name string) string { return Prefix + ":" + name }

// Name gives the name of an IRC network key, "" for any other network.
func Name(net string) string {
	if len(net) > len(Prefix)+1 && net[:len(Prefix)+1] == Prefix+":" {
		return net[len(Prefix)+1:]
	}
	return ""
}

// IsChannel : name starts with one of the four channel prefixes of RFC 2811.
func IsChannel(name string) bool { return name != "" && strings.ContainsRune("#&!+", rune(name[0])) }

// NetConfig : one IRC network ([[irc]] table). Name is the key of the
// network (irc:<name>); Channels is kept up to date by the backend (JOIN and
// PART) and joined again at the next connection — the client's own memory,
// there being no bouncer.
type NetConfig struct {
	Name             string `toml:"name"`
	Host             string `toml:"host"`
	Port             int    `toml:"port"`
	TLS              bool   `toml:"tls"`
	Nick             string `toml:"nick"`
	User             string `toml:"user"`
	RealName         string `toml:"realname"`
	NickServPassword string `toml:"nickserv_password"`
	// NickServPasswordCmd : command printing the password, like the Discord
	// token_cmd — the secret then never lands in config.toml. It wins over
	// nickserv_password.
	NickServPasswordCmd string `toml:"nickserv_password_cmd"`
	// PasswordWithoutTLS sends the password on a connection without TLS,
	// in clear; off (the default), it is withheld and a warning says so.
	PasswordWithoutTLS bool     `toml:"password_without_tls"`
	Channels           []string `toml:"channels"`
	Ignores            []string `toml:"ignores"`   // nick!user@host masks whose lines are dropped (/ignore)
	DCCIP              string   `toml:"dcc_ip"`    // address announced by DCC SEND; empty = the one of the IRC socket
	DCCPorts           string   `toml:"dcc_ports"` // "5000-5010"; empty = any free port
}

// Password gives the NickServ password: the output of nickserv_password_cmd
// when there is one, the plain nickserv_password otherwise. Read at each
// launch of the network, never kept.
func (n *NetConfig) Password() (string, error) {
	if strings.TrimSpace(n.NickServPasswordCmd) == "" {
		return n.NickServPassword, nil
	}
	return config.SecretCmd("irc:"+n.Name+": nickserv_password_cmd", n.NickServPasswordCmd)
}

// ValidName : the key of an IRC network — 1 to 32 characters of
// [a-z0-9_-]. Lower case only: it is a section key and a command argument.
func ValidName(name string) bool {
	if name == "" || len(name) > 32 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// Module : the IRC networks, one [[irc]] table each.
type Module struct{ nets []*NetConfig }

func NewModule() *Module { return &Module{} }

func (m *Module) Name() string { return Prefix }

// Load keeps the [[irc]] tables with a valid name, the first of each name;
// the others are unknown keys ("irc.<name>"), as before.
func (m *Module) Load(src module.ConfigSource) error {
	var list []*NetConfig
	if _, err := src.Decode("irc", &list); err != nil {
		return err
	}
	seen := map[string]bool{}
	m.nets = nil
	for _, n := range list {
		if !ValidName(n.Name) || seen[n.Name] {
			src.Unknown("irc." + n.Name)
			continue
		}
		seen[n.Name] = true
		m.nets = append(m.nets, n)
	}
	return nil
}

// Save writes the tables; none left, no key at all.
func (m *Module) Save(dst module.ConfigSink) {
	if len(m.nets) > 0 {
		dst.Set("irc", m.nets)
	}
}

func (m *Module) Networks() []string {
	out := make([]string, len(m.nets))
	for i, n := range m.nets {
		out[i] = Net(n.Name)
	}
	return out
}

// Cache : one directory per network; IRC keeps no history on the server,
// the files are the only copy.
func (m *Module) Cache(net string) (string, bool) { return net, true }

func (m *Module) ByName(name string) *NetConfig {
	for _, n := range m.nets {
		if n.Name == name {
			return n
		}
	}
	return nil
}

func (m *Module) Add(n *NetConfig) { m.nets = append(m.nets, n) }

// Remove takes the table of name out; restore puts the list back as it was
// (the write of config.toml failed).
func (m *Module) Remove(name string) (restore func()) {
	was := m.nets
	m.nets = slices.DeleteFunc(slices.Clone(was), func(n *NetConfig) bool { return n.Name == name })
	return func() { m.nets = was }
}

func (m *Module) Claims(name string) bool { return IsChannel(name) }

// Launch reads the table of net now: /irc add writes one while the client
// runs. The password command runs at each launch; a failing one is the
// error of the launch.
func (m *Module) Launch(ctx context.Context, h module.Host, net string, ev chan<- model.Event) (model.Backend, error) {
	name := Name(net)
	n := m.ByName(name)
	if n == nil {
		return nil, fmt.Errorf("%s: unknown network", net)
	}
	pw, err := n.Password()
	if err != nil {
		return nil, err
	}
	// The backend saves from its own goroutines: Do brings the write to the
	// goroutine of the UI, the one owner of config.toml. The table is looked
	// up again there: it may be gone (/irc delete) by then.
	save := func(set func(*NetConfig)) {
		h.Do(func() {
			if n := m.ByName(name); n != nil {
				set(n)
				h.SaveConfig()
			}
		})
	}
	return New(Config{Name: n.Name, Host: n.Host, Port: n.Port, TLS: n.TLS, Nick: n.Nick, User: n.User,
		RealName: n.RealName, Password: pw, PasswordWithoutTLS: n.PasswordWithoutTLS,
		Channels: n.Channels, DCCIP: n.DCCIP, DCCPorts: n.DCCPorts, Ignores: n.Ignores,
		SaveChannels: func(list []string) error {
			save(func(n *NetConfig) { n.Channels = slices.Clone(list) })
			return nil
		},
		SaveIgnores: func(list []string) error {
			save(func(n *NetConfig) { n.Ignores = slices.Clone(list) })
			return nil
		}}, ev), nil
}

// Template : the IRC block of a new config.toml.
func (m *Module) Template() string {
	return `# IRC networks, as many as wanted, one [[irc]] table each — /irc add writes one:
#   [[irc]]
#   name = "libera"              # network key: irc:libera
#   host = "irc.libera.chat"
#   port = 6697
#   tls = true
#   nick = "me"
#   nickserv_password_cmd = "pass show irc/libera"  # prints the password (no shell, like token_cmd); wins over the next key
#   nickserv_password = ""       # SASL PLAIN, or NickServ IDENTIFY when the server has no SASL
#   password_without_tls = false # true sends the password in clear on a connection without TLS; withheld otherwise
#   channels = ["#go-nuts"]      # kept up to date by /join and /part, joined again at start
#   ignores = ["spammer!*@*"]    # kept up to date by /ignore: lines of those masks are dropped
`
}
