package tgc

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
)

// peerPhoto gives the location of the small profile photo of a peer, nil when
// it has none. The Big flag is left out: the thumbnail is what is downloaded.
func peerPhoto(p peers.Peer) tg.InputFileLocationClass {
	var id int64
	switch v := p.(type) {
	case peers.User:
		if ph, ok := v.Raw().GetPhoto(); ok {
			if up, ok := ph.(*tg.UserProfilePhoto); ok {
				id = up.PhotoID
			}
		}
	case peers.Chat:
		if cp, ok := v.Raw().GetPhoto().(*tg.ChatPhoto); ok {
			id = cp.PhotoID
		}
	case peers.Channel:
		if cp, ok := v.Raw().GetPhoto().(*tg.ChatPhoto); ok {
			id = cp.PhotoID
		}
	}
	if id == 0 {
		return nil
	}
	return &tg.InputPeerPhotoFileLocation{Peer: p.InputPeer(), PhotoID: id}
}

// mediaOf turns a Telegram media into a model.Media (label + download location).
func mediaOf(mm tg.MessageMediaClass) *model.Media {
	switch v := mm.(type) {
	case nil, *tg.MessageMediaEmpty:
		return nil
	case *tg.MessageMediaPhoto:
		if p, ok := v.Photo.(*tg.Photo); ok {
			return photoMedia(p, 800)
		}
		return nil
	case *tg.MessageMediaWebPage:
		if p, ok := v.Webpage.(*tg.WebPage); ok { // Pending / Empty: nothing to show
			return webPageMedia(p)
		}
		return nil
	case *tg.MessageMediaDocument:
		if d, ok := v.Document.(*tg.Document); ok {
			return docMedia(d)
		}
		return nil
	case *tg.MessageMediaGeo:
		if g, ok := v.Geo.(*tg.GeoPoint); ok {
			if lat, long, ok := clampGeo(g.Lat, g.Long); ok {
				return geoMedia(lat, long, i18n.T("media_location", lat, long))
			}
		}
		return nil
	case *tg.MessageMediaGeoLive:
		if g, ok := v.Geo.(*tg.GeoPoint); ok {
			if lat, long, ok := clampGeo(g.Lat, g.Long); ok {
				return geoMedia(lat, long, i18n.T("media_live_location", lat, long))
			}
		}
		return nil
	case *tg.MessageMediaVenue:
		if g, ok := v.Geo.(*tg.GeoPoint); ok {
			if lat, long, ok := clampGeo(g.Lat, g.Long); ok {
				parts := []string{i18n.T("media_venue")}
				for _, s := range []string{oneLine(v.Title), oneLine(v.Address)} {
					if s != "" {
						parts = append(parts, s)
					}
				}
				return geoMedia(lat, long, "["+strings.Join(parts, " · ")+"]")
			}
		}
		return nil
	case *tg.MessageMediaContact:
		return &model.Media{Kind: model.MediaOther, Label: strings.TrimSpace(fmt.Sprintf("[contact %s %s %s]", v.FirstName, v.LastName, v.PhoneNumber))}
	case *tg.MessageMediaPoll:
		return &model.Media{Kind: model.MediaOther, Label: i18n.T("media_poll", v.Poll.Question.Text)}
	case *tg.MessageMediaDice:
		return &model.Media{Kind: model.MediaOther, Label: fmt.Sprintf("[%s %d]", v.Emoticon, v.Value)}
	}
	return &model.Media{Kind: model.MediaOther, Label: i18n.T("media_other", strings.TrimPrefix(mm.TypeName(), "messageMedia"))}
}

