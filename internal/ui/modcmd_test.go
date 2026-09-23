package ui

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

func init() {
	// The texts of the fake module, as a real module registers its catalogue.
	cat := `help_fk_name = "/fk <text>"
help_fk_short = "fake general command"
help_fk_long = "fake general command"
help_fkpoke_name = "/fkpoke"
help_fkpoke_short = "fake context command"
help_fkpoke_long = "fake context command"
help_section_fake = "Fake"
`
	i18n.Register(fstest.MapFS{"en.toml": {Data: []byte(cat)}, "fr.toml": {Data: []byte(cat)}})
}

// cmdMod : the fake module with a general command (/fk) and a context one
// (/fkpoke); ran records "name|net|text".
func cmdMod(nets ...string) (*UI, *fakeMod, *[]string) {
	u, m := modUI(nets...)
	u.netList = append(u.netList, nets...)
	ran := &[]string{}
	m.cmds = []module.Command{
		{Name: "fk", Help: module.Topic{Key: "help_fk", Section: "chats"},
			Complete: func(_ module.Host, _ module.Win, rest string) ([]string, string) {
				return []string{"alpha", "beta"}, rest
			},
			Run: func(h module.Host, w module.Win, _ []string, text string) {
				*ran = append(*ran, "fk||"+text)
				h.Print(w, "fk:"+text)
			}},
		{Name: "fkpoke", Context: true, Help: module.Topic{Key: "help_fkpoke", Section: "fake"},
			Run: func(h module.Host, w module.Win, _ []string, text string) {
				*ran = append(*ran, "fkpoke|"+h.ContextNet(w, "fake")+"|"+text)
			}},
	}
	return u, m, ran
}

// A general command of a module runs from anywhere; a context command only
// where one of its networks is meant, and asks which one otherwise.
func TestModuleCommands(t *testing.T) {
	u, _, ran := cmdMod("fake:a", "fake:b")
	w := u.ws.List[0]
	name, args, text, ok := ParseCommand("/fk hello", u.commandNames())
	if !ok || name != "fk" {
		t.Fatalf("parse: %q", name)
	}
	u.command(name, args, text)
	if len(*ran) != 1 || !strings.HasSuffix(w.Items[len(w.Items)-1].Sys, "fk:hello") {
		t.Fatalf("general: %v", *ran)
	}
	u.command("fkpoke", nil, "")
	if len(*ran) != 1 || !strings.Contains(w.Items[len(w.Items)-1].Sys, i18n.T("which_net", "fake")) {
		t.Fatalf("no context: %v", *ran)
	}
	if n, _, _, _ := ParseCommand("/fkp", u.commandNames()); n == "fkpoke" {
		t.Fatal("a context command resolves by prefix out of its context")
	}
	c := &model.Chat{Net: "fake:b", ID: 1, Title: "room"}
	u.chats, u.chatList = map[model.ChatKey]*model.Chat{c.Key(): c}, []*model.Chat{c}
	u.winFor(c)
	u.goTo(u.ws.ForChat(c.Key()))
	if n, _, _, _ := ParseCommand("/fkp x", u.commandNames()); n != "fkpoke" {
		t.Fatalf("prefix in context: %q", n)
	}
	u.command("fkpoke", []string{"x"}, "x")
	if (*ran)[len(*ran)-1] != "fkpoke|fake:b|x" {
		t.Fatalf("context: %v", *ran)
	}
}

// With no network of the module configured, its context commands are
// unknown, like the IRC ones were.
func TestModuleContextCommandWithoutNetwork(t *testing.T) {
	u, _, ran := cmdMod()
	if u.runModCommand(u.ws.List[0], "fkpoke", nil, "") || len(*ran) != 0 {
		t.Fatalf("ran with no network: %v", *ran)
	}
}

// Tab after a module command asks the module.
func TestModuleCompletion(t *testing.T) {
	u, _, _ := cmdMod("fake:a")
	u.ed.Set("/fk al")
	if got := u.candidates("al", false); !slices.Contains(got, "alpha") || slices.Contains(got, "beta") {
		t.Fatalf("candidates: %v", got)
	}
}

// /help lists the commands of the modules, in their sections.
func TestModuleHelp(t *testing.T) {
	u, _, _ := cmdMod("fake:a")
	var txt string
	for _, l := range helpLines(u.topics(), u.sections(), 100) {
		txt += render.LineText(l) + "\n"
	}
	if !strings.Contains(txt, "/fk <text>") || !strings.Contains(txt, "Fake") {
		t.Fatalf("help:\n%s", txt)
	}
	if !slices.Contains(helpCandidates(u.topics()), "fkpoke") {
		t.Fatal("help completion misses /fkpoke")
	}
}

// A module form: typing, a preset that fills another field (Pick), Submit
// with the values.
func TestModuleForm(t *testing.T) {
	u, _, _ := cmdMod()
	var got []string
	host{u}.OpenForm(&module.Form{Title: "t",
		Fields: []module.Field{{Label: "a", Sel: -1, Choices: []string{"one", "two"}}, {Label: "b", Sel: -1}},
		Pick:   func(f *module.Form, field, i int) { f.Fields[1].Value = "picked " + f.Fields[field].Value },
		Submit: func(v []string) string { got = v; return "" }})
	u.formKey(term.Key{Code: term.Right})
	u.formKey(term.Key{Code: term.Enter})
	if u.form != nil || !slices.Equal(got, []string{"one", "picked one"}) {
		t.Fatalf("form %v, values %v", u.form, got)
	}
}

// A module form draws its guide, folded to the box, and its link; Ctrl+O
// opens the link. At 60 columns nothing goes past the borders.
func TestFormIntroAndLink(t *testing.T) {
	u, _, _ := cmdMod()
	u.t.Cols, u.t.Rows = 60, 24
	var opened string
	host{u}.OpenForm(&module.Form{Title: "t",
		Intro:  []string{strings.Repeat("word ", 30)},
		Link:   "https://example.org",
		Fields: []module.Field{{Label: "a", Sel: -1}},
		Submit: func([]string) string { return "" }})
	u.form.openLink = func(s string) { opened = s }
	r := u.formRect()
	lines := u.form.Lines(u.th, r.w)
	var txt string
	for _, l := range lines {
		if w := render.Width(render.LineText(l)); w > r.w {
			t.Fatalf("line of %d columns in a box of %d: %q", w, r.w, render.LineText(l))
		}
		txt += render.LineText(l) + "\n"
	}
	if strings.Count(txt, "word") != 30 || !strings.Contains(txt, "https://example.org") || len(lines) != r.h {
		t.Fatalf("box (%d lines, rect %d):\n%s", len(lines), r.h, txt)
	}
	u.formKey(term.Key{Code: term.Ctrl, Rune: 'o'})
	if opened != "https://example.org" || u.form == nil {
		t.Fatalf("Ctrl+O: %q, form %v", opened, u.form)
	}
}
