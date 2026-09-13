// Package fsx walks the project once in the background and keeps a flat list of
// candidate paths in memory. Everything the UI needs (fuzzy find, grep targets)
// reads from that single slice, so no interactive action ever touches the disk
// to enumerate files.
package fsx

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sahilm/fuzzy"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

// Index is a snapshot of the project's files. It is safe for concurrent use:
// the walker publishes a finished slice under a write lock and readers take a
// read lock only long enough to grab the header.
type Index struct {
	fsys  vfs.FS
	root  string
	limit int

	mu    sync.RWMutex
	files []string // slash-separated, relative to root
	dirs  int

	building atomic.Bool
	done     atomic.Bool
	took     time.Duration
}

func NewIndex(fsys vfs.FS, root string, limit int) *Index {
	if limit <= 0 {
		limit = 200000
	}
	return &Index{fsys: fsys, root: root, limit: limit}
}

// Retarget repoints the index at another filesystem and root, clearing what it
// knew. The next Build repopulates it.
func (ix *Index) Retarget(fsys vfs.FS, root string) {
	ix.mu.Lock()
	ix.fsys, ix.root, ix.files = fsys, root, nil
	ix.mu.Unlock()
	ix.done.Store(false)
}

// FS is the filesystem this index was built from.
func (ix *Index) FS() vfs.FS {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.fsys
}

func (ix *Index) Root() string        { return ix.root }
func (ix *Index) Ready() bool         { return ix.done.Load() }
func (ix *Index) Building() bool      { return ix.building.Load() }
func (ix *Index) Took() time.Duration { return ix.took }

func (ix *Index) Len() int {
	ix.mu.RLock()
	n := len(ix.files)
	ix.mu.RUnlock()
	return n
}

// Files returns the backing slice. Callers must treat it as read-only; it is
// never mutated after publication, which is what makes sharing it safe.
func (ix *Index) Files() []string {
	ix.mu.RLock()
	f := ix.files
	ix.mu.RUnlock()
	return f
}

// Build enumerates the files. The work happens in the filesystem that owns
// them: in-process for the host, inside the container for a remote target.
func (ix *Index) Build() {
	if !ix.building.CompareAndSwap(false, true) {
		return
	}
	defer ix.building.Store(false)

	ix.mu.RLock()
	fsys, root, limit := ix.fsys, ix.root, ix.limit
	ix.mu.RUnlock()
	if fsys == nil {
		return
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	files, err := fsys.ListFiles(ctx, root, limit)
	if err != nil {
		files = nil
	}

	ix.mu.Lock()
	ix.files = files
	ix.mu.Unlock()
	ix.took = time.Since(start)
	ix.done.Store(true)
}

// Hit is one fuzzy-find result.
type Hit struct {
	Path    string
	Indexes []int // byte positions in Path that matched, for highlighting
}

// Find returns at most limit fuzzy matches for q. An empty query returns the
// head of the index, which makes the picker useful before the user types.
func (ix *Index) Find(q string, limit int) []Hit {
	files := ix.Files()
	if limit <= 0 {
		limit = 50
	}
	if strings.TrimSpace(q) == "" {
		n := min(limit, len(files))
		hits := make([]Hit, n)
		for i := 0; i < n; i++ {
			hits[i] = Hit{Path: files[i]}
		}
		return hits
	}

	matches := fuzzy.FindFrom(q, sliceSource(files))
	n := min(limit, len(matches))
	hits := make([]Hit, n)
	for i := 0; i < n; i++ {
		hits[i] = Hit{Path: matches[i].Str, Indexes: matches[i].MatchedIndexes}
	}
	return hits
}

// Abs resolves an indexed relative path back to an absolute one, in the
// namespace of whichever filesystem this index covers.
func (ix *Index) Abs(rel string) string {
	if strings.HasPrefix(rel, "/") {
		return rel
	}
	return vfs.Join(ix.Root(), rel)
}

// Rel is the inverse of Abs, falling back to the input when p is outside root.
func (ix *Index) Rel(p string) string { return vfs.Rel(ix.Root(), p) }

type sliceSource []string

func (s sliceSource) String(i int) string { return s[i] }
func (s sliceSource) Len() int            { return len(s) }

// Stat is a thin helper so callers do not need to import os for a size check.
func Stat(p string) (os.FileInfo, error) { return os.Stat(p) }
