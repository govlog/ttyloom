package i18n

import (
	"os"
	"regexp"
	"testing"
	"testing/fstest"
)

// TestHelpKeys : each help topic has its three texts in both tables. The test
// lives here because help.go builds these keys by concatenation: TestCatalogs
// (cmd/ttyloom), which checks the literal keys, cannot see them.
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

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestRegister : a catalogue registered by a module joins the tables of
// every language, and the core texts stay.
func TestRegister(t *testing.T) {
	Register(fstest.MapFS{
		"en.toml": {Data: []byte(`zz_register_test = "from a module"`)},
		"fr.toml": {Data: []byte(`zz_register_test = "d'un module"`)},
	})
	defer Set("en")
	Set("en")
	if got := T("zz_register_test"); got != "from a module" {
		t.Fatalf("en: %q", got)
	}
	Set("fr")
	if got := T("zz_register_test"); got != "d'un module" {
		t.Fatalf("fr: %q", got)
	}
	if got := T("main_prefix"); got == "main_prefix" {
		t.Fatal("core table lost")
	}
}
