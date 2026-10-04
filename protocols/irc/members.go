package irc

import (
	"slices"
	"strings"

	"github.com/ergochat/irc-go/ircmsg"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

// member bookkeeping: NAMES fills the list, JOIN/PART/KICK/QUIT/NICK keep it
// right. QUIT and NICK carry no channel: the lists say where the person was.
// Folded keys: a netsplit is a burst of QUITs, one lookup each.
func (c *Client) addMember(channel, nick string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := c.casefold(channel)
	if c.members[k] == nil {
		c.members[k] = map[string]string{}
	}
	c.members[k][c.casefold(nick)] = nick
}

func (c *Client) dropMember(channel, nick string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.members[c.casefold(channel)], c.casefold(nick))
}

// channelsOf : the channels nick is seen in.
func (c *Client) channelsOf(nick string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	f := c.casefold(nick)
	for ch, ns := range c.members {
		if _, in := ns[f]; in {
			out = append(out, ch)
		}
	}
	slices.Sort(out)
	return out
}

// onNames : 353 "<me> <=|*|@> <channel> :nick nick…", gathered until 366.
func (c *Client) onNames(e ircmsg.Message) {
	if len(e.Params) < 4 {
		return
	}
	k := c.casefold(e.Params[2])
	c.mu.Lock()
	if len(c.names[k]) < maxGather {
		c.names[k] = append(c.names[k], strings.Fields(e.Params[3])...)
	}
	c.mu.Unlock()
}

// onEndOfNames : the list is whole — it becomes the member list, and answers
// the Participants call waiting for it.
func (c *Client) onEndOfNames(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	k := c.casefold(e.Params[1])
	c.mu.Lock()
	names := c.names[k]
	delete(c.names, k)
	// Only a room we are in has a member list: the key is set by the self
	// JOIN. A 366 for any other room (/names #other) must not create one, or
	// Resolve would read it as "already joined" and skip the JOIN.
	if _, joined := c.members[k]; joined {
		c.members[k] = make(map[string]string, len(names))
		for _, n := range names {
			bare := strings.TrimLeft(n, "~&@%+")
			c.members[k][c.casefold(bare)] = bare
		}
	}
	chat := c.naming[k]
	delete(c.naming, k)
	reply, asked := c.asked["names:"+k]
	delete(c.asked, "names:"+k)
	c.mu.Unlock()
	if asked { // /names: the list as it comes, prefixes and all
		c.Post(model.EvLines{ChatID: reply, Lines: []string{i18n.T("irc_names", e.Params[1], strings.Join(names, " "))}})
	}
	if chat == nil {
		return
	}
	ev := model.EvParticipants{ChatID: chat.ID}
	for _, n := range names {
		bare := strings.TrimLeft(n, "~&@%+")
		ev.Lines = append(ev.Lines, model.Participant{Text: n, Name: bare, Query: bare})
	}
	c.Post(ev)
}
