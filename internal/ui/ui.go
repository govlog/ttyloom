// Package ui : ircii engine. Only one goroutine touches the state.
package ui

import (
	"context"
	"fmt"
	"image"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/govlog/ttyloom/internal/cache"
	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/spell"
	"github.com/govlog/ttyloom/internal/term"
	"github.com/govlog/ttyloom/internal/theme"
)

type typing struct {
	who   string
	until time.Time
}

type placed struct {
	row  int
	pid  uint32 // kitty placement: line+1 inline, hoverPID on top
	img  *render.Img
	crop image.Rectangle // zoomed preview: source sub-rectangle, empty elsewhere
}

type UI struct {
	ctx       context.Context
	cancel    context.CancelFunc
	t         *term.Term
	cfg       *config.Config
	th        theme.Theme
	nets      map[string]model.Backend      // network name → live backend (started, not stopped)
	netList   []string                      // configured networks, sorted: the ones /<net> login can start
	launch    model.Launcher                // builds and runs one of them, given by Run
	netCancel map[string]context.CancelFunc // ends the Run of a live network (/<net> logout)
	events    chan model.Event
	ws        *Windows
	agg       *Window // aggregated view of window 0: never in ws.List
	aggregate bool    // window 0 shows the aggregate (Alt+A)
	debug     *Window // internal log (backend WARN/ERROR, panics): never in ws.List
	showDebug bool    // window 0 shows the log (/debug), with no saving
	// cycleHome : window left by the first Ctrl+X of a last_unread tour, taken
	// back once nothing is unread any more (nil outside a tour).
	cycleHome *Window
	ed        Editor
	chats     map[model.ChatKey]*model.Chat
	chatList  []*model.Chat
	aliases   map[model.ChatKey]string // local names (/rename), saved in aliases.toml
	pending   map[string]*Window       // /query waiting for a lookup
	prompt    *model.EvAuthPrompt
	promptNet string // network that asked u.prompt: its stop takes the prompt away
	qr        *qrBox // QR code login running: passive overlay
	// self : identity of the account per network (EvReady). Zero value while
	// the network has not answered yet — id 0 owns nothing, name empty, not a
	// bot, which is what the interface showed before any connection.
	self   map[string]selfInfo
	images string // effective mode: kitty | halfblock | off
	// cellWait : the mode fell back to half blocks only because the cell size
	// is not known yet. Some terminals give it neither in the answer to
	// CSI 16 t nor in the pixels of TIOCGWINSZ before the window is really
	// laid out, which is the very first start in a new window: the wanted
	// mode is taken back at the first size that carries the cell.
	cellWait   bool
	typing     map[model.ChatKey]typing
	lastTyping map[model.ChatKey]time.Time // chat → last typing signal sent (throttle 5 s)
	// presence and avatars are keyed by peer, not by chat: user ids collide
	// between networks just like chat ids, so the ChatKey there carries the net
	// and the id of the peer.
	presence   map[model.ChatKey]string       // peer → online, seen yesterday… (status bar)
	avatars    map[model.ChatKey]*model.Media // peer → avatar; nil = no photo
	partsCache map[model.ChatKey]partsEntry   // members per chat, valid for 5 min
	kittyID    uint32                         // last kitty id given out; it never goes back down
	kittyLRU   lru                            // images alive in the terminal, cap kitty_images
	framesLRU  lru                            // media holding decoded frames, least recently shown first (framesBudget)
	kplaced    map[kplace]bool                // kitty placements on the screen (frame before), for the diff
	kittyOld   []uint32                       // ids made stale by a new decoding, freed at the end of the frame
	tmpID      int64
	conn       map[string]bool // connection state per network, keyed like u.self
	focused    bool            // terminal in front (CSI ?1004): otherwise the read is put off
	placed     []placed
	hits       []rowHit            // map of the clicks of the message area, made again at each draw
	drag       dragMode            // drag running: scrollbar or text selection
	selAnchor  *Item               // message of the press that opened the selection drag
	selEnd     *Item               // end of the selected range; nil = mouse not moved yet (click)
	selY       int                 // line of the press: a drag starts when it leaves it
	hover      *Item               // message under the pointer (hover), window shown
	zone       zone                // area under the pointer: what the wheel acts on (follow-mouse)
	who        *whoBox             // hover popup: who read (tick) or who reacted
	whoCache   map[whoKey]whoEntry // its answers, valid whoTTL
	lastClick  struct {            // last left button press on a message, double click detection
		item *Item
		at   time.Time
	}
	openNext map[*model.Media]bool
	// decodes : decoding running per media, to be ended when nobody waits for
	// it any more ("s", window closed, media freed, preview closed).
	decodes map[*model.Media]context.CancelFunc
	// pendingMsg : /msg with no window — tmpID → title of the chat. Keyed by a
	// bare id and not by ChatKey: a tmpID comes from u.tmpID, minted here, so it
	// is unique whatever the network.
	pendingMsg  map[int64]string
	clock       string
	flashMsg    string                // temporary message of the status bar (copy)
	flashUntil  time.Time             // end of the display of flashMsg
	pager       *pager                // page waiting to be shown, only one at a time
	pasteAsk    string                // multiline paste waiting for a decision (e/c/a)
	pasteIns    bool                  // pasteAsk in the expanded editor: the choice inserts, it never sends
	multi       bool                  // expanded input zone (/set multiline + Shift+Enter)
	sendAsk     *sendAsk              // pasted image waiting for a decision (e/l/a)
	selShow     bool                  // next draw: move the view to the selected message
	edit        *Item                 // message being edited (the input carries its text)
	editText    string                // text of the last edit sent, taken back when it fails
	reply       *Item                 // message the next input answers
	ask         *confirm              // confirmation (y/n) waiting: delete, block…
	menu        *ctxMenu              // context menu of the sidebar (right click): it takes everything
	parts       *partsBox             // member box shown (F3), nil = nothing to show
	mention     *mentionBox           // @… box above the input, nil = closed
	mentionMute int                   // 1+start of the @word muted by Esc; 0 = none
	spell       spellChecker          // nil = off (config, error, or nospell build)
	spellText   string                // input of the last spell scan
	spellCache  []spell.Range         // its wrong words
	spellFix    *spellFixBox          // correction box (Ctrl+R or right click)
	inMap       inputMap              // input geometry of the last frame (mouse)
	partsOn     bool                  // box asked for: it follows the shown window
	picker      *picker               // emoji picker open: it takes every key
	themePick   *themePicker          // theme picker open: it takes every key
	reactList   map[string][]string   // reactions the account can use per network (EvReactionsList)
	viewer      *viewer               // full screen preview open: it takes every key
	search      *searchState          // Ctrl+F search running in the shown window
	gsearch     *globalSearch         // 2nd Ctrl+F: overlay of the server results, it takes everything
	newChat     *newChatBox           // "new chat" overlay: it takes everything
	gifs        *gifBox               // GIF box (Ctrl+G): it takes everything
	gifOrphan   map[*model.Media]bool // previews of a closed GIF box whose download still comes
	contacts    []*model.Chat         // contacts of the account (contacts.getContacts), source of the overlay
	gotContacts bool                  // contacts already asked for: once per session
	jump        struct {              // message to join at the next history (global result)
		chat  model.ChatKey
		msgID int
	}
	// netFilter : network the sidebar and the aggregate are cut to (/net),
	// "" = all. Always empty with a single backend.
	netFilter  string
	side       sideMode        // sidebar (F2)
	sideW      int             // width of its content (sidebar_width, drag of the bar)
	sideScroll int             // first entry shown in the sidebar
	folded     map[string]bool // folded sidebar sections, by key (sidebar.toml); nil = no section
	marquee    marqueeState    // scrolling title of the current sidebar line
	// dialogsSeen : networks whose first chat list has come. It fires the
	// once-per-session triggers (automatic opening, sync) per network, and not
	// once for the whole session.
	dialogsSeen map[string]bool
	caches      map[string]*cache.Cache // one disk cache per network, empty: cache off (cache = false)
	cacheSelf   map[string]int64        // per network, account that wrote the cache read at start
	cached      map[model.ChatKey]bool
	dirty       map[model.ChatKey]bool
	// bgWait : the cache writes in flight (bg). Waiting on it is what lets a
	// test read back what a write left, instead of racing it.
	bgWait    sync.WaitGroup
	flushed   time.Time     // last write of the cache
	syncQueue []*model.Chat // chats left to sync
	syncCur   *model.Chat   // step in flight (nil: none)
	syncTotal int           // size of the queue at the start, for the progress
	syncNew   int           // messages brought back by the sync

	// dispatchNet : network of the envelope being dispatched. The events that
	// name a chat by a bare id read it back. UI goroutine only.
	dispatchNet string
}

