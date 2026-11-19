package tgc

import (
	"context"
	"io"
	"os"

	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
)

// Download downloads m.Loc to path (3 in parallel at most). A file already there = success at once.
func (c *Client) Download(ctx context.Context, m *model.Media, path string) {
	loc, ok := m.Loc.(tg.InputFileLocationClass)
	p := &model.Progress{Poster: c.Poster, Media: m, Total: m.Size}
	go func() {
		defer c.Guard("Download", func(err string) { c.Post(model.EvDownloaded{Media: m, Err: err}) })
		if !ok { // handle of another backend: nothing to download here
			c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("media_foreign")})
			c.Post(model.EvDownloaded{Media: m, Err: i18n.T("media_foreign")})
			return
		}
		select {
		case c.dlSem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-c.dlSem }()
		if _, err := os.Stat(path); err == nil {
			c.Post(model.EvDownloaded{Media: m, Path: path})
			return
		}
		err := media.Save(path, func(f *os.File) error {
			_, err := c.dl.Download(c.api, loc).Parallel(ctx, countedAt{f, p})
			return err
		})
		if err != nil {
			c.Post(model.EvDownloaded{Media: m, Err: err.Error()})
			return
		}
		c.Post(model.EvDownloaded{Media: m, Path: path})
	}()
}

// countedAt : the file of a parallel download, counting what its parts write.
type countedAt struct {
	io.WriterAt
	p *model.Progress
}

func (w countedAt) WriteAt(b []byte, off int64) (int, error) {
	n, err := w.WriterAt.WriteAt(b, off)
	w.p.Add(n)
	return n, err
}

// DownloadMap gets the OSM map of m (Lat/Long); there is no Telegram Loc — an
// HTTP call to tile.openstreetmap.org through media.Tile, with the same
// semaphore and the same event back as Download: the UI has nothing to tell apart.
func (c *Client) DownloadMap(ctx context.Context, m *model.Media, path string) {
	lat, long := m.Lat, m.Long
	go func() {
		defer c.Guard("DownloadMap", func(err string) { c.Post(model.EvDownloaded{Media: m, Err: err}) })
		select {
		case c.dlSem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-c.dlSem }()
		if err := media.Tile(ctx, lat, long, path); err != nil {
			c.Post(model.EvDownloaded{Media: m, Err: err.Error()})
			return
		}
		c.Post(model.EvDownloaded{Media: m, Path: path})
	}()
}
