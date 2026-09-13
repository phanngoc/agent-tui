package vfs

import (
	"context"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/charlievieth/fastwalk"

	"github.com/phanngoc/agent-tui/internal/ignore"
	"github.com/phanngoc/agent-tui/internal/search"
)

// Local is the filesystem this process is running on.
type Local struct{ root string }

// NewLocal returns the host filesystem, defaulting new sessions to root.
func NewLocal(root string) *Local { return &Local{root: root} }

func (l *Local) ID() string                   { return "host" }
func (l *Local) Label() string                { return "host" }
func (l *Local) IsLocal() bool                { return true }
func (l *Local) DefaultDir() string           { return l.root }
func (l *Local) Health(context.Context) error { return nil }

func (l *Local) ReadDir(_ context.Context, dir string) ([]DirEntry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]DirEntry, 0, len(des))
	for _, de := range des {
		isDir := de.IsDir()
		if !isDir && !de.Type().IsRegular() {
			continue
		}
		e := DirEntry{Name: de.Name(), Dir: isDir}
		if info, ierr := de.Info(); ierr == nil {
			e.Size, e.ModNano = info.Size(), info.ModTime().UnixNano()
		}
		out = append(out, e)
	}
	return out, nil
}

func (l *Local) ReadFile(_ context.Context, path string, max int64) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	if max <= 0 {
		b, rerr := io.ReadAll(f)
		return b, false, rerr
	}
	// Read one byte past the cap so truncation can be detected without a stat.
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(b)) > max {
		return b[:max], true, nil
	}
	return b, false, nil
}

func (l *Local) WriteFile(_ context.Context, path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	return os.WriteFile(path, data, mode)
}

func (l *Local) Stat(_ context.Context, path string) (FileInfo, error) {
	st, err := os.Stat(path)
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Size: st.Size(), Dir: st.IsDir(), ModNano: st.ModTime().UnixNano()}, nil
}

// ListFiles walks the tree in-process, which is as fast as this gets.
func (l *Local) ListFiles(ctx context.Context, root string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 200000
	}
	ig := ignore.Load(root)

	var (
		mu    sync.Mutex
		out   = make([]string, 0, 4096)
		count int64
	)
	conf := fastwalk.Config{NumWorkers: runtime.NumCPU(), Sort: fastwalk.SortNone}

	err := fastwalk.Walk(&conf, root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if ig.Match(rel, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || ig.Match(rel, false) {
			return nil
		}
		if atomic.AddInt64(&count, 1) > int64(limit) {
			return filepath.SkipAll
		}
		mu.Lock()
		out = append(out, rel)
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortShortestFirst(out)
	return out, nil
}

// Grep runs the in-process parallel scanner.
func (l *Local) Grep(ctx context.Context, root string, o GrepOptions) ([]GrepHit, bool, error) {
	res := search.Run(ctx, root, o.Files, search.Options{
		Query: o.Query, Regex: o.Regex, CaseSensitive: o.CaseSensitive,
		Limit: o.Limit, MaxFileBytes: o.MaxFileBytes, Workers: o.Workers,
	})
	if res.Err != nil {
		return nil, false, res.Err
	}
	hits := make([]GrepHit, len(res.Matches))
	for i, m := range res.Matches {
		hits[i] = GrepHit{Path: m.Path, Line: m.Line, Text: m.Text}
	}
	return hits, res.Truncated, nil
}

func (l *Local) Command(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd
}

// sortShortestFirst puts top-level files ahead of deeply nested ones, which is
// a good tiebreaker when fuzzy scores are equal.
func sortShortestFirst(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		di, dj := strings.Count(paths[i], "/"), strings.Count(paths[j], "/")
		if di != dj {
			return di < dj
		}
		return paths[i] < paths[j]
	})
}