// Run drives the UI. netList names the configured networks, launch starts one
// of them (at start here, then on /<net> login); caches are keyed by network
// name and events carries the envelopes of every backend, fanned in by main.
func Run(ctx context.Context, cancel context.CancelFunc, t *term.Term, cfg *config.Config, th theme.Theme,
	netList []string, launch model.Launcher, events <-chan model.Envelope, caches map[string]*cache.Cache) error {
	u := &UI{ctx: ctx, cancel: cancel, t: t, cfg: cfg, th: th, nets: map[string]model.Backend{}, netList: netList, launch: launch,
		netCancel: map[string]context.CancelFunc{}, events: make(chan model.Event, 256), ws: NewWindows(),
		agg: &Window{}, aggregate: cfg.Aggregate, debug: &Window{}, focused: true,
		chats: map[model.ChatKey]*model.Chat{}, pending: map[string]*Window{}, typing: map[model.ChatKey]typing{}, lastTyping: map[model.ChatKey]time.Time{},
		avatars: map[model.ChatKey]*model.Media{}, openNext: map[*model.Media]bool{}, pendingMsg: map[int64]string{}, presence: map[model.ChatKey]string{},
		caches: caches, dirty: map[model.ChatKey]bool{}, partsCache: map[model.ChatKey]partsEntry{}, whoCache: map[whoKey]whoEntry{},
		sideW:       clampSideW(cfg.SidebarWidth, t.Cols),
		aliases:     map[model.ChatKey]string{},
		folded:      map[string]bool{},
		self:        map[string]selfInfo{},
		conn:        map[string]bool{},
		dialogsSeen: map[string]bool{},
		reactList:   map[string][]string{}}
	u.ws.Log = cfg.Log
	u.setMaxItems(cfg.CacheMessages)
	// Exit (/quit, Ctrl+C, end of the terminal): the cache goes to the disk
	// before main gives the terminal back.
	defer u.flushCache(true)
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
	for _, n := range netList {
		u.startNet(n)
	}
	u.loadCache() // no cache at all: the loop over u.caches has nothing to read
	u.applySpell(cfg.Spell)
	u.clear()
	u.draw()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case k, ok := <-t.Keys():
			if !ok {
				return nil
			}
			switch {
			case k.Code == term.Mouse && (k.Mouse.Motion || !k.Mouse.Press) && u.drag == dragNone:
				// Dropped by key(): only a hover, popup or zone change is worth a
				// repaint (?1003 sends dozens of moves per second).
				hov, who := u.hoverAt(k.Mouse.X, k.Mouse.Y), u.whoAt(k.Mouse.X, k.Mouse.Y)
				if zon := u.zoneAt(k.Mouse.X, k.Mouse.Y); !k.Mouse.Motion || (!hov && !who && !zon) {
					continue
				}
			case k.Code == term.Mouse && k.Mouse.Motion && k.Mouse.Button == 0 && u.drag == dragText:
				// Same rule for the selection drag: repaint only on a range
				// change.
				if !u.selDragTo(k.Mouse) {
					continue
				}
			default:
				u.key(k)
				u.mentionScan() // the @… box follows the input
			}
		case env := <-events:
			u.dispatch(env)
			u.drain(events)
		case ev := <-u.events:
			u.event(ev)
			u.drain(events)
		case <-t.Resized():
			t.Size()
			// First size that carries the cell: the mode had fallen back to half
			// blocks at start with nothing to place an image in. We take the
			// wanted mode back and decode again, the way F4 does by hand.
			if u.cellWait && t.CellW > 0 && t.CellH > 0 {
				u.applyImages(cfg.Images)
			}
			// Nothing is freed nor decoded again here: the images in place stay
			// shown, and the next frame puts them at their new position (end of
			// frame diff). When the cell size changed (zoom), draw() starts the
			// decoding again for the placed images only, the terminal stretching
			// the old ones meanwhile.
			if u.viewer != nil {
				u.viewLoad() // new box: full screen decoding again
			}
			// Width wanted bounded again to the screen: it comes back as it was
			// as soon as the room is there.
			u.sideW = clampSideW(cfg.SidebarWidth, t.Cols)
			u.clear()
		case <-tick.C:
			if !u.tick() {
				continue
			}
		}
		u.draw()
	}
}

// dispatch : sole way in for a backend event. The net of the envelope is put
// on everything the event carries, and stays readable in u.dispatchNet for
// the events that only name a chat by its id.
func (u *UI) dispatch(env model.Envelope) {
	u.dispatchNet = env.Net
	u.event(stamp(env.Net, env.Ev))
}

// drain groups the bursts: everything already queued is handled before the
// next repaint, backends and UI goroutines alike.
//
// No ordering is promised between the two channels: an envelope of a backend
// and an event the UI posted to itself (evSyncTick, evFrames, evClipText…)
// come in the order select picks them. They never talk about the same thing,
// so nothing depends on it — but u.dispatchNet keeps the net of the last
// envelope through an interleaved UI event, and is only to be read inside a
// dispatch.
func (u *UI) drain(events <-chan model.Envelope) {
	// Return to input and rendering even when network events keep arriving.
	for range 256 {
		select {
		case env := <-events:
			u.dispatch(env)
		case ev := <-u.events:
			u.event(ev)
		default:
			return
		}
	}
}

// net gives the backend that owns chat; nil when the chat is nil, un-stamped
// or its network is gone. Callers skip the call on nil — the old nil-tg guard
// of who.go, now at every call site.
func (u *UI) net(c *model.Chat) model.Backend {
	if c == nil {
		return nil
	}
	return u.nets[c.Net]
}

// netOf : by name, for the sites that hold a net rather than a chat (a Msg
// already stamped, u.dispatchNet).
func (u *UI) netOf(name string) model.Backend { return u.nets[name] }

// selfInfo : what EvReady told us about our account on one network.
type selfInfo struct {
	ID   int64
	Name string
	Bot  bool
}

// selfOf : our identity on net. Zero value when the network has not said who
// we are yet — id 0 owns no message and gates nothing off.
func (u *UI) selfOf(net string) selfInfo { return u.self[net] }

// selfID : our id on the network of m, for render.Opts.Self. A message of the
// aggregate or of a search result carries its network, so the mixed views read
// the right identity.
func (u *UI) selfID(m *model.Msg) int64 { return u.selfOf(m.Net).ID }

// own tells whether m is mine, on its own network.
func (u *UI) own(m *model.Msg) bool { return render.Own(m, u.selfID(m)) }

// selfName : name shown in the status bar — our name on the network of the
// chat in front. With no chat (window 0, aggregated view) the only network up
// answers for us; with several, nothing says which one, so nothing is shown.
func (u *UI) selfName(c *model.Chat) string {
	if c != nil {
		return u.selfOf(c.Net).Name
	}
	if len(u.self) == 1 {
		for _, s := range u.self {
			return s.Name
		}
	}
	return ""
}

// botOnly : every network that answered is a bot account. The account-level
// gates (/chats, new chat, global search) close only then: one human network
// is enough to keep them open, and nothing is closed before the first EvReady.
func (u *UI) botOnly() bool {
	for _, s := range u.self {
		if !s.Bot {
			return false
		}
	}
	return len(u.self) > 0
}

// evKey : key of a chat named by a bare id in a backend event. The net is the
// one of the envelope being dispatched — to be called from an event handler only.
func (u *UI) evKey(chatID int64) model.ChatKey {
	return model.ChatKey{Net: u.dispatchNet, ID: chatID}
}

// eachNet runs f on every backend. Account-level calls (dialogs, contacts,
// global search) go to all of them: each answers what it can, and the answers
// come back tagged by their envelope.
func (u *UI) eachNet(f func(model.Backend)) {
	for _, b := range u.nets {
		f(b)
	}
}

// eachNetCap runs f on the backends that carry the capability has reads. false
// when none of them does: the caller then says net_unsupported rather than
// waiting for an answer that will never come.
func (u *UI) eachNetCap(has func(model.Caps) bool, f func(model.Backend)) bool {
	any := false
	for _, b := range u.nets {
		if has(b.Caps()) {
			f(b)
			any = true
		}
	}
	return any
}

// caps : what the network of the chat can do. Nil chat or network gone: the
// zero Caps, everything gated off.
func (u *UI) caps(c *model.Chat) model.Caps { return backendCaps(u.net(c)) }

// capsOf : same, from the net stamped on a message — a message of the
// aggregate or of a search result has no chat of the sidebar to go through.
func (u *UI) capsOf(m *model.Msg) model.Caps { return backendCaps(u.netOf(m.Net)) }

func backendCaps(b model.Backend) model.Caps {
	if b == nil {
		return model.Caps{}
	}
	return b.Caps()
}

// netUnsupported says in the current window that the network cannot do what
// was asked.
func (u *UI) netUnsupported(net string) { u.sys(i18n.T("net_unsupported", net)) }

// connStatus : status segment of the networks that are not connected, "" when
// every one of them is. A network with no EvConnected yet counts as down, as
// the single flag did before. With two backends the segment names them:
// "disconnected" alone would leave the user guessing which one dropped.
func (u *UI) connStatus() string {
	var down []string
	for _, n := range u.netNames() {
		if !u.conn[n] {
			down = append(down, n)
		}
	}
	switch {
	case len(down) == 0:
		return ""
	case u.multiNet():
		return i18n.T("status_disconnected_net", strings.Join(down, ", "))
	}
	return i18n.T("status_disconnected")
}

// startNet : /<net> login, and the start of every configured network. The
// launcher runs the token command of Discord again: a token renewed in the
// password manager is taken without a restart. A failure shows like the
// start-up error did (window 0 and the log) and nothing is started.
func (u *UI) startNet(net string) {
	if u.nets[net] != nil {
		u.sys(i18n.T("net_already_up", net))
		return
	}
	ctx, cancel := context.WithCancel(u.ctx)
	b, err := u.launch(ctx, net)
	if err != nil {
		cancel()
		u.event(model.EvLog{Level: "ERROR", Msg: err.Error()})
		u.status0(i18n.T("net_status_off", net, net)) // says how to retry
		return
	}
	u.nets[net], u.netCancel[net] = b, cancel
}

// stopNet : /<net> logout. The account session ends on the server when the
// backend knows how (Telegram), then its context is cancelled; the backend
// leaves u.nets at the EvStopped its Run posts on the way out.
func (u *UI) stopNet(net string) {
	b := u.nets[net]
	if b == nil {
		u.sys(i18n.T("net_not_up", net))
		return
	}
	cancel := u.netCancel[net]
	l, ok := b.(model.Logouter)
	if !ok {
		cancel()
		return
	}
	go func() {
		if err := l.Logout(u.ctx); err != nil {
			u.events <- model.EvLog{Level: "ERROR", Msg: i18n.T("net_logout_error", net, err)}
		}
		cancel()
	}()
}

// netStatus : /<net> [status] — one line: not started, disconnected,
// connecting (no account yet: the login is in progress), or connected as X.
func (u *UI) netStatus(w *Window, net string) {
	s := u.self[net]
	switch {
	case u.nets[net] == nil:
		w.AddSys(i18n.T("net_status_off", net, net))
	case !u.conn[net]:
		w.AddSys(i18n.T("disconnected_net", net))
	case s.Name == "":
		w.AddSys(i18n.T("net_status_connecting", net))
	default:
		w.AddSys(i18n.T("net_status_up", net, s.Name))
	}
}

