package explorer

import (
	"context"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/ignore"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// ParentRel is the synthetic row that leads out of the current root.
const ParentRel = ".."

// Node is one visible row of the tree.
type Node struct {
	Name  string
	Rel   string // slash-separated, relative to the root ("" is the root itself)
	Dir   bool
	Depth int
	Open  bool
	Size  int64
}

// IsParent reports whether this row is the way up out of the root.
func (n Node) IsParent() bool { return n.Rel == ParentRel }

// Tree is a lazily expanded directory listing.
//
// Only directories the user has opened are ever read, so the cost of staying
// current is proportional to what is on screen rather than to the size of the
// repository. That is what makes a 1-second refresh affordable even on a slow
// bind mount.
type Tree struct {
	mu       sync.RWMutex
	fs       vfs.FS
	root     string
	expanded map[string]bool
	rows     []Node
	prints   map[string]string // directory rel -> fingerprint of its listing
	ig       *ignore.Set
	env      Env

	watch *watcher
}

// New builds a tree over fs, rooted at root, with the root already open.
func New(fs vfs.FS, root string) *Tree {
	t := &Tree{
		fs:       fs,
		expanded: map[string]bool{"": true},
		prints:   map[string]string{},
	}
	t.setRoot(root, true)
	return t
}

// SetFS repoints the tree at a different filesystem, which is what happens when
// a session is aimed at a container instead of the host.
func (t *Tree) SetFS(fs vfs.FS, root string) {
	t.mu.RLock()
	same := t.fs != nil && fs != nil && t.fs.ID() == fs.ID() && t.root == root
	t.mu.RUnlock()
	if same {
		return
	}
	t.Close()
	t.mu.Lock()
	t.fs = fs
	t.mu.Unlock()
	t.setRoot(root, true)
	if fs != nil && fs.IsLocal() {
		t.Watch()
	}
}

// FS is the filesystem this tree reads through.
func (t *Tree) FS() vfs.FS {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.fs
}

// SetRoot repoints the tree, which is what happens when the user switches to a
// session whose agent runs somewhere else.
func (t *Tree) SetRoot(root string) { t.setRoot(root, false) }

func (t *Tree) setRoot(root string, force bool) {
	t.mu.Lock()
	if !force && root == t.root {
		t.mu.Unlock()
		return
	}
	local := t.fs == nil || t.fs.IsLocal()
	t.root = root
	t.expanded = map[string]bool{"": true}
	t.prints = map[string]string{}
	if local {
		t.ig = ignore.Load(root)
		t.env = DetectEnv(root)
	} else {
		// Ignore rules and the environment come from the remote filesystem,
		// which supplies its own; nothing here can read them.
		t.ig = nil
		t.env = Env{Kind: "container", WatchReliable: false,
			Note: "listings are read over docker exec; polling"}
	}
	t.mu.Unlock()

	if t.watch != nil {
		t.watch.reset()
	}
	t.Refresh()
}

func (t *Tree) Root() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.root
}

func (t *Tree) Env() Env {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.env
}

// Rows returns the current flattened view. The slice is replaced wholesale on
// every refresh and never mutated, so callers may hold it without copying.
func (t *Tree) Rows() []Node {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.rows
}

// IsOpen reports whether a directory is expanded.
func (t *Tree) IsOpen(rel string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.expanded[rel]
}

// Toggle opens or closes a directory and rebuilds the view.
func (t *Tree) Toggle(rel string) {
	t.mu.Lock()
	if t.expanded[rel] {
		delete(t.expanded, rel)
		// Forget the children's fingerprints so a reopen re-reads them.
		for k := range t.prints {
			if k == rel || strings.HasPrefix(k, rel+"/") {
				delete(t.prints, k)
			}
		}
	} else {
		t.expanded[rel] = true
	}
	t.mu.Unlock()

	t.Refresh()
	if t.watch != nil {
		t.watch.sync(t.root, t.openDirs())
	}
}

// Reveal opens every directory on the way to rel so the file becomes visible.
func (t *Tree) Reveal(rel string) {
	t.mu.Lock()
	for dir := path.Dir(rel); ; dir = path.Dir(dir) {
		if dir == "." || dir == "/" || dir == "" {
			break
		}
		t.expanded[dir] = true
	}
	t.mu.Unlock()
	t.Refresh()
	if t.watch != nil {
		t.watch.sync(t.root, t.openDirs())
	}
}

