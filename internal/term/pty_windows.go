//go:build windows

package term

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/conpty"
	"golang.org/x/sys/windows"
)

// winPty is a Windows pseudo-console, the one Windows Terminal hosts shells
// in, as the desktop window uses for the core.
type winPty struct {
	c      *conpty.ConPty
	handle windows.Handle
}

func start(s Spec) (pty, error) {
	c, err := conpty.New(s.Cols, s.Rows, 0)
	if err != nil {
		return nil, fmt.Errorf("pseudo-console: %w", err)
	}
	env := s.Env
	if env == nil {
		env = termEnv(os.Environ())
	}
	// Spawn finds a bare name in the working folder, not on PATH.
	bin := s.Argv[0]
	if p, err := exec.LookPath(bin); err == nil {
		bin = p
	}
	_, h, err := c.Spawn(bin, s.Argv, &syscall.ProcAttr{Dir: s.Dir, Env: env})
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("starting %s: %w", s.Argv[0], err)
	}
	return &winPty{c: c, handle: windows.Handle(h)}, nil
}

// termEnv is the gateway's environment for a shell in a terminal: told it has
// colour, and rid of what says it has none.
func termEnv(env []string) []string {
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "NO_COLOR", "TERM", "COLORTERM", "TERM_PROGRAM":
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=agent-tui-web")
}

func (p *winPty) Read(b []byte) (int, error)  { return p.c.Read(b) }
func (p *winPty) Write(b []byte) (int, error) { return p.c.Write(b) }
func (p *winPty) Resize(cols, rows int) error { return p.c.Resize(cols, rows) }
func (p *winPty) Close() error                { return p.c.Close() }

func (p *winPty) Wait() (int, error) {
	if _, err := windows.WaitForSingleObject(p.handle, windows.INFINITE); err != nil {
		return -1, err
	}
	var code uint32
	err := windows.GetExitCodeProcess(p.handle, &code)
	_ = windows.CloseHandle(p.handle)
	return int(code), err
}
