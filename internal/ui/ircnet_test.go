package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
)

// typeKeys feeds a string to the form as plain keys.
func typeKeys(u *UI, s string) {
	for _, r := range s {
		u.formKey(term.Key{Rune: r})
	}
}

// The form: typing lands in the current field, Tab and Up move, a secret
// field draws dots, a refused submit keeps the box with the error, Esc
// closes it.
func TestFormOverlay(t *testing.T) {
	u := listUI()
	var got []string
	u.form = &formBox{title: "t", fields: []formField{{label: "a"}, {label: "b", secret: true}},
		submit: func(v []string) string {
			got = v
			if v[0] == "" {
				return "a required"
			}
			return ""
		}}
	u.formKey(term.Key{Code: term.Enter})
	if u.form == nil || u.form.err != "a required" || len(got) != 2 {
		t.Fatalf("refused submit must keep the form: %+v", u.form)
	}
	typeKeys(u, "x")
	u.formKey(term.Key{Code: term.Tab})
	typeKeys(u, "pw")
	if u.form.cur != 1 {
		t.Fatalf("Tab: field %d", u.form.cur)
	}
	lines := u.form.Lines(u.th, formW)
	txt := ""
	for _, l := range lines {
		for _, s := range l.Spans {
			txt += s.Text
		}
		txt += "\n"
	}
	if !strings.Contains(txt, "••") || strings.Contains(txt, "pw") || !strings.Contains(txt, "a required") {
		t.Fatalf("drawn:\n%s", txt)
	}
	u.formKey(term.Key{Code: term.Up})
	u.formKey(term.Key{Code: term.Backspace})
	typeKeys(u, "y")
	u.formKey(term.Key{Code: term.Enter})
	if u.form != nil || got[0] != "y" || got[1] != "pw" {
		t.Fatalf("submit: form %v, values %v", u.form, got)
	}
	u.form = &formBox{fields: []formField{{label: "a"}}, submit: func([]string) string { return "" }}
	u.formKey(term.Key{Code: term.Esc})
	if u.form != nil {
		t.Fatal("Esc must close the form")
	}
}

// /irc add: the form, then a valid submit writes the [[irc]] table, lists
// the network and launches it; a taken name or a bad port is refused.
func TestIRCAdd(t *testing.T) {
	u, n := launchUI(nil)
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u.cfg = cfg
	u.command("irc", []string{"add"}, "add")
	if u.form == nil || len(u.form.fields) != 8 || string(u.form.fields[2].val) != "6697" {
		t.Fatalf("form: %+v", u.form)
	}
	vals := []string{"Libera", "irc.libera.chat", "abc", "yes", "me", "", "", "pw"}
	if e := u.ircAddSubmit(vals); e != i18n.T("irc_bad_port") {
		t.Fatalf("bad port: %q", e)
	}
	vals[2] = ""
	if e := u.ircAddSubmit(vals); e != "" {
		t.Fatalf("submit: %q", e)
	}
	if got := cfg.IRCByName("libera"); got == nil || got.Port != 6697 || !got.TLS || got.NickServPassword != "pw" || got.Nick != "me" {
		t.Fatalf("table: %+v", got)
	}
	again, _ := config.LoadFrom(filepath.Dir(cfg.Path()))
	if again.IRCByName("libera") == nil {
		t.Fatal("table not written")
	}
	if *n != 1 || u.nets["irc:libera"] == nil || u.netList[0] != model.NetDiscord || u.netList[1] != "irc:libera" {
		t.Fatalf("launched %d, nets %v, list %v", *n, u.nets, u.netList)
	}
	if e := u.ircAddSubmit(vals); e != i18n.T("irc_name_taken", "libera") {
		t.Fatalf("taken name: %q", e)
	}
	w := u.ws.List[0]
	u.command("irc", nil, "")
	if last := w.Items[len(w.Items)-1].Sys; !strings.Contains(last, "irc:libera") {
		t.Fatalf("status: %q", last)
	}
}

