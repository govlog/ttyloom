package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// frenchDirs : trees swept by TestNoHardcodedFrench — the whole repository.
// Every text shown goes through i18n.T; the tables live in internal/i18n/*.toml.
var frenchDirs = []string{"../.."}

// frenchWords : marks of French in a Go string literal. The accents first,
// then words that mean nothing in English. "message" is not among them: the
// word is written the same in both languages, and it flagged the English
// template of config.toml.
var frenchWords = []string{
	" le ", " la ", " les ", " des ", " une ", " pas ", " dans ", " pour ", " avec ",
	"aucun", "fenêtre", "conversation",
}

// frenchAllow : literals allowed, with their reason. Empty: none.
var frenchAllow = map[string]string{}

// TestNoHardcodedFrench : no string literal of the sources (tests apart)
// carries French. The accents and a handful of common words are enough to
// catch a regression: a text left hardcoded shows up here.
func TestNoHardcodedFrench(t *testing.T) {
	files := 0
	for _, dir := range frenchDirs {
		for _, path := range goFiles(t, dir) {
			files++
			for _, lit := range stringLits(t, path) {
				if _, ok := frenchAllow[lit]; ok {
					continue
				}
				if i := strings.IndexAny(lit, "éèêàçùôîûœâëï"); i >= 0 {
					t.Errorf("%s: accent in %q", path, lit)
					continue
				}
				low := strings.ToLower(lit)
				for _, w := range frenchWords {
					if strings.Contains(low, w) {
						t.Errorf("%s: French word %q in %q", path, w, lit)
						break
					}
				}
			}
		}
	}
	if files < 40 {
		t.Fatalf("%d files scanned: the scan missed something", files)
	}
}

// goFiles gives the .go files, tests apart, of the tree dir.
func goFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// stringLits gives the string literals of a Go file, decoded.
func stringLits(t *testing.T, path string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		if l, ok := n.(*ast.BasicLit); ok && l.Kind == token.STRING {
			if s, err := strconv.Unquote(l.Value); err == nil {
				out = append(out, s)
			}
		}
		return true
	})
	return out
}

// TestCleanLineOnSingleLineSites : render.Clean keeps '\n' by construction
// (Wrap needs it for the body of a message). Everything the interface draws
// on a single line — sidebar, /chats, input prompt, status bar, reaction
// picker, log — must go through render.CleanLine: a remote '\n' written after
// an absolute cursor move scrolls the screen and destroys the frame at every
// repaint. Only the sites that drop the breaks themselves, and the paste (a
// real multiline text), may still call render.Clean.
func TestCleanLineOnSingleLineSites(t *testing.T) {
	n := 0
	// paste.go: a paste is multiline by nature. select.go: the copy joins
	// several messages with '\n' and goes out in base64, never to the screen.
	multiline := []string{"paste.go", "select.go"}
	for _, path := range goFiles(t, ".") {
		if slices.ContainsFunc(multiline, func(f string) bool { return strings.HasSuffix(path, f) }) {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if !strings.Contains(line, "render.Clean(") {
				continue
			}
			n++
			if strings.Contains(line, `"\n"`) { // breaks dropped on the spot
				continue
			}
			t.Errorf("%s:%d: render.Clean in a single-line context: %s", path, i+1, strings.TrimSpace(line))
		}
	}
	if n == 0 {
		t.Fatal("no render.Clean call found: the scan missed the files")
	}
}
