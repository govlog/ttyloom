// ttyloom is a terminal chat client for several networks.
package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/module"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
	"github.com/govlog/ttyloom/internal/ui"
	"github.com/govlog/ttyloom/internal/update"
	"github.com/govlog/ttyloom/protocols/dsc"
	"github.com/govlog/ttyloom/protocols/irc"
	"github.com/govlog/ttyloom/protocols/tgc"
)

var version = "dev"
var commit = "unknown"

// cleanFiles removes what earlier sessions left in download_dir (dl) and in
// the cache, untouched for a while: download temp files of a killed session
// — at the root, in maps/ and paste/, and in avatars/<net>/ — after an hour
// (a second instance on the same download_dir may be writing the others);
// pasted images never sent (a failed send keeps its file, a quit at the
// prompt too) after a day; the pictures of the GIF box, the media browser and
// the emoji picker after a week: they are fetched again when they show.
func cleanFiles(dl, cache string) {
	const day = 24 * time.Hour
	for _, p := range []struct {
		pat string
		age time.Duration
	}{
		{dl + "/.part-*", time.Hour}, {dl + "/*/.part-*", time.Hour}, {dl + "/*/*/.part-*", time.Hour},
		{dl + "/paste/*", day},
		{cache + "/gifs/*/*", 7 * day}, {cache + "/thumbs/*/*", 7 * day}, {cache + "/emoji/*/*", 7 * day},
	} {
		m, _ := filepath.Glob(p.pat)
		for _, f := range m {
			if st, err := os.Lstat(f); err == nil && time.Since(st.ModTime()) > p.age {
				_ = os.Remove(f)
			}
		}
	}
}

func main() {
	showVersion := flag.Bool("version", false, "print the version, the build id and the source commit")
	flag.Parse()
	build := update.Current(version, commit)
	if *showVersion {
		fmt.Printf("ttyloom %s (build %s, commit %s)\n", build.Version, build.ID(), cmp.Or(build.Commit, "unknown"))
		return
	}
	if err := run(build); err != nil {
		fmt.Fprintln(os.Stderr, i18n.T("main_prefix"), err)
		os.Exit(1)
	}
}

// modules : the networks the client knows, in the order of their windows and
// of /net. Adding one is a line here.
func modules() []module.Module {
	return []module.Module{tgc.NewModule(), dsc.NewModule(), irc.NewModule()}
}

// load reads config.toml with the modules and sets the language of the
// texts — before it gives back the error of a module, which is translated
// when printed.
func load(mods []module.Module) (*config.Config, error) {
	cfg, err := config.Load(mods...)
	if cfg == nil {
		return nil, err
	}
	lang := cfg.Lang
	if lang == "" {
		lang = i18n.Detect(cmp.Or(os.Getenv("LC_ALL"), os.Getenv("LANG")))
	}
	i18n.Set(lang)
	return cfg, err
}

func run(build update.Build) error {
	mods := modules()
	cfg, err := load(mods)
	if err != nil {
		return err
	}
	cleanFiles(config.Expand(cfg.DownloadDir), config.CacheDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Closing the terminal (SIGHUP) or a kill (SIGTERM) quits like /quit: the
	// cache and the chat shown are written before the process ends.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGHUP, syscall.SIGTERM)
	defer stop()
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

	return ui.Run(ctx, cancel, t, cfg, th, build, mods...)
}