// /irc disconnect <name> cancels the network; /irc connect starts it again;
// an unknown name is refused.
func TestIRCConnectDisconnect(t *testing.T) {
	u, n := launchUI(nil)
	u.netList = append(u.netList, "irc:oftc")
	u.command("irc", []string{"connect", "oftc"}, "connect oftc")
	if *n != 1 || u.nets["irc:oftc"] == nil {
		t.Fatalf("connect: %d %v", *n, u.nets)
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.netCancel["irc:oftc"] = cancel
	u.command("irc", []string{"disconnect", "oftc"}, "disconnect oftc")
	if ctx.Err() == nil {
		t.Fatal("disconnect must cancel the network")
	}
	w := u.ws.List[0]
	u.command("irc", []string{"connect", "nope"}, "connect nope")
	if last := w.Items[len(w.Items)-1].Sys; !strings.Contains(last, "nope") || *n != 1 {
		t.Fatalf("unknown name: %q, launched %d", last, *n)
	}
}

// logoutBackend records whether Logout was asked (from a goroutine of
// stopNet, hence the atomic).
type logoutBackend struct {
	fakeBackend
	logouts atomic.Int32
}

func (b *logoutBackend) Logout(context.Context) error { b.logouts.Add(1); return nil }

// /discord disconnect cancels the context without the server logout;
// /discord logout asks the server first.
func TestNetDisconnectKeepsSession(t *testing.T) {
	u, _ := launchUI(nil)
	b := &logoutBackend{}
	u.launch = func(context.Context, string) (model.Backend, error) { return b, nil }
	u.command("discord", []string{"login"}, "login")
	ctx, cancel := context.WithCancel(context.Background())
	u.netCancel[model.NetDiscord] = cancel
	u.command("discord", []string{"disconnect"}, "disconnect")
	if ctx.Err() == nil || b.logouts.Load() != 0 {
		t.Fatalf("disconnect: cancelled %v, logouts %d", ctx.Err() != nil, b.logouts.Load())
	}
	u.command("discord", []string{"logout"}, "logout")
	time.Sleep(20 * time.Millisecond) // the logout runs in a goroutine
	if b.logouts.Load() != 1 {
		t.Fatalf("logout: %d calls", b.logouts.Load())
	}
}

// resolversFor: a "#room" from a bare window goes to the IRC networks only;
// a window bound to a chat asks its own network; a nick from a bare window
// asks every resolving network.
func TestResolversFor(t *testing.T) {
	tg := &queryBackend{fakeBackend: fakeBackend{caps: model.Caps{Resolve: true}}}
	lib := &queryBackend{fakeBackend: fakeBackend{caps: model.Caps{Resolve: true}}}
	oftc := &queryBackend{fakeBackend: fakeBackend{caps: model.Caps{Resolve: true}}}
	u := netUI()
	u.nets = map[string]model.Backend{model.NetTelegram: tg, "irc:libera": lib, "irc:oftc": oftc}
	if got := u.resolversFor(&Window{}, "#go"); len(got) != 2 {
		t.Fatalf("#room: %d resolvers", len(got))
	}
	if got := u.resolversFor(&Window{}, "alice"); len(got) != 3 {
		t.Fatalf("nick: %d resolvers", len(got))
	}
	w := &Window{Chat: &model.Chat{Net: "irc:oftc", ID: 1}}
	if got := u.resolversFor(w, "#go"); len(got) != 1 || got[0] != model.Backend(oftc) {
		t.Fatalf("bound window: %v", got)
	}
}

// /dcc get takes the last incoming file of the window and downloads it;
// with nothing waiting it says so. /dcc send from a window of the network
// sends to the known chat of the nick.
func TestDCCCommands(t *testing.T) {
	u := netUI("irc:libera")
	u.ctx = context.Background()
	u.netList = []string{"irc:libera"}
	u.cfg.DownloadDir = t.TempDir()
	b := u.nets["irc:libera"].(*fakeBackend)
	w := u.ws.List[0]
	u.command("dcc", []string{"get"}, "get")
	if last := w.Items[len(w.Items)-1].Sys; !strings.Contains(last, i18n.T("dcc_no_offer", "")) {
		t.Fatalf("no offer: %q", last)
	}
	bob := &model.Chat{Net: "irc:libera", ID: 9, Kind: model.ChatUser, Title: "bob"}
	u.chats = map[model.ChatKey]*model.Chat{bob.Key(): bob}
	u.chatList = append(u.chatList, bob)
	win := &Window{Chat: bob}
	u.ws.List = append(u.ws.List, win)
	win.Items = append(win.Items,
		&Item{Msg: &model.Msg{Net: "irc:libera", ChatID: 9, ID: 1, Media: &model.Media{Kind: model.MediaFile, Ext: ".txt", State: model.MediaReady}}},
		&Item{Msg: &model.Msg{Net: "irc:libera", ChatID: 9, ID: 2, Media: &model.Media{Kind: model.MediaFile, Ext: ".zip"}}},
		&Item{Msg: &model.Msg{Net: "irc:libera", ChatID: 9, ID: 3, Out: true, Media: &model.Media{Kind: model.MediaFile, Ext: ".png"}}})
	u.command("dcc", []string{"get", "bob"}, "get bob")
	if len(b.downloads) != 1 || !strings.HasSuffix(b.downloads[0], ".zip") {
		t.Fatalf("downloads: %v", b.downloads)
	}
	path := u.cfg.DownloadDir + "/f.txt"
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	u.self = map[string]selfInfo{"irc:libera": {Name: "me"}}
	u.command("dcc", []string{"send", "bob", path}, "send bob "+path)
	if b.file != 1 {
		t.Fatalf("SendFile calls: %d", b.file)
	}
}

// The hostname field of /irc add cycles through the well-known networks:
// a preset fills host, port, TLS and the empty name; typing on it gives a
// free host back; ← from the first preset returns to free text.
func TestIRCAddPresets(t *testing.T) {
	u, _ := launchUI(nil)
	u.command("irc", []string{"add"}, "add")
	f := u.form
	f.cur = fHost
	u.formKey(term.Key{Code: term.Right})
	v := f.values()
	if v[fHost] != "irc.libera.chat" || v[fPort] != "6697" || v[fTLS] != "yes" || v[fName] != "libera" {
		t.Fatalf("first preset: %v", v)
	}
	u.formKey(term.Key{Code: term.Right})
	if v = f.values(); v[fHost] != "irc.oftc.net" || v[fName] != "oftc" {
		t.Fatalf("second preset: %v", v)
	}
	txt := ""
	for _, l := range f.Lines(u.th, formW) {
		for _, s := range l.Spans {
			txt += s.Text
		}
	}
	if !strings.Contains(txt, "‹ irc.oftc.net  OFTC ›") {
		t.Fatalf("drawn: %s", txt)
	}
	typeKeys(u, "my.irc")
	if v = f.values(); v[fHost] != "my.irc" || f.fields[fHost].sel != -1 {
		t.Fatalf("typed host: %v sel %d", v, f.fields[fHost].sel)
	}
	u.formKey(term.Key{Code: term.Right})
	u.formKey(term.Key{Code: term.Left})
	if v = f.values(); v[fHost] != "" {
		t.Fatalf("back to free text: %q", v[fHost])
	}
}
