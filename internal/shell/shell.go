// Package shell picks the shell that runs a command string.
//
// The agent writes POSIX command lines, so a POSIX shell is used wherever one
// exists — including on Windows, where Git for Windows ships one. Only when
// there is none does this fall back to cmd.exe, which will not understand
// pipelines the model is likely to write.
//
// A container is always Linux however it is reached, so work that runs inside
// one keeps /bin/sh regardless of the host.
package shell

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// posix is the shell inside a container, and on every host but Windows.
const posix = "/bin/sh"

var (
	once sync.Once
	name string
	pre  []string
)

// For returns the program and the argument prefix that run one command string.
// Append the command itself to the returned args.
//
// local says whether the work happens on this machine; a remote filesystem is
// a Linux container and always takes the POSIX form.
func For(local bool) (string, []string) {
	if !local || runtime.GOOS != "windows" {
		return posix, []string{"-c"}
	}
	once.Do(resolve)
	return name, append([]string(nil), pre...)
}

// POSIX reports whether For(local) returns a shell with POSIX syntax. A caller
// that must compose a command line needs to know which quoting applies.
func POSIX(local bool) bool {
	n, _ := For(local)
	return !strings.EqualFold(filepath.Base(n), "cmd.exe")
}

func resolve() {
	if p := os.Getenv("AGENT_TUI_SHELL"); p != "" {
		name, pre = p, []string{"-c"}
		return
	}
	if p := findPOSIX(); p != "" {
		name, pre = p, []string{"-c"}
		return
	}
	name, pre = "cmd.exe", []string{"/C"}
}

// findPOSIX locates a Windows-native POSIX shell — one that understands a
// Windows working directory. The WSL launcher is deliberately not one: it runs
// the command in a different filesystem namespace, where the project root the
// agent was given does not exist.
func findPOSIX() string {
	var cands []string

	// Git for Windows installs bash beside git itself, so find it from
	// wherever git happens to be rather than guessing at Program Files.
	if git, err := exec.LookPath("git"); err == nil {
		if base := filepath.Dir(filepath.Dir(git)); base != "" {
			cands = append(cands,
				filepath.Join(base, "bin", "bash.exe"),
				filepath.Join(base, "usr", "bin", "bash.exe"))
		}
	}
	cands = append(cands,
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
	)
	for _, c := range cands {
		if isExec(c) {
			return c
		}
	}

	for _, n := range []string{"bash.exe", "sh.exe"} {
		if p, err := exec.LookPath(n); err == nil && !isWSLLauncher(p) {
			return p
		}
	}
	return ""
}

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// isWSLLauncher spots %SystemRoot%\System32\bash.exe and its WindowsApps
// alias, which enter WSL rather than running a shell here.
func isWSLLauncher(p string) bool {
	lp := strings.ToLower(filepath.Clean(p))
	return strings.Contains(lp, `\system32\`) || strings.Contains(lp, `\windowsapps\`)
}