// netAction : /telegram and /discord — status (the default), login, logout.
func (u *UI) netAction(w *Window, net, sub string) {
	if !slices.Contains(u.netList, net) {
		w.AddSys(i18n.T("net_unconfigured", net))
		return
	}
	switch sub {
	case "", "status":
		u.netStatus(w, net)
	case "login":
		u.startNet(net)
	case "logout":
		u.stopNet(net)
	default:
		w.AddSys(i18n.T("usage_net_cmd", net))
	}
}

// multiNet : more than one backend. Sole gate of everything the networks show
// — badge, /net filter, status segment. With a single one, nothing changes.
func (u *UI) multiNet() bool { return len(u.nets) > 1 }

// netNames : network names, sorted. Order of the /net cycle and of its completion.
func (u *UI) netNames() []string { return slices.Sorted(maps.Keys(u.nets)) }

// netShown : m passes the /net filter. No filter (the single network case
// included): everything shows.
func (u *UI) netShown(m *model.Msg) bool { return u.netFilter == "" || m.Net == u.netFilter }

// setNetFilter applies /net: the sidebar and the aggregate cut to that network
// ("" = all). The aggregate carries the predicate, so its drawing, its clicks
// and its search all read the same list.
func (u *UI) setNetFilter(net string) {
	u.netFilter = net
	u.agg.Filter = nil
	if net != "" {
		u.agg.Filter = u.netShown
	}
	if !u.agg.shown(u.agg.Sel) { // the filter just hid the selected message
		u.setSel(u.agg, nil)
	}
	u.sideScroll = 0
	u.marquee = marqueeState{}
}

// nextNet gives the next value of the /net cycle: all -> names in order -> all.
func nextNet(names []string, cur string) string {
	if i := slices.Index(names, cur) + 1; i < len(names) {
		return names[i]
	}
	return ""
}

func (u *UI) resolveImages(mode string) string {
	switch mode {
	case "off", "halfblock":
		return mode
	case "kitty":
		if u.t.Kitty && u.t.CellW > 0 && u.t.CellH > 0 {
			u.cellWait = false
			return "kitty"
		}
		u.cellWait = u.t.Kitty
		u.status0(i18n.T("kitty_unavailable"))
		return "halfblock"
	}
	if u.t.Kitty && u.t.CellW > 0 && u.t.CellH > 0 {
		u.cellWait = false
		return "kitty"
	}
	u.cellWait = u.t.Kitty
	return "halfblock"
}

// nextImages gives the next mode for F4 (cycle of the resolved mode, kitty
// there or not). kitty→halfblock→off→kitty; without kitty: halfblock↔off.
func nextImages(cur string, kitty bool) string {
	if !kitty {
		if cur == "off" {
			return "halfblock"
		}
		return "off"
	}
	switch cur {
	case "kitty":
		return "halfblock"
	case "halfblock":
		return "off"
	default:
		return "kitty"
	}
}

// applyImages changes the display mode, a path shared by /set images and F4.
// v is a mode already checked (auto/kitty/halfblock/off); saving it
// (cfg.Save) is up to the caller.
func (u *UI) applyImages(v string) {
	u.cfg.Images = v
	u.images = u.resolveImages(v)
	u.reloadMedia()
	u.clear()
}

// status0 : line in window 0. AddSys goes through render.Plain, which
// neutralises the control characters: safe for remote text.
func (u *UI) status0(s string) {
	u.ws.List[0].AddSys(s)
	// The aggregated view replaces window 0: with no copy, a state message
	// (disconnection, fatal error) would not show there.
	u.agg.AddSys(s)
	if u.ws.Cur != 0 {
		u.ws.List[0].Act++
	}
}

func (u *UI) sys(s string) { u.view().AddSys(s) }

// saveCfg writes the configuration; a failure shows in the current window.
// false: nothing was written.
func (u *UI) saveCfg() bool {
	if err := u.cfg.Save(); err != nil {
		u.sys(i18n.T("config_error", err))
		return false
	}
	return true
}

// flash : status bar message erased after 2 s (tick).
func (u *UI) flash(s string) {
	u.flashMsg, u.flashUntil = render.CleanLine(s), time.Now().Add(2*time.Second)
}

// view gives the shown window. In window 0, the log (/debug) wins over the
// aggregate (Alt+A).
func (u *UI) view() *Window {
	if u.ws.Cur == 0 {
		if u.showDebug {
			return u.debug
		}
		if u.aggregate {
			return u.agg
		}
	}
	return u.ws.Current()
}

// setAggregate toggles the aggregated view (Alt+A, /set aggregate); the caller saves it.
// It also closes the debug view: the two fight over window 0.
func (u *UI) setAggregate(on bool) {
	u.aggregate, u.cfg.Aggregate, u.showDebug = on, on, false
	if u.ws.Cur != 0 {
		return // the view does not change: neither the edit mode nor the selection moves
	}
	u.cancelMode()
	u.setSel(u.view(), nil)
}

// setDebug toggles the debug view of window 0 (/debug); unlike the aggregate,
// it is never saved.
func (u *UI) setDebug(on bool) {
	u.showDebug = on
	if u.ws.Cur != 0 {
		return
	}
	u.cancelMode()
	u.setSel(u.view(), nil)
}

// aggTarget gives the chat of the last message of the aggregate, nil when there
// is none. A message hidden by the /net filter is not a target: the input of the
// aggregate must go to the chat the user sees.
func (u *UI) aggTarget() *model.Chat {
	for i := len(u.agg.Items) - 1; i >= 0; i-- {
		if m := u.agg.Items[i].Msg; m != nil && u.netShown(m) {
			return u.chats[m.Key()]
		}
	}
	return nil
}

// sendWin gives the window the input goes to. From the aggregate, the one of
// the chat of the last message; failing that the aggregate itself ("window not
// bound" error). From a search result, the one of the chat: it alone gets the
// send receipt, which ForChat cannot route to a search window.
func (u *UI) sendWin() *Window {
	w := u.view()
	if w != u.agg && w.Search == "" {
		return w
	}
	c := w.Chat
	if w == u.agg {
		c = u.aggTarget()
	}
	if c == nil {
		return w
	}
	return u.winFor(c)
}

// winFor gives the window of the chat, opening a hidden one when there is
// none: a send receipt is routed by chat, and only that window can take it.
func (u *UI) winFor(c *model.Chat) *Window {
	if i := u.ws.ForChat(c.Key()); i >= 0 {
		return u.ws.List[i]
	}
	x := u.ws.New(true) // hidden window: Loaded stays false, the history comes at the visit
	u.bindChat(x, c)    // same reason as when a message comes: do not wipe the cache
	return x
}

// chatOf gives the chat of a message; an action (edit, delete, reaction) aims
// at the chat of the message, not at the one of the window — the aggregate has none.
func (u *UI) chatOf(m *model.Msg) *model.Chat {
	if m == nil {
		return nil
	}
	return u.chats[m.Key()]
}

// views gives every view where one message can show — the windows and the
// aggregate, which is not in ws.List. A message shows in the window of its
// chat, in the aggregate and in each /search result, with the same pointer or,
// when the views loaded it apart, one pointer per view.
//
// ponytail: full sweep of the views at each edit or reaction; these events are
// rare beside the incoming messages. Index by id if a burst of reactions ever
// shows on the screen.
func (u *UI) views() []*Window { return append([]*Window{u.agg}, u.ws.List...) }

// updateShared replaces m everywhere it shows: a remote edit comes with a new
// pointer, and each view must take it.
func (u *UI) updateShared(m *model.Msg) {
	for _, w := range u.views() {
		w.Update(m)
	}
}

// touchShared does the same for a change in place (reaction, edit receipt) —
// only the cached drawing is to be made again.
func (u *UI) touchShared(m *model.Msg) {
	for _, w := range u.views() {
		w.Touch(m)
	}
}

// clear invalidates every drawing, and the next draw() repaints everything. No
// screen clear: ED (CSI 2 J) destroys the DATA of the kitty images (Ghostty:
// eraseDisplay complete → kitty_images.clearScreen → deleteVisiblePlacements +
// deleteIfUnused) while the ids stay on the media, and the a=p that follows
// would fail in silence (q=2). draw() covers each cell without it: message
// area, status bar and input erased line by line (\x1b[K), sidebar filled up to
// the separator, alternate screen (CSI ?1049h) already blank at opening time.
func (u *UI) clear() {
	u.zone = zoneNone // the zones moved: nothing under the pointer until it moves again
	for _, w := range u.ws.List {
		w.Invalidate()
	}
	u.agg.Invalidate()
	u.debug.Invalidate()
}

func (u *UI) opts() render.Opts {
	cellW, cellH, maxCols, maxRows := u.cells()
	v := u.view()
	o := render.Opts{Width: u.width(), Theme: u.th, Timestamps: u.cfg.Timestamps, Seconds: u.cfg.TimestampsSeconds, Images: u.images, LinkPreviews: u.cfg.LinkPreviews,
		CellW: cellW, CellH: cellH, MaxImgCols: maxCols, MaxImgRows: maxRows, Self: u.selfID,
		Avatars: u.avatarsOn(), ShowChat: v == u.agg, ReadOutbox: u.readOutboxOf, ReadInbox: u.readInboxOf,
		ImagesHover: u.cfg.ImagesHover, Video: u.cfg.Video, ChatKind: u.chatKindOf, HoverHelp: u.cfg.Hover == config.HoverMenu,
		Alias: u.aliasOf, Redline: u.cfg.Redline, Caps: u.capsOf,
		// Search window: the palette offers "g go" (join the message in its chat)
		// rather than "g quoted".
		Jump: v.Search != "",
		// v.Chat nil (aggregate, window 0): global list, the restriction per
		// chat is caught at click time by react().
		Reactions: u.allowed(v.Chat)}
	if it := v.Sel; it != nil { // every view draws the shown window
		o.Selected = it.Msg
	}
	// The hover counts only for the shown window: after a window change at the
	// keyboard, the item pointed at is no longer its own (the aggregate shares
	// the *model.Msg, it would highlight the wrong message).
	if u.hover != nil && slices.Contains(v.Items, u.hover) {
		o.Hover = u.hover.Msg
	}
	if u.selEnd != nil { // selection drag: background only on the range
		o.TextSel = map[*model.Msg]bool{}
		for _, it := range rangeItems(v.Items, u.selAnchor, u.selEnd) {
			if it.Msg != nil {
				o.TextSel[it.Msg] = true
			}
		}
	}
	return o
}

