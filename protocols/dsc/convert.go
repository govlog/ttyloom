package dsc

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/diamondburned/arikawa/v3/discord"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/media"
	"github.com/govlog/ttyloom/internal/model"
	// Aliased: render is already the name of the function that turns segments
	// back into Discord markdown (markdown.go).
	rend "github.com/govlog/ttyloom/internal/render"
)

// Snowflake → int64: a Discord id carries a millisecond date in its high bits
// and its sign bit stays clear until 2084, so int64 holds every id this client
// will ever see. Every int64(…) and int(…) below is that same cast.

// msgOf converts one Discord message. The names of the mentions come from the
// message itself — Discord ships every mentioned user with it — and the
// nickname of the author from the member cache: never a call per message.
func (c *Client) msgOf(m *discord.Message) model.Msg {
	msg := model.Msg{
		ID:     int(m.ID),
		ChatID: int64(m.ChannelID),
		Date:   m.ID.Time(), // the snowflake is the send date, to the millisecond
		From:   c.nameOf(m.GuildID, m.Author),
		FromID: int64(m.Author.ID),
		Out:    int64(m.Author.ID) == c.self.Load(),
		Edited: m.EditedTimestamp.IsValid(),
		Media:  mediaOf(m),
	}
	if u := m.Author.AvatarURL(); u != "" {
		msg.FromPhoto = fileURL(u)
	}
	if s := serviceOf(m.Type, msg.From); s != "" {
		msg.Service = s
		return msg // no text, no reaction, no quote: the line is the whole message
	}
	msg.Text, msg.Entities = parse(m.Content, mentionNames(m.Mentions))
	msg.Reactions = reactionsOf(m.Reactions)
	if r := m.ReferencedMessage; r != nil {
		text, _ := parse(r.Content, nil) // the quote is one line: the markers only get in the way
		// The quoted message often carries no guild of its own; it lives in the
		// same channel, so the guild of the message that quotes it is the right
		// one to read the nickname with.
		msg.Reply = &model.Quote{ID: int(r.ID),
			From: c.nameOf(cmp.Or(r.GuildID, m.GuildID), r.Author), Text: text}
	}
	return msg
}

// serviceOf : Discord ships a member join, a pin, a boost and a dozen more as
// messages of a type of their own, with an empty content — shown as they are
// they would be an empty line from their author. The text of the line is what
// comes back, "" for an ordinary message. InlinedReply is one of those — a
// reply is a plain message that quotes another one — and so are the two
// answers to an interaction, ChatInputCommand (a slash command) and
// ContextMenuCommand: a bot posts them with a content and embeds of their own,
// and any guild running a bot would show them as an empty system line.
//
// ponytail: the three types worth a wording of their own, everything else on
// one generic line rather than thirty translations nobody would read. Name a
// type the day it shows up often enough to be worth it.
func serviceOf(t discord.MessageType, who string) string {
	switch t {
	case discord.DefaultMessage, discord.InlinedReplyMessage,
		discord.ChatInputCommandMessage, discord.ContextMenuCommand:
		return ""
	case discord.GuildMemberJoinMessage:
		return i18n.T("dsc_sys_join", who)
	case discord.ChannelPinnedMessage:
		return i18n.T("dsc_sys_pin", who)
	case discord.NitroBoostMessage, discord.NitroTier1Message,
		discord.NitroTier2Message, discord.NitroTier3Message:
		return i18n.T("dsc_sys_boost", who)
	}
	return i18n.T("dsc_sys_other", who)
}

// nameOf gives the name to show for a user: their nickname in the guild when
// the state knows it, their display name otherwise. On Discord the nickname is
// often the only recognisable one.
func (c *Client) nameOf(guild discord.GuildID, u discord.User) string {
	if c.state() != nil && guild.IsValid() {
		if m, err := c.state().Cabinet.Member(guild, u.ID); err == nil && m.Nick != "" {
			return m.Nick
		}
	}
	return u.DisplayOrUsername()
}

// mentionNames resolves <@id> for parse. Every user a message mentions travels
// with it, so nothing is looked up; an id missing from the list stays "@<id>".
func mentionNames(us []discord.GuildUser) func(uint64) string {
	if len(us) == 0 {
		return nil
	}
	return func(id uint64) string {
		for _, u := range us {
			if uint64(u.ID) != id {
				continue
			}
			if u.Member != nil && u.Member.Nick != "" {
				return u.Member.Nick
			}
			return u.DisplayOrUsername()
		}
		return ""
	}
}

// reactionsOf : the reactions of a message, in the order Discord gives them.
func reactionsOf(rs []discord.Reaction) []model.Reaction {
	var out []model.Reaction
	for _, r := range rs {
		out = append(out, model.Reaction{Emoji: emojiOf(r.Emoji), Count: r.Count, Mine: r.Me})
	}
	return out
}

// emojiOf : a unicode reaction is its own glyph; a custom one has none here,
// and ":name:" is what a text client can show of it.
func emojiOf(e discord.Emoji) string {
	if e.ID.IsValid() {
		return ":" + e.Name + ":"
	}
	return e.Name
}

// mediaOf : the first attachment, or failing that the first embed that has a
// link. Discord takes several of each, the UI shows one media per message.
func mediaOf(m *discord.Message) *model.Media {
	if len(m.Attachments) > 0 {
		return attachMedia(m.Attachments[0])
	}
	for _, e := range m.Embeds {
		if md := gifvMedia(e); md != nil {
			return md
		}
		if e.URL != "" {
			return embedMedia(e)
		}
	}
	return nil
}

