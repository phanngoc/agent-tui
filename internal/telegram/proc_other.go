//go:build !windows

package telegram

import "os/exec"

func hideWindow(*exec.Cmd) {}
