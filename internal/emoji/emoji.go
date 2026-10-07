// Package emoji holds the embedded Unicode data, the search and the recents.
package emoji

import (
	"cmp"
	_ "embed"
	"os"
	"slices"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/govlog/ttyloom/internal/config"
)

//go:embed emoji.txt
var data string

// aliasData : the gemoji shortcodes, "emoji<TAB>alias alias…" per line.
//
//go:embed aliases.txt
var aliasData string

// Emoji : one entry of the Unicode table (emoji-test.txt, fully-qualified).
type Emoji struct {
	Char  string
	Name  string
	Group string
	// Codes : the shortcodes of the ":" completion, without the colons — the
	// gemoji ones first (":+1:", ":tada:"), then the one made from Name.
	Codes []string
}

var (
	once sync.Once
	all  []Emoji
)

func load() {
	aliases := map[string][]string{}
	for _, l := range strings.Split(strings.TrimRight(aliasData, "\n"), "\n") {
		if c, a, ok := strings.Cut(l, "\t"); ok {
			aliases[Base(c)] = strings.Fields(a)
		}
	}
	lines := strings.Split(strings.TrimRight(data, "\n"), "\n")
	all = make([]Emoji, 0, len(lines))
	for _, l := range lines {
		f := strings.Split(l, "\t")
		if len(f) != 3 {
			continue
		}
		codes := aliases[Base(f[0])]
		if c := code(f[1]); c != "" && !slices.Contains(codes, c) {
			codes = append(slices.Clip(codes), c)
		}
		all = append(all, Emoji{Char: f[0], Name: f[1], Group: f[2], Codes: codes})
	}
}

// code : the shortcode made from a Unicode name — lower case, no accent, "_"
// between the words ("flag: Côte d’Ivoire" → flag_cote_d_ivoire).
func code(name string) string {
	var b strings.Builder
	gap := false
	for _, r := range norm.NFD.String(strings.ToLower(name)) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if gap && b.Len() > 0 {
				b.WriteByte('_')
			}
			gap = false
			b.WriteRune(r)
		default:
			gap = true
		}
	}
	return b.String()
}

// Hit : one answer of Lookup — the emoji and its shortcode that matched.
type Hit struct {
	Char  string
	Code  string
	Exact bool // Code is the whole query
}

// Lookup gives the emojis with a shortcode that holds q (lower case, folded):
// an exact code first, then a code that starts with q, then a word of a code
// (after a "_"). Inside a rank the recents lead, newest first, then the order
// of the table.
// ponytail: a scan of the table per key (~2k entries, ~3 codes each), well
// under a millisecond; an index if it ever shows up in a profile.
func Lookup(q string, recent []string) []Hit {
	if q == "" {
		return nil
	}
	type scored struct {
		Hit
		rank, rec int
	}
	var out []scored
	for _, e := range All() {
		rank, best := 3, ""
		for _, c := range e.Codes {
			r := 3
			switch {
			case c == q:
				r = 0
			case strings.HasPrefix(c, q):
				r = 1
			case strings.Contains(c, "_"+q):
				r = 2
			}
			if r < rank {
				rank, best = r, c
			}
		}
		if rank == 3 {
			continue
		}
		rec := slices.Index(recent, e.Char)
		if rec < 0 {
			rec = len(recent)
		}
		out = append(out, scored{Hit{Char: e.Char, Code: best, Exact: rank == 0}, rank, rec})
	}
	slices.SortStableFunc(out, func(a, b scored) int { return cmp.Or(a.rank-b.rank, a.rec-b.rec) })
	if len(out) == 0 {
		return nil
	}
	hits := make([]Hit, len(out))
	for i, s := range out {
		hits[i] = s.Hit
	}
	return hits
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
