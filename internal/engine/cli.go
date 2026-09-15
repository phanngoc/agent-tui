package engine

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// decoder turns one CLI's event stream into agent events. Implementations are
// stateful for the length of a single turn and are never shared.
type decoder interface {
	// line consumes one NDJSON line.
	line(raw []byte, emit func(agent.Event))
	// finish flushes anything still buffered when the stream ends.
	finish(emit func(agent.Event))
	// failure reports an error the CLI signalled in-band, as opposed to a
	// non-zero exit.
	failure() string
}

// CLI drives an installed coding-agent binary in its headless JSON mode.
type CLI struct {
	id, label, bin string
	root           string
	// fs decides where the binary runs. Pointing a session at a container runs
	// the agent inside it, next to the files it is being asked to edit.
	fs vfs.FS

	// approvals is set when this CLI can route permission prompts back to us.
	approvals bool

	version string
	ok      bool
	why     string
	// path is the resolved binary; winBin marks a Windows build reached from
	// WSL through interop, whose path arguments must be translated.
	path   string
	winBin bool

	argv   func(c *CLI, t agent.Turn, br *broker) []string
	newDec func() decoder

	detectOnce sync.Once
}

func (c *CLI) ID() string    { return c.id }
func (c *CLI) Label() string { return c.label }
func (c *CLI) Available() bool {
	c.detect()
	return c.ok
}

// CanAsk reports whether this engine is able to route approvals back to the UI.
//
// Only Claude Code can, and only on this machine: the broker talks over a unix
// socket that a process inside a container cannot reach.
func (c *CLI) CanAsk() bool {
	if c.fs != nil && !c.fs.IsLocal() {
		return false
	}
	return c.approvals
}

// SetFS repoints this engine at the filesystem its session targets.
func (c *CLI) SetFS(f vfs.FS) { c.fs = f }

func (c *CLI) Detail() string {
	c.detect()
	if !c.ok {
		return c.why
	}
	d := c.version
	switch {
	case c.CanAsk():
		d += " · can ask before acting"
	case c.noSandbox():
		// Do not claim a policy that is not being enforced.
		d += " · UNCONFINED: no sandbox on Windows"
	default:
		d += " · cannot ask; its mode is set by sandbox policy"
	}
	return d
}

// noSandbox reports whether this CLI's own confinement cannot start here.
//
// Codex builds its sandbox from a helper directory it re-ACLs at startup, which
// an ordinary Windows account may not do; the same applies to a Windows build
// reached from WSL, which is still a Windows process. Nothing is confined in
// that case, and the UI has to say so rather than imply a gate that is absent.
func (c *CLI) noSandbox() bool {
	if c.approvals {
		return false // this engine asks instead of confining
	}
	c.detect()
	return hostSandboxBroken() || c.winBin
}

// hostSandboxBroken is a variable so a test can exercise both platforms from
// either one, the same way lookPath and executable are.
var hostSandboxBroken = func() bool { return runtime.GOOS == "windows" }

// argPath renders a path for this CLI's own namespace. A Windows binary reached
// from WSL is handed Windows paths even though this process speaks Linux ones.
func (c *CLI) argPath(p string) string {
	if c.winBin && runtime.GOOS != "windows" {
		return toWindowsPath(p)
	}
	return p
}

// detect resolves the binary and its version once.
func (c *CLI) detect() {
	c.detectOnce.Do(func() {
		path, win, err := lookAgent(c.bin)
		if err != nil {
			c.why = "not installed"
			return
		}
		out, err := probeVersion(path)
		if err != nil && !win {
			// Under WSL a Linux launcher can be present but broken — an npm
			// install done on the Windows side leaves a shim whose platform
			// binary was never fetched. A working Windows build is often right
			// there, so do not let the broken one hide it.
			if alt, aerr := lookPath(c.bin + ".exe"); aerr == nil && underWSL() {
				if altOut, altErr := probeVersion(alt); altErr == nil {
					path, win, out, err = alt, true, altOut, nil
				}
			}
		}
		c.path, c.winBin = path, win
		if err != nil {
			// A binary that cannot even report its version is not usable, but
			// say what actually happened rather than claiming it is missing.
			c.why = "found at " + path + " but --version failed"
			return
		}
		c.ok = true
		c.version = firstVersionToken(string(out))
	})
}

// firstVersionToken picks something version-shaped out of --version output,
// which the three CLIs format differently (and opencode prefixes with ASCII art).
func firstVersionToken(s string) string {
	for _, line := range strings.Split(s, "\n") {
		for _, f := range strings.Fields(line) {
			f = strings.TrimPrefix(f, "v")
			if f == "" || (f[0] < '0' || f[0] > '9') {
				continue
			}
			if strings.Contains(f, ".") {
				return strings.Trim(f, "()")
			}
		}
	}
	return "installed"
}