// optsFor gives the same options as opts(), but ShowChat/Selected are about w
// and not about the shown window — to draw a window that is not the current
// view (the read position when a background window loads, say).
func (u *UI) optsFor(w *Window) render.Opts {
	o := u.opts()
	o.ShowChat, o.Selected, o.Hover, o.TextSel = w == u.agg, nil, nil, nil
	o.Jump = w.Search != "" // result: "g go" rather than "g quoted"
	if w.Sel != nil {
		o.Selected = w.Sel.Msg
	}
	return o
}

// chatKindOf gives the kind of the chat of a message, for render.Opts.ChatKind
// (avatar fallback in a private chat). Unknown chat: ChatGroup, with no bad
// effect (no fallback).
func (u *UI) chatKindOf(m *model.Msg) model.ChatKind {
	if c := u.chatOf(m); c != nil {
		return c.Kind
	}
	return model.ChatGroup
}

// readOutboxOf gives the last of my messages read in the chat of the message
// (✓✓ tick). Per chat and not per window: the aggregated view mixes several.
func (u *UI) readOutboxOf(m *model.Msg) int {
	if c := u.chatOf(m); c != nil {
		return c.ReadOutboxMaxID
	}
	return 0
}

// readInboxOf gives the last received message I read in the chat of the
// message (tick on the messages of other people).
func (u *UI) readInboxOf(m *model.Msg) int {
	if c := u.chatOf(m); c != nil {
		return c.ReadInboxMaxID
	}
	return 0
}

func (u *UI) cells() (cellW, cellH, maxCols, maxRows int) {
	cellW, cellH = 1, 2
	if u.images == "kitty" {
		cellW, cellH = u.t.CellW, u.t.CellH
	}
	return cellW, cellH, min(60, u.width()-8), max(u.viewRows()*40/100, 3)
}

// --- events ---

// isDebugLog tells whether an EvLog is to stay debug-only (never in window 0).
// The rule kept: the level — WARN/DEBUG/INFO (with the backend "unknown peer"
// flood) matter only for the full log; ERROR stays shown in window 0 (direct
// result of a user action: delete, reaction…), besides the log. Every EvLog
// lands in u.debug whatever the level.
func isDebugLog(ev model.Event) bool {
	e, ok := ev.(model.EvLog)
	return ok && e.Level != "ERROR"
}

func (u *UI) event(ev model.Event) {
	if e, ok := ev.(model.EvLog); ok {
		u.debug.AddSys(e.Level + " " + e.Msg) // full log, never Act
		if !isDebugLog(ev) {                  // ERROR: also shown in window 0, as before
			u.status0(e.Level + " " + e.Msg)
		}
		return
	}
	switch e := ev.(type) {
	case model.EvAuthPrompt:
		u.prompt, u.promptNet = &e, u.dispatchNet
		u.status0(e.Question)
		u.goTo(0)
	case model.EvQR:
		u.setQR(e)
	case model.EvQRDone:
		u.closeQR()
		if u.prompt != nil { // prompt of the QR: nobody waits for the answer any more
			u.prompt = nil
			u.ed.Set("")
		}
	case model.EvReady:
		net := u.dispatchNet
		u.self[net] = selfInfo{ID: e.SelfID, Name: render.CleanLine(e.SelfName), Bot: e.Bot}
		u.status0(i18n.T("connected_as", e.SelfName))
		// Only the cache of THAT network: another one keeps the account that
		// wrote it, and its history with it.
		if old := u.cacheSelf[net]; old != 0 && old != e.SelfID {
			u.dropCache(net)
		}
		if e.Bot {
			u.status0(i18n.T("bot_mode_notice"))
		} else if b := u.netOf(net); b != nil {
			// Not a broadcast although the call is account-level: EvReady is
			// per network, and each one asks for its own dialogs when it comes
			// up. eachNet here would ask a backend that is not connected yet.
			b.LoadDialogs(u.ctx)
		}
	case model.EvConnected:
		u.conn[u.dispatchNet] = true
	case model.EvDisconnected:
		u.conn[u.dispatchNet] = false
		if u.multiNet() { // with two networks, "disconnected" alone says nothing
			u.status0(i18n.T("disconnected_net", u.dispatchNet))
			return
		}
		u.status0(i18n.T("disconnected"))
	case model.EvStopped:
		// Run is over, on its own or after /<net> logout: the network leaves
		// the live map (every call site treats a missing one as gone) and
		// /<net> login may start it again.
		net := u.dispatchNet
		if c := u.netCancel[net]; c != nil {
			c() // Run ended on its own: frees the forwarder of its events
		}
		delete(u.nets, net)
		delete(u.netCancel, net)
		delete(u.self, net)
		delete(u.dialogsSeen, net) // the next login auto-opens and syncs again
		u.conn[net] = false
		if u.prompt != nil && u.promptNet == net { // a login question nobody waits for any more
			u.closeQR()
			u.prompt = nil
			u.ed.Set("")
		}
		if e.Err != "" {
			u.status0(i18n.T("net_stopped_error", net, e.Err, net))
			u.goTo(0)
			return
		}
		u.status0(i18n.T("net_stopped", net))
	case model.EvDialogs:
		if e.Err != "" {
			u.status0(i18n.T("dialogs_error", e.Err))
			return
		}
		fresh := make([]*model.Chat, 0, len(e.Chats))
		for _, c := range e.Chats {
			fresh = append(fresh, u.remember(c))
		}
		u.chatList = mergeDialogs(u.chatList, fresh)
		net := u.dispatchNet
		if e.Complete {
			u.pruneChats(net, fresh)
		}
		u.status0(i18n.T("chats_count", len(u.chatList)))
		// Automatic opening and sync fire once per network, at ITS first chat
		// list: a network that comes up late must still get them.
		first := !u.dialogsSeen[net]
		u.dialogsSeen[net] = true
		if first {
			u.autoOpen(net)
		}
		u.saveDialogs()
		if first {
			u.syncStart(net)
		}
	case model.EvNewMessage:
		u.newMessage(e)
	case model.EvSearch:
		u.searchResult(e)
	case model.EvSearchGlobal:
		u.searchGlobalResult(e)
	case model.EvContacts:
		u.contactsList(e)
	case model.EvContactsFound:
		u.contactsFound(e)
	case model.EvGifs:
		u.gifsResult(e)
	case model.EvEditMessage:
		if i := u.ws.ForChat(e.Msg.Key()); i >= 0 {
			m := e.Msg
			if c := u.chats[m.Key()]; c != nil {
				m.ChatLabel = c.Title
			}
			u.ws.List[i].Upsert(&m)
			u.updateShared(&m) // never Upsert: an edited history message does not enter the aggregate
			u.autoMedia(&m, false)
			u.markDirty(m.Key())
		}
	case model.EvDeleted:
		for _, w := range u.ws.List {
			// Net first: a global delete (ChatID 0) sweeps every window, and the
			// ids of one network mean nothing in another.
			if w.Chat == nil || w.Chat.Net != u.dispatchNet {
				continue
			}
			if e.ChatID != 0 && w.Chat.ID != e.ChatID {
				continue
			}
			if w.Chat.Channel && e.ChatID == 0 {
				continue // ids of a channel: never in a global UpdateDeleteMessages
			}
			for _, it := range w.Items {
				if it.Msg != nil && slices.Contains(e.IDs, it.Msg.ID) {
					it.Msg.Deleted, it.lines = true, nil
					u.agg.Touch(it.Msg)
					u.markDirty(w.Chat.Key())
				}
			}
		}
	case model.EvTyping:
		u.typing[u.evKey(e.ChatID)] = typing{render.CleanLine(e.Who), time.Now().Add(6 * time.Second)}
	case model.EvPresence:
		u.presence[u.evKey(e.UserID)] = e.Status
	case model.EvParticipants:
		u.participants(e)
	case model.EvChatGone:
		u.chatGone(u.evKey(e.ChatID))
	case model.EvWhois:
		u.whois(e)
	case model.EvHistory:
		if e.Since {
			u.syncHistory(e)
			return
		}
		u.history(e)
	case evSyncTick:
		u.syncNext()
	case model.EvEdited:
		u.edited(e)
	case model.EvReactions:
		u.reactions(e)
	case model.EvReactionsList:
		u.reactList[u.dispatchNet] = baseAll(e.Emojis)
		u.clear() // the hover emoji depends on the list
	case model.EvChatReactions:
		if c := u.chats[u.evKey(e.ChatID)]; c != nil {
			c.Reactions = baseAll(e.Emojis) // nil = no restriction left
			if e.None {
				c.Reactions = []string{}
			}
		}
	case model.EvReactionFailed:
		u.reactFailed(u.evKey(e.ChatID), e)
	case model.EvInfo:
		u.info(e)
	case model.EvWho:
		u.whoEv(e)
	case model.EvReadOutbox:
		u.readOutbox(u.chats[u.evKey(e.ChatID)], e.MaxID)
	case model.EvReadInbox:
		u.readInbox(u.chats[u.evKey(e.ChatID)], e.MaxID, e.Unread, e.HasUnread)
	case model.EvSent:
		u.sent(e)
	case model.EvChat:
		u.chatResolved(e)
	case model.EvDownloaded:
		u.downloaded(e)
	case evFrames:
		u.framesLoaded(e)
	case model.EvUpload:
		u.flash(e.Text)
	case evClipImage:
		u.clipImage(e)
	case evClipText:
		u.pasteText(u.view(), normalizePaste(e.Text))
	}
}

// remember keeps only one *Chat per id (the windows point at it).
func (u *UI) remember(c *model.Chat) *model.Chat {
	if old := u.chats[c.Key()]; old != nil {
		// Never ID nor Peer: the windows and the running RPCs point at them.
		old.Title, old.Username, old.Kind = c.Title, c.Username, c.Kind
		old.Pinned, old.Channel = c.Pinned, c.Channel
		// max: a read update that came before the dialog list does not get
		// wiped by an older value (Unread included).
		u.readOutbox(old, c.ReadOutboxMaxID)
		u.readInbox(old, c.ReadInboxMaxID, c.Unread, true)
		old.TopMessage, old.LastDate = c.TopMessage, c.LastDate
		old.PhotoLoc = c.PhotoLoc // photo refreshed; the avatar already loaded does not move
		return old
	}
	u.chats[c.Key()] = c
	return c
}

