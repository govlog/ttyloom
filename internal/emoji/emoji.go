// Package emoji holds the embedded Unicode data, the search and the recents.
package emoji

import (
	_ "embed"
	"os"
	"strings"
	"sync"

	"github.com/govlog/ttyloom/internal/config"
)

//go:embed emoji.txt
var data string

// Emoji : one entry of the Unicode table (emoji-test.txt, fully-qualified).
type Emoji struct {
	Char  string
	Name  string
	Group string
}

var (
	once sync.Once
	all  []Emoji
)

func load() {
	lines := strings.Split(strings.TrimRight(data, "\n"), "\n")
	all = make([]Emoji, 0, len(lines))
	for _, l := range lines {
		f := strings.Split(l, "\t")
		if len(f) != 3 {
			continue
		}
		all = append(all, Emoji{Char: f[0], Name: f[1], Group: f[2]})
	}
}

// All gives every entry, in file order.
func All() []Emoji {
	once.Do(load)
	return all
}

// Search : substring on Name, case insensitive. q == "" gives All().
func Search(q string) []Emoji {
	all := All()
	if q == "" {
		return all
	}
	q = strings.ToLower(q)
	var out []Emoji
	for _, e := range all {
		if strings.Contains(strings.ToLower(e.Name), q) {
			out = append(out, e)
		}
	}
	return out
}

// Base : the form sent to Telegram — without the U+FE0F variation selector.
// The Unicode table is fully-qualified (❤️), the reactions of the server are
// not (❤): comparing the two needs this normalisation.
func Base(s string) string { return strings.ReplaceAll(s, "\ufe0f", "") }

const recentMax = 24

// Recent gives the last emojis used, the newest first.
// File errors are ignored (it gives nil).
func Recent(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

// AddRecent puts char first in the recents (no duplicate), cut to 24.
// File errors are ignored.
func AddRecent(path, char string) {
	cur := Recent(path)
	out := make([]string, 0, recentMax)
	out = append(out, char)
	for _, c := range cur {
		if c != char {
			out = append(out, c)
		}
	}
	if len(out) > recentMax {
		out = out[:recentMax]
	}
	_ = config.WriteAtomic(path, []byte(strings.Join(out, "\n")+"\n"), 0o600)
}
