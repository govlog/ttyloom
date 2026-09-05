package spell

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Available : the .aff/.dic pairs of the folder, an orphan .aff does not count.
func TestAvailable(t *testing.T) {
	d := t.TempDir()
	for _, f := range []string{"fr_FR.aff", "fr_FR.dic", "en_US.aff", "en_US.dic", "de_DE.aff"} {
		os.WriteFile(filepath.Join(d, f), nil, 0o644)
	}
	if got := Available(d); !slices.Equal(got, []string{"en_US", "fr_FR"}) {
		t.Fatalf("Available: %v", got)
	}
}

// resolve : exact code, legacy alias, prefix; unknown = "".
func TestResolveDict(t *testing.T) {
	av := []string{"en_US", "fr_FR"}
	for _, c := range []struct{ in, want string }{
		{"fr_FR", "fr_FR"}, {"fr", "fr_FR"}, {"us", "en_US"}, {"en", "en_US"}, {"de", ""},
	} {
		if got := resolve(c.in, av); got != c.want {
			t.Fatalf("resolve(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
