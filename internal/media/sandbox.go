package media

// The decoders of ffmpeg and ffprobe are C code, and they read files that a
// peer chose, with no user action (auto_media_max_kb). Every run of them goes
// through ffCmd: ttyloom starts itself again (/proc/self/exe) with sandboxArg,
// and this launcher sets the restrictions, then executes ffmpeg or ffprobe in
// its place. The mode is chosen once per process, on "-version" (probeSandbox):
// a file can never change it. mpv (a user action, it needs the screen, the GPU
// and the sound) and the Go decoders stay out of it.

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/govlog/ttyloom/internal/i18n"
	"golang.org/x/sys/unix"
)

// sandboxArg : the hidden first argument that makes the binary the launcher.
// Any binary that links this package can be it: ttyloom, and the test binaries
// of the packages that decode (os.Args[0] is the test binary there).
const sandboxArg = "__ttyloom_sandbox"

// offEnv : TTYLOOM_FFMPEG_SANDBOX=off runs ffmpeg and ffprobe as before the
// sandbox, for a system where the sandbox breaks them.
const offEnv = "TTYLOOM_FFMPEG_SANDBOX"

// Exit codes of the launcher, one for each step that failed. ffmpeg and
// ffprobe exit with 1 (or a signal) on an error of their own.
const (
	exitUsage      = 120 + iota // the arguments are not those of jailCmd
	exitLimits                  // getrlimit or setrlimit
	exitNoNewPrivs              // prctl(PR_SET_NO_NEW_PRIVS)
	exitLandlock                // a Landlock call
	exitExec                    // execve of the program
)

// Limits. Measured with ffmpeg 8.0 (Ubuntu, 190 shared libraries) on 12 CPUs:
// 4K H.264, 4K VP9 (libvpx), 1080p H.264 and VP9, a 20 s GIF and an APNG,
// fitted to 1280x720, at most 1000 frames out.
const (
	// cpuMax : seconds of CPU time. The worst real decode measured took
	// 11 s of CPU for 300 frames of 4K VP9; the 1000 frames of a play at
	// 10 fps are 100 s of video, 6000 frames for a 60 fps source: about
	// 220 s. That is above the 2 min of the timeout of loadVideo too.
	cpuMax = 600
	// filesMax : open files. ffmpeg holds 5 at most (stdin, stdout,
	// stderr, the input, one library while the loader opens it).
	filesMax = 64
	// stackMax : the stack of each thread. glibc gives a thread the soft
	// RLIMIT_STACK: 16 MiB on this machine doubled the size of ffmpeg
	// (1434 MB of data instead of 1006 for 4K H.264). 8 MiB is the usual
	// default; a lower limit of the user stays.
	stackMax = 8 << 20
)

// memMax : bytes of RLIMIT_DATA (heap, private mappings, thread stacks).
// RLIMIT_AS does not fit: ffmpeg maps 2.6 to 3.1 GB of address space for
// 0.1 to 0.6 GB resident (190 libraries, guard pages, 64 MB malloc arenas
// reserved per thread). Its data size grows with the CPUs, as its threads do
// (about 4 a CPU, 8 MiB of stack each, plus frames): 166 MB on 1 CPU, 462 on
// 4, 1006 on 12 (4K H.264). The limit is about twice that.
func memMax() uint64 { return 256<<20 + 128<<20*uint64(runtime.NumCPU()) }

// jail : what the launcher gets from ffCmd, as JSON in its second argument.
type jail struct {
	Path string   // the program to execute, absolute
	ABI  int      // the Landlock ABI to use; 0: no Landlock
	Read []string // files and directory trees it may read
	Exec []string // files it may execute (and read): itself and its loader
	Data uint64   // RLIMIT_DATA, bytes
	CPU  uint64   // RLIMIT_CPU, seconds
}

// init runs the launcher before main, before the configuration and the
// terminal: os.Exit, never a return to the client.
// ponytail: a run pays 4.6 ms for the launcher (25 ms for ffprobe alone):
// Go starts 153 packages before this one (3.5 ms, 2.1 of them for the table
// of i18n), as golang.org/x/sys sorts after every github.com path. A package
// of its own, before i18n and on syscall alone, would start at 1.8 ms.
func init() {
	if len(os.Args) > 1 && os.Args[1] == sandboxArg {
		os.Exit(launch(os.Args[2:]))
	}
}

