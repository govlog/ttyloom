package tgc

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// loadTelegram : a config.toml written for the test, read with the Telegram
// module; the session and the cache live in the same directory.
func loadTelegram(t *testing.T, body string) (*config.Config, *Module, error) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TTYLOOM_DIR", dir)
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := NewModule()
	cfg, err := config.LoadFrom(dir, m)
	return cfg, m, err
}

func clearTG(t *testing.T) {
	for _, v := range []string{"TG_API_ID", "TG_API_HASH", "TG_BOT_TOKEN"} {
		t.Setenv(v, "")
	}
}

// The TG_* variables win over the file and are never saved; the session
// file follows the kind of account.
func TestModuleEnvAndSession(t *testing.T) {
	clearTG(t)
	t.Setenv("TG_API_ID", "42")
	t.Setenv("TG_API_HASH", "h")
	cfg, m, err := loadTelegram(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Settings().APIID != 42 || m.Settings().BotToken != "" || filepath.Base(m.SessionPath()) != "session.json" {
		t.Fatalf("user account: %+v %s", m.Settings(), m.SessionPath())
	}
	cfg.Theme = "Catppuccin Mocha"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	clearTG(t)
	m2 := NewModule()
	c2, err := config.LoadFrom(filepath.Dir(cfg.Path()), m2)
	// api_id came from the environment: Save keeps the value of the file (0
	// here), it does not persist the variable.
	if err != nil || c2.Theme != "Catppuccin Mocha" || m2.Settings().APIID != 0 {
		t.Fatalf("reload: %v %+v", err, m2.Settings())
	}
	t.Setenv("TG_API_ID", "42")
	t.Setenv("TG_API_HASH", "h")
	t.Setenv("TG_BOT_TOKEN", "123:abc")
	m3 := NewModule()
	if _, err := config.LoadFrom(filepath.Dir(cfg.Path()), m3); err != nil {
		t.Fatal(err)
	}
	if m3.Settings().BotToken != "123:abc" || filepath.Base(m3.SessionPath()) != "session-bot.json" {
		t.Fatalf("bot mode: %+v %s", m3.Settings(), m3.SessionPath())
	}
}

// TestSaveKeepsEnvSecretsOut : TG_API_ID / TG_API_HASH / TG_BOT_TOKEN take
// precedence over the file but never land in it through a Save.
func TestSaveKeepsEnvSecretsOut(t *testing.T) {
	clearTG(t)
	t.Setenv("TG_API_ID", "42")
	t.Setenv("TG_API_HASH", "SECRET_API_HASH")
	t.Setenv("TG_BOT_TOKEN", "123:SECRET_BOT_TOKEN")
	c, m, err := loadTelegram(t, "api_id = 7\napi_hash = \"DU_FICHIER\"\nbot_token = \"\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if s := m.Settings(); s.APIID != 42 || s.APIHash != "SECRET_API_HASH" || s.BotToken != "123:SECRET_BOT_TOKEN" {
		t.Fatalf("environment ignored: %+v", s)
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
	clearTG(t)
	m2 := NewModule()
	c2, err := config.LoadFrom(filepath.Dir(c.Path()), m2)
	if err != nil {
		t.Fatal(err)
	}
	if s := m2.Settings(); s.APIID != 7 || s.APIHash != "DU_FICHIER" || s.BotToken != "" {
		t.Fatalf("file values lost: %+v", s)
	}
	if c2.Theme != "Catppuccin Mocha" {
		t.Fatalf("the rest was not saved: %+v", c2)
	}
}

// TestTelegramSection : the historic flat keys keep meaning [telegram]; the
// section wins when both are there.
func TestTelegramSection(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		want Settings
	}{
		{"plat", "api_id = 7\napi_hash = \"H\"\nbot_token = \"T\"\n", Settings{APIID: 7, APIHash: "H", BotToken: "T"}},
		{"section", "[telegram]\napi_id = 9\napi_hash = \"S\"\n", Settings{APIID: 9, APIHash: "S"}},
		{"both", "api_id = 7\napi_hash = \"H\"\nbot_token = \"T\"\n[telegram]\napi_id = 9\napi_hash = \"S\"\n", Settings{APIID: 9, APIHash: "S"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			clearTG(t)
			_, m, err := loadTelegram(t, c.body)
			if err != nil {
				t.Fatal(err)
			}
			if m.Settings() != c.want {
				t.Fatalf("settings = %+v, want %+v", m.Settings(), c.want)
			}
		})
	}
}

// TestSaveKeepsSectionShape : a config.toml written with [telegram] keeps its
// section after a Save, and the TG_* secrets stay out of it just as much as
// with the flat keys.
func TestSaveKeepsSectionShape(t *testing.T) {
	clearTG(t)
	t.Setenv("TG_API_HASH", "SECRET_API_HASH")
	c, m, err := loadTelegram(t, "[telegram]\napi_id = 7\napi_hash = \"DU_FICHIER\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if m.Settings().APIHash != "SECRET_API_HASH" {
		t.Fatalf("environment ignored: %+v", m.Settings())
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
	clearTG(t)
	m2 := NewModule()
	c2, err := config.LoadFrom(filepath.Dir(c.Path()), m2)
	if err != nil {
		t.Fatal(err)
	}
	if s := m2.Settings(); s.APIID != 7 || s.APIHash != "DU_FICHIER" || c2.Theme != "Catppuccin Mocha" {
		t.Fatalf("round trip: %+v %s", s, c2.Theme)
	}
}

// Credentials: one network and its cache; the launch gives a backend.
func TestModuleTelegramOnly(t *testing.T) {
	clearTG(t)
	_, m, err := loadTelegram(t, "api_id = 42\napi_hash = \"hash\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Networks(), []string{"telegram"}) {
		t.Fatalf("networks: %v", m.Networks())
	}
	if sub, keep := m.Cache("telegram"); sub != "telegram" || keep {
		t.Fatalf("cache: %q %v", sub, keep)
	}
	if b, err := m.Launch(context.Background(), nil, "telegram", make(chan model.Event, 8)); err != nil || b == nil {
		t.Fatalf("launch: %v %v", b, err)
	}
}

// A bot and an account do not share their cache.
func TestModuleBotCache(t *testing.T) {
	clearTG(t)
	_, m, err := loadTelegram(t, "api_id = 42\napi_hash = \"hash\"\nbot_token = \"1:x\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if sub, _ := m.Cache("telegram"); sub != filepath.Join("telegram", "bot") {
		t.Fatalf("bot cache: %q", sub)
	}
}

// Without api_id nothing starts: the message names the file to fill in.
func TestModuleNoAPIID(t *testing.T) {
	clearTG(t)
	_, _, err := loadTelegram(t, "api_hash = \"hash\"\n")
	if err == nil || !strings.Contains(err.Error(), strings.SplitN(i18n.T("main_no_api_id"), ":", 2)[0]) {
		t.Fatalf("no api_id: %v", err)
	}
}

// No credentials at all: no network, no error.
func TestModuleNoTelegram(t *testing.T) {
	clearTG(t)
	_, m, err := loadTelegram(t, "theme = \"x\"\n")
	if err != nil || len(m.Networks()) != 0 {
		t.Fatalf("networks %v, %v", m.Networks(), err)
	}
}
