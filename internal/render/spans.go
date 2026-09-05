// Package render turns Telegram messages into lines of styled spans.
package render

import (
	"net/url"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/theme"
)

type Span struct {
	Text  string
	Style theme.Style
}

type Line struct {
	Spans []Span
	Img   *Img // kitty image block; Row = rank of the line in the block
	// Hover : media of the message in images_hover mode — no line is kept free,
	// Col holds the end of the label and Cols/Rows the size the image would have
	// had inline. draw() uses it to paint on top on hover.
	Hover *Img
	// Actions : clickable areas of the line (palette of the selected message,
	// reactions). Columns relative to the start of the line.
	Actions []Action
	// Avatar : peer whose avatar goes on 2 cells at AvatarCol (gutter kept free
	// by Message or by the sidebar). 0 = none.
	Avatar    int64
	AvatarCol int
}

// Action : clickable area [Col0, Col1). Key is the matching key of the
// palette; KeyReact also carries the emoji of the reaction clicked, KeyJump
// the id of the message aimed at.
type Action struct {
	Col0, Col1 int
	Key        rune
	Emoji      string
	ID         int
}

const (
	KeyReact = 'R'    // click on a reaction: toggles Emoji
	KeyEsc   = '\x1b' // click on "Esc": drops the selection
	KeyJump  = 'g'    // quote of a reply, "g" of the palette: jump to message ID
	KeyTicks = 'T'    // hover zone of the ✓/✓✓ tick of my messages: never a click
	KeyView  = 'V'    // click on the label of a photo, video, GIF or map: the preview
)

type Img struct {
	Media           *model.Media
	Col, Cols, Rows int
	Row             int // 0 = first line of the block, the only one that carries the placement
}

// CleanLine : Clean on a single line — the breaks become spaces. For
// everything that does not wrap (the status bar).
func CleanLine(s string) string { return strings.ReplaceAll(Clean(s), "\n", " ") }

// Clean neutralises the control characters (terminal sequence injection, OSC
// 8 included): every rune < 0x20 but '\n', 0x7f and U+0080–U+009F becomes a
// space, and so do the bidi overrides and isolates (U+202A–U+202E,
// U+2066–U+2069), which make "gpj.exe" read "exe.jpg". Rune for rune, so the
// span offsets stay valid.
func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && unsafeRune(r) {
			return ' '
		}
		return r
	}, s)
}

// unsafeRune : what Clean blanks and what SafeURL refuses.
func unsafeRune(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) ||
		(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

// Fold gives lower case without diacritics, so an accented word and its plain
// form compare equal.
func Fold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// maxURL : above that, the URL is no longer a URL — it goes neither into OSC
// 8 nor under xdg-open.
const maxURL = 2048

// SafeURL tells whether a URL can be trusted by the terminal (OSC 8 link) and
// by the desktop (xdg-open). It comes from the network: no control character
// (it would go out as it is in the sequence), and only http, https and mailto
// — never file:, javascript: nor an exotic handler. url.Parse puts the scheme
// in lower case.
func SafeURL(s string) bool {
	if s == "" || len(s) > maxURL || hasControl(s) {
		return false
	}
	v, err := url.Parse(s)
	if err != nil {
		return false
	}
	switch v.Scheme {
	case "http", "https", "mailto":
		return true
	}
	return false
}

func hasControl(s string) bool {
	for _, r := range s {
		if unsafeRune(r) {
			return true
		}
	}
	return false
}

// Runs applies the style spans (offsets in runes) to the text. Clean keeps
// the rune count, so the offsets stay valid.
func Runs(text string, ents []model.Span, base theme.Style, th theme.Theme) []Span {
	text = Clean(text)
	runes := []rune(text)
	styles := make([]theme.Style, len(runes))
	for i := range styles {
		styles[i] = base
	}
	quote := map[int]bool{}
	preLead := map[int]bool{} // rank of the first rune of each line of a Pre block: "┃ " to put in front
	for _, e := range ents {
		ri, rj := max(0, e.Start), min(len(runes), e.End)
		if ri >= rj {
			continue
		}
		var url string
		switch e.Kind {
		case model.SpanURL:
			if SafeURL(e.URL) { // scheme and control characters: what goes out in OSC 8 and under xdg-open
				url = e.URL
			}
		case model.SpanQuote:
			quote[ri] = true
		case model.SpanPre:
			preLead[ri] = true
			for i := ri; i < rj; i++ {
				if runes[i] == '\n' && i+1 < rj {
					preLead[i+1] = true
				}
			}
		}
		for i := ri; i < rj; i++ {
			s := &styles[i]
			switch e.Kind {
			case model.SpanBold:
				s.Bold = true
			case model.SpanItalic:
				s.Italic = true
			case model.SpanUnderline:
				s.Underline = true
			case model.SpanStrike:
				s.Strike = true
			case model.SpanCode, model.SpanPre:
				s.FG, s.BG = th.Color(theme.Code), th.Color(theme.CodeBG)
			case model.SpanSpoiler:
				s.Dim, s.Reverse = true, true
			case model.SpanQuote:
				s.Dim, s.Italic = true, true
			case model.SpanURL:
				s.FG, s.Underline, s.Curly = th.Color(theme.Link), true, theme.Curly
				if url != "" {
					s.URL = url
				}
			case model.SpanMention:
				s.FG = th.Color(theme.Mention)
			}
		}
	}
	// One span per run of equal style, its "│ " / "┃ " lead in front; the
	// text of a run is built once, not grown rune by rune (quadratic on a long
	// message with no entity).
	var spans []Span
	lead := "" // marks of the line the run starts
	start := 0 // first rune of the run being built
	flush := func(end int) {
		if end > start {
			spans = append(spans, Span{lead + string(runes[start:end]), styles[start]})
		}
		lead, start = "", end
	}
	for i := range runes {
		if quote[i] || preLead[i] {
			flush(i)
			if quote[i] {
				lead += "│ "
			}
			if preLead[i] {
				lead += "┃ "
			}
		} else if styles[i] != styles[start] {
			flush(i)
		}
	}
	flush(len(runes))
	return spans
}
