// Package config holds ~/.config/ttyloom/config.toml.
package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"

	"github.com/BurntSushi/toml"
)

// HoverMode : level of mouse hover — menu (alias of highlight, default),
// highlight (background only, help line on selection) or off (nothing on
// hover). MarshalText/UnmarshalText also cover the TOML compatibility: an old
// boolean field (true/false) is taken at load time and read back as menu/off.
type HoverMode string

const (
	HoverMenu      HoverMode = "menu"
	HoverHighlight HoverMode = "highlight"
	HoverOff       HoverMode = "off"
)

func (m HoverMode) MarshalText() ([]byte, error) { return []byte(m), nil }

func (m *HoverMode) UnmarshalText(b []byte) error {
	switch s := string(b); s {
	case "false":
		*m = HoverOff
	case "highlight", "off":
		*m = HoverMode(s)
	default: // "true" or any unknown value: the old behaviour
		*m = HoverMenu
	}
	return nil
}

// TelegramConfig, DiscordConfig : one section per network. The historic flat
// keys (api_id, api_hash, bot_token at the top level) keep working and mean
// [telegram]; the section wins when both are there.
type TelegramConfig struct {
	APIID    int    `toml:"api_id"`
	APIHash  string `toml:"api_hash"`
	BotToken string `toml:"bot_token"`
}

type DiscordConfig struct {
	// TokenCmd : command printing the token; never the token itself. Empty:
	// the token file of the configuration directory, written by the QR login.
	TokenCmd string `toml:"token_cmd"`
}

// IRCConfig : one IRC network ([[irc]] table). Name is the key of the
// network (irc:<name>); Channels is kept up to date by the backend (JOIN and
// PART) and joined again at the next connection — the client's own memory,
// there being no bouncer.
type IRCConfig struct {
	Name             string   `toml:"name"`
	Host             string   `toml:"host"`
	Port             int      `toml:"port"`
	TLS              bool     `toml:"tls"`
	Nick             string   `toml:"nick"`
	User             string   `toml:"user"`
	RealName         string   `toml:"realname"`
	NickServPassword string   `toml:"nickserv_password"`
	Channels         []string `toml:"channels"`
	DCCIP            string   `toml:"dcc_ip"`    // address announced by DCC SEND; empty = the one of the IRC socket
	DCCPorts         string   `toml:"dcc_ports"` // "5000-5010"; empty = any free port
}

// IRCByName gives the [[irc]] table of name, nil when there is none.
func (c *Config) IRCByName(name string) *IRCConfig {
	for _, n := range c.IRC {
		if n.Name == name {
			return n
		}
	}
	return nil
}

