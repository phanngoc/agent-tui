//go:build windows

package mcp

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps a console server from flashing a window of its own when
// the app that starts it has none, which is the desktop build's case.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
