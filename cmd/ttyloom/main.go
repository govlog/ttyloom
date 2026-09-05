// ttyloom is a terminal chat client for several networks.
package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/govlog/ttyloom/internal/cache"
	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
	"github.com/govlog/ttyloom/internal/ui"
	"github.com/govlog/ttyloom/protocols/dsc"
	"github.com/govlog/ttyloom/protocols/tgc"
)

var version = "dev"
var commit = "unknown"

// cleanParts removes download temp files left by a killed session — at the
// root, in maps/ and paste/, and in avatars/<net>/.
func cleanParts(dir string) {
	for _, pat := range []string{"/.part-*", "/*/.part-*", "/*/*/.part-*"} {
		if m, _ := filepath.Glob(dir + pat); m != nil {
			for _, f := range m {
				_ = os.Remove(f)
			}
		}
	}
}

func main() {
	showVersion := flag.Bool("version", false, "print the version and source commit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("ttyloom %s (%s)\n", version, commit)
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, i18n.T("main_prefix"), err)
		os.Exit(1)
	}
}

// backends builds one backend per configured network: its event chan forwarded
// into the UI chan under its name, its cache directory and the function that
// runs it. Each network starts only when its credentials are configured.
func backends(cfg *config.Config, events chan<- model.Envelope) (map[string]model.Backend, map[string]*cache.Cache, []func(context.Context), error) {
	// [telegram], or the historic flat keys synthesized into it by config.
	tg := cfg.Telegram
	useTelegram := tg != nil && (tg.APIID != 0 || tg.APIHash != "" || tg.BotToken != "")
	if useTelegram && (tg.APIID <= 0 || tg.APIHash == "") {
		return nil, nil, nil, fmt.Errorf(i18n.T("main_no_api_id"), cfg.Path())
	}
	nets := map[string]model.Backend{}
	caches := map[string]*cache.Cache{}
	var starters []func(context.Context)
	root := config.CacheDir()

	// add wires a backend: its own chan, the forwarder that stamps the network
	// on every event, and the body of its goroutine — an error or a panic comes
	// back as an event rather than killing the terminal.
	add := func(net string, mk func(chan<- model.Event) model.Backend) {
		raw := make(chan model.Event, 256)
		b := mk(raw)
		nets[net] = b
		starters = append(starters, func(ctx context.Context) {
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case ev := <-raw:
						select {
						case events <- model.Envelope{Net: net, Ev: ev}:
						case <-ctx.Done():
							return
						}
					}
				}
			}()
			defer func() {
				if r := recover(); r != nil {
					raw <- model.EvFatal{Err: i18n.T("panic", r)}
				}
			}()
			if err := b.Run(ctx); err != nil && ctx.Err() == nil {
				raw <- model.EvFatal{Err: err.Error()}
			}
		})
	}

	if useTelegram {
		add(model.NetTelegram, func(raw chan<- model.Event) model.Backend {
			return tgc.New(tgc.Config{AppID: tg.APIID, AppHash: tg.APIHash, BotToken: tg.BotToken,
				SessionPath: cfg.SessionPath()}, raw)
		})
		if cfg.Cache {
			dir := filepath.Join(root, model.NetTelegram) // one directory per network
			tgRoot := root
			if tg.BotToken != "" {
				dir = filepath.Join(dir, "bot")       // a bot and an account do not share their cache
				tgRoot = filepath.Join(tgRoot, "bot") // … and neither did they before the split
			}
			// Cache of the versions before the split: orphan files at the root of
			// the directory. Dropped once, best effort — everything comes back under
			// the network at the first write.
			// ponytail: "history" here is the pre-split legacy directory name, not a
			// network id — a future network literally named "history" would get its
			// cache directory wiped on every start. Rename this cleanup (or check the
			// network name) if that day comes; unlikely enough not to guard now.
			_ = os.Remove(filepath.Join(tgRoot, "dialogs.gob"))
			_ = os.RemoveAll(filepath.Join(tgRoot, "history"))
			caches[model.NetTelegram] = cache.New(dir, cfg.CacheMessages)
		}
	}

	if cfg.Discord != nil {
		// The token is read here and goes nowhere else: not in the log, not in
		// an event, not in config.toml. A command that fails leaves Discord out
		// and Telegram starts all the same.
		tok, err := cfg.Discord.Token()
		if err != nil {
			if len(nets) == 0 {
				return nil, nil, nil, err
			}
			events <- model.Envelope{Net: model.NetDiscord, Ev: model.EvLog{Level: "ERROR", Msg: err.Error()}}
		} else {
			add(model.NetDiscord, func(raw chan<- model.Event) model.Backend {
				return dsc.New(dsc.Config{Token: tok}, raw)
			})
			if cfg.Cache {
				caches[model.NetDiscord] = cache.New(filepath.Join(root, model.NetDiscord), cfg.CacheMessages)
			}
		}
	}
	if len(nets) == 0 {
		return nil, nil, nil, fmt.Errorf(i18n.T("main_no_networks"), cfg.Path())
	}
	return nets, caches, starters, nil
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cleanParts(config.Expand(cfg.DownloadDir))
	lang := cfg.Lang
	if lang == "" {
		lang = i18n.Detect(cmp.Or(os.Getenv("LC_ALL"), os.Getenv("LANG")))
	}
	i18n.Set(lang)
	// One goroutine per backend chan, fan-in into the UI chan: a backend posts
	// bare events, the UI wants to know which network they come from.
	events := make(chan model.Envelope, 256)
	nets, caches, starters, err := backends(cfg, events)
	if err != nil {
		return err
	}
	name := cfg.Theme
	if name == "" {
		name = theme.GhosttyDefault()
	}
	th, err := theme.Load(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.T("main_prefix"), err, i18n.T("main_theme_fallback"))
		th = theme.Terminal()
	}

	t, err := term.Open()
	if err != nil {
		return err
	}
	defer func() {
		if t.Panic != "" {
			fmt.Fprintln(os.Stderr, "ttyloom: "+t.Panic)
		}
	}()
	defer func() {
		if r := recover(); r != nil {
			t.Close()
			panic(r)
		}
	}()
	defer t.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, start := range starters {
		go start(ctx)
	}
	return ui.Run(ctx, cancel, t, cfg, th, nets, events, caches)
}
