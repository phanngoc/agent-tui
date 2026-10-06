//go:build windows

package telegram

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps the poller from opening a console of its own: the
// gateway runs without one.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
