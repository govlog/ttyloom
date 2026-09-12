package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
)

// Custom emojis of the picker: on kitty, the image of the emoji (Discord CDN,
// Chat.CustomLocs) takes its cell instead of the start of its name. Same road
// as the previews of the GIF box — download into the cache, decoding to the
// cell, placement of the frame, animate() for a ".gif" — but the media stay
// for the session: the picker opens again and again on the same room, and a
// frame of two cells weighs nothing. Half blocks: a cell of one line cannot
// hold an image, the name stays.

// customPID : kitty placements of the picker: customPID | rank of the item.
const customPID = 1 << 22

// customMedia gives the media of the custom emoji char in the room of the
// picker; nil when the room has no image for it.
func (u *UI) customMedia(char string) *model.Media {
	p := u.picker
	if p == nil || p.chat == nil {
		return nil
	}
	loc := p.chat.CustomLocs[char]
	if loc == nil {
		return nil
	}
	key := fmt.Sprint(loc)
	if md := u.customs[key]; md != nil {
		return md
	}
	md := &model.Media{Kind: model.MediaPhoto, Loc: loc, Ext: ".png", Mime: "image/png"}
	if strings.HasSuffix(key, ".gif") {
		md.Kind, md.Ext, md.Mime = model.MediaGIF, ".gif", "image/gif"
	}
	if u.customs == nil {
		u.customs = map[string]*model.Media{}
	}
	u.customs[key] = md
	return md
}

// customOwns tells whether md is the image of a custom emoji.
func (u *UI) customOwns(md *model.Media) bool {
	for _, x := range u.customs {
		if x == md {
			return true
		}
	}
	return false
}

// customVisible walks the custom emojis on the screen of the picker.
func (u *UI) customVisible(f func(i int, md *model.Media)) {
	p := u.picker
	lo := p.top() * p.cols
	for i := lo; i < min(len(p.items), lo+p.rows*p.cols); i++ {
		if p.items[i].Group != customGroup {
			continue
		}
		if md := u.customMedia(p.items[i].Char); md != nil {
			f(i, md)
		}
	}
}

// customLoad starts what the custom emojis on the screen still need: the
// download of an image never asked, the decoding again of one whose frames
// the budget dropped. Called at each drawing of the picker.
func (u *UI) customLoad() {
	p := u.picker
	if u.images != "kitty" || p.chat == nil {
		return
	}
	b := u.net(p.chat)
	if b == nil {
		return
	}
	u.customVisible(func(_ int, md *model.Media) {
		switch {
		case md.State == model.MediaNone:
			md.State = model.MediaLoading
			b.Download(u.ctx, md, filepath.Join(config.CacheDir(), "emoji", p.chat.Net, gifName(md)))
		case md.State == model.MediaReady && len(md.Frames) == 0 && md.Want == 0 && md.Path != "":
			u.customDecode(md)
		}
	})
}

// customDecode : the image just downloaded — decoded to the cell of the grid.
func (u *UI) customDecode(md *model.Media) {
	cw, ch, _, _ := u.cells()
	u.loadFrames(md, (cellW-1)*cw, ch, u.frameCount(md), 0)
}

// customShown : the image of char is decoded — its cell stays blank under it.
func (u *UI) customShown(char string) bool {
	if u.images != "kitty" {
		return false
	}
	md := u.customMedia(char)
	return md != nil && len(md.Frames) > 0
}

// customPlacements : the images on the screen as kitty placements of the
// frame, in the cell of their emoji. x0: first column of the message area
// (Col is relative to it).
func (u *UI) customPlacements(x0 int) []placed {
	p := u.picker
	if p == nil || u.images != "kitty" || p.chat == nil {
		return nil
	}
	r := u.pickerRect()
	cw, ch, _, _ := u.cells()
	var out []placed
	u.customVisible(func(i int, md *model.Media) {
		if len(md.Frames) == 0 {
			return
		}
		cols, rows := render.Box(md.FrameW, md.FrameH, cellW-1, 1, cw, ch)
		if cols < 1 || rows < 1 {
			return
		}
		row := r.row + 2 + i/p.cols - p.top()
		col := r.col + 1 + (i%p.cols)*cellW
		out = append(out, placed{row: row, pid: customPID | uint32(i), img: &render.Img{Media: md, Col: col - x0, Cols: cols, Rows: rows}})
	})
	return out
}
