package irc

import (
	"context"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// The methods of model.Backend. Every one comes back at once; the network
// work runs in a goroutine and ends in an event.

func (c *Client) unsupported() string { return i18n.T("net_unsupported", c.net()) }

// warn : a method IRC has no equivalent of and the UI waits no event from.
func (c *Client) warn(name string) {
	c.refuse(name, model.EvLog{Level: "WARN", Msg: c.net() + ": " + name + " not supported"})
}

// refuse : same for a method the UI does wait an event from.
func (c *Client) refuse(name string, ev model.Event) {
	go func() {
		defer c.Guard(name, nil)
		c.Post(ev)
	}()
}

// LoadDialogs : the rooms of the list and the private chats open. Never
// Complete — a chat the UI knows from its cache stays.
func (c *Client) LoadDialogs(context.Context) {
	c.mu.Lock()
	var chats []*model.Chat
	for _, ch := range c.channels {
		chats = append(chats, c.chatOf(ch))
	}
	for _, n := range c.queries {
		chats = append(chats, c.chatOf(n))
	}
	c.mu.Unlock()
	c.refuse("LoadDialogs", model.EvDialogs{Chats: chats})
}

// No history on the server: every page is empty and final, the disk cache
// of the UI is the scrollback.
func (c *Client) LoadHistory(_ context.Context, chat *model.Chat, beforeID, _ int) {
	c.refuse("LoadHistory", model.EvHistory{ChatID: chat.ID, Older: beforeID > 0, Done: true})
}

func (c *Client) LoadHistoryAround(_ context.Context, chat *model.Chat, id, _ int) {
	c.refuse("LoadHistoryAround", model.EvHistory{ChatID: chat.ID, Around: true, AroundID: id, Done: true})
}

func (c *Client) LoadHistorySince(_ context.Context, chat *model.Chat, _, _ int) {
	c.refuse("LoadHistorySince", model.EvHistory{ChatID: chat.ID, Since: true, Done: true})
}

// --- sends ---

// sendText : text to the chat, one PRIVMSG per line piece; the receipt
// unpends the local line (no server echo on IRC: the UI keeps what it has).
func (c *Client) sendText(name string, chat *model.Chat, text string, tmpID int64) {
	go func() {
		defer c.Guard(name, func(err string) { c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID, Err: err}) })
		to := nameOf(chat)
		for _, line := range splitLines(text, maxLine) {
			if err := c.send("PRIVMSG", to, line); err != nil {
				c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID, Err: err.Error()})
				return
			}
		}
		c.Post(model.EvSent{ChatID: chat.ID, TmpID: tmpID, ID: c.ids.next()})
	}()
}

func (c *Client) Send(_ context.Context, chat *model.Chat, text string, tmpID int64) {
	c.sendText("Send", chat, text, tmpID)
}

// SendReply : no reply on IRC — the nick of the author is put in front, the
// usual way to answer someone in a room. The UI gives the id only; the text
// of the message is not at hand here, so the prefix is what it can be.
func (c *Client) SendReply(_ context.Context, chat *model.Chat, text string, _ int, tmpID int64) {
	c.sendText("SendReply", chat, text, tmpID)
}

func (c *Client) SendStyled(_ context.Context, chat *model.Chat, segs []model.Seg, tmpID int64) {
	c.sendText("SendStyled", chat, styled(segs), tmpID)
}

func (c *Client) SendPre(_ context.Context, chat *model.Chat, text string, tmpID int64) {
	c.sendText("SendPre", chat, text, tmpID)
}

// SendPhoto : a file like any other — DCC SEND, to a person only.
func (c *Client) SendPhoto(ctx context.Context, chat *model.Chat, path, caption string, removeAfter bool, tmpID int64) {
	c.SendFile(ctx, chat, path, caption, removeAfter, tmpID)
}

func (c *Client) Edit(_ context.Context, chat *model.Chat, id int, _ string) {
	c.refuse("Edit", model.EvEdited{ChatID: chat.ID, ID: id, Err: c.unsupported()})
}

func (c *Client) EditStyled(_ context.Context, chat *model.Chat, id int, _ []model.Seg) {
	c.refuse("EditStyled", model.EvEdited{ChatID: chat.ID, ID: id, Err: c.unsupported()})
}

func (c *Client) Delete(context.Context, *model.Chat, int) { c.warn("Delete") }

