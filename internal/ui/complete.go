package ui

import (
	"os"
	"path"
	"strings"

	"github.com/govlog/ttyloom/internal/config"

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
		words := strings.Fields(s)
		return complChats, words[len(words)-1], ""
	}
	name := strings.ToLower(before[1:])
	if a, ok := aliases[name]; ok {
		name = a
	}
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
