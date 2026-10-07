package tgc

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"

	"github.com/govlog/ttyloom/internal/model"
)

// convert turns a raw message into a model.Msg + chat. ent carries the
// entities of the batch (history, search, update); a non-nil chat = chat
// already known, no peer is resolved by network. ok=false when it stays unknown.
func (c *Client) convert(ctx context.Context, mc tg.MessageClass, ent peer.Entities, chat *model.Chat) (model.Msg, *model.Chat, bool) {
	var peerID, fromID tg.PeerClass
	var id, date int
	var out bool
	switch v := mc.(type) {
	case *tg.Message:
		peerID, fromID, id, date, out = v.PeerID, v.FromID, v.ID, v.Date, v.Out
	case *tg.MessageService:
		peerID, fromID, id, date, out = v.PeerID, v.FromID, v.ID, v.Date, v.Out
	default:
		return model.Msg{}, nil, false
	}
	if chat == nil {
		cp, ok := c.peerOf(ent, peerID)
		if !ok { // update of a chat never seen: the only network lookup left
			var err error
			if cp, err = c.resolvePeer(ctx, peerID); err != nil {
				return model.Msg{}, nil, false
			}
		}
		chat = c.chatOf(cp)
	}
	m := model.Msg{ID: id, ChatID: chat.ID, Date: unixTime(date), Out: out}
	if v, ok := mc.(*tg.Message); ok {
		m.Mentioned = v.Mentioned && v.MediaUnread
	}
	if fromID != nil {
		if fp, ok := c.peerOf(ent, fromID); ok {
			m.From, m.FromID = nick(fp), int64(fp.TDLibPeerID())
			m.FromPhoto = peerPhoto(fp)
		}
	}
	if m.From == "" {
		self := c.me.Load()
		switch {
		case out && self != nil:
			me := c.peers.User(self)
			m.From, m.FromID, m.FromPhoto = nick(me), self.ID, peerPhoto(me)
		case fromID == nil && chat.Kind == model.ChatUser:
			// Private chat: from_id missing on the MTProto side (the incoming
			// sender is the peer for sure). Avatar = the one of the chat, already resolved.
			m.From, m.FromID, m.FromPhoto = chat.Title, chat.ID, chat.PhotoLoc
		case fromID == nil:
			// ponytail: channel post, FromID stays 0 (nick colour): no avatar,
			// only the gutter. The avatar of the channel is already in the
			// sidebar.
			m.From = chat.Title
		default:
			m.From = "?" // sender missing from the entities: no network call
		}
	}
	switch v := mc.(type) {
	case *tg.MessageService:
		m.Service = m.From + " " + describeAction(v.Action)
	case *tg.Message:
		m.Text, m.Entities, m.Edited = v.Message, spansOf(v.Message, v.Entities), v.EditDate != 0
		if a, ok := v.GetPostAuthor(); ok && fromID == nil {
			m.From = a
		}
		m.Media = mediaOf(v.Media)
		if f, ok := v.GetFwdFrom(); ok {
			switch {
			case f.FromName != "":
				m.FwdFrom = f.FromName
			case f.FromID != nil:
				m.FwdFrom = c.nameOf(ent, f.FromID)
			}
		}
		if r, ok := v.GetReplyTo(); ok {
			if h, ok := r.(*tg.MessageReplyHeader); ok && h.ReplyToMsgID != 0 {
				m.Reply = &model.Quote{ID: h.ReplyToMsgID, Text: oneLine(h.QuoteText)}
				// A reply to a message of another chat: its id means nothing in
				// this one. The quote is the header alone, named after that chat
				// — no jump, and never filled by a read of the id here.
				if p, ok := h.GetReplyToPeerID(); ok && tdlibID(p) != chat.ID {
					q := m.Reply
					q.ID, q.From = 0, c.nameOf(ent, p)
					if q.Text == "" {
						q.Text = "…"
						if md := mediaOf(h.ReplyMedia); md != nil {
							q.Text = md.Label
						}
					}
				}
			}
		}
		if rs, ok := v.GetReactions(); ok {
			m.Reactions = reactionsOf(rs.Results)
		}
	}
	return m, chat, true
}

