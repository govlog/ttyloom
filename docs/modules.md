# Writing a network module

[Contributing](../CONTRIBUTING.md)

Each network of TTYloom is a module: a package under `protocols/` that brings
its configuration, its launch, its commands and its texts. The client
(`internal/`) names no network. Adding a network is a package and one line in
`cmd/ttyloom/main.go`:

```go
func modules() []module.Module {
	return []module.Module{tgc.NewModule(), dsc.NewModule(), irc.NewModule(), matrix.NewModule()}
}
```

The order of that list is the order in which the networks start and in which
their blocks appear in a new `config.toml`.

A module has two parts. The backend implements `model.Backend`: it connects,
loads chats and history, sends, and posts events to the UI. The module
(`internal/module`) is what the client sees of the network. This page covers
the module; `protocols/irc` is the most complete example, `protocols/dsc` the
shortest.

## The Module interface

| Method | Rule |
|---|---|
| `Name()` | The module key: `"telegram"`. Its networks are `"<Name>"` (one account) or `"<Name>:<instance>"` (`"irc:libera"`). |
| `Load(src)` | Reads its keys of `config.toml` with `src.Decode(key, &v)`, and its environment variables. `src.Unknown(key)` reports a key it refuses. An error stops the start. |
| `Save(dst)` | Writes its keys back with `dst.Set(key, v)`, as the file had them. Never write a secret that came from the environment. |
| `Template()` | Its commented block of a new `config.toml`. Top-level keys only, no table: the client writes it after its own keys. |
| `Networks()` | The networks configured now. It is read again at every call: a command may add one while the client runs. |
| `Cache(net)` | The directory of the disk cache of `net` under the cache root, and `keepOld` when the cache is the only copy of the history (IRC). |
| `Launch(ctx, h, net, ev)` | Builds the backend of `net` from the configuration of the moment; the client runs it. An error (a token command that fails) starts nothing. |
| `Commands()` | Its slash commands (see below). |
| `Claims(name)` | A name only this module resolves (`"#room"` for IRC): `/query` and `/join` then ask its networks alone. |
| `Label()` | The name of the module in the Networks box (`"Telegram"`): a brand name, not a text of the catalogue. |
| `CanAdd()` | A network of the module can be added now: Telegram and Discord while none is configured, IRC always. The box shows `+ Add <Label>` when true. |
| `OpenSetup(h)` | Opens the page of the module in the box: a form (`h.OpenForm`), described below. |

A module whose networks the box can remove implements `module.Remover`:
`Remove(h, w, net)` takes the network out of `config.toml` (`h.SaveConfig`)
and stops it (`h.RemoveNetwork`). IRC does; Telegram and Discord do not.

**The page.** `OpenSetup` opens a `module.Form`. `Intro` holds lines of guide,
folded to the box above the fields; `Link` a link drawn under them, which
Ctrl+O opens with no question (it comes from the module, not the network).
`Submit` checks the values (an error text keeps the page open), sets the
settings of the module, writes them with `h.SaveConfig()` (on `false`, put the
settings back and return an error text) and calls `h.AddNetwork(net)`; the
login follows as at every start. A page never changes a network already
configured.

A module keeps its own settings type and reads it with `Decode`. A key the
client and no module took is listed as unknown at start.

## The Host

`Launch` and every command get a `module.Host`: what the UI offers a module.
Every method runs on the goroutine of the UI, except `Do`.

| Method | Use |
|---|---|
| `Print(w, line)`, `Status(line)` | A system line in the window of the command (the zero `Win`: the window shown), or in window 0. |
| `NetAction(w, net, sub)` | `status`, `login`, `logout`, `disconnect` of a network. |
| `AddNetwork(net)`, `RemoveNetwork(net)` | A network configured or deleted while the client runs. |
| `Backend(net)`, `Context(net)` | The running backend (nil when stopped) and its context. Type-assert your own backend type. |
| `ContextNet(w, mod)` | The network of your module that `w` means: the one of its chat or target, else the `/net` filter, else your only network. |
| `SaveConfig()` | Writes `config.toml`. |
| `Chats`, `ChatByTitle`, `SendFile`, `Resolve`, `Download`, `LastIncomingFile` | Actions of the UI on chats and files. |
| `OpenForm(f)` | A centred form (see `module.Form`); in the Networks box, the page of the module. |
| `Do(f)` | From a backend goroutine: `f` runs on the goroutine of the UI. The only way for a backend to change the configuration. |

## Commands, help, completion

```go
module.Command{
	Name:     "irc",
	Context:  false, // true: only where a network of the module is meant
	Help:     module.Topic{Key: "help_irc", Section: "chats"},
	Complete: func(h module.Host, w module.Win, rest string) ([]string, string) { … },
	Run:      func(h module.Host, w module.Win, args []string, text string) { … },
}
```

A general command resolves by prefix with the commands of the client. A
context command (IRC's `/kick`) exists only where `ContextNet` is not empty,
and never takes a prefix from a general one. `Help.Key` names three texts of
your catalogue: `<Key>_name`, `<Key>_short`, `<Key>_long`. `Help.Section` is a
section of `/help`; a new one needs `help_section_<Section>` in your
catalogue. `Complete` gets everything typed after the command and gives the
candidates with the part of the line they replace.

## Texts

Put `i18n/en.toml` and `i18n/fr.toml` in the package, one key per line, and
register them from `catalog.go`:

```go
//go:embed i18n/*.toml
var files embed.FS

var Catalog, _ = fs.Sub(files, "i18n")

func init() { i18n.Register(Catalog) }
```

`TestCatalogs` (`cmd/ttyloom`) checks that both languages have the same keys,
that no key is in two catalogues, and that every `i18n.T("…")` of the code has
its key. Add your `Catalog` to its list.

## Chats and remote text

Set `model.Chat.Group` for a chat that belongs to a group inside the network
(a Discord guild): the sidebar gives each group a section, and the title of
such a chat starts with `Group + " / "`. The UI cleans every remote string
when the event arrives; a backend never writes to the terminal.

A QR login posts `model.EvQR` with the keys of its two help lines (`Hint`,
`Keys`) in your catalogue.

## Hooks

Hooks ([hooks.md](hooks.md)) run on the messages your backend posts. Three
things tell them about your network:

- `model.Caps.NameIsID`: the name of a person is its identity (IRC: the
  nick). A hook's `from` then compares names, case apart, and a bare name in
  a message counts as a mention.
- `model.Msg.Notice`: a message no program should answer (IRC NOTICE). It
  never fires a hook.
- `module.AutoReplyWarner` (optional): `AutoReplyWarning()` gives a text of
  your catalogue, printed at load for each hook that sends to one of your
  networks. Discord has one: an automatic reply from a user account is a
  self-bot.

A message of mine (`Out`, or a `FromID` equal to the id of `EvReady`) never
fires a hook: set them right, and a hook never answers itself.

## Checklist

1. `protocols/<x>/`: the backend (`model.Backend`) and `module.go` (`Module`).
2. `catalog.go` and `i18n/{en,fr}.toml`; the catalogue added to `TestCatalogs`.
3. The module added to `modules()` in `cmd/ttyloom/main.go`.
4. Tests of the module with a small fake `Host`; `TestCoreNamesNoNetwork`
   still passes (the core names no network).
5. The README, `docs/guide.md` and the changelog, in both languages where they
   have two.

`internal/ui/e2e_test.go` holds `cfgFake`, with `fakeMod` of
`internal/ui/fakenet_test.go`: a whole module in a few dozen lines — a section
of its own, a network, a command — and the test that runs it through the
client, from `config.toml` to its command.
