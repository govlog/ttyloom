package ui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

func TestPasteImageName(t *testing.T) {
	at := time.Date(2026, 8, 30, 5, 32, 1, 0, time.UTC)
	for mime, want := range map[string]string{
		"image/png":  "20260830-053201.png",
		"image/jpeg": "20260830-053201.jpg",
		"image/gif":  "20260830-053201.gif",
		"image/webp": "20260830-053201.webp",
		"text/plain": "", // not an image: nothing to write
	} {
		if got := pasteImageName(at, mime); got != want {
			t.Fatalf("%s: %q, want %q", mime, got, want)
		}
	}
}

func TestSplitSendArgs(t *testing.T) {
	exists := func(p string) bool { return p == "/tmp/mon image.png" }
	if p, c := splitSendArgs("/tmp/mon image.png une légende", exists); p != "/tmp/mon image.png" || c != "une légende" {
		t.Fatalf("path with spaces: %q / %q", p, c)
	}
	if p, c := splitSendArgs("/tmp/mon image.png", exists); p != "/tmp/mon image.png" || c != "" {
		t.Fatalf("path alone: %q / %q", p, c)
	}
	if p, c := splitSendArgs("/tmp/absent.png bla", exists); p != "/tmp/absent.png bla" || c != "" {
		t.Fatalf("nonexistent: %q / %q", p, c)
	}
}

func TestClipPick(t *testing.T) {
	// wl-paste lists one type per line, with spaces possible.
	list := "TARGETS\ntext/plain;charset=utf-8 \ntext/plain\nimage/png\n"
	if got := clipPick(list, "image/png", "image/jpeg"); got != "image/png" {
		t.Fatalf("image: %q", got)
	}
	if got := clipPick(list, "image/jpeg"); got != "" {
		t.Fatalf("missing: %q", got)
	}
}

func TestRepeatedImagePasteKeepsSeparateFiles(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "clipboard")
	script := "#!/bin/sh\ncase \"$*\" in *--list-types*) printf 'image/png\\n';; *) printf '\\211PNG\\r\\n\\032\\n12345678';; esac\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	a, ok := readClip(context.Background(), bin, true, dir).(evClipImage)
	if !ok || a.Err != "" || a.Path == "" {
		t.Fatalf("first paste: %+v", a)
	}
	b, ok := readClip(context.Background(), bin, true, dir).(evClipImage)
	if !ok || b.Err != "" || a.Path == b.Path {
		t.Fatalf("second paste replaced the first: %+v", b)
	}
	if err := os.Remove(a.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b.Path); err != nil {
		t.Fatal("cancelling the first paste removed the second", err)
	}
}

// TestSendPathPending : a photo send shows at once as a pending line with its
// media placeholder, before the backend is even called. Until then the
// message appeared only after the upload, the RPC and the server echo — the
// "my own messages come late" report.
func TestSendPathPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\n0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &model.Chat{Net: model.NetTelegram, ID: 7, Title: "alice"}
	b := &fakeBackend{}
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{},
		chats: map[model.ChatKey]*model.Chat{c.Key(): c},
		self:  map[string]selfInfo{model.NetTelegram: {ID: 10, Name: "moi"}},
		nets:  map[string]model.Backend{model.NetTelegram: b}}
	w := u.ws.New(false)
	w.Chat = c

	u.sendPath(c, path, "légende", false)

	if len(w.Items) != 1 || w.Items[0].Msg == nil {
		t.Fatalf("no pending line: %d items", len(w.Items))
	}
	m := w.Items[0].Msg
	if !m.Pending || !m.Out || m.TmpID == 0 || m.Text != "légende" || m.From != "moi" || m.FromID != 10 {
		t.Fatalf("pending message: %+v", m)
	}
	if m.Media == nil || m.Media.Kind != model.MediaPhoto || m.Media.State != model.MediaLoading {
		t.Fatalf("media placeholder: %+v", m.Media)
	}
	if b.photo != 1 || b.photoTmp != m.TmpID {
		t.Fatalf("SendPhoto: %d call(s), tmpID %d, want %d", b.photo, b.photoTmp, m.TmpID)
	}
	if len(u.agg.Items) != 1 { // my sends show in the aggregate too
		t.Fatalf("aggregate: %d items", len(u.agg.Items))
	}

	// Receipt then echo: one line, no longer pending, and the real media of
	// the network replaces the placeholder.
	w.Sent(m.TmpID, 55, "")
	real := &model.Media{Kind: model.MediaPhoto, Label: "[photo 1x1 · 12 B]", Loc: "loc"}
	w.Upsert(&model.Msg{Net: model.NetTelegram, ChatID: 7, ID: 55, Out: true, Text: "légende", Media: real})
	if len(w.Items) != 1 {
		t.Fatalf("echo doubled the message: %d items", len(w.Items))
	}
	if got := w.Items[0].Msg; got.Pending || got.Media != real {
		t.Fatalf("after the echo: pending=%v media=%+v", got.Pending, got.Media)
	}
}

