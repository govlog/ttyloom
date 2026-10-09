// Package config holds ~/.config/ttyloom/config.toml.
package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/module"

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

// secretTimeout : the wait for a password prompt that nobody answers. A
// variable: the test shortens it.
var secretTimeout = 30 * time.Second

// SecretCmd runs cmd — split on blanks, no shell — and gives its trimmed
// stdout: a secret read from a password manager (the Discord token, a
// NickServ password). Shared by the modules. label heads the errors.
//
// The timeout of the command is the one of a password prompt that nobody
// answers: a pinentry waiting on a locked keyring would otherwise hold the
// start of the whole client with an empty screen. It kills the command; a
// child of it that keeps its pipes (gpg behind pass) is given up a second
// later, or the wait would last as long as that child.
func SecretCmd(label, cmd string) (string, error) {
	f := strings.Fields(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), secretTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, f[0], f[1:]...)
	c.WaitDelay = time.Second
	out, err := c.Output()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil // the command itself ended well: its output is whole
	}
	if err != nil {
		// The first line the command wrote on stderr names the cause ("cat:
		// …: No such file"); "exit status 1" alone says nothing.
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if line, _, _ := strings.Cut(strings.TrimSpace(string(ee.Stderr)), "\n"); line != "" {
				return "", fmt.Errorf("%s: %w: %s", label, err, line)
			}
		}
		return "", fmt.Errorf("%s: %w", label, err)
	}
	s := strings.TrimSpace(string(out))
	if s == "" { // a command that succeeds and prints nothing is not a secret
		return "", fmt.Errorf("%s printed nothing", label)
	}
	return s, nil
}

type Config struct {
	Theme             string    `toml:"theme"`
	DownloadDir       string    `toml:"download_dir"`
	AutoMediaMaxKB    int       `toml:"auto_media_max_kb"`
	Images            string    `toml:"images"`
	ImagesHover       bool      `toml:"images_hover"`
	Avatars           bool      `toml:"avatars"`
	KittyImages       int       `toml:"kitty_images"`
	VideoFrames       int       `toml:"video_inline_frames"`
	Video             string    `toml:"video"`
	GifPlay           string    `toml:"gifplay"` // animated GIFs: always | hover | off
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
	Tabs              bool      `toml:"tabs"` // tabs per network at the right of the status line (F9)
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
	Sidebar           string    `toml:"sidebar"` // mode F2 left the sidebar in: hidden | chats | windows
	Members           bool      `toml:"members"` // member box (F3) left open
	Lang              string    `toml:"lang"`
	UpdateCheck       bool      `toml:"update_check"` // asks GitHub at start whether a newer release exists

	// Unknown : keys of the file no field takes (a typo, imagess = "off"). The
	// UI says so at start, otherwise the user believes the option active.
	Unknown []string `toml:"-"`

	dir     string
	mods    []module.Module // the modules, for Save
	claimed map[string]bool // top-level keys the modules asked for, lower case
}

// source : the ConfigSource given to the modules — config.toml as
// primitives, and the top-level keys each one asked for.
type source struct {
	md      toml.MetaData
	raw     map[string]toml.Primitive
	claimed map[string]bool
	unknown []string
}

func (s *source) Decode(key string, v any) (bool, error) {
	s.claimed[strings.ToLower(key)] = true
	for k, p := range s.raw {
		if strings.EqualFold(k, key) {
			if err := s.md.PrimitiveDecode(p, v); err != nil {
				return true, fmt.Errorf(i18n.T("error_with_prefix"), key, err)
			}
			return true, nil
		}
	}
	return false, nil
}

func (s *source) Unknown(key string) { s.unknown = append(s.unknown, key) }

// sink : the ConfigSink of Save — the table written to config.toml.
type sink map[string]any

func (s sink) Set(key string, v any) { s[key] = v }

