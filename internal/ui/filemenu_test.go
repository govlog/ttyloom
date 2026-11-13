package ui

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

// menuKeys : the entries of the menu open, by their key.
func menuKeys(u *UI) string {
	var out []string
	for _, e := range u.menu.entries {
		out = append(out, e.key)
	}
	return strings.Join(out, "|")
}

// A click on the label of a file opens its menu. A photo offers its preview
// and a copy of the image; every file offers its path and the desktop. Copy
// the path of a file not on the disk yet downloads it, then copies its
// absolute path.
func TestFileMenu(t *testing.T) {
	u, b := gifUI()
	var out bytes.Buffer
	u.t = term.NewOffscreen(&out, 100, 30)
	u.fileNext, u.fulls = map[*model.Media]func(*model.Media){}, map[*model.Media]bool{}
	u.goTo(1)
	click := func(md *model.Media) {
		it := &Item{Msg: &model.Msg{Net: netTelegram, ChatID: 1, ID: 5, Media: md}}
		u.hits = []rowHit{{item: it, acts: []render.Action{{Col0: 0, Col1: 8, Key: render.KeyFile}}}}
		u.mouse(term.MouseEvent{Button: 0, X: 2, Y: 0, Press: true})
		if u.menu == nil || u.menu.file != it {
			t.Fatalf("click on the label of %s: menu %+v", md.Label, u.menu)
		}
	}

	click(&model.Media{Kind: model.MediaPhoto, Label: "[photo]", Mime: "image/jpeg", Loc: 1})
	if got := menuKeys(u); got != "view|copyimg|copypath|share|open" {
		t.Fatalf("photo menu: %s", got)
	}
	u.closeMenu()

	doc := &model.Media{Kind: model.MediaFile, Label: "[file notes.txt]", Name: "notes.txt", Loc: 2}
	click(doc)
	if got := menuKeys(u); got != "copypath|share|open" {
		t.Fatalf("file menu: %s", got)
	}
	u.menuKey(term.Key{Code: term.Enter}) // copy the path
	if len(b.downloads) != 1 || strings.Contains(out.String(), "\x1b]52;") {
		t.Fatalf("not on the disk: downloads %v, copied before it landed", b.downloads)
	}
	u.downloaded(model.EvDownloaded{Media: doc, Path: "dl/notes.txt"})
	u.t.Flush()
	abs, _ := filepath.Abs("dl/notes.txt")
	if !strings.Contains(out.String(), "\x1b]52;c;"+base64.StdEncoding.EncodeToString([]byte(abs))) {
		t.Fatalf("landed: no copy of %s", abs)
	}
}

// A right click on a picture of the conversation opens the menu of its
// file; anywhere else on the message, the menu of the message.
func TestRightClickPicture(t *testing.T) {
	u, _ := gifUI()
	md := &model.Media{Kind: model.MediaPhoto, Label: "[photo]", Loc: 1}
	it := &Item{Msg: &model.Msg{Net: netTelegram, ChatID: 1, ID: 5, Media: md}}
	u.hits = []rowHit{{item: it, img: &render.Img{Media: md, Cols: 4, Rows: 1}}, {item: it}}
	u.mouse(term.MouseEvent{Button: 2, X: 2, Y: 0, Press: true})
	if u.menu == nil || u.menu.file != it {
		t.Fatalf("right click on the picture: menu %+v", u.menu)
	}
	u.closeMenu()
	u.mouse(term.MouseEvent{Button: 2, X: 2, Y: 1, Press: true})
	if u.menu == nil || u.menu.item != it {
		t.Fatalf("right click on the text: menu %+v", u.menu)
	}
}

// "Send to a conversation": on the network of the file, the network
// forwards the message; on another one, the file goes up as a file of mine,
// once it is on the disk.
func TestShareFile(t *testing.T) {
	u, tg := gifUI()
	dc := &fakeBackend{caps: model.AllCaps()}
	u.nets[netDiscord] = dc
	u.fileNext = map[*model.Media]func(*model.Media){}
	alice := u.chats[model.ChatKey{Net: netTelegram, ID: 1}]
	bob := &model.Chat{Net: netTelegram, ID: 2, Kind: model.ChatUser, Title: "Bob"}
	general := &model.Chat{Net: netDiscord, ID: 3, Kind: model.ChatGroup, Title: "general"}
	u.chats[bob.Key()], u.chats[general.Key()] = bob, general
	md := &model.Media{Kind: model.MediaFile, Name: "plan.pdf", Size: 3, Loc: 1}
	m := &model.Msg{Net: netTelegram, ChatID: alice.ID, ID: 7, Date: time.Now(), Media: md}

	u.share(m, bob)
	if len(tg.forwards) != 1 || tg.file != 0 {
		t.Fatalf("same network: forwards %v, files %d", tg.forwards, tg.file)
	}
	u.share(m, general)
	if dc.file != 0 || len(tg.downloads) != 1 {
		t.Fatalf("other network, not on the disk: sent %d before the download %v", dc.file, tg.downloads)
	}
	u.downloaded(model.EvDownloaded{Media: md, Path: tg.downloads[0]})
	if dc.file != 1 {
		t.Fatalf("other network: %d files sent", dc.file)
	}
}
