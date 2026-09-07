package ui

import (
	"os"
	"path/filepath"
	"testing"
)

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

// TestCompletePath : Tab after /send completes a path of the disk — common
// prefix of the files, a directory with its "/" and no space so that the next
// Tab goes on inside it, one file with the space of the caption, "~" kept as
// typed, and a space inside the path (splitSendArgs allows it) handled.
func TestCompletePath(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"photo1.jpg", "photo2.jpg", "my doc.pdf"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o600)
	}
	os.Mkdir(filepath.Join(dir, "docs"), 0o700)
	u := &UI{}
	try := func(line, want string) {
		t.Helper()
		u.ed.Set(line)
		u.ed.Complete(u.candidates)
		if got := u.ed.String(); got != want {
			t.Fatalf("%q → %q, want %q", line, got, want)
		}
	}
	try("/send "+dir+"/ph", "/send "+dir+"/photo")
	try("/send "+dir+"/d", "/send "+dir+"/docs/")
	try("/send "+dir+"/photo1", "/send "+dir+"/photo1.jpg ")
	try("/send "+dir+"/my d", "/send "+dir+"/my doc.pdf ")
	t.Setenv("HOME", dir)
	try("/send ~/ph", "/send ~/photo")
	try("/send "+dir+"/photo1.jpg hello", "/send "+dir+"/photo1.jpg hello") // the caption is free text
}