// launch is the launcher: args are the jail, then the argv of the program. It
// sets the limits, no_new_privs, then the Landlock rules, and executes the
// program. A step that fails ends it with its own code: it never executes the
// program with less than it was asked (the parent chose the mode).
func launch(args []string) int {
	// no_new_privs and a Landlock domain belong to the thread that sets them,
	// and execve keeps the credentials of the thread that calls it: the three
	// calls on one thread. The test of the confinement checks it on the
	// program that runs.
	runtime.LockOSThread()
	var j jail
	if len(args) < 2 || json.Unmarshal([]byte(args[0]), &j) != nil || !filepath.IsAbs(j.Path) || j.Data == 0 || j.CPU == 0 {
		return fail(exitUsage, errors.New("bad arguments"))
	}
	if err := j.limit(); err != nil {
		return fail(exitLimits, err)
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fail(exitNoNewPrivs, fmt.Errorf("prctl(PR_SET_NO_NEW_PRIVS): %w", err))
	}
	if j.ABI > 0 {
		if err := j.landlock(); err != nil {
			return fail(exitLandlock, err)
		}
	}
	err := unix.Exec(j.Path, args[1:], os.Environ())
	return fail(exitExec, fmt.Errorf("exec %s: %w", j.Path, err))
}

// fail writes the one line of a launcher that stops, and gives code.
func fail(code int, err error) int {
	fmt.Fprintln(os.Stderr, "ttyloom sandbox:", err)
	return code
}

// limit sets each limit, soft and hard, never above the soft limit there was:
// a lower limit of the user stays, and an unprivileged process cannot raise
// a hard one. No RLIMIT_NPROC: it counts every process of the user.
func (j jail) limit() error {
	for _, l := range []struct {
		res int
		max uint64
	}{
		{unix.RLIMIT_DATA, j.Data},
		{unix.RLIMIT_CPU, j.CPU},
		{unix.RLIMIT_FSIZE, 0}, // nothing writes a file: frames go to a pipe
		{unix.RLIMIT_NOFILE, filesMax},
		{unix.RLIMIT_CORE, 0},
		{unix.RLIMIT_STACK, stackMax},
	} {
		var old unix.Rlimit
		if err := unix.Getrlimit(l.res, &old); err != nil {
			return fmt.Errorf("getrlimit(%d): %w", l.res, err)
		}
		v := min(l.max, old.Cur)
		if err := unix.Setrlimit(l.res, &unix.Rlimit{Cur: v, Max: v}); err != nil {
			return fmt.Errorf("setrlimit(%d, %d): %w", l.res, v, err)
		}
	}
	return nil
}

// handled gives the Landlock rights that the ruleset denies unless a rule
// allows them: every right of the ABI given, by the ABI that brought it
// (Documentation/userspace-api/landlock.rst, include/uapi/linux/landlock.h of
// Linux 7.3). An ABI above 10 gets the rights of 10: no rule needs more.
func handled(abi int) (fs, net, scoped uint64) {
	fs = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR | unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM
	if abi >= 2 {
		fs |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		fs |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 4 {
		net = unix.LANDLOCK_ACCESS_NET_BIND_TCP | unix.LANDLOCK_ACCESS_NET_CONNECT_TCP
	}
	if abi >= 5 {
		fs |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	if abi >= 6 {
		scoped = unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET | unix.LANDLOCK_SCOPE_SIGNAL
	}
	if abi >= 9 {
		fs |= unix.LANDLOCK_ACCESS_FS_RESOLVE_UNIX
	}
	if abi >= 10 {
		net |= unix.LANDLOCK_ACCESS_NET_BIND_UDP | unix.LANDLOCK_ACCESS_NET_CONNECT_SEND_UDP
	}
	return fs, net, scoped
}

// rights names, for /debug, what the Landlock rules of abi cover beyond the
// files (the names of the kernel).
func rights(abi int) string {
	r := []string{"fs"}
	for _, x := range []struct {
		abi  int
		name string
	}{{4, "tcp"}, {5, "ioctl_dev"}, {6, "scope"}, {9, "resolve_unix"}, {10, "udp"}} {
		if abi >= x.abi {
			r = append(r, x.name)
		}
	}
	return strings.Join(r, ", ")
}

// landlock restricts the calling thread: read on j.Read, read and execute on
// j.Exec, no other file right, and no network and no IPC out of the domain
// where the ABI has the rule. No rule allows a port: every TCP (and, from
// ABI 10, UDP) bind and connect fails.
func (j jail) landlock() error {
	fs, net, scoped := handled(j.ABI)
	attr := unix.LandlockRulesetAttr{Handled_access_fs: fs, Handled_access_net: net, Scoped: scoped}
	// The kernel reads the fields it knows and wants the others at zero: the
	// whole struct goes, whatever the ABI.
	r, _, e := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if e != 0 {
		return fmt.Errorf("landlock_create_ruleset: %w", e)
	}
	ruleset := int(r)
	defer unix.Close(ruleset) // the program gets no ruleset fd
	for _, p := range j.Read {
		if err := allow(ruleset, p, unix.LANDLOCK_ACCESS_FS_READ_FILE); err != nil {
			return err
		}
	}
	for _, p := range j.Exec {
		if err := allow(ruleset, p, unix.LANDLOCK_ACCESS_FS_READ_FILE|unix.LANDLOCK_ACCESS_FS_EXECUTE); err != nil {
			return err
		}
	}
	if _, _, e := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(ruleset), 0, 0); e != 0 {
		return fmt.Errorf("landlock_restrict_self: %w", e)
	}
	return nil
}

// allow adds the rule "access beneath path" (a file or a directory tree).
// Both rights are rights of a file, valid on both. A path that does not
// exist gets no rule: there is nothing to read, and ffmpeg reports the
// missing input itself, as before the sandbox.
func allow(ruleset int, path string, access uint64) error {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer unix.Close(fd)
	rule := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(fd)}
	if _, _, e := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH,
		uintptr(unsafe.Pointer(&rule)), 0, 0, 0); e != 0 {
		return fmt.Errorf("landlock_add_rule %s: %w", path, e)
	}
	return nil
}

