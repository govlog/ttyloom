package dsc

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/govlog/ttyloom/internal/model"
)

// Discord markdown, the subset the UI can style. No nesting: a marker closes
// on the next identical one, and inside a run the other markers are text
// ("**a *b* c**" is bold over "a *b* c"). A marker never closed is text as
// well. Mentions and bare URLs still count inside a run; a code run is
// opaque, nothing in it is read.

// markers, longest first: "```" is tried before "`", "**" before "*". opaque
// = the body goes out as it is.
var markers = []struct {
	open   string
	kind   model.SpanKind
	opaque bool
}{
	{"```", model.SpanPre, true},
	{"**", model.SpanBold, false},
	{"__", model.SpanUnderline, false},
	{"~~", model.SpanStrike, false},
	{"||", model.SpanSpoiler, false},
	{"`", model.SpanCode, true},
	{"*", model.SpanItalic, false},
	{"_", model.SpanItalic, false},
}

// Bare link. The only regexp here: the markers are scanned by hand.
var reURL = regexp.MustCompile(`^https?://[^\s<>` + "`" + `]+`)

// parse turns Discord markdown into plain text plus neutral spans, offsets
// in runes over the text returned. names resolves <@id> to a display name;
// nil or unknown gives "@<id>". The spans come out in closing order, the
// inner one first — Runs paints rune by rune, the order does not matter.
func parse(md string, names func(id uint64) string) (text string, spans []model.Span) {
	p := parser{names: names}
	p.scan(md, true)
	return string(p.out), p.spans
}

type parser struct {
	out   []rune
	spans []model.Span
	names func(id uint64) string
}

// scan walks s. styled false = already inside a run: the markers are text
// there, mentions and URLs are not.
func (p *parser) scan(s string, styled bool) {
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("*_~|`\\", s[i+1]) >= 0 {
			p.out = append(p.out, rune(s[i+1]))
			i += 2
			continue
		}
		if styled {
			if n := p.style(s, i); n > 0 {
				i += n
				continue
			}
		}
		if n := p.mention(s[i:]); n > 0 {
			i += n
			continue
		}
		if n := p.link(s[i:]); n > 0 {
			i += n
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		p.out = append(p.out, r)
		i += n
	}
}

// style reads a whole run when s opens one at i, and gives back the bytes
// eaten (0 = no marker here). A marker never closed is copied out as text.
func (p *parser) style(s string, i int) int {
	t := s[i:]
	for _, m := range markers {
		if !strings.HasPrefix(t, m.open) {
			continue
		}
		if m.open[0] == '_' && i > 0 {
			if r, _ := utf8.DecodeLastRuneInString(s[:i]); wordRune(r) {
				continue // glued to a word: part of it, not a marker
			}
		}
		rest := t[len(m.open):]
		end := closeIdx(rest, m.open)
		if end < 0 {
			p.out = append(p.out, []rune(m.open)...)
			return len(m.open)
		}
		body, lang := rest[:end], ""
		if m.kind == model.SpanPre {
			body, lang = fence(body)
		}
		start := len(p.out)
		if m.opaque {
			p.out = append(p.out, []rune(body)...)
		} else {
			p.scan(body, false)
		}
		if len(p.out) > start { // an empty run styles nothing
			p.spans = append(p.spans, model.Span{Start: start, End: len(p.out), Kind: m.kind, Lang: lang})
		}
		return len(m.open) + end + len(m.open)
	}
	return 0
}

// closeIdx finds the marker that closes the run: the first one, except for
// the "_" family where a marker glued to a word closes nothing (foo_bar_baz,
// a_b c_d). -1 = never closed.
func closeIdx(rest, open string) int {
	for i := 0; ; i += len(open) {
		n := strings.Index(rest[i:], open)
		if n < 0 {
			return -1
		}
		i += n
		if open[0] != '_' {
			return i
		}
		// End of the string: DecodeRuneInString gives RuneError, which is no
		// word rune — the marker closes.
		if r, _ := utf8.DecodeRuneInString(rest[i+len(open):]); !wordRune(r) {
			return i
		}
	}
}

// wordRune : what makes an "_" part of a word rather than a marker. The "_"
// itself counts, otherwise the second one of "foo__bar" would open a run.
func wordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// fence splits the body of a ``` block: a first line with no blank is the
// language and leaves with its "\n", and the "\n" in front of the closing
// fence is not part of the code.
func fence(body string) (text, lang string) {
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		if head := body[:i]; !strings.ContainsAny(head, " \t") {
			lang, body = head, body[i+1:]
		}
	}
	return strings.TrimSuffix(body, "\n"), lang
}