// photoMedia gives the largest size under maxPx px (800 for a message photo,
// 400 for the thumbnail of a link preview); failing that, the smallest one.
func photoMedia(p *tg.Photo, maxPx int) *model.Media {
	var bt, st string
	var bw, bh, bs int
	sw, sh, ss := 1<<30, 0, 0
	for _, s := range p.Sizes {
		var t string
		var w, h, size int
		switch v := s.(type) {
		case *tg.PhotoSize:
			t, w, h, size = v.Type, v.W, v.H, v.Size
		case *tg.PhotoSizeProgressive:
			t, w, h = v.Type, v.W, v.H
			if n := len(v.Sizes); n > 0 {
				size = v.Sizes[n-1]
			}
		default:
			continue
		}
		if w <= maxPx && h <= maxPx && w > bw {
			bt, bw, bh, bs = t, w, h, size
		}
		if w < sw {
			st, sw, sh, ss = t, w, h, size
		}
	}
	if bt == "" {
		bt, bw, bh, bs = st, sw, sh, ss
	}
	if bt == "" {
		return &model.Media{Kind: model.MediaOther, Label: "[photo]"}
	}
	return &model.Media{Kind: model.MediaPhoto, W: bw, H: bh, Size: int64(bs), Ext: ".jpg", Mime: "image/jpeg",
		Loc: p.AsInputPhotoFileLocation(bt), Label: fmt.Sprintf("[photo %dx%d · %s]", bw, bh, render.HumanSize(int64(bs)))}
}

// webPageMedia : link preview. The URL wins over the thumbnail: "o" opens the
// page and not the downloaded file.
func webPageMedia(p *tg.WebPage) *model.Media {
	parts := []string{i18n.T("media_link")}
	for _, s := range []string{p.SiteName, p.Title} {
		if s = oneLine(s); s != "" {
			parts = append(parts, s)
		}
	}
	m := &model.Media{Kind: model.MediaWebPage, URL: p.URL, Ext: ".jpg", Mime: "image/jpeg",
		Label: "[" + strings.Join(parts, " · ") + "]",
		Name:  render.Truncate(oneLine(p.Description), 200, "…")}
	if ph, ok := p.Photo.(*tg.Photo); ok {
		if t := photoMedia(ph, 400); t.Loc != nil { // thumbnail: the big picture of the site has nothing to do here
			m.Loc, m.W, m.H, m.Size = t.Loc, t.W, t.H, t.Size
		}
	}
	return m
}

// clampGeo gives a safe lat/lon for a map — it refuses NaN/Inf (wild
// position: no media) and clamps inside the bounds of the Web Mercator
// projection (±85.0511° of latitude, ±180° of longitude). The same value then
// feeds the label, the OSM URL and the cache file name (media.Tile and
// media.MapName clamp again: defence in depth).
func clampGeo(lat, long float64) (float64, float64, bool) {
	if math.IsNaN(lat) || math.IsInf(lat, 0) || math.IsNaN(long) || math.IsInf(long, 0) {
		return 0, 0, false
	}
	return max(-85.0511, min(85.0511, lat)), max(-180, min(180, long)), true
}

// geoMedia turns a location, a live position or a venue into an OSM map. W/H
// are fixed: media.Tile always builds a 512x512 image (2x2 tiles). URL: OSM
// link centred on the point — "o" and the click open it in the browser.
func geoMedia(lat, long float64, label string) *model.Media {
	return &model.Media{Kind: model.MediaMap, Lat: lat, Long: long, W: 512, H: 512,
		Ext: ".png", Mime: "image/png", Label: label,
		URL: fmt.Sprintf("https://www.openstreetmap.org/?mlat=%.5f&mlon=%.5f#map=15/%.5f/%.5f", lat, long, lat, long)}
}