// listChat brings the chat into the sidebar — and so into /chats, the cache,
// the sync and the completion. Single point of passage: dialogs of the cache,
// /query resolved, window opened (attach), incoming message. A chat only
// listed (search result, contact) stays out of the sidebar as long as it has
// neither a window nor a message.
func (u *UI) listChat(c *model.Chat) {
	if c != nil && !slices.Contains(u.chatList, c) {
		u.chatList = append(u.chatList, c)
	}
}

func (u *UI) newMessage(e model.EvNewMessage) {
	m := e.Msg
	chat := u.chats[m.Key()]
	if chat == nil {
		chat = u.remember(e.Chat)
	}
	// Always: the chat can be known to u.chats without being in the sidebar
	// (seen in a global search result, or in the contacts).
	u.listChat(chat)
	i := u.ws.ForChat(chat.Key())
	if i < 0 {
		u.bindChat(u.ws.New(true), chat)
		i = len(u.ws.List) - 1
	}
	m.ChatLabel = chat.Title // the aggregate labels each message with its chat
	w := u.ws.List[i]
	added := w.Upsert(&m)
	if added {
		u.logMsg(w, &m)
	}
	u.agg.Upsert(&m) // same pointer: edits, deletes and reactions follow
	// Read on the screen: current window or aggregated view, and only when
	// the terminal has the focus — away, nobody reads.
	seen := u.focused && (i == u.ws.Cur || u.view() == u.agg)
	if added && !m.Out {
		if !seen {
			w.Act++
			chat.Unread++
		}
		me := u.selfOf(m.Net)
		if (!u.focused || i != u.ws.Cur) && (chat.Kind == model.ChatUser || mentionsMe(&m, me.ID, me.Name)) {
			if u.cfg.Bell {
				u.t.WriteString("\a") // flushed at the next draw()
			}
			u.notify(chat, &m)
		}
	}
	if seen {
		u.markRead(w)
	}
	u.autoMedia(&m, false)
	u.markDirty(m.Key())
}

// logLine formats m for the window log: text (or media label with no text),
// cleaned and on a single line; a service message as "*** text", with no
// timestamp and no name, as on the screen (render.Message).
func logLine(m *model.Msg, now time.Time) string {
	if m.Service != "" {
		return "*** " + render.CleanLine(m.Service)
	}
	body := m.Text
	if body == "" && m.Media != nil {
		body = m.Media.Label
	}
	body = strings.ReplaceAll(render.Clean(body), "\n", " ")
	return fmt.Sprintf("%s <%s> %s", now.Format("2006-01-02 15:04"), render.CleanLine(m.From), body)
}

// logTitleRunes : readable part of a journal file name. Cut on runes, not on
// bytes: an accented title never comes out half a character.
const logTitleRunes = 40

// logName gives the journal file name of w — the head of the title, then the
// stable key of the chat (network and id). The title alone would not do: two
// long titles sharing their first characters used to write into the same file,
// silently mixing two chats in one journal. Renaming a chat still starts a new
// file, the price of a name one can read in an ls; the journals of two chats
// can no longer merge. A window bound to no chat (never logged by /log, only
// by log = true) keeps its bare name.
func logName(w *Window) string {
	title := []rune(w.Name())
	if len(title) > logTitleRunes {
		title = title[:logTitleRunes]
	}
	name := media.SafeName(string(title))
	if w.Chat == nil {
		return name + ".log"
	}
	return fmt.Sprintf("%s-%s-%d.log", name, w.Chat.Net, w.Chat.ID)
}

// logMsg adds m to the log of w when w.Log is on.
// ponytail: no cache of the file descriptor, open/close at each write — a
// logged chat stays far from the rate that would make that costly; to be
// looked at again only if the log becomes a measured bottleneck.
func (u *UI) logMsg(w *Window, m *model.Msg) {
	if !w.Log {
		return
	}
	dir := config.Expand(u.cfg.LogDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		u.status0(i18n.T("log_error", err))
		return
	}
	path := filepath.Join(dir, logName(w))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		u.status0(i18n.T("log_error", err))
		return
	}
	defer f.Close()
	if _, err := f.WriteString(logLine(m, time.Now()) + "\n"); err != nil {
		u.status0(i18n.T("log_error", err))
	}
}

// mentionsMe : @username as a whole word (case insensitive), or a mention by
// id (a SpanMention that carries a UserID, the form the Telegram clients send
// when the sender types a contact without going through their @username).
func mentionsMe(m *model.Msg, me int64, username string) bool {
	for _, e := range m.Entities {
		// UserID 0 = mention with no id (@username, hashtag): never a match,
		// not even before the self id is known.
		if e.Kind == model.SpanMention && e.UserID != 0 && e.UserID == me {
			return true
		}
	}
	if username == "" {
		return false
	}
	// ponytail: regexp compiled again at each message; the volume (unread incoming
	// messages) does not justify caching it on the current username.
	re := regexp.MustCompile(`(?i)(?:^|\W)@` + regexp.QuoteMeta(username) + `(?:$|\W)`)
	return re.MatchString(m.Text)
}

// netChats : the chats of one network, in the order of the sidebar. The
// per-network triggers (automatic opening, sync) work on that alone: a network
// that comes up late must not move the windows of the others.
func (u *UI) netChats(net string) []*model.Chat {
	out := make([]*model.Chat, 0, len(u.chatList))
	for _, c := range u.chatList {
		if c.Net == net {
			out = append(out, c)
		}
	}
	return out
}

// autoOpenChats gives the chats active for less than days days, sorted from
// the newest to the oldest. days <= 0: automatic opening off.
func autoOpenChats(chats []*model.Chat, now time.Time, days int) []*model.Chat {
	if days <= 0 {
		return nil
	}
	cutoff := now.AddDate(0, 0, -days)
	var out []*model.Chat
	for _, c := range chats {
		if !c.LastDate.Before(cutoff) {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b *model.Chat) int { return b.LastDate.Compare(a.LastDate) })
	return out
}

// autoOpen pre-opens (hidden windows, with no history) the recently active
// chats, at the first chat list of the session.
func (u *UI) autoOpen(net string) {
	for _, c := range autoOpenChats(u.netChats(net), time.Now(), u.cfg.AutoOpenDays) {
		i := u.ws.ForChat(c.Key())
		if i < 0 {
			u.bindChat(u.ws.New(true), c)
			i = len(u.ws.List) - 1
		}
		if i == u.ws.Cur {
			continue // window already visited: its counters are up to date
		}
		// The window may already exist (disk cache): its counters still come
		// from the network list, the only one up to date.
		w := u.ws.List[i]
		w.Act, w.scrolledToMark = c.Unread, false
		u.markRefresh(w)
	}
}

// jumpDone forgets the appointment set by gsOpen when it aimed at this chat:
// with no page to come, nobody would keep it any more.
func (u *UI) jumpDone(k model.ChatKey) {
	if u.jump.chat == k {
		u.jump.chat, u.jump.msgID = model.ChatKey{}, 0
	}
}

func (u *UI) history(e model.EvHistory) {
	key := u.evKey(e.ChatID)
	i := u.ws.ForChat(key)
	if i < 0 {
		u.jumpDone(key) // window closed meanwhile
		return
	}
	w := u.ws.List[i]
	w.Loading = false
	if e.Err != "" {
		w.AddSys(i18n.T("history_error", e.Err))
		u.jumpDone(key)
		return
	}
	w.dropCacheMark() // the network answered: the history is no longer the one of the disk
	ptrs := make([]*model.Msg, len(e.Msgs))
	for k := range e.Msgs {
		ptrs[k] = &e.Msgs[k]
	}
	if e.Around {
		w.MergeAround(ptrs)
	} else {
		w.Merge(ptrs)
	}
	u.markDirty(key)
	if e.Done {
		w.Full = true
	}
	// recent : page of the last messages. A page centred on a message (jump)
	// is not one: neither "loaded", nor read, nor a move to the mark —
	// which would fight the jump.
	recent := !e.Older && !e.Around
	if recent {
		w.Loaded = true
	}
	// On the items of the window and not on ptrs: Merge drops the messages
	// already there (cached history), and it is their own media that shows.
	// Starting the download on the dropped message never showed anything —
	// the image showed only at the first zoom, which loads everything again.
	ids := make(map[int]bool, len(ptrs))
	for _, m := range ptrs {
		ids[m.ID] = true
	}
	for _, it := range w.Items {
		if it.Msg != nil && ids[it.Msg.ID] {
			u.autoMedia(it.Msg, false)
		}
	}
	if recent && i == u.ws.Cur {
		u.markRead(w)
	}
	// Read position: at the first EvHistory (not a load upwards) of a window
	// with unread messages, it settles the view on the "new messages"
	// separator rather than on the very last message. Once per opening:
	// MarkID stays for the separator itself.
	if recent && w.MarkID > 0 && !w.scrolledToMark {
		w.scrolledToMark = true
		lines, _, idx := w.LineItems(u.optsFor(w))
		if idx >= 0 {
			w.Scroll = scrollTo(len(lines), idx, u.viewRows())
		}
	}
	// Appointment of a jump (search, quote): the selection is set as soon as
	// the message is there (selShow moves the view at the next draw). Any
	// other page that ends starts the centred page again — including a scroll
	// up in flight at the time of the jump, which had held it. Only the page
	// centred on the target, back without it, closes the appointment.
	if u.jump.msgID != 0 && u.jump.chat == key {
		switch it := itemByID(w, u.jump.msgID); {
		case it != nil:
			u.jumpDone(key)
			u.setSel(w, it)
		case e.Around && e.AroundID == u.jump.msgID:
			u.jumpDone(key) // page centred on it, without it: message gone
			w.AddSys(i18n.T("message_gone"))
		default:
			u.loadAround(w, u.jump.msgID)
		}
	}
}

