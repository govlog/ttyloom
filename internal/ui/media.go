package ui

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
)

// evFrames : decoded frames (UI goroutine → UI loop).
type evFrames struct {
	Media  *model.Media
	Frames *media.Frames
	Gen    int // generation of the preview (0: window media)
	// ctx : context of the decoding that produced this result. Cancelled =
	// nobody waits for it any more ("s", window closed, media freed), and it
	// lands on nothing.
	ctx context.Context
	// CellW/CellH : cell size of the decoding, carried to the media on arrival.
	// A stale result that comes last leaves its stale mark there: the next
	// placement starts again, and the state settles.
	CellW, CellH int
	// Partial : middle report of a video decoding that runs; the frames grow,
	// the decoding goes on.
	Partial bool
	Err     string
}

// avatarsOn tells whether avatars show (kitty only, the image is 2 cells).
func (u *UI) avatarsOn() bool { return u.images == "kitty" && u.cfg.Avatars }

// noteAvatar keeps the profile photo of a peer without downloading anything:
// the load starts at the first display (avatarAt).
// ponytail: no refresh when the peer changes photo; the cached file and the
// kitty image live for the session. To follow it, one would have to compare
// PhotoID and start again from MediaNone.
func (u *UI) noteAvatar(k model.ChatKey, loc any) {
	if k.ID == 0 || loc == nil || u.avatars[k] != nil {
		return
	}
	u.avatars[k] = &model.Media{Kind: model.MediaAvatar, Loc: loc, Ext: ".jpg", Mime: "image/jpeg"}
}

// avatarAt gives the avatar ready to place for the peer k, nil otherwise; it
// starts the load at the first display. The net of the key comes from what
// shows the avatar (the message, or the chat of the sidebar line): a photo
// handle only means something to the network that gave it.
func (u *UI) avatarAt(k model.ChatKey) *model.Media {
	md := u.avatars[k]
	if md == nil || !u.avatarsOn() {
		return nil
	}
	if md.State == model.MediaNone {
		b := u.netOf(k.Net)
		if b == nil {
			return nil
		}
		md.State = model.MediaLoading
		b.Download(u.ctx, md, avatarPath(config.Expand(u.cfg.DownloadDir), k))
	}
	if md.State != model.MediaReady || len(md.Frames) == 0 {
		return nil
	}
	return md
}

// avatarPath : file of the avatar of the peer k, under its network — ids
// collide between networks, and "file already there" would otherwise hand one
// network the photo of another.
func avatarPath(dir string, k model.ChatKey) string {
	return filepath.Join(dir, "avatars", k.Net, strconv.FormatInt(k.ID, 10)+".jpg")
}

// resetAvatars : avatars to load again (change of image mode or of cell
// size). u.t.Kitty and not u.images: the mode may already have switched.
func (u *UI) resetAvatars() {
	for _, md := range u.avatars {
		u.cancelDecode(md)
		s := u.kittyFree(md) // ids and LRU cleaned even when the mode has changed
		if u.t.Kitty {
			u.t.WriteString(s)
		}
		md.State, md.Frames = model.MediaNone, nil
	}
}

// autoMedia starts the download when the media can be previewed and is under the limit.
func (u *UI) autoMedia(m *model.Msg, visible bool) {
	md := m.Media
	if md == nil || md.State != model.MediaNone || !md.Previewable() || u.images == "off" {
		return
	}
	if md.Kind == model.MediaMap && (!u.cfg.Maps || !visible) { // third-party network: the user turns it on
		return
	}
	if md.Kind == model.MediaWebPage && !u.cfg.LinkPreviews { // cut previews: nothing to show
		return
	}
	if u.cfg.AutoMediaMaxKB <= 0 || md.Size > int64(u.cfg.AutoMediaMaxKB)*1024 {
		return
	}
	u.download(m)
}

// autoMediaPage : items swept by autoMediaWin, as many as one history page
// (loadHistory asks for 100). A window replayed from the disk cache holds up
// to cache_messages messages: starting every one of their downloads would
// flood the link for messages nobody scrolls back to.
//
// The cached messages older than that page load when their line shows
// (showItem, from draw).
const autoMediaPage = 100