func (t *Tree) openDirs() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, 0, len(t.expanded))
	for k := range t.expanded {
		out = append(out, k)
	}
	return out
}

// Refresh re-reads every open directory and rebuilds the view. It reports
// whether anything actually changed, so the UI only repaints when it must.
func (t *Tree) Refresh() bool {
	t.mu.RLock()
	root, ig, fsys := t.root, t.ig, t.fs
	expanded := make(map[string]bool, len(t.expanded))
	for k, v := range t.expanded {
		expanded[k] = v
	}
	oldPrints := t.prints
	t.mu.RUnlock()

	if root == "" || fsys == nil {
		return false
	}

	// Read every open directory in one go. On a container that is a single
	// round trip instead of one per directory.
	abs := make([]string, 0, len(expanded))
	for rel := range expanded {
		abs = append(abs, vfs.Join(root, rel))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	listings := vfs.ReadDirs(ctx, fsys, abs)

	rows := make([]Node, 0, 128)
	// A row that leads out of the root, so the tree is not a dead end once you
	// have narrowed a session down to a subdirectory.
	if parent := parentOf(root); parent != "" {
		rows = append(rows, Node{Name: ParentRel, Rel: ParentRel, Dir: true})
	}
	prints := make(map[string]string, len(expanded))
	changed := false

	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		entries, print := shape(listings[vfs.Join(root, rel)], rel, ig)
		prints[rel] = print
		if oldPrints[rel] != print {
			changed = true
		}
		for _, e := range entries {
			childRel := e.Rel
			node := Node{
				Name: e.Name, Rel: childRel, Dir: e.Dir,
				Depth: depth, Open: e.Dir && expanded[childRel], Size: e.Size,
			}
			rows = append(rows, node)
			if node.Open {
				walk(childRel, depth+1)
			}
		}
	}
	walk("", 0)

	// A directory that was closed since the last pass leaves a stale print.
	if len(prints) != len(oldPrints) {
		changed = true
	}

	t.mu.Lock()
	t.rows = rows
	t.prints = prints
	t.mu.Unlock()
	return changed
}

type entry struct {
	Name string
	Rel  string
	Dir  bool
	Size int64
}

// shape filters, sorts and fingerprints one directory listing. The fingerprint
// lets a refresh decide whether anything changed without diffing the tree.
func shape(des []vfs.DirEntry, rel string, ig *ignore.Set) ([]entry, string) {
	out := make([]entry, 0, len(des))
	var fp strings.Builder
	fp.Grow(len(des) * 24)

	for _, de := range des {
		childRel := de.Name
		if rel != "" {
			childRel = rel + "/" + de.Name
		}
		if ig != nil && ig.Match(childRel, de.Dir) {
			continue
		}
		out = append(out, entry{Name: de.Name, Rel: childRel, Dir: de.Dir, Size: de.Size})

		fp.WriteString(de.Name)
		fp.WriteByte('\x00')
		if de.Dir {
			fp.WriteByte('d')
		} else {
			fp.WriteString(strconv.FormatInt(de.Size, 10))
			fp.WriteByte(':')
			fp.WriteString(strconv.FormatInt(de.ModNano, 10))
		}
		fp.WriteByte('\n')
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, fp.String()
}

// Parent is the directory above the root, or "" when the root is already the
// top of its filesystem.
func (t *Tree) Parent() string { return parentOf(t.Root()) }

func parentOf(root string) string {
	root = strings.TrimSuffix(root, "/")
	if root == "" || root == "/" {
		return ""
	}
	p := vfs.Dir(root)
	if p == root {
		return ""
	}
	return p
}

// PollInterval is how often the tree re-reads its open directories. Filesystem
// events are an optimisation on top; on a bind mount or a WSL drive they never
// arrive, so this interval is the real refresh rate and is tightened there.
func (t *Tree) PollInterval() time.Duration {
	if t.Env().WatchReliable {
		return 900 * time.Millisecond
	}
	if f := t.FS(); f != nil && !f.IsLocal() {
		// Every poll is a round trip into the container, so it is paced to stay
		// responsive without hammering the daemon.
		return 1200 * time.Millisecond
	}
	return 400 * time.Millisecond
}
