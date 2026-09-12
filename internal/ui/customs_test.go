package ui

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// TestPickerCustomImage : on kitty, the custom emoji on the screen of the
// picker downloads its image once, and once decoded the image takes its cell
// — a placement of the frame, the name gone from the grid.
func TestPickerCustomImage(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "e.png")
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	u := netUI(model.NetDiscord)
	u.ctx, u.images, u.th = context.Background(), "kitty", theme.Terminal()
	u.events = make(chan model.Event, 4)
	u.t = &term.Term{Cols: 80, Rows: 24, Kitty: true, CellW: 9, CellH: 18}
	b := u.nets[model.NetDiscord].(*fakeBackend)
	c := u.chatList[0]
	c.Customs = []string{":profil:"}
	c.CustomLocs = map[string]any{":profil:": "https://cdn.example/emojis/1.png"}
	u.openPicker(c, nil)
	u.picker.recent = nil
	u.picker.filter()

	u.customLoad()
	u.customLoad() // a second drawing asks nothing again
	if len(b.downloads) != 1 || !strings.HasPrefix(b.downloads[0], config.CacheDir()) {
		t.Fatalf("downloads: %v", b.downloads)
	}
	if got := render.LineText(u.picker.Lines(u.th)[2]); !strings.HasPrefix(got, "│pr ") {
		t.Fatalf("grid before the image: %q", got)
	}
	md := u.customMedia(":profil:")
	u.downloaded(model.EvDownloaded{Media: md, Path: path})
	select {
	case e := <-u.events:
		u.framesLoaded(e.(evFrames))
	case <-time.After(2 * time.Second):
		t.Fatal("no evFrames")
	}
	pl := u.customPlacements(0)
	if len(pl) != 1 || pl[0].img.Cols > cellW-1 || pl[0].img.Rows != 1 {
		t.Fatalf("placements: %+v", pl)
	}
	r := u.pickerRect()
	if pl[0].row != r.row+2 || pl[0].img.Col != r.col+1 {
		t.Fatalf("placement at %d,%d; want the first cell of the grid %d,%d", pl[0].row, pl[0].img.Col, r.row+2, r.col+1)
	}
	if got := render.LineText(u.picker.Lines(u.th)[2]); !strings.HasPrefix(got, "│   ") {
		t.Fatalf("grid under the image: %q", got)
	}
}
