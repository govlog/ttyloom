package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFromDefaultsAndSave(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TG_API_ID", "42")
	c, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.APIID != 42 || c.Images != "auto" || !c.Avatars || !c.Timestamps || c.Hover != HoverMenu || c.AutoMediaMaxKB != 5120 || c.KittyImages != 48 || c.DownloadDir != "~/Downloads/ttyloom" || c.Maps {
		t.Fatalf("defaults: %+v", c)
	}
	if !c.Bell || c.AutoOpenDays != 7 {
		t.Fatalf("defaults bell/auto_open_days: %+v", c)
	}
	if c.Notify != "terminal" {
		t.Fatalf("default notify: %+v", c)
	}
	if !c.Cache || c.CacheMessages != 2000 {
		t.Fatalf("default cache/cache_messages: %+v", c)
	}
	if c.LogDir != "~/.local/share/ttyloom/logs" || c.Log {
		t.Fatalf("default log_dir/log: %+v", c)
	}
	if !c.Separator {
		t.Fatalf("default separator: %+v", c)
	}
	if c.SidebarSort != "recent" {
		t.Fatalf("default sidebar_sort: %+v", c)
	}
	if c.BotToken != "" || filepath.Base(c.SessionPath()) != "session.json" {
		t.Fatalf("user account: %q %s", c.BotToken, c.SessionPath())
	}
	c.Theme = "Catppuccin Mocha"
	c.Bell, c.AutoOpenDays = false, 3
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TG_API_ID", "")
	c2, err := LoadFrom(dir)
	// api_id came from the environment: Save keeps the value of the file (0
	// here), it does not persist the variable.
	if err != nil || c2.Theme != "Catppuccin Mocha" || c2.APIID != 0 || !c2.Timestamps {
		t.Fatalf("reload: %v %+v", err, c2)
	}
	if c2.Bell || c2.AutoOpenDays != 3 {
		t.Fatalf("reload bell/auto_open_days: %+v", c2)
	}
	t.Setenv("TG_BOT_TOKEN", "123:abc")
	c3, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c3.BotToken != "123:abc" || filepath.Base(c3.SessionPath()) != "session-bot.json" {
		t.Fatalf("bot mode: %q %s", c3.BotToken, c3.SessionPath())
	}
}

// TestHoverConfigCompat : compatibility of the old boolean hover field, and
// round trip of the string field.
func TestHoverConfigCompat(t *testing.T) {
	write := func(t *testing.T, body string) *Config {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		c, err := LoadFrom(dir)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if c := write(t, "hover = true\n"); c.Hover != HoverMenu {
		t.Fatalf("hover = true : %v", c.Hover)
	}
	if c := write(t, "hover = false\n"); c.Hover != HoverOff {
		t.Fatalf("hover = false : %v", c.Hover)
	}
	if c := write(t, `hover = "highlight"`+"\n"); c.Hover != HoverHighlight {
		t.Fatalf("hover = highlight : %v", c.Hover)
	}
}

// TestSaveKeepsEnvSecretsOut : TG_API_ID / TG_API_HASH / TG_BOT_TOKEN take
// over the file at load time, but Save must never write them into it —
// /set, /theme and F4 to F7 all save, and a config.toml deliberately left
// empty ends up in a dotfiles repository or in a backup.
func TestSaveKeepsEnvSecretsOut(t *testing.T) {
	dir := t.TempDir()
	body := "api_id = 7\napi_hash = \"DU_FICHIER\"\nbot_token = \"\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TG_API_ID", "42")
	t.Setenv("TG_API_HASH", "SECRET_API_HASH")
	t.Setenv("TG_BOT_TOKEN", "123:SECRET_BOT_TOKEN")
	c, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.APIID != 42 || c.APIHash != "SECRET_API_HASH" || c.BotToken != "123:SECRET_BOT_TOKEN" {
		t.Fatalf("environment ignored: %+v", c)
	}
	c.Theme = "Catppuccin Mocha"
	if c.Save() != nil {
		t.Fatal("Save")
	}
	fi, err := os.Stat(c.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 { // temporary file + rename must keep the mode
		t.Fatalf("perms after Save: %v", fi.Mode())
	}
	b, err := os.ReadFile(c.Path())
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, secret := range []string{"SECRET_API_HASH", "SECRET_BOT_TOKEN", "api_id = 42"} {
		if strings.Contains(got, secret) {
			t.Fatalf("%q written into config.toml:\n%s", secret, got)
		}
	}
	t.Setenv("TG_API_ID", "")
	t.Setenv("TG_API_HASH", "")
	t.Setenv("TG_BOT_TOKEN", "")
	c2, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c2.APIID != 7 || c2.APIHash != "DU_FICHIER" || c2.BotToken != "" {
		t.Fatalf("file values lost: %+v", c2)
	}
	if c2.Theme != "Catppuccin Mocha" {
		t.Fatalf("the rest was not saved: %+v", c2)
	}
}

// TestTelegramSection : the historic flat keys keep meaning [telegram]; the
// section wins when both are there. The effective values are also the flat
// fields — SessionPath and tgc read those.
func TestTelegramSection(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		want TelegramConfig
	}{
		{"plat", "api_id = 7\napi_hash = \"H\"\nbot_token = \"T\"\n", TelegramConfig{APIID: 7, APIHash: "H", BotToken: "T"}},
		{"section", "[telegram]\napi_id = 9\napi_hash = \"S\"\n", TelegramConfig{APIID: 9, APIHash: "S"}},
		{"both", "api_id = 7\napi_hash = \"H\"\nbot_token = \"T\"\n[telegram]\napi_id = 9\napi_hash = \"S\"\n", TelegramConfig{APIID: 9, APIHash: "S"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadFrom(dir)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Telegram == nil || *cfg.Telegram != c.want {
				t.Fatalf("[telegram] = %+v, want %+v", cfg.Telegram, c.want)
			}
			if (TelegramConfig{APIID: cfg.APIID, APIHash: cfg.APIHash, BotToken: cfg.BotToken}) != c.want {
				t.Fatalf("flat keys = %d %q %q, want %+v", cfg.APIID, cfg.APIHash, cfg.BotToken, c.want)
			}
		})
	}
}

