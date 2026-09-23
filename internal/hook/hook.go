// Package hook reads hooks.toml and runs its hooks: programs started when an
// incoming message matches their filters. It knows nothing of the UI: the UI
// gives it a Msg and does with the output what the Reply of the hook says.
package hook

import (
	"errors"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
)

// Reply : what becomes of the output of a run.
type Reply string

const (
	ReplyNone    Reply = "none"    // ignored
	ReplySend    Reply = "send"    // sent in the chat of the message
	ReplyDraft   Reply = "draft"   // put in the input of that chat when it is empty
	ReplyDisplay Reply = "display" // system lines of the window of that chat
)

// The kinds of chat, as hooks.toml and TTYLOOM_CHAT_KIND write them.
const (
	KindPrivate = "private"
	KindGroup   = "group"
	KindChannel = "channel"
)

const (
	defTimeout = 10 * time.Second
	maxTimeout = 60 * time.Second
)

// Hook : one [[hook]] table of hooks.toml, checked. An empty filter takes
// every message.
type Hook struct {
	Name    string
	Net     string   // a network ("x:y"), or a module: every network of it
	Chats   []string // titles, case apart
	Kinds   []string // KindPrivate, KindGroup, KindChannel
	From    []string // sender ids in decimal; names where the name is the id
	Match   *regexp.Regexp
	Mention bool
	Argv    []string // cmd split on blanks, ~ expanded
	Timeout time.Duration
	Reply   Reply
}

// table : a [[hook]] table as written.
type table struct {
	Name    string   `toml:"name"`
	Net     string   `toml:"net"`
	Chats   []string `toml:"chats"`
	Kinds   []string `toml:"kinds"`
	From    []any    `toml:"from"` // text or integer: an id pastes bare
	Match   string   `toml:"match"`
	Mention bool     `toml:"mention"`
	Cmd     string   `toml:"cmd"`
	Timeout string   `toml:"timeout"`
	Reply   string   `toml:"reply"`
}

// keys : the keys of a table. Any other one leaves the hook out: a typo would
// give a hook that filters less than its author thinks.
var keys = []string{"name", "net", "chats", "kinds", "from", "match", "mention", "cmd", "timeout", "reply"}

// Load reads hooks.toml. A missing file is no hook and no error; err is a
// file that cannot be read or is not TOML. An unknown top-level key, and each
// table with a mistake, give one error in rejected, in file order; the other
// tables load.
func Load(path string) (hooks []Hook, rejected []error, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var top map[string]any
	if _, err := toml.Decode(string(b), &top); err != nil {
		return nil, nil, err
	}
	for _, k := range slices.Sorted(maps.Keys(top)) {
		if k != "hook" {
			rejected = append(rejected, i18n.Error("hooks_bad_key", k))
		}
	}
	var f struct {
		Hook []toml.Primitive `toml:"hook"`
	}
	md, err := toml.Decode(string(b), &f)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	for i, p := range f.Hook {
		h, err := check(md, p, i+1, seen)
		if err != nil {
			rejected = append(rejected, err)
			continue
		}
		seen[h.Name] = true
		hooks = append(hooks, h)
	}
	return hooks, rejected, nil
}

// check turns table rank of the file into a Hook, or says what is wrong
// with it. seen holds the names already taken.
func check(md toml.MetaData, p toml.Primitive, rank int, seen map[string]bool) (Hook, error) {
	who := "#" + strconv.Itoa(rank) // until the table gives a name
	var raw map[string]any
	if err := md.PrimitiveDecode(p, &raw); err != nil {
		return Hook{}, i18n.Error("hook_note", who, err)
	}
	if n, ok := raw["name"].(string); ok && n != "" {
		who = n
	}
	for _, k := range slices.Sorted(maps.Keys(raw)) {
		if !slices.Contains(keys, k) {
			return Hook{}, i18n.Error("hook_bad_key", who, k)
		}
	}
	var t table
	if err := md.PrimitiveDecode(p, &t); err != nil {
		return Hook{}, i18n.Error("hook_note", who, err)
	}
	switch {
	case t.Name == "":
		return Hook{}, i18n.Error("hook_no_name", who)
	case strings.ContainsFunc(t.Name, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }):
		return Hook{}, i18n.Error("hook_bad_name", who)
	case seen[t.Name]:
		return Hook{}, i18n.Error("hook_dup_name", who)
	}
	h := Hook{Name: t.Name, Net: t.Net, Chats: t.Chats, Mention: t.Mention, Argv: strings.Fields(t.Cmd),
		Timeout: defTimeout, Reply: ReplyNone}
	if len(h.Argv) == 0 {
		return Hook{}, i18n.Error("hook_no_cmd", who)
	}
	for i, a := range h.Argv {
		h.Argv[i] = config.Expand(a)
	}
	for _, k := range t.Kinds {
		if k != KindPrivate && k != KindGroup && k != KindChannel {
			return Hook{}, i18n.Error("hook_bad_kind", who, k)
		}
	}
	h.Kinds = t.Kinds
	for _, v := range t.From {
		switch x := v.(type) {
		case string:
			h.From = append(h.From, x)
		case int64:
			h.From = append(h.From, strconv.FormatInt(x, 10))
		default:
			return Hook{}, i18n.Error("hook_bad_from", who, v)
		}
	}
	if t.Match != "" {
		re, err := regexp.Compile(t.Match)
		if err != nil {
			return Hook{}, i18n.Error("hook_bad_match", who, err)
		}
		h.Match = re
	}
	if t.Timeout != "" {
		d, err := time.ParseDuration(t.Timeout)
		if err != nil || d <= 0 || d > maxTimeout {
			return Hook{}, i18n.Error("hook_bad_timeout", who, t.Timeout)
		}
		h.Timeout = d
	}
	if t.Reply != "" {
		h.Reply = Reply(t.Reply)
		if !slices.Contains([]Reply{ReplyNone, ReplySend, ReplyDraft, ReplyDisplay}, h.Reply) {
			return Hook{}, i18n.Error("hook_bad_reply", who, t.Reply)
		}
	}
	return h, nil
}