// autoMediaWin starts the download of the media of the last items of w. The
// history replayed from the disk cache (bindChat) is covered by nothing else:
// history() only sweeps the ids of the network page, and there is no page at
// all when the sync brought nothing back — the window is then already Loaded
// and its media would stay labels until the next F4. Called where a window is
// shown (goTo, attach); the State != MediaNone guard of autoMedia makes every
// later pass a no-op, so no media is downloaded twice.
func (u *UI) autoMediaWin(w *Window) {
	items := w.Items
	if len(items) > autoMediaPage {
		items = items[len(items)-autoMediaPage:]
	}
	for _, it := range items {
		if it.Msg != nil {
			u.autoMedia(it.Msg, false)
		}
	}
}

func (u *UI) download(m *model.Msg) {
	b := u.netOf(m.Net)
	if b == nil {
		return
	}
	md := m.Media
	if md.Kind == model.MediaMap {
		u.downloadMap(b, md)
		return
	}
	title := ""
	if c := u.chats[m.Key()]; c != nil {
		title = c.Title
	}
	path := filepath.Join(config.Expand(u.cfg.DownloadDir), media.FileName(m.Key(), title, m.ID, m.Date, md.Ext))
	md.State = model.MediaLoading
	u.invalidateMedia(md)
	b.Download(u.ctx, md, path)
}

// downloadMap : "download" of an OSM map — no Telegram Loc, a separate
// network path (Backend.DownloadMap) but the same event back
// (model.EvDownloaded): downloaded() treats it like a photo. The backend is
// handed down by download(): a Media alone names no network.
func (u *UI) downloadMap(b model.Backend, md *model.Media) {
	path := filepath.Join(config.Expand(u.cfg.DownloadDir), "maps", media.MapName(md.Lat, md.Long))
	md.State = model.MediaLoading
	u.invalidateMedia(md)
	b.DownloadMap(u.ctx, md, path)
}

func (u *UI) invalidateMedia(md *model.Media) {
	for _, w := range u.ws.List {
		w.InvalidateMedia(md)
	}
	u.agg.InvalidateMedia(md)
}

func (u *UI) downloaded(e model.EvDownloaded) {
	md := e.Media
	if u.gifOrphan[md] { // preview of a GIF box closed meanwhile: nobody waits for it
		delete(u.gifOrphan, md)
		return
	}
	if e.Err != "" {
		md.State, md.Err = model.MediaFailed, e.Err
		if v := u.viewer; v != nil && v.src == md {
			v.md.State, v.md.Err = model.MediaFailed, e.Err
		}
		u.invalidateMedia(md)
		return
	}
	md.Path = e.Path
	if u.openNext[md] {
		delete(u.openNext, md)
		u.open(md.Path)
	}
	if v := u.viewer; v != nil && v.src == md {
		u.viewLoad() // preview open, waiting for the file
	}
	if g := u.gifs; g != nil && g.owns(md) { // preview of the GIF box: its cell, a few frames
		u.gifDecode(md)
		return
	}
	if !md.Previewable() || u.images == "off" {
		md.State = model.MediaReady
		u.invalidateMedia(md)
		return
	}
	maxW, maxH := u.imageBox(md)
	u.loadFrames(md, maxW, maxH, u.frameCount(md), 0)
}

// frameCount gives the frames to decode for md — 1 for a still image as well
// as for the thumbnail of a video, the whole GIF, the config cap for a video
// whose play has started.
func (u *UI) frameCount(md *model.Media) int {
	switch {
	case md.Kind == model.MediaGIF:
		return 100
	case render.Playing(md):
		return u.videoFrames()
	}
	return 1
}

// autoplay : video = autoplay, the whole clip decoded so that animate() runs
// it and loops it with no key press ("s" stops it, "l" takes the lead back).
// Called from draw() for the lines on the screen only: it used to start from
// the download on, so a hidden window or a message far above the view decoded
// its videos whole for nothing.
func (u *UI) autoplay(it *Item) {
	if u.cfg.Video != "autoplay" || u.images == "off" {
		return
	}
	md := mediaOfItem(it) // downloaded and decoded: State ready
	// Want != 0 : a decoding already runs. Paused : stopped by "s", or a clip
	// with a single frame (framesLoaded), and neither asks to be played again.
	if md == nil || md.Kind != model.MediaVideo || md.Path == "" ||
		md.Paused || md.Want != 0 || len(md.Frames) > 1 {
		return
	}
	maxW, maxH := u.imageBox(md)
	u.loadFrames(md, maxW, maxH, u.videoFrames(), 0)
}

