package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/govlog/ttyloom/internal/config"
)

// A config.toml as v0.7 wrote it — flat Telegram keys, [discord], two
// [[irc]], a key of a newer version — goes through Load and Save with every
// value kept, and the TG_* secrets of the environment stay out of the file.
func TestConfigCompatV07(t *testing.T) {
	body := `api_id = 7
api_hash = "FILEHASH"
bot_token = ""
theme = "Catppuccin Mocha"
future_key = "kept"
[discord]
token_cmd = "echo tok"
[[irc]]
name = "libera"
host = "irc.libera.chat"
port = 6697
tls = true
nick = "me"
channels = ["#go-nuts"]
ignores = ["spam!*@*"]
[[irc]]
name = "oftc"
host = "irc.oftc.net"
port = 6697
tls = true
nick = "me"
`
	dir := t.TempDir()
	t.Setenv("TTYLOOM_DIR", dir)
	t.Setenv("TG_API_ID", "")
	t.Setenv("TG_BOT_TOKEN", "")
	t.Setenv("TG_API_HASH", "ENV_SECRET")
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var before map[string]any
	if _, err := toml.DecodeFile(path, &before); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFrom(dir, modules()...)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "ENV_SECRET") {
		t.Fatalf("secret written:\n%s", b)
	}
	var after map[string]any
	if _, err := toml.DecodeFile(path, &after); err != nil {
		t.Fatal(err)
	}
	kept(t, "", before, after)
}

// kept : every value of want is in got, recursively. Save may add the zero
// value of a field the file left out (user = "" in an [[irc]] table): v0.7
// did the same.
func kept(t *testing.T, path string, want, got any) {
	t.Helper()
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			t.Errorf("%s: %v became %v", path, want, got)
			return
		}
		for k, v := range w {
			kept(t, path+"."+k, v, g[k])
		}
	case []map[string]any:
		g, ok := got.([]map[string]any)
		if !ok || len(g) != len(w) {
			t.Errorf("%s: %v became %v", path, want, got)
			return
		}
		for i := range w {
			kept(t, fmt.Sprintf("%s[%d]", path, i), w[i], g[i])
		}
	default:
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s: %v became %v", path, want, got)
		}
	}
}
