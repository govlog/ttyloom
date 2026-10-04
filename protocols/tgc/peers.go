package tgc

import (
	"context"
	"errors"
	"time"

	"github.com/gotd/td/constant"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
)

func nick(p peers.Peer) string {
	if u, ok := p.Username(); ok && u != "" {
		return u
	}
	return p.VisibleName()
}

// tdlibID gives the TDLib id of a raw peer, with no lookup at all.
func tdlibID(p tg.PeerClass) int64 {
	var id constant.TDLibPeerID
	switch v := p.(type) {
	case *tg.PeerUser:
		id.User(v.UserID)
	case *tg.PeerChat:
		id.Chat(v.ChatID)
	case *tg.PeerChannel:
		id.Channel(v.ChannelID)
	}
	return int64(id)
}

// isMin : "min" entity — seen from afar (searchGlobal, updates), its
// access_hash is worth nothing for an RPC. See https://core.telegram.org/api/min
func isMin(p peers.Peer) bool {
	switch v := p.(type) {
	case peers.User:
		return v.Raw().Min
	case peers.Channel:
		return v.Raw().Min
	}
	return false // peers.Chat (small group): no access_hash, so no min
}

// remember keeps a peer seen for the messages that follow. A "min" entity
// never replaces a peer already known: it would break the RPCs that use it
// (history, send) while we had something better.
func (c *Client) remember(p peers.Peer) peers.Peer {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := int64(p.TDLibPeerID())
	if old, ok := c.seen[id]; ok && isMin(p) {
		return old
	}
	c.seen[id] = p
	return p
}

// rememberAll keeps every entity of a batch (history, search, update).
func (c *Client) rememberAll(ent peer.Entities) {
	for _, u := range ent.Users() {
		c.remember(c.peers.User(u))
	}
	for _, ch := range ent.Chats() {
		c.remember(c.peers.Chat(ch))
	}
	for _, ch := range ent.Channels() {
		c.remember(c.peers.Channel(ch))
	}
}

// peerOf gives the peer from the entities of the batch, else from the peers
// already seen. Never a network call: resolving each unknown sender through
// users.getUsers during the sync fires a FLOOD_WAIT (hundreds of calls).
func (c *Client) peerOf(ent peer.Entities, p tg.PeerClass) (peers.Peer, bool) {
	switch v := p.(type) {
	case *tg.PeerUser:
		if u, ok := ent.Users()[v.UserID]; ok {
			return c.remember(c.peers.User(u)), true
		}
	case *tg.PeerChat:
		if ch, ok := ent.Chats()[v.ChatID]; ok {
			return c.remember(c.peers.Chat(ch)), true
		}
	case *tg.PeerChannel:
		if ch, ok := ent.Channels()[v.ChannelID]; ok {
			return c.remember(c.peers.Channel(ch)), true
		}
	default:
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	pr, ok := c.seen[tdlibID(p)]
	return pr, ok
}

// nameOf gives the name of a peer with no network call, "?" when unknown.
func (c *Client) nameOf(ent peer.Entities, p tg.PeerClass) string {
	if p == nil {
		return "?"
	}
	if pr, ok := c.peerOf(ent, p); ok {
		return nick(pr)
	}
	return "?"
}

// errFlood : the breaker is open. Built at each read: the language can change
// during a session.
func errFlood() error { return errors.New(i18n.T("flood_wait_active")) }

// floodOK : false while the FLOOD_WAIT breaker is open.
func (c *Client) floodOK() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().After(c.floodUntil)
}

// floodTrip opens the breaker when err is a FLOOD_WAIT; one WARN per
// opening, not one per refused call.
func (c *Client) floodTrip(err error) {
	d, ok := tgerr.AsFloodWait(err)
	if !ok {
		return
	}
	now := time.Now()
	c.mu.Lock()
	first := now.After(c.floodUntil)
	if u := now.Add(d); u.After(c.floodUntil) {
		c.floodUntil = u
	}
	c.mu.Unlock()
	if first {
		c.PostNB(model.EvLog{Level: "WARN", Msg: i18n.T("flood_wait_tripped", d.Round(time.Second))})
	}
}

// resolvePeer : network lookup, refused during a FLOOD_WAIT.
func (c *Client) resolvePeer(ctx context.Context, p tg.PeerClass) (peers.Peer, error) {
	if !c.floodOK() {
		return nil, errFlood()
	}
	pr, err := c.peers.ResolvePeer(ctx, p)
	if err != nil {
		c.floodTrip(err)
		return nil, err
	}
	return c.remember(pr), nil
}

// peer types back the opaque handle of a chat. A handle from another backend
// gives nil: the gotd call it feeds fails on its own, no branch to add here.
func (c *Client) peer(chat *model.Chat) tg.InputPeerClass {
	p, _ := chat.Peer.(tg.InputPeerClass)
	return p
}

func (c *Client) chatOf(p peers.Peer) *model.Chat {
	ch := &model.Chat{ID: int64(p.TDLibPeerID()), Title: p.VisibleName(), Peer: p.InputPeer()}
	ch.Username, _ = p.Username()
	ch.PhotoLoc = peerPhoto(p)
	switch v := p.(type) {
	case peers.User:
		ch.Kind = model.ChatUser
	case peers.Chat:
		ch.Kind = model.ChatGroup
	case peers.Channel:
		ch.Kind, ch.Channel = model.ChatGroup, true
		if v.IsBroadcast() {
			ch.Kind = model.ChatChannel
		}
	}
	return ch
}