// scrollTo gives the Scroll (physical lines from the bottom, see
// Window.Scroll) that brings the line idx (of total) to the 3rd line of the
// view (rank 2, 0-based) — see draw(): start = total-Scroll-view, line shown
// at rank idx-start. idx already visible in the last view lines: 0, nothing to
// do. idx too close to the start to leave 2 lines above: clamped to the top of
// the content.
func scrollTo(total, idx, view int) int {
	if idx >= total-view {
		return 0
	}
	return min(total-(idx-2)-view, max(0, total-view))
}

func (u *UI) sent(e model.EvSent) {
	if e.TmpID == 0 {
		return
	}
	u.agg.Sent(e.TmpID, e.ID, e.Err)
	if i := u.ws.ForChat(u.evKey(e.ChatID)); i >= 0 {
		w := u.ws.List[i]
		// Captured before Sent(): on a server duplicate (channel/supergroup),
		// Sent() drops the pending item instead of stamping it — pend.ID then
		// stays at 0, the message being already logged through newMessage.
		var pend *model.Msg
		for _, it := range w.Items {
			if it.Msg != nil && it.Msg.TmpID == e.TmpID {
				pend = it.Msg
				break
			}
		}
		if w.Sent(e.TmpID, e.ID, e.Err) {
			if e.Err == "" && pend != nil && pend.ID != 0 {
				u.logMsg(w, pend)
			}
			u.markDirty(u.evKey(e.ChatID))
			return
		}
	}
	// Send through /msg, with no window to stamp the result: it goes to window 0.
	title, ok := u.pendingMsg[e.TmpID]
	if !ok {
		return
	}
	delete(u.pendingMsg, e.TmpID)
	if e.Err != "" {
		u.status0(i18n.T("msg_send_failed", title, e.Err))
		return
	}
	u.status0(i18n.T("msg_sent", title))
}

func (u *UI) chatResolved(e model.EvChat) {
	w := u.pending[e.Query]
	delete(u.pending, e.Query)
	if e.Err != "" {
		msg := i18n.T("resolve_failed", e.Query, e.Err)
		if w == nil {
			u.status0(msg)
		} else {
			w.AddSys(msg)
		}
		return
	}
	c := u.remember(e.Chat)
	u.listChat(c)
	if w == nil { // orphan request: never bind the current window (that would be window 0)
		u.status0(i18n.T("resolved", u.title(c)))
		return
	}
	u.attach(w, c)
}

// markRefresh freezes w.MarkID (position of the redline) on the current
// ReadInboxMaxID of the chat. To be called before markRead(w), which moves it
// on: otherwise the redline would freeze after the last message already. One
// single point for the three ways into a window: opening (attach, autoOpen),
// coming back (goTo) and end of being away (FocusIn).
func (u *UI) markRefresh(w *Window) {
	if w.Chat != nil {
		w.MarkID = w.Chat.ReadInboxMaxID
	}
}

func (u *UI) markRead(w *Window) {
	if w.Search != "" {
		return // search result: nothing to mark read, the ids are not the last of the chat
	}
	if !u.focused {
		return // away: the messages stay unread until we come back
	}
	w.Act = 0
	if w.Chat != nil {
		w.Chat.Unread = 0
	}
	if w.Chat == nil || u.selfOf(w.Chat.Net).Bot {
		return
	}
	b := u.net(w.Chat)
	if b == nil {
		return
	}
	if last := w.LastID(); last > w.ReadSent {
		w.ReadSent = last
		b.MarkRead(u.ctx, w.Chat, last)
		// ticks to ✓✓ and badge to 0 at once, without waiting for the server echo.
		u.readInbox(w.Chat, last, 0, true)
	}
}

// --- windows ---

// cycleTarget : the window Ctrl+X goes to. Mode next: the one after the
// current, wrapping. Mode last_unread: the next window with activity in number
// order after the current one; the window left at the first jump is kept as
// home and taken back once nothing is unread any more — with no home left (or
// home closed, or already shown), the classic cycle.
func (u *UI) cycleTarget() int {
	n := len(u.ws.List)
	next := (u.ws.Cur + 1) % n
	if u.cfg.CycleMode != "last_unread" {
		return next
	}
	for i := 1; i < n; i++ {
		if j := (u.ws.Cur + i) % n; u.ws.List[j].Act > 0 {
			if u.cycleHome == nil {
				u.cycleHome = u.ws.Current()
			}
			return j
		}
	}
	home := u.cycleHome
	u.cycleHome = nil
	if j := slices.Index(u.ws.List, home); j >= 0 && j != u.ws.Cur {
		return j
	}
	return next
}

func (u *UI) goTo(n int) {
	u.selCancel()   // before the change: the range is about the window we leave
	u.closeViewer() // every window change frees the preview
	old := u.ws.Current()
	if !u.ws.Switch(n) {
		u.sys(i18n.T("no_window_n", n))
		return
	}
	u.showDebug = false // every window change closes the log again (/debug)
	w := u.ws.Current()
	// A mode running (edit, reply, login prompt, paste decision) drops the
	// draft rather than saving it: cancelMode() stays unconditional (Esc,
	// search, delete confirmation); only the draft swap is gated, and it comes
	// before cancelMode() so as not to be wiped (cancelMode() empties u.ed
	// only when edit/reply was on, never in this branch).
	if u.edit == nil && u.reply == nil && u.prompt == nil && u.pasteAsk == "" && u.sendAsk == nil {
		u.ed.Set(swapDraft(old, w, u.ed.String()))
	}
	u.cancelMode()
	u.setSel(u.view(), nil)
	u.sideReveal()
	u.loadParts() // box asked for: it follows the new window (cache per chat)
	if w.Chat != nil && !w.Loaded && !w.Loading {
		if u.selfOf(w.Chat.Net).Bot {
			w.Loaded, w.Full = true, true // a bot has no access to the history
		} else {
			u.loadHistory(w, 0)
		}
	}
	// Media of the history replayed from the disk cache: no EvHistory covers
	// them when the window is already loaded (sync with nothing new), and
	// freeImages left them at MediaNone.
	u.autoMediaWin(w)
	// Back in the window: redline set again on its ReadInboxMaxID before
	// markRead() moves it on (it may have stayed out of the current window
	// while messages came).
	u.markRefresh(w)
	u.markRead(w)
}

// loadHistory : network page, when a window opens (beforeID = 0) as well as
// when scrolling up in it (beforeID = OldestID, see scroll). w.Loading stops
// any overlap: only one load in flight per window.
func (u *UI) loadHistory(w *Window, beforeID int) {
	b := u.net(w.Chat)
	if b == nil {
		return // no network: w.Loading stays false, the window is not stuck
	}
	w.Loading = true
	b.LoadHistory(u.ctx, w.Chat, beforeID, 100)
}

// loadAround : network page centred on a message (precise jump). Same lock as
// loadHistory: only one load in flight per window.
func (u *UI) loadAround(w *Window, id int) {
	b := u.net(w.Chat)
	if b == nil {
		return
	}
	w.Loading = true
	b.LoadHistoryAround(u.ctx, w.Chat, id, 100)
}

// jumpTo brings the window of the chat to the message id. Already loaded:
// selection (selShow moves the view at the next draw). Missing: appointment
// u.jump and a page centred on it, kept by history(). A window that has just
// opened is already loading its recent page: we wait for it rather than call
// in parallel.
// ponytail: the centred page and the recent messages stay apart with a gap
// that NOTHING fills — scrolling up loads before the oldest one shown, so
// above the gap and never inside it. The seam is marked (Window.markGap), the
// hole itself is still nobody's job.
func (u *UI) jumpTo(c *model.Chat, id int) {
	if id == 0 {
		return // send not confirmed yet: no id to aim at
	}
	if c == nil {
		u.sys(i18n.T("jump_unknown_chat"))
		return
	}
	u.jump.chat, u.jump.msgID = model.ChatKey{}, 0
	if i := u.ws.ForChat(c.Key()); i != u.ws.Cur {
		// goTo → cancelMode() gives the input back and closes the local search;
		// useless (and rough) when the window aimed at is already the current one.
		u.openChat(c)
	}
	i := u.ws.ForChat(c.Key())
	if i < 0 {
		return // opening refused: nothing to settle
	}
	w := u.ws.List[i]
	if it := itemByID(w, id); it != nil {
		u.setSel(w, it)
		return
	}
	if u.selfOf(c.Net).Bot {
		w.AddSys(i18n.T("jump_bot_no_history"))
		return
	}
	u.jump.chat, u.jump.msgID = c.Key(), id
	if !w.Loading {
		u.loadAround(w, id)
	}
}

// attach binds a window to a chat (or switches when the chat already has its window).
func (u *UI) attach(w *Window, c *model.Chat) {
	if i := u.ws.ForChat(c.Key()); i >= 0 && u.ws.List[i] != w {
		u.goTo(i)
		return
	}
	u.freeImages(w)
	u.cancelMode()
	w.Items, w.Sel, w.Scroll, w.Full, w.ReadSent = nil, nil, 0, false, 0
	// Search set back to empty: /query on a search result makes it a plain chat
	// window again.
	w.MarkID, w.Loaded, w.Search, w.scrolledToMark = 0, false, "", false
	u.bindChat(w, c)  // cached history while waiting for the network answer
	u.autoMediaWin(w) // its media: the network page may cover none of them (bot, error)
	u.listChat(c)     // opened from a search or from the contacts: it enters the sidebar
	u.markRefresh(w)
	u.sideReveal()
	if w == u.view() { // hidden window: the scroll of the shown box stays
		u.loadParts()
	}
	if u.selfOf(c.Net).Bot {
		w.Loaded, w.Full = true, true // a bot has no access to the history
		return
	}
	u.loadHistory(w, 0)
}