// TestSaveKeepsSectionShape : a config.toml written with [telegram] keeps its
// section after a Save, and the TG_* secrets stay out of it just as much as
// with the flat keys.
func TestSaveKeepsSectionShape(t *testing.T) {
	dir := t.TempDir()
	body := "[telegram]\napi_id = 7\napi_hash = \"DU_FICHIER\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TG_API_HASH", "SECRET_API_HASH")
	c, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.APIHash != "SECRET_API_HASH" {
		t.Fatalf("environment ignored: %+v", c.Telegram)
	}
	c.Theme = "Catppuccin Mocha"
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(c.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); strings.Contains(got, "SECRET_API_HASH") || !strings.Contains(got, "[telegram]") {
		t.Fatalf("Save leaked the secret or lost the section:\n%s", got)
	}
	t.Setenv("TG_API_HASH", "")
	c2, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c2.APIID != 7 || c2.APIHash != "DU_FICHIER" || c2.Theme != "Catppuccin Mocha" {
		t.Fatalf("round trip: %+v %+v", c2, c2.Telegram)
	}
}

// TestAutoMediaMaxKBBounded : a hand-written value must not cut every
// automatic download in silence (negative) nor make int64(n)*1024 overflow.
func TestAutoMediaMaxKBBounded(t *testing.T) {
	for _, c := range []struct {
		body string
		want int
	}{
		{"auto_media_max_kb = -1\n", 0},
		{"auto_media_max_kb = 9223372036854775807\n", MaxAutoMediaKB},
		{"auto_media_max_kb = 2048\n", 2048},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadFrom(dir)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.AutoMediaMaxKB != c.want {
			t.Errorf("%q: %d, want %d", c.body, cfg.AutoMediaMaxKB, c.want)
		}
	}
}

// TestDiscordToken : the token comes from the command, never from the file.
// No shell — the words of token_cmd are the argv.
func TestDiscordToken(t *testing.T) {
	d := &DiscordConfig{TokenCmd: "echo  abc "}
	got, err := d.Token()
	if err != nil || got != "abc" {
		t.Fatalf("token of %q: %q, %v", d.TokenCmd, got, err)
	}
	// Empty command, command that fails, command that prints nothing: an empty
	// token would go to the network as it is and come back as an obscure error.
	for _, cmd := range []string{"", "   ", "ttyloom-no-such-command-here", "true", "echo   "} {
		d := &DiscordConfig{TokenCmd: cmd}
		if tok, err := d.Token(); err == nil {
			t.Errorf("token_cmd %q: no error, token %q", cmd, tok)
		}
	}
}

// WriteAtomic : the file appears whole or not at all, private, and leaves no
// temporary file behind.
func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.toml")
	if err := WriteAtomic(p, []byte("x = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "x = 1\n" {
		t.Fatalf("content: %q", b)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("perm: %v", st.Mode())
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Fatalf("temporary file left: %d entries", len(es))
	}
}

// A key config.toml does not know is reported, not silently ignored: the user
// would believe the option active.
func TestLoadFromUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("api_id = 1\nimagess = \"off\"\n"), 0o600)
	c, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Unknown) != 1 || c.Unknown[0] != "imagess" {
		t.Fatalf("unknown keys: %v", c.Unknown)
	}
}
