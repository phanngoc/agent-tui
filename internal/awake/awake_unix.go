//go:build !windows

package awake

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
)

// cmdHolder holds the machine awake with a helper process for as long as
// the process lives: caffeinate on macOS, systemd-inhibit on Linux.
type cmdHolder struct {
	args func(display bool) []string
	cmd  *exec.Cmd
}

func newHolder() (holder, string) {
	switch runtime.GOOS {
	case "darwin":
		if p, err := exec.LookPath("caffeinate"); err == nil {
			return &cmdHolder{args: func(display bool) []string {
				a := []string{p, "-i", "-w", strconv.Itoa(os.Getpid())}
				if display {
					a = append(a, "-d")
				}
				return a
			}}, "caffeinate"
		}
	case "linux":
		if p, err := exec.LookPath("systemd-inhibit"); err == nil {
			return &cmdHolder{args: func(display bool) []string {
				// idle covers the screen blanking too; the lid is the
				// user's own word and is left alone.
				_ = display
				return []string{p, "--what=idle:sleep", "--who=agent-tui", "--why=an agent is working", "--mode=block", "sleep", "infinity"}
			}}, "systemd-inhibit"
		}
	}
	return nil, "none (no caffeinate or systemd-inhibit)"
}

func (h *cmdHolder) hold(display bool) error {
	h.release()
	a := h.args(display)
	cmd := exec.Command(a[0], a[1:]...)
	if err := cmd.Start(); err != nil {
		return errors.New("starting " + a[0] + ": " + err.Error())
	}
	h.cmd = cmd
	go func() { _ = cmd.Wait() }()
	return nil
}

func (h *cmdHolder) release() {
	if h.cmd != nil && h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
	}
	h.cmd = nil
}
