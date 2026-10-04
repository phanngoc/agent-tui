//go:build !windows

package gateway

import (
	"os/exec"
	"syscall"
)

// detach starts the gateway in a session of its own, so closing the terminal
// does not take it down.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
