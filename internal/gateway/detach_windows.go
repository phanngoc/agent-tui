//go:build windows

package gateway

import (
	"os/exec"
	"syscall"
)

// detach starts the gateway on a console of its own that is never shown, and
// outside this process's group, so closing the terminal does not take it down.
//
// It must have a console. A process started with DETACHED_PROCESS has none,
// and then every console program it runs — claude, wsl.exe, git, an MCP
// server, several per turn — is given a new one, which Windows Terminal opens
// as an empty window that flashes up and away. CREATE_NO_WINDOW gives the
// gateway a hidden console instead, which those programs inherit. (Windows
// ignores CREATE_NO_WINDOW alongside DETACHED_PROCESS, so the two are not
// combined.)
//
// breakaway also takes it out of the starter's job object. The desktop window
// puts the terminal app in a job that kills everything in it when the window
// closes, which is right for the terminal app and wrong for the gateway: the
// web it serves would die with a window it has nothing to do with. A job that
// does not allow leaving refuses the start, and the caller tries again
// without.
func detach(cmd *exec.Cmd, breakaway bool) {
	const (
		createNewProcessGroup  = 0x00000200
		createNoWindow         = 0x08000000
		createBreakawayFromJob = 0x01000000
	)
	flags := uint32(createNewProcessGroup | createNoWindow)
	if breakaway {
		flags |= createBreakawayFromJob
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags}
}
