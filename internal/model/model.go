// Package model holds the types shared by tgc (Telegram) and ui.
package model

import "time"

type ChatKind int

const (
	ChatUser ChatKind = iota
	ChatGroup
	ChatChannel
)

// NetTelegram : name of the Telegram network, key of the backend maps.
const NetTelegram = "telegram"

// NetDiscord : name of the Discord network, same key of the backend maps.
const NetDiscord = "discord"

// ChatKey identifies a chat across networks: Discord snowflakes and TDLib
// ids can collide, the net disambiguates.
type ChatKey struct {
	Net string
	ID  int64
}

// Peer, PhotoLoc, Media.Loc and Msg.FromPhoto: opaque backend handles — the
// UI carries them, only the owning backend reads them.
type Chat struct {
	// Net : network that owns this chat; stamped by the UI dispatch, never
	// empty afterwards.
	Net      string
	ID       int64 // TDLib id, unique across all kinds
	Kind     ChatKind
	Title    string
	Username string
	Peer     any
	// Channel : peer backed by a Telegram channel — broadcast AND megagroup
	// (a megagroup keeps Kind ChatGroup). Its message ids never show up in a
	// global delete update.
	Channel         bool
	Unread          int
	ReadInboxMaxID  int
	ReadOutboxMaxID int // last of my messages read by the chat (✓✓ tick)
	Pinned          bool
	// Reactions : reactions allowed in this chat. nil = every reaction of the
	// account (ChatReactionsAll, or full record never read); empty non-nil =
	// none (ChatReactionsNone).
	Reactions  []string
	TopMessage int
	LastDate   time.Time
	PhotoLoc   any // profile photo (small size), nil with no photo
}

func (c *Chat) Key() ChatKey { return ChatKey{Net: c.Net, ID: c.ID} }

type MediaKind int

const (
	MediaPhoto MediaKind = iota
	MediaSticker
	MediaGIF
	MediaVideo
	MediaVoice
	MediaAudio
	MediaFile
	MediaOther  // geo, contact, poll…: label only
	MediaAvatar // profile photo: 2 cells x 1 line, never inside a message
	// MediaWebPage : link preview. Added at the end of the list — the cache keeps
	// Kind as an integer, a new number would change the meaning of written messages.
	MediaWebPage
	MediaMap // location, live position, venue: OSM map (same rule: end of the list)
)

type MediaState int

const (
	MediaNone MediaState = iota
	MediaLoading
	MediaReady
	MediaFailed
)

type Media struct {
	Kind      MediaKind
	Label     string // "[photo 640x480 · 42 KB]"
	W, H      int
	Size      int64
	Duration  float64
	Name      string // link preview: the description
	Mime      string
	Ext       string
	Loc       any     // nil for MediaOther
	Emoji     string  // sticker
	URL       string  // link preview: the page ("o" opens it, not the file)
	Animated  bool    // .tgs / webm sticker: text only
	Lat, Long float64 // MediaMap : coordinates of the point

	// Filled by the UI from the events.
	Path   string
	State  MediaState
	Err    string
	Frames [][]byte // PNG; 1 frame if still
	FrameW int
	FrameH int
	Delay  time.Duration
	Frame  int
	Next   time.Time // next frame (animation)
	Paused bool      // video: play stopped (keys l and s)
	// Want : frames asked of the current decoding; > len(Frames) while a video
	// decodes step by step (the "decoding 42 %" label).
	Want     int
	KittyID  uint32 // current image in the terminal (0 = not sent)
	KittyAlt uint32 // second animation buffer: the next frame goes there
	// CellW/CellH : cell size used to decode the frames. Different from the cell
	// size of the terminal (font zoom) = stale frames, to decode again.
	CellW int
	CellH int
}

func (m *Media) Previewable() bool {
	switch m.Kind {
	case MediaPhoto, MediaGIF, MediaVideo, MediaAvatar, MediaWebPage:
		return m.Loc != nil
	case MediaMap: // no Telegram Loc: the map comes from OSM, not from the Telegram network
		return true
	case MediaSticker:
		return m.Loc != nil && !m.Animated
	}
	return false
}

type Quote struct {
	ID   int
	From string
	Text string
}

type Reaction struct {
	Emoji string
	Count int
	Mine  bool
}

type SpanKind int

