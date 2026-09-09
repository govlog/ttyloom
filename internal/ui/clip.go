package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/term"
)

// maxUpload : Telegram cap for one upload.
const maxUpload = 2 << 30

// Bounds when reading the clipboard: the content comes from another program,
// so it is read under a time limit and under a size cap.
const (
	maxClip      = 64 << 20
	clipListWait = 5 * time.Second
	clipReadWait = 30 * time.Second
)

// evClipImage / evClipText : result of the clipboard read (goroutine → UI
// loop). Err carries the status to show.
type evClipImage struct {
	Path string
	Size int64
	Err  string
}

type evClipText struct{ Text string }

// clipBin : clipboard tool resolved once at start, Wayland first ("": neither
// of the two is installed).
var clipBin, clipWayland = func() (string, bool) {
	if p, err := exec.LookPath("wl-paste"); err == nil {
		return p, true
	}
	if p, err := exec.LookPath("xclip"); err == nil {
		return p, false
	}
	return "", false
}()

// clipArgs gives the read arguments of the clipboard for the type t; an empty
// t = the list of the types offered. Fixed arguments, never a shell.
func clipArgs(wayland bool, t string) []string {
	if wayland {
		if t == "" {
			return []string{"--list-types"}
		}
		return []string{"--type", t}
	}
	if t == "" {
		return []string{"-selection", "clipboard", "-t", "TARGETS", "-o"}
	}
	return []string{"-selection", "clipboard", "-t", t, "-o"}
}

// clipPick gives the first type of want really offered (one per line), "" else.
// The type is taken as it is: xclip like wl-paste wants the announced string.
func clipPick(list string, want ...string) string {
	var types []string
	for _, l := range strings.Split(list, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			types = append(types, t)
		}
	}
	for _, w := range want {
		if slices.Contains(types, w) {
			return w
		}
	}
	return ""
}

// pasteImageName gives the timestamped name of the pasted file, with the
// extension of the type read in the bytes. "": this is not an image. No name
// ever comes from the clipboard.
func pasteImageName(t time.Time, mime string) string {
	var ext string
	switch mime {
	case "image/png":
		ext = ".png"
	case "image/jpeg":
		ext = ".jpg"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	default:
		return ""
	}
	return t.Format("20060102-150405") + ext
}

// pasteClip : Ctrl+V — reads the clipboard out of band (the call would block
// the UI loop) and opens the send prompt when the image comes.
func (u *UI) pasteClip() {
	w := u.sendWin()
	if w == nil {
		return
	}
	if w.Chat == nil {
		w.AddSys(i18n.T("window_not_bound"))
		return
	}
	if clipBin == "" {
		u.sys(i18n.T("clip_no_tool"))
		return
	}
	dir := filepath.Join(config.Expand(u.cfg.DownloadDir), "paste")
	bin, wl, ctx := clipBin, clipWayland, u.ctx
	go func() {
		defer func() {
			if r := recover(); r != nil {
				u.events <- evClipImage{Err: i18n.T("clip_panic", r)}
			}
		}()
		u.events <- readClip(ctx, bin, wl, dir)
	}()
}