// Run executes one turn by spawning the CLI and translating its output.
func (c *CLI) Run(ctx context.Context, t agent.Turn, out chan<- agent.Event) {
	defer close(out)

	send := func(e agent.Event) bool {
		select {
		case out <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}

	root := t.Root
	if root == "" {
		root = c.root
	}

	// Stand up the approval broker before the CLI starts, so the very first
	// tool call already has somewhere to ask.
	//
	// The broker talks over a unix socket on this machine, which a process
	// inside a container cannot reach, so interception is host-only.
	// Trust granted at a prompt lasts for this run only, the same as it does
	// for the built-in agent.
	var trusted atomic.Bool

	// The broker is stood up for auto as well as ask. In auto mode the CLI only
	// asks about what its own policy could not settle — in practice, work
	// outside the project — and with nobody to answer, those come back denied
	// with no explanation. Auto means "act inside the project", so the broker
	// allows that much and puts the rest to the user.
	var br *broker
	wantBroker := t.Mode.Confirms() || t.Mode == agent.ModeAuto
	if c.CanAsk() && wantBroker && (t.FS == nil || t.FS.IsLocal()) {
		if self, err := executable(); err == nil {
			b, berr := startBroker(func(call session.ToolCall) bool {
				if ctxDone(ctx) {
					return false
				}
				if trusted.Load() {
					return true
				}
				reason := "the agent asked for permission"
				if !t.Mode.Confirms() {
					if inside, decided := call.PathsInside(root); decided && inside {
						return true // inside the project, which auto already allows
					}
					reason = "this is outside " + root
				}
				reply := make(chan agent.Verdict, 1)
				if !send(agent.EvApproval{Call: call, Reason: reason, Reply: reply}) {
					return false
				}
				select {
				case verdict := <-reply:
					if verdict == agent.AllowAll {
						trusted.Store(true)
					}
					return verdict != agent.Deny
				case <-ctx.Done():
					return false
				}
			})
			if berr == nil {
				br = b
				br.self = self
				defer br.Close()
			}
		}
	}
	if t.Mode.Confirms() && br == nil {
		send(agent.EvStatus{Text: "cannot ask here; running confined instead"})
	}

	args := c.argv(c, t, br)
	fsys := t.FS
	if fsys == nil {
		fsys = c.fs
	}
	if fsys == nil {
		// An engine is always constructed with a filesystem; this only guards
		// against a caller assembling one by hand.
		fsys = vfs.NewLocal(root)
	}
	bin := c.bin
	if c.path != "" && fsys.IsLocal() {
		bin = c.path
	}
	cmd := fsys.Command(ctx, root, bin, args...)
	// A nil Stdin gives the child /dev/null. Codex otherwise blocks reading a
	// prompt from a pipe it will never receive.
	cmd.Stdin = nil
	// Cancelling a turn kills the process we spawned, but not a grandchild it
	// left behind — a node process behind a .cmd shim on Windows, say. That
	// grandchild keeps the output pipes open and Wait would block on them for
	// as long as it lives, stranding the session. WaitDelay bounds that.
	cmd.WaitDelay = 5 * time.Second
	if fsys.IsLocal() {
		cmd.Env = append(os.Environ(), "NO_COLOR=1", "CLICOLOR=0")
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		send(agent.EvDone{Err: err})
		return
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		send(agent.EvDone{Err: err})
		return
	}
	if err := cmd.Start(); err != nil {
		send(agent.EvDone{Err: fmt.Errorf("%s: %w", c.bin, err)})
		return
	}

	var errTail tail
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderrPipe)
		sc.Buffer(make([]byte, 0, 8<<10), 1<<20)
		for sc.Scan() {
			errTail.add(sc.Text())
		}
	}()

	dec := c.newDec()
	sc := bufio.NewScanner(stdout)
	// Agent CLIs emit whole tool results on one line; the default 64 KB cap is
	// far too small.
	sc.Buffer(make([]byte, 0, 256<<10), 64<<20)

	stopped := false
	for sc.Scan() {
		raw := sc.Bytes()
		if len(raw) == 0 || raw[0] != '{' {
			continue // banners and progress noise
		}
		dec.line(raw, func(e agent.Event) {
			if !send(e) {
				stopped = true
			}
		})
		if stopped {
			break
		}
	}
	dec.finish(func(e agent.Event) { send(e) })

	waitErr := cmd.Wait()
	wg.Wait()

	switch {
	case ctx.Err() != nil:
		send(agent.EvDone{Err: ctx.Err()})
	case dec.failure() != "":
		send(agent.EvDone{Err: fmt.Errorf("%s: %s", c.id, dec.failure())})
	case waitErr != nil:
		send(agent.EvDone{Err: fmt.Errorf("%s exited: %w%s", c.bin, waitErr, errTail.suffix())})
	default:
		send(agent.EvDone{})
	}
}

// tail keeps the last few stderr lines so a failure can explain itself without
// buffering a runaway log.
type tail struct {
	mu    sync.Mutex
	lines []string
}

func (t *tail) add(s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	t.mu.Lock()
	t.lines = append(t.lines, s)
	if len(t.lines) > 6 {
		t.lines = t.lines[len(t.lines)-6:]
	}
	t.mu.Unlock()
}

func (t *tail) suffix() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) == 0 {
		return ""
	}
	return "\n" + strings.Join(t.lines, "\n")
}
