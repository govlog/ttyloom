package tgc

import (
	"testing"
	"time"

	"github.com/gotd/td/tg"
)

// TestParticipantsLines : admins first with ★, the (me) mark, /query by
// @username when there is one and by id otherwise, and online only while the
// TTL of the status runs.
func TestParticipantsLines(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	users := map[int64]*tg.User{
		1: {ID: 1, Username: "alice", Status: &tg.UserStatusOnline{Expires: 1_000_060}},
		2: {ID: 2, Status: &tg.UserStatusOnline{Expires: 999_900}}, // TTL expired
		3: {ID: 3, Username: "moi"},
	}
	names := map[int64]string{1: "alice", 2: "Bob Sans Pseudo", 3: "moi"}
	got := participantLines([]int64{2, 1, 3}, map[int64]bool{1: true}, users, 3, now,
		func(id int64) string { return names[id] })
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	if got[0].Text != "★ alice" || !got[0].Online || got[0].Query != "@alice" {
		t.Fatalf("admin first: %+v", got[0])
	}
	if got[1].Text != "Bob Sans Pseudo" || got[1].Online || got[1].Query != "2" {
		t.Fatalf("no @username: %+v", got[1])
	}
	if got[2].Text != "moi (moi)" {
		t.Fatalf("me: %+v", got[2])
	}
}
