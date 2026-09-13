// Package vfs is the filesystem the rest of the app reads through.
//
// It exists because the directory an agent works in is not always the one this
// process can see: the terminal may be on the host while the code lives inside
// a container. Everything that touches files — the explorer, the preview, the
// fuzzy index, content search, and the built-in agent's tools — goes through an
// FS, so pointing a session at a container repoints all of them at once.
package vfs

import (
	"context"
	"os/exec"
	"strings"
)

// DirEntry is one child of a directory.
type DirEntry struct {
	Name    string
	Dir     bool
	Size    int64
	ModNano int64
}

// FileInfo is what a stat returns.
type FileInfo struct {
	Size    int64
	Dir     bool
	ModNano int64
}

// GrepHit is one matching line.
type GrepHit struct {
	Path string // relative to the search root, slash-separated
	Line int
	Text string
}

// GrepOptions configures a content search.
type GrepOptions struct {
	Query         string
	Regex         bool
	CaseSensitive bool
	Limit         int
	MaxFileBytes  int64
	Workers       int
	// Files narrows the search. The local backend uses it directly; a remote
	// backend that can walk faster itself may ignore it.
	Files []string
}

// FS is a filesystem the app can browse, read and search.
//
// Paths are absolute in the filesystem's own namespace: on the host they are
// host paths, inside a container they are container paths.
type FS interface {
	// ID is the persisted identity, "host" or "docker:<name>".
	ID() string
	// Label is what the UI shows.
	Label() string
	// IsLocal reports whether plain os calls would reach these paths. Only the
	// local filesystem can be watched for change events or walked in-process.
	IsLocal() bool
	// DefaultDir is where a new session starts.
	DefaultDir() string

	ReadDir(ctx context.Context, dir string) ([]DirEntry, error)
	// ReadFile reads at most max bytes and reports whether it stopped early.
	ReadFile(ctx context.Context, path string, max int64) (data []byte, truncated bool, err error)
	Stat(ctx context.Context, path string) (FileInfo, error)
	// WriteFile replaces a file, creating parent directories as needed.
	WriteFile(ctx context.Context, path string, data []byte) error

	// ListFiles enumerates candidate files under root, relative and
	// slash-separated, already filtered by the project's ignore rules.
	ListFiles(ctx context.Context, root string, limit int) ([]string, error)
	// Grep searches file contents under root.
	Grep(ctx context.Context, root string, o GrepOptions) (hits []GrepHit, truncated bool, err error)

	// Command builds a command that runs where this filesystem lives, so an
	// agent CLI executes next to the files it is being asked to edit.
	Command(ctx context.Context, dir, name string, args ...string) *exec.Cmd

	// Health reports a problem that makes this filesystem unusable right now.
	Health(ctx context.Context) error
}

// MultiDirReader reads several directories in one round trip.
//
// It matters for remote filesystems: a refresh touches every open directory,
// and paying a process spawn per directory would make the tree lag behind the
// user. Backends that have no round trip to save simply do not implement it.
type MultiDirReader interface {
	ReadDirs(ctx context.Context, dirs []string) map[string][]DirEntry
}

// ReadDirs reads many directories, using the batching fast path when the
// filesystem offers one.
func ReadDirs(ctx context.Context, f FS, dirs []string) map[string][]DirEntry {
	if m, ok := f.(MultiDirReader); ok {
		return m.ReadDirs(ctx, dirs)
	}
	out := make(map[string][]DirEntry, len(dirs))
	for _, d := range dirs {
		entries, err := f.ReadDir(ctx, d)
		if err != nil {
			out[d] = nil
			continue
		}
		out[d] = entries
	}
	return out
}

// Join concatenates path elements in the filesystem's own namespace. Container
// paths are always slash-separated regardless of the host's OS, and host paths
// on the platforms this runs on are too.
func Join(base string, parts ...string) string {
	out := strings.TrimSuffix(base, "/")
	for _, p := range parts {
		p = strings.Trim(p, "/")
		if p == "" {
			continue
		}
		out += "/" + p
	}
	if out == "" {
		return "/"
	}
	return out
}

// Rel makes p relative to root, or returns p unchanged when it is outside.
func Rel(root, p string) string {
	root = strings.TrimSuffix(root, "/")
	switch {
	case p == root:
		return ""
	case strings.HasPrefix(p, root+"/"):
		return p[len(root)+1:]
	default:
		return p
	}
}

// Base is the final element of a path.
func Base(p string) string {
	p = strings.TrimSuffix(p, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	if p == "" {
		return "/"
	}
	return p
}

// Dir is everything but the final element.
func Dir(p string) string {
	p = strings.TrimSuffix(p, "/")
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return "/"
}

// Within reports whether p resolves inside root.
//
// A relative path is relative to the root by definition, so it counts as
// inside; anything that climbs out with .. does not.
func Within(root, p string) bool {
	root = strings.TrimSuffix(root, "/")
	if root == "" || p == "" {
		return false
	}
	if !strings.HasPrefix(p, "/") {
		return !strings.Contains(p, "..")
	}
	clean := CleanPath(p)
	return clean == root || strings.HasPrefix(clean, root+"/")
}

// CleanPath resolves . and .. without touching the filesystem, so a symlinked
// directory is compared as written rather than as resolved.
func CleanPath(p string) string {
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, part)
		}
	}
	return "/" + strings.Join(out, "/")
}