// whois : result of /whois, in the shown window.
// ponytail: the answer goes where the user looks, not into the window that
// ran the command (one server round trip, they have not changed window). If
// that ever gets in the way, keep the window as u.pending does for /query.
func (u *UI) whois(e model.EvWhois) {
	if e.Err != "" {
		title := "?"
		if c := u.chats[u.evKey(e.ChatID)]; c != nil {
			title = u.title(c)
		}
		u.sys(i18n.T("whois_error", title, e.Err))
		return
	}
	// Renamed locally: the real title stays visible here.
	if c := u.chats[u.evKey(e.ChatID)]; c != nil && u.aliases[u.evKey(e.ChatID)] != "" {
		u.sys(i18n.T("telegram_title", render.CleanLine(c.Title)))
	}
	for _, l := range e.Lines {
		u.sys(l)
	}
}

// findChat : exact name (username, title or local name) then unique prefix,
// case insensitive.
func (u *UI) findChat(q string) (chat *model.Chat, ambiguous bool) {
	q = strings.ToLower(strings.TrimPrefix(q, "@"))
	var exact, pref []*model.Chat
	for _, c := range u.chatList {
		un, ti := strings.ToLower(c.Username), strings.ToLower(c.Title)
		al := strings.ToLower(u.aliases[c.Key()])
		switch {
		case un == q || ti == q || al == q:
			exact = append(exact, c)
		case (un != "" && strings.HasPrefix(un, q)) || strings.HasPrefix(ti, q) || (al != "" && strings.HasPrefix(al, q)):
			pref = append(pref, c)
		}
	}
	// An exact name wins over the prefixes; two chats with the same exact name
	// (a local name that copies the title of another) stay ambiguous.
	if len(exact) > 0 {
		pref = exact
	}
	if len(pref) == 1 {
		return pref[0], false
	}
	if len(pref) > 1 {
		var names []string
		for _, c := range pref {
			names = append(names, u.title(c))
		}
		u.sys(i18n.T("ambiguous", strings.Join(names, ", ")))
		return nil, true
	}
	return nil, false
}

func (u *UI) scroll(w *Window, delta int) {
	lines := w.Lines(u.opts())
	maxScroll := max(0, len(lines)-u.viewRows())
	w.Scroll = max(0, min(w.Scroll+delta, maxScroll))
	if delta > 0 && w.Scroll >= maxScroll && w.Chat != nil && !w.Full && !w.Loading {
		u.loadHistory(w, w.OldestID())
	}
}

// --- keyboard ---

func (u *UI) key(k term.Key) {
	// Neither the release of a click nor a move is an action: otherwise they
	// would close the overlay (emoji picker, pager, preview) that the press
	// has just opened, which treats every mouse event as a click. They go
	// through during a drag of the bar, which they alone keep alive.
	if k.Code == term.Mouse && (k.Mouse.Motion || !k.Mouse.Press) && u.drag == dragNone {
		return
	}
	// The focus is not a key: no mode (picker, pager, search, confirmation)
	// must swallow it.
	switch k.Code {
	case term.FocusOut:
		u.focused = false
		if u.hover != nil { // the pointer left with no motion
			u.hover.lines = nil
			u.hover = nil
		}
		u.who, u.zone = nil, zoneNone
		return
	case term.FocusIn:
		u.focused = true
		// Catches up with what came while we were away. markRefresh before
		// markRead: when messages came while we were away, the redline settles
		// on them instead of going away at once.
		// ponytail: in the aggregated view, markRead touches only the aggregate
		// (Chat nil) and the counters of the shown windows stay; emptying them would
		// mean walking the messages of the aggregate.
		u.markRefresh(u.view())
		u.markRead(u.view())
		return
	}
	if u.viewer != nil { // the full screen preview comes before everything else
		u.viewerKey(k)
		return
	}
	if u.picker != nil {
		// Ctrl+C closes the overlay with no choice (Ctrl+C does not quit while
		// it is open); otherwise the picker keeps the lead until Enter, Esc or a
		// click.
		if k.Code == term.Mouse {
			u.pickerMouse(k.Mouse)
			return
		}
		if (k.Code == term.Ctrl && k.Rune == 'c') || u.picker.Key(k) {
			u.picker = nil
		}
		return
	}
	// Overlays that take everything until they close, in this order — it is the
	// order that decides which one wins when two are open. The picker (above)
	// and the viewer keep their own block: their mouse path is not the same
	// shape.
	for _, o := range []leadOverlay{
		{u.themePick != nil, u.themeKey, u.themeMouse}, // like the emoji picker: lead until Enter or Esc
		{u.newChat != nil, u.ncKey, u.ncMouse},         // new chat
		{u.gifs != nil, u.gifKey, u.gifMouse},          // GIF box
		{u.gsearch != nil, u.gsKey, u.gsMouse},         // global search
		{u.menu != nil, u.menuKey, u.menuMouse},        // context menu, until the choice
	} {
		if !o.open {
			continue
		}
		if k.Code == term.Mouse {
			o.mouse(k.Mouse)
		} else {
			o.key(k)
		}
		return
	}
	if u.pasteAsk != "" && u.pasteKey(k) {
		return
	}
	if u.sendAsk != nil && u.sendKey(k) {
		return
	}
	if u.pager != nil && u.pagerKey(k) {
		return
	}
	if u.ask != nil && u.askKey(k) {
		return
	}
	if u.search != nil && u.searchKey(k) {
		return
	}
	if u.spellFix != nil && k.Code != term.Mouse && u.spellFixKey(k) {
		return
	}
	if u.mention != nil && k.Code != term.Mouse && u.mentionKey(k) {
		return
	}
	w := u.view()
	switch {
	case k.Code == term.Mouse:
		u.mouse(k.Mouse)
	case k.Code == term.F2 && k.Shift: // /net cycle, like /net with no argument
		u.cycleNet()
	case k.Code == term.F2:
		// The width of the message area changes: everything is wrapped again.
		u.nextSide()
		u.clear()
	case k.Code == term.F3: // members of the chat, on top
		u.toggleParts()
	case k.Code == term.F6, k.Alt && k.Rune == 'a': // aggregated view (Alt+A kept, taken by some window managers)
		u.setAggregate(!u.aggregate)
		u.saveCfg()
	case k.Code == term.F4: // cycle of the image display mode, like /set images
		u.applyImages(nextImages(u.images, u.t.Kitty))
		u.saveCfg()
	case k.Code == term.F5: // image on hover, like /set images_hover
		u.cfg.ImagesHover = !u.cfg.ImagesHover
		u.clear()                                                // the lines kept free show or go
		if u.cfg.ImagesHover && u.cfg.Hover == config.HoverOff { // with no hover, no image shows any more
			u.sys(i18n.T("hover_images_disabled"))
		}
		u.saveCfg()
	case k.Code == term.F7: // sidebar sort, like /set sidebar_sort
		u.cycleSort()
	case k.Code == term.Ctrl:
		switch k.Rune {
		case 'x':
			u.goTo(u.cycleTarget())
		case 'a':
			u.edHome()
		case 'e':
			u.edEnd()
		case 't':
			u.openPicker(func(s string) { u.ed.Insert(s) })
		case 'v': // Ctrl+V (Ctrl+Insert sends it too): image from the clipboard
			u.pasteClip()
		case 'f':
			// The selection running stays: the search changes only the view.
			u.search = &searchState{cur: -1}
		case 'n': // free outside a search (Ctrl+N goes to the next hit there)
			u.openNewChat()
		case 'g':
			u.openGifs("")
		case 'k':
			u.ed.KillToEnd()
		case 'u':
			u.ed.KillLine()
		case 'w':
			u.ed.KillWord()
		case 'r': // spell check walk (Enter fixes, i ignores, a adds)
			u.spellFixStart()
		case 'l':
			u.clear()
		case 'c':
			u.cancel()
		}
	case k.Alt && k.Rune >= '0' && k.Rune <= '9':
		u.goTo(int(k.Rune - '0'))
	case k.Alt && k.Code == term.Left:
		u.ws.Prev()
		u.goTo(u.ws.Cur)
	case k.Alt && k.Code == term.Right:
		u.ws.Next()
		u.goTo(u.ws.Cur)
	case k.Alt && k.Code == term.Up:
		u.selMove(w, -1)
	case k.Alt && k.Code == term.Down:
		u.selMove(w, 1)
	case k.Alt && k.Code == term.Backspace:
		u.ed.KillWord()
	case k.Code == term.Esc:
		if u.multi {
			// The draft stays: the fold only changes the shape of the zone.
			u.multi = false
			u.flash(i18n.T("multiline_hint"))
			return
		}
		u.cancelMode()
		u.setSel(w, nil)
	case k.Code == term.Enter && (k.Shift || k.Alt) && u.prompt == nil:
		// Shift/Alt+Enter: line break (shown as ⏎), Enter sends everything.
		// It needs the kitty keyboard protocol; on a login answer, no.
		// With /set multiline on, it also expands the input zone.
		if u.multilineOn() {
			u.multi = true
		}
		u.ed.Insert("\n")
		u.sendTyping(w)
	case k.Code == term.Enter:
		u.submit()
		if u.ed.String() == "" { // input consumed (a refused command puts it back)
			u.multi = false
		}
	case k.Code == term.Backspace:
		u.ed.Backspace()
	case k.Code == term.Delete:
		u.ed.Delete()
	case k.Ctrl && k.Code == term.Left:
		u.ed.WordLeft()
	case k.Ctrl && k.Code == term.Right:
		u.ed.WordRight()
	case k.Code == term.Left:
		u.ed.Left()
	case k.Code == term.Right:
		u.ed.Right()
	case k.Code == term.Home:
		u.edHome()
	case k.Code == term.End:
		u.edEnd()
	case k.Code == term.Up:
		if u.multi {
			u.ed.CursorUp()
			return
		}
		// While editing, the input history does not wipe the preloaded text.
		if u.edit == nil && !u.editLast(w) {
			u.ed.Up()
		}
	case k.Code == term.Down:
		if u.multi {
			u.ed.CursorDown()
			return
		}
		if u.edit == nil {
			u.ed.Down()
		}
	case k.Code == term.PgUp:
		u.scroll(w, u.viewRows()/2)
	case k.Code == term.PgDn:
		u.scroll(w, -u.viewRows()/2)
	case k.Code == term.Tab:
		u.ed.Complete(u.candidates)
	case k.Code == term.Paste:
		u.pasteText(w, normalizePaste(k.Text))
	case k.Code == term.None && k.Rune != 0 && !k.Alt:
		if !u.selKey(w, k.Rune) {
			u.ed.Insert(string(k.Rune))
			u.sendTyping(w)
		}
	}
}

