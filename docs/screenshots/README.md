# Screenshot fixtures

The screenshots are produced by `TestScreenshots` in `internal/ui/shot_test.go`.
It uses the real UI renderer, three fake backends and fictional conversations;
the GIF tiles are the frames committed by the fixtures step below. The test
connects to no account and reads no private data. The palette is Catppuccin
Mocha; its MIT notice is in `licenses/assets/`.

One file per scene: `main` (the opening view, tab bar on the all tab),
`discord`, `tabs` (the IRC tab, a channel, a WHOIS answer and a window waiting
with a hot counter), `search`, `gifs`, `members`, `newchat` and `picker`.

From the repository root:

```bash
mkdir -p /tmp/ttyloom-shots
TTYLOOM_SHOTS=/tmp/ttyloom-shots go test -count=1 -run TestScreenshots ./internal/ui
for frame in /tmp/ttyloom-shots/*.ansi; do
  name=$(basename "$frame" .ansi)
  python3 docs/screenshots/ansi2svg.py "$frame" "docs/screenshots/$name.svg" "TTYloom · $name"
done
```

Python 3 uses only its standard library. `ansi2svg.py` reads cursor moves,
colours, text and the uncropped PNG placements emitted by the kitty renderer.
The terminal chrome is added by the exporter. The fixtures fix message times
and the status clock; date labels can depend on the day of generation.

Open the SVG files in a browser to inspect them. GIF tiles represent one frame,
so these images do not demonstrate animation speed or a live network session.

## Fixtures

The GIF tiles are real frames, not drawings. `scripts/gifshots.go` asks the
Discord and Telegram GIF pickers of the configured account for a query and
writes the first frame of each result, fitted in 96x54 px like the GIF box of
the UI:

```bash
go run scripts/gifshots.go -out docs/screenshots/fixtures -query cat -n 8
```

This is a separate manual step, run once when the tiles need refreshing; the
screenshot test itself only reads the committed PNG files. It needs the
sessions of the client already in place and ffmpeg, both pickers answering mp4
clips. It writes `gif-discord-<i>.png`, `gif-telegram-<i>.png` and
`SOURCES.md` in the output directory, and nothing else: no token, no session,
no conversation. A network that fails prints its error and the other one goes
on.

Those tiles are frames of third-party GIF returned by the Discord and Telegram
GIF searches. They remain the property of their authors and are used here to
illustrate the documentation only; `SOURCES.md` lists the origin of each one.
