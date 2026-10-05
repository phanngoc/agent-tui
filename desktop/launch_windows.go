package main

import (
	"bytes"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// launchCore runs `agent-tui <args>` with no window at all and waits for it:
// what the Start menu's "agent-tui web" and "agent-tui gateway" entries do.
// agent-tui.exe is a console program, and a shortcut straight to it flashes a
// console up for the second it takes to start the gateway and open the
// browser. This program is a windowed one, so through it nothing appears
// but the browser. A failure is said in a message box, since there is no
// console to say it in.
func launchCore(args []string) int {
	bin, err := findCore()
	if err != nil {
		alert(err.Error())
		return 1
	}
	const createNoWindow = 0x08000000
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(out.String())
		if msg == "" {
			msg = err.Error()
		}
		alert("agent-tui " + strings.Join(args, " ") + ":\n\n" + msg)
		return 1
	}
	return 0
}

func alert(text string) {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString("agent-tui")
	_, _ = windows.MessageBox(0, t, c, windows.MB_OK|windows.MB_ICONWARNING)
}