func docMedia(d *tg.Document) *model.Media {
	m := &model.Media{Kind: model.MediaFile, Size: d.Size, Mime: d.MimeType, Loc: d.AsInputDocumentFileLocation("")}
	var animated, video, sticker bool
	for _, a := range d.Attributes {
		switch v := a.(type) {
		case *tg.DocumentAttributeAnimated:
			animated = true
		case *tg.DocumentAttributeSticker:
			sticker, m.Emoji = true, v.Alt
		case *tg.DocumentAttributeVideo:
			video, m.Duration, m.W, m.H = true, v.Duration, v.W, v.H
		case *tg.DocumentAttributeImageSize:
			m.W, m.H = v.W, v.H
		case *tg.DocumentAttributeAudio:
			m.Duration = float64(v.Duration)
			if v.Voice {
				m.Kind = model.MediaVoice
			} else {
				m.Kind = model.MediaAudio
				m.Name = strings.Trim(v.Performer+" – "+v.Title, " –")
			}
		case *tg.DocumentAttributeFilename:
			if m.Kind != model.MediaAudio || m.Name == "" { // do not overwrite "Performer – Title"
				m.Name = v.FileName
			}
		}
	}
	m.Ext = extOf(m.Name, m.Mime)
	switch {
	case sticker:
		m.Kind = model.MediaSticker
		m.Animated = m.Mime != "image/webp"
		m.Label = "[sticker " + m.Emoji + "]"
		if m.Animated {
			m.Label = i18n.T("media_animated_sticker", m.Emoji)
		}
	case animated:
		m.Kind = model.MediaGIF
		m.Label = fmt.Sprintf("[gif %s · %s]", fmtDur(m.Duration), render.HumanSize(m.Size))
	case video:
		m.Kind = model.MediaVideo
		m.Label = fmt.Sprintf("[video %s · %s]", fmtDur(m.Duration), render.HumanSize(m.Size))
	case m.Kind == model.MediaVoice:
		m.Label = fmt.Sprintf("[voice %s]", fmtDur(m.Duration))
	case m.Kind == model.MediaAudio:
		m.Label = fmt.Sprintf("[audio %s %s]", m.Name, fmtDur(m.Duration))
	default:
		m.Label = i18n.T("media_file", m.Name, render.HumanSize(m.Size))
		if strings.HasPrefix(m.Mime, "image/") { // image sent "as a file"
			m.Kind = model.MediaPhoto
		}
	}
	return m
}

// allowExt : the extensions kept as they are on disk — inert types only. A
// deny list ages badly (.jar, .jnlp, .deb, .rpm, .py, .php… all start
// something under xdg-open, and the next one is not in the list yet); this
// one refuses everything it does not know. Anything else is saved as .bin,
// and the interface never hands a .bin to the desktop.
var allowExt = map[string]bool{
	// images
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".bmp": true, ".tif": true, ".tiff": true, ".heic": true, ".avif": true,
	// video
	".mp4": true, ".webm": true, ".mkv": true, ".mov": true, ".avi": true,
	".m4v": true, ".mpg": true, ".mpeg": true, ".3gp": true,
	// audio
	".mp3": true, ".ogg": true, ".oga": true, ".opus": true, ".m4a": true,
	".aac": true, ".flac": true, ".wav": true,
	// office documents
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
	".ppt": true, ".pptx": true, ".odt": true, ".ods": true, ".odp": true,
	// archives
	".zip": true, ".tar": true, ".gz": true, ".7z": true,
	// plain text
	".txt": true, ".md": true, ".log": true, ".csv": true, ".json": true,
}

// extOf : the mime type decides; the name comes from the sender, so its
// extension is kept only for lack of anything better, and only when it is on
// the allow list.
func extOf(name, mime string) string {
	if e := extOfMime(mime); e != ".bin" {
		return e
	}
	if e := strings.ToLower(filepath.Ext(name)); allowExt[e] {
		return e
	}
	return ".bin"
}

func extOfMime(mime string) string {
	if mime == "application/x-tgsticker" {
		return ".tgs"
	}
	return media.Extension(mime)
}

