package ui

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/theme"
)

func TestPlacementDiff(t *testing.T) {
	prev := map[kplace]bool{{id: 1, pid: 3}: true, {id: 2, pid: 7}: true}
	cur := map[kplace]bool{{id: 1, pid: 3}: true, {id: 2, pid: 9}: true}
	got := placeDiff(prev, cur)
	if len(got) != 1 || got[0] != (kplace{id: 2, pid: 7}) {
		t.Fatalf("%+v", got)
	}
	if len(placeDiff(cur, cur)) != 0 {
		t.Fatal("identical frame: nothing to remove")
	}
	if got := placeDiff(prev, nil); len(got) != 2 { // frame with no image: everything goes
		t.Fatalf("%+v", got)
	}
	// Two avatars on the same line (sidebar + message) keep separate pids, and
	// neither falls back on the one of a message image.
	if a, b := avatarPID(4, 0), avatarPID(4, 12); a == b || a <= 1<<24 || b <= 1<<24 {
		t.Fatalf("avatar pids: %d %d", a, b)
	}
}

// TestFrameKittySequence : a frame places first, drops after. The image that
// moved is placed again at its new pid before the old one goes, and the id
// made stale by a new decoding is freed only after the new one is placed.
func TestFrameKittySequence(t *testing.T) {
	md := &model.Media{}
	u := &UI{kittyLRU: lru{cap: 4}, kplaced: map[kplace]bool{{id: 9, pid: 11}: true}, kittyOld: []uint32{9}}
	var b strings.Builder
	cur := map[kplace]bool{}
	u.placeKitty(&b, cur, 6, md, []byte{1}, 3, 2) // first placement: send
	u.endFrameKitty(&b, cur)
	got := b.String()
	place := strings.Index(got, "a=T,f=100,i=1,p=6,")
	del := strings.Index(got, "a=d,d=i,i=9,p=11,")
	free := strings.Index(got, "a=d,d=I,i=9,")
	if place < 0 || del < 0 || free < 0 || place > del || del > free {
		t.Fatalf("place/delete/free order: %q", got)
	}
	if len(u.kittyOld) != 0 || !u.kplaced[kplace{id: 1, pid: 6}] || len(u.kplaced) != 1 {
		t.Fatalf("end-of-frame state: %v %v", u.kittyOld, u.kplaced)
	}
}

// TestFramesLoadedRedraw : when the frames come, the item that carries them is
// invalidated in every view — not only in the one shown — and the old kitty
// image goes at the deferred free. The repaint itself has no flag: the loop of
// Run() draws after each event.
func TestFramesLoadedRedraw(t *testing.T) {
	// MediaLoading: the state set by download() while the decoding runs
	// (MediaNone would mean a media freed meanwhile, a result to drop).
	md := &model.Media{Kind: model.MediaPhoto, Label: "[photo]", State: model.MediaLoading,
		KittyID: 5, KittyAlt: 6}
	m := &model.Msg{ID: 1, ChatID: 7, Media: md}
	u := &UI{ws: NewWindows(), agg: &Window{}, images: "kitty"}
	chat := u.ws.New(true)   // window of the chat: hidden, the current view is window 0
	search := u.ws.New(true) // result of /search: the same *Msg shared
	search.Search = "x"
	for _, w := range []*Window{chat, u.agg, search} {
		w.Upsert(m)
		w.Items[len(w.Items)-1].lines = []render.Line{{}} // cached drawing
	}
	u.ws.Cur = 0

	u.framesLoaded(evFrames{Media: md, Frames: &media.Frames{PNG: [][]byte{{1}}, W: 8, H: 4}})

	if md.State != model.MediaReady || len(md.Frames) != 1 || md.FrameW != 8 {
		t.Fatalf("media: %+v", md)
	}
	for i, w := range []*Window{chat, u.agg, search} {
		if w.Items[0].lines != nil {
			t.Fatalf("view %d: drawing not invalidated", i)
		}
	}
	if md.KittyID != 0 || md.KittyAlt != 0 {
		t.Fatalf("kitty ids kept: %d %d", md.KittyID, md.KittyAlt)
	}
	if len(u.kittyOld) != 2 || u.kittyOld[0] != 5 || u.kittyOld[1] != 6 {
		t.Fatalf("deferred free: %v", u.kittyOld)
	}
}

// TestClearNoEraseDisplay : no screen clear at all while running. ED (CSI 2J)
// destroys the DATA of the kitty images — Ghostty: eraseDisplay(.complete) →
// kitty_images.clearScreen → deleteVisiblePlacements + deleteIfUnused — while
// the ids stay on the media and in u.kplaced: the a=p that follows would fail
// in silence (q=2) and the images would go for good. draw() covers each cell
// without it.
func TestClearNoEraseDisplay(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	ed := []string{"\\x1b[" + "2J", "\x1b[" + "2J"} // written as a Go literal or as a raw byte
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ed {
			if strings.Contains(string(b), e) {
				t.Fatalf("%s clears the screen: kitty images lose their data there", f)
			}
		}
	}
}