// fillReplies gets in one request the quoted messages whose text we lack.
func (c *Client) fillReplies(ctx context.Context, chat *model.Chat, msgs []*model.Msg) {
	want := map[int][]*model.Msg{}
	var ids []tg.InputMessageClass
	for _, m := range msgs {
		if m.Reply == nil || m.Reply.Text != "" {
			continue
		}
		if _, seen := want[m.Reply.ID]; !seen {
			ids = append(ids, &tg.InputMessageID{ID: m.Reply.ID})
		}
		want[m.Reply.ID] = append(want[m.Reply.ID], m)
	}
	if len(ids) == 0 || !c.floodOK() {
		return
	}
	res, err := c.getMessages(ctx, chat, ids)
	if err != nil {
		return
	}
	mod, ok := res.AsModified()
	if !ok {
		return
	}
	ent := c.applyResult(ctx, mod.GetUsers(), mod.GetChats())
	for _, mc := range mod.GetMessages() {
		q, _, ok := c.convert(ctx, mc, ent, chat)
		if !ok {
			continue
		}
		for _, m := range want[q.ID] {
			m.Reply.From, m.Reply.Text = q.From, oneLine(q.Summary())
		}
	}
}

// getMessages reads messages of chat by id: channels.getMessages in a
// channel or a supergroup, messages.getMessages elsewhere. A FLOOD_WAIT opens
// the breaker.
func (c *Client) getMessages(ctx context.Context, chat *model.Chat, ids []tg.InputMessageClass) (tg.MessagesMessagesClass, error) {
	var res tg.MessagesMessagesClass
	var err error
	if ch, ok := c.peer(chat).(*tg.InputPeerChannel); ok {
		res, err = c.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: inputChannel(ch), ID: ids})
	} else {
		res, err = c.api.MessagesGetMessages(ctx, ids)
	}
	c.floodTrip(err)
	return res, err
}

// applyResult applies the users and chats of an answer to the peers, and
// gives their entities, kept for the names to come.
func (c *Client) applyResult(ctx context.Context, users []tg.UserClass, chats []tg.ChatClass) peer.Entities {
	c.peers.Apply(ctx, users, chats)
	ent := peer.NewEntities(tg.UserClassArray(users).UserToMap(),
		tg.ChatClassArray(chats).ChatToMap(), tg.ChatClassArray(chats).ChannelToMap())
	c.rememberAll(ent)
	return ent
}

// applyBatch applies the entities of an element of an iterator once per
// batch: every element of a batch carries the entities of the whole batch.
// last is the batch applied before.
func (c *Client) applyBatch(ctx context.Context, ent peer.Entities, last *peer.Entities) {
	if maps.Equal(ent.Users(), last.Users()) && maps.Equal(ent.Chats(), last.Chats()) &&
		maps.Equal(ent.Channels(), last.Channels()) {
		return
	}
	c.applyEntities(ctx, ent)
	*last = ent
}

func (c *Client) applyEntities(ctx context.Context, ent peer.Entities) {
	c.rememberAll(ent)
	var users []tg.UserClass
	for _, u := range ent.Users() {
		users = append(users, u)
	}
	var chats []tg.ChatClass
	for _, ch := range ent.Chats() {
		chats = append(chats, ch)
	}
	for _, ch := range ent.Channels() {
		chats = append(chats, ch)
	}
	c.peers.Apply(ctx, users, chats)
}

// --- asynchronous requests ---

func (c *Client) LoadDialogs(ctx context.Context) {
	go func() {
		defer c.Guard("LoadDialogs", func(err string) { c.Post(model.EvDialogs{Err: err}) })
		started := time.Now()
		elems, err := query.GetDialogs(c.api).BatchSize(100).Collect(ctx)
		if err != nil {
			c.floodTrip(err)
			c.Post(model.EvDialogs{Err: err.Error()})
			return
		}
		// The archive (folder 1) is a list of its own: a chat put away there
		// is still one of the account, and nothing else would ever show it.
		archived, err := query.GetDialogs(c.api).BatchSize(100).FolderID(1).Collect(ctx)
		if err != nil {
			c.floodTrip(err)
			c.Post(model.EvDialogs{Err: err.Error()})
			return
		}
		elems = append(elems, archived...)
		var chats []*model.Chat
		// complete : the two lists are the whole account, so the UI may drop
		// the chats missing from them (left or deleted on another device). A
		// dialog dropped here makes it false: its chat would look gone.
		complete := true
		var last peer.Entities
		for _, e := range elems {
			d, ok := e.Dialog.(*tg.Dialog)
			if !ok {
				continue // the archive entry (tg.DialogFolder), read through FolderID(1)
			}
			c.applyBatch(ctx, e.Entities, &last)
			// From the entities of the batch: FromInputPeer would do one
			// users.getUsers per dialog (the cache of the peer manager is a no-op).
			p, ok := c.peerOf(e.Entities, d.Peer)
			if !ok {
				complete = false
				continue
			}
			ch := c.chatOf(p)
			ch.Unread, ch.ReadInboxMaxID, ch.TopMessage = d.UnreadCount, d.ReadInboxMaxID, d.TopMessage
			ch.ReadOutboxMaxID = d.ReadOutboxMaxID
			ch.UnreadReactions = d.UnreadReactionsCount > 0
			ch.UnreadMentions = d.UnreadMentionsCount > 0
			ch.Pinned = d.Pinned
			if e.Last != nil {
				ch.LastDate = unixTime(e.Last.GetDate())
			}
			chats = append(chats, ch)
		}
		// An empty list never prunes: it would wipe every cached chat.
		c.Post(model.EvDialogs{Chats: chats, Complete: complete && len(chats) > 0, Started: started})
	}()
}

