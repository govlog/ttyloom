package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/spell"
)

// setting : one key of /set, described once. values gives what Tab offers
// (nil: free text); show gives the line "/set <key>" prints; set applies
// "/set <key> <v>" and tells whether the value was taken — a refused value is
// told in w and nothing is saved.
type setting struct {
	key    string
	values func() []string
	show   func(u *UI) string
	set    func(u *UI, w *Window, v string) bool
}

func fixed(v ...string) func() []string { return func() []string { return v } }

var onOffValues = fixed("on", "off")

// boolSetting : an on/off key kept in a bool of the configuration; after,
// when given, runs once the value is set.
func boolSetting(key string, field func(*config.Config) *bool, after func(*UI)) setting {
	return setting{key, onOffValues,
		func(u *UI) string { return fmt.Sprintf("%s = %v", key, *field(u.cfg)) },
		func(u *UI, _ *Window, v string) bool {
			*field(u.cfg) = onOff(v)
			if after != nil {
				after(u)
			}
			return true
		}}
}

// choiceSetting : a key with a closed choice of values, offered by Tab and
// checked on /set; refuse gives the message of a value outside it, apply runs
// with an accepted one.
func choiceSetting(key string, values []string, refuse func() string, show func(*UI) string, apply func(*UI, string)) setting {
	return setting{key, fixed(values...), show,
		func(u *UI, w *Window, v string) bool {
			if !slices.Contains(values, v) {
				w.AddSys(refuse())
				return false
			}
			apply(u, v)
			return true
		}}
}

