package i18n

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestI18nComplete : fr.toml and en.toml carry the same keys, and every
// literal key given to i18n.T is in both.
func TestI18nComplete(t *testing.T) {
	fr, en := load("fr"), load("en")
	if len(fr) == 0 || len(en) == 0 {
		t.Fatalf("empty table: fr=%d en=%d", len(fr), len(en))
	}
	for k := range fr {
		if _, ok := en[k]; !ok {
			t.Errorf("key %q missing from en.toml", k)
		}
	}
	for k := range en {
		if _, ok := fr[k]; !ok {
			t.Errorf("key %q missing from fr.toml", k)
		}
	}
	callT := regexp.MustCompile(`i18n\.T\("([a-z0-9_]+)"[,)]`)
	n := 0
	for _, f := range GoFiles(t, "..", "../../cmd") {
		for _, m := range callT.FindAllStringSubmatch(read(t, f), -1) {
			n++
			if _, ok := fr[m[1]]; !ok {
				t.Errorf("%s: key %q missing from tables", f, m[1])
			}
		}
	}
	if n < 100 {
		t.Fatalf("%d i18n.T calls found: the scan missed files", n)
	}
}

// TestHelpKeys : each help topic has its three texts in both tables. The test
// lives here because help.go builds these keys by concatenation:
// TestI18nComplete cannot see them.
func TestHelpKeys(t *testing.T) {
	fr := load("fr")
	keys := regexp.MustCompile(`"(help_[a-z0-9_]+)", "`).FindAllStringSubmatch(read(t, "../ui/help.go"), -1)
	if len(keys) < 50 {
		t.Fatalf("%d topics found in help.go", len(keys))
	}
	for _, m := range keys {
		for _, suffix := range []string{"_name", "_short", "_long"} {
			if _, ok := fr[m[1]+suffix]; !ok {
				t.Errorf("key %q missing", m[1]+suffix)
			}
		}
	}
	for _, sec := range []string{"windows", "chats", "messages", "media", "sidebar", "input", "options"} {
		if _, ok := fr["help_section_"+sec]; !ok {
			t.Errorf("key %q missing", "help_section_"+sec)
		}
	}
}

func TestT(t *testing.T) {
	Set("fr")
	if got := T("no_such_key_at_all"); got != "no_such_key_at_all" {
		t.Errorf("missing key: %q", got)
	}
	if got := T("no_such_key_at_all", 1, 2); got != "no_such_key_at_all" {
		t.Errorf("missing key with arguments: %q", got)
	}
	if Lang() != "fr" {
		t.Errorf("Lang() = %q", Lang())
	}
	Set("de") // unknown language: English
	if Lang() != "en" {
		t.Errorf("Set(de) → %q", Lang())
	}
	for _, c := range []struct{ env, want string }{
		{"fr_FR.UTF-8", "fr"}, {"fr", "fr"}, {"en_US.UTF-8", "en"}, {"", "en"}, {"C", "en"},
	} {
		if got := Detect(c.env); got != c.want {
			t.Errorf("Detect(%q) = %q, want %q", c.env, got, c.want)
		}
	}
}

// Language chain: key taken from the first table that has it, then en,
// then the key itself. Langs lists the embedded toml files.
func TestLangChain(t *testing.T) {
	defer Set("en")
	if got := Langs(); len(got) < 2 || got[0] != "en" || got[1] != "fr" {
		t.Fatalf("Langs: %v", got)
	}
	Set("fr+en")
	if Lang() != "fr+en" {
		t.Fatalf("Lang: %q", Lang())
	}
	if T("loading") != "chargement…" { // fr key: the first table wins
		t.Fatalf("fr first: %q", T("loading"))
	}
	Set("nope+fr")
	if Lang() != "fr" || T("loading") != "chargement…" {
		t.Fatalf("unknown language ignored: %q %q", Lang(), T("loading"))
	}
	Set("nope")
	if Lang() != "en" {
		t.Fatalf("fallback en: %q", Lang())
	}
	Set("en+fr")
	if Plural(0, "res") != "res_many" { // the plural follows the first language
		t.Fatalf("plural: %q", Plural(0, "res"))
	}
}

// GoFiles gives the .go files, tests apart, of the given trees.
func GoFiles(t *testing.T, roots ...string) []string {
	t.Helper()
	var out []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			out = append(out, p)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
