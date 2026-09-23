package ui

import (
	"github.com/govlog/ttyloom/internal/i18n"
	"strings"
	"testing"

	"github.com/govlog/ttyloom/internal/render"
)

func TestHelpTopics(t *testing.T) {
	u, _ := ircUI() // the real IRC module: its commands need topics too
	u.goTo(1)       // an IRC room: its context commands count
	topics := u.topics()
	names := map[string]bool{}
	for _, tp := range topics {
		if names[tp.name] {
			t.Fatalf("duplicate name: %q", tp.name)
		}
		names[tp.name] = true
		for _, suffix := range []string{"_name", "_short", "_long"} {
			if k := tp.key + suffix; i18n.T(k) == k {
				t.Errorf("topic %s: no text %q", tp.name, k)
			}
		}
	}

	for _, cmd := range u.commandNames().all() {
		if !names[cmd] {
			t.Errorf("command without topic: %s", cmd)
		}
	}

	for _, key := range setKeys {
		if !names[key] {
			t.Errorf("/set key without topic: %s", key)
		}
	}

	lines := helpLines(topics, u.sections(), 80)
	if len(lines) < len(topics) {
		t.Fatalf("helpLines: %d lines for %d topics", len(lines), len(topics))
	}
}

// TestHelpKeyOAndOpen : "o" and "open" are two topics, not one — /help o gives
// the palette key (which names /open N, its command form), /help open gives the
// command.
func TestHelpKeyOAndOpen(t *testing.T) {
	key := helpText(helpTopic(helpTopics, "o", 200))
	if !strings.HasPrefix(key, "*** o\n") {
		t.Fatalf("/help o: %q", key)
	}
	if !strings.Contains(key, "/open") {
		t.Fatalf("/help o never names the command: %q", key)
	}
	if cmd := helpText(helpTopic(helpTopics, "open", 200)); !strings.HasPrefix(cmd, "*** /open") {
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
