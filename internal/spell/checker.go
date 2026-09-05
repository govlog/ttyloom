//go:build !nospell

package spell

import (
	"bufio"
	"errors"
	"os"
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
)

// Checker : one hunspell handle per language; a word is right when one of
// them knows it. The personal file (one word per line) is loaded with Add at
// opening time; AddPersist appends to it. Add() only lives in memory: the
// session Ignore uses it too.
type Checker struct {
	hs    []*hunhandle
	perso string          // path of the personal words file, "" = none
	cache map[string]bool // word -> right; wiped by Ignore/AddPersist
}

// New opens the dictionaries of mode ("fr", "us", "en_GB", "fr+us") and loads
// the personal file. Each language is resolved against the dictionaries found
// in DictDir; an unknown one is an error listing what is there.
func New(mode, perso string) (*Checker, error) {
	c := &Checker{perso: perso, cache: map[string]bool{}}
	av := Available(DictDir)
	if len(av) == 0 {
		return nil, errors.New(i18n.T("spell_dict_missing", DictDir))
	}
	for _, lang := range strings.Split(mode, "+") {
		n := resolve(lang, av)
		if n == "" {
			return nil, errors.New(i18n.T("spell_lang_unknown", lang, strings.Join(av, ", ")))
		}
		c.hs = append(c.hs, newHunspell(DictDir+"/"+n+".aff", DictDir+"/"+n+".dic"))
	}
	if perso != "" {
		f, err := os.Open(perso)
		if err == nil {
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				if w := strings.TrimSpace(sc.Text()); w != "" {
					c.add(w)
				}
			}
			f.Close()
		}
	}
	return c, nil
}

// norm : the typographic apostrophe goes to the straight one of the
// dictionaries.
func norm(w string) string { return strings.ReplaceAll(w, "’", "'") }

// Check tells whether word is right for one of the languages. A word with an
// elision (l'eau) is also tried after its last apostrophe.
func (c *Checker) Check(word string) bool {
	w := norm(word)
	if ok, hit := c.cache[w]; hit {
		return ok
	}
	ok := c.raw(w)
	if !ok {
		if i := strings.LastIndexByte(w, '\''); i >= 0 && i+1 < len(w) {
			ok = c.raw(w[i+1:])
		}
	}
	c.cache[w] = ok
	return ok
}

func (c *Checker) raw(w string) bool {
	for _, h := range c.hs {
		if h.Spell(w) {
			return true
		}
	}
	return false
}

// Suggest merges the suggestions of every language, the first language first,
// capped at 6.
func (c *Checker) Suggest(word string) []string {
	var out []string
	seen := map[string]bool{}
	for _, h := range c.hs {
		for _, s := range h.Suggest(norm(word)) {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
			if len(out) == 6 {
				return out
			}
		}
	}
	return out
}

func (c *Checker) add(word string) {
	w := norm(word)
	for _, h := range c.hs {
		h.Add(w)
	}
	c.cache[w] = true
}

// Ignore accepts word for the session only.
func (c *Checker) Ignore(word string) { c.add(word) }

// AddPersist accepts word and appends it to the personal file.
func (c *Checker) AddPersist(word string) error {
	c.add(word)
	if c.perso == "" {
		return nil
	}
	f, err := os.OpenFile(c.perso, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(norm(word) + "\n")
	return err
}
