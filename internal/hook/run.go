package hook

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/govlog/ttyloom/internal/config"
	"github.com/govlog/ttyloom/internal/i18n"
	"github.com/govlog/ttyloom/internal/render"
)

// maxOut : the most a run may write on stdout; past it the run is killed and fails.
const maxOut = 64 << 10

// Run starts h with no shell, in the configuration directory, with the
// environment of ttyloom plus env and stdin on its standard input, and gives
// its stdout. The run and its children are one process group: past the
// timeout, or past maxOut on stdout, the whole group is killed. The error
// says why: output too big, timeout, a non-zero exit (with the first line of
// stderr), or a program that could not start.
func Run(ctx context.Context, h Hook, env []string, stdin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.Argv[0], h.Argv[1:]...)
	cmd.Dir = config.Dir()
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	out, errOut := &capped{limit: maxOut, over: cancel}, &capped{limit: 4 << 10}
	cmd.Stdout, cmd.Stderr = out, errOut
	// A group of its own: the kill takes the children too, and a program that
	// reads the terminal is stopped (SIGTTIN) instead of taking the keys of the UI.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	// A child left in the background (mpv ding.ogg &) holds stdout open: Wait
	// stops waiting for it after this delay, the program itself being done.
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	var exit *exec.ExitError
	switch {
	case out.full:
		return "", i18n.Error("hook_err_big")
	case err == nil:
		return out.String(), nil
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "", i18n.Error("hook_err_timeout", h.Timeout)
	case errors.As(err, &exit):
		if l := firstLine(escSeq.ReplaceAllString(errOut.String(), "")); l != "" {
			return "", i18n.Error("hook_err_exit_said", exit.ExitCode(), l)
		}
		return "", i18n.Error("hook_err_exit", exit.ExitCode())
	}
	return "", i18n.Error("hook_err_start", err)
}

// capped keeps the first limit bytes written to it. Past them it drops the
// rest, sets full and calls over once: a run that floods stdout is killed
// rather than read to the end. The buffer is a field, not embedded: an
// embedded bytes.Buffer brings its ReadFrom, which io.Copy (os/exec) takes
// over Write — the cap would never run.
type capped struct {
	buf   bytes.Buffer
	limit int
	full  bool
	over  func()
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); len(p) > room {
		c.buf.Write(p[:max(room, 0)])
		if !c.full && c.over != nil {
			c.over()
		}
		c.full = true
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *capped) String() string { return c.buf.String() }

// firstLine : the first line of s that is not blank, for an error message.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// escSeq : an escape sequence, whole — CSI (colours), OSC (title, link), or a
// two-character one. render.Clean alone would blank the ESC and leave "[31m".
var escSeq = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)?|[ -/]*[0-~])`)

// Clean makes the output of a run into text for a chat: escape sequences go
// whole, CRLF becomes LF, any other control character a space
// (render.Clean), and the blanks around go.
func Clean(out string) string {
	out = strings.ReplaceAll(escSeq.ReplaceAllString(out, ""), "\r\n", "\n")
	return strings.TrimSpace(render.Clean(out))
}
