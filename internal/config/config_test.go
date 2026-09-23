package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFromDefaultsAndSave(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Images != "auto" || !c.Avatars || !c.Timestamps || c.Hover != HoverMenu || c.AutoMediaMaxKB != 5120 || c.KittyImages != 48 || c.DownloadDir != "~/Downloads/ttyloom" || c.Maps {
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
	c.Theme = "Catppuccin Mocha"
	c.Bell, c.AutoOpenDays = false, 3
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c2, err := LoadFrom(dir)
	if err != nil || c2.Theme != "Catppuccin Mocha" || !c2.Timestamps {
		t.Fatalf("reload: %v %+v", err, c2)
	}
	if c2.Bell || c2.AutoOpenDays != 3 {
		t.Fatalf("reload bell/auto_open_days: %+v", c2)
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

// TestSaveKeepsUnknownKeys : the keys of config.toml no field takes (a newer
// version's, a table of its own) stay through a Save. The known ones are the
// struct's.
func TestSaveKeepsUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	body := "future = \"kept\"\n[future_table]\nx = 1\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.Images = "kitty"
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(c.Path())
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Images != "kitty" {
		t.Fatalf("known keys: images %q\n%s", again.Images, b)
	}
	if got := strings.Join(again.Unknown, ","); got != "future,future_table,future_table.x" {
		t.Fatalf("unknown keys after Save: %q\n%s", got, b)
	}
}

// TestSaveLeavesBrokenFile : a config.toml broken by hand while the client
// runs is not written over — Save fails and names the file.
func TestSaveLeavesBrokenFile(t *testing.T) {
	c, err := LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const broken = "theme = \n"
	if err := os.WriteFile(c.Path(), []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err == nil || !strings.Contains(err.Error(), c.Path()) {
		t.Fatalf("Save over a broken file: %v", err)
	}
	if b, _ := os.ReadFile(c.Path()); string(b) != broken {
		t.Fatalf("broken file written over:\n%s", b)
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
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("theme = \"x\"\nimagess = \"off\"\n"), 0o600)
	c, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Unknown) != 1 || c.Unknown[0] != "imagess" {
		t.Fatalf("unknown keys: %v", c.Unknown)
	}
}

// TestTabs : the tab bar key is read back from the file.
func TestTabs(t *testing.T) {
	dir := t.TempDir()
	body := "tabs = true\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Tabs {
		t.Fatal("tabs not read")
	}
}
