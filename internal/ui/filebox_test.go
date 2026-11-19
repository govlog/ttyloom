package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/cache"
	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// filesUI : Telegram with a history on the disk only (Alice sends a
// document, a photo and a sticker, I send a file), and Discord with an open
// window where Bob sends a file.
func filesUI(t *testing.T) *UI {
	t.Helper()
	day := func(d int) time.Time { return time.Date(2026, 11, d, 10, 0, 0, 0, time.Local) }
	alice := &model.Chat{Net: netTelegram, ID: 1, Kind: model.ChatUser, Title: "Alice"}
	general := &model.Chat{Net: netDiscord, ID: 2, Kind: model.ChatGroup, Title: "general"}
	tg := cache.New(t.TempDir(), 200)
	if err := tg.SaveHistory(1, []model.Msg{
		{ID: 1, ChatID: 1, Date: day(1), From: "Alice", FromID: 7, Media: &model.Media{Kind: model.MediaFile, Name: "report.pdf", Loc: 1}},
		{ID: 2, ChatID: 1, Date: day(2), From: "Alice", FromID: 7, Media: &model.Media{Kind: model.MediaPhoto, Label: "[photo]", Loc: 2}},
		{ID: 3, ChatID: 1, Date: day(3), From: "Alice", FromID: 7, Media: &model.Media{Kind: model.MediaSticker, Loc: 3}},
		{ID: 4, ChatID: 1, Date: day(4), Out: true, From: "me", FromID: 9, Media: &model.Media{Kind: model.MediaFile, Name: "mine.txt", Loc: 4}},
	}); err != nil {
		t.Fatal(err)
	}
	u := &UI{ctx: context.Background(), events: make(chan model.Event, 8), ws: NewWindows(), agg: &Window{}, debug: &Window{},
		th: theme.Terminal(), cfg: &config.Config{}, t: term.NewOffscreen(&strings.Builder{}, 120, 30),
		caches: map[string]*cache.Cache{netTelegram: tg, netDiscord: cache.New(t.TempDir(), 200)},
		chats:  map[model.ChatKey]*model.Chat{alice.Key(): alice, general.Key(): general}, chatList: []*model.Chat{alice, general}}
	w := u.ws.New(true)
	w.Chat = general
	w.Items = append(w.Items, &Item{Msg: &model.Msg{Net: netDiscord, ChatID: 2, ID: 5, Date: day(5), From: "bob", FromID: 8,
		Media: &model.Media{Kind: model.MediaFile, Name: "notes.md", Loc: 5}}})
	return u
}

// boxText : the text of the file browser as the screen shows it.
func boxText(u *UI) string {
	var b strings.Builder
	for _, l := range u.filesLines(u.filesRect()) {
		b.WriteString(render.LineText(l) + "\n")
	}
	return b.String()
}

// openedFiles opens the file browser and waits for the disk.
func openedFiles(u *UI) string {
	u.openFiles()
	done := make(chan struct{})
	go func() { u.fileRuns.Wait(); close(done) }()
	for {
		select {
		case ev := <-u.events:
			u.event(ev)
		case <-done:
			u.drain()
			return boxText(u)
		}
	}
}

// order : the rank of each s in text, -1 when one is missing.
func inOrder(text string, s ...string) bool {
	at := -1
	for _, x := range s {
		i := strings.Index(text, x)
		if i <= at {
			return false
		}
		at = i
	}
	return true
}

