package git

import (
	"context"
	"strings"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

// The working tree: what has changed since the last commit, staged or not,
// and the files that are new. It is what an agent's work looks like before
// anyone commits it, which is the question someone watching one asks.

// maxUntracked bounds the new files listed. A build directory nobody ignored
// is thousands of them, and a list that long says nothing a count would not.
const maxUntracked = 300

// maxPatch bounds the diff read for one file. A generated file can be
// megabytes of change, and nobody reads past the first screens of it here.
const maxPatch = 512 << 10

// WorkingFiles lists what the working tree changed against HEAD, with line
// counts, followed by the files git does not track yet.
func WorkingFiles(ctx context.Context, fsys vfs.FS, dir string) ([]FileChange, error) {
	out, err := Run(ctx, fsys, dir,
		"diff", "HEAD", "--numstat", "-z", "--no-color", "--find-renames")
	if err != nil {
		// A repository with no commits has no HEAD; what it has is staged.
		out, err = Run(ctx, fsys, dir, "diff", "--cached", "--numstat", "-z", "--no-color")
		if err != nil {
			return nil, err
		}
	}
	files := parseNumstat(string(out))

	untracked, err := Run(ctx, fsys, dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err == nil {
		n := 0
		for _, p := range strings.Split(string(untracked), "\x00") {
			if p == "" {
				continue
			}
			if n++; n > maxUntracked {
				break
			}
			files = append(files, FileChange{Path: p, Untracked: true})
		}
	}
	return files, nil
}

// WorkingPatch is the diff of one changed file against HEAD; a new file is
// shown whole, as added.
func WorkingPatch(ctx context.Context, fsys vfs.FS, dir string, f FileChange) (string, error) {
	var out []byte
	var err error
	if f.Untracked {
		// --no-index exits 1 when the files differ, which for a new file is
		// always; what it printed is the answer, not an error.
		out, err = Run(ctx, fsys, dir, "diff", "--no-index", "--no-color", "--", "/dev/null", f.Path)
		if len(out) > 0 {
			err = nil
		}
	} else {
		args := []string{"diff", "HEAD", "--no-color", "--find-renames", "-U3", "--", f.Path}
		if f.Old != "" {
			args = append(args, f.Old)
		}
		out, err = Run(ctx, fsys, dir, args...)
	}
	if err != nil {
		return "", err
	}
	if len(out) > maxPatch {
		out = out[:maxPatch]
	}
	return string(out), nil
}