// multilineOn : /set multiline, nil-safe (tests build a UI with no config).
func (u *UI) multilineOn() bool { return u.cfg != nil && u.cfg.Multiline }

// edHome / edEnd : Home/End and Ctrl+A/E stay on the current line in the
// expanded editor, and keep the whole buffer elsewhere.
func (u *UI) edHome() {
	if u.multi {
		u.ed.LineHome()
		return
	}
	u.ed.Home()
}

func (u *UI) edEnd() {
	if u.multi {
		u.ed.LineEnd()
		return
	}
	u.ed.End()
}

// sendTyping tells "typing" in w, at most once every 5 s per chat
// (u.lastTyping). Nothing in the aggregate (w.Chat nil), a /search result
// (w.Search) or the authentication prompt (u.prompt): no real message being
// typed.
func (u *UI) sendTyping(w *Window) {
	if w.Chat == nil || w.Search != "" || u.prompt != nil {
		return
	}
	b := u.net(w.Chat)
	if b == nil {
		return
	}
	if now := time.Now(); now.Sub(u.lastTyping[w.Chat.Key()]) >= 5*time.Second {
		u.lastTyping[w.Chat.Key()] = now
		b.Typing(u.ctx, w.Chat, false)
	}
}

// openPicker opens the centred emoji picker; onPick gets the choice.
func (u *UI) openPicker(onPick func(string)) {
	u.picker = newPicker(min(60, u.t.Cols-4), min(14, u.t.Rows-4), onPick)
}

func (u *UI) submit() {
	if p := u.prompt; p != nil { // login answer, never in the history
		line := u.ed.String()
		u.ed.Set("")
		u.prompt = nil
		p.Reply <- line
		return
	}
	line := u.ed.Submit()
	if it := u.edit; it != nil { // while editing, the text is never a command
		u.edit = nil
		u.applyEdit(u.view(), it, strings.TrimSpace(line))
		return
	}
	if a := u.sendAsk; a != nil && a.caption { // Enter sends; an empty caption = no caption
		u.sendAsk = nil
		u.sendPath(a.chat, a.path, strings.TrimSpace(line), a.tmp) // the paste is dropped after the send
		u.ed.Set(a.draft)                                          // draft broken off by the "l"
		return
	}
	if strings.TrimSpace(line) == "" {
		// Search window: nothing to send, Enter joins the selected result in
		// the chat.
		if w := u.view(); w.Search != "" && w.Sel != nil && w.Sel.Msg != nil {
			u.jumpTo(u.chatOf(w.Sel.Msg), w.Sel.Msg.ID)
		}
		return
	}
	name, args, text, ok := ParseCommand(line)
	if ok {
		if strings.Contains(line, "\n") { // a command fits on one line
			u.sys(i18n.T("multiline_command_refused"))
			u.ed.Set(line)
			return
		}
		u.command(name, args, text)
		return
	}
	u.send(u.sendWin(), text)
}

func (u *UI) send(w *Window, text string) {
	u.sendWith(w, text, false)
}

// sendWith : pre=true sends text as a code block (multiline paste).
func (u *UI) sendWith(w *Window, text string, pre bool) {
	if w.Chat == nil {
		w.AddSys(i18n.T("window_not_bound"))
		return
	}
	// Before the pending message is put in the window: nothing is shown as on
	// its way when nobody can send it. No user message here — a chat always
	// carries its network, so this is a guard, not a case to explain.
	b := u.net(w.Chat)
	if b == nil {
		return
	}
	b.Typing(u.ctx, w.Chat, true)
	u.tmpID++
	me := u.selfOf(w.Chat.Net)
	m := &model.Msg{Net: w.Chat.Net, ChatID: w.Chat.ID, ChatLabel: w.Chat.Title, Date: time.Now(), From: me.Name, FromID: me.ID,
		Out: true, Text: text, Pending: true, TmpID: u.tmpID}
	// The pending reply counts only for the chat aimed at: /msg aims at
	// another chat and does not use it up.
	replyTo := 0
	if it := u.reply; it != nil && it.Msg != nil && it.Msg.Key() == w.Chat.Key() {
		u.reply = nil
		if q := quoteOf(it); q.ID != 0 {
			m.Reply, replyTo = &q, q.ID
		}
	}
	if pre && replyTo == 0 {
		m.Entities = []model.Span{{End: len([]rune(text)), Kind: model.SpanPre}}
	}
	var segs []model.Seg
	if !pre && replyTo == 0 {
		if segs = parseFences(text); segs != nil {
			m.Text, m.Entities = fenceText(segs), fenceEntities(segs)
		}
	}
	// w is never the aggregate here: with no chat, the function has already given the lead back.
	u.insertPending(w, m)
	switch {
	case replyTo != 0:
		// ponytail: reply + paste as a code block: the reply wins, the text
		// goes out raw; SendReply would otherwise take one mode more.
		b.SendReply(u.ctx, w.Chat, text, replyTo, u.tmpID)
	case pre:
		b.SendPre(u.ctx, w.Chat, text, u.tmpID)
	case segs != nil:
		b.SendStyled(u.ctx, w.Chat, segs, u.tmpID)
	default:
		b.Send(u.ctx, w.Chat, text, u.tmpID)
	}
}

// insertPending puts the message sent locally into w and into the aggregate
// (my sends show there too), and settles the selection and the scroll.
func (u *UI) insertPending(w *Window, m *model.Msg) {
	w.Upsert(m)
	u.setSel(w, nil)
	w.Scroll = 0
	u.agg.Upsert(m)
	if u.view() == u.agg {
		u.setSel(u.agg, nil)
		u.agg.Scroll = 0
	}
}

// meMsg builds the text and the italic entity of /me <text>: "* <meName>
// <text>", in italics over its whole length (IRC convention).
func meMsg(meName, arg string) (string, []model.Span) {
	text := "* " + meName + " " + arg
	return text, []model.Span{{End: len([]rune(text)), Kind: model.SpanItalic}}
}

// sendMe : /me <text>.
func (u *UI) sendMe(w *Window, arg string) {
	if w.Chat == nil {
		w.AddSys(i18n.T("window_not_bound"))
		return
	}
	b := u.net(w.Chat)
	if b == nil {
		return // same guard as sendWith: no pending message with no sender
	}
	u.tmpID++
	me := u.selfOf(w.Chat.Net)
	text, ents := meMsg(me.Name, arg)
	m := &model.Msg{Net: w.Chat.Net, ChatID: w.Chat.ID, ChatLabel: w.Chat.Title, Date: time.Now(), From: me.Name, FromID: me.ID,
		Out: true, Text: text, Entities: ents, Pending: true, TmpID: u.tmpID}
	u.insertPending(w, m)
	b.SendStyled(u.ctx, w.Chat, []model.Seg{{Text: text, Kind: model.SegItalic}}, u.tmpID)
}

// candidates for Tab: dispatch by command context (complContext).
func (u *UI) candidates(word string, atStart bool) []string {
	src, tail, setKey := complContext(u.ed.String(), u.ed.Cursor())
	switch src {
	case complCommands:
		return commandNames
	case complChats:
		var names []string
		for _, c := range u.chatList {
			if c.Username != "" {
				names = append(names, c.Username)
			}
			names = append(names, u.title(c)) // the local name can be completed, findChat resolves it
		}
		return multiWord(word, tail, names)
	case complWindows:
		out := []string{"new", "close", "list"}
		for i, w := range u.ws.List {
			out = append(out, strconv.Itoa(i))
			if w.Chat != nil {
				out = append(out, u.winName(w))
			}
		}
		return out
	case complWindowNewArg:
		return []string{"hide"}
	case complSetKey:
		return setKeys
	case complSetValue:
		return setValues(setKey)
	case complTheme:
		return multiWord(word, tail, theme.Names())
	case complHelp:
		return helpCandidates()
	case complNet:
		return append(u.netNames(), netAll)
	case complPath:
		// The editor completes its last word; a path with a space is longer
		// than that word, so the candidates are cut to the part after the
		// last space of what was typed.
		cut := len(tail) - len(word)
		var out []string
		for _, c := range pathCandidates(tail) {
			if len(c) >= cut {
				out = append(out, c[cut:])
			}
		}
		return out
	case complFold:
		return multiWord(word, tail, sectionKeys(u.foldSections()))
	default:
		return nil
	}
}

// tick : typing timeout, clock, animation. true when a repaint is worth it.
func (u *UI) tick() bool {
	now := time.Now()
	redraw := false
	if len(u.dirty) > 0 && now.Sub(u.flushed) >= flushEvery {
		u.flushCache(false)
	}
	for k, t := range u.typing {
		if now.After(t.until) {
			delete(u.typing, k)
			redraw = true
		}
	}
	if c := now.Format("15:04"); c != u.clock {
		u.clock, redraw = c, true
	}
	if u.flashMsg != "" && now.After(u.flashUntil) {
		u.flashMsg, redraw = "", true
	}
	if q := u.qr; q != nil { // countdown of the token
		if n := q.left(now); n != q.shown {
			q.shown, redraw = n, true
		}
	}
	// Global search: the query goes out only once the typing has settled.
	if g := u.gsearch; g != nil && !g.typed.IsZero() && now.Sub(g.typed) >= gsDelay {
		g.typed = time.Time{}
		u.gsSend()
		redraw = true
	}
	// New chat: the server search waits for the end of the typing.
	if n := u.newChat; n != nil && !n.typed.IsZero() && now.Sub(n.typed) >= ncDelay {
		n.typed = time.Time{}
		u.ncSend()
		redraw = true
	}
	// GIF box: same rule, the query goes out once the typing has settled.
	if g := u.gifs; g != nil && !g.typed.IsZero() && now.Sub(g.typed) >= gsDelay {
		g.typed = time.Time{}
		u.gifQuery()
		redraw = true
	}
	if u.marqueeTick(now) { // always called (no lazy ||): its rate depends on it
		redraw = true
	}
	return u.animate(now) || redraw
}