func fmtDur(s float64) string {
	t := int(s + 0.5)
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func describeAction(a tg.MessageActionClass) string {
	switch v := a.(type) {
	case *tg.MessageActionChatAddUser, *tg.MessageActionChatJoinedByLink:
		return i18n.T("action_joined")
	case *tg.MessageActionChatDeleteUser:
		return i18n.T("action_left")
	case *tg.MessageActionPinMessage:
		return i18n.T("action_pinned")
	case *tg.MessageActionChatEditTitle:
		return i18n.T("action_renamed_group", v.Title)
	case *tg.MessageActionChatCreate:
		return i18n.T("action_created_group", v.Title)
	case *tg.MessageActionChannelCreate:
		return i18n.T("action_created_channel", v.Title)
	case *tg.MessageActionContactSignUp:
		return i18n.T("action_joined_telegram")
	}
	return strings.TrimPrefix(a.TypeName(), "messageAction")
}

func unixTime(d int) time.Time { return time.Unix(int64(d), 0) }

// emojiOf gives the emoji of a Telegram reaction (custom → ⭐), "" for an
// unknown form.
func emojiOf(r tg.ReactionClass) string {
	switch e := r.(type) {
	case *tg.ReactionEmoji:
		return e.Emoticon
	case *tg.ReactionCustomEmoji:
		return "⭐"
	}
	return ""
}

// reactionsOf turns a Telegram ReactionCount into a model.Reaction.
func reactionsOf(rs []tg.ReactionCount) []model.Reaction {
	var out []model.Reaction
	for _, r := range rs {
		e := emojiOf(r.Reaction)
		if e == "" {
			continue
		}
		_, mine := r.GetChosenOrder()
		out = append(out, model.Reaction{Emoji: e, Count: r.Count, Mine: mine})
	}
	return out
}

// reactionUpdate looks for an UpdateMessageReactions in the answer of an RPC
// (Updates / UpdatesCombined / UpdateShort are the forms that can carry it).
func reactionUpdate(u tg.UpdatesClass) *tg.UpdateMessageReactions {
	var list []tg.UpdateClass
	switch v := u.(type) {
	case *tg.UpdateShort:
		list = []tg.UpdateClass{v.Update}
	case *tg.Updates:
		list = v.Updates
	case *tg.UpdatesCombined:
		list = v.Updates
	}
	for _, up := range list {
		if r, ok := up.(*tg.UpdateMessageReactions); ok {
			return r
		}
	}
	return nil
}

// formatStatus gives the Telegram presence, "" when the account hides it.
func formatStatus(s tg.UserStatusClass, now time.Time) string {
	switch v := s.(type) {
	case *tg.UserStatusOnline:
		if now.Before(unixTime(v.Expires)) { // Expires is a TTL, not an end-of-session date
			return i18n.T("presence_online")
		}
		return i18n.T("presence_recently")
	case *tg.UserStatusOffline:
		return i18n.T("presence_seen", since(unixTime(v.WasOnline), now))
	case *tg.UserStatusRecently:
		return i18n.T("presence_recently")
	case *tg.UserStatusLastWeek:
		return i18n.T("presence_last_week")
	case *tg.UserStatusLastMonth:
		return i18n.T("presence_last_month")
	}
	return ""
}

// since gives "5 min ago", "3 h ago", "yesterday", "on 12/08". The calendar is
// the one of now: past midnight, 20 min earlier stays "20 min ago".
func since(t, now time.Time) string {
	t = t.In(now.Location())
	d := now.Sub(t)
	sameDay := func(a, b time.Time) bool {
		ay, am, ad := a.Date()
		by, bm, bd := b.Date()
		return ay == by && am == bm && ad == bd
	}
	switch {
	case d < time.Hour: // covers a clock running fast (d negative): "1 min ago"
		return i18n.T("since_minutes", max(1, int(d.Minutes())))
	case sameDay(t, now):
		return i18n.T("since_hours", int(d.Hours()))
	case sameDay(t, now.AddDate(0, 0, -1)):
		return i18n.T("since_yesterday")
	}
	return i18n.T("since_date", t.Format(i18n.T("date_layout_short")))
}
