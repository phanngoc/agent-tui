package git

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

// Branches is what the composer shows of a working tree: the branch it is
// on and the ones it could switch to.
type Branches struct {
	Current string `json:"current"`
	// Detached is a checkout of a commit rather than a branch: Current is
	// then the short sha.
	Detached bool     `json:"detached,omitempty"`
	Local    []string `json:"local"`
	Remote   []string `json:"remote,omitempty"`
	// Dirty counts changed files, which a switch may refuse to carry.
	Dirty int `json:"dirty"`
	// Top is the working tree's root; Worktree says it is a linked worktree
	// rather than the repository's main one.
	Top      string `json:"top"`
	Worktree bool   `json:"worktree,omitempty"`
}

// ListBranches reads the branches of the repository dir is in, most
// recently committed first.
func ListBranches(ctx context.Context, fsys vfs.FS, dir string) (Branches, error) {
	var b Branches
	top, ok := Root(ctx, fsys, dir)
	if !ok {
		return b, errors.New("not a git repository")
	}
	b.Top = top
	if out, err := Run(ctx, fsys, dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		b.Current = strings.TrimSpace(string(out))
	}
	if b.Current == "HEAD" || b.Current == "" {
		b.Detached = true
		if out, err := Run(ctx, fsys, dir, "rev-parse", "--short", "HEAD"); err == nil {
			b.Current = strings.TrimSpace(string(out))
		}
	}
	out, err := Run(ctx, fsys, dir, "for-each-ref", "--sort=-committerdate", "--format=%(refname)", "refs/heads", "refs/remotes")
	if err != nil {
		return b, err
	}
	for _, ref := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		switch {
		case strings.HasPrefix(ref, "refs/heads/"):
			b.Local = append(b.Local, strings.TrimPrefix(ref, "refs/heads/"))
		case strings.HasPrefix(ref, "refs/remotes/") && !strings.HasSuffix(ref, "/HEAD"):
			b.Remote = append(b.Remote, strings.TrimPrefix(ref, "refs/remotes/"))
		}
	}
	if b.Local == nil {
		b.Local = []string{}
	}
	if out, err := Run(ctx, fsys, dir, "status", "--porcelain"); err == nil {
		for _, l := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(l) != "" {
				b.Dirty++
			}
		}
	}
	gitDir, err1 := Run(ctx, fsys, dir, "rev-parse", "--git-dir")
	common, err2 := Run(ctx, fsys, dir, "rev-parse", "--git-common-dir")
	if err1 == nil && err2 == nil {
		b.Worktree = strings.TrimSpace(string(gitDir)) != strings.TrimSpace(string(common))
	}
	return b, nil
}

var branchName = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// ValidBranch says name can be a branch, and is not an option in disguise.
func ValidBranch(name string) bool {
	return name != "" && !strings.HasPrefix(name, "-") && !strings.Contains(name, "..") &&
		!strings.HasSuffix(name, "/") && !strings.HasSuffix(name, ".lock") && branchName.MatchString(name)
}

// Switch checks out branch in dir: a local branch, a remote one (which git
// sets up to track), or a new branch from HEAD when create is set. Git
// refuses a switch that would overwrite uncommitted changes, and says so.
func Switch(ctx context.Context, fsys vfs.FS, dir, branch string, create bool) error {
	if !ValidBranch(branch) {
		return errors.New("that is not a branch name")
	}
	args := []string{"switch", branch}
	if create {
		args = []string{"switch", "-c", branch}
	} else if i := strings.IndexByte(branch, '/'); i > 0 {
		// origin/feature: a local branch tracking it, named feature.
		if _, err := Run(ctx, fsys, dir, "show-ref", "--verify", "--quiet", "refs/remotes/"+branch); err == nil {
			local := branch[i+1:]
			if _, err := Run(ctx, fsys, dir, "show-ref", "--verify", "--quiet", "refs/heads/"+local); err != nil {
				args = []string{"switch", "-c", local, "--track", branch}
			} else {
				args = []string{"switch", local}
			}
		}
	}
	_, err := Run(ctx, fsys, dir, args...)
	return err
}

// WorktreeDir is where a worktree for branch goes: beside the repository,
// in <repo>.worktrees/<branch>, outside it so it is nothing git or the
// project's tools would pick up as a file of the main tree.
func WorktreeDir(fsys vfs.FS, top, branch string) string {
	name := strings.NewReplacer("/", "-", `\`, "-").Replace(branch)
	if _, posix := fsys.(*vfs.WSL); posix || strings.HasPrefix(top, "/") {
		return path.Join(path.Dir(top), path.Base(top)+".worktrees", name)
	}
	return filepath.Join(filepath.Dir(top), filepath.Base(top)+".worktrees", name)
}

// AddWorktree makes a linked worktree of the repository dir is in, on
// branch: a new branch from base (HEAD when empty) when create is set,
// otherwise an existing one. It returns the worktree's directory.
func AddWorktree(ctx context.Context, fsys vfs.FS, dir, branch, base string, create bool) (string, error) {
	if !ValidBranch(branch) {
		return "", errors.New("that is not a branch name")
	}
	if base != "" && !ValidBranch(base) {
		return "", errors.New("that is not a branch to start from")
	}
	top, ok := Root(ctx, fsys, dir)
	if !ok {
		return "", errors.New("not a git repository: a worktree needs one")
	}
	// From a linked worktree, the new one still goes beside the main tree.
	if out, err := Run(ctx, fsys, dir, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		common := strings.TrimSpace(string(out))
		if strings.HasSuffix(common, "/.git") || strings.HasSuffix(common, `\.git`) {
			top = common[:len(common)-len("/.git")]
		}
	}
	wt := WorktreeDir(fsys, top, branch)
	args := []string{"worktree", "add"}
	if create {
		args = append(args, "-b", branch, wt)
		if base != "" {
			args = append(args, base)
		}
	} else {
		args = append(args, wt, branch)
	}
	if _, err := Run(ctx, fsys, dir, args...); err != nil {
		return "", err
	}
	return wt, nil
}
