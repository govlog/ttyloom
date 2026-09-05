package spell

import "testing"

func ranges(text string, quotes bool) []string {
	var out []string
	r := []rune(text)
	for _, w := range Words(text, quotes) {
		out = append(out, string(r[w.Start:w.End]))
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestWords(t *testing.T) {
	for _, c := range []struct {
		text   string
		quotes bool
		want   []string
	}{
		{"salut les amis", false, []string{"salut", "les", "amis"}},
		{"c'est peut-être ça", false, []string{"c'est", "peut-être", "ça"}},
		{"voir https://ex.org/x ok", false, []string{"voir", "ok"}},
		{"mail bob@ex.org ok", false, []string{"mail", "ok"}},
		{"yo @alice ça va", false, []string{"yo", "ça", "va"}},
		{"v2 et 42 fois x86", false, []string{"et", "fois"}},
		{"/win close", false, nil}, // command: nothing
		{"un\n> cité mal\ndeux", false, []string{"un", "deux"}},
		{"un\n> cité mal\ndeux", true, []string{"un", "cité", "mal", "deux"}},
		{"a\n```\ncode mot\n```\nb", false, []string{"a", "b"}},
		{"a\n```\ncode mot\n```\nb", true, []string{"a", "code", "mot", "b"}},
		{"fin d'une ligne’vraie", false, []string{"fin", "d'une", "ligne’vraie"}},
	} {
		if got := ranges(c.text, c.quotes); !eq(got, c.want) {
			t.Errorf("Words(%q, %v) = %v, want %v", c.text, c.quotes, got, c.want)
		}
	}
}