// showItem : the line of it is on the screen. Its media starts its download
// when nothing holds it — never asked, or frames dropped by framesBudget: the
// file on disk gives the lead back at once — and an autoplay video takes its
// frames. it is nil on a separator line.
func (u *UI) showItem(it *Item) {
	if it == nil || it.Msg == nil {
		return
	}
	u.autoMedia(it.Msg, true)
	u.autoplay(it)
}

// videoFrames gives the frame cap of a video play (media bounds it in turn in
// number and in bytes). Two at least: under that, nothing to animate.
func (u *UI) videoFrames() int { return max(u.cfg.VideoFrames, 2) }

// playPause : key "l" on a downloaded video. First press: the frames decode
// in the background in maxW x maxH px (0x0: the box of the image block of the
// message), and the animation starts at the first partial report. After that:
// pause / resume.
// ponytail: no sound — "o" opens the video with the desktop player.
func (u *UI) playPause(md *model.Media, maxW, maxH, gen int) {
	if render.Playing(md) {
		md.Paused = !md.Paused
		md.Next = time.Now().Add(md.Delay)
		u.invalidateMedia(md) // the palette switches "l play" ↔ "l pause"
		return
	}
	if u.images == "off" {
		u.sys(i18n.T("images_off"))
		return
	}
	if !media.HaveFFmpeg {
		u.sys(i18n.T("ffmpeg_missing"))
		return
	}
	if maxW <= 0 || maxH <= 0 {
		maxW, maxH = u.imageBox(md)
	}
	md.Paused = false
	u.loadFrames(md, maxW, maxH, u.videoFrames(), gen)
}

// stopVideo : key "s" — back to the first frame, play stopped. The frames
// stay in memory, "l" starts again at once.
func (u *UI) stopVideo(md *model.Media) {
	if u.cancelDecode(md) {
		// The decoding stopped in the middle: what it had already given is a
		// truncated clip. Kept whole, render.Playing would stay true and "l"
		// would only flip the pause, looping that stump for ever. One frame
		// left, and "l" goes back through loadFrames.
		md.Frames = md.Frames[:min(1, len(md.Frames))]
	}
	md.Paused, md.Frame = true, 0
	u.retireKitty(md) // the screen carries the last animated frame
	u.invalidateMedia(md)
}

// cancelDecode ends the decoding running on md, if there is one: the ffmpeg
// goes with it and the result, should it still come, lands on nothing
// (framesLoaded). One decoding at a time per media, preview included — the
// marks md.Want and md.CellW are already shared by the two paths.
// true when a decoding was really running.
func (u *UI) cancelDecode(md *model.Media) bool {
	cancel := u.decodes[md]
	if cancel == nil {
		return false
	}
	cancel()
	delete(u.decodes, md)
	md.Want = 0 // the "decoding %" label stops with the decoding
	return true
}

// loadFrames decodes md in the background in maxW x maxH px; the result comes
// back through evFrames, tagged gen (generation of the preview, 0 elsewhere).
func (u *UI) loadFrames(md *model.Media, maxW, maxH, frames, gen int) {
	path, mime, kind := md.Path, md.Mime, md.Kind // captured: the goroutine does not read the state of the UI
	cw, ch, _, _ := u.cells()
	if cw <= 0 || ch <= 0 {
		// Kitty with a cell size not known yet: render.Box would give a block of
		// zero line, the image would never be placed, and redecode — which runs
		// at placement time — would never start it again. The media stays
		// loading rather than failing; the mode is resolved again, and the
		// media decoded again, at the first size that carries the cell.
		return
	}
	u.cancelDecode(md)          // the decoding before, if one is still running
	md.CellW, md.CellH = cw, ch // mark set at start: only one decoding in flight
	md.Want = frames            // progress shown in the label
	ctx, cancel := context.WithCancel(u.ctx)
	if u.decodes == nil {
		u.decodes = map[*model.Media]context.CancelFunc{}
	}
	u.decodes[md] = cancel
	go func() {
		// No cancel() here: the result is on its way to the UI loop, which reads
		// the context to know whether anybody still wants it. framesLoaded ends
		// the context once it has taken the frames.
		defer func() {
			if r := recover(); r != nil {
				u.events <- evFrames{Media: md, Gen: gen, ctx: ctx, Err: i18n.T("panic", r)}
			}
		}()
		var progress []func(*media.Frames)
		if kind == model.MediaVideo && frames > 1 { // play: shown from the first frames on
			progress = append(progress, func(f *media.Frames) {
				u.events <- evFrames{Media: md, Frames: f, Gen: gen, ctx: ctx, CellW: cw, CellH: ch, Partial: true}
			})
		}
		f, err := media.Load(ctx, path, mime, maxW, maxH, frames, progress...)
		ev := evFrames{Media: md, Frames: f, Gen: gen, ctx: ctx, CellW: cw, CellH: ch}
		if err != nil {
			ev.Err = err.Error()
		}
		u.events <- ev
	}()
}

