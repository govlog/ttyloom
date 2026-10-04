package ui

import (
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"

	"github.com/govlog/ttyloom/internal/i18n"
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
	complWords                           // a closed list: /log <on|off>, /media <tab> (argWords)
	complNone                            // /open /history …
	complModule                          // a command of a module: its Complete
)

// argWords : the arguments of the commands that take a closed list.
var argWords = map[string][]string{"log": {"on", "off"}, "media": {"media", "gifs", "files"}}

// complContext gives the completion source and the line "tail" to complete
// (everything after the command, for the candidates of several words). cursor
// is a rune index; only the part of the line before the cursor is read.
func complContext(line string, cursor int, names cmdNames) (src complSource, tail string, setKey string) {
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
		return complChats, s[strings.LastIndexByte(s, '\n')+1:], "" // last word, a line break ends the one before
	}
	if !strings.HasPrefix(before, "/") {
		return complChats, s[strings.LastIndexAny(s, " \n")+1:], ""
	}
	name := resolveCommand(strings.ToLower(before[1:]), names)
	if names.module["/"+name] {
		return complModule, rest, name
	}
	switch name {
	case "query", "msg", "join", "whois", "rename", "unrename", "mute", "unmute":
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
	case "log", "media":
		return complWords, rest, name
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
		isDir := e.IsDir()
		if e.Type()&fs.ModeSymlink != 0 { // a link: what it points to (one stat, not one per entry)
			st, err := os.Stat(config.Expand(dir + n))
			isDir = err == nil && st.IsDir()
		}
		if isDir {
			n += "/"
		}
		out = append(out, dir+n)
	}
	return out
}

// chatCandidates : the names Tab may complete after /query, /join, /msg —
// each chat by its @username, its bare username and its shown title. A name
// matches from its start or from the start of any of its words ("cop" gives
// "copains du foot"), case and accents apart ("stef" gives "Stéfany"), and the
// candidate is the name from there on: the editor completes its last word,
// and findChat takes the piece it gives.
func (u *UI) chatCandidates(word, tail string) []string {
	keep := chatKinds(u.ed.String())
	ft := []rune(render.Fold(tail))
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		r := []rune(n)
		f, orig := foldRunes(n) // orig: rune of n each folded rune comes from
		for j := range f {
			i := orig[j]
			if (j > 0 && orig[j-1] == i) || (i > 0 && r[i-1] != ' ') || len(f)-j < len(ft) {
				continue // inside a rune, or not at the start of a word
			}
			if slices.Equal(f[j:j+len(ft)], ft) {
				end := len(r)
				if k := j + len(ft); k < len(f) {
					end = orig[k]
				}
				candidate := word + string(r[end:])
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
	switch resolveCommand(strings.ToLower(name), cmdNames{general: commandNames}) {
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
	line     string
	cursor   int
	matches  []string
	index    int
	cycle    bool
	expanded bool // the long list is shown: a further Tab adds nothing
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
	w := u.view()
	w.Items = append(w.Items, &Item{Sys: i18n.T("complete_choices", strings.Join(shown, "  ")), At: time.Now(), Choices: true})
	w.trim()
}

// dropChoices : Esc after a listing — the lists go from the current window.
func (u *UI) dropChoices() {
	w := u.view()
	w.Items = slices.DeleteFunc(w.Items, func(it *Item) bool { return it.Choices })
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
		} else if !s.expanded {
			s.expanded = true
			// Presence and the member list may have changed between the two presses.
			src, _, _ := complContext(line, cursor, u.commandNames())
			if src == complChats {
				start := cursor
				for start > 0 && !sep(u.ed.buf[start-1]) {
					start--
				}
				s.matches = u.candidates(string(u.ed.buf[start:cursor]), start == 0)
			}
			u.showCompletionChoices(s.matches, true)
		}
		return
	}
	u.completion = nil
	src, tail, _ := complContext(line, cursor, u.commandNames())
	var list []string
	if src == complCommands {
		for _, name := range u.commandNames().all() {
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
