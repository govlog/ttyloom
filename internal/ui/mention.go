package ui

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/model"
	"github.com/govlog/ttyloom/internal/render"
)

// Mentions: the @… candidates of the completion box (popbox.go) and the
// mention by id of a member with no username.

// mentionFilter keeps the members whose @username or folded name starts with
// q. A member with no username (Query = id) goes out as a mention by id.
func mentionFilter(all []model.Participant, q string) []model.Participant {
	q = render.Fold(q)
	var out []model.Participant
	for _, p := range all {
		if p.Query == "" {
			continue // header or "loading…" line
		}
		if q == "" || (mentionByID(p) == 0 && strings.HasPrefix(render.Fold(strings.TrimPrefix(p.Query, "@")), q)) || strings.HasPrefix(render.Fold(mentionName(p)), q) {
			out = append(out, p)
		}
	}
	return out
}

// mentionByID gives the user id of a member with no @username, 0 otherwise.
func mentionByID(p model.Participant) int64 {
	if strings.HasPrefix(p.Query, "@") {
		return 0
	}
	id, _ := strconv.ParseInt(p.Query, 10, 64)
	return id
}

// mentionInsert : what the pick puts in the draft — @username, or @Name for
// a member with no username (mentionSegs turns it into a mention by id).
func mentionInsert(p model.Participant) string {
	if mentionByID(p) != 0 {
		return "@" + mentionName(p)
	}
	return p.Query
}

// mentionName : the name of a member without the marks of the box (★, " (me)");
// Text for a line that has no Name.
func mentionName(p model.Participant) string { return cmp.Or(p.Name, p.Text) }

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// mentionSegs turns every "@Name" of the plain segments naming a cached
// member with no username into a SegMention (the @ dropped, as the Telegram
// clients show it). ok=false when nothing changed.
func (u *UI) mentionSegs(c *model.Chat, segs []model.Seg) ([]model.Seg, bool) {
	var names []model.Participant
	for _, p := range u.partsCache[c.Key()].lines {
		if mentionByID(p) != 0 {
			p.Text = mentionName(p) // a copy: the box keeps its marks
			names = append(names, p)
		}
	}
	if len(names) == 0 {
		return segs, false
	}
	// Longest first: "Bob" must not take the "@Bobby" of another member.
	slices.SortFunc(names, func(a, b model.Participant) int { return len(b.Text) - len(a.Text) })
	var out []model.Seg
	found := false
	for _, s := range segs {
		if s.Kind != model.SegPlain {
			out = append(out, s)
			continue
		}
		rs := []rune(s.Text)
		from := 0
		for i := 0; i < len(rs); i++ {
			if rs[i] != '@' {
				continue
			}
			rest := string(rs[i+1:])
			for _, p := range names {
				n := len([]rune(p.Text))
				if !strings.HasPrefix(rest, p.Text) || (i+1+n < len(rs) && wordRune(rs[i+1+n])) {
					continue
				}
				if i > from {
					plain := s
					plain.Text = string(rs[from:i])
					out = append(out, plain)
				}
				m := s
				m.Text, m.Kind, m.UserID = p.Text, model.SegMention, mentionByID(p)
				out = append(out, m)
				from, i, found = i+1+n, i+n, true
				break
			}
		}
		if from < len(rs) {
			plain := s
			plain.Text = string(rs[from:])
			out = append(out, plain)
		}
	}
	return out, found
}

// mentionAll gives the raw candidates for c — the peer of a private chat, the
// cached members otherwise (the fetch is fired like loadParts does).
func (u *UI) mentionAll(c *model.Chat) []model.Participant {
	if c.Kind == model.ChatUser {
		if c.Username == "" {
			return nil
		}
		return []model.Participant{{Text: render.CleanLine(u.title(c)), Query: "@" + c.Username}}
	}
	e, ok := u.partsCache[c.Key()]
	if b := u.net(c); b != nil && (!ok || time.Since(e.at) > partsTTL) {
		e = partsEntry{lines: []model.Participant{{Text: i18n.T("loading")}}, at: time.Now()}
		u.partsCache[c.Key()] = e
		b.Participants(u.backendContext(b), c)
	}
	return e.lines
}

// mentionLabel : "@username  Name", the name left out when it repeats the
// username; the name alone for a member with no username. An IRC nick comes
// with no @.
func mentionLabel(p model.Participant) string {
	if mentionByID(p) != 0 || render.Fold(p.Text) == render.Fold(strings.TrimPrefix(p.Query, "@")) {
		return mentionInsert(p)
	}
	return p.Query + "  " + p.Text
}
