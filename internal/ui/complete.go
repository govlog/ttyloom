package ui

import (
	"os"
	"path"
	"slices"
	"strings"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/spell"
)

// complSource : kind of Tab completion for the command being typed.
type complSource int

const (
	complCommands     complSource = iota // start of the line
	complChats                           // /query /q /msg /m /join /j and plain text
	complWindows                         // /win /w /window : new close list + windows
	complWindowNewArg                    // "hide" after /window new
	complSetKey                          // /set <key>
	complSetValue                        // /set <key> <value>
	complTheme                           // /theme (list + names)
	complHelp                            // /help <topic>
	complNet                             // /net <network>
	complFold                            // /fold <section>
	complPath                            // /send <path>: files of the disk
	complNetCmd                          // /telegram /discord <status|login|logout>
	complLog                             // /log <on|off>
	complNone                            // /open /history …
)

// complContext gives the completion source and the line "tail" to complete
// (everything after the command, for the candidates of several words). cursor
// is a rune index; only the part of the line before the cursor is read.
func complContext(line string, cursor int) (src complSource, tail string, setKey string) {
	r := []rune(line)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(r) {
		cursor = len(r)
	}
	s := string(r[:cursor])

	before, rest, hasSpace := strings.Cut(s, " ")
	if !hasSpace {
		if strings.HasPrefix(s, "/") {
			return complCommands, s, ""
		}
		return complChats, s, "" // last (and only) word
	}
	if !strings.HasPrefix(before, "/") {
		return complChats, s[strings.LastIndexAny(s, " \n")+1:], ""
	}
	name := resolveCommand(strings.ToLower(before[1:]))
	switch name {
	case "query", "msg", "join", "whois", "rename", "unrename":
		// After the target, the rest is free text (message, new name).
		if (name == "msg" || name == "rename") && strings.Contains(rest, " ") {
			return complNone, "", ""
		}
		return complChats, rest, ""
	case "window":
		if !strings.Contains(rest, " ") {
			return complWindows, rest, ""
		}
		if strings.HasPrefix(rest, "new ") {
			return complWindowNewArg, rest[len("new "):], ""
		}
		return complNone, "", ""
	case "set":
		if !strings.Contains(rest, " ") {
			return complSetKey, rest, ""
		}
		k, v, _ := strings.Cut(rest, " ")
		return complSetValue, v, k
	case "theme":
		return complTheme, rest, ""
	case "help":
		return complHelp, rest, ""
	case "net":
		return complNet, rest, ""
	case "fold":
		return complFold, rest, ""
	case model.NetTelegram, model.NetDiscord:
		return complNetCmd, rest, ""
	case "log":
		return complLog, rest, ""
	case "send":
		// The whole tail: a path may hold spaces (splitSendArgs allows it);
		// once it names a file, the rest is the caption.
		if p, _ := splitSendArgs(rest, isFile); p != rest {
			return complNone, "", ""
		}
		return complPath, rest, ""
	default:
		return complNone, "", ""
	}
}

// setKeys : keys that /set knows.
var setKeys = []string{"timestamps", "timestamps_seconds", "cycle_mode", "multiline", "link_previews", "maps", "hover", "images", "images_hover", "video", "avatars", "auto_media_max_kb", "download_dir", "bell", "notify", "auto_open_days", "aggregate", "log", "log_dir", "separator", "redline", "sidebar_sort", "sidebar_width", "spell", "spell_quotes", "kitty_images", "cache_messages", "lang"}

// setValues gives the valid values of a /set key with a closed choice, nil otherwise.
func setValues(key string) []string {
	switch key {
	case "images":
		return []string{"auto", "kitty", "halfblock", "off"}
	case "hover":
		return []string{"menu", "highlight", "off"}
	case "video":
		return []string{"show", "hidden", "autoplay"}
	case "notify":
		return []string{"terminal", "desktop", "off"}
	case "sidebar_sort":
		return []string{"recent", "alpha", "unread"}
	case "cycle_mode":
		return []string{"next", "last_unread"}
	case "spell":
		// a chain ("fr+us") is typed by hand
		return append([]string{"off", "us"}, spell.Available(spell.DictDir)...)
	case "lang":
		return i18n.Langs() // a chain ("fr+en") is typed by hand
	case "timestamps", "timestamps_seconds", "link_previews", "maps", "images_hover", "avatars", "bell", "aggregate", "log", "separator", "redline", "spell_quotes":
		return []string{"on", "off"}
	default:
		return nil
	}
}

// multiWord gives the candidates of several words (chats, themes) in the form
// Editor.Complete wants — word + name[len(tail):] for each name prefixed by tail.
func multiWord(word, tail string, names []string) []string {
	var out []string
	tl := strings.ToLower(tail)
	for _, n := range names {
		if strings.HasPrefix(strings.ToLower(n), tl) {
			out = append(out, word+n[len(tail):])
		}
	}
	return out
}

// isFile : the path names a regular file ("~" expanded).
func isFile(p string) bool {
	st, err := os.Stat(config.Expand(p))
	return err == nil && st.Mode().IsRegular()
}

