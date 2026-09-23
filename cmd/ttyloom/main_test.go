package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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
	nets, caches, launch, err := backends(context.Background(), cfg, make(chan model.Envelope, 8), nil)
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

// Without api_id nothing starts: the message names the file to fill in.
func TestBackendsNoAPIID(t *testing.T) {
	cfg := loadCfg(t, "api_hash = \"hash\"\n")
	if _, _, _, err := backends(context.Background(), cfg, make(chan model.Envelope, 8), nil); err == nil {
		t.Fatal("no api_id: no error")
	}
}

// A killed session leaves .part-* files at every depth a download uses:
// download_dir, its maps/ and paste/, and avatars/<net>/. A recent one may
// be the download of another running instance: it stays.
func TestCleanPartsAllDepths(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "avatars", "discord")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, p := range []string{filepath.Join(dir, ".part-1"), filepath.Join(dir, "maps", ".part-2"), filepath.Join(deep, ".part-3")} {
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, nil, 0o600)
		os.Chtimes(p, old, old)
	}
	keep := filepath.Join(deep, "7.jpg")
	os.WriteFile(keep, nil, 0o600)
	fresh := filepath.Join(dir, ".part-4")
	os.WriteFile(fresh, nil, 0o600)
	cleanParts(dir)
	for _, p := range []string{filepath.Join(dir, ".part-1"), filepath.Join(dir, "maps", ".part-2"), filepath.Join(deep, ".part-3")} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("left behind: %s", p)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("a real file was removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("the part file of a running download was removed")
	}
}
