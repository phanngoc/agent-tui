package shell

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A container is Linux however it is reached, so work inside one keeps /bin/sh
// even when the host running this process is Windows.
func TestContainerAlwaysGetsPOSIX(t *testing.T) {
	name, args := For(false)
	if name != posix || len(args) != 1 || args[0] != "-c" {
		t.Errorf("For(remote) = %q %v, want %q -c", name, args, posix)
	}
}

func TestHostShellRuns(t *testing.T) {
	name, args := For(true)
	if runtime.GOOS != "windows" {
		if name != posix {
			t.Fatalf("For(local) = %q, want %q off Windows", name, posix)
		}
		return
	}
	if name == "" {
		t.Fatal("no shell was chosen")
	}
	// Whatever was picked has to actually run something.
	out, err := exec.Command(name, append(args, "echo hello")...).Output()
	if err != nil {
		t.Fatalf("%s could not run a command: %v", name, err)
	}
	if !strings.Contains(string(out), "hello") {
		t.Errorf("output = %q, want it to contain hello", out)
	}
}

// The WSL launcher is a shell, but not one that can see the project: it enters
// a different filesystem namespace, where the Windows path the agent was given
// does not exist. Picking it would break every command.
func TestWSLLauncherIsNotChosen(t *testing.T) {
	for _, p := range []string{
		`C:\Windows\System32\bash.exe`,
		`C:\Users\me\AppData\Local\Microsoft\WindowsApps\bash.exe`,
		`c:\windows\system32\bash.exe`,
	} {
		if !isWSLLauncher(p) {
			t.Errorf("isWSLLauncher(%q) = false, want true", p)
		}
	}
	for _, p := range []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files\Git\usr\bin\bash.exe`,
		`/bin/sh`,
	} {
		if isWSLLauncher(p) {
			t.Errorf("isWSLLauncher(%q) = true, want false", p)
		}
	}
}

// The agent writes POSIX command lines; a caller that has to quote one needs to
// know whether it landed on cmd.exe instead.
func TestPOSIXReportsTheSyntax(t *testing.T) {
	if !POSIX(false) {
		t.Error("a container shell is POSIX")
	}
	name, _ := For(true)
	want := !strings.EqualFold(filepath.Base(name), "cmd.exe")
	if POSIX(true) != want {
		t.Errorf("POSIX(true) = %v for shell %q", POSIX(true), name)
	}
}