// TestHoverImageBox : the hover image on top never covers a line of the block
// of the hovered message (help line and reactions included) when another
// position is possible — at the right of the label, else under the block, else
// above it, else cropped at the right as a last resort.
func TestHoverImageBox(t *testing.T) {
	// At the right: 10 (end of the label) + 1 + 8 ≤ 40, nothing of the block
	// under the label (msgBottom == labelRow).
	if col, row, ok := hoverImageBox(3, 3, 3, 10, 8, 4, 40, 20); !ok || col != 11 || row != 3 {
		t.Fatalf("on the right: %d %d %v", col, row, ok)
	}
	// At the right even though the block has a help line under the label
	// (msgBottom = labelRow+1): the image (4 lines) fits without going past it.
	if col, row, ok := hoverImageBox(2, 4, 3, 10, 8, 1, 40, 20); !ok || col != 11 || row != 3 {
		t.Fatalf("on the right, low image: %d %d %v", col, row, ok)
	}
	// Too wide at the right: under the WHOLE block (msgBottom+1), not under the
	// label alone — this is the bug fixed: the help line (msgBottom=5) stays
	// shown, not just labelRow+1.
	if col, row, ok := hoverImageBox(3, 5, 3, 35, 8, 4, 40, 20); !ok || col != 0 || row != 6 {
		t.Fatalf("below the block: %d %d %v", col, row, ok)
	}
	// Does not fit under (bottom of the area): above the whole block.
	if col, row, ok := hoverImageBox(10, 17, 15, 35, 8, 6, 40, 20); !ok || col != 0 || row != 4 {
		t.Fatalf("above: %d %d %v", col, row, ok)
	}
	// Too large to fit under or above: cropped at the right, as a last resort
	// only (it can cover the message).
	if col, row, ok := hoverImageBox(5, 15, 8, 35, 30, 18, 40, 20); !ok || col != 10 || row != 2 {
		t.Fatalf("crop: %d %d %v", col, row, ok)
	}
	// Larger than the whole area: cropped, never negative.
	if col, row, ok := hoverImageBox(0, 5, 2, 2, 60, 30, 40, 20); !ok || col != 0 || row != 0 {
		t.Fatalf("outside area: %d %d %v", col, row, ok)
	}
	if _, _, ok := hoverImageBox(0, 0, 0, 0, 0, 0, 40, 20); ok {
		t.Fatal("empty image: nothing to draw")
	}
}

// TestHoverImageBoxNeverCoversBlock : property swept over several dozen
// combinations — when the position chosen is not the last resort (crop at the
// right), it never crosses a line of the block [msgTop, msgBottom], but the
// label itself at the right of labelEndCol.
func TestHoverImageBoxNeverCoversBlock(t *testing.T) {
	n := 0
	for _, msgTop := range []int{0, 2, 5} {
		for _, blockLen := range []int{0, 1, 3} { // lines besides labelRow (help, reactions)
			for _, labelOff := range []int{0, blockLen} { // label at the head or at the tail of the block
				labelRow := msgTop + labelOff
				msgBottom := msgTop + blockLen
				for _, labelEndCol := range []int{0, 10, 25} {
					for _, imgCols := range []int{1, 8, 20} {
						for _, imgRows := range []int{1, 3, 10} {
							for _, areaCols := range []int{15, 40} {
								for _, areaRows := range []int{10, 20} {
									n++
									col, row, ok := hoverImageBox(msgTop, msgBottom, labelRow, labelEndCol, imgCols, imgRows, areaCols, areaRows)
									if !ok {
										continue
									}
									// Last resort (crop): neither at the right without covering another
									// line of the block (once moved up into the area), nor above or under
									// the whole block.
									rightRow := max(0, min(labelRow, areaRows-imgRows))
									rightSafe := true
									for i := rightRow; i < rightRow+imgRows; i++ {
										if i != labelRow && i >= msgTop && i <= msgBottom {
											rightSafe = false
										}
									}
									lastResort := !(labelEndCol+1+imgCols <= areaCols && rightSafe) &&
										msgBottom+1+imgRows > areaRows && msgTop-imgRows < 0
									if lastResort {
										continue // covering taken as a last resort
									}
									for r := row; r < row+imgRows; r++ {
										if r < msgTop || r > msgBottom {
											continue // outside the block: no clash possible
										}
										if r == labelRow && col > labelEndCol {
											continue // at the right of the label, on its own line: allowed
										}
										t.Fatalf("covers the block: msgTop=%d msgBottom=%d labelRow=%d labelEndCol=%d imgCols=%d imgRows=%d area=%dx%d -> col=%d row=%d (line %d)",
											msgTop, msgBottom, labelRow, labelEndCol, imgCols, imgRows, areaCols, areaRows, col, row, r)
									}
								}
							}
						}
					}
				}
			}
		}
	}
	if n < 30 {
		t.Fatalf("sweep too short: %d combinations", n)
	}
}

