package tgc

import (
	"cmp"
	"context"
	"os"
	"path/filepath"

	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
)

// Download downloads m.Loc to path (3 in parallel at most). A file already there = success at once.
func (c *Client) Download(ctx context.Context, m *model.Media, path string) {
	loc, ok := m.Loc.(tg.InputFileLocationClass)
	go func() {
		defer c.Guard("Download", func(err string) { c.Post(model.EvDownloaded{Media: m, Err: err}) })
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
		// 0700 like the rest of the project (config, cache, logs, maps): a
		// private media must not be readable by the other accounts of the
		// machine. os.CreateTemp gives a 0600 file with a name of its own —
		// two downloads of the same media no longer walk on each other, and
		// no pre-existing symbolic link is followed.
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			c.Post(model.EvDownloaded{Media: m, Err: err.Error()})
			return
		}
		f, err := os.CreateTemp(dir, ".part-*")
		if err != nil {
			c.Post(model.EvDownloaded{Media: m, Err: err.Error()})
			return
		}
		tmp := f.Name()
		if !ok { // handle of another backend: nothing to download here
			f.Close()
			os.Remove(tmp)
			c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("media_foreign")})
			c.Post(model.EvDownloaded{Media: m, Err: i18n.T("media_foreign")})
			return
		}
		_, derr := c.dl.Download(c.api, loc).Parallel(ctx, f)
		cerr := f.Close()
		if derr != nil || cerr != nil {
			os.Remove(tmp)
			c.Post(model.EvDownloaded{Media: m, Err: cmp.Or(derr, cerr).Error()})
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			os.Remove(tmp)
			c.Post(model.EvDownloaded{Media: m, Err: err.Error()})
			return
		}
		c.Post(model.EvDownloaded{Media: m, Path: path})
	}()
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
