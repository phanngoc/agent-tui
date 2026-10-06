package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

func repoWithBranch(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git here")
	}
	dir := filepath.Join(t.TempDir(), "app")
	_ = os.MkdirAll(dir, 0o755)
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "first")
	run("branch", "feature")
	return dir
}

// The composer's branch menu: what the tree is on, what it could switch to,
// a switch, a new branch, and a worktree beside the repository.
func TestBranchesSwitchAndWorktree(t *testing.T) {
	ctx := context.Background()
	dir := repoWithBranch(t)
	fs := vfs.NewLocal(dir)
	b, err := ListBranches(ctx, fs, dir)
	if err != nil || b.Current != "main" || !slices.Contains(b.Local, "feature") || b.Dirty != 0 || b.Worktree {
		t.Fatalf("branches %+v %v", b, err)
	}
	if err := Switch(ctx, fs, dir, "feature", false); err != nil {
		t.Fatal(err)
	}
	if err := Switch(ctx, fs, dir, "fix/thing", true); err != nil {
		t.Fatal(err)
	}
	if b, _ = ListBranches(ctx, fs, dir); b.Current != "fix/thing" {
		t.Fatalf("after switching: %+v", b)
	}
	if err := Switch(ctx, fs, dir, "--orphan", false); err == nil {
		t.Fatal("an option was taken for a branch")
	}

	wt, err := AddWorktree(ctx, fs, dir, "agent/try", "main", true)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(dir), "app.worktrees", "agent-try")
	if !sameDir(wt, want) {
		t.Fatalf("worktree at %s; want %s", wt, want)
	}
	wb, err := ListBranches(ctx, vfs.NewLocal(wt), wt)
	if err != nil || wb.Current != "agent/try" || !wb.Worktree {
		t.Fatalf("in the worktree: %+v %v", wb, err)
	}
	// From inside a worktree, another still goes beside the main tree.
	wt2, err := AddWorktree(ctx, vfs.NewLocal(wt), wt, "agent/two", "", true)
	if err != nil || !sameDir(filepath.Dir(wt2), filepath.Dir(want)) {
		t.Fatalf("second worktree at %s: %v", wt2, err)
	}
}

// sameDir compares directories however they are spelled: a temp folder's
// short 8.3 name and its long one are the same folder.
func sameDir(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

func TestValidBranch(t *testing.T) {
	for _, ok := range []string{"main", "feat/x-1", "v1.2"} {
		if !ValidBranch(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "-x", "a..b", "x/", "x.lock", "a b", "a;rm"} {
		if ValidBranch(bad) {
			t.Errorf("%q taken", bad)
		}
	}
}
