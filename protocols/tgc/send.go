package tgc

import (
	"context"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/message/unpack"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
)

// sent runs a send in a goroutine of its own and posts its receipt: the id
// of the message, or the error — the UI unpends its line with it.
func (c *Client) sent(name string, chat *model.Chat, tmpID int64, send func() (tg.UpdatesClass, error)) {
	go func() {
		defer c.Guard(name, func(err string) { c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID, Err: err}) })
		id, err := unpack.MessageID(send())
		ev := model.EvSent{ChatID: chat.ID, TmpID: tmpID, ID: id}
		if err != nil {
			ev.Err = err.Error()
		}
		c.Post(ev)
	}()
}

func (c *Client) Send(ctx context.Context, chat *model.Chat, text string, tmpID int64) {
	c.sent("Send", chat, tmpID, func() (tg.UpdatesClass, error) { return c.sender.To(c.peer(chat)).Text(ctx, text) })
}

// SendStyled : like Send, with formatting on the segments (pre, italic…).
func (c *Client) SendStyled(ctx context.Context, chat *model.Chat, segs []model.Seg, tmpID int64) {
	c.sent("SendStyled", chat, tmpID, func() (tg.UpdatesClass, error) {
		return c.sender.To(c.peer(chat)).StyledText(ctx, c.stylingOf(segs)...)
	})
}

// SendPre : like Send, but text goes as a code block (multiline paste).
func (c *Client) SendPre(ctx context.Context, chat *model.Chat, text string, tmpID int64) {
	c.SendStyled(ctx, chat, []model.Seg{{Text: text, Kind: model.SegPre}}, tmpID)
}

// stylingOf : the options of segs, the mentions by id resolved from the peers
// seen in the session — the member box keeps its users there, access hash
// included. Never a network call (the rule of peerOf): an unknown user, or a
// min one whose hash is worth nothing, leaves its name plain.
func (c *Client) stylingOf(segs []model.Seg) []styling.StyledTextOption {
	return stylingOf(segs, func(id int64) tg.InputUserClass {
		c.mu.Lock()
		p, ok := c.seen[id]
		c.mu.Unlock()
		if u, user := p.(peers.User); ok && user && !isMin(u) {
			return u.InputUser()
		}
		return nil
	})
}

// edited : sent for an edit, its receipt an EvEdited.
func (c *Client) edited(name string, chat *model.Chat, id int, edit func() (tg.UpdatesClass, error)) {
	go func() {
		defer c.Guard(name, func(err string) { c.Post(model.EvEdited{ChatID: chat.ID, ID: id, Err: err}) })
		ev := model.EvEdited{ChatID: chat.ID, ID: id}
		if _, err := edit(); err != nil {
			ev.Err = err.Error()
		}
		c.Post(ev)
	}()
}

// EditStyled : like Edit, with formatting on the segments (fences of the draft).
func (c *Client) EditStyled(ctx context.Context, chat *model.Chat, id int, segs []model.Seg) {
	c.edited("EditStyled", chat, id, func() (tg.UpdatesClass, error) {
		return c.sender.To(c.peer(chat)).Edit(id).StyledText(ctx, c.stylingOf(segs)...)
	})
}

// Edit replaces the text of one of my messages.
func (c *Client) Edit(ctx context.Context, chat *model.Chat, id int, text string) {
	c.edited("Edit", chat, id, func() (tg.UpdatesClass, error) { return c.sender.To(c.peer(chat)).Edit(id).Text(ctx, text) })
}

// Delete deletes a message for everybody. The server update comes too: the UI
// drops the duplicate on Deleted.
func (c *Client) Delete(ctx context.Context, chat *model.Chat, id int) {
	go func() {
		defer c.Guard("Delete", nil)
		var err error
		if ch, ok := c.peer(chat).(*tg.InputPeerChannel); ok {
			_, err = c.api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{Channel: inputChannel(ch), ID: []int{id}})
		} else {
			_, err = c.api.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{Revoke: true, ID: []int{id}})
		}
		if err != nil {
			c.Post(model.EvLog{Level: "ERROR", Msg: i18n.T("delete_error", err)})
			return
		}
		c.Post(model.EvDeleted{ChatID: chat.ID, IDs: []int{id}})
	}()
}

// SendReply : like Send, as a reply to the message replyTo.
func (c *Client) SendReply(ctx context.Context, chat *model.Chat, text string, replyTo int, tmpID int64) {
	c.sent("SendReply", chat, tmpID, func() (tg.UpdatesClass, error) {
		return c.sender.To(c.peer(chat)).Reply(replyTo).Text(ctx, text)
	})
}

