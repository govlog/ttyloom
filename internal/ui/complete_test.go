package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
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
		{"/qu ant", complChats, "ant", ""},
		{"/quer ant", complChats, "ant", ""},
		{"/m ", complChats, "", ""},
		{" ", complChats, "", ""},
		{"hello ", complChats, "", ""},
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

func TestTabCommandCycle(t *testing.T) {
	u := listUI()
	u.ed.Set("/ne")
	for _, want := range []string{"/net", "/new", "/net"} {
		u.key(term.Key{Code: term.Tab})
		if u.ed.String() != want {
			t.Fatalf("Tab = %q, want %q", u.ed.String(), want)
		}
	}
	// Editing ends the cycle; the next Tab completes the network argument.
	u.nets = map[string]model.Backend{model.NetTelegram: &fakeBackend{}}
	u.key(term.Key{Rune: ' '})
	u.key(term.Key{Rune: 't'})
	u.key(term.Key{Code: term.Tab})
	if u.ed.String() != "/net telegram " {
		t.Fatalf("argument completion: %q", u.ed.String())
	}
	// A cursor move also ends the cycle, even if it is moved back.
	u.ed.Set("/ne")
	u.key(term.Key{Code: term.Tab})
	u.key(term.Key{Code: term.Left})
	u.key(term.Key{Code: term.Right})
	u.key(term.Key{Code: term.Tab})
	if u.ed.String() != "/net " {
		t.Fatalf("stale cycle after moving the cursor: %q", u.ed.String())
	}
	u.ed.Set("/se blop")
	u.ed.cur = 3
	u.key(term.Key{Code: term.Tab})
	u.key(term.Key{Code: term.Tab})
	if u.ed.String() != "/send blop" {
		t.Fatalf("command cycle lost suffix: %q", u.ed.String())
	}
}

func TestEmptyChatCompletionExpandsOnlineFirst(t *testing.T) {
	for _, command := range []string{"/m ", "/q ", "/j "} {
		t.Run(command, func(t *testing.T) {
			u := listUI()
			u.presence = map[model.ChatKey]string{}
			for i := 0; i < 35; i++ {
				u.chatList = append(u.chatList, &model.Chat{Net: model.NetTelegram, ID: int64(i + 1),
					Title: fmt.Sprintf("contact%02d", i), Username: fmt.Sprintf("contact%02d", i)})
			}
			u.presence[u.chatList[34].Key()] = i18n.T("presence_online")
			original := slices.Clone(u.chatList)
			u.ed.Set(command)
			u.key(term.Key{Code: term.Tab})
			short := lastSys(u.view())
			if u.ed.String() != command || !strings.HasPrefix(short, i18n.T("complete_choices", "@contact34")) ||
				!strings.Contains(short, "…") || strings.Contains(short, "@contact25") {
				t.Fatalf("first Tab: input %q, list %q", u.ed.String(), short)
			}
			u.key(term.Key{Code: term.Tab})
			long := lastSys(u.view())
			if !strings.Contains(long, "@contact25") || strings.Contains(long, "…") || u.ed.String() != command {
				t.Fatalf("second Tab: input %q, list %q", u.ed.String(), long)
			}
			if !slices.Equal(u.chatList, original) {
				t.Fatal("completion reordered the source chat list")
			}
			u.key(term.Key{Code: term.Esc})
			u.key(term.Key{Code: term.Tab})
			if got := lastSys(u.view()); got != short {
				t.Fatalf("Esc should reset the short list: %q", got)
			}
		})
	}
	// Even one known contact requires a letter before Tab inserts its name.
	u := listUI()
	u.chatList = []*model.Chat{{Title: "Alice", Username: "alice"}}
	u.ed.Set("/m ")
	u.key(term.Key{Code: term.Tab})
	if u.ed.String() != "/m " {
		t.Fatal("empty target was filled without listing")
	}
	u.key(term.Key{Rune: 'a'})
	u.key(term.Key{Code: term.Tab})
	if u.ed.String() != "/m alice " {
		t.Fatalf("duplicate username/title prevented completion: %q", u.ed.String())
	}
}

func TestCommandPrefixesOnSubmit(t *testing.T) {
	for input, want := range map[string]string{
		"/qu blop": "query", "/quer blop": "query", "/Q blop": "query", "/qui": "quit",
		"/ne": "ne", "/se": "se", "/win 2": "window", "/42": "42", "/blah": "blah", "/": "",
	} {
		name, _, _, _ := ParseCommand(input)
		if name != want {
			t.Errorf("%s resolved to %q, want %q", input, name, want)
		}
	}
	for _, name := range commandNames {
		if got, _, _, _ := ParseCommand(name); got != name[1:] {
			t.Errorf("exact command %s resolved to %s", name, got)
		}
	}
}