// mention reads <@id> and <@!id> (a Mention span over "@name"), <#id>
// (plain "#id": the channel names are not known here) or a custom emoji
// <:name:id> / <a:name:id> (plain ":name:", the convention of emojiOf: a
// text client has no glyph for it). Anything else, a role <@&id> among them,
// stays text.
func (p *parser) mention(s string) int {
	if len(s) < 4 || s[0] != '<' {
		return 0
	}
	end := strings.IndexByte(s, '>')
	if end < 0 {
		return 0
	}
	if name, ok := customEmoji(s[1:end]); ok {
		p.out = append(p.out, []rune(":"+name+":")...)
		return end + 1
	}
	if s[1] != '@' && s[1] != '#' {
		return 0
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(s[2:end], "!"), 10, 64)
	if err != nil {
		return 0
	}
	if s[1] == '#' {
		p.out = append(p.out, []rune("#"+strconv.FormatUint(id, 10))...)
		return end + 1
	}
	name := ""
	if p.names != nil {
		name = p.names(id)
	}
	if name == "" {
		name = strconv.FormatUint(id, 10)
	}
	start := len(p.out)
	p.out = append(p.out, []rune("@"+name)...)
	p.spans = append(p.spans, model.Span{Start: start, End: len(p.out), Kind: model.SpanMention, UserID: int64(id)})
	return end + 1
}

// customEmoji reads the inside of <:name:id> or <a:name:id>: the name.
func customEmoji(s string) (string, bool) {
	s = strings.TrimPrefix(s, "a")
	name, id, ok := strings.Cut(strings.TrimPrefix(s, ":"), ":")
	if !ok || name == "" || !strings.HasPrefix(s, ":") {
		return "", false
	}
	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
		return "", false
	}
	return name, true
}

// link reads a bare https?:// URL. The punctuation that ends a sentence or
// closes a bracket is not part of the target.
func (p *parser) link(s string) int {
	if !strings.HasPrefix(s, "http") {
		return 0
	}
	raw := reURL.FindString(s)
	raw = strings.TrimRight(raw, ".,;:!?)]}")
	if raw == "" {
		return 0
	}
	start := len(p.out)
	p.out = append(p.out, []rune(raw)...)
	p.spans = append(p.spans, model.Span{Start: start, End: len(p.out), Kind: model.SpanURL, URL: raw})
	return len(raw)
}

// mdEscape : the markers of a styled body go out escaped, otherwise a "*" in
// a /me would close the italic run on the Discord side.
var mdEscape = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "~", `\~`, "|", `\|`, "`", "\\`")

// render turns neutral segments back into Discord markdown for a send. Plain
// goes out as it is: Discord reads the markdown the user types, that is the
// convention. One "\n" around a fence, like fenceText. The style markers of
// the runs nest (closed in the reverse order of their opening) and a style
// kept from one run to the next stays open: "**b****__c__**" would break
// Discord's parser.
func render(segs []model.Seg) string {
	var b strings.Builder
	var open []string // markers open, in opening order
	want := func(s model.Seg) []string {
		var out []string
		for _, m := range []struct {
			on bool
			m  string
		}{{s.Bold, "**"}, {s.Italic, "*"}, {s.Underline, "__"}} {
			if m.on {
				out = append(out, m.m)
			}
		}
		return out
	}
	closeAll := func() {
		for i := len(open) - 1; i >= 0; i-- {
			b.WriteString(open[i])
		}
		open = nil
	}
	for i, s := range segs {
		if model.SegBreak(segs, i) {
			closeAll()
			b.WriteString("\n")
		}
		if s.Kind == model.SegPre {
			closeAll()
			b.WriteString("```" + s.Lang + "\n" + s.Text + "\n```")
			continue
		}
		w := want(s)
		// Close down to the first open marker that is not wanted any more.
		keep := 0
		for keep < len(open) && slices.Contains(w, open[keep]) {
			keep++
		}
		for i := len(open) - 1; i >= keep; i-- {
			b.WriteString(open[i])
		}
		open = open[:keep]
		for _, m := range w {
			if !slices.Contains(open, m) {
				b.WriteString(m)
				open = append(open, m)
			}
		}
		if len(open) > 0 {
			b.WriteString(mdEscape.Replace(s.Text))
		} else {
			b.WriteString(s.Text)
		}
	}
	closeAll()
	return b.String()
}