// clipRead gives the output of a clipboard command, bounded in time and in
// size. The pipe is read straight: nothing bigger than maxClip goes into
// memory, and the producer is killed rather than waited for.
func clipRead(ctx context.Context, wait time.Duration, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, rerr := io.ReadAll(io.LimitReader(out, maxClip+1))
	if len(data) > maxClip {
		cancel() // above the cap: the producer is killed, not waited for
		cmd.Wait()
		return nil, errors.New(i18n.T("clip_image_too_big"))
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	return data, rerr
}

// readClip : the real read, outside the UI goroutine. It gives evClipImage
// (image written to disk, or an error status) or evClipText.
func readClip(ctx context.Context, bin string, wl bool, dir string) model.Event {
	fail := func(err error) model.Event { return evClipImage{Err: i18n.T("clip_error", err)} }
	list, err := clipRead(ctx, clipListWait, bin, clipArgs(wl, "")...)
	if err != nil {
		return fail(err)
	}
	if t := clipPick(string(list), "image/png", "image/jpeg"); t != "" {
		data, err := clipRead(ctx, clipReadWait, bin, clipArgs(wl, t)...)
		if err != nil {
			return fail(err)
		}
		if len(data) == 0 {
			return evClipImage{Err: i18n.T("clip_empty")}
		}
		// The type announced binds only the source application: the extension
		// follows the bytes received.
		name := pasteImageName(time.Now(), http.DetectContentType(data))
		if name == "" {
			return evClipImage{Err: i18n.T("clip_no_image")}
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fail(err)
		}
		ext := filepath.Ext(name)
		f, err := os.CreateTemp(dir, strings.TrimSuffix(name, ext)+"-*"+ext)
		if err != nil {
			return fail(err)
		}
		path := f.Name()
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if err := cmp.Or(writeErr, closeErr); err != nil {
			os.Remove(path)
			return fail(err)
		}
		return evClipImage{Path: path, Size: int64(len(data))}
	}
	if t := clipPick(string(list), "text/plain;charset=utf-8", "text/plain", "UTF8_STRING", "STRING"); t != "" {
		args := clipArgs(wl, t)
		if wl {
			args = append(args, "-n") // otherwise wl-paste adds a trailing line break
		}
		text, err := clipRead(ctx, clipReadWait, bin, args...)
		if err != nil {
			return fail(err)
		}
		// TrimRight for safety: xclip has no equivalent of -n, and a single line
		// paste must not open the "2 lines" prompt.
		if s := strings.TrimRight(string(text), "\n"); s != "" {
			return evClipText{Text: s}
		}
		return evClipImage{Err: i18n.T("clip_empty")}
	}
	return evClipImage{Err: i18n.T("clip_no_image")}
}

// sendAsk : image waiting for a confirmation (e/l/a prompt).
type sendAsk struct {
	path    string
	size    int64
	chat    *model.Chat
	title   string // shown name of the chat (local name included) at paste time
	tmp     bool   // file written by the paste: dropped once sent or cancelled
	caption bool   // the input line holds the caption
	draft   string // input in progress at the time of the "l", given back on cancel
}

// prompt : prompt of the input line.
func (a *sendAsk) prompt() string {
	if a.caption {
		return i18n.T("caption_prompt")
	}
	return i18n.T("send_ask", render.HumanSize(a.size), render.CleanLine(a.title))
}

// clipImage : pasted image ready, the prompt opens on the current window (it
// may have changed during the read).
func (u *UI) clipImage(e evClipImage) {
	if e.Err != "" {
		u.sys(e.Err)
		return
	}
	w := u.sendWin()
	if w == nil {
		os.Remove(e.Path)
		return
	}
	if w.Chat == nil {
		os.Remove(e.Path)
		w.AddSys(i18n.T("window_not_bound"))
		return
	}
	u.cancelSend() // an image waiting is replaced by the new one
	u.sendAsk = &sendAsk{path: e.Path, size: e.Size, chat: w.Chat, title: u.title(w.Chat), tmp: true}
}

// sendKey handles the keys while u.sendAsk waits for a decision, like
// pasteKey. While a caption is typed it hands back the lead: the line edits
// as usual, Enter goes through submit() and Esc through cancelMode().
func (u *UI) sendKey(k term.Key) bool {
	a := u.sendAsk
	if a.caption {
		return false
	}
	switch {
	case k.Code == term.None && k.Rune == 'e':
		u.sendAsk = nil
		u.sendPath(a.chat, a.path, "", a.tmp)
	case k.Code == term.None && k.Rune == 'l':
		a.caption, a.draft = true, u.ed.String()
		u.ed.Set("")
	case k.Code == term.Esc, k.Code == term.None && k.Rune == 'a':
		u.cancelSend()
	}
	return true
}

// cancelSend drops the send prompt; the pasted file goes with it.
func (u *UI) cancelSend() {
	a := u.sendAsk
	if a == nil {
		return
	}
	u.sendAsk = nil
	if a.caption {
		u.ed.Set(a.draft) // the input broken off by the "l" comes back
	}
	if a.tmp {
		os.Remove(a.path)
	}
}

// sendFile sends a file chosen by the user, never deleted.
func (u *UI) sendFile(chat *model.Chat, path, caption string) {
	u.sendPath(chat, path, caption, false)
}

// sendPath sends a local file: a photo for a png or a jpeg, a document
// otherwise (a gif, a webp or a video thus keep their nature). tmp = file
// written by a paste, dropped once the send worked.
//
// The line goes into the window at once, like a text send: waiting for the
// upload, the RPC and the echo of the server left the user with nothing on
// the screen for the whole transfer. The placeholder media is replaced by the
// real one when the echo comes.
func (u *UI) sendPath(chat *model.Chat, path, caption string, tmp bool) {
	b := u.net(chat)
	if b == nil {
		return // same guard as sendWith: nothing pending with nobody to send it
	}
	photo := false
	switch media.Sniff(path) {
	case "image/png", "image/jpeg":
		photo = true
	}
	w := u.winFor(chat) // the receipt lands there, never in a search window
	u.tmpID++
	me := u.selfOf(chat.Net)
	m := &model.Msg{Net: chat.Net, ChatID: chat.ID, ChatLabel: chat.Title, Date: time.Now(),
		From: me.Name, FromID: me.ID, Out: true, Text: caption, Pending: true, TmpID: u.tmpID,
		Media: placeholderMedia(path, photo)}
	u.insertPending(w, m)
	if photo {
		b.SendPhoto(u.ctx, chat, path, caption, tmp, u.tmpID)
	} else {
		b.SendFile(u.ctx, chat, path, caption, tmp, u.tmpID)
	}
	if w == u.view() {
		u.flash(i18n.T("sending"))
		return
	}
	// Send from the aggregate or from a /search result: the line lands in the
	// window of the chat, which is not the one on the screen. Say where.
	u.flash(i18n.T("sent_to_window", slices.Index(u.ws.List, w), u.winName(w)))
}

// placeholderMedia : media of a send while it goes up — the label of the file
// with no network handle, replaced by the real media at the echo. The state
// is Loading so that the line reads as being on its way.
func placeholderMedia(path string, photo bool) *model.Media {
	size := int64(0)
	if st, err := os.Stat(path); err == nil {
		size = st.Size()
	}
	name := filepath.Base(path)
	md := &model.Media{Kind: model.MediaFile, State: model.MediaLoading,
		Label: i18n.T("media_file", name, render.HumanSize(size))}
	if photo {
		md.Kind, md.Label = model.MediaPhoto, fmt.Sprintf("[photo %s · %s]", name, render.HumanSize(size))
	}
	return md
}

// splitSendArgs handles /send <path> [caption]. The path can hold spaces: it
// grows word by word until an existing file is found, and the rest is the
// caption. No file found: everything is the path, and the error names it
// whole.
func splitSendArgs(args string, exists func(string) bool) (path, caption string) {
	args = strings.TrimSpace(args)
	for i, r := range args {
		if r == ' ' && exists(args[:i]) {
			return args[:i], strings.TrimSpace(args[i:])
		}
	}
	return args, ""
}

// sendCmd : /send <path> [caption].
func (u *UI) sendCmd(w *Window, args string) {
	if w == nil {
		return
	}
	if w.Chat == nil {
		w.AddSys(i18n.T("window_not_bound"))
		return
	}
	path, caption := splitSendArgs(args, func(p string) bool {
		st, err := os.Stat(config.Expand(p))
		return err == nil && st.Mode().IsRegular()
	})
	path = config.Expand(path)
	st, err := os.Stat(path)
	if err != nil {
		w.AddSys(i18n.T("send_error", err))
		return
	}
	if !st.Mode().IsRegular() {
		w.AddSys(i18n.T("send_not_a_file", path))
		return
	}
	if st.Size() > maxUpload {
		w.AddSys(i18n.T("send_too_big"))
		return
	}
	u.sendFile(w.Chat, path, caption)
}

// --- copy of the image shown by the viewer ("c") ---

// copyBin : clipboard writer resolved once at start, Wayland first ("":
// neither wl-copy nor xclip is installed).
var copyBin, copyWayland = func() (string, bool) {
	if p, err := exec.LookPath("wl-copy"); err == nil {
		return p, true
	}
	if p, err := exec.LookPath("xclip"); err == nil {
		return p, false
	}
	return "", false
}()

// copyArgs gives the write arguments for the type mime of the file path;
// wl-copy reads its stdin (the file is given to it by copyImage). Fixed
// arguments, never a shell.
func copyArgs(wayland bool, mime, path string) []string {
	if wayland {
		return []string{"--type", mime}
	}
	return []string{"-selection", "clipboard", "-t", mime, "-i", path}
}

// copyMime : the image type read from the bytes of the file, "" when it is
// no image (video, document) or cannot be read.
func copyMime(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	mime := http.DetectContentType(head[:n])
	if !strings.HasPrefix(mime, "image/") {
		return ""
	}
	return mime
}

// evFlash : a word for the status bar, posted by a goroutine.
type evFlash struct{ Text string }

// copyImage puts the file of md into the clipboard as an image; the result
// shows in the status bar.
func (u *UI) copyImage(md *model.Media) {
	if copyBin == "" {
		u.flash(i18n.T("clip_no_copy_tool"))
		return
	}
	mime := copyMime(md.Path)
	if mime == "" {
		u.flash(i18n.T("clip_not_image"))
		return
	}
	bin, wl, path, ctx := copyBin, copyWayland, md.Path, u.ctx
	go func() {
		ctx, cancel := context.WithTimeout(ctx, clipReadWait)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, copyArgs(wl, mime, path)...)
		if wl {
			f, err := os.Open(path)
			if err != nil {
				u.events <- evFlash{Text: i18n.T("clip_error", err)}
				return
			}
			defer f.Close()
			cmd.Stdin = f
		}
		// wl-copy and xclip fork to serve the clipboard, the parent returns at once.
		if err := cmd.Run(); err != nil {
			u.events <- evFlash{Text: i18n.T("clip_error", err)}
			return
		}
		u.events <- evFlash{Text: i18n.T("clip_copied")}
	}()
}