const defaultFile = `# ttyloom
# Sections go at the END of the file: a plain key written after a [section]
# would be read as one of its keys. The keys of each network follow the ones
# of the client.
theme = ""            # empty = current Ghostty theme, "terminal" = the colours of the terminal, otherwise a theme name
download_dir = "~/Downloads/ttyloom"
auto_media_max_kb = 5120 # media downloaded on their own up to this size, in KiB (0 = none; 524288 at most)
images = "auto"       # auto | kitty | sixel | terminology | halfblock | off (F4 cycles them)
images_hover = false  # image shown only under the mouse (F5), with no line kept free
avatars = true        # profile photos before the names and in the sidebar (kitty only)
kitty_images = 48     # images kept by the terminal (kitty); above that, the least recently shown are freed
video_inline_frames = 300 # frames decoded by "l" on a video without mpv (300 = 30 s at 10 fps, 1000 at most)
video = "show"        # inline video: show (first frame, "l" plays) | hidden (label only) | autoplay
gifplay = "always"    # animated GIFs: always | hover (the one under the mouse only) | off (first frame alone)
timestamps = true
timestamps_seconds = false # 15:04:05 instead of 15:04 before each message
cycle_mode = "next"   # Ctrl+X: next (window after the current one) | last_unread (unread windows in turn, then back)
multiline = false     # Shift+Enter opens an expanded editor
link_previews = true   # link preview: title, description and thumbnail under the message
maps = false           # OpenStreetMap map under a position (third-party network, off by default)
hover = "menu"         # mouse hover: menu | highlight (both: background of the message under the pointer) | off
bell = true            # bell (\a) on a private message or a mention
notify = "terminal"    # notification on a private message or a mention: terminal (OSC 777) | desktop (notify-send) | off
auto_open_days = 7     # opens at start the chats active for N days, in hidden windows (0 = off)
aggregate = false      # window 0: stream of every message received (F6)
tabs = false           # tabs per network at the right of the status line, F9 switches (several networks only)
cache = true           # local cache (dialogs, history) for a fast start
cache_messages = 2000  # messages kept per chat in the disk cache; scrolling up loads the rest from the network — not on IRC, where the cache is the only copy (/log keeps everything)
log_dir = "~/.local/share/ttyloom/logs"
log = false            # /log: logs every window created afterwards
separator = true       # separator line above the status bar
redline = true         # red last-read line (unread separator) in each window
sidebar = "hidden"      # sidebar at start: hidden | chats | windows (F2 writes it)
sidebar_sort = "recent" # sort of the F2 sidebar (F7 cycles it): recent | alpha | unread
sidebar_width = 26      # width of the F2 sidebar; the vertical bar drags with the mouse
sidebar_split = false   # windows list in two sections, channels then direct messages (⊟ of the header)
members = false         # member box (F3) open at start (F3 writes it)
spell = "off"           # spell checking: off, or Hunspell dictionaries joined with + (fr, en_US, fr+en_US)
spell_quotes = false    # also check the quoted lines and the code blocks
lang = ""               # interface language: en | fr | a chain such as fr+en; empty = $LC_ALL, $LC_MESSAGES or $LANG
update_check = false    # at start, asks GitHub whether a newer release exists (off: no request)
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

func Load(mods ...module.Module) (*Config, error) { return LoadFrom(Dir(), mods...) }

// LoadFrom reads config.toml of dir, and the keys of each module through its
// Load. A module that fails gives its error with the Config of the client
// still read — its language is the one the error is to be shown in.
func LoadFrom(dir string, mods ...module.Module) (*Config, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "config.toml")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		body := defaultFile
		for _, m := range mods {
			body += "\n" + m.Template()
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			return nil, err
		}
	}
	c := &Config{dir: dir, mods: mods, DownloadDir: "~/Downloads/ttyloom", AutoMediaMaxKB: 5120, Images: "auto", Avatars: true, KittyImages: 48, VideoFrames: 300, Video: "show", GifPlay: "always", Timestamps: true, LinkPreviews: true, Hover: HoverMenu,
		Bell: true, Notify: "terminal", AutoOpenDays: 7, Cache: true, CacheMessages: 2000, LogDir: "~/.local/share/ttyloom/logs", Separator: true, Redline: true, SidebarSort: "recent", SidebarWidth: 26, Sidebar: "hidden", Spell: "off", CycleMode: "next"}
	md, err := toml.DecodeFile(path, c)
	if err != nil {
		return nil, fmt.Errorf(i18n.T("error_with_prefix"), path, err)
	}
	var raw map[string]toml.Primitive
	md2, err := toml.DecodeFile(path, &raw)
	if err != nil {
		return nil, fmt.Errorf(i18n.T("error_with_prefix"), path, err)
	}
	src := &source{md: md2, raw: raw, claimed: map[string]bool{}}
	for _, m := range mods {
		if err := m.Load(src); err != nil {
			return c, err // the keys of the client are read: the language of the error
		}
	}
	c.claimed = src.claimed
	// A key is unknown when neither the client nor a module took it. For the
	// modules, a top-level key is taken when one asked for it: a primitive
	// counts as decoded at the top, only its sub-keys stay undecoded.
	left := map[string]bool{}
	for k := range raw {
		if !src.claimed[strings.ToLower(k)] {
			left[k] = true
		}
	}
	for _, k := range md2.Undecoded() {
		left[k.String()] = true
	}
	for _, k := range md.Undecoded() {
		if left[k.String()] {
			c.Unknown = append(c.Unknown, k.String())
		}
	}
	c.Unknown = append(c.Unknown, src.unknown...)
	c.AutoMediaMaxKB = min(max(c.AutoMediaMaxKB, 0), MaxAutoMediaKB)
	return c, nil
}

// Save writes the configuration back: F4 to F7 and every /set call go
// through here. Each module writes its own keys, as its file had them (never
// a secret that came from the environment).
//
// The top-level keys of the file no field takes (a newer version's, a typo
// still to fix) are written back as they are; a file that no longer parses
// is left alone and the error says why.
//
// Temporary file then rename (WriteAtomic): a truncating write cut in the
// middle would leave a config.toml without the keys of its networks.
func (c *Config) Save() error {
	if c.dir == "" {
		return nil // built with no file (the tests): nothing to write, and never the working directory
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return err
	}
	var known map[string]any
	if _, err := toml.Decode(buf.String(), &known); err != nil {
		return err
	}
	for _, m := range c.mods {
		m.Save(sink(known))
	}
	file := map[string]any{}
	if _, err := toml.DecodeFile(c.Path(), &file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf(i18n.T("error_with_prefix"), c.Path(), err)
	}
	// A key goes to a field, or to the module that asked for it, when it is
	// its name in any case, like the decoder matches it: they write it, never
	// the file.
	t := reflect.TypeFor[Config]()
	for k := range file {
		if c.claimed[strings.ToLower(k)] {
			delete(file, k)
			continue
		}
		for i := range t.NumField() {
			name, _, _ := strings.Cut(t.Field(i).Tag.Get("toml"), ",")
			if name != "" && name != "-" && strings.EqualFold(name, k) {
				delete(file, k)
			}
		}
	}
	// ponytail: comments are lost at every Save (no comment round trip in the
	// TOML package), and the file comes out sorted; a local date or time
	// among the unknown keys moves by the UTC offset (written in UTC). Edit
	// the lines in place if that ever matters.
	maps.Copy(file, known)
	buf.Reset()
	if err := toml.NewEncoder(&buf).Encode(file); err != nil {
		return err
	}
	return WriteAtomic(c.Path(), buf.Bytes(), 0o600)
}

// WriteAtomic writes data to path through a temporary file of the same
// directory and a rename: the file is there whole or not at all, never cut in
// the middle by a crash. Shared by the config, the local names, the folded
// sections, the cache and the map tiles. os.CreateTemp gives 0600; perm is
// applied after, and the temporary file never survives a failure.
//
// A path that is a symbolic link (config.toml kept in a dotfiles repository)
// stays one: the file it points to is the one written. The data is on the
// disk before the rename — with delayed allocation (XFS, ext4) a crash just
// after it could leave the name on an empty file, and an IRC history has no
// other copy.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename went through
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if err != nil {
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

// Expand replaces ~ with the home directory.
func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[1:])
		}
	}
	return p
}
