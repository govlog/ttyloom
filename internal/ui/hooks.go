package ui

import (
	"cmp"
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/hook"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
)

// The guards of every hook. Constants: the spec leaves them out of /set.
const (
	hookMaxRuns  = 4               // runs of one hook at a time; one message more is skipped
	hookMaxSends = 10              // send replies of one hook a minute; one more is dropped
	hookMaxAge   = 2 * time.Minute // an older message was caught up after a cut: no hook
)

// hookStat : the counters of one hook, shown by /hooks, kept by name across
// a reload.
type hookStat struct {
	runs, fails, skipped, dropped int
	running                       int         // runs in flight
	sends                         []time.Time // send replies of the last minute
	last                          string      // ok or the error of the last run; "" never ran
	failing                       bool        // a failure line is in window 0 since the last success
}

// evHook : the end of a run, back on the goroutine of the UI.
type evHook struct {
	name  string
	reply hook.Reply    // as when the run started: a reload does not change it
	key   model.ChatKey // the chat of the message
	test  *Window       // /hooks test: the window that asked; nil for a message
	out   string
	err   error
}

// hookPath : hooks.toml, beside config.toml (TTYLOOM_DIR included).
func hookPath() string { return filepath.Join(config.Dir(), "hooks.toml") }

// loadHooks reads hooks.toml, and say gets its lines. A file that cannot be
// read keeps the hooks of before (none at start). The count is said at a
// reload, and at start when the file holds anything.
func (u *UI) loadHooks(say func(string), reload bool) {
	hs, rejected, err := hook.Load(hookPath())
	if err != nil {
		say(i18n.T("hooks_file_error", err))
		return
	}
	for _, e := range rejected {
		say(e.Error())
	}
	stats := make(map[string]*hookStat, len(hs))
	for _, h := range hs {
		if stats[h.Name] = u.hookStats[h.Name]; stats[h.Name] == nil {
			stats[h.Name] = &hookStat{}
		}
	}
	u.hooks, u.hookStats = hs, stats
	if reload || len(hs)+len(rejected) > 0 {
		say(i18n.T(i18n.Plural(len(hs), "hooks_loaded"), len(hs)))
	}
	for _, h := range hs {
		if h.Reply != hook.ReplySend {
			continue
		}
		for _, m := range u.mods {
			if wr, ok := m.(module.AutoReplyWarner); ok && u.hookReaches(h, m.Name()) {
				say(i18n.T("hook_note", h.Name, wr.AutoReplyWarning()))
			}
		}
	}
}

// runHooks fires every hook that takes m, a message just come in live in c,
// in the order of the file. Never a message of mine — the anti-loop: the
// reply of a hook is one — nor a service line, a notice, or a message caught
// up after a cut.
func (u *UI) runHooks(c *model.Chat, m *model.Msg) {
	if len(u.hooks) == 0 || u.own(m) || m.Service != "" || m.Notice || time.Since(m.Date) > hookMaxAge {
		return
	}
	hm := u.hookMsg(c, m)
	for _, h := range u.hooks {
		if got, ok := hook.Match(h, hm); ok {
			u.fireHook(h, hm, got, c.Key(), nil)
		}
	}
}

// hookMsg : m, of chat c, as the filters see it.
func (u *UI) hookMsg(c *model.Chat, m *model.Msg) hook.Msg {
	me := u.selfOf(c.Net)
	byName := backendCaps(u.netOf(c.Net)).NameIsID
	return hook.Msg{Net: c.Net, Chat: c.Title, ChatID: c.ID, Kind: hook.KindOf(c.Kind), From: m.From, FromID: m.FromID,
		ID: m.ID, Text: m.Text, NameIsID: byName,
		Mention: mentionsMe(m, me.ID, me.Name) || (byName && nameSaid(m.Text, me.Name))}
}

// nameSaid : name as a whole word of text, case apart — how IRC calls on
// someone ("chris: hello"), with no @.
// ponytail: compiled for each message the hooks look at; cache it on the
// name if a busy network ever makes it show.
func nameSaid(text, name string) bool {
	if name == "" {
		return false
	}
	return regexp.MustCompile(`(?i)(?:^|\W)` + regexp.QuoteMeta(name) + `(?:$|\W)`).MatchString(text)
}