// imageBox gives the target size in pixels for the display mode.
func (u *UI) imageBox(md *model.Media) (int, int) {
	cellW, cellH, maxCols, maxRows := u.cells()
	if md.Kind == model.MediaAvatar {
		return 2 * cellW, cellH
	}
	if md.Kind == model.MediaWebPage {
		maxRows = min(maxRows, render.MaxLinkRows) // same cap as the block of the message
	}
	cols, rows := maxCols, maxRows
	if md.W > 0 && md.H > 0 {
		cols, rows = render.Box(md.W, md.H, maxCols, maxRows, cellW, cellH)
	}
	return cols * cellW, rows * cellH
}

func (u *UI) framesLoaded(e evFrames) {
	// Decoding cancelled ("s", window closed, media freed): its result, partial
	// or final, is of no use to anybody.
	if e.ctx != nil && e.ctx.Err() != nil {
		return
	}
	if !e.Partial {
		if cancel := u.decodes[e.Media]; cancel != nil {
			cancel() // decoding over, result taken: the context ends with it
			delete(u.decodes, e.Media)
		}
	}
	// Preview: a decoding started for a stale box (resize) or for a preview
	// already closed does not overwrite the current result.
	if e.Gen != 0 {
		if v := u.viewer; v == nil || v.md != e.Media || v.gen != e.Gen {
			return
		}
	}
	md := e.Media
	// Decoding back after a free (window closed, display mode changed):
	// MediaNone is set only by those paths, so the media must not come back to
	// life with its frames and start animating again.
	if e.Gen == 0 && md.State == model.MediaNone {
		return
	}
	if e.Err != "" {
		md.State, md.Err, md.Want = model.MediaFailed, e.Err, 0
		u.invalidateMedia(md)
		return
	}
	// Video play: the frames only grow, the image in place stays good and the
	// animation does not start from zero at each report.
	grow := e.Partial || (render.Playing(md) && len(e.Frames.PNG) >= len(md.Frames))
	if !grow {
		md.Frame, md.Next = 0, time.Now().Add(e.Frames.Delay)
	}
	// New frames (first decoding or zoom): the image sent to the terminal is
	// stale. Its id leaves the media — the next placement sends it again — but
	// it is freed only at the end of the next frame, after the new one is
	// placed. A play that runs keeps its own: the next frame goes right after
	// through animate(), with no blink — but on pause, where nothing would be
	// sent again.
	if !e.Partial && (!grow || md.Paused) {
		u.retireKitty(md)
	}
	want := md.Want // frames asked of this decoding, before the counter is cleared
	md.Frames, md.FrameW, md.FrameH, md.Delay = e.Frames.PNG, e.Frames.W, e.Frames.H, e.Frames.Delay
	md.CellW, md.CellH = e.CellW, e.CellH
	if !e.Partial {
		md.Want = 0
	}
	md.State, md.Err = model.MediaReady, ""
	// A play decoding that comes back with a single frame has nothing to
	// animate: the media stays at rest, and autoplay does not ask for it again
	// at each repaint.
	if !e.Partial && want > 1 && len(md.Frames) < 2 {
		md.Paused = true
	}
	u.framesLRU.note(md)
	u.evictFrames(md)
	u.invalidateMedia(md) // the kitty send starts from draw(), at placement time
}

