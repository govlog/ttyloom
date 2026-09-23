// ttyloom is a terminal chat client for several networks.
package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/module"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
	"github.com/govlog/ttyloom/internal/ui"
	"github.com/govlog/ttyloom/protocols/dsc"
	"github.com/govlog/ttyloom/protocols/irc"
	"github.com/govlog/ttyloom/protocols/tgc"
)

var version = "dev"
var commit = "unknown"

// cleanParts removes download temp files left by a killed session — at the
// root, in maps/ and paste/, and in avatars/<net>/. Only the ones untouched
// for an hour: a second instance on the same download_dir may be writing the
// others.
func cleanParts(dir string) {
	for _, pat := range []string{"/.part-*", "/*/.part-*", "/*/*/.part-*"} {
		if m, _ := filepath.Glob(dir + pat); m != nil {
			for _, f := range m {
				if st, err := os.Lstat(f); err == nil && time.Since(st.ModTime()) > time.Hour {
					_ = os.Remove(f)
				}
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

// modules : the networks the client knows, in the order of their windows and
// of /net. Adding one is a line here.
func modules() []module.Module {
	return []module.Module{tgc.NewModule(), dsc.NewModule(), irc.NewModule()}
}

func run() error {
	mods := modules()
	cfg, err := config.Load(mods...)
	if err != nil {
		return err
	}
	cleanParts(config.Expand(cfg.DownloadDir))
	lang := cfg.Lang
	if lang == "" {
		lang = i18n.Detect(cmp.Or(os.Getenv("LC_ALL"), os.Getenv("LANG")))
	}
	i18n.Set(lang)
	n := 0
	for _, m := range mods {
		n += len(m.Networks())
	}
	if n == 0 {
		return fmt.Errorf(i18n.T("main_no_networks"), cfg.Path())
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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

	return ui.Run(ctx, cancel, t, cfg, th, mods...)
}
