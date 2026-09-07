package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// No [discord] section: one network, one cache.
func TestBackendsTelegramOnly(t *testing.T) {
	cfg := loadCfg(t, tgOnly)
	nets, caches, launch, err := backends(context.Background(), cfg, make(chan model.Envelope, 8))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(nets, []string{model.NetTelegram}) || launch == nil {
		t.Fatalf("networks: %v", nets)
	}
	if len(caches) != 1 || caches[model.NetTelegram] == nil {
		t.Fatalf("caches %v", caches)
	}
}

// [discord] with a token_cmd that works: two networks, two caches, and the
// launch of Discord gives a backend whose stop reaches the UI chan.
func TestBackendsWithDiscord(t *testing.T) {
	cfg := loadCfg(t, tgOnly+"[discord]\ntoken_cmd = \"echo tok\"\n")
	events := make(chan model.Envelope, 8)
	nets, caches, launch, err := backends(context.Background(), cfg, events)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(nets, []string{model.NetTelegram, model.NetDiscord}) {
		t.Fatalf("networks: %v", nets)
	}
	if len(caches) != 2 || caches[model.NetDiscord] == nil {
		t.Fatalf("caches %v", caches)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Run ends at once: no network is reached
	b, err := launch(ctx, model.NetDiscord)
	if err != nil || b == nil {
		t.Fatalf("launch: %v %v", b, err)
	}
	select {
	case env := <-events:
		if _, ok := env.Ev.(model.EvStopped); !ok || env.Net != model.NetDiscord {
			t.Fatalf("event after the stop: %+v", env)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no EvStopped after the stop")
	}
}

func TestBackendsDiscordOnly(t *testing.T) {
	cfg := loadCfg(t, "[discord]\ntoken_cmd = \"echo tok\"\n")
	nets, caches, _, err := backends(context.Background(), cfg, make(chan model.Envelope, 8))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(nets, []string{model.NetDiscord}) || len(caches) != 1 {
		t.Fatalf("networks %v, caches %v", nets, caches)
	}
}

// [discord] with no token_cmd: the token file of the configuration directory
// is the source, and with no file either the launch still gives a backend —
// the one that logs in by QR and writes the file.
func TestBackendsDiscordTokenFile(t *testing.T) {
	cfg := loadCfg(t, "[discord]\n")
	nets, _, launch, err := backends(context.Background(), cfg, make(chan model.Envelope, 8))
	if err != nil || !slices.Equal(nets, []string{model.NetDiscord}) {
		t.Fatalf("networks %v, %v", nets, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if b, err := launch(ctx, model.NetDiscord); err != nil || b == nil {
		t.Fatalf("launch with no token: %v %v", b, err)
	}
	os.WriteFile(cfg.DiscordTokenPath(), []byte("tok\n"), 0o600)
	if b, err := launch(ctx, model.NetDiscord); err != nil || b == nil {
		t.Fatalf("launch with the token file: %v %v", b, err)
	}
}

// token_cmd that fails: the network is configured all the same (the client
// starts, /discord login retries), its launch is the one that fails.
func TestBackendsTokenError(t *testing.T) {
	cfg := loadCfg(t, "[discord]\ntoken_cmd = \"false\"\n")
	events := make(chan model.Envelope, 8)
	nets, _, launch, err := backends(context.Background(), cfg, events)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(nets, []string{model.NetDiscord}) {
		t.Fatalf("networks: %v", nets)
	}
	b, err := launch(context.Background(), model.NetDiscord)
	if b != nil || err == nil || !strings.Contains(err.Error(), "token_cmd") {
		t.Fatalf("launch with a failing token_cmd: %v %v", b, err)
	}
	select {
	case env := <-events:
		t.Fatalf("event after a failed launch: %+v", env)
	default:
	}
}

// Without api_id nothing starts: the message names the file to fill in.
func TestBackendsNoAPIID(t *testing.T) {
	cfg := loadCfg(t, "api_hash = \"hash\"\n")
	if _, _, _, err := backends(context.Background(), cfg, make(chan model.Envelope, 8)); err == nil {
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