// animate moves the visible GIFs of the current window on. true when a repaint is needed (halfblock).
// In kitty the frame is sent straight to its position, on the second buffer
// (Ghostty does not animate on the terminal side: a=f/a=a not supported).
func (u *UI) animate(now time.Time) bool {
	redraw := false
	wrote := false
	x0, _ := u.layout()
	for _, p := range u.placed {
		md := p.img.Media
		if len(md.Frames) < 2 || md.Paused || now.Before(md.Next) {
			continue
		}
		md.Frame = (md.Frame + 1) % len(md.Frames)
		md.Next = now.Add(md.Delay)
		if u.images == "kitty" {
			// Double buffer: the new frame is sent and placed on the other id, and
			// it covers the old one; the placement of the old one is erased only
			// after (d=i: placements only, data kept). Erasing first gave a black
			// hole for the time of the decoding.
			old := md.KittyID
			if md.KittyAlt == 0 {
				u.kittyID++
				md.KittyAlt = u.kittyID
			}
			pid := p.pid // same pid as draw(): replacement in place
			u.t.WriteString("\x1b[s")
			u.t.WriteString("\x1b[" + itoa(p.row+1) + ";" + itoa(x0+p.img.Col+1) + "H")
			// p.crop : the zoomed preview keeps its sub-rectangle from one frame to the next.
			u.t.WriteString(media.KittyDisplay(md.KittyAlt, pid, md.Frames[md.Frame], p.img.Cols, p.img.Rows, p.crop))
			if old != 0 {
				u.t.WriteString(media.KittyDeletePlacement(old, 0))
			}
			u.t.WriteString("\x1b[u")
			// d=i took every placement of the old id: without this update, the
			// diff of the next frame would drop the new one.
			u.forgetKitty(old)
			if u.kplaced == nil {
				u.kplaced = map[kplace]bool{}
			}
			u.kplaced[kplace{id: md.KittyAlt, pid: pid}] = true
			md.KittyID, md.KittyAlt = md.KittyAlt, old
			wrote = true
		}
	}
	if u.images == "halfblock" {
		if g := u.gifs; g != nil { // GIF box: its previews on the screen
			lo, hi := g.visible()
			for i := lo; i < hi; i++ {
				if md := g.gifs[i].Preview; len(md.Frames) > 1 && !now.Before(md.Next) {
					md.Frame = (md.Frame + 1) % len(md.Frames)
					md.Next = now.Add(md.Delay)
					redraw = true
				}
			}
		}
		if v := u.viewer; v != nil { // preview: only its image is drawn
			md := v.md
			if len(md.Frames) > 1 && !md.Paused && !now.Before(md.Next) {
				md.Frame = (md.Frame + 1) % len(md.Frames)
				md.Next = now.Add(md.Delay)
				redraw = true
			}
		} else {
			for _, it := range u.view().Items {
				if md := mediaOfItem(it); md != nil && len(md.Frames) > 1 && !md.Paused && !now.Before(md.Next) {
					md.Frame = (md.Frame + 1) % len(md.Frames)
					md.Next = now.Add(md.Delay)
					it.lines = nil
					redraw = true
				}
			}
		}
	}
	if wrote {
		u.t.Flush()
	}
	return redraw
}

func mediaOfItem(it *Item) *model.Media {
	if it.Msg == nil || it.Msg.Media == nil || it.Msg.Media.State != model.MediaReady {
		return nil
	}
	return it.Msg.Media
}

func itoa(n int) string { return strconv.Itoa(n) }

// openMedia opens the nth media (from the end) of the window with xdg-open.
func (u *UI) openMedia(w *Window, n int) {
	for i := len(w.Items) - 1; i >= 0; i-- {
		it := w.Items[i]
		if it.Msg == nil || !render.Openable(it.Msg.Media) || !w.shown(it) {
			continue // shown: the nth from the end is counted on the screen, filter included
		}
		n--
		if n > 0 {
			continue
		}
		u.openItemMedia(w, it)
		return
	}
	w.AddSys(i18n.T("no_media"))
}

// openItemMedia opens the media of an item: the file when it is already
// there, else we download it and open it on arrival.
func (u *UI) openItemMedia(w *Window, it *Item) {
	if it.Msg == nil || !render.Openable(it.Msg.Media) {
		return
	}
	md := it.Msg.Media
	if render.SafeURL(md.URL) { // link preview: the page, never the thumbnail
		u.open(md.URL)
		return
	}
	if md.Path != "" {
		u.open(md.Path)
		return
	}
	if md.State == model.MediaLoading { // already running: we will open it on arrival
		u.openNext[md] = true
		return
	}
	u.openNext[md] = true
	w.AddSys(i18n.T("downloading", md.Label))
	u.download(it.Msg)
}

