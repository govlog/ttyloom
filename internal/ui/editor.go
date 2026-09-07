package ui

import (
	"slices"
	"strings"
	"unicode"
)

// Editor : input line in the readline style, ↑/↓ history, Tab completion.
type Editor struct {
	buf   []rune
	cur   int
	hist  []string
	hi    int // index in hist while browsing; len(hist) = not browsing
	draft []rune
}

func (e *Editor) String() string { return string(e.buf) }
func (e *Editor) Cursor() int    { return e.cur }
func (e *Editor) Set(s string)   { e.buf = []rune(s); e.cur = len(e.buf) }

func (e *Editor) Insert(s string) {
	r := []rune(s)
	e.buf = slices.Insert(e.buf, e.cur, r...)
	e.cur += len(r)
}

func (e *Editor) Left() {
	if e.cur > 0 {
		e.cur--
	}
}

func (e *Editor) Right() {
	if e.cur < len(e.buf) {
		e.cur++
	}
}

func (e *Editor) Home() { e.cur = 0 }
func (e *Editor) End()  { e.cur = len(e.buf) }

// sep : word boundary of Ctrl+←/→; the line break counts, the word navigation
// never jumps over a line without stopping.
func sep(r rune) bool { return r == ' ' || r == '\n' }

func (e *Editor) WordLeft() {
	for e.cur > 0 && sep(e.buf[e.cur-1]) {
		e.cur--
	}
	for e.cur > 0 && !sep(e.buf[e.cur-1]) {
		e.cur--
	}
}

func (e *Editor) WordRight() {
	for e.cur < len(e.buf) && sep(e.buf[e.cur]) {
		e.cur++
	}
	for e.cur < len(e.buf) && !sep(e.buf[e.cur]) {
		e.cur++
	}
}

// lineBounds gives the current line: start = after the previous \n, end = the
// next \n (or the end of the buffer).
func (e *Editor) lineBounds(pos int) (start, end int) {
	start = pos
	for start > 0 && e.buf[start-1] != '\n' {
		start--
	}
	end = pos
	for end < len(e.buf) && e.buf[end] != '\n' {
		end++
	}
	return start, end
}

func (e *Editor) LineHome() { e.cur, _ = e.lineBounds(e.cur) }
func (e *Editor) LineEnd()  { _, e.cur = e.lineBounds(e.cur) }

// CursorUp moves to the line above, keeping the column when it exists there.
func (e *Editor) CursorUp() {
	start, _ := e.lineBounds(e.cur)
	if start == 0 {
		return
	}
	col := e.cur - start
	ps, pe := e.lineBounds(start - 1)
	e.cur = min(ps+col, pe)
}

func (e *Editor) CursorDown() {
	_, end := e.lineBounds(e.cur)
	if end >= len(e.buf) {
		return
	}
	start, _ := e.lineBounds(e.cur)
	col := e.cur - start
	ns, ne := e.lineBounds(end + 1)
	e.cur = min(ns+col, ne)
}

// Replace swaps the runes [start, end) for s and puts the cursor after s.
func (e *Editor) Replace(start, end int, s string) {
	r := []rune(s)
	e.buf = slices.Concat(e.buf[:start], r, e.buf[end:])
	e.cur = start + len(r)
}

func (e *Editor) Backspace() {
	if e.cur > 0 {
		e.buf = slices.Delete(e.buf, e.cur-1, e.cur)
		e.cur--
	}
}

func (e *Editor) Delete() {
	if e.cur < len(e.buf) {
		e.buf = slices.Delete(e.buf, e.cur, e.cur+1)
	}
}

func (e *Editor) KillToEnd() { e.buf = e.buf[:e.cur] }
func (e *Editor) KillLine()  { e.buf = slices.Delete(e.buf, 0, e.cur); e.cur = 0 }

func (e *Editor) KillWord() {
	i := e.cur
	for i > 0 && e.buf[i-1] == ' ' {
		i--
	}
	for i > 0 && e.buf[i-1] != ' ' {
		i--
	}
	e.buf = slices.Delete(e.buf, i, e.cur)
	e.cur = i
}

func (e *Editor) Up() {
	if e.hi == 0 {
		return
	}
	if e.hi == len(e.hist) {
		e.draft = e.buf
	}
	e.hi--
	e.Set(e.hist[e.hi])
}

func (e *Editor) Down() {
	if e.hi >= len(e.hist) {
		return
	}
	e.hi++
	if e.hi == len(e.hist) {
		e.buf, e.cur = e.draft, len(e.draft)
		return
	}
	e.Set(e.hist[e.hi])
}

// Submit gives the line back, adds it to the history and empties the input.
func (e *Editor) Submit() string {
	s := string(e.buf)
	if s != "" && (len(e.hist) == 0 || e.hist[len(e.hist)-1] != s) {
		e.hist = append(e.hist, s)
	}
	e.hi = len(e.hist)
	e.buf, e.cur, e.draft = nil, 0, nil
	return s
}

// Complete completes the word under the cursor with cands(word, startOfLine):
// only one → candidate + space; several → longest common prefix.
func (e *Editor) Complete(cands func(word string, atStart bool) []string) {
	start := e.cur
	for start > 0 && e.buf[start-1] != ' ' {
		start--
	}
	word := string(e.buf[start:e.cur])
	var matches []string
	for _, c := range cands(word, start == 0) {
		if strings.HasPrefix(strings.ToLower(c), strings.ToLower(word)) {
			matches = append(matches, c)
		}
	}
	if len(matches) == 0 {
		return
	}
	repl := matches[0] + " "
	if strings.HasSuffix(matches[0], "/") { // a directory: the next Tab goes on inside it
		repl = matches[0]
	}
	if len(matches) > 1 {
		repl = commonPrefix(matches)
		if len([]rune(repl)) <= len([]rune(word)) {
			return
		}
	}
	e.buf = slices.Concat(e.buf[:start], []rune(repl), e.buf[e.cur:])
	e.cur = start + len([]rune(repl))
}

func commonPrefix(ss []string) string {
	p := []rune(ss[0])
	for _, s := range ss[1:] {
		r := []rune(s)
		i := 0
		for i < len(p) && i < len(r) && unicode.ToLower(p[i]) == unicode.ToLower(r[i]) {
			i++
		}
		p = p[:i]
	}
	return string(p)
}