// fireHook starts h on m in a goroutine of its own; the end comes back as an
// evHook. With hookMaxRuns runs in flight the message is skipped and counted.
// test is the window of /hooks test: no guard, no counter.
func (u *UI) fireHook(h hook.Hook, m hook.Msg, c hook.Captures, key model.ChatKey, test *Window) {
	if test == nil {
		st := u.hookStats[h.Name]
		if st.running >= hookMaxRuns {
			st.skipped++
			u.event(model.EvLog{Level: "WARN", Msg: i18n.T("hook_busy", h.Name, hookMaxRuns)})
			return
		}
		st.running++
		st.runs++
	}
	env, ctx := hook.Env(h, m, c), u.ctx
	u.hookWait.Add(1)
	go func() {
		defer u.hookWait.Done()
		ev := evHook{name: h.Name, reply: h.Reply, key: key, test: test}
		func() {
			defer func() { // a panic here would take the terminal down
				if r := recover(); r != nil {
					ev.err = errors.New(i18n.T("panic", r))
				}
			}()
			ev.out, ev.err = hook.Run(ctx, h, env, m.Text)
		}()
		select {
		case u.events <- ev:
		case <-ctx.Done(): // /quit: nobody reads the events any more
		}
	}()
}

// hookDone : the end of a run — its counters, then its reply.
func (u *UI) hookDone(e evHook) {
	if e.test != nil {
		u.hookTested(e)
		return
	}
	st := u.hookStats[e.name]
	if st == nil {
		return // the hook left hooks.toml meanwhile (/hooks reload)
	}
	st.running--
	if e.err != nil {
		st.fails++
		st.last = e.err.Error()
		u.event(model.EvLog{Level: "WARN", Msg: i18n.T("hook_note", e.name, st.last)})
		if !st.failing { // one line in window 0 per series of failures
			st.failing = true
			u.status0(i18n.T("hook_failed", e.name, st.last))
		}
		return
	}
	st.last, st.failing = i18n.T("hook_ok"), false
	out, c := hook.Clean(e.out), u.chats[e.key]
	switch {
	case out == "" || e.reply == hook.ReplyNone:
	case c == nil: // chat left or blocked meanwhile
		u.event(model.EvLog{Level: "WARN", Msg: i18n.T("hook_reply_lost", e.name)})
	case e.reply == hook.ReplySend:
		u.hookSend(e.name, st, c, out)
	case e.reply == hook.ReplyDraft:
		u.hookDraft(e.name, c, out)
	default:
		u.hookDisplay(u.winFor(c), e.name, out)
	}
}

// hookSend : a send reply, as a message of mine in c, by a path of its own:
// the reply being prepared, the input and the typing signal stay as they are.
func (u *UI) hookSend(name string, st *hookStat, c *model.Chat, text string) {
	b := u.net(c)
	if b == nil {
		u.event(model.EvLog{Level: "WARN", Msg: i18n.T("hook_reply_lost", name)})
		return
	}
	now := time.Now()
	st.sends = slices.DeleteFunc(st.sends, func(t time.Time) bool { return now.Sub(t) >= time.Minute })
	if len(st.sends) >= hookMaxSends {
		st.dropped++
		u.event(model.EvLog{Level: "WARN", Msg: i18n.T("hook_send_dropped", name, hookMaxSends)})
		return
	}
	st.sends = append(st.sends, now)
	w := u.winFor(c)
	u.tmpID++
	me := u.selfOf(c.Net)
	m := &model.Msg{Net: c.Net, ChatID: c.ID, ChatLabel: c.Title, Date: now, From: me.Name, FromID: me.ID,
		Out: true, Text: text, Pending: true, TmpID: u.tmpID}
	u.noteActivity(c, m)
	w.Upsert(m)
	u.agg.Upsert(m)
	b.Send(u.backendContext(b), c, text, u.tmpID)
}

// hookDraft : a draft reply goes into the input of the window of c when it is
// empty — the editor when that window is shown, its Draft otherwise, taken
// back at the visit. My text is never replaced, nor a mode I am in (a reply
// being prepared would quote a message the draft was not written for): then
// it shows as display.
func (u *UI) hookDraft(name string, c *model.Chat, text string) {
	w := u.winFor(c)
	if w == u.view() {
		if u.ed.String() == "" && u.edit == nil && u.reply == nil && u.prompt == nil && u.sendAsk == nil && u.pasteAsk == "" {
			u.ed.Set(text)
			return
		}
	} else if w.Draft == "" {
		w.Draft = text
		w.Act++
		return
	}
	u.hookDisplay(w, name, text)
}

