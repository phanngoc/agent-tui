package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/charmbracelet/x/conpty"
	"golang.org/x/sys/windows"
)

// The core, running.
//
// agent-tui is started exactly as a terminal would start it: in a Windows
// pseudo-console, the same ConPTY that Windows Terminal hosts shells in. It
// does not know it is in a window rather than a terminal, which is the point —
// the core is not changed, not forked, not linked in. Its output is read on one
// goroutine into the screen; what the screen has to send back is written on
// another; a third waits for it to exit.

// Core is one run of agent-tui.
type Core struct {
	pty    *conpty.ConPty
	handle windows.Handle

	once sync.Once
	done chan struct{}
	code uint32
	err  error
}

// findCore locates the agent-tui binary: named by AGENT_TUI_CORE, beside this
// executable, or on PATH — in that order, so a build sitting next to the
// window it belongs to wins over whatever older one is installed.
func findCore() (string, error) {
	if p := os.Getenv("AGENT_TUI_CORE"); p != "" {
		return p, nil
	}
	if self, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(self), "agent-tui.exe")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	if p, err := exec.LookPath("agent-tui"); err == nil {
		return p, nil
	}
	return "", errors.New("agent-tui.exe was not found beside this program or on PATH — install it with the deploy script, or set AGENT_TUI_CORE")
}

// startDir is where the core starts when nothing says otherwise.
//
// Started from a terminal, that is the terminal's directory, as it is for the
// core itself. Started from the Start menu or a shortcut, the working directory
// is this program's own folder or the Windows directory — and opening an agent
// on either is never what was meant, so it is the home directory instead.
// agent-tui's own /settings can still send it somewhere else.
func startDir() string {
	wd, err := os.Getwd()
	home, _ := os.UserHomeDir()
	if err != nil {
		return home
	}
	if self, err := os.Executable(); err == nil && samePath(wd, filepath.Dir(self)) {
		return home
	}
	if win := os.Getenv("WINDIR"); win != "" && hasPrefixFold(wd, win) {
		return home
	}
	return wd
}

func samePath(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// coreEnv is the environment the core starts with: this program's own, less
// what describes the terminal this program was started from.
//
// That is not the terminal the core runs in. Launched from a shell with
// NO_COLOR set, or from a CI-like one with TERM=dumb, the window inherited
// both and the core believed them, and drew a colour screen in grey. The
// window is a colour terminal whoever started it.
func coreEnv(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "TERM", "COLORTERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION":
			continue
		}
		out = append(out, kv)
	}
	return out
}

// StartCore runs agent-tui in a pseudo-console of the given size, wired to the
// screen. onOutput is called after each chunk of output reaches the screen.
func StartCore(t *Term, bin string, args []string, dir string, onOutput func()) (*Core, error) {
	t.mu.Lock()
	cols, rows := t.cols, t.rows
	t.mu.Unlock()

	pty, err := conpty.New(cols, rows, 0)
	if err != nil {
		return nil, fmt.Errorf("pseudo-console: %w", err)
	}

	env := append(coreEnv(os.Environ()),
		// The pseudo-console carries 24-bit colour; saying so spares the core
		// from guessing, and a guess that comes out at 256 colours turns every
		// theme into an approximation of itself.
		"COLORTERM=truecolor",
		"TERM=xterm-256color",
		"TERM_PROGRAM=agent-tui-desktop",
	)
	argv := append([]string{bin}, args...)
	_, handle, err := pty.Spawn(bin, argv, &syscall.ProcAttr{Dir: dir, Env: env})
	if err != nil {
		_ = pty.Close()
		return nil, fmt.Errorf("starting %s: %w", bin, err)
	}

	c := &Core{pty: pty, handle: windows.Handle(handle), done: make(chan struct{})}
	bindToWindow(c.handle)

	// Output: the pipe is read in large chunks and handed to the screen whole.
	// A repaint per chunk, not per byte, and the window coalesces repaints
	// into frames on its own.
	go func() {
		buf := make([]byte, 64<<10)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				t.Write(buf[:n])
				onOutput()
			}
			if err != nil {
				return
			}
		}
	}()

	// Input: whatever the screen encodes — keys, mouse, replies to queries.
	go func() {
		buf := make([]byte, 4<<10)
		for {
			n, err := t.Read(buf)
			if n > 0 {
				if _, werr := pty.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Exit: when the core ends, the pseudo-console is closed, which ends the
	// output reader.
	go func() {
		_, werr := windows.WaitForSingleObject(c.handle, windows.INFINITE)
		var code uint32
		if werr == nil {
			werr = windows.GetExitCodeProcess(c.handle, &code)
		}
		c.finish(code, werr)
		onOutput()
	}()

	return c, nil
}

// The core's lifetime is the window's.
//
// Closing the window stops the core on the way out, but a window can also end
// without a way out — killed from Task Manager, crashed — and an agent left
// running with nothing on screen is the worst kind of process: one that may
// still be editing files. So the core is put in a job object that kills what
// is in it when the last handle to it closes, and the only handle is this
// process's. However this process ends, Windows ends the core with it.
//
// Except the gateway. The core starts one when there is none, and it is a
// service of its own — the web runs on it — so the job lets it leave, and it
// does (CREATE_BREAKAWAY_FROM_JOB). Everything else the core runs stays in.
var (
	jobOnce sync.Once
	job     windows.Handle
)

func bindToWindow(proc windows.Handle) {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
		if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			_ = windows.CloseHandle(h)
			return
		}
		job = h
	})
	if job != 0 {
		_ = windows.AssignProcessToJobObject(job, proc)
	}
}

// finish records how the core ended and lets go of the pseudo-console.
//
// The ending is published first and the console closed after, on a goroutine
// of its own: ClosePseudoConsole waits for the console host to flush what it
// has left, and that can take a while or, with nobody reading, for ever. A
// window waiting on it is a window that does not close — which is exactly
// what it did, until this was the other way round.
func (c *Core) finish(code uint32, err error) {
	c.once.Do(func() {
		c.code, c.err = code, err
		close(c.done)
		go func() {
			_ = c.pty.Close()
			_ = windows.CloseHandle(c.handle)
		}()
	})
}

// Resize tells the core the screen changed size.
func (c *Core) Resize(cols, rows int) { _ = c.pty.Resize(cols, rows) }

// Exited reports whether the core has ended, and how.
func (c *Core) Exited() (bool, uint32) {
	select {
	case <-c.done:
		return true, c.code
	default:
		return false, 0
	}
}

// Stop ends the core. The window is closing, and a process left behind would
// be an agent still running with nothing on screen to show it.
//
// It waits a moment and no longer: the job object ends the core with this
// process in any case, so the wait is for tidiness, and a window that hangs
// on the way out is worse than one that leaves the last step to Windows.
func (c *Core) Stop() {
	if ok, _ := c.Exited(); ok {
		return
	}
	_ = windows.TerminateProcess(c.handle, 1)
	select {
	case <-c.done:
	case <-time.After(1500 * time.Millisecond):
	}
}
