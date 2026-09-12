package ui

import (
	"strings"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/render"
	"github.com/govlog/ttyloom/internal/theme"
)

// topic : one help entry. name is the bare, stable name, the one of the
// search, of the completion and of the test coverage ("/send"); key is the
// i18n prefix of the three texts shown: <key>_name (the name with its
// placeholders, "/send <path> [caption]"), <key>_short and <key>_long.
type topic struct{ name, key, section string }

// helpSections : display order of /help; each name is a suffix of an i18n key
// (help_section_<name>).
var helpSections = []string{"windows", "chats", "messages", "media", "sidebar", "input", "options"}

// helpTopics : one entry per command of commandNames, per notable key and per
// key of setKeys. Descriptions condensed from README.md.
var helpTopics = []topic{
	// Windows
	{"/window", "help_window", "windows"},
	{"/close", "help_close", "windows"},
	{"Ctrl+X", "help_ctrl_x", "windows"},
	{"Alt+←/→", "help_alt_arrows", "windows"},
	{"Alt+1..9", "help_alt_digits", "windows"},
	{"F6", "help_f6", "windows"},
	{"/log", "help_log", "windows"},

	// Chats
	{"/query", "help_query", "chats"},
	{"/join", "help_join", "chats"},
	{"/new", "help_new", "chats"},
	{"Ctrl+N", "help_ctrl_n", "chats"},
	{"/msg", "help_msg", "chats"},
	{"/me", "help_me", "chats"},
	{"/chats", "help_chats", "chats"},
	{"/net", "help_net", "chats"},
	{"/telegram", "help_telegram", "chats"},
	{"/discord", "help_discord", "chats"},
	{"/history", "help_history", "chats"},
	{"/search", "help_search", "chats"},
	{"/whois", "help_whois", "chats"},
	{"/rename", "help_rename", "chats"},
	{"/unrename", "help_unrename", "chats"},
	{"/clear", "help_clear", "chats"},
	{"/help", "help_help", "chats"},

	// Messages
	{"Alt+↑/↓", "help_alt_updown", "messages"},
	{"Ctrl+↑/↓", "help_ctrl_updown", "messages"},
	{"right click", "help_msg_menu", "messages"},
	{"Ctrl+F", "help_ctrl_f", "messages"},
	{"e", "help_key_e", "messages"},
	{"d", "help_key_d", "messages"},
	{"p", "help_key_p", "messages"},
	{"r", "help_key_r", "messages"},
	{"i", "help_key_i", "messages"},
	{"o", "help_key_o", "messages"},
	{"v", "help_key_v", "messages"},
	{"l", "help_key_l", "messages"},
	{"s", "help_key_s", "messages"},
	{"c", "help_key_c", "messages"},
	{"Esc", "help_esc", "messages"},

	// Media
	{"/open", "help_open", "media"},
	{"/view", "help_view", "media"},
	{"/send", "help_send", "media"},
	{"F4", "help_f4", "media"},
	{"F5", "help_f5", "media"},
	{"Ctrl+V", "help_ctrl_v", "media"},
	{"/gif", "help_gif", "media"},
	{"Ctrl+G", "help_ctrl_g", "media"},

	// Sidebar
	{"F2", "help_f2", "sidebar"},
	{"Shift+F2", "help_shift_f2", "sidebar"},
	{"F3", "help_f3", "sidebar"},
	{"F7", "help_f7", "sidebar"},
	{"/fold", "help_fold", "sidebar"},

	// Input
	{"/emoji", "help_emoji", "input"},
	{"Ctrl+T", "help_ctrl_t", "input"},
	{"Ctrl+A/E", "help_ctrl_ae", "input"},
	{"Ctrl+B/I/U", "help_ctrl_biu", "input"},
	{"Shift+Enter", "help_shift_enter", "input"},
	{"Ctrl+R", "help_ctrl_r", "input"},
	{"PgUp/PgDn", "help_pgupdn", "input"},
	{"Ctrl+L", "help_ctrl_l", "input"},
	{"/quit", "help_quit", "input"},
	{"Ctrl+C", "help_ctrl_c", "input"},

	// Options
	{"/theme", "help_theme", "options"},
	{"/set", "help_set", "options"},
	{"/debug", "help_debug", "options"},
	{"timestamps", "help_timestamps", "options"},
	{"timestamps_seconds", "help_timestamps_seconds", "options"},
	{"cycle_mode", "help_cycle_mode", "options"},
	{"multiline", "help_multiline", "options"},
	{"link_previews", "help_link_previews", "options"},
	{"maps", "help_maps", "options"},
	{"hover", "help_hover", "options"},
	{"images", "help_images", "options"},
	{"images_hover", "help_images_hover", "options"},
	{"video", "help_video", "options"},
	{"avatars", "help_avatars", "options"},
	{"auto_media_max_kb", "help_auto_media_max_kb", "options"},
	{"download_dir", "help_download_dir", "options"},
	{"bell", "help_bell", "options"},
	{"notify", "help_notify", "options"},
	{"auto_open_days", "help_auto_open_days", "options"},
	{"aggregate", "help_aggregate", "options"},
	{"log", "help_log_opt", "options"},
	{"log_dir", "help_log_dir", "options"},
	{"separator", "help_separator", "options"},
	{"redline", "help_redline", "options"},
	{"spell", "help_spell", "options"},
	{"spell_quotes", "help_spell_quotes", "options"},
	{"sidebar_sort", "help_sidebar_sort", "options"},
	{"sidebar_width", "help_sidebar_width", "options"},
	{"kitty_images", "help_kitty_images", "options"},
	{"cache_messages", "help_cache_messages", "options"},
	{"lang", "help_lang", "options"},
}

