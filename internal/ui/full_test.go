package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/model"
)

// The line keeps its 800 px photo; "o" and the viewer take the larger variant
// the backend gave (Media.Full), downloaded to its own file. Its arrival
// touches neither the line's media nor the inline decoding.
func TestFullSizePhotoForOpenAndViewer(t *testing.T) {
	u, b := gifUI()
	u.cfg.DownloadDir = t.TempDir()
	u.openNext, u.fulls = map[*model.Media]bool{}, map[*model.Media]bool{}
	w := u.ws.List[1]
	full := &model.Media{Kind: model.MediaPhoto, Loc: "y", Ext: ".jpg", W: 1280, H: 960}
	md := &model.Media{Kind: model.MediaPhoto, Loc: "x", Ext: ".jpg", W: 800, H: 600, Full: full}
	m := &model.Msg{Net: netTelegram, ChatID: 1, ID: 7, Date: time.Now(), Media: md}
	it := &Item{Msg: m}
	w.Items = append(w.Items, it)

	u.openItemMedia(w, it)
	if len(b.downloads) != 1 || !strings.HasSuffix(b.downloads[0], "_7_full.jpg") ||
		full.State != model.MediaLoading || md.State != model.MediaNone || !u.openNext[full] {
		t.Fatalf("o: downloads %v, full %v, line %v", b.downloads, full.State, md.State)
	}
	delete(u.openNext, full) // no xdg-open from a test

	u.openViewer(m)
	if u.viewer == nil || u.viewer.src != full || len(b.downloads) != 1 {
		t.Fatalf("viewer: %+v, downloads %v", u.viewer, b.downloads)
	}

	u.images = "off" // no full screen decoding here
	u.downloaded(model.EvDownloaded{Media: full, Path: "/nonexistent/full.jpg"})
	if full.Path != "/nonexistent/full.jpg" || full.State != model.MediaReady || full.Want != 0 || md.Path != "" || md.State != model.MediaNone {
		t.Fatalf("arrival: full %+v, line %+v", full, md)
	}
}

// A photo with no larger variant keeps the one path for the line, "o" and the viewer.
func TestPhotoWithoutFullUsesLineMedia(t *testing.T) {
	u, b := gifUI()
	u.cfg.DownloadDir = t.TempDir()
	u.openNext, u.fulls = map[*model.Media]bool{}, map[*model.Media]bool{}
	w := u.ws.List[1]
	md := &model.Media{Kind: model.MediaPhoto, Loc: "x", Ext: ".jpg", W: 800, H: 600}
	m := &model.Msg{Net: netTelegram, ChatID: 1, ID: 8, Date: time.Now(), Media: md}
	it := &Item{Msg: m}
	w.Items = append(w.Items, it)

	u.openItemMedia(w, it)
	u.openViewer(m)
	if len(b.downloads) != 1 || !strings.HasSuffix(b.downloads[0], "_8.jpg") || md.State != model.MediaLoading || u.viewer.src != md {
		t.Fatalf("downloads %v, state %v, viewer %+v", b.downloads, md.State, u.viewer)
	}
}