// pathCandidates : the entries of the directory of p whose name starts with
// its last element, as p was typed ("~" and relative paths kept); a directory
// ends with "/" so that the next Tab goes on inside it. Hidden entries only
// when the typed name starts with ".".
func pathCandidates(p string) []string {
	dir, base := path.Split(p)
	list := dir
	if list == "" {
		list = "."
	}
	es, err := os.ReadDir(config.Expand(list))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range es {
		n := e.Name()
		if !strings.HasPrefix(n, base) || (base == "" && strings.HasPrefix(n, ".")) {
			continue
		}
		if st, err := os.Stat(config.Expand(dir + n)); err == nil && st.IsDir() { // symlinks too
			n += "/"
		}
		out = append(out, dir+n)
	}
	return out
}

// chatCandidates : the names Tab may complete after /query, /join, /msg —
// each chat by its @username, its bare username and its shown title. A name
// matches from its start or from the start of any of its words ("cop" gives
// "copains du foot"), and the candidate is the name from there on: the editor
// completes its last word, and findChat takes the piece it gives.
func (u *UI) chatCandidates(word, tail string) []string {
	keep := chatKinds(u.ed.String())
	tr := []rune(tail)
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		r := []rune(n)
		for i := range r {
			if i > 0 && r[i-1] != ' ' || len(r)-i < len(tr) {
				continue
			}
			if strings.EqualFold(string(r[i:i+len(tr)]), tail) {
				candidate := word + string(r[i+len(tr):])
				key := strings.ToLower(candidate)
				if !seen[key] {
					out = append(out, candidate)
					seen[key] = true
				}
				return
			}
		}
	}
	// Keep the existing dialog order for the stable part of the list, but put
	// online contacts first. This matters most for an empty target ("/m <Tab>"):
	// the first, short list should still offer the useful peers.
	chats := slices.Clone(u.chatList)
	slices.SortStableFunc(chats, func(a, b *model.Chat) int {
		if u.online(a) != u.online(b) {
			if u.online(a) {
				return -1
			}
			return 1
		}
		return 0
	})
	for _, c := range chats {
		if keep != nil && !keep(c) {
			continue
		}
		if tail == "" { // one useful name per chat in the unfiltered list
			if c.Username != "" {
				add("@" + c.Username)
			} else {
				add(u.title(c))
			}
			continue
		}
		if c.Username != "" {
			add("@" + c.Username)
			add(c.Username)
		}
		add(u.title(c)) // the local name can be completed, findChat resolves it
	}
	return out
}

// chatKinds : the chats a command completes — /join a channel or a group,
// /query a private conversation, /msg and free text any of them (nil).
func chatKinds(line string) func(*model.Chat) bool {
	if !strings.HasPrefix(line, "/") {
		return nil
	}
	name, _, _ := strings.Cut(line[1:], " ")
	switch resolveCommand(strings.ToLower(name)) {
	case "join":
		return func(c *model.Chat) bool { return c.Kind != model.ChatUser }
	case "query":
		return func(c *model.Chat) bool { return c.Kind == model.ChatUser }
	}
	return nil
}

const completionShortLimit = 20
const completionLongLimit = 100

type completionState struct {
	line    string
	cursor  int
	matches []string
	index   int
	cycle   bool
}

func (u *UI) showCompletionChoices(list []string, expanded bool) {
	limit := completionShortLimit
	if expanded {
		limit = completionLongLimit
	}
	shown := list
	if len(shown) > limit {
		shown = append(slices.Clone(shown[:limit]), "…")
	}
	u.sys(i18n.T("complete_choices", strings.Join(shown, "  ")))
}

// completeTab cycles command names from the original prefix. Chat targets
// without any letters always list first; repeated Tab expands the list.
// Paths and other arguments retain the editor's common-prefix completion.
func (u *UI) completeTab() {
	line, cursor := u.ed.String(), u.ed.Cursor()
	if s := u.completion; s != nil && s.line == line && s.cursor == cursor {
		if s.cycle {
			s.index = (s.index + 1) % len(s.matches)
			u.ed.Replace(0, cursor, s.matches[s.index])
			s.line, s.cursor = u.ed.String(), u.ed.Cursor()
		} else {
			// Presence may have changed between the two presses.
			src, tail, _ := complContext(line, cursor)
			if src == complChats {
				start := cursor
				for start > 0 && !sep(u.ed.buf[start-1]) {
					start--
				}
				s.matches = u.chatCandidates(string(u.ed.buf[start:cursor]), tail)
			}
			u.showCompletionChoices(s.matches, true)
		}
		return
	}
	u.completion = nil
	src, tail, _ := complContext(line, cursor)
	var list []string
	if src == complCommands {
		for _, name := range commandNames {
			if strings.HasPrefix(name, strings.ToLower(tail)) {
				list = append(list, name)
			}
		}
		slices.Sort(list) // /ne: /net, /new
		if len(list) > 1 {
			u.ed.Replace(0, cursor, list[0])
			u.completion = &completionState{line: u.ed.String(), cursor: u.ed.Cursor(), matches: list, cycle: true}
			return
		}
	}
	if src == complChats && tail == "" {
		list = u.chatCandidates("", "")
	} else {
		list = u.ed.Complete(u.candidates)
	}
	if len(list) > 0 {
		u.completion = &completionState{line: u.ed.String(), cursor: u.ed.Cursor(), matches: list}
		u.showCompletionChoices(list, false)
	}
}
