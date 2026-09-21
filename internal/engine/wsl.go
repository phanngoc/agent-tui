package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Inside WSL the coding-agent CLIs are often installed on the Windows side
// only, reachable through interop as claude.exe rather than claude. Go's
// LookPath does not try .exe on Linux, so those engines looked "not installed"
// on a machine where they plainly were.
//
// A Windows binary reached this way runs in the Windows filesystem namespace:
// it is handed Windows paths, even though this process speaks Linux ones.

var wslOnce struct {
	sync.Once
	yes bool
}

// underWSL reports whether this is a Linux build running on WSL.
func underWSL() bool {
	wslOnce.Do(func() {
		if runtime.GOOS != "linux" {
			return
		}
		if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
			wslOnce.yes = true
			return
		}
		b, err := os.ReadFile("/proc/version")
		wslOnce.yes = err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
	})
	return wslOnce.yes
}

// lookAgent resolves a CLI by name. Under WSL it also accepts the Windows build
// reached through interop, and reports which one it found: a Windows binary
// needs its path arguments translated, a Linux one does not.
func lookAgent(bin string) (path string, windows bool, err error) {
	path, err = lookPath(bin)
	if err == nil {
		return path, isWindowsExe(path), nil
	}
	if !underWSL() {
		return "", false, err
	}
	// Fall back to the Windows build; keep the original error if there is none,
	// because "not installed" is the more useful thing to say.
	if p, e := lookPath(bin + ".exe"); e == nil {
		return p, true, nil
	}
	return "", false, err
}

func isWindowsExe(p string) bool {
	return strings.EqualFold(filepath.Ext(p), ".exe")
}

// toWindowsPath converts a WSL path into the form a Windows binary understands.
// It returns p unchanged when the conversion is not possible, which keeps a
// misconfigured machine working as well as it did before rather than worse.
func toWindowsPath(p string) string {
	if p == "" {
		return p
	}
	out, err := exec.Command("wslpath", "-w", p).Output()
	if err != nil {
		return p
	}
	if s := strings.TrimSpace(string(out)); s != "" {
		return s
	}
	return p
}

// probeVersion asks a binary to identify itself, which is also the cheapest
// proof that it can run here at all.
func probeVersion(path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, path, "--version").Output()
}