// settings : the /set table, in the order "/set" alone lists the keys.
var settings = []setting{
	boolSetting("timestamps", func(c *config.Config) *bool { return &c.Timestamps }, (*UI).clear),
	boolSetting("timestamps_seconds", func(c *config.Config) *bool { return &c.TimestampsSeconds }, (*UI).clear), // the prefix changes width: everything is wrapped again
	choiceSetting("cycle_mode", []string{"next", "last_unread"}, func() string { return i18n.T("set_cycle_mode_values") },
		func(u *UI) string { return "cycle_mode = " + u.cfg.CycleMode },
		func(u *UI, v string) { u.cfg.CycleMode, u.cycleHome = v, nil }),
	boolSetting("multiline", func(c *config.Config) *bool { return &c.Multiline }, func(u *UI) {
		if !u.cfg.Multiline {
			u.multi = false
		}
	}),
	boolSetting("link_previews", func(c *config.Config) *bool { return &c.LinkPreviews }, (*UI).clear), // the "│" block shows or goes: lines to draw again
	boolSetting("maps", func(c *config.Config) *bool { return &c.Maps }, (*UI).reloadMedia),            // turned on: the shown locations move to downloading
	{"hover", fixed("menu", "highlight", "off"),
		func(u *UI) string { return fmt.Sprintf("hover = %v", u.cfg.Hover) },
		func(u *UI, w *Window, v string) bool {
			if v == "on" { // old alias: on = menu
				v = "menu"
			}
			if v != "menu" && v != "highlight" && v != "off" {
				w.AddSys(i18n.T("set_hover_values"))
				return false
			}
			u.cfg.Hover = config.HoverMode(v)
			u.hover.Invalidate() // the drawing (background/help) depends on the mode
			// Zone dropped at once: "off" must leave nothing lit, and the mode just
			// changed under a pointer that is not moving. zoneAt recomputes it at
			// the next move.
			u.zone = zoneNone
			if u.cfg.Hover == config.HoverOff {
				u.hover = nil
			}
			return true
		}},
	choiceSetting("images", []string{"auto", "kitty", "halfblock", "off"}, func() string { return i18n.T("set_images_values") },
		func(u *UI) string { return i18n.T("set_images_effective", u.cfg.Images, u.images) },
		(*UI).applyImages),
	boolSetting("images_hover", func(c *config.Config) *bool { return &c.ImagesHover }, (*UI).clear), // the lines kept free show or go
	choiceSetting("video", []string{"show", "hidden", "autoplay"}, func() string { return i18n.T("set_video_values") },
		func(u *UI) string { return "video = " + u.cfg.Video },
		func(u *UI, v string) {
			u.cfg.Video = v
			u.reloadMedia() // the lines kept free and the frames decoded both change
		}),
	choiceSetting("gifplay", []string{"always", "hover", "off"}, func() string { return i18n.T("set_gifplay_values") },
		func(u *UI) string { return "gifplay = " + u.cfg.GifPlay },
		func(u *UI, v string) {
			u.cfg.GifPlay = v
			if v == "off" {
				u.gifRewind() // the frames stay: no reload for a display setting
			}
		}),
	{"avatars", onOffValues,
		func(u *UI) string { return i18n.T("set_avatars_effective", u.cfg.Avatars, u.avatarsOn()) },
		func(u *UI, _ *Window, v string) bool {
			u.cfg.Avatars = onOff(v)
			u.clear() // the gutter shows or goes: lines to draw again
			return true
		}},
	{"auto_media_max_kb", nil,
		func(u *UI) string { return fmt.Sprintf("auto_media_max_kb = %d", u.cfg.AutoMediaMaxKB) },
		func(u *UI, w *Window, v string) bool {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > config.MaxAutoMediaKB {
				w.AddSys(i18n.T("number_expected_kb", config.MaxAutoMediaKB))
				return false
			}
			u.cfg.AutoMediaMaxKB = n
			return true
		}},
	{"download_dir", nil,
		func(u *UI) string { return "download_dir = " + u.cfg.DownloadDir },
		func(u *UI, _ *Window, v string) bool { u.cfg.DownloadDir = v; return true }},
	boolSetting("bell", func(c *config.Config) *bool { return &c.Bell }, nil),
	choiceSetting("notify", []string{"terminal", "desktop", "off"}, func() string { return i18n.T("set_notify_values") },
		func(u *UI) string { return "notify = " + u.cfg.Notify },
		func(u *UI, v string) { u.cfg.Notify = v }),
	{"auto_open_days", nil,
		func(u *UI) string { return fmt.Sprintf("auto_open_days = %d", u.cfg.AutoOpenDays) },
		func(u *UI, w *Window, v string) bool {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				w.AddSys(i18n.T("number_expected_days"))
				return false
			}
			u.cfg.AutoOpenDays = n
			return true
		}},
	{"aggregate", onOffValues,
		func(u *UI) string { return fmt.Sprintf("aggregate = %v", u.aggregate) },
		func(u *UI, _ *Window, v string) bool { u.setAggregate(onOff(v)); return true }},
	boolSetting("tabs", func(c *config.Config) *bool { return &c.Tabs }, (*UI).clear), // the bar shows or goes: the view changes height
	boolSetting("log", func(c *config.Config) *bool { return &c.Log }, func(u *UI) { u.ws.Log = u.cfg.Log }),
	{"log_dir", nil,
		func(u *UI) string { return "log_dir = " + u.cfg.LogDir },
		func(u *UI, _ *Window, v string) bool { u.cfg.LogDir = v; return true }},
	boolSetting("separator", func(c *config.Config) *bool { return &c.Separator }, (*UI).clear), // the separator line shows or goes: the view changes height
	boolSetting("redline", func(c *config.Config) *bool { return &c.Redline }, (*UI).clear),     // the redline shows or goes: the view changes height
	choiceSetting("sidebar_sort", []string{"recent", "alpha", "unread"}, func() string { return i18n.T("set_sidebar_sort_values") },
		func(u *UI) string { return "sidebar_sort = " + u.cfg.SidebarSort },
		func(u *UI, v string) {
			u.cfg.SidebarSort = v
			u.sideScroll = 0
			u.marquee = marqueeState{}
		}),
	boolSetting("sidebar_split", func(c *config.Config) *bool { return &c.SidebarSplit }, func(u *UI) { u.sideScroll = 0 }),
	{"sidebar_width", nil,
		func(u *UI) string { return fmt.Sprintf("sidebar_width = %d", u.cfg.SidebarWidth) },
		func(u *UI, w *Window, v string) bool {
			n, err := strconv.Atoi(v)
			if err != nil {
				w.AddSys(i18n.T("number_expected_width"))
				return false
			}
			u.sideW = clampSideW(n, u.t.Cols)
			u.cfg.SidebarWidth = u.sideW
			u.clear() // the message area changes width: everything is wrapped again
			return true
		}},
	{"spell", func() []string { return append([]string{"off", "us"}, spell.Available(spell.DictDir)...) }, // a chain ("fr+us") is typed by hand
		func(u *UI) string { return "spell = " + u.cfg.Spell },
		func(u *UI, _ *Window, v string) bool {
			if err := u.applySpell(v); err != nil { // resolved against spell.DictDir
				return false // refused mode: told there, nothing saved
			}
			u.cfg.Spell = v
			return true
		}},
	boolSetting("spell_quotes", func(c *config.Config) *bool { return &c.SpellQuotes }, (*UI).spellDirty),
	{"kitty_images", nil,
		func(u *UI) string { return fmt.Sprintf("kitty_images = %d", u.cfg.KittyImages) },
		func(u *UI, w *Window, v string) bool {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				w.AddSys(i18n.T("number_expected_positive"))
				return false
			}
			u.cfg.KittyImages = n
			return true
		}},
	{"cache_messages", nil,
		func(u *UI) string { return i18n.T("set_cache_messages", u.cfg.CacheMessages) },
		func(u *UI, w *Window, v string) bool {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				w.AddSys(i18n.T("number_expected_positive"))
				return false
			}
			u.cfg.CacheMessages = n
			u.setMaxItems(n) // the memory of the windows follows at once
			return true
		}},
	{"lang", i18n.Langs, // a chain ("fr+en") is typed by hand
		func(u *UI) string { return "lang = " + i18n.Lang() },
		func(u *UI, w *Window, v string) bool {
			langs := i18n.Langs()
			for _, l := range strings.Split(v, "+") { // "fr+en": fallback chain
				if !slices.Contains(langs, l) {
					w.AddSys(i18n.T("lang_unknown", l, strings.Join(langs, ", ")))
					return false
				}
			}
			u.cfg.Lang = v
			u.relang(v)
			u.clear() // the drawn texts change language
			return true
		}},
}

