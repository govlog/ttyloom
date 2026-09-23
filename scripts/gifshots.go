//go:build ignore

// gifshots builds the GIF tiles of the documentation screenshots from the
// real GIF searches of the configured Discord and Telegram accounts.
//
// Run it from the repository root, with the sessions of the client already in
// place (ffmpeg is needed: both pickers answer mp4 clips):
//
//	go run scripts/gifshots.go -out docs/screenshots/fixtures -query cat -n 8
//
// It writes gif-<network>-<i>.png — the first frame, fitted in 96x54 px, as
// the UI decodes it — and SOURCES.md in the output directory. Nothing else
// leaves the account: no token, no session, no conversation. A network that
// fails prints its error and the other one goes on; the exit code is 0 as
// soon as one network gave tiles.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/protocols/dsc"
	"github.com/govlog/ttyloom/protocols/tgc"
)

// Size of a tile, in pixels: what the GIF box of the UI shows.
const tileW, tileH = 96, 54

// tile : one written file, one line of SOURCES.md.
type tile struct{ file, net, query, origin string }

func main() {
	out := flag.String("out", "docs/screenshots/fixtures", "output directory")
	query := flag.String("query", "cat", "query sent to both GIF pickers")
	n := flag.Int("n", 8, "tiles wanted per network")
	timeout := flag.Duration("timeout", 90*time.Second, "deadline of the whole run")
	flag.Parse()
	if err := run(*out, *query, *n, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, "gifshots:", err)
		os.Exit(1)
	}
}

