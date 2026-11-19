package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// helperArg : the first argument that makes the test binary the program a
// test runs in the sandbox, in place of ffmpeg (TestMain).
const helperArg = "__media_test_helper"

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == helperArg {
		os.Exit(helper(os.Args[2], os.Args[3:]))
	}
	os.Exit(m.Run())
}

// helper does one thing that a decoder taken over could try, and exits 0
// when it could; the error goes to stderr.
func helper(what string, args []string) int {
	var err error
	switch what {
	case "read":
		var b []byte
		if b, err = os.ReadFile(args[0]); err == nil {
			os.Stdout.Write(b)
		}
	case "write":
		err = os.WriteFile(args[0], []byte("x"), 0o600)
	case "dial":
		var c net.Conn
		if c, err = net.DialTimeout("tcp", args[0], 5*time.Second); err == nil {
			c.Close()
		}
	case "nnp":
		var v int
		v, err = unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
		fmt.Print(v)
	case "alloc": // MiB, two pages touched: no RAM is used when the limit fails
		n, _ := strconv.Atoi(args[0])
		b := make([]byte, n<<20)
		b[0], b[len(b)-1] = 1, 1
	case "spin": // CPU time for a minute
		for start := time.Now(); time.Since(start) < time.Minute; {
		}
	default:
		err = errors.New("unknown helper " + what)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// testJail : the jail of the test binary run as a helper, as ffCmd makes it
// for ffmpeg, with Landlock at abi (0: none).
func testJail(t *testing.T, abi int) (jail, string) {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	j, err := jailFor(bin, abi)
	if err != nil {
		t.Fatalf("jail of %s: %v", bin, err)
	}
	return j, bin
}

// runHelper runs the helper mode what in j and gives its stdout, its stderr
// and its error.
func runHelper(t *testing.T, j jail, what string, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := jailCmd(ctx, j, "helper", append([]string{helperArg, what}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("helper %s: still running after 30 s", what)
	}
	return stdout.String(), stderr.String(), err
}

// The program that runs in the sandbox reads the one input file it is given
// and no other file, writes no file, opens no TCP connection, and has
// no_new_privs: the restrictions the launcher set on its own thread are on
// the program it executed.
func TestSandboxConfinesProgram(t *testing.T) {
	abi, err := landlockABI()
	if err != nil {
		t.Skipf("no Landlock here (%v): TestSandboxFallback covers this kernel", err)
	}
	j, _ := testJail(t, abi)
	dir := t.TempDir()
	input, secret := filepath.Join(dir, "input"), filepath.Join(dir, "secret")
	for _, p := range []string{input, secret} {
		if err := os.WriteFile(p, []byte(filepath.Base(p)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	j.Read = append(j.Read, input)
	denied := func(t *testing.T, what, stderr string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(stderr, "permission denied") {
			t.Fatalf("%s: err %v, stderr %q; want permission denied", what, err, stderr)
		}
	}
	t.Run("reads its input", func(t *testing.T) {
		if out, stderr, err := runHelper(t, j, "read", input); err != nil || out != "input" {
			t.Fatalf("read of the input: %q, err %v %s; want its content", out, err, stderr)
		}
	})
	t.Run("reads no other file", func(t *testing.T) {
		out, stderr, err := runHelper(t, j, "read", secret)
		denied(t, "read of a file beside the input", stderr, err)
		if out != "" {
			t.Fatalf("secret read: %q", out)
		}
	})
	t.Run("writes no file", func(t *testing.T) {
		out := filepath.Join(dir, "out")
		_, stderr, err := runHelper(t, j, "write", out)
		denied(t, "write in "+dir, stderr, err)
		if _, err := os.Stat(out); err == nil {
			t.Fatalf("%s written", out)
		}
	})
	t.Run("opens no TCP connection", func(t *testing.T) {
		if abi < 4 {
			t.Skipf("Landlock ABI %d: TCP rules came with ABI 4", abi)
		}
		ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		_, stderr, err := runHelper(t, j, "dial", ln.Addr().String())
		denied(t, "connect to "+ln.Addr().String(), stderr, err)
		ln.SetDeadline(time.Now().Add(100 * time.Millisecond))
		if c, err := ln.Accept(); err == nil {
			c.Close()
			t.Fatal("a connection reached the listener")
		}
	})
	t.Run("has no_new_privs", func(t *testing.T) {
		if out, stderr, err := runHelper(t, j, "nnp"); err != nil || out != "1" {
			t.Fatalf("PR_GET_NO_NEW_PRIVS: %q, err %v %s; want 1", out, err, stderr)
		}
	})
}

// A program that allocates past the memory limit, or runs past the CPU limit,
// is stopped by the limit. The limits here are small ones for the test, not
// the ones of ffmpeg.
func TestSandboxLimits(t *testing.T) {
	abi, _ := landlockABI() // 0 when there is none: the limits alone
	j, _ := testJail(t, abi)
	j.Data, j.CPU = 1<<30, 1 // 1 GiB: the race runtime of the helper needs about 300 MiB to start
	t.Run("memory", func(t *testing.T) {
		if _, stderr, err := runHelper(t, j, "alloc", "16"); err != nil {
			t.Fatalf("16 MiB under 1 GiB: err %v %s; want it allocated", err, stderr)
		}
		// Go says "out of memory" or "cannot allocate memory"; the race
		// runtime "failed to allocate ... (errno: 12)", ENOMEM.
		_, stderr, err := runHelper(t, j, "alloc", "4096")
		if err == nil || !strings.Contains(stderr, "memory") && !strings.Contains(stderr, "errno: 12") {
			t.Fatalf("4 GiB under 1 GiB: err %v, stderr %q; want out of memory", err, stderr)
		}
	})
	t.Run("cpu", func(t *testing.T) {
		start := time.Now()
		_, _, err := runHelper(t, j, "spin")
		ee, ok := err.(*exec.ExitError)
		if !ok || !ee.Sys().(syscall.WaitStatus).Signaled() {
			t.Fatalf("spin under 1 s of CPU: err %v; want a kill by a signal", err)
		}
		if d := time.Since(start); d > 15*time.Second {
			t.Fatalf("spin under 1 s of CPU: stopped after %v", d)
		}
	})
}

// The launcher never becomes the client: called with its argument and a bad
// target or a bad jail, it exits at once with its own code and one line on
// stderr, and writes nothing on stdout (the client would draw there, the
// test binary would print its PASS).
func TestSandboxLauncherNeverRunsClient(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		what string
		args []string
		code int
	}{
		{"bad target", []string{sandboxArg, `{"Path":"/nonexistent/ffmpeg","Data":1073741824,"CPU":10}`, "ffmpeg", "-version"}, exitExec},
		{"bad jail", []string{sandboxArg, "not a jail"}, exitUsage},
		{"no jail", []string{sandboxArg}, exitUsage},
	} {
		cmd := exec.Command(bin, c.args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		start := time.Now()
		err := cmd.Run()
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != c.code {
			t.Errorf("%s: err %v; want exit code %d", c.what, err, c.code)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%s: exit after %v", c.what, d)
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: stdout %q; want nothing", c.what, stdout.String())
		}
		if lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n"); len(lines) != 1 || !strings.HasPrefix(lines[0], "ttyloom sandbox: ") {
			t.Errorf("%s: stderr %q; want one line of the launcher", c.what, stderr.String())
		}
	}
}

// resetSandbox makes the next ffmpeg run probe the sandbox again, as at the
// start of a process; the state before comes back at the end of the test.
func resetSandbox(t *testing.T) {
	was, wasProbed, wasABI, wasLauncher := theSandbox, probed.Load(), landlockABI, launcher
	theSandbox = sync.OnceValue(probeSandbox)
	probed.Store(nil)
	t.Cleanup(func() {
		theSandbox, landlockABI, launcher = was, wasABI, wasLauncher
		probed.Store(wasProbed)
	})
}

// testVideo writes a 1 s 64x32 MP4 of the test pattern in dir.
func testVideo(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "good.mp4")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=1:size=64x32:rate=10",
		"-pix_fmt", "yuv420p", p).CombinedOutput(); err != nil {
		t.Skipf("generation: %v %s", err, out)
	}
	return p
}

// A video loads in every mode with the same frames, and /debug tells the mode
// in use: Landlock when the kernel has it; the limits and no_new_privs alone
// when it has not; plain exec when the launcher cannot start, and with the
// switch of the user.
func TestSandboxFallback(t *testing.T) {
	if !HaveFFmpeg {
		t.Skip("ffmpeg not available")
	}
	p := testVideo(t, t.TempDir())
	var want *Frames
	for _, c := range []struct {
		mode   string
		set    func(t *testing.T)
		inNote string // what the line of /debug says
		noNote string // ... and does not
	}{
		{"off switch", func(t *testing.T) { t.Setenv(offEnv, "off") }, offEnv + "=off", "no_new_privs"},
		{"Landlock", func(t *testing.T) {
			if _, err := landlockABI(); err != nil {
				t.Skipf("no Landlock here: %v", err)
			}
		}, "Landlock ABI", ""},
		{"no Landlock", func(*testing.T) {
			landlockABI = func() (int, error) { return 0, unix.ENOSYS }
		}, "no_new_privs", "Landlock ABI"},
		{"no launcher", func(t *testing.T) { launcher = filepath.Join(t.TempDir(), "none") }, "-version", "no_new_privs"},
	} {
		t.Run(c.mode, func(t *testing.T) {
			resetSandbox(t)
			c.set(t)
			fr, err := Load(context.Background(), p, "video/mp4", 64, 64, 5)
			if err != nil || len(fr.PNG) != 5 {
				t.Fatalf("%s: %s, err %v; want 5 frames", c.mode, shape(fr), err)
			}
			if want == nil {
				want = fr
			}
			for i := range fr.PNG {
				if !bytes.Equal(fr.PNG[i], want.PNG[i]) {
					t.Fatalf("%s: frame %d differs from the frame of plain exec", c.mode, i)
				}
			}
			note := SandboxNote()
			if !strings.Contains(note, c.inNote) || c.noNote != "" && strings.Contains(note, c.noNote) {
				t.Fatalf("%s: /debug says %q; want %q in it, not %q", c.mode, note, c.inNote, c.noNote)
			}
			if again := SandboxNote(); again != "" {
				t.Fatalf("%s: the line of /debug comes twice: %q", c.mode, again)
			}
		})
	}
}

// The mode is chosen once per process, never by a file. A list that makes
// ffprobe read a file beside it (ffconcat) fails in the sandbox; a corrupt
// file and a good one then go through, and the list still fails: no failure
// turned the sandbox off.
func TestSandboxModeSurvivesBadFile(t *testing.T) {
	if !HaveFFmpeg {
		t.Skip("ffmpeg not available")
	}
	if _, err := landlockABI(); err != nil {
		t.Skipf("no Landlock here: %v", err)
	}
	resetSandbox(t)
	dir := t.TempDir()
	good := testVideo(t, dir)
	list := filepath.Join(dir, "list.mp4") // a name beside it: the safe mode of concat takes it
	if err := os.WriteFile(list, []byte("ffconcat version 1.0\nfile good.mp4\nduration 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Outside the sandbox the list reads the video: what fails below fails
	// for the sandbox alone.
	if out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=width", "-of", "csv=p=0", "-protocol_whitelist", "file,pipe", "-i", list).Output(); err != nil || !strings.Contains(string(out), "64") {
		t.Skipf("ffprobe does not read the list outside the sandbox: %q %v", out, err)
	}
	probeList := func(when string) {
		t.Helper()
		if w, h, d, err := ProbeVideo(context.Background(), list); err == nil {
			t.Fatalf("%s: the list read %s through the sandbox (%dx%d, %v)", when, good, w, h, d)
		}
	}
	probeList("first run")
	b, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), writeFile(t, "cut.mp4", b[:len(b)/3]), "video/mp4", 64, 64, 5); err == nil {
		t.Fatal("a cut MP4 decoded")
	}
	if fr, err := Load(context.Background(), good, "video/mp4", 64, 64, 5); err != nil || len(fr.PNG) != 5 {
		t.Fatalf("good video after a bad one: %s, err %v; want 5 frames", shape(fr), err)
	}
	probeList("after a corrupt file and a good one")
}
