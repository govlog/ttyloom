package ui

import (
	"testing"

	"github.com/govlog/ttyloom/internal/model"
)

func TestParseFences(t *testing.T) {
	if parseFences("pas de bloc") != nil || parseFences("```\nouvert sans fin") != nil {
		t.Fatal("false positive")
	}
	segs := parseFences("a\n```go\ncode\n```\nb")
	if len(segs) != 3 || segs[1].Kind != model.SegPre || segs[1].Lang != "go" || segs[1].Text != "code" {
		t.Fatalf("segments: %+v", segs)
	}
	if got := fenceText(segs); got != "a\ncode\nb" {
		t.Fatalf("text: %q", got)
	}
	ents := fenceEntities(segs)
	if len(ents) != 1 {
		t.Fatalf("entities: %+v", ents)
	}
	if pre := ents[0]; pre.Start != 2 || pre.End != 6 || pre.Kind != model.SpanPre || pre.Lang != "go" {
		t.Fatalf("span pre: %+v", pre)
	}
	// A fence alone: no plain segment around.
	segs = parseFences("```\nseul\n```")
	if len(segs) != 1 || fenceText(segs) != "seul" {
		t.Fatalf("solo block: %+v", segs)
	}
}
