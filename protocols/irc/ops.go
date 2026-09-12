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
		chats = append(chats, chatOf(ch))
	}
	for _, n := range c.queries {
		chats = append(chats, chatOf(n))
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
func (c *Client) Resolve(_ context.Context, q string, _ bool) {
	q = strings.TrimSpace(q)
	if !isChannel(q) {
		nick := strings.TrimPrefix(q, "@")
		if nick == "" || strings.ContainsAny(nick, " ,") {
			c.refuse("Resolve", model.EvChat{Query: q, Err: i18n.T("irc_bad_nick")})
			return
		}
		c.mu.Lock()
		c.queries[casefold(nick)] = nick
		c.mu.Unlock()
		c.refuse("Resolve", model.EvChat{Query: q, Chat: chatOf(nick)})
		return
	}
	c.mu.Lock()
	_, in := c.members[casefold(q)]
	if !in {
		c.joining[casefold(q)] = q
	}
	c.mu.Unlock()
	if in {
		c.refuse("Resolve", model.EvChat{Query: q, Chat: chatOf(q)})
		return
	}
	go func() {
		defer c.Guard("Resolve", nil)
		if err := c.send("JOIN", q); err != nil {
			c.mu.Lock()
			delete(c.joining, casefold(q))
			c.mu.Unlock()
			c.Post(model.EvChat{Query: q, Err: err.Error()})
		}
	}()
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
	c.naming[casefold(name)] = chat
	c.mu.Unlock()
	go func() {
		defer c.Guard("Participants", nil)
		if err := c.send("NAMES", name); err != nil {
			c.mu.Lock()
			delete(c.naming, casefold(name))
			c.mu.Unlock()
			c.Post(model.EvParticipants{ChatID: chat.ID, Err: err.Error()})
		}
	}()
}

// whoisNick : WHOIS nick, the lines gathered until 318 (or 401). A silent
// server would leave the request hanging: ten seconds, then an error.
func (c *Client) whoisNick(nick string, chatID int64) {
	k := casefold(nick)
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
func (c *Client) WhoisMember(_ context.Context, token string) { c.whoisNick(token, chatID(token)) }

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
	c.mu.Lock()
	c.channels = slices.DeleteFunc(c.channels, func(x string) bool { return casefold(x) == casefold(name) })
	delete(c.members, casefold(name))
	c.saveChannelsLocked()
	c.mu.Unlock()
	go func() {
		defer c.Guard("Leave", nil)
		_ = c.send("PART", name) // not connected: the room is out of the list all the same
		c.Post(model.EvChatGone{ChatID: chat.ID})
	}()
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
	delete(c.queries, casefold(name))
	delete(c.offers, casefold(name))
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
