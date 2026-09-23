package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/protocols/dsc"
	"github.com/govlog/ttyloom/protocols/irc"
	"github.com/govlog/ttyloom/protocols/tgc"
)

// Every catalogue — the core's and each module's — has the same keys in en
// and fr, no key is in two catalogues, and every literal key given to i18n.T
// in the code (tests apart) is in one of them.
func TestCatalogs(t *testing.T) {
	cats := map[string]fs.FS{"core": i18n.Core, "tgc": tgc.Catalog, "dsc": dsc.Catalog, "irc": irc.Catalog}
	owner := map[string]string{}
	for name, c := range cats {
		en, fr := i18n.Table(c, "en"), i18n.Table(c, "fr")
		if len(en) == 0 {
			t.Fatalf("%s: empty catalogue", name)
		}
		for k := range en {
			if _, ok := fr[k]; !ok {
				t.Errorf("%s: %q missing in fr", name, k)
			}
			if o, ok := owner[k]; ok {
				t.Errorf("%q in %s and %s", k, o, name)
			}
			owner[k] = name
		}
		for k := range fr {
			if _, ok := en[k]; !ok {
				t.Errorf("%s: %q missing in en", name, k)
			}
		}
	}
	call := regexp.MustCompile(`i18n\.T\("([a-z0-9_]+)"[,)]`)
	n := 0
	for _, p := range goFiles(t, "../..") {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range call.FindAllStringSubmatch(string(b), -1) {
			n++
			if _, ok := owner[m[1]]; !ok {
				t.Errorf("%s: key %q in no catalogue", p, m[1])
			}
		}
	}
	if n < 100 {
		t.Fatalf("%d i18n.T calls found: the scan missed files", n)
	}
}

// The core names no network: no string "telegram", "discord" or "irc" (alone
// or as "<name>:…"), no import of a protocol, in the Go files of internal/ —
// tests apart, and the exceptions below with their reason.
func TestCoreNamesNoNetwork(t *testing.T) {
	allowed := map[string]string{
		"internal/ui/alias.go": "legacyAliasNet: aliases.toml of before several networks",
	}
	name := regexp.MustCompile(`(?i)^"(telegram|discord|irc)(:.*)?"$`)
	files := goFiles(t, "../../internal")
	if len(files) < 50 {
		t.Fatalf("%d files: the scan missed the core", len(files))
	}
	for _, p := range files {
		rel, _ := filepath.Rel("../..", p)
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && name.MatchString(lit.Value) {
				if _, ok := allowed[rel]; !ok {
					t.Errorf("%s: network name %s in the core", rel, lit.Value)
				}
			}
			return true
		})
		for _, imp := range f.Imports {
			if strings.Contains(imp.Path.Value, "/protocols/") {
				t.Errorf("%s: imports %s", rel, imp.Path.Value)
			}
		}
	}
}

// goFiles : the Go files under dir, tests and hidden directories apart.
func goFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != dir && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
