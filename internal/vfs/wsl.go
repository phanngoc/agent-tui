package vfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// WSL reads the filesystem inside a WSL distribution.
//
// A Linux distribution under WSL is a second filesystem namespace on the same
// machine: /home/you is not reachable by the os package from a Windows build,
// and C:\src is /mnt/c/src over there. An agent told to work in one and a file
// tree reading the other would disagree about what the project even contains,
// so this is a filesystem in its own right rather than a path prefix.
//
// Every operation is a `wsl.exe` launch of a shell script; the scripts live in
// posixFS, shared with the container backend.
type WSL struct {
	posixFS
	distro string

	homeOnce sync.Once
	home     string
}

// NewWSL targets a distribution by its registered name. An empty name means
// the machine's default distribution, which is what a bare `wsl` enters.
func NewWSL(distro string) *WSL {
	w := &WSL{distro: distro}
	w.run = w
	return w
}

func (w *WSL) ID() string {
	if w.distro == "" {
		return "wsl:"
	}
	return "wsl:" + w.distro
}

func (w *WSL) Label() string {
	if w.distro == "" {
		return "wsl"
	}
	return w.distro
}

func (w *WSL) IsLocal() bool { return false }

// Distro is the registered name this filesystem speaks to.
func (w *WSL) Distro() string { return w.distro }

// DefaultDir is the login home inside the distribution. It is probed once and
// remembered: the answer cannot change while the distribution is registered,
// and a probe costs a process launch.
func (w *WSL) DefaultDir() string {
	w.homeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		out, err := w.sh(ctx, "", `cd ~ && pwd`)
		if home := strings.TrimSpace(string(out)); err == nil && strings.HasPrefix(home, "/") {
			w.home = home
			return
		}
		w.home = "/"
	})
	return w.home
}

// Health starts the distribution if it is not already running and reports why
// it cannot be used when that fails. It is also the moment a cold distribution
// pays its start-up cost, rather than the first directory listing.
func (w *WSL) Health(ctx context.Context) error {
	if _, err := exec.LookPath("wsl.exe"); err != nil {
		return fmt.Errorf("wsl.exe is not installed")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := w.sh(ctx, "", `exit 0`); err != nil {
		return fmt.Errorf("%s is not reachable: %s", w.Label(), err)
	}
	return nil
}

// args builds a wsl.exe invocation.
//
// --cd is always passed, even for the default directory: without it wsl.exe
// adopts this process's Windows working directory, which on a UNC path makes
// it warn and fall back to the home directory anyway.
//
// --exec, rather than the bare --, is what keeps arguments intact. Without it
// wsl.exe joins everything after -- back into one line and hands that to the
// user's login shell, which parses it a second time: a prompt containing
// $HOME comes out expanded, and one containing $(...) or backticks is not
// mangled but *executed*, inside the distribution, with whatever the user
// typed. Every argument here crosses that boundary — the agent CLIs take the
// prompt on their command line — so the second parse is not a quoting
// inconvenience, it is arbitrary command execution from message text.
// --exec passes argv straight to execvp and there is no second parse.
func (w *WSL) args(dir string) []string {
	var a []string
	if w.distro != "" {
		a = append(a, "-d", w.distro)
	}
	if dir == "" {
		dir = "~"
	}
	return append(a, "--cd", dir, "--exec")
}

// wslCmd builds a wsl.exe invocation.
//
// WSL_UTF8 asks wsl.exe for UTF-8 diagnostics rather than the UTF-16 it writes
// to a redirected pipe by default. Builds too old to know the variable ignore
// it, which is what decodeUTF16 is for.
func wslCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "wsl.exe", args...)
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	return cmd
}

