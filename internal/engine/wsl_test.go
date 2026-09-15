package engine

import (
	"errors"
	"os/exec"
	"testing"
)

// Inside WSL the CLIs are frequently installed on the Windows side only, where
// they are reachable as claude.exe. LookPath does not try .exe on Linux, so the
// engine reported "not installed" on a machine where it was plainly installed.
func TestWSLFindsTheWindowsBuild(t *testing.T) {
	defer pinWSL(true)()
	defer pinLookPath(map[string]string{
		"claude.exe": `C:\Users\me\.local\bin\claude.exe`,
	})()

	path, win, err := lookAgent("claude")
	if err != nil {
		t.Fatalf("lookAgent: %v", err)
	}
	if path != `C:\Users\me\.local\bin\claude.exe` {
		t.Errorf("path = %q", path)
	}
	if !win {
		t.Error("a .exe reached from WSL is a Windows binary and must be flagged")
	}
}

// A Linux build on PATH is still preferred: it shares this filesystem, so it
// needs no path translation.
func TestWSLPrefersTheLinuxBuild(t *testing.T) {
	defer pinWSL(true)()
	defer pinLookPath(map[string]string{
		"claude":     "/usr/local/bin/claude",
		"claude.exe": `C:\claude.exe`,
	})()

	path, win, err := lookAgent("claude")
	if err != nil {
		t.Fatalf("lookAgent: %v", err)
	}
	if path != "/usr/local/bin/claude" || win {
		t.Errorf("lookAgent = %q win=%v, want the native build", path, win)
	}
}

// Off WSL nothing changes: a missing binary stays missing rather than being
// searched for under a name that platform never uses.
func TestNonWSLDoesNotGuessAtExe(t *testing.T) {
	defer pinWSL(false)()
	defer pinLookPath(map[string]string{"claude.exe": `C:\claude.exe`})()

	if _, _, err := lookAgent("claude"); err == nil {
		t.Error("lookAgent should have failed without the interop fallback")
	}
}

func TestArgPathOnlyTranslatesForAWindowsBinary(t *testing.T) {
	c := newCodex("/project")
	if got := c.argPath("/project"); got != "/project" {
		t.Errorf("a native binary needs no translation, got %q", got)
	}
}

// ---- helpers ---------------------------------------------------------------

func pinWSL(yes bool) func() {
	wslOnce.Do(func() {})
	prev := wslOnce.yes
	wslOnce.yes = yes
	return func() { wslOnce.yes = prev }
}

func pinLookPath(found map[string]string) func() {
	prev := lookPath
	lookPath = func(name string) (string, error) {
		if p, ok := found[name]; ok {
			return p, nil
		}
		return "", errors.New("not found: " + name)
	}
	return func() { lookPath = prev }
}

var _ = exec.LookPath
