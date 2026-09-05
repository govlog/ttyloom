package ui

import (
	"strings"
	"testing"
)

func TestNotifyArgs(t *testing.T) {
	title, body := notifyArgs("hi\x01there;now\nend", strings.Repeat("a", 50)+";"+strings.Repeat("b", 300))
	if title != "hi there,now end" {
		t.Fatalf("title = %q", title)
	}
	want := strings.Repeat("a", 50) + "," + strings.Repeat("b", 149)
	if body != want {
		t.Fatalf("body : longueur %d, attendu %d", len(body), len(want))
	}
}