// SendPhoto sends a local image as a photo, caption optional.
// SendFile does the same as a document (name and mime kept).
//
// EvSent like the text sends: the UI has already shown the send as a pending
// line, and the receipt unpends it. The echo of the sent message comes back
// besides, through the updates of the RPC (fed back by hook.UpdateHook, see
// New); whichever of the two comes first, the window keeps one line.
// removeAfter : work file (pasted image), erased once the send worked; a file
// chosen by the user never is.
func (c *Client) SendPhoto(ctx context.Context, chat *model.Chat, path, caption string, removeAfter bool, tmpID int64) {
	c.upload(ctx, chat, path, caption, true, removeAfter, tmpID)
}

func (c *Client) SendFile(ctx context.Context, chat *model.Chat, path, caption string, removeAfter bool, tmpID int64) {
	c.upload(ctx, chat, path, caption, false, removeAfter, tmpID)
}

func (c *Client) upload(ctx context.Context, chat *model.Chat, path, caption string, photo, removeAfter bool, tmpID int64) {
	go func() {
		fail := func(err string) {
			c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID,
				Err: i18n.T("upload_error", filepath.Base(path), err)})
		}
		defer c.Guard("upload", fail)
		// Own semaphore: a send never waits behind three downloads.
		select {
		case c.ulSem <- struct{}{}:
		case <-ctx.Done():
			fail(ctx.Err().Error())
			return
		}
		defer func() { <-c.ulSem }()
		f, err := uploader.NewUploader(c.api).WithProgress(&upProgress{c: c, last: -1}).FromPath(ctx, path)
		if err != nil {
			fail(err.Error())
			return
		}
		var capt []styling.StyledTextOption
		if caption != "" {
			capt = append(capt, styling.Plain(caption))
		}
		b := c.sender.To(c.peer(chat))
		var id int
		if photo {
			id, err = unpack.MessageID(b.UploadedPhoto(ctx, f, capt...))
		} else {
			// No ForceFile: the server thus keeps the preview of a video or a gif.
			mt := mimeOf(path)
			doc := message.UploadedDocument(f, capt...).
				Filename(filepath.Base(path)).MIME(mt)
			// A video without DocumentAttributeVideo shows up as a plain file
			// at the other end. ffprobe absent or failing: plain document.
			if strings.HasPrefix(mt, "video/") || strings.HasPrefix(mt, "audio/") {
				if w, h, dur, perr := media.ProbeVideo(ctx, path); perr == nil {
					doc = doc.Attributes(sendAttrs(mt, w, h, dur)...)
				}
			}
			id, err = unpack.MessageID(b.Media(ctx, doc))
		}
		if err != nil {
			fail(err.Error()) // failure: the file stays, the send can be tried again
			return
		}
		if removeAfter {
			os.Remove(path)
		}
		// The id, like a text send: without it Window.Sent cannot tell the
		// pending line from the echo of the server, and both would stay.
		c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID, ID: id})
	}()
}

// sendAttrs gives the attributes that make a media play at the other end
// instead of hanging there as a file. w, h and dur come from ffprobe; a probe
// that gave nothing usable (dur <= 0) leaves the document as it is.
func sendAttrs(mt string, w, h int, dur time.Duration) []tg.DocumentAttributeClass {
	switch {
	case strings.HasPrefix(mt, "video/") && dur > 0 && w > 0 && h > 0:
		return []tg.DocumentAttributeClass{&tg.DocumentAttributeVideo{
			SupportsStreaming: true,
			Duration:          dur.Seconds(),
			W:                 w,
			H:                 h,
		}}
	// w > 0 on a sound file means an embedded cover: ffprobe hands back the
	// picture as a video stream. The file then stays a plain document rather
	// than going out as a track that is really a video (.webm is announced
	// audio/webm by the system table).
	case strings.HasPrefix(mt, "audio/") && dur > 0 && w <= 0:
		return []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{
			Duration: int(dur.Seconds()),
		}}
	}
	return nil
}

// mimeOf gives the type of a local file from its extension — the server uses
// it for the preview (video, gif). Unknown extension: octet-stream.
func mimeOf(path string) string {
	if m := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); m != "" {
		return strings.SplitN(m, ";", 2)[0]
	}
	return "application/octet-stream"
}

// upProgress : progress of an upload, posted at each whole percent. postNB: a
// late status bar is better than a blocked upload. The uploader keeps its
// single thread by default: Chunk is not concurrent.
type upProgress struct {
	c    *Client
	last int
}

func (p *upProgress) Chunk(_ context.Context, s uploader.ProgressState) error {
	if s.Total <= 0 {
		return nil
	}
	pct := int(s.Uploaded * 100 / s.Total)
	if pct == p.last {
		return nil
	}
	p.last = pct
	p.c.PostNB(model.EvUpload{Text: i18n.T("upload_progress", pct)})
	return nil
}
