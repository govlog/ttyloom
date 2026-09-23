// Package module is the boundary between the client and its networks. A
// network package (protocols/<x>) gives a Module; the UI gives it a Host.
// Nothing here knows a network by its name.
package module

import (
	"context"

	"github.com/govlog/ttyloom/internal/model"
)

// Module : one network protocol, seen by the client. Its networks are named
// "<Name>" (one account) or "<Name>:<instance>" (IRC: "irc:libera").
type Module interface {
	Name() string
	// Load reads its keys of config.toml — sections or top-level keys — and
	// the environment variables that are its own. An error stops the start.
	Load(src ConfigSource) error
	// Save writes its keys back, as read from the file (never a secret that
	// came from the environment).
	Save(dst ConfigSink)
	// Template : its commented block of a new config.toml. Top-level keys
	// only: the client writes it after its own keys, before any table.
	Template() string
	// Networks : the networks configured now, read again at every call
	// (/irc add changes them while the client runs).
	Networks() []string
	// Cache : directory of the disk cache of net under the cache root, and
	// whether files in an older format are still read (the cache is the only
	// copy of the history: IRC).
	Cache(net string) (subdir string, keepOld bool)
	// Launch builds the backend of net from the configuration of the moment.
	// The UI runs it. An error (a token command that fails) starts nothing.
	Launch(ctx context.Context, h Host, net string, ev chan<- model.Event) (model.Backend, error)
	Commands() []Command
	// Claims : a name only this module resolves ("#room" for IRC): /query and
	// /join then ask its networks alone.
	Claims(name string) bool
}

// Win : the window a command comes from. Ref is the UI's own handle, opaque
// to a module; a nil Ref means the window of Chat when it has one, else the
// window shown.
type Win struct {
	Chat, Target *model.Chat
	Ref          any
}

// Topic : the help entry of a command. Key is the prefix of three texts of
// the module catalogue: <Key>_name, <Key>_short, <Key>_long. Section is a
// section of /help (help_section_<Section>): an existing one ("chats") or one
// of the module ("irc").
type Topic struct{ Key, Section string }

// Command : a slash command of a module.
type Command struct {
	Name string // without the slash
	// Context : the command exists only where a network of the module is
	// meant (Host.ContextNet not empty): IRC's /kick in an IRC window.
	Context bool
	Help    Topic
	// Complete gives the candidates of the argument being typed: rest is
	// everything after the command; typing is the part the candidates
	// replace. nil: no completion.
	Complete func(h Host, w Win, rest string) (names []string, typing string)
	Run      func(h Host, w Win, args []string, text string)
}

// Field : one line of a Form.
type Field struct {
	Label   string
	Value   string
	Secret  bool     // shown as dots
	Choices []string // presets cycled with ← → or Space
	// Sel : -1 for free text. 0 or more: the value came from a choice (or a
	// preset the module marks so), and typing replaces it.
	Sel int
}

// Form : a centred box of fields (Tab moves, Enter submits, Esc closes).
type Form struct {
	Title string
	// Intro : lines of guide drawn above the fields, folded to the width of
	// the box; Link : a link drawn under them, opened with Ctrl+O.
	Intro  []string
	Link   string
	Fields []Field
	// Submit gets the values, trimmed, in field order; an error text keeps
	// the box open, "" closes it.
	Submit func(vals []string) string
	// Pick : choice i was shown on field f; the module may fill other fields
	// (Value, Sel) of form.
	Pick func(form *Form, f, i int)
}

// Host : what the UI gives a module. Every method runs on the goroutine of
// the UI — a command, Launch, a Submit or a Pick, a function given to Do —
// except Do itself, which any goroutine may call.
type Host interface {
	// Print writes a system line in w; the zero Win is the window shown.
	Print(w Win, line string)
	// Status writes a line in window 0.
	Status(line string)

	// NetAction : status | login | logout | disconnect of net, the answer in w.
	NetAction(w Win, net, sub string)
	// AddNetwork lists a network just configured, makes its cache and starts it.
	AddNetwork(net string)
	// RemoveNetwork stops net, closes its windows and drops its chats.
	RemoveNetwork(net string)
	Backend(net string) model.Backend // nil when not running
	Context(net string) context.Context
	// ContextNet : the network of module mod that w means — the one of its
	// chat or send target, else the /net filter, else the only configured
	// network of mod; "" when none.
	ContextNet(w Win, mod string) string

	// SaveConfig writes config.toml; false (and a line saying why) when it fails.
	SaveConfig() bool

	Chats(net string) []*model.Chat
	ChatByTitle(net, title string) *model.Chat
	SendFile(c *model.Chat, path string)
	// Resolve makes the chat of name on net, then sends sendPath to it when
	// sendPath is not empty.
	Resolve(w Win, name, net, sendPath string)
	Download(m *model.Msg)
	// LastIncomingFile : the last file of w received and not yet fetched.
	LastIncomingFile(w Win) *model.Msg

	OpenForm(f *Form)
	// Do runs f on the goroutine of the UI: the way for a backend goroutine
	// to change the configuration.
	Do(f func())
}

// ConfigSource : config.toml, as Load reads it.
type ConfigSource interface {
	// Decode fills v from the top-level key k (a section or a scalar; the
	// case of k is ignored, like the rest of the file). present is false
	// when the file has no such key.
	Decode(k string, v any) (present bool, err error)
	// Unknown reports a key of the module it refuses (an [[irc]] table with
	// no valid name): the start lists it with the unknown keys.
	Unknown(k string)
}

// ConfigSink : the table Save writes to config.toml.
type ConfigSink interface {
	Set(k string, v any)
}
