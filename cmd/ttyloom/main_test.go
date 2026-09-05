package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
)

// loadCfg : a configuration read from a file written for the test. The cache
// and the session stay in the temporary directory (TTYLOOM_DIR).
func loadCfg(t *testing.T, body string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TTYLOOM_DIR", dir)
	for _, v := range []string{"TG_API_ID", "TG_API_HASH", "TG_BOT_TOKEN"} {
		t.Setenv(v, "")
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

const tgOnly = "api_id = 42\napi_hash = \"hash\"\n"

// No [discord] section: one network, one cache, one thing to run.
func TestBackendsTelegramOnly(t *testing.T) {
	cfg := loadCfg(t, tgOnly)
	events := make(chan model.Envelope, 8)
	nets, caches, run, err := backends(cfg, events)
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 1 || nets[model.NetTelegram] == nil {
		t.Fatalf("networks: %v", nets)
	}
	if len(caches) != 1 || caches[model.NetTelegram] == nil || len(run) != 1 {
		t.Fatalf("caches %v, run %d", caches, len(run))
	}
}

// [discord] with a token_cmd that works: two networks, two caches.
func TestBackendsWithDiscord(t *testing.T) {
	cfg := loadCfg(t, tgOnly+"[discord]\ntoken_cmd = \"echo tok\"\n")
	events := make(chan model.Envelope, 8)
	nets, caches, run, err := backends(cfg, events)
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 2 || nets[model.NetDiscord] == nil {
		t.Fatalf("networks: %v", nets)
	}
	if len(caches) != 2 || caches[model.NetDiscord] == nil || len(run) != 2 {
		t.Fatalf("caches %v, run %d", caches, len(run))
	}
}

func TestBackendsDiscordOnly(t *testing.T) {
	cfg := loadCfg(t, "[discord]\ntoken_cmd = \"echo tok\"\n")
	nets, caches, run, err := backends(cfg, make(chan model.Envelope, 8))
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 1 || nets[model.NetDiscord] == nil || len(caches) != 1 || len(run) != 1 {
		t.Fatalf("networks %v, caches %v, starters %d", nets, caches, len(run))
	}
}

func TestBackendsDiscordOnlyTokenError(t *testing.T) {
	cfg := loadCfg(t, "[discord]\ntoken_cmd = \"false\"\n")
	if _, _, _, err := backends(cfg, make(chan model.Envelope, 8)); err == nil {
		t.Fatal("started with no usable network")
	}
}

// token_cmd that fails: the error is said, Discord is left out and Telegram
// starts all the same.
func TestBackendsTokenError(t *testing.T) {
	cfg := loadCfg(t, tgOnly+"[discord]\ntoken_cmd = \"ttyloom-no-such-command-here\"\n")
	events := make(chan model.Envelope, 8)
	nets, _, _, err := backends(cfg, events)
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 1 || nets[model.NetTelegram] == nil {
		t.Fatalf("networks: %v", nets)
	}
	select {
	case env := <-events:
		log, ok := env.Ev.(model.EvLog)
		if !ok || log.Level != "ERROR" || env.Net != model.NetDiscord {
			t.Fatalf("event of the token error: %+v", env)
		}
	default:
		t.Fatal("token error: nothing said")
	}
}

// Without api_id nothing starts: the message names the file to fill in.
func TestBackendsNoAPIID(t *testing.T) {
	cfg := loadCfg(t, "api_hash = \"hash\"\n")
	if _, _, _, err := backends(cfg, make(chan model.Envelope, 8)); err == nil {
		t.Fatal("no api_id: no error")
	}
}

// A killed session leaves .part-* files at every depth a download uses:
// download_dir, its maps/ and paste/, and avatars/<net>/.
func TestCleanPartsAllDepths(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "avatars", "discord")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, ".part-1"), filepath.Join(dir, "maps", ".part-2"), filepath.Join(deep, ".part-3")} {
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, nil, 0o600)
	}
	keep := filepath.Join(deep, "7.jpg")
	os.WriteFile(keep, nil, 0o600)
	cleanParts(dir)
	for _, p := range []string{filepath.Join(dir, ".part-1"), filepath.Join(dir, "maps", ".part-2"), filepath.Join(deep, ".part-3")} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("left behind: %s", p)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("a real file was removed")
	}
}
