// Package git reads a repository's history and diffs.
//
// Everything runs through the session's vfs.FS, so a session working inside a
// WSL distribution or a container reads that repository rather than a
// same-named one on the host. Nothing here writes: this is the reading half,
// and staging or committing would be a different file with different care.
package git

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

// Field and record separators. Git will happily put a newline in a subject and
// a tab in a filename, so the format is delimited by bytes that cannot appear
// in either rather than by whitespace.
const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
)

// Commit is one entry in the history.
type Commit struct {
	SHA     string
	Short   string
	Subject string
	Body    string
	Author  string
	When    time.Time
	// Refs are the branch and tag names pointing at this commit, as git prints
	// them: "HEAD -> main", "origin/main", "tag: v1.0".
	Refs []string
	// Parents beyond the first mark a merge, which is the only thing the graph
	// column needs to know.
	Parents []string
}

// Merge reports whether this commit joins two histories.
func (c Commit) Merge() bool { return len(c.Parents) > 1 }

// FileChange is one file touched by a commit, with its line counts.
type FileChange struct {
	Path string
	// Old is the previous path of a renamed file, empty otherwise.
	Old     string
	Added   int
	Deleted int
	Binary  bool
}

// Run executes a git command in dir and returns its stdout.
func Run(ctx context.Context, fsys vfs.FS, dir string, args ...string) ([]byte, error) {
	cmd := fsys.Command(ctx, dir, "git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("%s", firstLine(msg))
	}
	return stdout.Bytes(), nil
}

// Root returns the top of the repository dir belongs to, and whether there is
// one at all. The answer is the worktree root rather than dir itself, so the
// history shown is the repository's and not a subdirectory's slice of it.
func Root(ctx context.Context, fsys vfs.FS, dir string) (string, bool) {
	out, err := Run(ctx, fsys, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", false
	}
	return root, true
}

// logFormat asks for everything the list needs in one pass.
var logFormat = strings.Join([]string{
	"%H", "%h", "%an", "%aI", "%D", "%P", "%s", "%b",
}, fieldSep) + recordSep

// Log reads the most recent commits, newest first.
func Log(ctx context.Context, fsys vfs.FS, dir string, limit int) ([]Commit, error) {
	if limit <= 0 {
		limit = 300
	}
	out, err := Run(ctx, fsys, dir,
		"log", "--max-count="+strconv.Itoa(limit), "--format="+logFormat)
	if err != nil {
		return nil, err
	}
	return parseLog(string(out)), nil
}

func parseLog(s string) []Commit {
	var out []Commit
	for _, rec := range strings.Split(s, recordSep) {
		// Records are separated, not terminated, so each one after the first
		// carries the newline git wrote at the end of the previous.
		rec = strings.TrimLeft(rec, "\n")
		if strings.TrimSpace(rec) == "" {
			continue
		}
		f := strings.Split(rec, fieldSep)
		if len(f) < 7 {
			continue
		}
		c := Commit{
			SHA: f[0], Short: f[1], Author: f[2],
			Subject: f[6],
		}
		if len(f) > 7 {
			c.Body = strings.TrimSpace(f[7])
		}
		if t, err := time.Parse(time.RFC3339, f[3]); err == nil {
			c.When = t
		}
		c.Refs = parseRefs(f[4])
		if p := strings.Fields(f[5]); len(p) > 0 {
			c.Parents = p
		}
		out = append(out, c)
	}
	return out
}

// parseRefs splits the %D decoration. "HEAD -> main" is kept whole, because
// which branch is checked out is the useful part of it.
func parseRefs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	for _, r := range strings.Split(s, ", ") {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// Files lists what a commit changed, with added and deleted line counts.
func Files(ctx context.Context, fsys vfs.FS, dir, sha string) ([]FileChange, error) {
	// -z keeps paths intact: a filename may contain anything but NUL, and
	// without it git escapes and quotes the awkward ones.
	out, err := Run(ctx, fsys, dir,
		"show", "--numstat", "--format=", "-z", "--no-color", sha)
	if err != nil {
		return nil, err
	}
	return parseNumstat(string(out)), nil
}

// parseNumstat reads `added \t deleted \t path` records.
//
// With -z a rename spends three NUL-terminated fields instead of one: the
// counts and an empty path, then the old name, then the new one.
func parseNumstat(s string) []FileChange {
	fields := strings.Split(s, "\x00")
	var out []FileChange
	for i := 0; i < len(fields); i++ {
		rec := strings.TrimLeft(fields[i], "\n")
		if strings.TrimSpace(rec) == "" {
			continue
		}
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		fc := FileChange{Path: parts[2]}
		if parts[0] == "-" && parts[1] == "-" {
			fc.Binary = true
		} else {
			fc.Added, _ = strconv.Atoi(parts[0])
			fc.Deleted, _ = strconv.Atoi(parts[1])
		}
		if fc.Path == "" && i+2 < len(fields) {
			fc.Old, fc.Path = fields[i+1], fields[i+2]
			i += 2
		}
		out = append(out, fc)
	}
	return out
}

// Patch returns the unified diff for a commit, or for one file in it when
// path is given.
//
// A merge shows nothing by default, because git cannot say which parent to
// compare against; --first-parent picks the one the branch was on, which is
// what someone reading the history means by "what did this merge bring in".
func Patch(ctx context.Context, fsys vfs.FS, dir, sha, path string) (string, error) {
	args := []string{
		"show", "--format=", "--no-color", "--first-parent",
		"--find-renames", "-U3", sha,
	}
	// An empty pathspec is an error rather than a wildcard, so the separator
	// only goes in when there is something after it.
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := Run(ctx, fsys, dir, args...)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Status counts what is uncommitted, for the line the list shows above the
// history. It is deliberately a count and not a listing: the working tree is
// the one thing here that changes while you look at it.
func Status(ctx context.Context, fsys vfs.FS, dir string) (staged, unstaged int, err error) {
	out, err := Run(ctx, fsys, dir, "status", "--porcelain=v1", "-z")
	if err != nil {
		return 0, 0, err
	}
	for _, rec := range strings.Split(string(out), "\x00") {
		if len(rec) < 3 {
			continue
		}
		// XY where X is the index and Y the worktree.
		if rec[0] != ' ' && rec[0] != '?' {
			staged++
		}
		if rec[1] != ' ' {
			unstaged++
		}
	}
	return staged, unstaged, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