// hookDisplay : each line of text as a system line of w, after the name of
// the hook; a window not shown counts it as activity.
func (u *UI) hookDisplay(w *Window, name, text string) {
	for _, l := range strings.Split(text, "\n") {
		w.AddSys("[" + name + "] " + l)
	}
	if w != u.view() {
		w.Act++
	}
}

// hookReaches : h can fire on a configured network of module mod.
func (u *UI) hookReaches(h hook.Hook, mod string) bool {
	return slices.ContainsFunc(u.netList, func(n string) bool {
		return model.NetModule(n) == mod && (h.Net == "" || h.Net == n || h.Net == mod)
	})
}

// hooksCmd : /hooks, /hooks reload, /hooks test <name> <text>.
func (u *UI) hooksCmd(w *Window, args []string, text string) {
	switch {
	case len(args) == 0:
		u.hooksList(w)
	case args[0] == "reload" && len(args) == 1:
		u.loadHooks(w.AddSys, true)
	case args[0] == "test" && len(args) >= 2:
		_, rest, _ := strings.Cut(text, " ")                    // after "test"
		_, body, _ := strings.Cut(strings.TrimSpace(rest), " ") // after the name
		u.hookTest(w, args[1], strings.TrimSpace(body))
	default:
		w.AddSys(i18n.T("usage_hooks"))
	}
}

// hooksList : one line per hook — name, filters, reply, counters, last result.
func (u *UI) hooksList(w *Window) {
	if len(u.hooks) == 0 {
		w.AddSys(i18n.T("hooks_none", hookPath()))
		return
	}
	for _, h := range u.hooks {
		st := u.hookStats[h.Name]
		w.AddSys(i18n.T("hook_row", h.Name, hookScope(h), h.Reply, st.runs, st.fails, st.skipped, st.dropped,
			cmp.Or(st.last, i18n.T("hook_never"))))
	}
}

// hookScope : the filters of h in a few words; "*" when it takes every message.
func hookScope(h hook.Hook) string {
	var s []string
	if h.Net != "" {
		s = append(s, h.Net)
	}
	if len(h.Chats) > 0 {
		s = append(s, strings.Join(h.Chats, ","))
	}
	if len(h.Kinds) > 0 {
		s = append(s, strings.Join(h.Kinds, ","))
	}
	if len(h.From) > 0 {
		s = append(s, "from:"+strings.Join(h.From, ","))
	}
	if h.Mention {
		s = append(s, "@me")
	}
	if h.Match != nil {
		s = append(s, "/"+h.Match.String()+"/")
	}
	if len(s) == 0 {
		return "*"
	}
	return strings.Join(s, " ")
}

// hookTest : /hooks test — h runs on text as if I wrote it in the chat of w
// (none in window 0). Only match is checked; the end shows in w, never sent.
func (u *UI) hookTest(w *Window, name, text string) {
	i := slices.IndexFunc(u.hooks, func(h hook.Hook) bool { return h.Name == name })
	if i < 0 {
		w.AddSys(i18n.T("hook_unknown", name))
		return
	}
	h, m, key := u.hooks[i], hook.Msg{Text: text}, model.ChatKey{}
	if c := w.Chat; c != nil {
		me := u.selfOf(c.Net)
		m = hook.Msg{Net: c.Net, Chat: c.Title, ChatID: c.ID, Kind: hook.KindOf(c.Kind), From: me.Name, FromID: me.ID,
			Text: text, NameIsID: backendCaps(u.netOf(c.Net)).NameIsID}
		key = c.Key()
	}
	got, ok := hook.Match(hook.Hook{Match: h.Match}, m)
	if !ok {
		w.AddSys(i18n.T("hook_test_nomatch", name))
		return
	}
	u.fireHook(h, m, got, key, w)
}

// hookTested : the end of a /hooks test, in the window that asked.
func (u *UI) hookTested(e evHook) {
	if e.err != nil {
		e.test.AddSys(i18n.T("hook_test_failed", e.name, e.err))
		return
	}
	out := hook.Clean(e.out)
	if out == "" {
		e.test.AddSys(i18n.T("hook_test_empty", e.name))
		return
	}
	u.hookDisplay(e.test, e.name, out)
}
