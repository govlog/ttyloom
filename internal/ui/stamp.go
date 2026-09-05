package ui

import "github.com/govlog/ttyloom/internal/model"

// stamp sets Net on every Chat and Msg the event carries, so nothing past the
// dispatch has to guess which network an object comes from. Events with a
// bare ChatID are left alone: their net is the one of the envelope, read from
// u.dispatchNet.
//
// Chats travel by pointer and are stamped in place; a Msg is a value, the
// events that carry one alone come back rewritten.
func stamp(net string, ev model.Event) model.Event {
	switch e := ev.(type) {
	case model.EvDialogs:
		stampChats(net, e.Chats)
	case model.EvContacts:
		stampChats(net, e.Peers)
	case model.EvContactsFound:
		stampChats(net, e.Peers)
	case model.EvChat:
		stampChat(net, e.Chat)
	case model.EvSearchGlobal:
		for _, h := range e.Hits {
			stampChat(net, h.Chat)
		}
	case model.EvHistory:
		stampMsgs(net, e.Msgs)
	case model.EvSearch:
		stampMsgs(net, e.Msgs)
	case model.EvNewMessage:
		e.Msg.Net = net
		stampChat(net, e.Chat)
		return e
	case model.EvEditMessage:
		e.Msg.Net = net
		return e
	}
	return ev
}

// stampChat : a nil chat is a normal answer (lookup failed, hit with no peer).
func stampChat(net string, c *model.Chat) {
	if c != nil {
		c.Net = net
	}
}

func stampChats(net string, cs []*model.Chat) {
	for _, c := range cs {
		stampChat(net, c)
	}
}

func stampMsgs(net string, ms []model.Msg) {
	for i := range ms {
		ms[i].Net = net
	}
}