// sh runs a shell snippet inside the distribution and returns its stdout.
//
// stdout is left as the bytes Linux wrote; only wsl.exe's own diagnostics are
// decoded, because those it writes as UTF-16.
func (w *WSL) sh(ctx context.Context, dir, script string) ([]byte, error) {
	args := append(w.args(dir), "/bin/sh", "-c", script)
	cmd := wslCmd(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(decodeUTF16(stderr.Bytes()))
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("%s", msg)
	}
	return stdout.Bytes(), nil
}

// shIn runs a script with data on its stdin.
func (w *WSL) shIn(ctx context.Context, script string, stdin io.Reader) error {
	args := append(w.args(""), "/bin/sh", "-c", script)
	cmd := wslCmd(ctx, args...)
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(decodeUTF16(stderr.Bytes())); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

// Command runs a program inside the distribution, so an agent CLI executes in
// the same namespace as the files it is being asked to edit.
//
// It goes through a login shell, which is not ceremony: wsl.exe runs a command
// with a bare inherited PATH, and that PATH is not the one the user has. The
// per-user directories where tools actually install — ~/.local/bin above all,
// which is where the Claude Code installer puts its binary — are added by
// ~/.profile, so a distribution with the CLI plainly installed reports
// "command not found" until something reads that file. A login shell is the
// one thing that does.
//
// Only Command takes this route. The listing and search scripts go through sh,
// where the extra startup cost would be paid on every directory read and a
// chatty profile could write noise into output that is about to be parsed.
func (w *WSL) Command(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
	line := shellJoin(append([]string{name}, args...))
	full := append(w.args(dir), "bash", "-lc", line)
	return wslCmd(ctx, full...)
}

// shellJoin renders a command and its arguments as one shell line, quoted so
// that a path with a space in it survives being handed to `bash -lc`.
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// EnterWSL resolves a distribution and where to land in it, in one launch.
//
// It lands in the login home, which is where entering a distribution puts you.
// Note that this is deliberately *not* what bare `wsl.exe` does: given a
// Windows working directory it translates it and starts under /mnt, so a
// Windows checkout stays on screen. That is the wrong half of the choice here,
// because the reason to enter a distribution is the work that only exists
// inside it — a Linux checkout under ~, a toolchain that was never installed
// on the host. A session that wanted the /mnt view of a Windows project did
// not need to leave the host to get it.
//
// It is one launch on purpose. Asking separately — enumerate, check it is
// alive, probe the home directory — is three round trips before the explorer
// may move, and at a quarter of a second each that is most of a second showing
// the directory the user has just left. Everything needed is read from one
// shell instead: the distribution names itself through WSL_DISTRO_NAME, and
// the same shell reports where ~ is.
func EnterWSL(ctx context.Context, distro string) (*WSL, string, error) {
	w := NewWSL(distro)

	// Probed from /, which always exists: a login home that does not would
	// otherwise fail the launch before the script could report anything.
	out, err := w.sh(ctx, "/", `printf '%s\n' "${WSL_DISTRO_NAME:-wsl}"; cd ~ 2>/dev/null && pwd || echo /`)
	if err != nil {
		return nil, "", err
	}
	name, home := parseEntry(string(out))
	if name == "" {
		return nil, "", fmt.Errorf("%s did not identify itself", w.Label())
	}

	// Pin the resolved name, so a session that entered the default
	// distribution keeps working after the default changes.
	if !strings.EqualFold(name, w.distro) {
		w = NewWSL(name)
	}
	if home == "" {
		home = "/"
	}
	w.homeOnce.Do(func() { w.home = home })
	return w, home, nil
}

// parseEntry reads the two lines EnterWSL's script prints.
func parseEntry(out string) (name, home string) {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > 0 {
		name = lines[0]
	}
	if len(lines) > 1 {
		home = lines[1]
	}
	return name, home
}

// ---- enumeration -----------------------------------------------------------

// Distro is a registered WSL distribution.
type Distro struct {
	Name    string
	State   string // as wsl.exe reports it, for display only
	Default bool
}

// Distros lists the registered distributions. A machine without WSL is not an
// error worth surfacing loudly: it just means there is nothing to offer beyond
// the host.
func Distros(ctx context.Context) []Distro {
	if _, err := exec.LookPath("wsl.exe"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// -v adds the state and the default marker. Its output is UTF-16, and its
	// header row is localised, so the header is skipped rather than matched.
	out, err := wslCmd(ctx, "-l", "-v").Output()
	if err != nil {
		return nil
	}
	return parseDistros(decodeUTF16(out))
}

func parseDistros(text string) []Distro {
	var list []Distro
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if i == 0 || strings.TrimSpace(line) == "" {
			continue // the header
		}
		def := strings.HasPrefix(strings.TrimSpace(line), "*")
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "*"))
		// The trailing two fields are the state and the WSL version; a name
		// containing spaces is everything before them.
		if len(fields) < 3 {
			continue
		}
		name := strings.Join(fields[:len(fields)-2], " ")
		if name == "" {
			continue
		}
		list = append(list, Distro{
			Name: name, State: fields[len(fields)-2], Default: def,
		})
	}
	return list
}

