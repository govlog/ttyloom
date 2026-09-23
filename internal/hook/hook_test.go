package hook

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestLoad : a missing file is no hook and no error; a file that is not TOML
// is an error; an unknown top-level key and each table with a mistake give one
// error naming them, in file order, and the other tables load.
func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.toml")
	if hs, bad, err := Load(path); hs != nil || bad != nil || err != nil {
		t.Fatalf("missing file: %v %v %v", hs, bad, err)
	}
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("[[hook]\nname =")
	if hs, _, err := Load(path); hs != nil || err == nil {
		t.Fatalf("not TOML: %v %v", hs, err)
	}
	write(`
[[hook]]
name    = "full"
net     = "x:y"
chats   = ["#Go"]
kinds   = ["group", "private"]
from    = ["alice", 42]
match   = '^!m (?P<city>\w+)$'
mention = true
cmd     = "~/bin/m.sh  --flag"
timeout = "15s"
reply   = "send"

[[hook]]
cmd = "true"

[[hook]]
name = "two words"
cmd  = "true"

[[hook]]
name = "full"
cmd  = "true"

[[hook]]
name = "nocmd"

[[hook]]
name    = "typo"
cmd     = "true"
mentoin = true

[[hook]]
name  = "badtype"
cmd   = "true"
chats = "#go"

[[hook]]
name  = "badre"
cmd   = "true"
match = "("

[[hook]]
name  = "badkind"
cmd   = "true"
kinds = ["room"]

[[hook]]
name = "badfrom"
cmd  = "true"
from = [1.5]

[[hook]]
name    = "slow"
cmd     = "true"
timeout = "2m"

[[hook]]
name    = "zero"
cmd     = "true"
timeout = "0s"

[[hook]]
name  = "badreply"
cmd   = "true"
reply = "shout"

[[hook]]
name = "plain"
cmd  = "true"

[[hoook]]
name = "lost"
cmd  = "true"
`)
	hs, bad, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"hoook", "#2", "two words", "full", "nocmd", "typo", "badtype", "badre", "badkind",
		"badfrom", "slow", "zero", "badreply"}
	if len(bad) != len(want) {
		t.Fatalf("%d left out, want %d: %v", len(bad), len(want), bad)
	}
	for i, w := range want {
		if !strings.Contains(bad[i].Error(), w) {
			t.Errorf("left out %d: %q does not name %q", i, bad[i], w)
		}
	}
	if !strings.Contains(bad[5].Error(), "mentoin") {
		t.Errorf("the unknown key is not named: %q", bad[5])
	}
	if len(hs) != 2 || hs[0].Name != "full" || hs[1].Name != "plain" {
		t.Fatalf("loaded: %+v", hs)
	}
	home, _ := os.UserHomeDir()
	f := hs[0]
	if f.Net != "x:y" || !slices.Equal(f.Chats, []string{"#Go"}) || !slices.Equal(f.Kinds, []string{KindGroup, KindPrivate}) ||
		!slices.Equal(f.From, []string{"alice", "42"}) || f.Match == nil || f.Match.String() != `^!m (?P<city>\w+)$` ||
		!f.Mention || !slices.Equal(f.Argv, []string{filepath.Join(home, "bin/m.sh"), "--flag"}) ||
		f.Timeout != 15*time.Second || f.Reply != ReplySend {
		t.Errorf("full: %+v", f)
	}
	if p := hs[1]; p.Match != nil || p.Timeout != 10*time.Second || p.Reply != ReplyNone || !slices.Equal(p.Argv, []string{"true"}) {
		t.Errorf("defaults: %+v", p)
	}
}
