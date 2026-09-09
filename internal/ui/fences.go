package ui

import (
	"strings"

	"github.com/govlog/ttyloom/internal/model"
)

// ``` fences of the draft (multiline paste inserted as a code block). The
// fence lines are dropped at the send; the content goes out as a pre entity.
// A fence opens on a line of ``` plus an optional language, and closes on a
// line of ``` alone. No complete pair → nil: the text goes out raw.

func parseFences(s string) []model.Seg {
	lines := strings.Split(s, "\n")
	var segs []model.Seg
	var plain []string
	found := false
	flush := func() {
		if len(plain) > 0 {
			segs = append(segs, model.Seg{Text: strings.Join(plain, "\n")})
			plain = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if !strings.HasPrefix(l, "```") || strings.Contains(l[3:], "`") {
			plain = append(plain, l)
			continue
		}
		close := -1
		for j := i + 1; j < len(lines); j++ {
			if lines[j] == "```" {
				close = j
				break
			}
		}
		if close < 0 { // fence left open: kept as plain text
			plain = append(plain, l)
			continue
		}
		found = true
		flush()
		segs = append(segs, model.Seg{Text: strings.Join(lines[i+1:close], "\n"), Lang: strings.TrimSpace(l[3:]), Kind: model.SegPre})
		i = close
	}
	if !found {
		return nil
	}
	flush()
	return segs
}

// fenceText gives the text really sent: the fences gone, one line break
// between the blocks kept by the plain segments around them.
func fenceText(segs []model.Seg) string {
	var b strings.Builder
	for i, s := range segs {
		if model.SegBreak(segs, i) {
			b.WriteString("\n")
		}
		b.WriteString(s.Text)
	}
	return b.String()
}

// fenceEntities : pre and style spans of the local echo, offsets in runes
// like the rest of the model.
func fenceEntities(segs []model.Seg) []model.Span {
	var ents []model.Span
	off := 0
	for i, s := range segs {
		if model.SegBreak(segs, i) {
			off++ // the "\n" of fenceText
		}
		n := len([]rune(s.Text))
		add := func(k model.SpanKind) {
			ents = append(ents, model.Span{Start: off, End: off + n, Kind: k, Lang: s.Lang})
		}
		if s.Kind == model.SegPre {
			add(model.SpanPre)
		}
		for _, k := range []struct {
			on bool
			k  model.SpanKind
		}{{s.Bold, model.SpanBold}, {s.Italic, model.SpanItalic}, {s.Underline, model.SpanUnderline}} {
			if k.on {
				add(k.k)
			}
		}
		off += n
	}
	return ents
}
