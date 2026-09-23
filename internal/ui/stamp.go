package ui

import (
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
)

// stamp sets Net on every Chat and Msg the event carries, so nothing past the
// dispatch has to guess which network an object comes from, and cleans the
// remote text once (render.Clean: no terminal sequence, no bidi override), so
// nothing past the dispatch has to. Events with a bare ChatID are left alone:
// their net is the one of the envelope, read from u.dispatchNet.
//
// Chats travel by pointer and are stamped in place; a Msg is a value, the
// events that carry one alone come back rewritten, and so do the events whose
// own strings are cleaned.
func stamp(net string, ev model.Event) model.Event {
	switch e := ev.(type) {
	case model.EvDialogs:
		stampChats(net, e.Chats)
	case model.EvContacts:
		stampChats(net, e.Peers)
		e.Err = render.CleanLine(e.Err)
		return e
	case model.EvContactsFound:
		stampChats(net, e.Peers)
		e.Err = render.CleanLine(e.Err)
		return e
	case model.EvChat:
		stampChat(net, e.Chat)
	case model.EvSearchGlobal:
		for i := range e.Hits {
			h := &e.Hits[i]
			stampChat(net, h.Chat)
			h.From, h.Text = render.CleanLine(h.From), render.CleanLine(h.Text)
		}
		e.Err = render.CleanLine(e.Err)
		return e
	case model.EvHistory:
		stampMsgs(net, e.Msgs)
	case model.EvSearch:
		stampMsgs(net, e.Msgs)
	case model.EvNewMessage:
		stampMsg(net, &e.Msg)
		stampChat(net, e.Chat)
		return e
	case model.EvEditMessage:
		stampMsg(net, &e.Msg)
		return e
	case model.EvLines:
		cleanLines(e.Lines)
	case model.EvInfo:
		cleanLines(e.Lines)
	case model.EvWhois:
		cleanLines(e.Lines)
		e.Err = render.CleanLine(e.Err)
		return e
	case model.EvWho:
		e.Text = render.CleanLine(e.Text)
		return e
	case model.EvAuthPrompt:
		e.Question = render.CleanLine(e.Question)
		return e
	case model.EvTyping:
		e.Who = render.CleanLine(e.Who)
		return e
	case model.EvParticipants:
		for i := range e.Lines {
			p := &e.Lines[i]
			p.Text, p.Name = render.CleanLine(p.Text), render.CleanLine(p.Name)
		}
	case model.EvGifs:
		e.Err = render.CleanLine(e.Err)
		return e
	case model.EvDownloaded:
		e.Err = render.CleanLine(e.Err) // becomes Media.Err
		return e
	}
	return ev
}

// stampChat : a nil chat is a normal answer (lookup failed, hit with no peer).
func stampChat(net string, c *model.Chat) {
	if c != nil {
		c.Net = net
		c.Title, c.Username = render.CleanLine(c.Title), render.CleanLine(c.Username)
	}
}

func stampChats(net string, cs []*model.Chat) {
	for _, c := range cs {
		stampChat(net, c)
	}
}

// stampMsg : the text and the link description keep their line breaks
// (render.Clean, rune for rune: the entity offsets stay valid); the names and
// the labels are single lines.
func stampMsg(net string, m *model.Msg) {
	m.Net = net
	m.Text, m.Service = render.Clean(m.Text), render.Clean(m.Service)
	m.From, m.ChatLabel = render.CleanLine(m.From), render.CleanLine(m.ChatLabel)
	if md := m.Media; md != nil {
		md.Label, md.Name, md.Err = render.CleanLine(md.Label), render.Clean(md.Name), render.CleanLine(md.Err)
	}
}

func stampMsgs(net string, ms []model.Msg) {
	for i := range ms {
		stampMsg(net, &ms[i])
	}
}

func cleanLines(ls []string) {
	for i, l := range ls {
		ls[i] = render.CleanLine(l)
	}
}