// TestGifSurvivesResize : the window grows, the view stays anchored at the
// bottom. Before, a tall image block met by the top of the view moved the
// whole slice up without moving the bottom with it: the end of the thread
// fell past the screen and the last image block, cut in turn, was never
// placed — its GIF looked gone (label alone, blank lines) while the images
// higher up kept showing.
func TestGifSurvivesResize(t *testing.T) {
	block := func(md *model.Media, rows int) []render.Line {
		out := make([]render.Line, rows)
		for k := range out {
			out[k].Img = &render.Img{Media: md, Cols: 40, Rows: rows, Row: k}
		}
		return out
	}
	photo := &model.Media{Kind: model.MediaPhoto}
	gif := &model.Media{Kind: model.MediaGIF}
	lines := block(photo, 14) // image at the head of the drawing
	for i := 0; i < 27; i++ { // messages
		lines = append(lines, render.Line{})
	}
	lines = append(lines, render.Line{})     // label of the gif
	lines = append(lines, block(gif, 12)...) // its image block
	lines = append(lines, render.Line{})     // message after the gif

	for _, view := range []int{26, 53} { // window as it was, then enlarged
		start, end := viewSlice(lines, 0, view)
		if start+view < end {
			t.Fatalf("height %d: %d bottom lines off screen (start %d, end %d)",
				view, end-start-view, start, end)
		}
		placed := false
		for r := 0; r < view && start+r < end; r++ {
			img := lines[start+r].Img
			if img != nil && img.Media == gif {
				if _, rows, _, ok := imgPlace(img, r, view); ok && rows == img.Rows {
					placed = true // same rule as draw(), and the gif shows whole
				}
			}
		}
		if !placed {
			t.Fatalf("height %d: gif image block not placed (start %d, end %d)", view, start, end)
		}
	}
}

// The [network] segment of the status bar follows the current chat, and
// only exists from two backends on.
func TestStatusNetSegment(t *testing.T) {
	for _, c := range []struct {
		nets []string
		want bool
	}{
		{[]string{model.NetTelegram}, false},
		{[]string{model.NetTelegram, "discord"}, true},
	} {
		u := netUI(c.nets...)
		u.th = theme.Terminal()
		u.ws.New(false).Chat = u.chatList[0] // window 1, bound to the telegram chat
		var b strings.Builder
		u.drawStatus(&b, 1, 0, 80)
		if got := strings.Contains(b.String(), " ["+model.NetTelegram+"]"); got != c.want {
			t.Fatalf("%d network(s): segment %v, want %v — %q", len(c.nets), got, c.want, b.String())
		}
	}
}

// A block cut by the top of the view is no longer taken back whole: the
// scroll moves line by line and the image shows its lower part.
func TestViewSliceCutsBlock(t *testing.T) {
	var lines []render.Line
	for i := 0; i < 10; i++ {
		lines = append(lines, render.Line{})
	}
	md := &model.Media{}
	for k := 0; k < 5; k++ {
		lines = append(lines, render.Line{Img: &render.Img{Media: md, Cols: 20, Rows: 5, Row: k}})
	}
	for i := 0; i < 10; i++ {
		lines = append(lines, render.Line{})
	}
	start, end := viewSlice(lines, 3, 10) // end 22, start 12: line 12 is row 2 of the block
	if start != 12 || end != 22 {
		t.Fatalf("slice: %d..%d", start, end)
	}
}

// imgPlace : the placement of a block from the line of the view that
// carries it — its first line, or the first line of the view when the top
// cuts it — with the rows hidden above and below taken off; cropOf gives
// the source pixels that stay.
func TestImgPlace(t *testing.T) {
	img := func(row int) *render.Img { return &render.Img{Cols: 20, Rows: 5, Row: row} }
	for _, c := range []struct {
		row, r, view      int
		wRow, wRows, wTop int
		ok                bool
	}{
		{0, 3, 10, 3, 5, 0, true},  // whole
		{2, 0, 10, 0, 3, 2, true},  // cut by the top: 2 rows hidden
		{0, 8, 10, 8, 2, 0, true},  // cut by the bottom
		{2, 4, 10, 0, 0, 0, false}, // middle line of a block placed by its first line
	} {
		row, rows, top, ok := imgPlace(img(c.row), c.r, c.view)
		if row != c.wRow || rows != c.wRows || top != c.wTop || ok != c.ok {
			t.Fatalf("Row %d at r %d: got %d %d %d %v", c.row, c.r, row, rows, top, ok)
		}
	}
	if r := cropOf(0, 5, 5, 100, 50); !r.Empty() {
		t.Fatalf("whole block: crop %v", r)
	}
	if r := cropOf(2, 3, 5, 100, 50); r != image.Rect(0, 20, 100, 50) {
		t.Fatalf("lower 3 of 5 rows: crop %v", r)
	}
}
