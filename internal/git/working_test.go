package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

// repoWithChanges makes a repository with one commit, then changes a tracked
// file and adds an untracked one.
func repoWithChanges(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git here")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	write("main.go", "package main\n\nfunc main() {}\n")
	run("add", ".")
	run("commit", "-q", "-m", "first")
	write("main.go", "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n")
	write("new.go", "package main\n\nconst New = 1\n")
	return dir
}

func TestWorkingFilesListsChangedAndNew(t *testing.T) {
	dir := repoWithChanges(t)
	files, err := WorkingFiles(context.Background(), vfs.NewLocal(dir), dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]FileChange{}
	for _, f := range files {
		got[f.Path] = f
	}
	if m := got["main.go"]; m.Untracked || m.Added == 0 {
		t.Errorf("main.go = %+v, want a tracked change with additions", m)
	}
	if n, ok := got["new.go"]; !ok || !n.Untracked {
		t.Errorf("new.go = %+v, want an untracked file", n)
	}
}

func TestWorkingPatchShowsANewFileWhole(t *testing.T) {
	dir := repoWithChanges(t)
	out, err := WorkingPatch(context.Background(), vfs.NewLocal(dir), dir, FileChange{Path: "new.go", Untracked: true})
	if err != nil {
		t.Fatal(err)
	}
	files := ParsePatch(out)
	if len(files) != 1 || files[0].Added() != 3 {
		t.Fatalf("parsed %+v from:\n%s", files, out)
	}
	if !strings.Contains(out, "+const New = 1") {
		t.Errorf("patch:\n%s", out)
	}
}
