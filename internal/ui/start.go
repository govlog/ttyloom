package ui

import (
	"context"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/cache"
	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/module"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

// newUI builds the UI and starts it: the files of the user read, the
// networks of each module listed, given their cache and launched, the cache
// loaded, the first frame drawn. Run adds the loop.
func newUI(ctx context.Context, cancel context.CancelFunc, t *term.Term, cfg *config.Config, th theme.Theme, mods []module.Module) *UI {
	u := &UI{ctx: ctx, cancel: cancel, t: t, cfg: cfg, th: th, nets: map[string]model.Backend{}, mods: mods, envs: make(chan model.Envelope, 256),
		netCancel: map[string]context.CancelFunc{}, events: make(chan model.Event, 256), ws: NewWindows(),
		agg: &Window{}, aggregate: cfg.Aggregate, debug: &Window{}, focused: true,
		chats: map[model.ChatKey]*model.Chat{}, lookups: map[uint64]*lookup{}, typing: map[model.ChatKey]typing{}, lastTyping: map[model.ChatKey]time.Time{},
		avatars: map[model.ChatKey]*model.Media{}, openNext: map[*model.Media]bool{}, fulls: map[*model.Media]bool{}, presence: map[model.ChatKey]string{},
		caches: map[string]*cache.Cache{}, dirty: map[model.ChatKey]bool{}, partsCache: map[model.ChatKey]partsEntry{}, whoCache: map[whoKey]whoEntry{},
		sideW:       clampSideW(cfg.SidebarWidth, t.Cols),
		aliases:     map[model.ChatKey]string{},
		folded:      map[string]bool{},
		self:        map[string]selfInfo{},
		conn:        map[string]bool{},
		dialogsSeen: map[string]bool{},
		tabLast:     map[string]*Window{},
		reactList:   map[string][]string{}}
	u.ws.Log = cfg.Log
	u.setMaxItems(cfg.CacheMessages)
	u.images = u.resolveImages(cfg.Images)
	u.status0(i18n.T("banner_help"))
	if len(cfg.Unknown) > 0 {
		u.status0(i18n.T("config_unknown_keys", strings.Join(cfg.Unknown, ", ")))
	}
	u.status0(i18n.T("banner_terminal", t.Cols, t.Rows, t.CellW, t.CellH, t.Kitty, t.KittyKbd, u.images, th.Name))
	if al, err := loadAliases(aliasPath()); err != nil {
		u.status0(i18n.T("aliases_error", err)) // unreadable file: we start with no local names
	} else {
		u.aliases = al
	}
	if f, err := loadFolds(sidebarPath()); err != nil {
		u.status0(i18n.T("sidebar_error", err)) // unreadable file: we start with everything unfolded
	} else {
		u.folded = f
	}
	u.launch = u.launchModule
	for _, m := range mods {
		for _, net := range m.Networks() {
			u.netList = append(u.netList, net)
			u.addCache(m, net)
		}
	}
	for _, n := range u.netList {
		u.startNet(n)
	}
	u.loadHooks(u.status0, false)
	if len(u.netList) == 0 { // first start: the hub says what to add
		u.openHub()
	}
	u.loadCache() // no cache at all: the loop over u.caches has nothing to read
	u.applySpell(cfg.Spell)
	u.clear()
	u.draw()
	return u
}
