package irc

import (
	"hash/fnv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ergochat/irc-go/ircfmt"

	"github.com/govlog/ttyloom/internal/model"
)

// casefold : the comparison form of a nick or a channel name.
// ponytail: plain ASCII lower case, not the rfc1459 mapping ({}| = []\);
// every network in use today runs ascii casemapping.
func casefold(s string) string { return strings.ToLower(s) }

// isChannel : the four channel prefixes of RFC 2811.
func isChannel(name string) bool {
	return name != "" && strings.ContainsRune("#&!+", rune(name[0]))
}

// chatID : the id of a channel or a nick — FNV-64a of its folded name, sign
// bit cleared, never 0. IRC has no ids: the name is the identity, and a
// stable hash keeps the disk cache and the windows across sessions.
func chatID(name string) int64 {
	h := fnv.New64a()
	h.Write([]byte(casefold(name)))
	id := int64(h.Sum64() &^ (1 << 63))
	if id == 0 {
		id = 1
	}
	return id
}

// chatOf builds the chat of a channel or a nick.
func chatOf(name string) *model.Chat {
	kind := model.ChatUser
	if isChannel(name) {
		kind = model.ChatGroup
	}
	return &model.Chat{ID: chatID(name), Kind: kind, Title: name, Peer: peer{Name: name}}
}

// nameOf : the channel or nick a chat stands for; the title when the peer is
// not ours (a chat of another network never reaches here, the UI routes by
// net).
func nameOf(c *model.Chat) string {
	if p, ok := c.Peer.(peer); ok {
		return p.Name
	}
	return c.Title
}

// idGen : message ids. IRC gives none; ms since the epoch, strictly
// increasing, so that "older than" keeps its meaning in the cache and two
// lines of the same ms do not collide.
type idGen struct {
	mu   sync.Mutex
	last int
}

func (g *idGen) next() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	id := int(time.Now().UnixMilli())
	if id <= g.last {
		id = g.last + 1
	}
	g.last = id
	return id
}

// spansOf strips the mIRC formatting codes of text and gives the styles back
// as spans (rune offsets). Colours are dropped: the theme owns the colours.
func spansOf(text string) (string, []model.Span) {
	if !strings.ContainsAny(text, "\x02\x03\x04\x11\x16\x1d\x1e\x1f\x0f") {
		return text, nil
	}
	var sb strings.Builder
	var spans []model.Span
	pos := 0
	for _, f := range ircfmt.Split(text) {
		n := utf8.RuneCountInString(f.Content)
		sb.WriteString(f.Content)
		for _, st := range []struct {
			on   bool
			kind model.SpanKind
		}{{f.Bold, model.SpanBold}, {f.Italic, model.SpanItalic}, {f.Underline, model.SpanUnderline},
			{f.Strikethrough, model.SpanStrike}, {f.Monospace, model.SpanCode}} {
			if st.on && n > 0 {
				spans = append(spans, model.Span{Start: pos, End: pos + n, Kind: st.kind})
			}
		}
		pos += n
	}
	return sb.String(), spans
}

// styled renders the segments of a styled send into one text with mIRC
// codes: bold \x02, italic \x1d, underline \x1f, a fence as bare lines. The
// caller splits it into PRIVMSGs.
func styled(segs []model.Seg) string {
	var sb strings.Builder
	for i, s := range segs {
		if model.SegBreak(segs, i) {
			sb.WriteByte('\n')
		}
		if s.Kind == model.SegPre {
			sb.WriteString(s.Text)
			continue
		}
		open := ""
		if s.Bold {
			open += "\x02"
		}
		if s.Italic {
			open += "\x1d"
		}
		if s.Underline {
			open += "\x1f"
		}
		if open == "" {
			sb.WriteString(s.Text)
			continue
		}
		// Reset at the end of the run, then the codes again on the next line:
		// a code does not survive a line break on the wire.
		for j, line := range strings.Split(s.Text, "\n") {
			if j > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(open + line + "\x0f")
		}
	}
	return sb.String()
}

// maxLine : bytes of text per PRIVMSG. 512 minus the prefix the server adds
// (":nick!user@host PRIVMSG #chan :") leaves about 400 for the longest
// user@host; cutting there never hits the server limit.
const maxLine = 400

// splitLines cuts text into PRIVMSG-sized pieces: one per line, a line longer
// than max cut on the last blank before the limit (a rune boundary otherwise).
// Empty lines are dropped — an empty PRIVMSG is a protocol error.
func splitLines(text string, max int) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		for len(line) > max {
			cut := max
			for cut > 0 && !utf8.RuneStart(line[cut]) {
				cut--
			}
			if i := strings.LastIndexByte(line[:cut], ' '); i > max/2 {
				cut = i
			}
			if cut == 0 {
				cut = max
			}
			out = append(out, strings.TrimRight(line[:cut], " "))
			line = strings.TrimLeft(line[cut:], " ")
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// serverTime : the server-time tag when the server sends one, now otherwise.
func serverTime(ok bool, tag string) time.Time {
	if ok {
		if t, err := time.Parse(time.RFC3339Nano, tag); err == nil {
			return t
		}
	}
	return time.Now()
}
