package hook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// script writes body as a shell script and gives a hook that runs it through
// sh with args: a file just written is never exec'd itself (ETXTBSY).
func script(t *testing.T, body string, args ...string) Hook {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(p, []byte(body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return Hook{Name: "t", Argv: append([]string{"sh", p}, args...), Timeout: 5 * time.Second}
}

// A run gets the text on stdin, the environment of ttyloom plus its own, its
// arguments, and the configuration directory as working directory; its stdout
// comes back as it is.
func TestRunOutput(t *testing.T) {
	t.Setenv("TTYLOOM_KEPT", "k")
	h := script(t, `in=$(cat); echo "$in/$TTYLOOM_KEPT/$TTYLOOM_X/$1"; pwd -P`, "arg")
	out, err := Run(context.Background(), h, []string{"TTYLOOM_X=x"}, "hello")
	dir, _ := filepath.EvalSymlinks(os.Getenv("TTYLOOM_DIR"))
	if err != nil || out != "hello/k/x/arg\n"+dir+"\n" {
		t.Fatalf("out %q, err %v", out, err)
	}
}

// A non-zero exit fails with its status and the first line of stderr.
func TestRunExit(t *testing.T) {
	_, err := Run(context.Background(), script(t, "echo first >&2\necho second >&2\nexit 3"), nil, "")
	if err == nil || !strings.Contains(err.Error(), "3") || !strings.Contains(err.Error(), "first") ||
		strings.Contains(err.Error(), "second") {
		t.Fatalf("err %v", err)
	}
}

// A program that is not there fails and says which.
func TestRunNotFound(t *testing.T) {
	_, err := Run(context.Background(), Hook{Argv: []string{"ttyloom-no-such-program"}, Timeout: time.Second}, nil, "")
	if err == nil || !strings.Contains(err.Error(), "ttyloom-no-such-program") {
		t.Fatalf("err %v", err)
	}
}

// Past its timeout the run is killed with the processes it started: a child
// that would write a file later never does.
func TestRunTimeout(t *testing.T) {
	late := filepath.Join(t.TempDir(), "late")
	h := script(t, "(sleep 0.3; echo late > \"$1\") &\nsleep 30", late)
	h.Timeout = 100 * time.Millisecond
	start := time.Now()
	_, err := Run(context.Background(), h, nil, "")
	if err == nil || !strings.Contains(err.Error(), "100ms") || time.Since(start) > 3*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(late); err == nil {
		t.Fatal("a child outlived the timeout")
	}
}

// Past 64 KiB on stdout the run is killed at once and fails.
func TestRunOutputCap(t *testing.T) {
	h := script(t, `head -c 70000 /dev/zero | tr '\0' a`+"\nsleep 30")
	start := time.Now()
	_, err := Run(context.Background(), h, nil, "")
	if err == nil || !strings.Contains(err.Error(), "64") || time.Since(start) > 3*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

// A program that leaves a child in the background succeeds once it is done,
// without waiting for the child that still holds stdout.
func TestRunBackgroundChild(t *testing.T) {
	start := time.Now()
	out, err := Run(context.Background(), script(t, "sleep 5 &\necho done"), nil, "")
	if err != nil || out != "done\n" || time.Since(start) > 4*time.Second {
		t.Fatalf("out %q, err %v after %v", out, err, time.Since(start))
	}
}

// Clean : escape sequences go whole, CRLF becomes LF, any other control
// character a space, the blanks around go.
func TestClean(t *testing.T) {
	in := "\x1b[31mhot\x1b[0m\r\nline\x1b]8;;http://x\x07 2\x1b]8;;\x07\n\x07"
	if got := Clean(in); got != "hot\nline 2" {
		t.Errorf("Clean(%q) = %q", in, got)
	}
	if got := Clean(" \r\n\t "); got != "" {
		t.Errorf("blank output: %q", got)
	}
}
