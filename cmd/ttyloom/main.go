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

// backends : the configured networks, their caches, and the launcher that
// builds and runs one of them — at start for each, and again on /<net> login.
// A network is configured only when its credentials are; the Discord token
// is read here once, then at every launch, and goes nowhere else: not in the
// log, not in an event, not in config.toml. ctx is the one of the whole client: the last
// event of a network is delivered as long as the UI runs.
func backends(ctx context.Context, cfg *config.Config, events chan<- model.Envelope) ([]string, map[string]*cache.Cache, model.Launcher, error) {
	// [telegram], or the historic flat keys synthesized into it by config.
	tg := cfg.Telegram
	useTelegram := tg != nil && (tg.APIID != 0 || tg.APIHash != "" || tg.BotToken != "")
	if useTelegram && (tg.APIID <= 0 || tg.APIHash == "") {
		return nil, nil, nil, fmt.Errorf(i18n.T("main_no_api_id"), cfg.Path())
	}
	var nets []string
	caches := map[string]*cache.Cache{}
	root := config.CacheDir()

	if useTelegram {
		nets = append(nets, model.NetTelegram)
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
		nets = append(nets, model.NetDiscord)
		if cfg.Cache {
			caches[model.NetDiscord] = cache.New(filepath.Join(root, model.NetDiscord), cfg.CacheMessages)
		}
	}
	if len(nets) == 0 {
		return nil, nil, nil, fmt.Errorf(i18n.T("main_no_networks"), cfg.Path())
	}

	// The first Discord token is read now, before the terminal goes raw: a
	// token command that prompts on the tty (pinentry-curses) works at start
	// as it always did. The launches that follow (/discord login) read it
	// again from inside the raw terminal — such a command needs a graphical
	// pinentry or an unlocked agent by then.
	var firstTok string
	var firstErr error
	first := cfg.Discord != nil
	if first {
		firstTok, firstErr = cfg.Discord.Token()
	}
	// build makes the backend of net on its own chan. A token command that
	// fails is the error of the launch: the UI shows it and starts nothing.
	build := func(net string, raw chan<- model.Event) (model.Backend, error) {
		switch net {
		case model.NetTelegram:
			return tgc.New(tgc.Config{AppID: tg.APIID, AppHash: tg.APIHash, BotToken: tg.BotToken,
				SessionPath: cfg.SessionPath()}, raw), nil
		case model.NetDiscord:
			tok, err := firstTok, firstErr
			if !first { // the first read is spent: the command or the file again
				tok, err = cfg.Discord.Token()
			}
			first = false
			if err != nil {
				return nil, err
			}
			return dsc.New(dsc.Config{Token: tok}, raw), nil
		}
		return nil, fmt.Errorf("%s: unknown network", net)
	}
	// launch wires a backend: its own chan, the forwarder that stamps the
	// network on every event, and the goroutine of its Run — an error or a
	// panic comes back as an event rather than killing the terminal.
	launch := func(nctx context.Context, net string) (model.Backend, error) {
		raw := make(chan model.Event, 256)
		b, err := build(net, raw)
		if err != nil {
			return nil, err
		}
		go func() {
			for {
				select {
				case <-nctx.Done():
					return
				case ev := <-raw:
					select {
					case events <- model.Envelope{Net: net, Ev: ev}:
					case <-nctx.Done():
						return
					}
				}
			}
		}()
		go func() {
			var stopped model.EvStopped
			// Straight into the UI chan, not through raw: when the context that
			// ended Run is the one of a logout, the forwarder is already gone.
			defer func() {
				if r := recover(); r != nil {
					stopped.Err = i18n.T("panic", r)
				}
				select {
				case events <- model.Envelope{Net: net, Ev: stopped}:
				case <-ctx.Done():
				}
			}()
			if err := b.Run(nctx); err != nil && nctx.Err() == nil {
				stopped.Err = err.Error()
			}
		}()
		return b, nil
	}
	return nets, caches, launch, nil
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nets, caches, launch, err := backends(ctx, cfg, events)
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

	return ui.Run(ctx, cancel, t, cfg, th, nets, launch, events, caches)
}
