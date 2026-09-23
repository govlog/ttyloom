package dsc

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
)

// loadDiscord : a config.toml written for the test, read with the Discord
// module; the token file lives in the same directory (TTYLOOM_DIR).
func loadDiscord(t *testing.T, body string) (*config.Config, *Module) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TTYLOOM_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewModule()
	cfg, err := config.LoadFrom(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, m
}

func TestDiscordToken(t *testing.T) {
	file := filepath.Join(t.TempDir(), "discord.token")
	d := &Settings{TokenCmd: "echo  abc "}
	got, err := d.Token(file)
	if err != nil || got != "abc" {
		t.Fatalf("token of %q: %q, %v", d.TokenCmd, got, err)
	}
	// Command that fails, command that prints nothing: an empty token would go
	// to the network as it is and come back as an obscure error.
	for _, cmd := range []string{"ttyloom-no-such-command-here", "true", "echo   "} {
		d := &Settings{TokenCmd: cmd}
		if tok, err := d.Token(file); err == nil {
			t.Errorf("token_cmd %q: no error, token %q", cmd, tok)
		}
	}
	// What the command said on stderr is in the error: "exit status 1" alone
	// hid a wrong path for days.
	d = &Settings{TokenCmd: "cat /ttyloom-no-such-file"}
	if _, err := d.Token(file); err == nil || !strings.Contains(err.Error(), "No such file") {
		t.Fatalf("stderr of token_cmd missing: %v", err)
	}
	// No command: the token file, or nothing at all (the QR login then).
	d = &Settings{}
	if tok, err := d.Token(file); err != nil || tok != "" {
		t.Fatalf("no file: %q, %v", tok, err)
	}
	os.WriteFile(file, []byte(" tok-1\n"), 0o600)
	if tok, err := d.Token(file); err != nil || tok != "tok-1" {
		t.Fatalf("file: %q, %v", tok, err)
	}
}

// [discord] with a token_cmd that works: one network, its cache, and a
// launch that gives a backend.
func TestModuleWithDiscord(t *testing.T) {
	_, m := loadDiscord(t, "api_id = 42\napi_hash = \"hash\"\n[discord]\ntoken_cmd = \"echo tok\"\n")
	if !slices.Equal(m.Networks(), []string{"discord"}) {
		t.Fatalf("networks: %v", m.Networks())
	}
	if sub, keep := m.Cache("discord"); sub != "discord" || keep {
		t.Fatalf("cache: %q %v", sub, keep)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if b, err := m.Launch(ctx, nil, "discord", make(chan model.Event, 8)); err != nil || b == nil {
		t.Fatalf("launch: %v %v", b, err)
	}
}

// No [discord]: no network.
func TestModuleNoDiscord(t *testing.T) {
	if _, m := loadDiscord(t, "api_id = 42\napi_hash = \"hash\"\n"); len(m.Networks()) != 0 {
		t.Fatalf("networks: %v", m.Networks())
	}
}

// [discord] with no token_cmd: the token file of the configuration directory
// is the source, and with no file either the launch still gives a backend —
// the one that logs in by QR and writes the file.
func TestModuleDiscordTokenFile(t *testing.T) {
	cfg, m := loadDiscord(t, "[discord]\n")
	if !slices.Equal(m.Networks(), []string{"discord"}) {
		t.Fatalf("networks %v", m.Networks())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if b, err := m.Launch(ctx, nil, "discord", make(chan model.Event, 8)); err != nil || b == nil {
		t.Fatalf("launch with no token: %v %v", b, err)
	}
	os.WriteFile(filepath.Join(filepath.Dir(cfg.Path()), "discord.token"), []byte("tok\n"), 0o600)
	if b, err := m.Launch(ctx, nil, "discord", make(chan model.Event, 8)); err != nil || b == nil {
		t.Fatalf("launch with the token file: %v %v", b, err)
	}
}

// A token_cmd that fails is the error of the launch, which starts nothing.
func TestModuleTokenError(t *testing.T) {
	_, m := loadDiscord(t, "[discord]\ntoken_cmd = \"false\"\n")
	if !slices.Equal(m.Networks(), []string{"discord"}) {
		t.Fatalf("networks: %v", m.Networks())
	}
	b, err := m.Launch(context.Background(), nil, "discord", make(chan model.Event, 8))
	if b != nil || err == nil || !strings.Contains(err.Error(), "token_cmd") {
		t.Fatalf("launch with a failing token_cmd: %v %v", b, err)
	}
}

// The token command runs once at Load — before the terminal goes raw, where a
// pinentry can still ask — and the first launch takes that answer; the next
// launch (/discord login) runs the command again.
func TestModuleTokenReadOnceAtLoad(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	script := filepath.Join(dir, "tok.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho x >> "+count+"\necho tok\n"), 0o700)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[discord]\ntoken_cmd = \""+script+"\"\n"), 0o600)
	t.Setenv("TTYLOOM_DIR", dir)
	m := NewModule()
	if _, err := config.LoadFrom(dir, m); err != nil {
		t.Fatal(err)
	}
	runs := func() int { b, _ := os.ReadFile(count); return strings.Count(string(b), "x") }
	if runs() != 1 {
		t.Fatalf("runs at Load: %d", runs())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i, want := range []int{1, 2} {
		if _, err := m.Launch(ctx, nil, "discord", make(chan model.Event, 8)); err != nil {
			t.Fatal(err)
		}
		if runs() != want {
			t.Fatalf("launch %d: %d runs", i+1, runs())
		}
	}
}