const (
	SpanBold SpanKind = iota + 1
	SpanItalic
	SpanUnderline
	SpanStrike
	SpanCode
	SpanPre // Lang: language of the block
	SpanSpoiler
	SpanQuote   // blockquote
	SpanURL     // URL: the target (render still SafeURL-filters it)
	SpanMention // UserID: the peer when known (mention by id), 0 otherwise
)

// Span : one style range of Msg.Text, offsets in runes [Start, End).
type Span struct {
	Start, End int
	Kind       SpanKind
	URL        string
	UserID     int64
	Lang       string
}

type SegKind int

const (
	SegPlain SegKind = iota
	SegPre           // Lang
)

// Seg : one block of a styled send (fences, /me, Ctrl+B/I/U runs), in
// document order. Bold/Italic/Underline stack on a plain segment.
type Seg struct {
	Text                    string
	Kind                    SegKind
	Lang                    string
	Bold, Italic, Underline bool
}

// SegBreak : a line break sits between two segments only around a fence;
// the styled runs of one line (Ctrl+B/I/U) follow one another.
func SegBreak(segs []Seg, i int) bool {
	return i > 0 && (segs[i-1].Kind == SegPre || segs[i].Kind == SegPre)
}

type Msg struct {
	// Net : network that owns this message; stamped by the UI dispatch, never
	// empty afterwards.
	Net       string
	ID        int
	TmpID     int64 // local message waiting to be sent
	ChatID    int64
	ChatLabel string // chat title, shown by the aggregated view
	Date      time.Time
	From      string
	FromID    int64
	FromPhoto any // profile photo of the author, nil with no photo
	Out       bool
	Text      string
	Entities  []Span
	Media     *Media
	Reply     *Quote
	FwdFrom   string
	Edited    bool
	Deleted   bool
	Reactions []Reaction
	Service   string // service message ("alice joined"); Text is ignored
	Pending   bool
	Err       string
}

// Key : the chat the message belongs to, not the message itself.
func (m *Msg) Key() ChatKey { return ChatKey{Net: m.Net, ID: m.ChatID} }

// Event : events posted by tgc (and by the UI for its own goroutines).
type Event any

// Envelope tags a backend event with the network it came from. Backends
// post bare Events; the per-backend forwarder in main wraps them.
type Envelope struct {
	Net string
	Ev  Event
}

type EvAuthPrompt struct {
	Question string
	Secret   bool
	Reply    chan string
}

// EvQR : QR login token to show. The URL is built locally
// (tg://login?token=…): it is worth a session, so it is never logged.
type EvQR struct {
	URL     string
	Expires time.Time
}

// EvQRDone : the QR has no reason to stay (scanned, dropped, failed) —
// the overlay and the prompt beside it close.
type EvQRDone struct{}

type EvReady struct {
	SelfID   int64
	SelfName string
	Bot      bool
}
type EvConnected struct{}
type EvDisconnected struct{}

// EvStopped : the Run of the backend is over. Err is empty when it was asked
// to end (logout, quit), the error otherwise — the network is gone either
// way, until the next /<net> login.
type EvStopped struct{ Err string }
type EvLog struct{ Level, Msg string }
type EvDialogs struct {
	Chats []*Chat
	Err   string
	// Complete : the list is the whole account; chats of this network missing
	// from it are gone.
	Complete bool
}
type EvNewMessage struct {
	Msg  Msg
	Chat *Chat
}
type EvEditMessage struct{ Msg Msg }
type EvDeleted struct {
	ChatID int64 // 0 = unknown, look everywhere
	IDs    []int
}
type EvTyping struct {
	ChatID int64
	Who    string
}
type EvHistory struct {
	ChatID int64
	Msgs   []Msg // from the oldest to the newest
	Older  bool  // loading upwards
	Since  bool  // sync step: messages newer than a known id
	Done   bool  // nothing left before
	// Around : page centred on AroundID (precise jump) — neither the top nor the
	// bottom of the window, it slots in.
	Around   bool
	AroundID int
	Err      string
}
type EvSearch struct {
	ChatID int64
	Query  string
	Msgs   []Msg // from the oldest to the newest
	Err    string
}

// SearchHit : one global search result. A Chat and not an id: it carries the
// Peer, the only way to open the chat, and the UI replaces it with the pointer
// it already knows when there is one.
type SearchHit struct {
	Chat  *Chat
	MsgID int
	Date  time.Time
	From  string
	Text  string
}