func run(out, query string, n int, timeout time.Duration) error {
	m := mods{tg: tgc.NewModule(), dc: dsc.NewModule()}
	_, err := config.Load(m.tg, m.dc)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	tmp, err := os.MkdirTemp("", "gifshots-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp) // the downloaded clips never outlive the run
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	var tiles []tile
	for _, net := range []string{dsc.Net, tgc.Net} {
		got, err := grab(ctx, m, net, query, n, tmp, out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gifshots: %s: %v\n", net, err)
		}
		tiles = append(tiles, got...)
	}
	if len(tiles) == 0 {
		return errors.New("no tile written")
	}
	if err := writeSources(filepath.Join(out, "SOURCES.md"), tiles); err != nil {
		return err
	}
	fmt.Printf("%d tiles in %s\n", len(tiles), out)
	return nil
}

// grab connects net, searches query and writes at most n tiles. What was
// already written comes back even with an error: a result the decoder refuses
// must not lose the ones before it.
func grab(ctx context.Context, m mods, net, query string, n int, tmp, out string) ([]tile, error) {
	// One reader for this chan, and the backend posts while we decode: the
	// buffer holds the updates of a few seconds of connection.
	events := make(chan model.Event, 1024)
	b, err := build(m, net, events)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // the backend ends with this function
	if p, ok := b.(interface{ SetContext(context.Context) }); ok {
		p.SetContext(ctx)
	}
	go func() {
		msg := ""
		if err := b.Run(ctx); err != nil {
			msg = err.Error()
		}
		select {
		case events <- model.EvStopped{Err: msg}:
		case <-ctx.Done():
		}
	}()
	if _, err := wait[model.EvReady](ctx, events); err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	chat, err := inlinePeer(ctx, net, b, events)
	if err != nil {
		return nil, err
	}
	b.SearchGifs(ctx, chat, query)
	ev, err := wait[model.EvGifs](ctx, events)
	if err != nil {
		return nil, err
	}
	if ev.Err != "" {
		return nil, errors.New(ev.Err)
	}
	var tiles []tile
	for _, g := range ev.Gifs {
		if len(tiles) >= n {
			break
		}
		if g.Preview == nil {
			continue
		}
		name := fmt.Sprintf("gif-%s-%d.png", net, len(tiles)+1)
		src := filepath.Join(tmp, fmt.Sprintf("%s-%d%s", net, len(tiles)+1, g.Preview.Ext))
		if err := tileOf(ctx, b, events, g, src, filepath.Join(out, name)); err != nil {
			fmt.Fprintf(os.Stderr, "gifshots: %s: %s: %v\n", net, name, err)
			continue
		}
		tiles = append(tiles, tile{file: name, net: net, query: query, origin: origin(g)})
	}
	if len(tiles) == 0 {
		return nil, errors.New("no usable result")
	}
	return tiles, nil
}

// mods : the two networks the tiles come from, read from config.toml by
// their modules.
type mods struct {
	tg *tgc.Module
	dc *dsc.Module
}

// build makes the backend of net on events through its module, the way the
// client does. What it reads stays where it is: the Discord token and the
// Telegram session go to the backend and nowhere else, never to the output.
func build(m mods, net string, events chan<- model.Event) (model.Backend, error) {
	switch net {
	case dsc.Net:
		if len(m.dc.Networks()) == 0 {
			return nil, errors.New("no [discord] section in the configuration")
		}
		tok, err := m.dc.Token()
		if err != nil {
			return nil, err
		}
		if tok == "" {
			// No QR login here: it would wait for a scan nobody is watching.
			return nil, errors.New("no token, log in with the client first")
		}
		return m.dc.Launch(context.Background(), nil, net, events)
	case tgc.Net:
		s := m.tg.Settings()
		if s.APIID <= 0 || s.APIHash == "" {
			return nil, errors.New("no api_id/api_hash in the configuration")
		}
		if s.BotToken == "" {
			if _, err := os.Stat(m.tg.SessionPath()); err != nil {
				return nil, errors.New("no session, log in with the client first")
			}
		}
		return m.tg.Launch(context.Background(), nil, net, events)
	}
	return nil, errors.New("unknown network")
}

// inlinePeer gives the conversation the search is asked from: Telegram sends
// the inline query to @gif inside one, Discord ignores it. The first dialog
// of the account is enough — the query is never posted.
func inlinePeer(ctx context.Context, net string, b model.Backend, events <-chan model.Event) (*model.Chat, error) {
	if net != tgc.Net {
		return nil, nil
	}
	b.LoadDialogs(ctx)
	for { // the first answer can be an empty cached list
		ev, err := wait[model.EvDialogs](ctx, events)
		if err != nil {
			return nil, err
		}
		if ev.Err != "" {
			return nil, errors.New(ev.Err)
		}
		if len(ev.Chats) > 0 {
			return ev.Chats[0], nil
		}
	}
}

// tileOf downloads the preview of g into src, decodes its first frame fitted
// in tileW x tileH px and writes it to dst.
func tileOf(ctx context.Context, b model.Backend, events <-chan model.Event, g model.Gif, src, dst string) error {
	b.Download(ctx, g.Preview, src)
	dl, err := wait[model.EvDownloaded](ctx, events)
	if err != nil {
		return err
	}
	if dl.Err != "" {
		return errors.New(dl.Err)
	}
	f, err := media.Load(ctx, dl.Path, g.Preview.Mime, tileW, tileH, 1)
	if err != nil {
		return err
	}
	if len(f.PNG) == 0 {
		return errors.New("no frame decoded")
	}
	return os.WriteFile(dst, f.PNG[0], 0o644)
}

// origin : where a tile comes from, for SOURCES.md — the page URL when the
// network gives one (Discord sends a link, and that link is the result), a
// plain description otherwise (Telegram answers an inline result of @gif,
// valid for its query only). Never a handle that could carry a credential.
func origin(g model.Gif) string {
	if g.Preview != nil && g.Preview.URL != "" {
		return g.Preview.URL
	}
	if s, ok := g.Send.(string); ok && strings.HasPrefix(s, "http") {
		return s
	}
	return "inline result of the network GIF bot"
}

func writeSources(path string, tiles []tile) error {
	var b strings.Builder
	b.WriteString(`# GIF tile sources

Frames of third-party GIF returned by the Discord and Telegram GIF searches,
written by ` + "`scripts/gifshots.go`" + `. They belong to their authors and
illustrate the documentation screenshots only.

| File | Network | Query | Origin |
| --- | --- | --- | --- |
`)
	for _, t := range tiles {
		fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s |\n", t.file, t.net, t.query, cell(t.origin))
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// cell keeps a value inside its table column: a pipe would open a new one.
func cell(s string) string { return strings.ReplaceAll(s, "|", "%7C") }

// wait gives the first event of type T and drops the others — a backend posts
// logs, updates and dialogs all along. The end of Run comes back as EvStopped:
// waiting for an event that will never come would otherwise last until the
// timeout.
func wait[T any](ctx context.Context, events <-chan model.Event) (T, error) {
	var zero T
	for {
		select {
		case ev := <-events:
			if v, ok := ev.(T); ok {
				return v, nil
			}
			switch e := ev.(type) {
			case model.EvStopped:
				return zero, errors.New(cmp.Or(e.Err, "connection closed"))
			case model.EvLog:
				if e.Level == "ERROR" { // the backend says why a download died
					fmt.Fprintln(os.Stderr, "gifshots:", e.Msg)
				}
			}
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}
}
