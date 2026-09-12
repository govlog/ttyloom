package ui

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

// TestLoadDeferredUntilCell : in kitty mode, as long as the cell size is
// unknown (first startup in a fresh window: neither the CSI 16 t answer nor
// the TIOCGWINSZ pixels), decoding is deferred. A decode into a 0x0 box
// would give a zero-line image block, never placed, that redecode — which
// runs on placement — would never catch up on; and the media must also not
// end up in permanent failure.
func TestLoadDeferredUntilCell(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{Separator: true},
		events: make(chan model.Event, 4), images: "kitty", ctx: context.Background(),
		t: &term.Term{Cols: 80, Rows: 24, Kitty: true}}
	md := &model.Media{Kind: model.MediaPhoto, Path: "/nonexistent.png", Mime: "image/png",
		State: model.MediaLoading}

	u.loadFrames(md, 100, 100, 1, 0)
	select {
	case ev := <-u.events:
		t.Fatalf("unknown cell: decode launched (%+v)", ev)
	case <-time.After(50 * time.Millisecond):
	}
	if md.State != model.MediaLoading || md.Want != 0 || md.CellW != 0 {
		t.Fatalf("unknown cell: state %v, want %d, cell %dx%d", md.State, md.Want, md.CellW, md.CellH)
	}

	// The cell arrives (first resize): decoding starts.
	u.t.CellW, u.t.CellH = 9, 18
	u.loadFrames(md, 100, 100, 1, 0)
	if md.Want != 1 || md.CellW != 9 || md.CellH != 18 {
		t.Fatalf("known cell: want %d, cell %dx%d", md.Want, md.CellW, md.CellH)
	}
	select {
	case <-u.events:
	case <-time.After(2 * time.Second):
		t.Fatal("known cell: no evFrames")
	}
}

// TestResolveImagesWaitsForCell : the half-block fallback caused only by the
// unknown cell is flagged (cellWait); the main loop resumes the wanted mode
// on the first resize that carries the cell.
func TestResolveImagesWaitsForCell(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{},
		t: &term.Term{Cols: 80, Rows: 24, Kitty: true}}
	if got := u.resolveImages("auto"); got != "halfblock" || !u.cellWait {
		t.Fatalf("unknown cell: mode %q, cellWait %v", got, u.cellWait)
	}
	u.t.CellW, u.t.CellH = 9, 18
	if got := u.resolveImages("auto"); got != "kitty" || u.cellWait {
		t.Fatalf("known cell: mode %q, cellWait %v", got, u.cellWait)
	}
	// Terminal without kitty graphics: nothing to wait for, the fallback is permanent.
	u.t.Kitty, u.t.CellW, u.t.CellH = false, 0, 0
	if got := u.resolveImages("auto"); got != "halfblock" || u.cellWait {
		t.Fatalf("without kitty: mode %q, cellWait %v", got, u.cellWait)
	}
}

// TestVideoFrameCount : a download decodes one frame — the thumbnail — for a
// still image as well as for a video, whatever the video mode; only a video
// being played takes video_inline_frames, and a GIF its whole length.
func TestVideoFrameCount(t *testing.T) {
	u := &UI{cfg: &config.Config{VideoFrames: 42}}
	vid := &model.Media{Kind: model.MediaVideo}
	gif := &model.Media{Kind: model.MediaGIF}
	pic := &model.Media{Kind: model.MediaPhoto}
	for _, mode := range []string{"show", "hidden", "autoplay", ""} {
		u.cfg.Video = mode
		if got := u.frameCount(vid); got != 1 {
			t.Errorf("video=%q: %d frames", mode, got)
		}
	}
	playing := &model.Media{Kind: model.MediaVideo, Want: 42}
	if got := u.frameCount(playing); got != 42 {
		t.Errorf("video being played: %d frames, want video_inline_frames", got)
	}
	if got := u.frameCount(gif); got != 100 {
		t.Errorf("GIF at %d frames", got)
	}
	if got := u.frameCount(pic); got != 1 {
		t.Errorf("photo at %d frames", got)
	}
}

// TestAutoplayGatedOnDisplay : video = autoplay used to decode EVERY video
// that came in — window hidden, or message far above the view included — and
// held the whole clip in memory for nothing. The download now decodes the
// first frame only; the whole length is asked when the message shows.
func TestAutoplayGatedOnDisplay(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{Video: "autoplay", VideoFrames: 8},
		events: make(chan model.Event, 8), images: "halfblock", ctx: context.Background(),
		t: &term.Term{Cols: 80, Rows: 24}}
	md := &model.Media{Kind: model.MediaVideo, Mime: "video/mp4", Loc: "loc", State: model.MediaLoading}

	u.downloaded(model.EvDownloaded{Media: md, Path: "/nonexistent.mp4"})

	if md.Want != 1 {
		t.Fatalf("window not shown: %d frames asked, want the first one only", md.Want)
	}

	// The line reaches the screen: the whole clip is asked for.
	md.State, md.Frames, md.Want = model.MediaReady, [][]byte{{0}}, 0
	u.autoplay(&Item{Msg: &model.Msg{ID: 1, Media: md}})
	if md.Want != 8 {
		t.Fatalf("line shown: %d frames asked, want video_inline_frames", md.Want)
	}
}

