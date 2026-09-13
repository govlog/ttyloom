#!/usr/bin/env python3
"""ansi2svg: render an ANSI frame written by ttyloom (cursor addressing, SGR
with 24-bit colours, OSC 8 links and kitty PNG images) into an SVG terminal window.

    go test -run TestScreenshots ./internal/ui/   # with TTYLOOM_SHOTS=<dir>
    python3 docs/screenshots/ansi2svg.py <dir>/main.ansi docs/screenshots/main.svg "ttyloom — #gophers"

A small virtual terminal: a grid of cells with a style each, filled by the
sequences the client emits. Anything unknown is skipped, never drawn.
"""
import json
import re
import sys
import unicodedata
from dataclasses import dataclass, replace
from html import escape

COLS, ROWS = 112, 34
FG, BG = "#cdd6f4", "#1e1e2e"
CHROME = "#11111b"
PALETTE = ["#45475a", "#f38ba8", "#a6e3a1", "#f9e2af", "#89b4fa", "#f5c2e7", "#94e2d5", "#bac2de",
           "#585b70", "#f38ba8", "#a6e3a1", "#f9e2af", "#89b4fa", "#f5c2e7", "#94e2d5", "#a6adc8"]
CW, CH, FS = 8.0, 17.0, 13.2  # cell width, cell height, font size (px)


@dataclass(frozen=True)
class Style:
    fg: str = ""
    bg: str = ""
    bold: bool = False
    dim: bool = False
    italic: bool = False
    underline: bool = False
    reverse: bool = False
    strike: bool = False


def width(ch):
    if unicodedata.combining(ch) or ch in "‍️​":
        return 0
    return 2 if unicodedata.east_asian_width(ch) in ("W", "F") else 1


def widths(src):
    """The <frame>.widths sidecar TestScreenshots writes: every non-ASCII
    grapheme cluster of the frame with the width the Go renderer gave it. That
    renderer is the source of truth, its own measure decided where the cells
    after it went. Without the file, the exporter measures with unicodedata.
    """
    try:
        with open(re.sub(r"[.]ansi$", "", src) + ".widths", encoding="utf-8") as f:
            return json.load(f)
    except FileNotFoundError:
        return {}


def sgr(style, params):
    ps = params.split(";") if params else ["0"]
    i = 0
    while i < len(ps):
        p = ps[i].split(":")[0] or "0"
        n = int(p)
        if n == 0:
            style = Style()
        elif n == 1:
            style = replace(style, bold=True)
        elif n == 2:
            style = replace(style, dim=True)
        elif n == 3:
            style = replace(style, italic=True)
        elif n == 4:
            style = replace(style, underline=True)
        elif n == 7:
            style = replace(style, reverse=True)
        elif n == 9:
            style = replace(style, strike=True)
        elif n == 22:
            style = replace(style, bold=False, dim=False)
        elif n in (24, 27, 29):
            style = replace(style, underline=False) if n == 24 else replace(style, reverse=False) if n == 27 else replace(style, strike=False)
        elif 30 <= n <= 37:
            style = replace(style, fg=PALETTE[n - 30])
        elif 90 <= n <= 97:
            style = replace(style, fg=PALETTE[n - 90 + 8])
        elif 40 <= n <= 47:
            style = replace(style, bg=PALETTE[n - 40])
        elif 100 <= n <= 107:
            style = replace(style, bg=PALETTE[n - 100 + 8])
        elif n == 39:
            style = replace(style, fg="")
        elif n == 49:
            style = replace(style, bg="")
        elif n in (38, 48, 58) and i + 1 < len(ps):
            mode = ps[i + 1]
            if mode == "2" and i + 4 < len(ps):
                c = "#%02x%02x%02x" % tuple(int(ps[i + 2 + k]) for k in range(3))
                i += 4
            elif mode == "5" and i + 2 < len(ps):
                k = int(ps[i + 2])
                c = PALETTE[k] if k < 16 else FG
                i += 2
            else:
                i += 1
                continue
            if n == 38:
                style = replace(style, fg=c)
            elif n == 48:
                style = replace(style, bg=c)
        i += 1
    return style


