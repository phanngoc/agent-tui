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
	// recent are files added since they were made, newest first. An empty
	// query lists them ahead of the rest: the file the agent just wrote is
	// the one you are most likely opening the finder to find.
	recent []string
	// gen counts retargets, so a walk that started before one does not
	// publish the old root's files under the new root's name.
	gen uint64

	building atomic.Bool
	// again asks the walk in progress to go round once more: something
	// changed after it started, and what it publishes may predate it.
	again atomic.Bool
	stale atomic.Bool
	done  atomic.Bool
	took  time.Duration
	at    time.Time
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
	ix.fsys, ix.root, ix.files, ix.recent = fsys, root, nil, nil
	ix.gen++
	ix.mu.Unlock()
	ix.done.Store(false)
	ix.again.Store(true) // a walk in progress is of the old root
}

// FS is the filesystem this index was built from.
func (ix *Index) FS() vfs.FS {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.fsys
}

func (ix *Index) Root() string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.root
}
func (ix *Index) Ready() bool         { return ix.done.Load() }
func (ix *Index) Building() bool      { return ix.building.Load() }
func (ix *Index) Took() time.Duration { return ix.took }

// MarkStale says files may have appeared or gone since the last walk. It costs
// a store: the walk itself waits until something reads the index (Fresh).
func (ix *Index) MarkStale() { ix.stale.Store(true) }

// Stale reports whether a change has been reported since the last walk began.
func (ix *Index) Stale() bool { return ix.stale.Load() }

// Fresh reports whether the index can be trusted as it is: walked, nothing
// said to have changed since, and walked within maxAge — the bound on what a
// change nobody reported can have been missing for.
func (ix *Index) Fresh(maxAge time.Duration) bool {
	if !ix.done.Load() || ix.stale.Load() {
		return false
	}
	ix.mu.RLock()
	at := ix.at
	ix.mu.RUnlock()
	return time.Since(at) < maxAge
}

// Add puts files into the index without walking for them: a file the agent
// has just written is known by name, and waiting for the next walk to find it
// is the whole of the delay it would otherwise see. Paths are relative to the
// root; ones already present only move to the front of the recent list.
//
// The published slice is never written to — readers hold it without a lock —
// so an addition copies it. That is a copy of string headers, a few hundred
// microseconds at the index's limit, for an event that happens once per file
// written.
func (ix *Index) Add(rels ...string) {
	if len(rels) == 0 {
		return
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	have := make(map[string]bool, len(rels))
	for _, f := range ix.files {
		for _, r := range rels {
			if f == r {
				have[r] = true
			}
		}
	}
	var fresh []string
	for _, r := range rels {
		if r != "" && !have[r] {
			have[r] = true
			fresh = append(fresh, r)
		}
	}
	if len(fresh) > 0 {
		files := make([]string, 0, len(ix.files)+len(fresh))
		files = append(append(files, fresh...), ix.files...)
		ix.files = files
	}
	for _, r := range rels {
		ix.recent = addRecent(ix.recent, r)
	}
}

const maxRecent = 32

// addRecent moves r to the front of a newest-first list, copying it for the
// same reason Add copies the files.
func addRecent(list []string, r string) []string {
	out := make([]string, 0, min(len(list)+1, maxRecent))
	out = append(out, r)
	for _, x := range list {
		if x != r && len(out) < maxRecent {
			out = append(out, x)
		}
	}
	return out
}

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
//
// It reports whether this call did the walk. One already running is not
// joined or duplicated: it is asked to go round once more, which covers
// whatever prompted this call, and this one returns at once.
func (ix *Index) Build() bool {
	if !ix.building.CompareAndSwap(false, true) {
		ix.again.Store(true)
		return false
	}
	defer ix.building.Store(false)
	for {
		ix.again.Store(false)
		ix.walk()
		if !ix.again.Load() {
			return true
		}
	}
}

func (ix *Index) walk() {
	ix.mu.RLock()
	fsys, root, limit, gen := ix.fsys, ix.root, ix.limit, ix.gen
	ix.mu.RUnlock()
	if fsys == nil {
		return
	}
	// Cleared before the walk rather than after: a change reported while it
	// runs may or may not be in what it finds, and has to be looked for again.
	ix.stale.Store(false)

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	files, err := fsys.ListFiles(ctx, root, limit)
	if err != nil {
		files = nil
	}

	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.gen != gen {
		return // retargeted while walking; again is set, and the loop goes round
	}
	ix.files = files
	// A recent file the walk did not find has gone — or is one the walk
	// leaves out — and either way is not offered.
	if len(ix.recent) > 0 {
		found := make(map[string]bool, len(ix.recent))
		for _, f := range files {
			for _, r := range ix.recent {
				if f == r {
					found[r] = true
				}
			}
		}
		kept := make([]string, 0, len(ix.recent))
		for _, r := range ix.recent {
			if found[r] {
				kept = append(kept, r)
			}
		}
		ix.recent = kept
	}
	ix.took = time.Since(start)
	ix.at = time.Now()
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
	ix.mu.RLock()
	files, recent := ix.files, ix.recent
	ix.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	if strings.TrimSpace(q) == "" {
		hits := make([]Hit, 0, min(limit, len(files)))
		seen := make(map[string]bool, len(recent))
		for _, r := range recent {
			if len(hits) == limit {
				return hits
			}
			seen[r] = true
			hits = append(hits, Hit{Path: r})
		}
		for _, f := range files {
			if len(hits) == limit {
				break
			}
			if !seen[f] {
				hits = append(hits, Hit{Path: f})
			}
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
