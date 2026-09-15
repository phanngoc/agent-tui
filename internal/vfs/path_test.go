package vfs

import "testing"

// The POSIX cases pin the behaviour these helpers had before they learned about
// Windows: a path with no drive letter and no backslash must come out exactly as
// it always did, so a macOS or Linux host — and every container path, whatever
// the host — is untouched.
func TestPOSIXPathsAreUnchanged(t *testing.T) {
	if got := Join("/a/b", "c", "d"); got != "/a/b/c/d" {
		t.Errorf("Join = %q", got)
	}
	if got := Join("/a/b/", "/c/"); got != "/a/b/c" {
		t.Errorf("Join trailing = %q", got)
	}
	if got := Dir("/a/b/c"); got != "/a/b" {
		t.Errorf("Dir = %q", got)
	}
	if got := Dir("/a"); got != "/" {
		t.Errorf("Dir of one element = %q, want the root", got)
	}
	if got := Base("/a/b/c"); got != "c" {
		t.Errorf("Base = %q", got)
	}
	if got := Rel("/a/b", "/a/b/c/d"); got != "c/d" {
		t.Errorf("Rel = %q", got)
	}
	if got := Rel("/a/b", "/other"); got != "/other" {
		t.Errorf("Rel outside should pass through, got %q", got)
	}
	if got := CleanPath("/a/b/../c/./d"); got != "/a/c/d" {
		t.Errorf("CleanPath = %q", got)
	}
	if !Within("/a", "/a/b") || Within("/a", "/b") {
		t.Error("Within got a POSIX case wrong")
	}
	if !IsAbs("/a") || IsAbs("a/b") {
		t.Error("IsAbs got a POSIX case wrong")
	}
}

func TestWindowsPaths(t *testing.T) {
	if got := Join(`C:\repo`, "internal", "ui"); got != `C:\repo\internal\ui` {
		t.Errorf("Join = %q", got)
	}
	// A part written with slashes still lands in the host's namespace.
	if got := Join(`C:\repo`, "internal/ui"); got != `C:\repo\internal\ui` {
		t.Errorf("Join of a slashed part = %q", got)
	}
	if got := Join(`C:\repo\`, `\internal\`); got != `C:\repo\internal` {
		t.Errorf("Join trailing = %q", got)
	}
	if got := Dir(`C:\repo\internal`); got != `C:\repo` {
		t.Errorf("Dir = %q", got)
	}
	if got := Dir(`C:\repo`); got != `C:\` {
		t.Errorf("Dir of one element = %q, want the volume root", got)
	}
	if got := Base(`C:\repo\internal`); got != "internal" {
		t.Errorf("Base = %q", got)
	}
	// Rel is a key for ignore rules and the index, which speak one separator.
	if got := Rel(`C:\repo`, `C:\repo\internal\ui`); got != "internal/ui" {
		t.Errorf("Rel = %q, want slash-separated", got)
	}
	if got := CleanPath(`C:\repo\internal\..`); got != `C:\repo` {
		t.Errorf("CleanPath = %q", got)
	}
	if !IsAbs(`C:\repo`) || IsAbs(`repo\x`) {
		t.Error("IsAbs got a Windows case wrong")
	}
}

// Windows filesystems do not distinguish case, and a drive letter reaches the
// app in both, so a containment check that folded neither would refuse work
// inside the project the user opened.
func TestWindowsContainmentIgnoresCaseAndSeparator(t *testing.T) {
	root := `C:\Users\me\repo`
	for _, p := range []string{
		`C:\Users\me\repo\internal\ui.go`,
		`c:\users\me\repo\internal\ui.go`,
		`C:/Users/me/repo/internal/ui.go`,
		`C:\Users\me\repo`,
	} {
		if !Within(root, p) {
			t.Errorf("Within(%q, %q) = false, want true", root, p)
		}
	}
	for _, p := range []string{
		`C:\Users\me\elsewhere\x.go`,
		`D:\Users\me\repo\x.go`,
		`C:\Users\me\repo\..\other\x.go`,
	} {
		if Within(root, p) {
			t.Errorf("Within(%q, %q) = true, want false", root, p)
		}
	}
}

// A Windows host can drive a Linux container, so both namespaces are live in
// the same process and the separator has to come from the path, not the build.
func TestContainerPathsStayPOSIXOnAWindowsHost(t *testing.T) {
	if got := Join("/workspace", "src", "main.go"); got != "/workspace/src/main.go" {
		t.Errorf("a container path must stay POSIX, got %q", got)
	}
	if got := Dir("/workspace/src"); got != "/workspace" {
		t.Errorf("Dir = %q", got)
	}
}