// decodeUTF16 converts the UTF-16 that wsl.exe writes into UTF-8, and leaves
// anything already UTF-8 alone.
//
// WSL_UTF8=1, which every launch here sets, makes recent wsl.exe builds emit
// UTF-8 and reduces this to a copy. Builds that predate that variable ignore
// it and still write UTF-16LE to a redirected pipe, which arrives as ASCII
// interleaved with NULs; this is what reads those.
func decodeUTF16(b []byte) string {
	switch {
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		b = b[2:]
	case looksUTF16LE(b):
	default:
		return string(b)
	}
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

// looksUTF16LE spots UTF-16 without a byte-order mark.
//
// The tell for Latin text is a NUL as every character's high byte and never as
// a low one. Testing both halves is what separates it from binary, which
// scatters its NULs across both: a NUL high byte alone would also describe an
// ELF header. Text this misreads is a diagnostic from wsl.exe, never file
// contents — those are the bytes Linux wrote and are returned untouched.
func looksUTF16LE(b []byte) bool {
	if len(b) < 4 || len(b)%2 != 0 {
		return false
	}
	n := min(len(b), 128)
	n -= n % 2
	var high, low int
	for i := 0; i < n; i += 2 {
		if b[i] == 0 {
			low++
		}
		if b[i+1] == 0 {
			high++
		}
	}
	return low == 0 && high >= n/4
}

// ---- path translation ------------------------------------------------------

// HostToWSL maps a Windows path onto the /mnt mount WSL gives it. It reports
// false for a path with no equivalent — a UNC share, or a path that is already
// POSIX.
//
// Entering a distribution deliberately does not use this: it lands in the
// login home, because the work that justifies going in is the work that only
// exists inside. This is the inverse of WSLToHost, which coming back out does
// use, and the pair is what the round-trip test can hold to account.
func HostToWSL(p string) (string, bool) {
	if driveLen(p) != 2 {
		return "", false
	}
	drive := strings.ToLower(p[:1])
	rest := strings.TrimPrefix(toSep(p[2:], '/'), "/")
	out := "/mnt/" + drive
	if rest != "" {
		out += "/" + rest
	}
	return out, true
}

// WSLToHost is the reverse, for coming back out to the host. Only the /mnt
// mounts have a Windows equivalent; the distribution's own filesystem does not.
func WSLToHost(p string) (string, bool) {
	rest, ok := strings.CutPrefix(p, "/mnt/")
	if !ok || rest == "" {
		return "", false
	}
	drive, tail, _ := strings.Cut(rest, "/")
	if len(drive) != 1 {
		return "", false
	}
	c := drive[0]
	if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') {
		return "", false
	}
	out := strings.ToUpper(drive) + `:\`
	if tail != "" {
		out += toSep(tail, '\\')
	}
	return out, true
}
