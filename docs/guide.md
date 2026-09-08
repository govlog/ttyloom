# TTYloom manual

[English](guide.md) · [Français](guide.fr.md) · [Overview](../README.md) · [Account setup](authentication.md)

Manual for **1.1.1**, checked against the code on **September 5, 2026**.

<details>
<summary>Contents</summary>

- [Introduction](#introduction) and [requirements](#requirements)
- [Installation](#installation)
- [Configuration and accounts](#configuration)
- [Windows, conversations and controls](#using-ttyloom)
- [Image and video viewer](#viewer)
- [Cache and synchronization](#cache-and-synchronization)
- [Version and limits](#version-and-limits)
- [Signing out](#signing-out)
- [Troubleshooting](#troubleshooting)

</details>

## Introduction

This manual covers installation, accounts, every configuration option, commands,
mouse controls and keyboard shortcuts on Linux. The amd64 and arm64 binaries
do not need Go. Building from source requires **Go 1.26.7 or newer**.

**TTYloom** is a terminal client for **Telegram and Discord**, inspired by
**ircii** and **BitchX**: numbered message windows, a status bar and an input
line. The kitty graphics protocol displays photos, stickers, GIFs and video
frames inside Ghostty or kitty. Unicode half blocks provide a fallback in other
terminals. It supports message formatting, clickable OSC 8 links and Ghostty
color themes.

Automated checks run on Ubuntu; the ARM64 binary gets a startup check under
QEMU. These checks do not cover every terminal combination or live account login.

Connect **Telegram, Discord, or both**. Discord alone needs no Telegram
credentials. The sidebar and aggregate view combine networks; `/net` filters
them. IRC and WhatsApp are planned but not implemented.

Telegram supports a user account, through QR or phone/code/2FA login, and bot
login through BotFather. Bots only see messages received after login. Discord
uses a user token; bot tokens and OAuth login are not supported.

## Requirements

- For source builds only: [Go 1.26.7 or newer](https://go.dev/dl/).
- Optional: `ffmpeg` and `ffprobe` for videos and animated WebP. Actual GIF
  files are decoded in Go.
- For source builds with spell checking: Hunspell development files and
  dictionaries. The portable release binaries use `nospell`.
- For Telegram only: an application API ID and hash from
  [my.telegram.org/apps](https://my.telegram.org/apps). Both Telegram user and bot
  modes need them; Discord does not. See the [account setup guide](authentication.md).

On Debian or Ubuntu:

```bash
sudo apt update
sudo apt install ffmpeg
sudo apt install build-essential libhunspell-dev hunspell-fr hunspell-en-us
```

Install only the optional components you intend to use.

## Installation

### Ready-to-run binary

Download the Linux archive for your CPU (`amd64` for x86-64, `arm64` for ARM64)
and `SHA256SUMS` from [v1.1.1](https://github.com/govlog/ttyloom/releases/tag/v1.1.1).

```bash
sha256sum --check --ignore-missing SHA256SUMS
tar -xzf ttyloom_1.1.1_linux_amd64.tar.gz
cd ttyloom_1.1.1_linux_amd64
./ttyloom --version
./ttyloom
```

Replace `amd64` with `arm64` for ARM64. Portable binaries omit Hunspell. Keep
the supplied licenses and notices with the binary.

### Build from source

```bash
git clone https://github.com/govlog/ttyloom.git
cd ttyloom
go build -trimpath -o ttyloom ./cmd/ttyloom
```

Optionally install it on your command path:

```bash
sudo install -m 0755 ttyloom /usr/local/bin/ttyloom
ttyloom --version
ttyloom
```

With no network configured, the expected result is a setup message and a new
`~/.config/ttyloom/config.toml` containing defaults. A source build without
release flags reports `dev` for its version.

To build without Hunspell or a C compiler:

```bash
CGO_ENABLED=0 go build -trimpath -tags nospell -o ttyloom ./cmd/ttyloom
```

Other optional tools are `wl-paste` or `xclip` for clipboard paste and
`notify-send` for desktop notifications.

## Configuration

### Configuration file

The default file is `~/.config/ttyloom/config.toml`, or
`$XDG_CONFIG_HOME/ttyloom/config.toml` when that variable is set. The first run
creates it. `TTYLOOM_DIR` overrides the complete configuration directory path.

Edit existing keys. This is a reference example; do not replace a file holding
your credentials with this empty example. Put `[telegram]` and `[discord]`
sections after all top-level settings.

```toml
api_id = 0
api_hash = ""
bot_token = ""
theme = ""
lang = ""
download_dir = "~/Downloads/ttyloom"
auto_media_max_kb = 5120
images = "auto"
images_hover = false
kitty_images = 48
video_inline_frames = 300
video = "show"
link_previews = true
maps = false
avatars = true
hover = "menu"
separator = true
redline = true
sidebar_sort = "recent"
spell = "off"
spell_quotes = false
sidebar_width = 26
timestamps = true
timestamps_seconds = false
cycle_mode = "next"
multiline = false
bell = true
auto_open_days = 7
aggregate = false
cache = true
cache_messages = 2000
notify = "terminal"
log = false
log_dir = "~/.local/share/ttyloom/logs"
```

| Key | Purpose |
| --- | --- |
| `api_id`, `api_hash` | Telegram application credentials; required only for Telegram. |
| `bot_token` | Empty for a user account; a BotFather token for Telegram bot mode. |
| `lang` | `en`, `fr`, or a fallback chain such as `fr+en`. The first language also selects plural rules. Empty follows `LC_ALL`/`LANG`: French for `fr_*`, otherwise English. `/set lang` changes it live and rejects unknown languages. |
| `theme` | Ghostty theme name. Empty reads the `theme` setting in `~/.config/ghostty/config`; `terminal` uses the terminal’s ANSI colors. |
| `download_dir` | Downloaded media directory. Names include date, network, chat ID, title and message ID. |
| `auto_media_max_kb` | Automatic download threshold in KiB, from 0 to 524288. **0 disables all automatic media downloads**, including unknown sizes. `/open` and `/view` remain explicit requests. |
| `images` | `auto` detects kitty graphics; `kitty`, `halfblock` and `off` select an explicit mode. `F4` cycles modes. |
| `images_hover` | Show images in an overlay when hovering over a message, with no reserved image rows in the conversation. `F5` toggles it. |
| `kitty_images` | Maximum number of images retained in the terminal, including GIFs and avatars. The least recently displayed image is released and sent again when needed. |
| `video_inline_frames` | Frames decoded for video playback, at 10 frames per second. Default 300; decoding also has a 200 MiB budget. Change this in the file and restart. |
| `video` | `show`: first frame until `l` starts playback. `hidden`: label only, with `v` and `o` still available. `autoplay`: loop a downloaded video when visible; `s` stops and `l` controls playback. Frame limits still apply. |
| `link_previews` | Display a recognized Telegram link’s site, title, description and thumbnail. `o` or a click on the label opens its page. |
| `maps` | Fetch OpenStreetMap tiles for a visible Telegram location or an explicit viewer request. Attribution remains visible; tiles are cached by coordinates under `download_dir/maps`. Disabled by default because it contacts `tile.openstreetmap.org`. |
| `avatars` | Small profile photos beside names and in the sidebar; kitty graphics only. |
| `hover` | `menu`: highlight and show actions under the pointer. `highlight`: highlight only, with actions on selection. `off`: disable hover tracking. Old boolean values remain accepted. Also controls pointer tracking for sidebar wheel navigation. |
| `separator` | A horizontal rule between messages and the status bar. |
| `redline` | An unread divider after the last read message. It stays in place during the visit and resets when you return to the window or regain focus. |
| `sidebar_width` | Width in columns, from 12 to half the terminal width. Drag its vertical border to resize and save it. |
| `sidebar_sort` | `recent`, `alpha` or `unread`. `F7` cycles these orders. |
| `spell` | `off`, or installed Hunspell dictionary codes joined with `+`, such as `fr+en_US`. Dictionaries are `.aff`/`.dic` pairs in `/usr/share/hunspell`. Two-letter prefixes resolve to installed dictionaries; `us` is an alias for `en_US`. Misspellings get a red underline. `Ctrl+R` cycles corrections; Enter applies, `i` ignores, `a` adds to `spell.txt`, Escape closes. Right-click a word to correct only that word. |
| `spell_quotes` | Also check quoted lines and fenced code blocks; off by default. |
| `timestamps` | Show a timestamp before each message. |
| `timestamps_seconds` | Include seconds: `15:04:05` instead of `15:04`. |
| `cycle_mode` | `next`: Ctrl+X selects the next window. `last_unread`: visit unread windows in number order, then return to the window you left; use the normal cycle when nothing is unread. |
| `multiline` | Shift+Enter opens an expanded editor. Enter sends and collapses it; Escape collapses it but keeps the draft. Editing a multiline message reopens it. |
| `bell` | Terminal bell for a private message or mention outside the active window. |
| `auto_open_days` | Create hidden windows for conversations active within N days on startup; 0 disables this. |
| `aggregate` | Combine open conversations in window 0; `F6` toggles it. |
| `cache` | Save dialogs and recent message history on disk. False disables the history cache. |
| `cache_messages` | Recent messages kept per conversation on disk; window memory follows the same message-count setting. |
| `notify` | `terminal` for native terminal notifications, `desktop` for `notify-send`, or `off`. Notification delivery also depends on focus. |
| `log`, `log_dir` | Plain-text conversation logs. `/log` toggles one window; `log = true` enables logging for new windows. |
| `[discord] token_cmd` | Command printing the Discord user token. Omit the section to disable Discord; a section without the command logs in by QR code and keeps the token in `discord.token`. |

`[telegram]` accepts `api_id`, `api_hash` and `bot_token` and takes precedence
over those top-level keys. Unknown settings produce a startup warning. `/set`
lists settings that can change live. Credentials, `cache` and
`video_inline_frames` are file settings that require a restart.

The `TG_API_ID`, `TG_API_HASH` and `TG_BOT_TOKEN` environment variables override
file values. `/set` and `/theme` save the configuration, but do not copy these
environment-supplied secrets into it.

```bash
chmod 0600 ~/.config/ttyloom/config.toml
TTYLOOM_DIR=/absolute/path/to/existing/config ttyloom
```

That directory must contain `config.toml` and session files directly. When a
destination directory already exists, `mv old_directory destination` nests the
old directory inside it instead of merging its contents. Back up your
configuration before moving it, and review `download_dir` and `log_dir` too.

### Telegram user login

Leave `bot_token` empty and supply your application ID and hash. On startup,
scan the QR code through **Telegram → Settings → Devices → Link Desktop
Device**. The code refreshes when it expires. Complete the 2FA prompt if enabled.
For phone login, follow the phone/code prompts instead.

```text
Phone number (+1…):
Code received:
2FA password:
```

Window 0 reports the connected account and conversation count. The session is
saved as `~/.config/ttyloom/session.json`, with mode `0600`, without encryption.
Later starts reuse it. A session file grants account access: never commit it or
share it. See [detailed Telegram setup](authentication.md#telegram).

### Telegram bot login

Create a bot through **@BotFather** and set `bot_token` in your configuration.
The application API ID and hash are still required.

```toml
bot_token = "YOUR_BOTFATHER_TOKEN"
```

Bots cannot list dialogs or load account history: Telegram rejects
`messages.getDialogs` and `messages.getHistory` for bots. TTYloom only shows
messages received after login; `/chats` and `/history` are unavailable. A bot
cannot initiate a private conversation with a user who has not contacted it.

Bot sessions use `session-bot.json`, separate from the user session. Switching
modes does not merge identities.

### Discord login

Discord user-token access is unsupported by Discord and can lead to account
suspension. Read the [official policy](https://discord.com/safety/360044104071-Tips-against-spam-and-hacking)
and the [token setup guide](authentication.md#discord).

Add this section once, at the end of `config.toml`, and start TTYloom:

```toml
[discord]
```

A QR code appears in window 0, like the Telegram one. Scan it from the Discord
app (**Settings → Scan QR Code**), confirm on the phone, and TTYloom is logged
in as its own device: the session shows in **Discord → Settings → Devices**
and survives a logout of your browser. The code is valid five minutes;
`/discord login` shows a new one. The token is kept in
`~/.config/ttyloom/discord.token`, mode `0600`, next to the Telegram session;
`/discord logout` ends that session on the server and deletes the file, Enter
gives the QR up.

You may instead keep the token yourself, in a password manager for example,
and name the command that prints it:

```toml
[discord]
token_cmd = "pass show discord/token"
```

The command must print only the token. It runs without a shell, splits on
whitespace and times out after 30 seconds: no pipes, redirections, variable
expansion or shell quoting. Use a wrapper script for a command that needs those
features. Its output is not copied into the configuration, logs or window 0.
A failing command leaves Discord disconnected and is reported in window 0
with what the command said on stderr; the client starts all the same. Fix the
manager entry, then `/discord login` runs the command again and connects
without a restart. That later run happens inside the raw terminal: a command
that prompts on the tty (a curses pinentry) only works at start; use a
graphical pinentry or an unlocked agent for `/discord login`. With
`token_cmd`, no QR is shown and `/discord logout` only disconnects: the token
is yours.

Sections must follow top-level settings: a plain key written after `[discord]`
belongs to that section. Telegram is optional. With both configured, each
network reports its connected account in window 0.

Outside sections, superscript `ᵗ` and `ᵈ` indicate the network in the sidebar.
Direct messages use the other person’s name. Server text channels are grouped
by server and sorted by channel name within it. Network and server headers
appear when needed; click a header or use `/fold` to collapse it. A collapsed
header shows unread activity and whether it contains an open window. The state
is saved in `sidebar.toml`. One network without a server needs no section headers.

| Command or key | Effect |
| --- | --- |
| `/net`, Shift+F2 | Cycle all networks, then each network in name order. |
| `/net discord` | Show Discord in the sidebar and aggregate view. |
| `/net telegram` | Show Telegram. |
| `/net all` | Remove the filter. |
| `/discord`, `/telegram` | Network status: not started, connecting, connected as X. |
| `/discord login` | Log in again: the QR code, or `token_cmd` run again when it is set. |
| `/discord logout` | End the session on the server and forget the token file; with `token_cmd`, only disconnect. |
| `/telegram login` | Start Telegram again after a logout or a fatal error: QR, or phone and code. |
| `/telegram logout` | End the session on the server (it leaves **Telegram → Settings → Devices**) and disconnect. |

A network whose connection ends with an error (a revoked token, a closed
session) says so in window 0 and is stopped, not the client: `/discord login`
or `/telegram login` starts it again.

Filtering does not close windows. In the sidebar’s window mode, window 0 stays
visible and F2 cycles network filters before hiding the panel. With one network,
`/net` simply reports it.

Available on Discord:

- Direct messages, group DMs and server text/announcement channels.
- Sending, replies, editing, deletion and Discord Markdown.
- Paged history and disk cache, reactions and reaction participants.
- Typing, synchronization of your own read position, attachments, images and
  uploads with `/send`. This is not a read receipt from the other person.
- F3 members and presence: online, idle and do-not-disturb states. Server member
  lists use the members currently available to the client.
- Deleting a DM; deleting a group DM leaves that group.
- Conversation search and global search across servers plus the ten most recent
  DMs, with a 15-second global-search deadline.
- Ctrl+G GIF search through Discord's provider (currently KLIPY). Sending a GIF
  posts its page URL; supported received GIFs animate in the conversation.

Current limits:

- No threads, forums, voice channels or categories in the list.
- No `/whois`, contact directory or resolution of an unknown Discord username.
- No other-person read receipts, stickers or location cards.
- Leave/report/block actions are hidden where unsupported. Closing a DM window
  only closes the local window. Deleting a server channel conversation is
  unsupported and records a warning in `/debug`.
- No startup sweep of all Discord history. Open a conversation to load its
  history; the disk cache retains what has already been fetched.

Other unsupported actions report their limit in the window.

### Themes

```text
/theme list catppuccin
/theme Catppuccin Mocha
```

Tab completes names. A theme applies immediately and is saved in `config.toml`.
Theme files are read from `~/.config/ttyloom/themes`, `~/.config/ghostty/themes`,
`$GHOSTTY_RESOURCES_DIR/themes` and `/usr/share/ghostty/themes`, using Ghostty’s
`palette = N=#rrggbb`, `background` and `foreground` settings.

`/theme` without arguments opens a filterable picker. Arrows, the wheel or a
click preview themes live. Enter keeps the choice; Escape restores the old theme.

## Using TTYloom

### Windows

Window 0 starts as the status window for connections, logs and command output.
Every open conversation has a numbered window.

| Command or key | Effect |
| --- | --- |
| `/window new` | Create a window and switch to it. |
| `/window new hide` | Create a window without switching. |
| Ctrl+X | Next window; with `/set cycle_mode last_unread`, visit unread windows then return. |
| Alt+1…9, Alt+0 | Switch to windows 1–9 or 0. |
| Alt+Left, Alt+Right | Previous or next window. |
| `/win N`, `/win name` | Switch by number or name. |
| `/N` | Switch to any window number, such as `/5` or `/21`. |
| `/window close`, `/close` | Close the current window, except window 0. |
| `/window list` | List windows and their activity. |
| F2 | Sidebar: conversations, windows, hidden; window mode also cycles networks. |
| Shift+F2 | Cycle the network filter. |
| F3 | Open the member box in the upper-right corner. |
| F4 | Cycle kitty images, half blocks and images off. |
| F5 | Toggle images on hover only. |
| F6 | Toggle aggregate window 0. |
| F7 | Sidebar order: recent, alphabetical, unread. |
| Ctrl+R | Spell correction: Enter applies, `i` ignores, `a` adds, Escape closes. |

For example, create a window, then bind it to a contact:

```text
/window new
/query alice
```

An incoming message for a conversation without a window creates a hidden
window and adds its activity to `[Act: …]` in the status bar.

### Conversations

| Command | Effect |
| --- | --- |
| `/query name`, `/q name` | Bind the current window to a DM, using an exact name or prefix. |
| `/join @channel`, `/j @channel` | Join a public Telegram channel or group and bind the window. |
| `/msg name text`, `/m name text` | Send without switching windows. |
| `/new`, Ctrl+N | Open the new-conversation picker. |
| `/chats` | List conversations, highlighting unread ones. |
| `/net [network]` | Filter the sidebar and aggregate view; `telegram`, `discord`, `all`, or no argument to cycle. |
| `/telegram [status\|login\|logout]`, `/discord [status\|login\|logout]` | One network: its status, a new login (Discord runs `token_cmd` again) or a logout (Telegram ends the session on the server). |
| `/fold [section]` | Toggle a sidebar section by key, such as `telegram` or `discord:Gophers`, or a displayed-name prefix. No argument lists sections and their collapsed/expanded state. |
| `/history N`, `/hist N` | Load N older messages; PgUp at the top also loads older history. |
| `/clear`, `/c` | Clear the current window. |
| `/rename [target] name`, `/unrename [target]` | Set or remove a local chat/contact alias, saved in `aliases.toml`. It applies to the sidebar, status bar, aggregate view, completion and the DM contact’s displayed name. It is not sent to the network. |
| `/help`, `/h` | List commands, keys and options. `/help topic` explains one, including `/help F3` or `/help hover`; Tab completes topics. |
| Text without `/` | Send to the current conversation. |
| `//text` | Send text beginning with `/`. |

The window command also accepts `/w`; `/theme` accepts `/t`, `/open` accepts
`/o`, and `/quit` accepts `/exit`. Quote names with spaces when using `/rename`,
for example `/rename "Friends - General" General`. `/whois` retains Telegram’s
real contact information.

### Media

| Command or key | Effect |
| --- | --- |
| `/open`, `/open N` | Download if needed and open the newest, or Nth-newest, media with `xdg-open`. |
| `/set images halfblock` | Select half blocks; `auto`, `kitty` and `off` are also available. |
| `/view [N]`, `v` on a selected message | Open media in the full-screen viewer. |
| `l` on a downloaded video | Play in the conversation without sound; press again to pause. |
| `s` | Stop playback and return to the first frame. |
| `/set video hidden` | Keep only video labels in the conversation; `show` previews one frame and `autoplay` loops visible downloaded videos. |
| `/send path [caption]` | Send a local file: PNG/JPEG as a photo, MP4 as a video with ffprobe metadata, other formats as documents. Tab completes the path: `~`, relative paths and spaces work, a directory gets its `/` and the next Tab goes on inside it. |
| Ctrl+V | Paste an image through `wl-paste` or `xclip`, then choose send, caption or cancel using the displayed prompt keys. Plain text goes into the editor. |
| Ctrl+G, `/gif [query]` | Search animated GIF previews: Telegram’s `@gif` bot or Tenor through Discord. Trends appear immediately; typing searches. Arrows or the wheel move; Enter or a click sends; Escape closes. |
| `/set auto_media_max_kb 20480` | Raise the automatic download threshold to 20 MiB. |

<a id="viewer"></a>

#### Built-in viewer: zoom, pan and video

Click an inline image, select a message and press `v`, or use `/view [N]`.
Images must be enabled with F4. The viewer works with kitty pixels and Unicode
half blocks.

| Key or gesture | Effect |
| --- | --- |
| `+`, `=`, wheel up | Zoom in. |
| `-`, wheel down | Zoom out. |
| Arrow keys | Pan the visible area. |
| Hold the left mouse button and drag | Pan with the mouse. |
| `0` | Center and fit to the screen again. |
| `l` on a video | Start or pause full-screen playback, without sound. |
| `s` | Stop and return to the first frame. |
| `o` | Open the file externally, or the web page for a link preview. |
| Escape, `q`, or a click without dragging | Close the viewer. |

Zoom ranges from 25% to 800% of the fitted view, by a factor of 1.25 per step.
Other unassigned keys close the viewer. Videos require FFmpeg; playback is
bounded by frame and memory limits. This is silent terminal playback, with
`show`, `hidden` and `autoplay` controlling the conversation view.

Photos, stickers and GIFs below the download threshold are fetched into
`download_dir`; GIFs animate. Recognized Telegram links can show a clickable
site/title label, description and thumbnail. Only HTTP, HTTPS and mailto links
are passed to `xdg-open`. Larger media stay as labels until requested. Files
outside the allowed extension list are saved as `.bin` and cannot be opened
through `o`. The list covers common images, video, audio, office files, archives,
text and PDF. Downloads are created with mode `0600` in `0700` directories.

Image decoding rejects still images over about 40 megapixels and animated GIF
frames over about 1.6 megapixels. GIF previews decode at most 100 frames; the
picker uses at most 40 per thumbnail. Videos use `video_inline_frames`, capped
at 1,000 frames and 200 MiB of decoded PNG per operation. Off-screen decoded
images are released when the 256 MiB target budget is exceeded, then decoded
again from their files when visible. This is not a cap on total process memory.

Kitty images are transmitted when first visible. At most `kitty_images` stay
in the terminal; least recently displayed images are freed first. Alternating
GIF placements avoid black flashes between frames, and stable placement IDs
avoid retransmitting pixels on a simple terminal resize. Font-size changes
trigger decoding for the new cell size.

With `images_hover` or F5, images take no rows in the conversation. A preview
appears beside the label when space allows, otherwise below the message, without
covering the message text itself.

### Cache and synchronization

The default cache is `~/.cache/ttyloom`, or `$XDG_CACHE_HOME/ttyloom`. With
`TTYLOOM_DIR`, it becomes `$TTYLOOM_DIR/cache`, so separate instances can keep
separate identities. Each network has its own directory: `telegram/` and
`discord/`, each with `dialogs.gob` and `history/`. Telegram bots use
`telegram/bot/`.

Cached conversations and history appear before the network connects. Recently
active conversations get hidden windows according to `auto_open_days`; other
history loads when a window is opened. PgUp or the wheel at the top loads older
history from the conversation’s network, normally 100 messages at a time. The
disk cache keeps the most recent `cache_messages` per conversation.

After a Telegram user account connects, TTYloom synchronizes conversations
sequentially: up to 200 recent messages on the first pass, then newer messages
on later starts. Requests are spaced by 150 ms. An error is reported and the
pass continues; window 0 shows progress. Discord does not sweep all history on
startup: opening a conversation fetches its history. Bots do not run a history
sync and can cache only received messages. Media under the threshold are fetched
for open conversations.

A cache belonging to another account is detected after login and ignored.
To rebuild a cache, quit TTYloom and move only the affected network’s directory
to a backup. Adjust the path for `TTYLOOM_DIR` or `XDG_CACHE_HOME`:

```bash
mv ~/.cache/ttyloom/telegram ~/.cache/ttyloom/telegram.backup
```

Choose a backup name that does not already exist. Session files remain in the
configuration directory; rebuilding the cache does not log out the account.

### Sidebar and notifications

F2 cycles conversations, open windows and hidden. With multiple networks,
window mode visits each network filter before hiding; Shift+F2 changes only the
filter. F7, or clicking the sidebar title, cycles recent, alphabetical and
unread order. Window 0 stays first; unbound windows follow in number order.
Sorting changes display order, not actual window numbers.

The sidebar uses `#` for a group, `&` for a channel, `@` for a username and `*`
for the status window. Kitty can show avatars. A long current-chat title scrolls
within the column. Drag the vertical border to resize and save the panel width.

Click a conversation to open it; click a section header or use `/fold` to
collapse it. With hover tracking enabled, the wheel over the sidebar switches
between already-open windows in sidebar order. It skips headers and unopened
chats, does not wrap, and does not create new windows. Arriving in a hidden
pre-opened window loads its history, marks it read and refreshes the member box
just as a click or keyboard switch would.

The `+ new message` row, `/new` or Ctrl+N opens a live-filtered conversation
picker. Telegram contacts and local chats appear immediately; at least three
characters or `@` adds Telegram network search. Enter or a click opens a window.

Right-click a sidebar row for available actions such as close/leave, report or
block, delete, information and search. Availability depends on the network.
Destructive actions ask for confirmation with `y`; another key cancels. Members
have a similar context menu.

F3 opens the member box: contact and presence for DMs, members with admins
marked `★` and online members highlighted in groups, subscriber count for
channels. Click a member to open a DM; click `[x]` to close the box.

The message scrollbar supports clicking to jump and dragging to scroll. Its
track highlights under the pointer, and its thumb becomes a solid block during
mouse interaction. The status bar lists background activity as
`[Act: 2(3),5(1)]`: window number and unread count. Private messages and mentions
can trigger the bell; `/set bell off` disables it. Desktop or terminal
notifications are controlled by `notify` and focus.

### Aggregate window 0 and search

F6 or `/set aggregate on` combines open conversations in window 0. Each message
includes its conversation name, for example `12:01 [alice] <alice> hello`.
Typing replies to the conversation of the last displayed message; the prompt
shows the target. While the terminal has focus, displayed messages are marked
read on their network. Discord synchronizes your read position without exposing
other people’s read receipts.

Ctrl+F starts local search. Typing filters without case or accent sensitivity,
highlights matches and shows the current/total count. Enter moves to the previous
match, Ctrl+N to the next, and Escape closes search.

Press Ctrl+F again for global search. Telegram searches the account; Discord
searches each server and the ten most recent DMs, with a 15-second deadline.
The `/net` filter limits the networks queried. After a 300 ms typing delay,
results from participating networks are merged newest first, with up to 50
results per backend. Use arrows, the wheel or a click to select; Enter opens
the conversation at the message. Ctrl+F returns to local search; Escape closes.

### Drafts, focus and logging

Each window keeps its own draft across window switches. `/me text` sends an
italic action in IRC style.

When the terminal loses focus, the status bar shows an away marker, read
acknowledgments pause and unread counts increase even in the current window.
Returning to the window marks visible messages read. Bell and notifications
depend on absence or activity in other windows.

`/log` toggles conversation logging; `/log on` and `/log off` set it explicitly.
Plain-text lines go to `<log_dir>/<title>-<network>-<id>.log`, for example
`2026-09-05 12:01 <alice> hello`. The title is capped at 40 characters; network
and chat ID distinguish similar titles. `[log]` appears in the status bar.
Renaming a conversation starts a new filename; previous logs keep their names.
Logs contain private conversation content.

```text
/search invoice
```

Server search works for Telegram and Discord, with up to 50 results in a
separate search window bound to the conversation. Selection, reply, reactions
and editing work there. Enter on a selected result, or `g`, jumps to the real
conversation. `/close` closes the search window.

```text
/whois alice
```

Telegram contact information can include name, username, phone, bio, last seen
and common chats. With no argument, `/whois` describes the current private
conversation. A DM status bar can show presence such as online or last seen.
Discord does not support `/whois`.

`/debug` replaces window 0 with the in-memory diagnostic log. F6 or switching
windows leaves it. Library warnings appear there; errors also remain visible
in window 0. This diagnostic view does not write a disk log.

Received code blocks have a vertical border. Opening an unread conversation
positions the view at the unread divider when enabled. Typing indicators are
sent at most once every five seconds.

### Messages

Select with Alt+Up/Down or a click outside links and images. The message gets
a selection marker and clickable action labels. Click it again or press Escape
to deselect. Hover can show the same actions without selecting or shifting text.

The first action emoji is a quick reaction: your existing reaction, the most
popular one, or a choice based on the message. Click to toggle it. Double-click
a message to toggle 👍. Clicking any reaction below a message also toggles it.
The active mouse area controls the wheel: sidebar navigation over the sidebar,
history scrolling over messages. `hover = "off"` disables hover tracking and
sidebar wheel switching; normal message scrolling remains available.

| Key | Effect |
| --- | --- |
| `e` | Edit your message; Enter sends, Escape cancels. |
| `d` | Delete your message after confirmation. |
| `p` | Reply to the selected message. |
| `r` | Choose a supported reaction; choosing it again removes it. Telegram chats can restrict the available set. |
| `i` | Show message details: dates, ID, views, reaction participants and, where supported, read receipts. |
| `o` | Open the message’s media externally. |
| `v` | Open the built-in full-screen viewer. |
| `l`, `s` | Play/pause a video, or stop and reset it. |
| `c` | Copy message text through OSC 52. |
| `g` | Jump to a quoted message, or from a search result to its conversation. Clicking the quote does the same; missing surrounding history is loaded. |
| Escape | Deselect. |

Up on an empty input edits your last message when no other editing/selection
mode is active. Drag across messages to copy a range: plain message text,
without timestamps or indentation, is copied on release.

On Telegram, outgoing messages show sent and read markers, and incoming
messages distinguish unread from read. `i` can show who read a group message,
subject to Telegram’s privacy and server limits. These other-person read
receipts are unavailable on Discord.

Editing starts with the message’s text, without reconstructing its original
formatting. Complete triple-backtick fences work when sending and editing;
restore their delimiters to retain a code block. Discord interprets its Markdown,
while ordinary Telegram input is not a full Markdown editor. A reply takes
precedence over sending a fenced/code-block paste.

### Input line

| Key or gesture | Effect |
| --- | --- |
| Left/Right, Home/End, Ctrl+A/E | Move the cursor; Home/End and Ctrl+A/E apply to the current line in the expanded editor. |
| Ctrl+Left/Right | Move by word. |
| Ctrl+K, Ctrl+U, Ctrl+W | Delete to the end, to the start, or the previous word. |
| Ctrl+T, `/emoji` | Search the emoji picker; arrows or a click choose, Enter inserts, Escape closes. Recent choices are saved. |
| Up/Down | Input history, or vertical movement in the expanded editor. Up on empty input can edit the last sent message. |
| Shift+Enter, Alt+Enter | Insert a line break. Shift+Enter needs kitty keyboard support; Alt+Enter is the fallback. With `multiline` enabled, open the expanded editor. |
| Enter, Ctrl+Enter | Send the draft. |
| Tab | Complete commands, chats (`/query`, `/join`, `/msg`: from the start of any word of a title, `@username` too), windows, settings, themes, help topics, `/send` paths, `/log` and `/telegram`/`/discord` arguments. Several names left and nothing more to add: a second Tab lists them. |
| PgUp/PgDn | Scroll history. |
| Ctrl+L | Repaint the screen. |
| Ctrl+C, `/quit`, `/exit` | Quit. |
| Wheel | Scroll three message lines; at the top, load history. Over the sidebar with hover tracking, switch existing windows. |
| Click a link | Open it with `xdg-open`. |
| Click an inline image | Open the viewer. Shift+click keeps the terminal’s own selection behavior where supported. |
| Ctrl+V | Paste an image with a confirmation prompt, or text into the editor. |

Long output from `/chats`, `/theme list`, `/help` and `/window list` is paged.
Space or Enter shows the next page; `q` shows all; Escape abandons the rest.
The wheel shows all output and returns scrolling control.

Pastes over 64 KiB are rejected. Multiline paste asks before sending: send as
text, send as a code block, or cancel, using the keys shown in your interface
language. In the expanded editor it can instead insert text or a code block
into the draft.

Typing `@` opens member suggestions for people with usernames. Arrows choose,
Tab or Enter inserts, and Escape closes. Spell correction and mention completion
work without leaving the conversation.

```text
/set
/set timestamps off
```

`/set` lists live settings; `/set key value` changes one and saves it.

## Version and limits

This manual describes **1.1.1**. See the [changelog](../CHANGELOG.md) and
[planned work](../TODO.md). Videos play without sound. Portable binaries omit
Hunspell. Discord threads, forums, voice and bot tokens are not supported;
IRC and WhatsApp are not implemented. One configuration directory holds one
account per network. Use separate `TTYLOOM_DIR` paths for separate instances.

## Signing out

Quit TTYloom first. To remove local Telegram sessions:

```bash
rm -f ~/.config/ttyloom/session.json ~/.config/ttyloom/session-bot.json
```

Adjust the path if you use another configuration directory. A user account will
ask for login again; a bot can reconnect while its token remains configured.
Deleting a local file does not revoke another copy. Use **Telegram → Settings →
Devices** to terminate the server-side session when necessary.

Removing `[discord]` disables Discord in TTYloom. Revoke an exposed token through
Discord account settings. The [authentication guide](authentication.md) covers
session and credential handling in more detail.

## Troubleshooting

### No network configured after moving directories

The active `config.toml` must contain your credentials directly in the directory
named by the error, not inside a nested folder created by `mv`. Start with
`TTYLOOM_DIR=/absolute/path/to/config ttyloom` to select the intended directory.
Do not attach a complete configuration to a public issue.

### Viewer controls or videos do not respond

Enable images with F4, open the media with `v`, and wait for the download before
using the wheel or `+`/`-`. Hold the left button to pan: a click without dragging
closes the viewer. For videos, install `ffmpeg` and `ffprobe`, then press `l`.
Playback has no audio.

### Spell checking stays disabled

Portable archives use `nospell`. Build normally with Hunspell, install the
dictionaries, then try `/set spell fr+en_US`. Installing dictionaries alone
does not add Hunspell support to a `nospell` executable.

### Window 0 fills with warnings

Telegram library warnings belong in `/debug`; errors still appear in window 0.
The debug view can show `FLOOD_WAIT` or peer-resolution warnings. A flood wait
pauses network name resolution for the server’s requested interval, with one
notice describing that pause. It does not mean that credentials are missing.

### Alt+A does not work

Desktop environments may intercept Alt+letter combinations. Use F6 for the
aggregate view and F4 for image mode; those are the supported function-key
shortcuts.

### Reaction unavailable here

Telegram has a supported reaction set that each chat can restrict. The picker
offers permitted reactions. In Saved Messages, reactions are tags and require
Telegram Premium; the client reports that restriction when applicable.

### Images use half blocks in Ghostty

TTYloom probes the terminal at startup. If a multiplexer does not relay the
kitty graphics response, it falls back to half blocks. This depends on the
terminal and multiplexer configuration. Try Ghostty or kitty directly to
isolate the cause. The startup diagnostic line reports cell dimensions, graphics
support, keyboard support and the selected image mode.

If graphics support is true but the cell size is `0x0`, the terminal may not
have supplied dimensions yet. Resizing supplies them and restores the selected
mode, with visible media decoded again. F4 can also change mode manually.

### Shift+Enter does not insert a newline

Shift+Enter needs kitty keyboard support. Check the startup diagnostic line for
the keyboard-probe result. Try running directly in the terminal to see whether
tmux or screen blocks the probe. Alt+Enter provides the newline fallback without
that protocol; pasting multiline text is another option.

### GIFs remain labels

Some media called GIFs by Telegram are actually MP4 clips. Animated WebP also
needs FFmpeg. The label reports the failure if FFmpeg is missing; install it
and request the media again. Actual GIF files are decoded in Go.

### BOT_METHOD_INVALID in window 0

A Telegram bot attempted a user-only operation, commonly history access, or an
operation on a user who has never contacted the bot. Use a user account for
history and dialog listing. A bot must wait for a user to contact it first.

### Missing api_id or api_hash

For Telegram, create an application at [my.telegram.org/apps](https://my.telegram.org/apps)
and edit the active configuration, or supply `TG_API_ID` and `TG_API_HASH`.
Check whether a `[telegram]` section overrides top-level values. If you only
want Discord, omit Telegram credentials and configure `[discord] token_cmd`.

## Sources

- [Telegram API](https://core.telegram.org/api)
- [Kitty graphics protocol](https://sw.kovidgoyal.net/kitty/graphics-protocol/)
- [Account setup](authentication.md)
- [Contribution guide](../CONTRIBUTING.md)
- [Security policy](../SECURITY.md)
- [Project license](../LICENSE.md) and [third-party notices](../THIRD_PARTY_NOTICES.md)