// Token gives the Discord token: the trimmed stdout of token_cmd when there
// is one, the token file otherwise — "" with no error when that file does not
// exist yet, and the backend then logs in by QR and writes it. The token
// never lands in config.toml, the log or an event.
//
// The timeout of the command is the one of a password prompt that nobody
// answers: a pinentry waiting on a locked keyring would otherwise hold the
// start of the whole client with an empty screen.
func (d *DiscordConfig) Token(file string) (string, error) {
	f := strings.Fields(d.TokenCmd)
	if len(f) == 0 {
		b, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("discord: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, f[0], f[1:]...).Output()
	if err != nil {
		// The first line the command wrote on stderr names the cause ("cat:
		// …: No such file"); "exit status 1" alone says nothing.
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if line, _, _ := strings.Cut(strings.TrimSpace(string(ee.Stderr)), "\n"); line != "" {
				return "", fmt.Errorf("discord: token_cmd: %w: %s", err, line)
			}
		}
		return "", fmt.Errorf("discord: token_cmd: %w", err)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" { // a command that succeeds and prints nothing is not a token
		return "", errors.New("discord: token_cmd printed nothing")
	}
	return tok, nil
}

type Config struct {
	APIID             int       `toml:"api_id"`
	APIHash           string    `toml:"api_hash"`
	BotToken          string    `toml:"bot_token"`
	Theme             string    `toml:"theme"`
	DownloadDir       string    `toml:"download_dir"`
	AutoMediaMaxKB    int       `toml:"auto_media_max_kb"`
	Images            string    `toml:"images"`
	ImagesHover       bool      `toml:"images_hover"`
	Avatars           bool      `toml:"avatars"`
	KittyImages       int       `toml:"kitty_images"`
	VideoFrames       int       `toml:"video_inline_frames"`
	Video             string    `toml:"video"`
	Timestamps        bool      `toml:"timestamps"`
	TimestampsSeconds bool      `toml:"timestamps_seconds"` // 15:04:05 instead of 15:04 before each message
	CycleMode         string    `toml:"cycle_mode"`         // Ctrl+X: next | last_unread
	Multiline         bool      `toml:"multiline"`
	LinkPreviews      bool      `toml:"link_previews"`
	Maps              bool      `toml:"maps"`
	Hover             HoverMode `toml:"hover"`
	Bell              bool      `toml:"bell"`
	Notify            string    `toml:"notify"`
	AutoOpenDays      int       `toml:"auto_open_days"`
	Aggregate         bool      `toml:"aggregate"`
	Cache             bool      `toml:"cache"`
	CacheMessages     int       `toml:"cache_messages"`
	LogDir            string    `toml:"log_dir"`
	Log               bool      `toml:"log"`
	Separator         bool      `toml:"separator"`
	Redline           bool      `toml:"redline"`
	SidebarSort       string    `toml:"sidebar_sort"`
	SidebarSplit      bool      `toml:"sidebar_split"`
	Spell             string    `toml:"spell"`        // off | a hunspell code, chained with + (fr, us, en_GB, fr+us)
	SpellQuotes       bool      `toml:"spell_quotes"` // check the > quotes and the ``` fences too
	SidebarWidth      int       `toml:"sidebar_width"`
	Lang              string    `toml:"lang"`

	// Sections, after every scalar: the TOML encoder writes the tables last.
	Telegram *TelegramConfig `toml:"telegram"`
	Discord  *DiscordConfig  `toml:"discord"`
	IRC      []*IRCConfig    `toml:"irc"`

	// Unknown : keys of the file no field takes (a typo, imagess = "off"). The
	// UI says so at start, otherwise the user believes the option active.
	Unknown []string `toml:"-"`

	dir string
	// Values read from the file, before the TG_* variables override them.
	// Save writes those back: a secret given by the environment must never
	// land in a config.toml the user deliberately left empty (a dotfiles
	// repository, a backup…). fileTelegram is the [telegram] section of the
	// file, nil when it had none: Save gives the file back its own shape.
	fileID       int
	fileHash     string
	fileToken    string
	fileTelegram *TelegramConfig
}

const defaultFile = `# ttyloom
# api_id / api_hash / bot_token below are the telegram network. They can also
# be written as a section, which then wins:
#   [telegram]
#   api_id = 0
#   api_hash = ""
#   bot_token = ""
# Discord is a second, optional network. Its token is never written here: the
# command below prints it (a password manager), and it runs with no shell —
# split on blanks, so a path with a space in it needs a wrapper script.
# Third-party clients on a user account are against the Discord terms of
# service: a secondary account is the safe way to try it.
#   [discord]
#   token_cmd = "pass show discord/token"   # or nothing: /discord login shows a QR code
# IRC networks, as many as wanted, one [[irc]] table each — /irc add writes one:
#   [[irc]]
#   name = "libera"              # network key: irc:libera
#   host = "irc.libera.chat"
#   port = 6697
#   tls = true
#   nick = "me"
#   nickserv_password = ""       # SASL PLAIN, or NickServ IDENTIFY without SASL
#   channels = ["#go-nuts"]      # kept up to date by /join and /leave, joined again at start
# Sections go at the END of the file: a plain key written after [discord]
# would be read as one of its keys.
api_id = 0            # https://my.telegram.org
api_hash = ""
bot_token = ""        # empty = user account; otherwise a BotFather token (bot mode)
theme = ""            # empty = current Ghostty theme, otherwise a theme name
download_dir = "~/Downloads/ttyloom"
auto_media_max_kb = 5120
images = "auto"       # auto | kitty | halfblock | off
images_hover = false  # image shown only under the mouse (F5), with no line kept free
avatars = true        # profile photos before the names and in the member box (kitty only)
kitty_images = 48     # images kept by the terminal (kitty); above that, the oldest ones are freed
video_inline_frames = 300 # frames decoded by "l" on a video (300 = 30 s at 10 fps)
video = "show"        # inline video: show (first frame, "l" plays) | hidden (label only) | autoplay
timestamps = true
timestamps_seconds = false # 15:04:05 instead of 15:04 before each message
cycle_mode = "next"   # Ctrl+X: next (window after the current one) | last_unread (unread windows in turn, then back)
link_previews = true   # link preview: title, description and thumbnail under the message
maps = false           # OpenStreetMap map under a position (third-party network, off by default)
hover = "menu"         # mouse hover: menu | highlight (both: background of the message under the pointer) | off
bell = true            # bell (\a) on a private message or a mention
notify = "terminal"    # notification on a private message or a mention: terminal (OSC 777) | desktop (notify-send) | off
auto_open_days = 7     # opens at start the chats active for N days (0 = off)
aggregate = false      # window 0: stream of every message received (Alt+A)
cache = true           # local cache (dialogs, history) for a fast start
cache_messages = 2000  # messages kept per chat in the disk cache; scrolling up loads the rest from the network
log_dir = "~/.local/share/ttyloom/logs"
log = false            # /log: logs every window created afterwards
separator = true       # separator line above the status bar
redline = true         # red last-read line (unread separator) in each window
sidebar_sort = "recent" # sort of the F2 sidebar (F7 cycles it): recent | alpha | unread
sidebar_width = 26      # width of the F2 sidebar; the vertical bar drags with the mouse
sidebar_split = false   # windows list in two sections, channels then direct messages (⊟ of the header)
lang = ""               # interface language: empty = $LANG, otherwise fr | en
`

func Dir() string {
	if d := os.Getenv("TTYLOOM_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = Expand("~/.config")
	}
	return filepath.Join(base, "ttyloom")
}

// CacheDir : cache directory (recent emojis, dialogs, history).
// os.UserCacheDir follows $XDG_CACHE_HOME and falls back to ~/.cache. The
// directory is made only at the first write.
// TTYLOOM_DIR names another identity (like SessionPath): its cache lives in
// that directory, never in the one of the default account.
func CacheDir() string {
	if d := os.Getenv("TTYLOOM_DIR"); d != "" {
		return filepath.Join(d, "cache")
	}
	base, err := os.UserCacheDir()
	if err != nil {
		base = Expand("~/.cache")
	}
	return filepath.Join(base, "ttyloom")
}

// RecentPath : file of the recent emojis (see picker.pick).
func RecentPath() string { return filepath.Join(CacheDir(), "emoji-recent") }

// SpellPath : personal dictionary, one word per line, next to config.toml.
func (c *Config) SpellPath() string { return filepath.Join(c.dir, "spell.txt") }

// MaxAutoMediaKB : cap of auto_media_max_kb (512 MB). A negative value cut
// every automatic download without a word, and a huge one made
// int64(n)*1024 overflow, which made it unbounded.
const MaxAutoMediaKB = 512 << 10

func Load() (*Config, error) { return LoadFrom(Dir()) }

func LoadFrom(dir string) (*Config, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "config.toml")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, []byte(defaultFile), 0o600); err != nil {
			return nil, err
		}
	}
	c := &Config{dir: dir, DownloadDir: "~/Downloads/ttyloom", AutoMediaMaxKB: 5120, Images: "auto", Avatars: true, KittyImages: 48, VideoFrames: 300, Video: "show", Timestamps: true, LinkPreviews: true, Hover: HoverMenu,
		Bell: true, Notify: "terminal", AutoOpenDays: 7, Cache: true, CacheMessages: 2000, LogDir: "~/.local/share/ttyloom/logs", Separator: true, Redline: true, SidebarSort: "recent", SidebarWidth: 26, Spell: "off", CycleMode: "next"}
	md, err := toml.DecodeFile(path, c)
	if err != nil {
		return nil, fmt.Errorf(i18n.T("error_with_prefix"), path, err)
	}
	for _, k := range md.Undecoded() {
		c.Unknown = append(c.Unknown, k.String())
	}
	// An [[irc]] table with no valid name, or the same name twice, is left
	// out: the network would have no key, or two networks would share one.
	seen := map[string]bool{}
	var nets []*IRCConfig
	for _, n := range c.IRC {
		if !ValidIRCName(n.Name) || seen[n.Name] {
			c.Unknown = append(c.Unknown, "irc."+n.Name)
			continue
		}
		seen[n.Name] = true
		nets = append(nets, n)
	}
	c.IRC = nets
	// Shape of the file, kept as it is for Save.
	c.fileID, c.fileHash, c.fileToken, c.fileTelegram = c.APIID, c.APIHash, c.BotToken, c.Telegram
	if c.Telegram != nil { // the section wins over the flat keys
		c.APIID, c.APIHash, c.BotToken = c.Telegram.APIID, c.Telegram.APIHash, c.Telegram.BotToken
	}
	c.AutoMediaMaxKB = min(max(c.AutoMediaMaxKB, 0), MaxAutoMediaKB)
	if v := os.Getenv("TG_API_ID"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf(i18n.T("error_with_prefix"), "TG_API_ID", err)
		}
		c.APIID = n
	}
	if v := os.Getenv("TG_API_HASH"); v != "" {
		c.APIHash = v
	}
	if v := os.Getenv("TG_BOT_TOKEN"); v != "" {
		c.BotToken = v
	}
	// Effective telegram configuration, environment included: a fresh struct,
	// never the one of the file kept by fileTelegram.
	c.Telegram = &TelegramConfig{APIID: c.APIID, APIHash: c.APIHash, BotToken: c.BotToken}
	return c, nil
}