// openable : last net before xdg-open. The URLs from the network are already
// filtered by render.SafeURL (spans and link previews); only our own paths
// are left here, plus the scheme guard just in case.
func openable(s string) bool {
	if i := strings.IndexAny(s, ":/"); i < 0 || s[i] != ':' {
		return true // no scheme in front: a path
	}
	return render.SafeURL(s)
}

func (u *UI) open(path string) {
	if !openable(path) {
		u.sys(i18n.T("open_refused", render.Truncate(render.CleanLine(path), 60, "…")))
		return
	}
	// .bin = extension neutralised at download time (by the backend): the type is
	// not on the allow list, so the file stays on disk and never reaches the
	// desktop, which would run it. Local files only: a URL ending in .bin is
	// for the browser to judge.
	if i := strings.IndexAny(path, ":/"); (i < 0 || path[i] != ':') && strings.HasSuffix(strings.ToLower(path), ".bin") {
		u.sys(i18n.T("open_denied_type", render.Truncate(render.CleanLine(filepath.Base(path)), 60, "…")))
		return
	}
	cmd := exec.Command("xdg-open", path)
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		u.sys(i18n.T("xdg_open_error", err))
		return
	}
	go cmd.Wait()
}

// reloadMedia starts again from zero for every shown media (image mode change).
func (u *UI) reloadMedia() {
	u.dropKitty() // first of all: otherwise draw would place the old images again
	u.resetAvatars()
	for _, w := range u.ws.List {
		u.freeImages(w)
		for _, it := range w.Items {
			// No filter on State: freeImages has just set the kitty images back to
			// MediaNone. autoMedia filters again (Previewable, size, off mode) and
			// Backend.Download gives the lead back at once when the file is already there.
			if it.Msg != nil && it.Msg.Media != nil {
				u.cancelDecode(it.Msg.Media)
				it.Msg.Media.State, it.Msg.Media.Frames, it.Msg.Media.Err = model.MediaNone, nil, ""
				it.Msg.Media.Want, it.Msg.Media.Frame = 0, 0
				it.lines = nil
				u.autoMedia(it.Msg, false)
			}
		}
	}
}

// lru : media whose image lives in the terminal, from the oldest to the
// newest. The terminal has a quota: above the cap we free the oldest ones,
// the frames stay in memory and sending them again is free.
// ponytail: linear search over cap entries — kitty_images (48 by default),
// raised by draw() to what is on the screen; an index map if the cap ever
// gets large.
type lru struct {
	cap  int
	list []*model.Media
}

// note moves md to last used.
func (l *lru) note(md *model.Media) {
	l.drop(md)
	l.list = append(l.list, md)
}

// touch : note, and gives back the media dropped over the cap.
func (l *lru) touch(md *model.Media) []*model.Media {
	l.note(md)
	if len(l.list) <= l.cap {
		return nil
	}
	n := len(l.list) - l.cap
	out := slices.Clone(l.list[:n])
	l.list = slices.Delete(l.list, 0, n)
	return out
}

// drop takes md out of the tracking (image freed elsewhere).
func (l *lru) drop(md *model.Media) {
	if i := slices.Index(l.list, md); i >= 0 {
		l.list = slices.Delete(l.list, i, i+1)
	}
}

// framesBudget : bytes of decoded frames kept in memory, every media together.
// A Discord GIF at the inline box weighs 12 to 15 MB of PNG, a played video
// up to videoBudget; kept for the life of the window, a session of GIFs went
// past 7 GB. Over the budget, the media not shown for the longest time lose
// their frames and decode again from their file when they show again.
// ponytail: a constant — a config key if 256 MB proves wrong on some machine.
var framesBudget = 256 << 20

func framesBytes(md *model.Media) int {
	n := 0
	for _, f := range md.Frames {
		n += len(f)
	}
	return n
}

// noteShown : the media on the screen go to the tail of framesLRU, at the end
// of each draw — eviction starts from the head, the ones not seen for the longest.
func (u *UI) noteShown() {
	for _, h := range u.hits {
		if h.item != nil && h.item.Msg != nil && h.item.Msg.Media != nil && len(h.item.Msg.Media.Frames) > 0 {
			u.framesLRU.note(h.item.Msg.Media)
		}
	}
	for _, p := range u.placed {
		u.framesLRU.note(p.img.Media)
	}
}

