package hook

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/govlog/ttyloom/internal/model"
)

// Msg : an incoming message as the filters see it.
type Msg struct {
	Net      string // network of the message
	Chat     string // title of its chat
	ChatID   int64
	Kind     string // KindPrivate, KindGroup, KindChannel; "" with no chat (/hooks test in window 0)
	From     string
	FromID   int64
	ID       int
	Text     string // as shown: an IRC action starts with "* nick "; empty for a media with no caption
	Mention  bool   // the message mentions me
	NameIsID bool   // the network knows a person by name (model.Caps.NameIsID)
}

// Captures : what the match of a hook found, for its environment.
type Captures struct {
	Whole  string
	Groups []string          // numbered groups, the first is group 1
	Named  map[string]string // named groups
}

// KindOf : the name of a chat kind in hooks.toml and TTYLOOM_CHAT_KIND.
func KindOf(k model.ChatKind) string {
	switch k {
	case model.ChatGroup:
		return KindGroup
	case model.ChatChannel:
		return KindChannel
	}
	return KindPrivate
}

// Match tells whether h fires on m: every filter set must take it.
func Match(h Hook, m Msg) (Captures, bool) {
	var c Captures
	switch {
	case h.Net != "" && h.Net != m.Net && h.Net != model.NetModule(m.Net):
		return c, false
	case len(h.Chats) > 0 && !slices.ContainsFunc(h.Chats, func(t string) bool { return strings.EqualFold(t, m.Chat) }):
		return c, false
	case len(h.Kinds) > 0 && !slices.Contains(h.Kinds, m.Kind):
		return c, false
	case len(h.From) > 0 && !slices.ContainsFunc(h.From, func(f string) bool { return sender(f, m) }):
		return c, false
	case h.Mention && !m.Mention:
		return c, false
	}
	if h.Match == nil {
		return c, true
	}
	sub := h.Match.FindStringSubmatch(m.Text)
	if sub == nil {
		return c, false
	}
	c.Whole, c.Groups = sub[0], sub[1:]
	for i, n := range h.Match.SubexpNames() {
		if n == "" {
			continue
		}
		if c.Named == nil {
			c.Named = map[string]string{}
		}
		c.Named[n] = sub[i]
	}
	return c, true
}

// sender : f, an entry of from, names the sender of m — its id, or its name,
// case apart, on a network where the name is the id.
func sender(f string, m Msg) bool {
	if m.NameIsID {
		return strings.EqualFold(f, m.From)
	}
	return f == strconv.FormatInt(m.FromID, 10)
}

// Env : the TTYLOOM_* variables of a run. An environment cannot carry a NUL:
// it is dropped from the values.
func Env(h Hook, m Msg, c Captures) []string {
	mention := "0"
	if m.Mention {
		mention = "1"
	}
	env := []string{
		"TTYLOOM_HOOK=" + h.Name,
		"TTYLOOM_NET=" + m.Net,
		"TTYLOOM_CHAT=" + m.Chat,
		"TTYLOOM_CHAT_ID=" + strconv.FormatInt(m.ChatID, 10),
		"TTYLOOM_CHAT_KIND=" + m.Kind,
		"TTYLOOM_FROM=" + m.From,
		"TTYLOOM_FROM_ID=" + strconv.FormatInt(m.FromID, 10),
		"TTYLOOM_MSG_ID=" + strconv.Itoa(m.ID),
		"TTYLOOM_TEXT=" + m.Text,
		"TTYLOOM_MENTION=" + mention,
		"TTYLOOM_MATCH=" + c.Whole,
	}
	for i, g := range c.Groups {
		env = append(env, "TTYLOOM_MATCH_"+strconv.Itoa(i+1)+"="+g)
	}
	for _, n := range slices.Sorted(maps.Keys(c.Named)) {
		env = append(env, "TTYLOOM_MATCH_"+strings.ToUpper(n)+"="+c.Named[n])
	}
	for i, v := range env {
		env[i] = strings.ReplaceAll(v, "\x00", "")
	}
	return env
}
