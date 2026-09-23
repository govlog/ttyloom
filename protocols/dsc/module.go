package dsc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
)

// The Discord module: the [discord] section, the token, the launch.

// Net : the network key of Discord.
const Net = model.NetDiscord

// Settings : the [discord] section.
type Settings struct {
	// TokenCmd : command printing the token; never the token itself. Empty:
	// the token file of the configuration directory, written by the QR login.
	TokenCmd string `toml:"token_cmd"`
}

// Token gives the Discord token: the trimmed stdout of token_cmd when there
// is one, the token file otherwise — "" with no error when that file does not
// exist yet, and the backend then logs in by QR and writes it. The token
// never lands in config.toml, the log or an event.
func (d *Settings) Token(file string) (string, error) {
	if strings.TrimSpace(d.TokenCmd) == "" {
		b, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("discord: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return config.SecretCmd("discord: token_cmd", d.TokenCmd)
}

// Module : the Discord account, section [discord].
type Module struct {
	set      *Settings // nil: no [discord], no network
	first    bool      // the token read at Load is not spent yet
	firstTok string
	firstErr error
}

func NewModule() *Module { return &Module{} }

func (m *Module) Name() string { return Net }

// Load reads [discord] and, when it is there, the token: now, before the
// terminal goes raw, a token command that prompts on the tty (pinentry-curses)
// works as it always did. The launches that follow read it again from inside
// the raw terminal — such a command needs a graphical pinentry or an unlocked
// agent by then.
func (m *Module) Load(src module.ConfigSource) error {
	var s Settings
	ok, err := src.Decode("discord", &s)
	if err != nil || !ok {
		m.set = nil
		return err
	}
	m.set, m.first = &s, true
	m.firstTok, m.firstErr = s.Token(tokenPath())
	return nil
}

func (m *Module) Save(dst module.ConfigSink) {
	if m.set != nil {
		dst.Set("discord", m.set)
	}
}

// Template : the Discord block of a new config.toml.
func (m *Module) Template() string {
	return `# Discord is a second, optional network. Its token is never written here: the
# command below prints it (a password manager), and it runs with no shell —
# split on blanks, so a path with a space in it needs a wrapper script.
# Third-party clients on a user account are against the Discord terms of
# service: a secondary account is the safe way to try it.
#   [discord]
#   token_cmd = "pass show discord/token"   # or nothing: /discord login shows a QR code
`
}

func (m *Module) Networks() []string {
	if m.set == nil {
		return nil
	}
	return []string{Net}
}

func (m *Module) Cache(net string) (string, bool) { return net, false }
func (m *Module) Claims(string) bool              { return false }

// tokenPath : the token file of the QR login (mode 0600, next to the
// Telegram session), read when [discord] has no token_cmd.
func tokenPath() string { return filepath.Join(config.Dir(), "discord.token") }

// Launch : the first launch takes the token read at Load, the next ones run
// the command (or read the file) again — a token renewed in the password
// manager is taken without a restart. With no command the backend logs in by
// QR and writes the file when it is missing.
func (m *Module) Launch(_ context.Context, _ module.Host, _ string, ev chan<- model.Event) (model.Backend, error) {
	if m.set == nil {
		return nil, fmt.Errorf("%s: not configured", Net)
	}
	tok, err := m.firstTok, m.firstErr
	if !m.first {
		tok, err = m.set.Token(tokenPath())
	}
	m.first = false
	if err != nil {
		return nil, err
	}
	file := ""
	if m.set.TokenCmd == "" {
		file = tokenPath()
	}
	return New(Config{Token: tok, TokenFile: file}, ev), nil
}

func (m *Module) Commands() []module.Command {
	return []module.Command{{Name: Net, Help: module.Topic{Key: "help_discord", Section: "chats"},
		Complete: func(_ module.Host, _ module.Win, rest string) ([]string, string) {
			return []string{"status", "login", "logout", "disconnect"}, rest
		},
		Run: func(h module.Host, w module.Win, args []string, _ string) {
			sub := ""
			if len(args) > 0 {
				sub = strings.ToLower(args[0])
			}
			h.NetAction(w, Net, sub)
		}}}
}
