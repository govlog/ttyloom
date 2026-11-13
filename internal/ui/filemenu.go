package ui

import (
	"path/filepath"
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
)

// Menu of a file: a click on the label of a media (render.KeyFile), or Enter
// on a file of the file browser (Ctrl+D). Its entries act on the file of the
// media, downloaded first when it is not on the disk yet (withFile).

// fileEntries : view when the media has a preview, copy the image when it
// is one, then copy the path, send it to a conversation, open with the
// desktop.
func fileEntries(md *model.Media) []menuEntry {
	var es []menuEntry
	if md.Previewable() {
		es = append(es, menuEntry{label: i18n.T("menu_file_view"), key: "view"})
	}
	if imageMedia(md) {
		es = append(es, menuEntry{label: i18n.T("menu_file_copy_image"), key: "copyimg"})
	}
	return append(es,
		menuEntry{label: i18n.T("menu_file_copy_path"), key: "copypath"},
		menuEntry{label: i18n.T("menu_file_share"), key: "share"},
		menuEntry{label: i18n.T("menu_file_open"), key: "open"})
}

// imageMedia : the file of md is a picture the clipboard can take. The GIFs
// of Telegram and of the GIF providers are MP4 files.
func imageMedia(md *model.Media) bool {
	switch md.Kind {
	case model.MediaPhoto:
		return true
	case model.MediaGIF:
		return !strings.HasPrefix(md.Mime, "video/")
	case model.MediaSticker:
		return !md.Animated
	}
	return strings.HasPrefix(md.Mime, "image/")
}

// openFileMenu : the menu of the file of the media of it, anchored at (x, y).
func (u *UI) openFileMenu(it *Item, x, y int) {
	m := it.Msg
	if m == nil || m.Media == nil || !render.Actionable(m) {
		return
	}
	u.menu = &ctxMenu{file: it, win: u.view(), entries: fileEntries(m.Media), x: x, y: y}
}

// fileDo runs the entry key of the menu of a file.
func (u *UI) fileDo(c *ctxMenu, key string) {
	it, w := c.file, c.win
	switch key {
	case "view":
		u.viewMsg(it.Msg)
	case "copyimg":
		u.withFile(w, it.Msg, u.copyImage)
	case "copypath":
		u.withFile(w, it.Msg, func(md *model.Media) {
			p, err := filepath.Abs(md.Path)
			if err != nil {
				p = md.Path
			}
			u.copyText(p)
		})
	case "share":
		u.openShare(it.Msg)
	case "open":
		u.openItemMedia(w, it)
	case "jump":
		u.filesJump(it)
	}
}

// openShare : the "new chat" box, over every network, picks where the file
// of m goes (share).
func (u *UI) openShare(m *model.Msg) {
	u.openNewChat()
	if u.newChat != nil {
		u.newChat.share = m
	}
}

// share sends the file of m to the chat to. On the network of m, when it
// forwards and the media is the only one of m, the network forwards the
// message: the receiver sees where it comes from, nothing goes up again.
// Anywhere else — another network, an IRC nick (DCC), one attachment of
// several — the file downloads if it must, then goes up as a file of mine.
func (u *UI) share(m *model.Msg, to *model.Chat) {
	if _, ok := u.net(to).(model.Forwarder); ok && to.Net == m.Net && m.ID != 0 && len(m.More) == 0 &&
		u.capsOf(m).Forward && u.chatOf(m) != nil {
		u.forward(m, to)
		return
	}
	u.withFile(u.winFor(to), m, func(md *model.Media) { u.sendFile(to, md.Path, "") })
}