// setKeys : the keys /set knows, in the order of the table.
var setKeys = func() []string {
	ks := make([]string, len(settings))
	for i, s := range settings {
		ks[i] = s.key
	}
	return ks
}()

// settingOf gives the entry of a key, nil for an unknown one.
func settingOf(key string) *setting {
	for i := range settings {
		if settings[i].key == key {
			return &settings[i]
		}
	}
	return nil
}

// setValues gives the valid values of a /set key with a closed choice, nil otherwise.
func setValues(key string) []string {
	if s := settingOf(key); s != nil && s.values != nil {
		return s.values()
	}
	return nil
}

// setCmd : /set — every key with its value, one key, or an assignment that
// is saved and echoed.
func (u *UI) setCmd(args []string) {
	w := u.view()
	if len(args) == 0 {
		for _, s := range settings {
			w.AddSys(s.show(u))
		}
		return
	}
	s := settingOf(args[0])
	if s == nil { // "/set foobar" alone was mute
		w.AddSys(i18n.T("unknown_key", args[0]))
		return
	}
	if len(args) > 1 {
		if !s.set(u, w, strings.Join(args[1:], " ")) {
			return
		}
		u.saveCfg()
	}
	u.view().AddSys(s.show(u)) // the view may have changed (aggregate): shown where the user looks
}