func (c *Client) React(_ context.Context, chat *model.Chat, id int, e string) {
	c.refuse("React", model.EvReactionFailed{ChatID: chat.ID, ID: id, Emoji: e, Reason: c.unsupported()})
}

// Typing and MarkRead : nothing to tell the server.
func (c *Client) Typing(context.Context, *model.Chat, bool)  {}
func (c *Client) MarkRead(context.Context, *model.Chat, int) {}

func (c *Client) Search(_ context.Context, chat *model.Chat, q string, _ int) {
	c.refuse("Search", model.EvSearch{ChatID: chat.ID, Query: q, Err: c.unsupported()})
}

func (c *Client) SearchGlobal(_ context.Context, q string, _ int) {
	c.refuse("SearchGlobal", model.EvSearchGlobal{Query: q, Err: c.unsupported()})
}

func (c *Client) SearchContacts(_ context.Context, q string, _ int) {
	c.refuse("SearchContacts", model.EvContactsFound{Query: q, Err: c.unsupported()})
}

func (c *Client) Contacts(context.Context) {
	c.refuse("Contacts", model.EvContacts{Err: c.unsupported()})
}

// Resolve : "#room" joins it (the answer comes with the JOIN, or with the
// error numeric); anything else is a nick, a private chat opens at once.
func (c *Client) Resolve(_ context.Context, q string, _ bool, request uint64) {
	q = strings.TrimSpace(q)
	if !isChannel(q) {
		nick := strings.TrimPrefix(q, "@")
		if nick == "" || strings.ContainsAny(nick, " ,") {
			c.refuse("Resolve", model.EvChat{Request: request, Query: q, Err: i18n.T("irc_bad_nick")})
			return
		}
		c.mu.Lock()
		c.queries[c.casefold(nick)] = nick
		c.mu.Unlock()
		c.refuse("Resolve", model.EvChat{Request: request, Query: q, Chat: c.chatOf(nick)})
		return
	}
	name, key, _ := strings.Cut(q, " ") // "#room key" joins a room with a key
	c.mu.Lock()
	_, in := c.members[c.casefold(name)]
	if !in {
		c.joining[c.casefold(name)] = append(c.joining[c.casefold(name)], model.EvChat{Request: request, Query: q})
	}
	c.mu.Unlock()
	if in {
		c.refuse("Resolve", model.EvChat{Request: request, Query: q, Chat: c.chatOf(name)})
		return
	}
	go func() {
		defer c.Guard("Resolve", nil)
		params := []string{name}
		if key != "" {
			params = append(params, key)
		}
		if err := c.send("JOIN", params...); err != nil {
			c.mu.Lock()
			waiting := c.joining[c.casefold(name)]
			delete(c.joining, c.casefold(name))
			c.mu.Unlock()
			for _, ev := range waiting {
				ev.Err = err.Error()
				c.Post(ev)
			}
		}
	}()
}

// Members : who the client knows in chat, as the UI completes them — the
// member list of the room, kept up to date by NAMES and JOIN/PART, or the
// peer alone for a private chat.
func (c *Client) Members(chat *model.Chat) []string {
	name := nameOf(chat)
	if !isChannel(name) {
		return []string{name}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.members[c.casefold(name)])
}

// Participants : NAMES of the room (the answer lands on 366); a private chat
// has the two of us.
func (c *Client) Participants(_ context.Context, chat *model.Chat) {
	name := nameOf(chat)
	if !isChannel(name) {
		c.refuse("Participants", model.EvParticipants{ChatID: chat.ID, Lines: []model.Participant{
			{Text: c.me(), Query: c.me()}, {Text: name, Query: name}}})
		return
	}
	c.mu.Lock()
	c.naming[c.casefold(name)] = chat
	c.mu.Unlock()
	go func() {
		defer c.Guard("Participants", nil)
		if err := c.send("NAMES", name); err != nil {
			c.mu.Lock()
			delete(c.naming, c.casefold(name))
			c.mu.Unlock()
			c.Post(model.EvParticipants{ChatID: chat.ID, Err: err.Error()})
		}
	}()
}

