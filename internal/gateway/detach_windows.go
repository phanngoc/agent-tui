//go:build windows

package gateway

import (
	"os/exec"
	"syscall"
)

// detach starts the gateway with no console of its own and outside this
// process's group, so closing the terminal does not take it down.
func detach(cmd *exec.Cmd) {
	const (
		detachedProcess       = 0x00000008
		createNewProcessGroup = 0x00000200
		createNoWindow        = 0x08000000
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true,
		CreationFlags: detachedProcess | createNewProcessGroup | createNoWindow}
}
