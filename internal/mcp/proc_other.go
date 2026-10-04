//go:build !windows

package mcp

import "os/exec"

func hideWindow(*exec.Cmd) {}