// EvSearchGlobal : answer of messages.searchGlobal (Ctrl+F twice). Query is
// the query sent: the UI drops the answers of a query it gave up.
type EvSearchGlobal struct {
	Query string
	Hits  []SearchHit
	Err   string
}

// EvContacts : contacts of the account (contacts.getContacts), asked only
// once per session, when "new chat" opens for the first time.
type EvContacts struct {
	Peers []*Chat
	Err   string
}

// EvContactsFound : answer of contacts.search (the "on Telegram" section of
// "new chat"). Query is the query sent: the UI drops the answers of a query
// it gave up.
type EvContactsFound struct {
	Query string
	Peers []*Chat
	Err   string
}

type EvEdited struct {
	ChatID int64
	ID     int
	Err    string
}
type EvSent struct {
	ChatID int64
	TmpID  int64
	ID     int
	Err    string
}
type EvChat struct {
	Query string
	Chat  *Chat
	Err   string
}

// EvUpload : progress of a file upload, for the status bar.
type EvUpload struct{ Text string }
type EvDownloaded struct {
	Media *Media
	Path  string
	Err   string
}
type EvReactions struct {
	ChatID    int64
	ID        int
	Reactions []Reaction
	// Refetch : state that comes from a re-read of the message, not from an
	// update. The server can answer before it applies the reaction: an empty
	// list from there never wipes the one already shown.
	Refetch bool
}

// EvReactionsList : reactions the account can use (global Telegram list),
// posted at connection time.
type EvReactionsList struct{ Emojis []string }

// EvChatReactions : reactions allowed in a chat. Emojis nil and None false
// = no restriction (every reaction of the account).
type EvChatReactions struct {
	ChatID int64
	Emojis []string
	None   bool
}

// EvWho : answer of WhoRead (React false) or WhoReacted (React true) — one
// line for the hover popup of the tick or of the reactions.
type EvWho struct {
	ChatID int64
	ID     int
	React  bool
	Text   string
}

// EvReactionFailed : reaction refused by the server. Reason is the text to
// show in the chat window, the log is not enough.
type EvReactionFailed struct {
	ChatID int64
	ID     int
	Emoji  string
	Reason string
}
type EvInfo struct {
	ChatID int64
	ID     int
	Lines  []string
}
type EvReadOutbox struct {
	ChatID int64
	MaxID  int
}
type EvReadInbox struct {
	ChatID int64
	MaxID  int
	// Unread : StillUnreadCount of the server, valid only if HasUnread.
	Unread    int
	HasUnread bool
}
type EvWhois struct {
	ChatID int64
	Lines  []string
	Err    string
}
type EvPresence struct {
	UserID int64 // user id = TDLib id of the private chat
	Status string
}

// Participant : one line of the member box (F3). Text is already cleaned and
// ready to show; an empty Query = the line is not clickable (a counter, a
// state message).
type Participant struct {
	Text   string
	Query  string // /query on click: @username, or user id when there is none
	Online bool   // shown in the accent colour
}

type EvParticipants struct {
	ChatID int64
	Lines  []Participant
	Err    string
}

// Gif : one result of the GIF search (Ctrl+G). Preview is what the box shows,
// downloaded and decoded like any media; Send is the opaque handle the owning
// backend reads to post it (an inline result on Telegram, a page URL on
// Discord).
type Gif struct {
	Preview *Media
	Send    any
}

// EvGifs : answer of SearchGifs. Query is the query sent: the UI drops the
// answers of a query it gave up.
type EvGifs struct {
	Query string
	Gifs  []Gif
	Err   string
}

// EvChatGone : chat left or blocked — the UI drops the entry from the
// sidebar, closes the bound windows and wipes its cached history.
type EvChatGone struct{ ChatID int64 }

// DefaultReactions : fallback when the backend cannot give the list of the
// reactions of the account (call failed or slow) — the most common ones, taken
// by every chat with no restriction. The UI starts from them, otherwise
// nothing could be reacted to before the answer.
var DefaultReactions = []string{"👍", "👎", "❤", "🔥", "🥰", "👏", "😁", "🤔", "🤯", "😱", "🤬", "😢", "🎉", "🤩", "🙏", "👌", "😍", "💯", "🤣", "😭"}
