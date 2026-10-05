//go:build !windows

package gateway

import (
	"os/exec"
	"syscall"
)

// detach starts the gateway in a session of its own, so closing the terminal
// does not take it down. There are no job objects to leave here.
func detach(cmd *exec.Cmd, _ bool) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