// landlockABI gives the Landlock ABI of the kernel, or an error when it has
// none (ENOSYS) or when it is off (EOPNOTSUPP). A variable: a test takes it away.
var landlockABI = func() (int, error) {
	v, _, e := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if e != 0 {
		return 0, fmt.Errorf("landlock_create_ruleset: %w", e)
	}
	return int(v), nil
}

// jailFor gives the jail in which the program at path starts with Landlock at
// abi: execute the program and its loader (the kernel opens the loader for
// execution), read the directories of the libraries that the loader itself
// resolves for it ("--list", with the environment of the sandbox), and
// /etc/ld.so.cache, where glibc looks them up. That covers /usr, /usr/local,
// /opt and the Nix store alike; a static program needs nothing. Only these
// directories, not /usr whole: Ubuntu gives one, /usr/lib/x86_64-linux-gnu.
// ffmpeg needs nothing else: /proc, /sys, /dev and /etc/gnutls are refused
// and the frames stay the same (measured).
func jailFor(path string, abi int) (jail, error) {
	j := jail{Path: path, ABI: abi, Data: memMax(), CPU: cpuMax}
	if abi == 0 {
		return j, nil
	}
	j.Exec = []string{path}
	interp, err := interpreter(path)
	if err != nil || interp == "" {
		return j, err
	}
	j.Exec = append(j.Exec, interp)
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	list := exec.CommandContext(ctx, interp, "--list", path)
	list.Env = sandboxEnv()
	out, err := list.Output()
	if err != nil {
		return j, fmt.Errorf("%s --list %s: %w", interp, path, err)
	}
	if _, err := os.Stat("/etc/ld.so.cache"); err == nil {
		j.Read = append(j.Read, "/etc/ld.so.cache")
	}
	// "\tlibz.so.1 => /usr/lib/x86_64-linux-gnu/libz.so.1 (0x...)", and the
	// loader alone on its line.
	for _, ln := range strings.Split(string(out), "\n") {
		f := strings.Fields(ln)
		if i := slices.Index(f, "=>"); i >= 0 {
			f = f[i+1:]
		}
		if len(f) == 0 || !filepath.IsAbs(f[0]) {
			continue
		}
		lib, err := filepath.EvalSymlinks(f[0]) // the rule holds where the file is
		if err != nil {
			return j, err
		}
		if d := filepath.Dir(lib); !slices.Contains(j.Read, d) {
			j.Read = append(j.Read, d)
		}
	}
	return j, nil
}

// interpreter gives the loader that the ELF program at path names, "" for a
// static one.
func interpreter(path string) (string, error) {
	f, err := elf.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			b, err := io.ReadAll(p.Open())
			return strings.TrimRight(string(b), "\x00"), err
		}
	}
	return "", nil
}

// sandboxEnv : the environment of the launcher and of the program. Nothing of
// ttyloom, no LD_PRELOAD; LD_LIBRARY_PATH only when the user set it, for an
// ffmpeg built in a folder of its own (jailFor then allows the folders it
// gives, as the loader finds the libraries there). ffmpeg needs no other
// variable for a decode.
func sandboxEnv() []string {
	env := []string{}
	if v, ok := os.LookupEnv("LD_LIBRARY_PATH"); ok {
		env = append(env, "LD_LIBRARY_PATH="+v)
	}
	return env
}