// LoadHistory loads limit messages before beforeID (0 = the newest ones).
func (c *Client) LoadHistory(ctx context.Context, chat *model.Chat, beforeID, limit int) {
	snapshot := *chat // The UI can update chat fields while the request runs.
	chat = &snapshot
	go c.history(ctx, chat, beforeID, 0, limit, false)
}

// LoadHistoryAround loads a page of limit messages centred on id (half
// before, half after): a precise jump must not end on its target. Direct
// call, the gotd builder does not expose add_offset.
func (c *Client) LoadHistoryAround(ctx context.Context, chat *model.Chat, id, limit int) {
	snapshot := *chat // The UI can update chat fields while the request runs.
	chat = &snapshot
	go func() {
		ev := model.EvHistory{Started: time.Now(), ChatID: chat.ID, Around: true, AroundID: id}
		defer c.Guard("LoadHistoryAround", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		if !c.floodOK() {
			ev.Err = errFlood().Error()
			c.Post(ev)
			return
		}
		res, err := c.api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer: c.peer(chat), OffsetID: id, AddOffset: -limit / 2, Limit: limit})
		if err != nil {
			c.floodTrip(err)
			ev.Err = err.Error()
			c.Post(ev)
			return
		}
		mod, ok := res.AsModified()
		if !ok {
			c.Post(ev) // messagesNotModified: empty page, the appointment closes
			return
		}
		ent := c.applyResult(ctx, mod.GetUsers(), mod.GetChats())
		var msgs []model.Msg
		for _, mc := range mod.GetMessages() {
			if m, _, ok := c.convert(ctx, mc, ent, chat); ok {
				msgs = append(msgs, m)
			}
		}
		ev.Msgs = c.finishPage(ctx, chat, msgs)
		c.Post(ev)
	}()
}

// finishPage fills in the quoted messages of a page and gives it oldest
// first: the server sends the newest first.
func (c *Client) finishPage(ctx context.Context, chat *model.Chat, msgs []model.Msg) []model.Msg {
	ptrs := make([]*model.Msg, len(msgs))
	for i := range msgs {
		ptrs[i] = &msgs[i]
	}
	c.fillReplies(ctx, chat, ptrs)
	slices.Reverse(msgs)
	return msgs
}

// each walks it from the newest message to the oldest and gives each one to
// f with the entities of its batch, until f says false. It is the one path of
// the history, the sync, /search and the media browser: the FLOOD_WAIT
// breaker sits here, refused before the first request and opened by a
// FLOOD_WAIT on any of them.
func (c *Client) each(ctx context.Context, it *messages.Iterator, f func(mc tg.MessageClass, ents peer.Entities) bool) error {
	if !c.floodOK() {
		return errFlood()
	}
	var last peer.Entities
	for it.Next(ctx) {
		e := it.Value()
		c.applyBatch(ctx, e.Entities, &last)
		mc, ok := e.Msg.(tg.MessageClass) // Elem.Msg: NotEmptyMessage subset
		if ok && !f(mc, e.Entities) {
			break
		}
	}
	err := it.Err()
	c.floodTrip(err)
	return err
}

// page reads it up to limit messages, down to the first id at or below minID
// (0: no floor), and gives them finished (finishPage).
func (c *Client) page(ctx context.Context, chat *model.Chat, it *messages.Iterator, limit, minID int) ([]model.Msg, error) {
	var msgs []model.Msg
	err := c.each(ctx, it, func(mc tg.MessageClass, ents peer.Entities) bool {
		// GetHistory does not expose min_id: the read goes from the newest to the
		// oldest, the first message already known stops everything.
		if mc.GetID() <= minID {
			return false
		}
		if m, _, ok := c.convert(ctx, mc, ents, chat); ok {
			msgs = append(msgs, m)
		}
		return len(msgs) < limit
	})
	if err != nil {
		return nil, err
	}
	return c.finishPage(ctx, chat, msgs), nil
}

