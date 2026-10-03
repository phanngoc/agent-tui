package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitify turns the test model's project into a repository with a commit, then
// changes a file and adds one, so there is something to list.
func gitify(t *testing.T, m *Model) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git here")
	}
	dir := m.hostRoot()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("add", ".")
	run("commit", "-q", "-m", "first")
	mustWrite(t, filepath.Join(dir, "main.go"),
		"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n\tfmt.Println(\"changed\")\n}\n")
	mustWrite(t, filepath.Join(dir, "added.go"), "package main\n\nconst Added = 1\n")
}

// TestEmptyPreviewShowsTheChanges is the feature: with no file open, the
// preview is what the working tree changed.
func TestEmptyPreviewShowsTheChanges(t *testing.T) {
	m := newTestModel(t)
	gitify(t, m)
	follow(m, m.refreshChanges(0))

	out := stripANSI(m.View().Content)
	for _, want := range []string{"2 files changed", "main.go", "added.go", "new", "changed\")"} {
		if !strings.Contains(out, want) {
			t.Errorf("the changes pane is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "No file open") {
		t.Error("the empty-preview hint is still shown over the changes")
	}
}

func TestChangesKeysPickOpenAndComeBack(t *testing.T) {
	m := newTestModel(t)
	gitify(t, m)
	follow(m, m.refreshChanges(0))
	m.setFocus(focusPreview)

	first := m.changes.files[m.changes.sel].Path
	follow(m, m.onKey(key("down")))
	if m.changes.files[m.changes.sel].Path == first || m.changes.patchPath != m.changes.files[m.changes.sel].Path {
		t.Fatalf("down did not move to the next file and read its diff: sel=%d patch=%q", m.changes.sel, m.changes.patchPath)
	}

	// Back to main.go, and open it: at its first change.
	for m.changes.files[m.changes.sel].Path != "main.go" {
		follow(m, m.onKey(key("up")))
	}
	follow(m, m.onKey(key("enter")))
	if m.file == nil || m.file.Rel != "main.go" || m.fileLine != 7 {
		t.Fatalf("opened %+v at %d, want main.go at line 7", m.file, m.fileLine)
	}

	follow(m, m.onKey(key("q")))
	if m.file != nil || !m.showingChanges() {
		t.Error("q did not come back to the changes")
	}
}

func TestCleanTreeKeepsTheHint(t *testing.T) {
	m := newTestModel(t)
	gitify(t, m)
	// Undo the changes: commit them.
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = m.hostRoot()
	_ = cmd.Run()
	cmd = exec.Command("git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "second")
	cmd.Dir = m.hostRoot()
	_ = cmd.Run()

	follow(m, m.refreshChanges(0))
	if out := stripANSI(m.View().Content); !strings.Contains(out, "No file open") {
		t.Errorf("a clean tree should leave the usual hint:\n%s", out)
	}
}

func TestNotARepositoryKeepsTheHint(t *testing.T) {
	m := newTestModel(t)
	follow(m, m.refreshChanges(0))
	if m.showingChanges() {
		t.Error("a folder that is not a repository showed a changes listing")
	}
}
