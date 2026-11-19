package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// A download shows its percent on the line of its file, on every network:
// the line drawn before the report is drawn again with it.
func TestDownloadPercentOnLine(t *testing.T) {
	u, _ := gifUI()
	w := u.ws.List[1]
	md := &model.Media{Kind: model.MediaFile, Name: "notes.txt", Label: "[file notes.txt · 100 B]", Loc: 1, Size: 100,
		State: model.MediaLoading}
	w.Upsert(&model.Msg{Net: netTelegram, ChatID: 1, ID: 1, From: "alice", Date: time.Now(), Media: md})
	if got := viewText(u, w); !strings.Contains(got, "[file notes.txt · 100 B]"+i18n.T("media_loading")) {
		t.Fatalf("before any report: %q", got)
	}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvDownloadProgress{Media: md, Done: 42, Total: 100}})
	if got, want := viewText(u, w), "[file notes.txt · 100 B]"+i18n.T("media_loading_pct", 42); !strings.Contains(got, want) {
		t.Fatalf("after 42 of 100 bytes: %q, want %q", got, want)
	}
}

// A file that never downloads on its own (a document, a DCC offer) names its
// percent in the status bar too: its line may be off the screen (/dcc get
// <nick>, Ctrl+D). A picture that loads by itself stays on its line.
func TestFileDownloadInStatusBar(t *testing.T) {
	u, _ := gifUI()
	photo := &model.Media{Kind: model.MediaPhoto, Loc: 1, State: model.MediaLoading}
	u.event(model.EvDownloadProgress{Media: photo, Done: 10, Total: 100})
	if u.flashMsg != "" {
		t.Fatalf("status bar for a picture: %q", u.flashMsg)
	}
	doc := &model.Media{Kind: model.MediaFile, Name: "notes.txt", Loc: 2, State: model.MediaLoading}
	u.event(model.EvDownloadProgress{Media: doc, Done: 42, Total: 100})
	if want := i18n.T("downloading_pct", "notes.txt", 42); u.flashMsg != want {
		t.Fatalf("status bar: %q, want %q", u.flashMsg, want)
	}
}

// The receipt of a send ends the transfer of its file: a failed upload says
// why and no longer says it is loading.
func TestUploadReceiptStopsLoading(t *testing.T) {
	u, b := gifUI()
	c := u.chats[model.ChatKey{Net: netTelegram, ID: 1}]
	path := filepath.Join(t.TempDir(), "doc.txt")
	if err := os.WriteFile(path, []byte("bonjour"), 0o600); err != nil {
		t.Fatal(err)
	}
	u.sendPath(c, path, "", false)
	w := u.winFor(c)
	if got := viewText(u, w); !strings.Contains(got, i18n.T("media_loading")) {
		t.Fatalf("upload on its way: %q", got)
	}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvSent{ChatID: 1, TmpID: b.fileTmp, Err: "boom"}})
	if got := viewText(u, w); !strings.Contains(got, i18n.T("msg_failed", "boom")) || strings.Contains(got, i18n.T("media_loading")) {
		t.Fatalf("failed upload: %q", got)
	}
}

// A network may answer a download with a file of its own rather than the
// path asked (IRC: a file sent from this machine). The UI opens that file
// and never changes its time.
func TestDownloadElsewhereKeepsTime(t *testing.T) {
	u, b := gifUI()
	u.cfg.DownloadDir = t.TempDir()
	own := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(own, []byte("pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(own)
	if err != nil {
		t.Fatal(err)
	}
	m := &model.Msg{Net: netTelegram, ChatID: 1, ID: 1, Out: true, Date: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC),
		Media: &model.Media{Kind: model.MediaFile, Name: "report.pdf", Loc: 1, Size: 3}}
	u.ws.List[1].Upsert(m)
	u.download(m)
	if len(b.downloads) != 1 || b.downloads[0] == own {
		t.Fatalf("downloads asked: %v", b.downloads)
	}
	u.dispatch(model.Envelope{Net: netTelegram, Ev: model.EvDownloaded{Media: m.Media, Path: own}})
	after, err := os.Stat(own)
	if err != nil || !after.ModTime().Equal(fi.ModTime()) || m.Media.Path != own {
		t.Fatalf("file of the network: time %v → %v (%v), media path %q", fi.ModTime(), after.ModTime(), err, m.Media.Path)
	}
}