// display gives the shown name of a topic, with its argument placeholders.
func (t topic) display() string { return i18n.T(t.key + "_name") }

// base gives the bare name of a shown name, with no placeholder ("/send <path>" → "/send").
func base(name string) string {
	if i := strings.IndexByte(name, ' '); i >= 0 {
		return name[:i]
	}
	return name
}

// fold gives a comparable form of a topic name, with no leading / and no case or accent.
func fold(s string) string { return render.Fold(strings.TrimPrefix(s, "/")) }

// match tells whether the topic answers the folded form q. The stable name and
// the shown name are both taken: the English one as well as the local one.
func (t topic) match(q string) bool {
	return fold(t.name) == q || fold(base(t.display())) == q
}

// helpCandidates gives the topic names for the Tab completion of /help (no /).
func helpCandidates() []string {
	out := make([]string, 0, len(helpTopics))
	for _, t := range helpTopics {
		out = append(out, strings.TrimPrefix(t.name, "/"))
	}
	return out
}

// helpLines : /help with no argument — one line per entry, grouped by section,
// column lined up on the longest name.
func helpLines(width int) []render.Line {
	col := 0
	for _, t := range helpTopics {
		if w := render.Width(t.display()); w > col {
			col = w
		}
	}
	col += 2
	var lines []render.Line
	for _, sec := range helpSections {
		lines = append(lines, render.Plain("── "+i18n.T("help_section_"+sec)+" ──", theme.Style{Bold: true}, width)...)
		for _, t := range helpTopics {
			if t.section != sec {
				continue
			}
			name := t.display()
			pad := strings.Repeat(" ", col-render.Width(name))
			lines = append(lines, render.Plain("*** "+name+pad+i18n.T(t.key+"_short"), theme.Style{}, width)...)
		}
	}
	return lines
}

// suggestLimit : most suggestions on an unknown topic.
const suggestLimit = 5

// helpTopic : /help <topic> — detailed help, or suggestions when unknown.
func helpTopic(query string, width int) []render.Line {
	q := fold(query)
	for _, t := range helpTopics {
		if t.match(q) {
			var lines []render.Line
			lines = append(lines, render.Plain("*** "+t.display(), theme.Style{Bold: true}, width)...)
			body := i18n.T(t.key + "_long")
			if body == "" {
				body = i18n.T(t.key + "_short")
			}
			lines = append(lines, render.Plain(body, theme.Style{}, width)...)
			return lines
		}
	}
	sug := suggest(q, func(k string) bool { return strings.HasPrefix(k, q) })
	if len(sug) == 0 {
		sug = suggest(q, func(k string) bool { return strings.Contains(k, q) })
	}
	msg := i18n.T("help_no_topic", query)
	if len(sug) > 0 {
		msg += i18n.T("help_try", strings.Join(sug, ", "))
	}
	return render.Plain(msg, theme.Style{}, width)
}

// suggest gives up to suggestLimit topic names whose folded form satisfies match.
func suggest(q string, match func(folded string) bool) []string {
	var out []string
	for _, t := range helpTopics {
		if match(fold(t.name)) {
			out = append(out, t.name)
			if len(out) == suggestLimit {
				break
			}
		}
	}
	return out
}
