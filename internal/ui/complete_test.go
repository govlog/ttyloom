package ui

import "testing"

func TestComplContext(t *testing.T) {
	cases := []struct {
		line string
		src  complSource
		tail string
		key  string
	}{
		{"/q", complCommands, "/q", ""},
		{"/q ant", complChats, "ant", ""},
		{"/query Antonio G", complChats, "Antonio G", ""},
		{"/msg bob salut", complNone, "", ""}, // 2nd argument of /msg = free text
		{"/win ", complWindows, "", ""},
		{"/window new h", complWindowNewArg, "h", ""},
		{"/set im", complSetKey, "im", ""},
		{"/set images ha", complSetValue, "ha", "images"},
		{"/theme Catppuccin M", complTheme, "Catppuccin M", ""},
		{"/net tel", complNet, "tel", ""},
		{"/open 2", complNone, "", ""},
		{"salut al", complChats, "al", ""},
	}
	for _, c := range cases {
		src, tail, key := complContext(c.line, len([]rune(c.line)))
		if src != c.src || tail != c.tail || key != c.key {
			t.Errorf("%q: got %v %q %q", c.line, src, tail, key)
		}
	}
}

func TestSetValueCandidates(t *testing.T) {
	if got := setValues("images"); len(got) != 4 || got[0] != "auto" {
		t.Fatalf("%v", got)
	}
	if got := setValues("timestamps"); len(got) != 2 {
		t.Fatalf("%v", got)
	}
	if got := setValues("bell"); len(got) != 2 {
		t.Fatalf("%v", got)
	}
	if got := setValues("download_dir"); got != nil {
		t.Fatalf("%v", got)
	}
}

// TestComplFold : Tab after /fold completes a section name.
func TestComplFold(t *testing.T) {
	src, tail, _ := complContext("/fold goph", len("/fold goph"))
	if src != complFold || tail != "goph" {
		t.Fatalf("got %v %q", src, tail)
	}
}