// launcher : the binary that becomes the launcher. A variable: a test takes it
// away.
var launcher = "/proc/self/exe"

// jailCmd gives the command that runs j.Path in j, with argv0 and args as its
// arguments. ctx ends it as exec.CommandContext does: the launcher, then the
// program that replaced it (same process), is killed.
func jailCmd(ctx context.Context, j jail, argv0 string, args ...string) *exec.Cmd {
	spec, err := json.Marshal(j)
	cmd := exec.CommandContext(ctx, launcher, append([]string{sandboxArg, string(spec), argv0}, args...)...)
	cmd.Env = sandboxEnv()
	if err != nil {
		cmd.Err = err // Start fails: never a run outside the jail
	}
	return cmd
}

// sandbox : the mode chosen for the process. jails holds ffmpeg and ffprobe
// in a sandbox mode, nothing in plain mode.
type sandbox struct {
	jails map[string]jail
	note  string      // the line of /debug
	noted atomic.Bool // ... already taken by SandboxNote
}

var (
	// theSandbox : probed at the first run of ffmpeg or ffprobe, then kept,
	// as haveLibvpx. A variable: a test probes again.
	theSandbox = sync.OnceValue(probeSandbox)
	probed     atomic.Pointer[sandbox] // the probe is over (SandboxNote)
)

// probeTimeout : the most one step of the probe may take (the loader lists
// the 190 libraries of ffmpeg in 7 ms; "-version" takes 30 ms).
const probeTimeout = 10 * time.Second

// probeSandbox chooses the mode, from the strongest down: Landlock with the
// limits and no_new_privs; the limits and no_new_privs alone (no Landlock in
// the kernel, or ffmpeg does not start under it); plain exec as before (the
// launcher cannot start, or the user's switch). Each sandbox mode must run
// "ffmpeg -version" and "ffprobe -version": no file of a peer is involved,
// and a failure of a later run never comes back here. The context of the
// caller does not end it: a decode cancelled at the first run would choose a
// lower mode for the whole process.
func probeSandbox() *sandbox {
	s := &sandbox{}
	defer probed.Store(s)
	if os.Getenv(offEnv) == "off" {
		s.note = i18n.T("ffmpeg_sandbox_off", offEnv+"=off")
		return s
	}
	abi, err := landlockABI()
	if err == nil {
		if s.jails, err = tryJails(abi); err == nil {
			s.note = i18n.T("ffmpeg_sandbox", abi, rights(abi))
			return s
		}
	}
	why := err
	if s.jails, err = tryJails(0); err == nil {
		s.note = i18n.T("ffmpeg_sandbox_limits", why)
		return s
	}
	s.note = i18n.T("ffmpeg_sandbox_off", err)
	return s
}

// tryJails gives the jails of ffmpeg and ffprobe at abi when both run
// "-version" in them.
func tryJails(abi int) (map[string]jail, error) {
	m := map[string]jail{}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		path, err := exec.LookPath(name)
		if err == nil {
			path, err = filepath.Abs(path)
		}
		if err != nil {
			return nil, err
		}
		j, err := jailFor(path, abi)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		out, err := jailCmd(ctx, j, name, "-version").Output()
		cancel()
		if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
			line, _, _ := strings.Cut(strings.TrimSpace(string(ee.Stderr)), "\n")
			err = fmt.Errorf("%w: %s", err, line)
		}
		if err == nil && !bytes.HasPrefix(out, []byte(name+" version")) {
			err = fmt.Errorf("output %.40q", out)
		}
		if err != nil {
			return nil, fmt.Errorf("%s -version: %w", name, err)
		}
		m[name] = j
	}
	return m, nil
}

// ffCmd is exec.CommandContext for ffmpeg and ffprobe (name), in the mode of
// the process. input is the one file the program may read ("" for none): a
// file of a peer, opened by path. In plain mode it is exec.CommandContext.
func ffCmd(ctx context.Context, input, name string, args ...string) *exec.Cmd {
	j, ok := theSandbox().jails[name]
	if !ok {
		return exec.CommandContext(ctx, name, args...)
	}
	if input != "" && j.ABI > 0 {
		j.Read = append(slices.Clip(j.Read), input) // a copy: the jail of the process stays as it is
	}
	return jailCmd(ctx, j, name, args...)
}

// SandboxNote gives the line that tells, in /debug, how ffmpeg and ffprobe
// run: once per process, after their first run; "" otherwise.
func SandboxNote() string {
	if s := probed.Load(); s != nil && !s.noted.Swap(true) {
		return s.note
	}
	return ""
}
