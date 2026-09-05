package spell

// Dictionary discovery: pure, no cgo, so both build modes (with and without
// hunspell) get it — the UI needs Available for the /set spell completion.

import (
	"os"
	"slices"
	"strings"
)

// DictDir : where Debian/Ubuntu keep the hunspell dictionaries; no other
// layout supported.
const DictDir = "/usr/share/hunspell"

// Available gives the dictionary codes of dir: every base name that has both
// its .aff and its .dic, sorted.
func Available(dir string) []string {
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	aff := map[string]bool{}
	for _, e := range es {
		if n, ok := strings.CutSuffix(e.Name(), ".aff"); ok {
			aff[n] = true
		}
	}
	var out []string
	for _, e := range es {
		if n, ok := strings.CutSuffix(e.Name(), ".dic"); ok && aff[n] {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// resolve maps a /set spell code onto an available dictionary: exact name,
// then the legacy "us" alias, then the first "xx_" prefix match.
func resolve(code string, av []string) string {
	if slices.Contains(av, code) {
		return code
	}
	if code == "us" {
		if slices.Contains(av, "en_US") { // legacy "us" is American, not en_AU
			return "en_US"
		}
		code = "en"
	}
	for _, a := range av {
		if strings.HasPrefix(a, code+"_") {
			return a
		}
	}
	return ""
}
