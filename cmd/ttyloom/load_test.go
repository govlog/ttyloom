package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/protocols/tgc"
)

// A module's error at load (api_id missing) reads in the language of the
// configuration, as when main checked it after choosing the language.
func TestLoadErrorInConfigLanguage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TTYLOOM_DIR", dir)
	for _, v := range []string{"TG_API_ID", "TG_API_HASH", "TG_BOT_TOKEN"} {
		t.Setenv(v, "")
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("lang = \"fr\"\napi_hash = \"h\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer i18n.Set("en")
	_, err := load(modules())
	want := fmt.Sprintf(i18n.Table(tgc.Catalog, "fr")["main_no_api_id"], filepath.Join(dir, "config.toml"))
	if err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
}