// TestDecodeCancelledOnStop : a decoding in flight ran to its end whatever
// happened — "s" pressed, window closed, media freed — and its ffmpeg kept
// going with it. Stopping now cancels it, and the result that comes back
// late lands on nothing.
func TestDecodeCancelledOnStop(t *testing.T) {
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{VideoFrames: 8},
		ctx: context.Background(), events: make(chan model.Event, 4), images: "halfblock",
		t: &term.Term{Cols: 80, Rows: 24}}
	md := &model.Media{Kind: model.MediaVideo, Path: "/nonexistent.mp4", Mime: "video/mp4",
		State: model.MediaLoading}

	u.loadFrames(md, 100, 100, 8, 0)
	u.stopVideo(md) // key "s": nobody waits for these frames any more

	if md.Want != 0 {
		t.Fatalf("stopped: %d frames still asked", md.Want)
	}
	select {
	case e := <-u.events: // the goroutine gave up and reported
		u.framesLoaded(e.(evFrames))
	case <-time.After(2 * time.Second):
		t.Fatal("the decoding goroutine did not end")
	}
	if md.State != model.MediaLoading || len(md.Frames) != 0 {
		t.Fatalf("a cancelled decoding landed: state %v, %d frames", md.State, len(md.Frames))
	}
}

// TestDecodedFramesLand : the plain path — a decoding that goes to its end
// hands its frames to the media. It guards the cancellation from cutting the
// branch it sits on: the context of a decoding must stay alive until the UI
// loop has taken the result.
func TestDecodedFramesLand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "img.png")
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{},
		events: make(chan model.Event, 4), images: "halfblock", ctx: context.Background(),
		t: &term.Term{Cols: 80, Rows: 24}}
	md := &model.Media{Kind: model.MediaPhoto, Path: path, Mime: "image/png", State: model.MediaLoading}

	u.loadFrames(md, 100, 100, 1, 0)
	select {
	case e := <-u.events:
		u.framesLoaded(e.(evFrames))
	case <-time.After(2 * time.Second):
		t.Fatal("no evFrames")
	}

	if md.State != model.MediaReady || len(md.Frames) != 1 {
		t.Fatalf("state %v, %d frames", md.State, len(md.Frames))
	}
	if len(u.decodes) != 0 {
		t.Fatalf("%d decoding(s) still tracked once over", len(u.decodes))
	}
}

// TestStopThenPlayRedecodes : "s" during a decoding cancels it and used to
// leave the last partial batch of frames in place. render.Playing stayed true
// (more than one frame), so "l" only flipped Paused and looped a truncated
// clip for ever, where the decoding used to run to its end. Stopping now keeps
// one frame at most, and "l" decodes again.
func TestStopThenPlayRedecodes(t *testing.T) {
	if !media.HaveFFmpeg {
		t.Skip("ffmpeg absent")
	}
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{VideoFrames: 8},
		ctx: context.Background(), events: make(chan model.Event, 8), images: "halfblock",
		t: &term.Term{Cols: 80, Rows: 24}}
	md := &model.Media{Kind: model.MediaVideo, Path: "/nonexistent.mp4", Mime: "video/mp4",
		State: model.MediaReady, Frames: [][]byte{{1}, {2}, {3}}} // partial batch already landed

	u.loadFrames(md, 100, 100, 8, 0) // decoding running
	u.stopVideo(md)                  // key "s"

	if len(md.Frames) != 1 {
		t.Fatalf("stopped: %d frames kept, want the first one only", len(md.Frames))
	}
	u.playPause(md, 100, 100, 0) // key "l"
	if md.Want != 8 {
		t.Fatalf("play again: %d frames asked, want video_inline_frames", md.Want)
	}
}

// The avatar file lives under its network: ids collide between networks, and
// "file already there" would otherwise hand one network the photo of another.
func TestAvatarPathPerNet(t *testing.T) {
	if got := avatarPath("/d", model.ChatKey{Net: "discord", ID: 7}); got != filepath.Join("/d", "avatars", "discord", "7.jpg") {
		t.Fatalf("avatar path: %q", got)
	}
}

// framesUI : a halfblock terminal, one window holding n messages whose media
// are decoding; the frames land through framesLoaded.
func framesUI(n int) (*UI, []*model.Media) {
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{}, ctx: context.Background(),
		images: "halfblock", t: &term.Term{Cols: 80, Rows: 24}}
	w := u.ws.New(false)
	mds := make([]*model.Media, n)
	for i := range mds {
		mds[i] = &model.Media{Kind: model.MediaPhoto, Path: "/x.png", Mime: "image/png", State: model.MediaLoading}
		w.Upsert(&model.Msg{ID: i + 1, Media: mds[i]})
	}
	return u, mds
}

