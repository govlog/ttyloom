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

// With no lang in config.toml the messages follow LC_ALL, then LC_MESSAGES,
// then LANG: French formats with English messages (LANG=fr_FR, LC_MESSAGES=C)
// is a common setup.
func TestLanguageFromLocale(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TTYLOOM_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	defer i18n.Set("en")
	for _, c := range []struct{ all, messages, lang, want string }{
		{"", "C", "fr_FR.UTF-8", "en"},
		{"", "fr_FR.UTF-8", "C", "fr"},
		{"fr_FR.UTF-8", "C", "C", "fr"},
	} {
		t.Setenv("LC_ALL", c.all)
		t.Setenv("LC_MESSAGES", c.messages)
		t.Setenv("LANG", c.lang)
		if _, err := load(modules()); err != nil {
			t.Fatal(err)
		}
		if got := i18n.Lang(); got != c.want {
			t.Errorf("LC_ALL=%q LC_MESSAGES=%q LANG=%q: %s, want %s", c.all, c.messages, c.lang, got, c.want)
		}
	}
}
