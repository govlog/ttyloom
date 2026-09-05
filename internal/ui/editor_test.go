package ui

import "testing"

func TestWordNav(t *testing.T) {
	var e Editor
	e.Set("un deux\ntrois")
	e.WordLeft() // from the end: start of "trois"
	if e.Cursor() != 8 {
		t.Fatalf("WordLeft : %d", e.Cursor())
	}
	e.WordLeft() // over the \n: start of "deux"
	if e.Cursor() != 3 {
		t.Fatalf("WordLeft \\n : %d", e.Cursor())
	}
	e.WordLeft()
	e.WordLeft() // at 0: it stays
	if e.Cursor() != 0 {
		t.Fatalf("WordLeft at bound: %d", e.Cursor())
	}
	e.WordRight() // end of "un"
	if e.Cursor() != 2 {
		t.Fatalf("WordRight : %d", e.Cursor())
	}
	e.WordRight()
	e.WordRight()
	e.WordRight() // at the end: it stays
	if e.Cursor() != 13 {
		t.Fatalf("WordRight at bound: %d", e.Cursor())
	}
}

func TestCursorUpDown(t *testing.T) {
	var e Editor
	e.Set("long ligne\nab\nfinale")
	// End of "finale" (20) → up: column 6 > len("ab"): end of "ab".
	e.CursorUp()
	if e.Cursor() != 13 {
		t.Fatalf("CursorUp col bounded: %d", e.Cursor())
	}
	e.CursorUp() // column 2 of "long ligne"
	if e.Cursor() != 2 {
		t.Fatalf("CursorUp : %d", e.Cursor())
	}
	e.CursorUp() // first line: it stays
	if e.Cursor() != 2 {
		t.Fatalf("CursorUp at bound: %d", e.Cursor())
	}
	e.CursorDown() // column 2 of "ab"
	if e.Cursor() != 13 {
		t.Fatalf("CursorDown : %d", e.Cursor())
	}
	e.CursorDown()
	e.CursorDown() // last line: it stays
	if e.Cursor() != 16 {
		t.Fatalf("CursorDown at bound: %d", e.Cursor())
	}
}

func TestLineHomeEnd(t *testing.T) {
	var e Editor
	e.Set("abc\ndef")
	e.LineHome()
	if e.Cursor() != 4 {
		t.Fatalf("LineHome : %d", e.Cursor())
	}
	e.LineEnd()
	if e.Cursor() != 7 {
		t.Fatalf("LineEnd : %d", e.Cursor())
	}
}
