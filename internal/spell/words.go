// Package spell: hunspell spell checking of the input line. Words gives the
// word ranges worth checking; the Checker (cgo, hunspell) says which are
// wrong. Built with -tags nospell, New only returns an error.
package spell

import (
	"strings"
	"unicode"
)

// Range : one word of the text, in runes.
type Range struct{ Start, End int }

// wordRune : letters make words; ' ’ - glue two letter runs together
// (c'est, peut-être), never start or end one.
func wordRune(r rune) bool { return unicode.IsLetter(r) }
func glueRune(r rune) bool { return r == '\'' || r == '’' || r == '-' }

// Words gives the ranges of the words to check. Skipped: the whole text when
// it is a /command; URLs, emails and @mentions (any space-delimited token
// holding ://, www., or @); tokens holding a digit; with quotes false, the
// lines quoted with "> " and the ``` fenced blocks.
func Words(text string, quotes bool) []Range {
	if strings.HasPrefix(text, "/") {
		return nil
	}
	var out []Range
	pos := 0
	fence := false
	for _, line := range strings.Split(text, "\n") {
		l := []rune(line)
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fence = !fence
			pos += len(l) + 1
			continue
		}
		if fence && !quotes || !quotes && strings.HasPrefix(line, ">") {
			pos += len(l) + 1
			continue
		}
		// Tokens split on spaces first: a URL or an @mention is skipped as a
		// whole, its inner letter runs included.
		i := 0
		for i < len(l) {
			if unicode.IsSpace(l[i]) {
				i++
				continue
			}
			j := i
			for j < len(l) && !unicode.IsSpace(l[j]) {
				j++
			}
			out = append(out, tokenWords(l, i, j, pos)...)
			i = j
		}
		pos += len(l) + 1
	}
	return out
}

// tokenWords gives the word ranges inside the token l[i:j], offset by pos.
func tokenWords(l []rune, i, j, pos int) []Range {
	tok := string(l[i:j])
	if strings.Contains(tok, "://") || strings.Contains(tok, "www.") || strings.Contains(tok, "@") {
		return nil
	}
	for _, r := range tok {
		if unicode.IsDigit(r) {
			return nil
		}
	}
	var out []Range
	for k := i; k < j; {
		if !wordRune(l[k]) {
			k++
			continue
		}
		e := k
		for e < j && (wordRune(l[e]) || glueRune(l[e]) && e+1 < j && wordRune(l[e+1])) {
			e++
		}
		out = append(out, Range{Start: pos + k, End: pos + e})
		k = e
	}
	return out
}
