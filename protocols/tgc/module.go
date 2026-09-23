package tgc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
)

// The Telegram module: the api_id of the application, the account or bot,
// the session file, the launch.

// Net : the network key of Telegram.
const Net = model.NetTelegram

// Settings : the [telegram] section, or the historic flat keys of the same
// names at the top of config.toml.
type Settings struct {
	APIID    int    `toml:"api_id"`
	APIHash  string `toml:"api_hash"`
	BotToken string `toml:"bot_token"`
}

// Module : the Telegram account — the flat keys api_id, api_hash, bot_token
// of config.toml, or the [telegram] section, which wins; then the TG_*
// variables, which win over both and never go back into the file (a dotfiles
// repository, a backup…).
type Module struct {
	eff     Settings  // effective values: file, section, environment
	file    Settings  // the flat keys as read from the file
	section *Settings // the [telegram] section of the file, nil when none
}

func NewModule() *Module { return &Module{} }

func (m *Module) Name() string { return Net }

func (m *Module) Load(src module.ConfigSource) error {
	m.file, m.section = Settings{}, nil
	for _, k := range []struct {
		key string
		v   any
	}{{"api_id", &m.file.APIID}, {"api_hash", &m.file.APIHash}, {"bot_token", &m.file.BotToken}} {
		if _, err := src.Decode(k.key, k.v); err != nil {
			return err
		}
	}
	var s Settings
	ok, err := src.Decode("telegram", &s)
	if err != nil {
		return err
	}
	m.eff = m.file
	if ok { // the section wins over the flat keys
		m.section, m.eff = &s, s
	}
	if v := os.Getenv("TG_API_ID"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf(i18n.T("error_with_prefix"), "TG_API_ID", err)
		}
		m.eff.APIID = n
	}
	if v := os.Getenv("TG_API_HASH"); v != "" {
		m.eff.APIHash = v
	}
	if v := os.Getenv("TG_BOT_TOKEN"); v != "" {
		m.eff.BotToken = v
	}
	if m.configured() && (m.eff.APIID <= 0 || m.eff.APIHash == "") {
		return fmt.Errorf(i18n.T("main_no_api_id"), filepath.Join(config.Dir(), "config.toml"))
	}
	return nil
}

// Save writes the three flat keys as the file had them (0 and "" when it had
// none, as before), and the section only when the file had one.
func (m *Module) Save(dst module.ConfigSink) {
	dst.Set("api_id", m.file.APIID)
	dst.Set("api_hash", m.file.APIHash)
	dst.Set("bot_token", m.file.BotToken)
	if m.section != nil {
		dst.Set("telegram", m.section)
	}
}

// Template : the Telegram block of a new config.toml — top-level keys.
func (m *Module) Template() string {
	return `# api_id / api_hash / bot_token below are the telegram network. They can also
# be written as a section, which then wins:
#   [telegram]
#   api_id = 0
#   api_hash = ""
#   bot_token = ""
api_id = 0            # https://my.telegram.org
api_hash = ""
bot_token = ""        # empty = user account; otherwise a BotFather token (bot mode)
`
}

// Settings : the effective values (environment included).
func (m *Module) Settings() Settings { return m.eff }

func (m *Module) configured() bool {
	return m.eff.APIID != 0 || m.eff.APIHash != "" || m.eff.BotToken != ""
}

func (m *Module) Networks() []string {
	if !m.configured() {
		return nil
	}
	return []string{Net}
}

// Cache : a bot and an account do not share their cache. The files of the
// versions before the split by network (orphans at the root of the cache)
// are dropped here, best effort: everything comes back under the network at
// the first write.
// ponytail: "history" here is the pre-split legacy directory name, not a
// network id — a future network literally named "history" would get its
// cache directory wiped on every start. Rename this cleanup (or check the
// network name) if that day comes; unlikely enough not to guard now.
func (m *Module) Cache(net string) (string, bool) {
	root, sub := config.CacheDir(), net
	if m.eff.BotToken != "" {
		root, sub = filepath.Join(root, "bot"), filepath.Join(net, "bot") // … and neither did they before the split
	}
	_ = os.Remove(filepath.Join(root, "dialogs.gob"))
	_ = os.RemoveAll(filepath.Join(root, "history"))
	return sub, false
}

// SessionPath : one session per identity; a bot and a user account never
// share the same file.
func (m *Module) SessionPath() string {
	if m.eff.BotToken != "" {
		return filepath.Join(config.Dir(), "session-bot.json")
	}
	return filepath.Join(config.Dir(), "session.json")
}

func (m *Module) Launch(_ context.Context, _ module.Host, _ string, ev chan<- model.Event) (model.Backend, error) {
	return New(Config{AppID: m.eff.APIID, AppHash: m.eff.APIHash, BotToken: m.eff.BotToken, SessionPath: m.SessionPath()}, ev), nil
}

func (m *Module) Claims(string) bool { return false }

func (m *Module) Commands() []module.Command {
	return []module.Command{{Name: Net, Help: module.Topic{Key: "help_telegram", Section: "chats"},
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
