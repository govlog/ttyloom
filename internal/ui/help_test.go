package ui

import (
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/render"
)

func TestHelpTopics(t *testing.T) {
	names := map[string]bool{}
	for _, tp := range helpTopics {
		if names[tp.name] {
			t.Fatalf("duplicate name: %q", tp.name)
		}
		names[tp.name] = true
	}

	for _, cmd := range commandNames {
		found := false
		for _, tp := range helpTopics {
			if tp.name == cmd {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("command without topic: %s", cmd)
		}
	}

	for _, key := range setKeys {
		found := false
		for _, tp := range helpTopics {
			if tp.name == key {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("/set key without topic: %s", key)
		}
	}

	lines := helpLines(80)
	if len(lines) < len(helpTopics) {
		t.Fatalf("helpLines: %d lines for %d topics", len(lines), len(helpTopics))
	}
}

// TestHelpKeyOAndOpen : "o" and "open" are two topics, not one — /help o gives
// the palette key (which names /open N, its command form), /help open gives the
// command.
func TestHelpKeyOAndOpen(t *testing.T) {
	key := helpText(helpTopic("o", 200))
	if !strings.HasPrefix(key, "*** o\n") {
		t.Fatalf("/help o: %q", key)
	}
	if !strings.Contains(key, "/open") {
		t.Fatalf("/help o never names the command: %q", key)
	}
	if cmd := helpText(helpTopic("open", 200)); !strings.HasPrefix(cmd, "*** /open") {
		t.Fatalf("/help open: %q", cmd)
	}
}

// helpText : the lines of a help topic joined back into plain text.
func helpText(lines []render.Line) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = body(l)
	}
	return strings.Join(out, "\n")
}