// TestSendPathRoutedWindow : sent from a /search result, the pending line
// lands in the window of the chat — the only one the send receipt reaches —
// and the status bar says where it went, since that window is not the one on
// the screen.
func TestSendPathRoutedWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc.txt")
	if err := os.WriteFile(path, []byte("bonjour"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &model.Chat{Net: model.NetTelegram, ID: 7, Title: "alice"}
	b := &fakeBackend{}
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{},
		chats: map[model.ChatKey]*model.Chat{c.Key(): c},
		self:  map[string]selfInfo{model.NetTelegram: {ID: 10, Name: "moi"}},
		nets:  map[string]model.Backend{model.NetTelegram: b}}
	chatWin := u.ws.New(true)
	chatWin.Chat = c
	sw := u.ws.New(false) // /search result on the same chat: the current window
	sw.Chat, sw.Search = c, "bonjour"

	u.sendPath(c, path, "", false)

	if len(sw.Items) != 0 {
		t.Fatalf("the search window took the line: %d items", len(sw.Items))
	}
	if len(chatWin.Items) != 1 || chatWin.Items[0].Msg == nil || !chatWin.Items[0].Msg.Pending {
		t.Fatalf("window of the chat: %d items", len(chatWin.Items))
	}
	if md := chatWin.Items[0].Msg.Media; md == nil || md.Kind != model.MediaFile {
		t.Fatalf("a text file goes as a document: %+v", md)
	}
	if b.file != 1 || b.fileTmp != chatWin.Items[0].Msg.TmpID {
		t.Fatalf("SendFile: %d call(s), tmpID %d", b.file, b.fileTmp)
	}
	if want := i18n.T("sent_to_window", 1, "alice"); u.flashMsg != want {
		t.Fatalf("status bar: %q, want %q", u.flashMsg, want)
	}
}

// "c" in the viewer: the file goes to the clipboard under its image type,
// read from its bytes; a video or a text file is refused.
func TestCopyImage(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "a.png")
	os.WriteFile(png, append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...), 0o600)
	txt := filepath.Join(dir, "a.txt")
	os.WriteFile(txt, []byte("hello"), 0o600)
	if m := copyMime(png); m != "image/png" {
		t.Fatalf("png: %q", m)
	}
	if m := copyMime(txt); m != "" {
		t.Fatalf("text: %q", m)
	}
	want := []string{"--type", "image/png"}
	if got := copyArgs(true, "image/png", png); !slices.Equal(got, want) {
		t.Fatalf("wl-copy: %v", got)
	}
	want = []string{"-selection", "clipboard", "-t", "image/png", "-i", png}
	if got := copyArgs(false, "image/png", png); !slices.Equal(got, want) {
		t.Fatalf("xclip: %v", got)
	}
}