def render(data, table=None):
    table = table or {}
    starts = {k[0] for k in table}
    longest = max((len(k) for k in table), default=0)
    grid = [[(" ", Style()) for _ in range(COLS)] for _ in range(ROWS)]
    row = col = 0
    pictures, placements = {}, {}
    pending, chunks = None, []
    saved = (0, 0)
    style = Style()
    i, n = 0, len(data)
    csi = re.compile(r"\x1b\[([?<>0-9;:]*)([A-Za-z@`])")
    while i < n:
        ch = data[i]
        if ch == "\x1b":
            m = csi.match(data, i)
            if m:
                params, final = m.group(1), m.group(2)
                if final == "H":
                    a, _, b = params.partition(";")
                    row, col = max(0, int(a or 1) - 1), max(0, int(b or 1) - 1)
                elif final == "K":
                    for c in range(col, COLS):
                        if row < ROWS:
                            grid[row][c] = (" ", style)
                elif final == "m":
                    style = sgr(style, params)
                elif final == "s":
                    saved = (row, col)
                elif final == "u":
                    row, col = saved
                i = m.end()
                continue
            if data.startswith("\x1b_G", i):
                end = data.index("\x1b\\", i)
                control, _, payload = data[i + 3:end].partition(";")
                keys = dict(part.split("=", 1) for part in control.split(","))
                action = keys.get("a")
                if action == "T":
                    if keys.get("f") != "100" or any(k in keys for k in ("x", "y", "w", "h")):
                        raise ValueError("Screenshot exporter supports uncropped PNG placements only")
                    pending, chunks = (keys, row, col), []
                if pending is not None and payload:
                    chunks.append(payload)
                    if keys.get("m", "0") == "0":
                        first, r, c = pending
                        ident, pid = first["i"], first["p"]
                        pictures[ident] = "".join(chunks)
                        placements[ident, pid] = (r, c, int(first["c"]), int(first["r"]))
                        pending = None
                elif action == "p":
                    placements[keys["i"], keys["p"]] = (row, col, int(keys["c"]), int(keys["r"]))
                elif action == "d":
                    for key in list(placements):
                        if key[0] == keys.get("i") and ("p" not in keys or key[1] == keys["p"]):
                            del placements[key]
                i = end + 2
                continue
            if i + 1 < n and data[i + 1] in "]_P^X":  # OSC / APC / DCS: skip to ST or BEL
                j = data.find("\x1b\\", i)
                k = data.find("\x07", i)
                ends = [x for x in (j, k) if x >= 0]
                i = (min(ends) + (2 if min(ends) == j else 1)) if ends else n
                continue
            i += 1
            continue
        if ch == "\r":
            col = 0
        elif ch == "\n":
            row += 1
        elif ch == "\a":
            pass
        else:
            cl, w = ch, width(ch)
            if ch in starts:  # longest cluster of the table at this position
                for k in range(min(longest, n - i), 0, -1):
                    if data[i:i + k] in table:
                        cl = data[i:i + k]
                        w = table[cl]
                        break
            if w == 0 and col > 0 and row < ROWS:
                c, st = grid[row][col - 1]
                grid[row][col - 1] = (c + cl, st)
            elif row < ROWS and col < COLS:
                grid[row][col] = (cl, style)  # the whole cluster in one cell
                for k in range(1, w):
                    if col + k < COLS:
                        grid[row][col + k] = ("", style)
                col += w
            i += len(cl)
            continue
        i += 1
    images = [(pictures[ident], *rect) for (ident, _), rect in placements.items()]
    return grid, images


def svg(frame, title):
    grid, images = frame
    pad, bar = 16, 34
    w, h = COLS * CW + 2 * pad, ROWS * CH + 2 * pad + bar
    out = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{w:.0f}" height="{h:.0f}" viewBox="0 0 {w:.0f} {h:.0f}" font-family="JetBrains Mono, Fira Code, Cascadia Code, DejaVu Sans Mono, Menlo, Noto Color Emoji, Apple Color Emoji, Segoe UI Emoji, monospace" font-size="{FS}">',
           f'<rect width="{w:.0f}" height="{h:.0f}" rx="12" fill="{CHROME}"/>',
           f'<rect x="{pad}" y="{pad + bar}" width="{COLS * CW:.0f}" height="{ROWS * CH:.0f}" fill="{BG}"/>',
           f'<circle cx="{pad + 10}" cy="{pad + bar / 2 - 2}" r="6" fill="#f38ba8"/><circle cx="{pad + 30}" cy="{pad + bar / 2 - 2}" r="6" fill="#f9e2af"/><circle cx="{pad + 50}" cy="{pad + bar / 2 - 2}" r="6" fill="#a6e3a1"/>',
           f'<text x="{w / 2:.0f}" y="{pad + bar / 2 + 3}" text-anchor="middle" fill="#a6adc8" font-size="12">{escape(title)}</text>']
    for r, cells in enumerate(grid):
        y = pad + bar + r * CH
        c = 0
        while c < COLS:
            ch, st = cells[c]
            start = c
            c += 1
            # a wide cluster takes the next cell as "" and gets a run of its
            # own: squeezed to one cell it would lose the shape of its glyph.
            wide = c < COLS and cells[c][0] == ""
            if wide:
                while c < COLS and cells[c][0] == "":
                    c += 1
            else:
                while c < COLS and cells[c][1] == st and cells[c][0] != "" and not (c + 1 < COLS and cells[c + 1][0] == ""):
                    c += 1
            text = "".join(cells[k][0] for k in range(start, c))
            fg, bg = st.fg or FG, st.bg or BG
            if st.reverse:
                fg, bg = bg, fg
            if st.dim:
                fg = fg + "99"
            width_px = (c - start) * CW
            if bg != BG:
                out.append(f'<rect x="{pad + start * CW:.1f}" y="{y:.1f}" width="{width_px:.1f}" height="{CH}" fill="{bg}"/>')
            if text.strip():
                attrs = f'x="{pad + start * CW:.1f}" y="{y + CH - 4.5:.1f}" fill="{fg}" textLength="{width_px:.1f}" xml:space="preserve"'
                if not wide:
                    attrs += ' lengthAdjust="spacingAndGlyphs"'
                if st.bold:
                    attrs += ' font-weight="bold"'
                if st.italic:
                    attrs += ' font-style="italic"'
                deco = " ".join(d for d, on in (("underline", st.underline), ("line-through", st.strike)) if on)
                if deco:
                    attrs += f' text-decoration="{deco}"'
                out.append(f"<text {attrs}>{escape(text)}</text>")
    for png, row, col, cols, rows in images:
        out.append(f'<image x="{pad + col * CW}" y="{pad + bar + row * CH}" width="{cols * CW}" height="{rows * CH}" preserveAspectRatio="none" href="data:image/png;base64,{png}"/>')
    out.append("</svg>")
    return "\n".join(out)


if __name__ == "__main__":
    src, dst, title = sys.argv[1], sys.argv[2], sys.argv[3] if len(sys.argv) > 3 else "ttyloom"
    with open(src, encoding="utf-8") as f:
        grid = render(f.read(), widths(src))
    with open(dst, "w", encoding="utf-8") as f:
        f.write(svg(grid, title))