// Save writes the configuration back. The identifiers keep the value read
// from the file, never the one of the TG_* variables: F4 to F7 and every
// /set call go through here. Same rule for the [telegram] section, which
// Load rebuilt with the environment in it: the file gets its own back, and a
// file without a section keeps none.
//
// Temporary file then rename (WriteAtomic): a truncating write cut in the
// middle would leave a config.toml without its api_id and its api_hash.
func (c *Config) Save() error {
	if c.dir == "" {
		return nil // built with no file (the tests): nothing to write, and never the working directory
	}
	out := *c
	out.APIID, out.APIHash, out.BotToken = c.fileID, c.fileHash, c.fileToken
	out.Telegram = c.fileTelegram
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(&out); err != nil {
		return err
	}
	return WriteAtomic(c.Path(), buf.Bytes(), 0o600)
}

// ValidIRCName : the key of an IRC network — 1 to 32 characters of
// [a-z0-9_-]. Lower case only: it is a section key and a command argument.
func ValidIRCName(name string) bool {
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

// WriteAtomic writes data to path through a temporary file of the same
// directory and a rename: the file is there whole or not at all, never cut in
// the middle by a crash. Shared by the config, the local names, the folded
// sections, the cache and the map tiles. os.CreateTemp gives 0600; perm is
// applied after, and the temporary file never survives a failure.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename went through
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (c *Config) Path() string { return filepath.Join(c.dir, "config.toml") }

// SessionPath : one session per identity; a bot and a user account never
// share the same file.
// DiscordTokenPath : the token file of the QR login (mode 0600, next to the
// Telegram session), read when [discord] has no token_cmd.
func (c *Config) DiscordTokenPath() string { return filepath.Join(c.dir, "discord.token") }

func (c *Config) SessionPath() string {
	if c.BotToken != "" {
		return filepath.Join(c.dir, "session-bot.json")
	}
	return filepath.Join(c.dir, "session.json")
}

// Expand replaces ~ with the home directory.
func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[1:])
		}
	}
	return p
}
