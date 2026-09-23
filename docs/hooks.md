# Hooks

[Manual](guide.md)

A hook runs a program when a message comes in and matches its filters. The
program gets the message; what it prints can be sent as a reply, put in the
input line, shown in the window, or ignored. A command bot, a notification, a
log, a reply suggested by a language model: each is a few lines of
`hooks.toml` and a script.

## The file

`hooks.toml` sits next to `config.toml` (`~/.config/ttyloom/`, or the
directory of `TTYLOOM_DIR`). TTYloom reads it at start and at `/hooks reload`,
and never writes it. With no file there is no hook, and nothing is said.

```toml
[[hook]]
name    = "meteo"                         # required: one word, unique
net     = "irc:libera"                    # a network, or a module: "irc" is every IRC network
chats   = ["#ttyloom"]                    # chat titles, case apart
kinds   = ["group"]                       # "private", "group", "channel"
from    = ["chris"]                       # sender ids; nicks on IRC
match   = '^!meteo\s+(?P<city>[\pL-]+)$'  # a Go regular expression on the text
mention = false                           # the message must mention you
cmd     = "sh ~/bin/meteo.sh"             # required: the program and its arguments
timeout = "10s"                           # 10s by default, 60s at most
reply   = "send"                          # "send", "draft", "display" or "none" (the default)
```

Every key but `name` and `cmd` is optional. A key that is set narrows the
hook; one left out does not filter. Without `match` or `mention`, every
message in the scope fires the hook. For an OR, write two hooks. Every hook
that matches a message runs, in the order of the file.

`cmd` is split on blanks and runs with no shell: no pipe, redirection or
`$VAR` there — write a script for those. `~/` is your home directory; a bare
name is looked up in `PATH`. A script needs its execute bit, or run it through
its interpreter: `cmd = "sh ~/bin/meteo.sh"`.

A hook with a mistake is left out, with a line in window 0 that names it and
says why — an unknown key too, so that a typo (`mentoin = true`) never gives a
hook that filters less than you think. A file that is not valid TOML gives
one line: at start no hook runs, at `/hooks reload` the hooks of before stay.

## What fires a hook

Only a message that arrives live: never the history, the cache or an edit.
And never:

- a message of yours, from any device — a reply of a hook is one, so a hook
  never answers itself;
- a service line (a join, a pin…);
- an IRC NOTICE: by IRC convention nothing answers a notice automatically;
- a message dated more than two minutes ago: the messages caught up after a
  cut.

**The filters.** `net` takes the network of the message or its module.
`chats` compares titles, case apart; a Discord channel is titled
`Server / channel`. `kinds`: `private` is a conversation with one person,
`group` a group or a room, `channel` a broadcast channel. `from` takes the id
of the sender: on Telegram `/whois <name>` shows it; on Discord, turn on
Developer Mode (User Settings → Advanced), then right-click the user → Copy
User ID; on IRC `from` compares nicks, case apart. `mention` is the test of
the notifications — an `@name` or a mention — and on IRC your nick as a word
(`chris: hello`). `match` runs on the text as shown: an IRC action starts
with `* nick `, and a photo with no caption has an empty text.

## The program

It runs in the configuration directory, with the environment of TTYloom
plus:

| Variable | Value |
| --- | --- |
| `TTYLOOM_HOOK` | The name of the hook. |
| `TTYLOOM_NET` | The network: `telegram`, `discord`, `irc:libera`. |
| `TTYLOOM_CHAT`, `TTYLOOM_CHAT_ID`, `TTYLOOM_CHAT_KIND` | Title, id and kind (`private`, `group`, `channel`) of the chat. |
| `TTYLOOM_FROM`, `TTYLOOM_FROM_ID` | Name and id of the sender. |
| `TTYLOOM_MSG_ID` | The id of the message. |
| `TTYLOOM_TEXT` | The text of the message, also on the standard input. |
| `TTYLOOM_MENTION` | `1` when the message mentions you, `0` otherwise. |
| `TTYLOOM_MATCH` | What `match` found. |
| `TTYLOOM_MATCH_1`, `TTYLOOM_MATCH_2`… | Its numbered groups. |
| `TTYLOOM_MATCH_<NAME>` | Its named groups, the name in capitals: `(?P<city>…)` is `TTYLOOM_MATCH_CITY`. |

Its standard output is the reply: 64 KiB at most, the blanks around trimmed.
A run fails on a non-zero exit, past its timeout, past 64 KiB of output, or
when the program cannot start; past its timeout or its 64 KiB it is killed
with every process it started. The failure says why, with the first line of
the standard error. The first failure of a hook writes a line in window 0;
the next ones stay quiet until the hook works again. `/debug` keeps every
failure, `/hooks` the last result.

