// Package i18n holds the user-visible texts of ttyloom, in French and in
// English. It is a leaf package: every other package can import it.
package i18n

import (
	"embed"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

//go:embed *.toml
var files embed.FS

var (
	mu     sync.RWMutex
	cur    = "en"
	tables = []map[string]string{load("en")}
)

// load reads one embedded table. A broken file gives an empty table: T then
// returns the keys themselves, which is still readable.
func load(lang string) map[string]string {
	b, err := files.ReadFile(lang + ".toml")
	if err != nil {
		return map[string]string{}
	}
	m := map[string]string{}
	if err := toml.Unmarshal(b, &m); err != nil {
		return map[string]string{}
	}
	return m
}

// Langs gives the embedded languages, sorted ("en", "fr", …).
func Langs() []string {
	es, _ := files.ReadDir(".")
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, strings.TrimSuffix(e.Name(), ".toml"))
	}
	sort.Strings(out)
	return out
}

// Set selects the language chain ("fr", "fr+en"). An unknown name is
// dropped; nothing left gives English. English is always the last resort.
func Set(lang string) {
	var ts []map[string]string
	var kept []string
	for _, l := range strings.Split(lang, "+") {
		if t := load(l); len(t) > 0 {
			ts, kept = append(ts, t), append(kept, l)
		}
	}
	if len(kept) == 0 {
		ts, kept = []map[string]string{load("en")}, []string{"en"}
	}
	mu.Lock()
	cur, tables = strings.Join(kept, "+"), append(ts, load("en"))
	mu.Unlock()
}

// Lang gives the language in use.
func Lang() string {
	mu.RLock()
	defer mu.RUnlock()
	return cur
}

// T gives the text of a key, taken from the first table of the chain that
// has it. With arguments the text is a format string. An unknown key comes
// back as the key itself: no panic, and the miss is visible.
func T(key string, args ...any) string {
	mu.RLock()
	var s string
	var ok bool
	for _, t := range tables {
		if s, ok = t[key]; ok {
			break
		}
	}
	mu.RUnlock()
	if !ok {
		return key
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// Detect reads a locale value ($LC_ALL, else $LANG) and gives the language.
func Detect(env string) string {
	if strings.HasPrefix(env, "fr") {
		return "fr"
	}
	return "en"
}

// Plural returns base+"_one" or base+"_many" for a count. English treats 0
// as plural ("0 results"), French keeps the singular ("0 résultat"). The
// first language of the chain decides.
func Plural(n int, base string) string {
	if n > 1 || (n == 0 && strings.Split(Lang(), "+")[0] == "en") {
		return base + "_many"
	}
	return base + "_one"
}

// LocalTime gives the local time, in the layout of the language in use.
func LocalTime(t time.Time) string { return t.Local().Format(T("datetime_layout")) }
