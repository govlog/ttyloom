package config

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
)

// fakeMod : a module with one section, [fake], and one refused key.
type fakeMod struct {
	Section struct {
		Key string `toml:"key"`
	}
	present bool
}

func (m *fakeMod) Name() string { return "fake" }
func (m *fakeMod) Load(src module.ConfigSource) error {
	var err error
	m.present, err = src.Decode("fake", &m.Section)
	if m.Section.Key == "refused" {
		src.Unknown("fake.key")
	}
	return err
}
func (m *fakeMod) Save(dst module.ConfigSink) {
	if m.present {
		dst.Set("fake", m.Section)
	}
}
func (m *fakeMod) Template() string            { return "# fake network: [fake] key = \"…\"\n" }
func (m *fakeMod) Networks() []string          { return nil }
func (m *fakeMod) Cache(string) (string, bool) { return "", false }
func (m *fakeMod) Commands() []module.Command  { return nil }
func (m *fakeMod) Claims(string) bool          { return false }
func (m *fakeMod) Label() string               { return "Fake" }
func (m *fakeMod) CanAdd() bool                { return false }
func (m *fakeMod) OpenSetup(module.Host)       {}
func (m *fakeMod) Launch(context.Context, module.Host, string, chan<- model.Event) (model.Backend, error) {
	return nil, nil
}

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A module reads its section, its unknown sub-keys are reported, and Save
// writes the section back with the rest of the file.
func TestModuleSection(t *testing.T) {
	dir := writeCfg(t, "theme = \"x\"\n[fake]\nkey = \"v\"\nextra = 1\n")
	m := &fakeMod{}
	c, err := LoadFrom(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	if m.Section.Key != "v" || !m.present {
		t.Fatalf("section: %+v", m)
	}
	if !slices.Contains(c.Unknown, "fake.extra") || slices.Contains(c.Unknown, "fake") || slices.Contains(c.Unknown, "fake.key") {
		t.Fatalf("unknown keys: %v", c.Unknown)
	}
	m.Section.Key = "w"
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	m2 := &fakeMod{}
	c2, err := LoadFrom(dir, m2)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Section.Key != "w" || c2.Theme != "x" {
		t.Fatalf("round trip: %+v theme %q", m2.Section, c2.Theme)
	}
}

// Unknown(k) of a module joins the unknown keys.
func TestModuleRefusedKey(t *testing.T) {
	c, err := LoadFrom(writeCfg(t, "[fake]\nkey = \"refused\"\n"), &fakeMod{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(c.Unknown, "fake.key") {
		t.Fatalf("unknown keys: %v", c.Unknown)
	}
}

// A new config.toml carries the block of each module.
func TestModuleTemplate(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadFrom(dir, &fakeMod{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil || !strings.Contains(string(b), "# fake network") {
		t.Fatalf("template: %v\n%s", err, b)
	}
}