// LoadHistorySince loads up to limit messages with an id above minID (sync of
// a chat at start). minID zero: the recent history.
func (c *Client) LoadHistorySince(ctx context.Context, chat *model.Chat, minID, limit int) {
	snapshot := *chat // The UI can update chat fields while the request runs.
	chat = &snapshot
	go c.history(ctx, chat, 0, minID, limit, true)
}

// history : common body of the two loads. It runs in its own goroutine.
func (c *Client) history(ctx context.Context, chat *model.Chat, beforeID, minID, limit int, since bool) {
	ev := model.EvHistory{Started: time.Now(), ChatID: chat.ID, Older: beforeID > 0, Since: since}
	defer c.Guard("history", func(err string) {
		ev.Err = err
		c.Post(ev)
	})
	// The server caps the batch at 100; above that, the gotd iterator takes the
	// short batch for the end of the history and stops (lastBatch).
	q := query.Messages(c.api).GetHistory(c.peer(chat)).BatchSize(min(limit, 100))
	if beforeID > 0 {
		q = q.OffsetID(beforeID)
	}
	msgs, err := c.page(ctx, chat, q.Iter(), limit, minID)
	if err != nil {
		ev.Err = err.Error()
		c.Post(ev)
		return
	}
	ev.Msgs = msgs
	// A short sync proves nothing about the older messages: Done (w.Full) stays
	// false, otherwise scrolling up in the window would be blocked.
	ev.Done = !since && len(msgs) < limit
	c.Post(ev)
}

// Search runs messages.search in a chat, limit results at most, from the
// oldest to the newest. The default filter of the builder
// (InputMessagesFilterEmpty) takes every kind of message.
func (c *Client) Search(ctx context.Context, chat *model.Chat, q string, limit int) {
	snapshot := *chat // The UI can update chat fields while the request runs.
	chat = &snapshot
	go func() {
		ev := model.EvSearch{Started: time.Now(), ChatID: chat.ID, Query: q}
		defer c.Guard("Search", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		it := query.Messages(c.api).Search(c.peer(chat)).Q(q).BatchSize(min(limit, 100)).Iter()
		msgs, err := c.page(ctx, chat, it, limit, 0)
		if err != nil {
			ev.Err = err.Error()
			c.Post(ev)
			return
		}
		ev.Msgs = msgs
		c.Post(ev)
	}()
}

// SearchGlobal runs messages.searchGlobal over every chat, limit results at
// most (from the newest to the oldest, server order). The names come from the
// entities of the answer: a message whose chat is not there is dropped rather
// than resolved through the network (one result per unknown chat = as many
// calls, so a sure FLOOD_WAIT).
func (c *Client) SearchGlobal(ctx context.Context, q string, limit int) {
	go func() {
		ev := model.EvSearchGlobal{Query: q}
		defer c.Guard("SearchGlobal", func(err string) {
			ev.Err = err
			c.Post(ev)
		})
		if !c.floodOK() {
			ev.Err = errFlood().Error()
			c.Post(ev)
			return
		}
		it := query.Messages(c.api).SearchGlobal().Q(q).BatchSize(min(limit, 100)).Iter()
		var last peer.Entities
		// Walk cap: three batches at most. Without it, a request whose results
		// are all dropped would page over the whole account.
		for seen := 0; len(ev.Hits) < limit && seen < 3*limit && it.Next(ctx); seen++ {
			e := it.Value()
			c.applyBatch(ctx, e.Entities, &last)
			mc, ok := e.Msg.(tg.MessageClass) // Elem.Msg: NotEmptyMessage subset
			if !ok {
				continue
			}
			if _, ok := c.peerOf(e.Entities, e.Msg.GetPeerID()); !ok {
				continue // unknown chat: convert() would go and resolve it through the network
			}
			m, chat, ok := c.convert(ctx, mc, e.Entities, nil)
			if !ok {
				continue // nothing to show and nothing to open
			}
			ev.Hits = append(ev.Hits, model.SearchHit{Chat: chat, MsgID: m.ID, Date: m.Date, From: m.From, Text: m.Summary()})
		}
		if err := it.Err(); err != nil {
			c.floodTrip(err)
			ev.Err = err.Error()
		}
		c.Post(ev)
	}()
}
