package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
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
		{"/discord l", complNetCmd, "l", ""},
		{"/telegram ", complNetCmd, "", ""},
		{"/log o", complLog, "o", ""},
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

// TestChatCandidates : Tab on a chat name matches the start of any word of the
// title, not only its first, and "@" before a username is understood — the
// two ways /query and /join are typed.
func TestChatCandidates(t *testing.T) {
	u := &UI{cfg: &config.Config{}, chatList: []*model.Chat{
		{Net: "telegram", ID: 1, Title: "Les copains du foot"},
		{Net: "telegram", ID: 2, Title: "Go", Username: "golang"},
	}}
	try := func(line, want string) {
		t.Helper()
		u.ed.Set(line)
		u.ed.Complete(u.candidates)
		if got := u.ed.String(); got != want {
			t.Fatalf("%q → %q, want %q", line, got, want)
		}
	}
	try("/query cop", "/query copains du foot ")
	try("/q les", "/q les copains du foot ")
	try("/join @gol", "/join @golang ")
	try("/msg gola", "/msg golang ")
}

// TestCompleteListsWhenStuck : with several candidates and nothing more to
// add, Complete gives them back so that the UI can list them — a second Tab
// on "/q Jean Du" says Dupont and Durand instead of staying mute.
func TestCompleteListsWhenStuck(t *testing.T) {
	u := &UI{cfg: &config.Config{}, chatList: []*model.Chat{
		{Net: "telegram", ID: 1, Title: "Jean Dupont"}, {Net: "telegram", ID: 2, Title: "Jean Durand"}}}
	u.ed.Set("/q Je")
	if got := u.ed.Complete(u.candidates); u.ed.String() != "/q Jean Du" || got != nil {
		t.Fatalf("first Tab: %q, list %v", u.ed.String(), got)
	}
	if got := u.ed.Complete(u.candidates); len(got) != 2 || got[0] != "Dupont" || got[1] != "Durand" {
		t.Fatalf("second Tab: %q, list %v", u.ed.String(), got)
	}
}