// The file browser lists the people who sent files, by network, from the
// disk cache and the open windows; stickers are no files. Enter on a person
// goes to their files, Enter on a file opens the menu of its file. What is
// typed filters the files by name, a pattern too. The aggregate view gives
// one list; /net keeps one network.
func TestFileBrowser(t *testing.T) {
	u := filesUI(t)
	text := openedFiles(u)
	me := "me (sent)"
	if !strings.Contains(text, me) {
		me = "moi (envoyés)"
	}
	if !inOrder(text, "── discord", "bob", "── telegram", me, "Alice") {
		t.Fatalf("by network:\n%s", text)
	}
	if !strings.Contains(text, "notes.md") { // the files of the first person
		t.Fatalf("files of bob:\n%s", text)
	}

	u.key(term.Key{Code: term.Down}) // telegram: me
	u.key(term.Key{Code: term.Down}) // Alice
	u.key(term.Key{Code: term.Enter})
	text = boxText(u)
	if !inOrder(text, "[photo]", "report.pdf") || strings.Contains(text, "mine.txt") {
		t.Fatalf("files of Alice, newest first, no sticker:\n%s", text)
	}
	u.key(term.Key{Code: term.Enter})
	if u.menu == nil || u.menu.file == nil || u.menu.file.Msg.Media.Kind != model.MediaPhoto {
		t.Fatalf("Enter on the photo: menu %+v", u.menu)
	}
	u.key(term.Key{Code: term.Esc}) // the menu closes, the box stays
	if u.menu != nil || u.files == nil {
		t.Fatal("Esc: the menu closes first")
	}

	for _, r := range "*.pdf" { // typing filters by name
		u.key(term.Key{Rune: r})
	}
	text = boxText(u)
	if !strings.Contains(text, "report.pdf") || strings.Contains(text, "[photo]") || strings.Contains(text, "bob") {
		t.Fatalf("*.pdf:\n%s", text)
	}
	u.key(term.Key{Code: term.Esc}) // clears the filter, the box stays
	if u.files == nil || !strings.Contains(boxText(u), "bob") {
		t.Fatal("Esc with a filter: the filter goes first")
	}

	u.aggregate = true
	text = openedFiles(u)
	if strings.Contains(text, "── ") || !strings.Contains(text, "bob") || !strings.Contains(text, "Alice") {
		t.Fatalf("aggregate: one list\n%s", text)
	}

	u.aggregate, u.netFilter = false, netTelegram
	text = openedFiles(u)
	if strings.Contains(text, "bob") || !strings.Contains(text, "Alice") {
		t.Fatalf("/net telegram:\n%s", text)
	}
}

// The filter of the file browser: a pattern with * and ? on the whole name,
// else a part of it; case and accents aside; [ is a plain letter.
func TestFileMatch(t *testing.T) {
	cases := []struct {
		q, name string
		want    bool
	}{
		{"*toto*.gif", "Mon TOTO final.gif", true},
		{"*toto*.gif", "toto.gifv", false},
		{"*.pdf", "Plan YubiKey.PDF", true},
		{"rapp", "Rapport annuel.docx", true},
		{"resume", "Résumé.txt", true},
		{"[x]*", "[x] liste.md", true},
		{"photo?.jpg", "photo1.jpg", true},
	}
	for _, c := range cases {
		if got := fileMatch(c.q, &model.Media{Name: c.name}); got != c.want {
			t.Errorf("fileMatch(%q, %q) = %v, want %v", c.q, c.name, got, c.want)
		}
	}
}

// The file browser looks for the documents on the disk in its background
// read, never on the UI loop (5000 documents took 38 ms there, seconds on a
// network home). The result stays the same: a document downloaded under the
// name of its message moves to the folder of its conversation, and the box
// knows the file of each document on the disk, the ones of the open windows
// included.
func TestFileBrowserDiskOffLoop(t *testing.T) {
	u := filesUI(t)
	dir := t.TempDir()
	u.cfg.DownloadDir = dir
	day1 := time.Date(2026, 11, 1, 10, 0, 0, 0, time.Local)
	legacy := filepath.Join(dir, media.FileName(model.ChatKey{Net: netTelegram, ID: 1}, "Alice", 1, day1, ""))
	report := filepath.Join(dir, "telegram", "Alice", "report.pdf")
	notes := filepath.Join(dir, "discord", "general", "notes.md")
	for _, p := range []string{legacy, notes} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(notes, time.Date(2026, 11, 5, 10, 0, 0, 0, time.Local), time.Date(2026, 11, 5, 10, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	path := func(name string) string { // the file the box knows for the document name
		for _, p := range u.files.all {
			for _, it := range p.files {
				if it.Msg.Media.Name == name {
					return it.Msg.Media.Path
				}
			}
		}
		t.Fatalf("%s not in the box", name)
		return ""
	}

	hist, err := u.caches[netTelegram].LoadHistory(1)
	if err != nil {
		t.Fatal(err)
	}
	u.files = &filesBox{loading: true}
	u.filesLoaded(evFiles{box: u.files, hist: map[model.ChatKey][]model.Msg{{Net: netTelegram, ID: 1}: hist}})
	if _, err := os.Stat(legacy); err != nil || path("report.pdf") != "" {
		t.Fatalf("the UI loop looked at the disk: old name %v, path %q; want both untouched", err, path("report.pdf"))
	}

	openedFiles(u)
	if _, err := os.Stat(report); err != nil || path("report.pdf") != report || path("notes.md") != notes {
		t.Fatalf("after the disk read: report.pdf %v at %q, notes.md at %q; want %s and %s",
			err, path("report.pdf"), path("notes.md"), report, notes)
	}
}