Each run has its own process: TTYloom never waits for one. A program left in
the background does not hold the run more than a second past the end of the
script, but what it writes to the standard output during that second is part
of the reply: send its output elsewhere (`mpv ding.ogg >/dev/null 2>&1 &`).

Two guards, per hook: at most 4 runs at a time — one message more is skipped
and counted; at most 10 `send` replies a minute — one more is dropped and
counted.

## The reply

The output is cleaned first: terminal escape sequences go whole (the colours
of `curl`), Windows line ends become plain ones, other control characters
become spaces. An empty output does nothing, whatever the mode.

| `reply` | What happens |
| --- | --- |
| `send` | Sent as a normal message in the chat of the message, even when its window is not shown. Your input line, the reply you are preparing and the typing indicator stay as they are. Several lines make one message (one message per line on IRC). A reply too long for the network fails like any send. |
| `draft` | Put in the input line of the chat when it is empty: the input on the screen, or the draft you find when you go to that window. Your own text is never replaced, nor a reply or an edit you are preparing: then the output shows as with `display`. |
| `display` | Each line becomes a line of the window of the chat, `[name] text`. Nothing leaves your machine. |
| `none` | The output is ignored: the hook works for what it does (a notification, a log). |

`draft` and `display` mark the window of the chat as active in the list.

## Commands

| Command | Effect |
| --- | --- |
| `/hooks` | One line per hook: name, filters, reply, runs, failures, skipped messages, dropped replies, last result. |
| `/hooks reload` | Reads `hooks.toml` again. A hook that keeps its name keeps its counters. |
| `/hooks test <name> <text>` | Runs the hook as if you wrote `<text>` in the chat of the window (none in window 0). Only `match` is checked, with its groups; the output shows as with `display`, never sent. |

## Safety

- A hook runs with your rights and the environment of TTYloom, every token or
  key in it included. Give it only programs you trust.
- The text comes from anyone who can write to you. Never hand it to a shell
  (`sh -c "$TTYLOOM_TEXT"`, `eval`), quote every variable in a script, and
  prefer a `match` that only takes what you expect: `[\pL-]+` for a city
  rather than `.+`.
- `from` compares ids, which a sender cannot choose. On IRC it compares
  nicks, and anybody can take a free nick; a nick registered with the
  services of the network is safer, not proof.
- **Discord**: an automatic reply from a user account is a self-bot, against
  the terms of Discord; the account can be closed. TTYloom lets `send`
  through and says so in window 0 when it loads such a hook; `draft` or
  `display` leave the sending to you.
- On IRC each line of a `send` reply is a message: keep replies short. A long
  output takes a while to go out and can get you kicked for flooding.

## Examples

**Ping**, one line of shell:

```toml
[[hook]]
name  = "ping"
match = '^!ping$'
cmd   = "echo pong"
reply = "send"
```

**Weather bot** with a named group, in the IRC rooms:

```toml
[[hook]]
name    = "meteo"
net     = "irc"
kinds   = ["group"]
match   = '^!meteo\s+(?P<city>[\pL-]+)$'
cmd     = "sh ~/bin/meteo.sh"
timeout = "15s"
reply   = "send"
```

```sh
#!/bin/sh
# ~/bin/meteo.sh — the match lets only letters and dashes through.
exec curl -fsS "https://wttr.in/$TTYLOOM_MATCH_CITY?format=3"
```

**Desktop notification** of each private message, nothing sent:

```toml
[[hook]]
name  = "notify"
kinds = ["private"]
cmd   = "sh ~/bin/notify.sh"
```

```sh
#!/bin/sh
exec notify-send -a ttyloom -- "$TTYLOOM_FROM" "$TTYLOOM_TEXT"
```

**Assistant**: a language model suggests a reply to each private message, in
the input line, for you to edit or send:

```toml
[[hook]]
name    = "assist"
kinds   = ["private"]
cmd     = "python3 ~/bin/assist.py"
timeout = "30s"
reply   = "draft"
```

```python
#!/usr/bin/env python3
# ~/bin/assist.py — pip install anthropic; ANTHROPIC_API_KEY in the environment.
import os
import sys

import anthropic

text = sys.stdin.read()
reply = anthropic.Anthropic().messages.create(
    model="claude-sonnet-5",
    max_tokens=300,
    system="Suggest a short reply to this chat message, in its language. Answer with the reply only.",
    messages=[{"role": "user", "content": f"{os.environ['TTYLOOM_FROM']}: {text}"}],
)
print(next(b.text for b in reply.content if b.type == "text"))
```
