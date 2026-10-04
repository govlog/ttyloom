package ui

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/update"
)

// updateAPI : root of the GitHub API the update check asks; the tests point
// it at a local server.
var updateAPI = "https://api.github.com"

// evUpdate : answer of the update check. manual: asked by /set update_check
// on, which tells "up to date" and the errors too.
type evUpdate struct {
	rel    update.Release
	newer  bool
	err    error
	manual bool
}

// checkUpdate asks GitHub for the newest release in the background: the start
// never waits on the network, and the answer comes back as an event.
func (u *UI) checkUpdate(manual bool) {
	b, ctx := u.build, u.ctx
	go func() {
		ev := evUpdate{manual: manual}
		func() {
			defer func() { // a panic here would take the terminal down
				if r := recover(); r != nil {
					ev.err = errors.New(i18n.T("panic", r))
				}
			}()
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			ev.rel, ev.newer, ev.err = update.Check(ctx, http.DefaultClient, updateAPI, b)
		}()
		select {
		case u.events <- ev:
		case <-ctx.Done(): // /quit: nobody reads the events any more
		}
	}()
}

// updateDone shows the answer in window 0. At start, a failure only goes to
// the log: an offline start is not an error.
func (u *UI) updateDone(e evUpdate) {
	switch {
	case e.err != nil && e.manual:
		u.status0(i18n.T("update_error", render.CleanLine(e.err.Error())))
	case e.err != nil:
		u.event(model.EvLog{Level: "WARN", Msg: i18n.T("update_error", e.err)})
	case e.newer:
		u.status0(i18n.T("update_available", e.rel.Tag, e.rel.ID(), u.build.ID(), e.rel.URL))
	case e.manual:
		u.status0(i18n.T("update_none", u.build.Version, u.build.ID()))
	}
}