// evictFrames keeps the frames of every media under framesBudget: from the
// head of framesLRU, the media off the screen lose theirs (dropFrames) until
// what is left fits. What the screen shows and the frames that just landed
// (keep) stay whatever the budget — dropped, they would be decoded again at
// the next repaint, for ever.
func (u *UI) evictFrames(keep *model.Media) {
	l := &u.framesLRU
	l.list = slices.DeleteFunc(l.list, func(md *model.Media) bool { return len(md.Frames) == 0 }) // freed elsewhere
	total := 0
	for _, md := range l.list {
		total += framesBytes(md)
	}
	if total <= framesBudget {
		return
	}
	shown := map[*model.Media]bool{keep: true}
	for _, h := range u.hits {
		if h.item != nil && h.item.Msg != nil {
			shown[h.item.Msg.Media] = true
		}
	}
	for _, p := range u.placed {
		shown[p.img.Media] = true
	}
	if v := u.viewer; v != nil {
		shown[v.md], shown[v.src] = true, true
	}
	for i := 0; i < len(l.list) && total > framesBudget; {
		md := l.list[i]
		if shown[md] {
			i++
			continue
		}
		total -= framesBytes(md)
		u.dropFrames(md) // takes md out of the list: the next one is at i
	}
}

// dropFrames frees the frames of md and its image in the terminal. The media
// goes back to MediaNone: shown again, showItem starts it again from its file
// (Path stays). A decoding running on it ends with them.
func (u *UI) dropFrames(md *model.Media) {
	u.cancelDecode(md)
	u.framesLRU.drop(md)
	if md.KittyID == 0 && md.Frames == nil {
		return
	}
	if s := u.kittyFree(md); s != "" && u.t.Kitty {
		u.t.WriteString(s)
	}
	md.State, md.Frames = model.MediaNone, nil
	md.Want, md.Frame, md.Paused = 0, 0, false // a video being played goes with the rest
	u.invalidateMedia(md)                      // the aggregate and the search windows share the media
}

// kittyFree gives the sequences that free the images of the media in the
// terminal, with ids and LRU reset. The frames stay in memory: the next visit
// sends them again.
func (u *UI) kittyFree(md *model.Media) string {
	s := ""
	for _, id := range [...]uint32{md.KittyID, md.KittyAlt} {
		if id != 0 {
			s += media.KittyFree(id)
			u.forgetKitty(id) // d=I took the placements: nothing left to drop
		}
	}
	md.KittyID, md.KittyAlt = 0, 0
	u.kittyLRU.drop(md)
	return s
}

// retireKitty : the image sent is stale (frames decoded again). The media
// loses its id — the next placement sends it again — but the old one is freed
// only at the end of the frame, after the new one is placed: freeing first
// left the screen with no image for the time of the decoding (blink on zoom).
func (u *UI) retireKitty(md *model.Media) {
	for _, id := range [...]uint32{md.KittyID, md.KittyAlt} {
		if id != 0 {
			u.kittyOld = append(u.kittyOld, id)
		}
	}
	md.KittyID, md.KittyAlt = 0, 0
}

// forgetKitty drops from the set of the placements the ones of an image the
// terminal knows nothing about any more.
func (u *UI) forgetKitty(id uint32) {
	for k := range u.kplaced {
		if k.id == id {
			delete(u.kplaced, k)
		}
	}
}

// redecode : frames decoded for another cell size (font zoom) — we start the
// decoding again in the new box. Nothing is freed: the frames and the
// placement in place hold the screen (the terminal stretches them) until the
// result comes, hence no hole.
//
// Called at placement time, so only for what is on the screen: sweeping every
// window started up to one decoding per message. An image outside the view
// keeps its stale frames and is made again at its next placement — one
// repaint where its block is still in the old geometry, then it is settled.
func (u *UI) redecode(md *model.Media) {
	cellW, cellH, _, _ := u.cells()
	if md.Path == "" || (md.CellW == cellW && md.CellH == cellH) {
		return
	}
	maxW, maxH := u.imageBox(md)
	u.loadFrames(md, maxW, maxH, u.frameCount(md), 0)
}

// dropKitty : display mode change; we free everything, the next placement
// sends it again. A resize no longer goes through here: the placements of the
// next frame are enough.
func (u *UI) dropKitty() {
	list := u.kittyLRU.list
	u.kittyLRU.list = nil
	for _, md := range list {
		s := u.kittyFree(md)
		if u.t.Kitty {
			u.t.WriteString(s)
		}
	}
}