func land(u *UI, md *model.Media) {
	u.framesLoaded(evFrames{Media: md, Frames: &media.Frames{PNG: [][]byte{{1, 2, 3}}, W: 1, H: 1}})
}

// TestFramesBudgetEvictsOldest : the decoded frames kept in memory stay under
// framesBudget — the media not shown for the longest time loses its frames
// (and goes back to MediaNone: shown again, it is decoded again from its
// file), the recent ones keep theirs.
func TestFramesBudgetEvictsOldest(t *testing.T) {
	defer func(n int) { framesBudget = n }(framesBudget)
	framesBudget = 7 // two media of 3 bytes fit, three do not
	u, mds := framesUI(3)
	for _, md := range mds {
		land(u, md)
	}
	if mds[0].Frames != nil || mds[0].State != model.MediaNone {
		t.Fatalf("oldest media kept over the budget: %d frames, state %v", len(mds[0].Frames), mds[0].State)
	}
	if len(mds[1].Frames) != 1 || len(mds[2].Frames) != 1 {
		t.Fatalf("recent media lost their frames under the budget: %d %d", len(mds[1].Frames), len(mds[2].Frames))
	}
}

// TestFramesBudgetKeepsScreen : what is on the screen is never evicted, nor
// the frames that just landed — otherwise a screen over the budget would
// decode and drop the same media in a loop.
func TestFramesBudgetKeepsScreen(t *testing.T) {
	defer func(n int) { framesBudget = n }(framesBudget)
	framesBudget = 7
	u, mds := framesUI(3)
	u.hits = []rowHit{{item: u.ws.Current().Items[0]}} // the oldest one shows
	for _, md := range mds {
		land(u, md)
	}
	if len(mds[0].Frames) != 1 || len(mds[2].Frames) != 1 {
		t.Fatalf("shown media or newcomer evicted: %d %d", len(mds[0].Frames), len(mds[2].Frames))
	}
	if mds[1].Frames != nil {
		t.Fatal("the media off the screen kept its frames over the budget")
	}
	// Everything on the screen and over the budget: nothing is dropped.
	u.hits = append(u.hits, rowHit{item: u.ws.Current().Items[2]})
	mds[1].State = model.MediaLoading // shown again: download() started it again
	land(u, mds[1])
	for i, md := range mds {
		if len(md.Frames) != 1 {
			t.Fatalf("media %d evicted while on the screen", i)
		}
	}
}

// TestShownItemLoadsAgain : a media with nothing in memory — frames gone with
// the budget, or a cached message never asked — loads again as soon as its
// line shows: one download asked (the file on disk answers at once), not one
// per repaint. A separator line (nil item) is no item.
func TestShownItemLoadsAgain(t *testing.T) {
	b := &fakeBackend{caps: model.AllCaps()}
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{AutoMediaMaxKB: 1}, ctx: context.Background(),
		images: "halfblock", t: &term.Term{Cols: 80, Rows: 24}, nets: map[string]model.Backend{model.NetTelegram: b}}
	md := &model.Media{Kind: model.MediaPhoto, Loc: 1, Path: "/x.png", Mime: "image/png", Size: 100}
	it := &Item{Msg: &model.Msg{Net: model.NetTelegram, ID: 1, Media: md}}
	u.showItem(nil)
	u.showItem(it)
	u.showItem(it) // repaint
	if len(b.downloads) != 1 || md.State != model.MediaLoading {
		t.Fatalf("shown line: %d downloads, state %v", len(b.downloads), md.State)
	}
}

func TestMapIsNotPrefetchedOffscreen(t *testing.T) {
	u := &UI{cfg: &config.Config{Maps: true, AutoMediaMaxKB: 5120}, images: "kitty"}
	m := &model.Msg{Media: &model.Media{Kind: model.MediaMap}}
	u.autoMedia(m, false)
	if m.Media.State != model.MediaNone {
		t.Fatal("offscreen map started downloading")
	}
}

// TestAnimateHidesCursor : a kitty frame moves the cursor onto the image and
// back; hidden meanwhile, the terminal cannot draw it there between two chunks
// of the frame (an erratic blink), and it comes back once the frame is out.
func TestAnimateHidesCursor(t *testing.T) {
	var out bytes.Buffer
	u := &UI{ws: NewWindows(), agg: &Window{}, cfg: &config.Config{}, images: "kitty",
		t: term.NewOffscreen(&out, 80, 24)}
	md := &model.Media{State: model.MediaReady, Frames: [][]byte{{1}, {2}}, Delay: time.Millisecond}
	u.placed = []placed{{row: 2, pid: 3, img: &render.Img{Media: md, Cols: 4, Rows: 2}}}
	u.animate(time.Now().Add(time.Second))
	s := out.String()
	hide, show := strings.Index(s, "\x1b[?25l"), strings.LastIndex(s, "\x1b[?25h")
	if hide < 0 || show < 0 || hide > strings.Index(s, "\x1b_G") || show < strings.LastIndex(s, "\x1b[u") {
		t.Fatalf("cursor not hidden around the frame: %q", s)
	}
}