// gifvMedia : a Tenor or Giphy link unfurled by Discord (what SendGif posts)
// carries the clip as a "gifv" embed — an animated GIF of the message, the
// page kept as its URL, rather than a link label nobody can play. nil for any
// other embed, or one whose URLs are not http(s).
func gifvMedia(e discord.Embed) *model.Media {
	if e.Type != discord.GIFVEmbed || e.Video == nil || !rend.SafeURL(string(e.Video.URL)) || !rend.SafeURL(e.URL) {
		return nil
	}
	w, h := int(e.Video.Width), int(e.Video.Height)
	return &model.Media{Kind: model.MediaGIF, W: w, H: h, Mime: "video/mp4", Ext: ".mp4",
		Loc: fileURL(e.Video.URL), URL: e.URL, Label: fmt.Sprintf("[gif %dx%d]", w, h)}
}

func attachMedia(a discord.Attachment) *model.Media {
	// A media type can come with its parameters ("text/plain; charset=utf-8");
	// the type alone decides the kind and the extension.
	mime, _, _ := strings.Cut(a.ContentType, ";")
	ext := media.Extension(mime)
	m := &model.Media{Kind: model.MediaFile, W: int(a.Width), H: int(a.Height),
		Size: int64(a.Size), Name: a.Filename, Mime: mime,
		Ext: ext, Loc: fileURL(a.URL)}
	switch {
	case strings.HasPrefix(mime, "image/"):
		m.Kind = model.MediaPhoto
		m.Label = fmt.Sprintf("[photo %dx%d · %s]", m.W, m.H, rend.HumanSize(m.Size))
	case strings.HasPrefix(mime, "video/"):
		m.Kind = model.MediaVideo
		m.Label = fmt.Sprintf("[video %dx%d · %s]", m.W, m.H, rend.HumanSize(m.Size))
	default:
		m.Label = i18n.T("media_file", a.Filename, rend.HumanSize(m.Size))
	}
	return m
}

// embedMedia : link preview. Like tgc the URL wins — "o" opens the page, and
// no thumbnail is downloaded (no Loc: label only).
func embedMedia(e discord.Embed) *model.Media {
	parts := []string{i18n.T("media_link")}
	if t := rend.CleanLine(e.Title); t != "" {
		parts = append(parts, t)
	}
	return &model.Media{Kind: model.MediaWebPage, URL: e.URL,
		Label: "[" + strings.Join(parts, " · ") + "]",
		Name:  rend.Truncate(rend.CleanLine(e.Description), 200, "…")}
}

// chatOf turns a channel into an entry of the sidebar. guild is the name of
// the guild it belongs to, "" for a DM or a guild that is not known yet.
// Channel stays false: it means "Telegram channel", a peer whose ids never
// show up in a global delete — Discord has no such thing.
func chatOf(ch *discord.Channel, guild string) *model.Chat {
	c := &model.Chat{ID: int64(ch.ID), Kind: model.ChatGroup,
		Peer: peer{Channel: uint64(ch.ID), Guild: uint64(ch.GuildID)}}
	switch ch.Type {
	case discord.DirectMessage:
		c.Kind = model.ChatUser
		if len(ch.DMRecipients) > 0 {
			r := ch.DMRecipients[0]
			c.Title, c.Username = r.DisplayOrUsername(), r.Username
			if u := r.AvatarURL(); u != "" {
				c.PhotoLoc = fileURL(u)
			}
		}
	case discord.GroupDM:
		c.Title = cmp.Or(ch.Name, recipients(ch.DMRecipients))
	default:
		c.Title = "#" + ch.Name
		if guild != "" {
			c.Title = guild + " / " + c.Title
		}
	}
	// A DM whose recipients the state does not carry would land in the sidebar
	// with no title at all; its id is at least something to click on.
	c.Title = cmp.Or(c.Title, ch.ID.String())
	if ch.LastMessageID.IsValid() {
		c.LastDate = ch.LastMessageID.Time()
	}
	return c
}

// recipients : fallback title of a group DM with no name of its own.
func recipients(us []discord.User) string {
	names := make([]string, 0, len(us))
	for _, u := range us {
		names = append(names, u.DisplayOrUsername())
	}
	return strings.Join(names, ", ")
}

// readOf fills the read marks of a chat from the read state of the account.
// Discord gives no unread count, only a mention count and "there is something
// after what I read": 1 then stands for "unread", which is all the sidebar
// needs to mark the line.
func (c *Client) readOf(chat *model.Chat, ch *discord.Channel) {
	rs := c.state().ReadState.ReadState(ch.ID)
	if rs == nil {
		// ponytail: a channel never opened carries no read state and stays
		// unmarked, rather than showing everything it holds as unread — much
		// quieter on an account with many guilds. Count it if the missing
		// badge ever shows in use.
		return
	}
	chat.ReadInboxMaxID = int(rs.LastMessageID)
	switch {
	case rs.MentionCount > 0:
		chat.Unread = rs.MentionCount
	case ch.LastMessageID > rs.LastMessageID:
		chat.Unread = 1
	}
}

// presenceOf : Discord status → the presence line of the UI. "online" is the
// exact text u.online() compares against, so it must stay that key — and idle
// and do-not-disturb, which have a line of their own, thus do not light the
// online marker of the new-chat overlay: neither one is available. Offline and
// invisible give no line at all, like an account that hides its presence on
// Telegram.
func presenceOf(s discord.Status) string {
	switch s {
	case discord.OnlineStatus:
		return i18n.T("presence_online")
	case discord.IdleStatus:
		return i18n.T("presence_idle")
	case discord.DoNotDisturbStatus:
		return i18n.T("presence_dnd")
	}
	return ""
}