// whoisNick : WHOIS nick, the lines gathered until 318 (or 401). A silent
// server would leave the request hanging: ten seconds, then an error.
func (c *Client) whoisNick(nick string, chatID int64) {
	k := c.casefold(nick)
	req := &whoisReq{chatID: chatID}
	c.mu.Lock()
	c.whois[k] = req
	c.mu.Unlock()
	go func() {
		defer c.Guard("Whois", nil)
		if err := c.send("WHOIS", nick); err != nil {
			c.mu.Lock()
			delete(c.whois, k)
			c.mu.Unlock()
			c.Post(model.EvWhois{ChatID: chatID, Err: err.Error()})
			return
		}
		time.Sleep(10 * time.Second)
		c.mu.Lock()
		late := c.whois[k] == req // a newer request for the same nick keeps its slot
		if late {
			delete(c.whois, k)
		}
		c.mu.Unlock()
		if late {
			c.Post(model.EvWhois{ChatID: chatID, Err: i18n.T("irc_timeout")})
		}
	}()
}

func (c *Client) Whois(_ context.Context, chat *model.Chat) { c.whoisNick(nameOf(chat), chat.ID) }

// WhoisMember : token is the Query of a participant line — the nick.
func (c *Client) WhoisMember(_ context.Context, token string) { c.whoisNick(token, c.chatID(token)) }

func (c *Client) WhoRead(_ context.Context, chat *model.Chat, id, _ int) {
	c.refuse("WhoRead", model.EvWho{ChatID: chat.ID, ID: id, Text: c.unsupported()})
}

func (c *Client) WhoReacted(_ context.Context, chat *model.Chat, id int, _ []model.Reaction) {
	c.refuse("WhoReacted", model.EvWho{ChatID: chat.ID, ID: id, React: true, Text: c.unsupported()})
}

func (c *Client) Info(_ context.Context, chat *model.Chat, id, _, _ int) {
	c.refuse("Info", model.EvInfo{ChatID: chat.ID, ID: id, Lines: []string{i18n.T("info_error", c.unsupported())}})
}

func (c *Client) Block(context.Context, *model.Chat)  { c.warn("Block") }
func (c *Client) BlockMember(context.Context, string) { c.warn("BlockMember") }

// Leave : PART, the room leaves the list (and the file), the UI drops it.
func (c *Client) Leave(_ context.Context, chat *model.Chat) {
	name := nameOf(chat)
	if !isChannel(name) {
		c.DeleteChat(context.Background(), chat)
		return
	}
	go func() {
		defer c.Guard("Leave", nil)
		c.part(name, "")
	}()
}

// part : PART with a reason, the room out of the list and of the file; the
// UI drops the window on EvChatGone.
func (c *Client) part(name, reason string) {
	c.mu.Lock()
	c.channels = slices.DeleteFunc(c.channels, func(x string) bool { return c.casefold(x) == c.casefold(name) })
	delete(c.members, c.casefold(name))
	c.saveChannelsLocked()
	c.mu.Unlock()
	params := []string{name}
	if reason != "" {
		params = append(params, reason)
	}
	_ = c.send("PART", params...) // not connected: the room is out of the list all the same
	c.Post(model.EvChatGone{ChatID: c.chatID(name)})
}

// DeleteChat : a private chat closes — nothing on the server. A room goes
// through Leave.
func (c *Client) DeleteChat(ctx context.Context, chat *model.Chat) {
	name := nameOf(chat)
	if isChannel(name) {
		c.Leave(ctx, chat)
		return
	}
	c.mu.Lock()
	delete(c.queries, c.casefold(name))
	delete(c.offers, c.casefold(name))
	c.mu.Unlock()
	c.refuse("DeleteChat", model.EvChatGone{ChatID: chat.ID})
}

// Download : a DCC offer is the only media of this network.
func (c *Client) Download(ctx context.Context, m *model.Media, path string) {
	off, ok := m.Loc.(dccOffer)
	if !ok {
		c.refuse("Download", model.EvDownloaded{Media: m, Err: i18n.T("media_foreign")})
		return
	}
	if _, err := os.Stat(path); err == nil {
		c.refuse("Download", model.EvDownloaded{Media: m, Path: path})
		return
	}
	go c.dccGet(ctx, m, off, path)
}

func (c *Client) DownloadMap(_ context.Context, m *model.Media, _ string) {
	c.refuse("DownloadMap", model.EvDownloaded{Media: m, Err: c.unsupported()})
}

func (c *Client) SearchGifs(_ context.Context, _ *model.Chat, q string) {
	c.refuse("SearchGifs", model.EvGifs{Query: q, Err: c.unsupported()})
}

func (c *Client) SendGif(_ context.Context, chat *model.Chat, _ model.Gif, tmpID int64) {
	c.refuse("SendGif", model.EvSent{ChatID: chat.ID, TmpID: tmpID, Err: c.unsupported()})
}
