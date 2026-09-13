package explorer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newTree builds a tree over the local filesystem.
func newTree(root string) *Tree { return New(vfs.NewLocal(root), root) }

// names lists the visible rows, dropping the synthetic ".." row so the
// existing expectations stay about real entries.
func names(t *Tree) []string {
	rows := t.Rows()
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.IsParent() {
			continue
		}
		out = append(out, r.Rel)
	}
	return out
}

func TestTreeIsLazyAndOrdered(t *testing.T) {
	root := t.TempDir()
	write(t, root, "zebra.go", "")
	write(t, root, "Alpha.go", "")
	write(t, root, "src/deep/file.go", "")
	write(t, root, "node_modules/junk.js", "")

	tr := newTree(root)
	got := names(tr)
	// Directories first, case-insensitive by name, ignored dirs dropped, and
	// nothing below an unopened directory.
	want := []string{"src", "Alpha.go", "zebra.go"}
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rows = %v, want %v", got, want)
		}
	}
}

func TestToggleExpandsAndCollapses(t *testing.T) {
	root := t.TempDir()
	write(t, root, "src/deep/file.go", "")

	tr := newTree(root)
	tr.Toggle("src")
	if got := names(tr); len(got) != 2 || got[1] != "src/deep" {
		t.Fatalf("after expanding src: %v", got)
	}
	tr.Toggle("src/deep")
	if got := names(tr); len(got) != 3 || got[2] != "src/deep/file.go" {
		t.Fatalf("after expanding src/deep: %v", got)
	}
	tr.Toggle("src")
	if got := names(tr); len(got) != 1 {
		t.Fatalf("after collapsing src: %v", got)
	}
}

func TestRevealOpensAncestors(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/b/c/file.go", "")

	tr := newTree(root)
	tr.Reveal("a/b/c/file.go")
	var found bool
	for _, r := range tr.Rows() {
		if r.Rel == "a/b/c/file.go" {
			found = true
			if r.Depth != 3 {
				t.Errorf("depth = %d, want 3", r.Depth)
			}
		}
	}
	if !found {
		t.Errorf("reveal did not make the file visible: %v", names(tr))
	}
}

// TestRefreshDetectsChanges is the guarantee the Docker and WSL cases rest on:
// correctness comes from re-reading, not from filesystem events.
func TestRefreshDetectsChanges(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "one")

	tr := newTree(root)
	if tr.Refresh() {
		t.Error("an unchanged tree reported a change")
	}

	write(t, root, "b.go", "two")
	if !tr.Refresh() {
		t.Fatal("a new file was not detected")
	}
	if len(names(tr)) != 2 {
		t.Errorf("rows = %v", names(tr))
	}

	// Content changes matter too: the preview pane keys off size and mtime.
	time.Sleep(10 * time.Millisecond)
	write(t, root, "a.go", "one but longer")
	if !tr.Refresh() {
		t.Error("an edited file was not detected")
	}

	if err := os.Remove(filepath.Join(root, "b.go")); err != nil {
		t.Fatal(err)
	}
	if !tr.Refresh() {
		t.Error("a deleted file was not detected")
	}
	if len(names(tr)) != 1 {
		t.Errorf("rows after delete = %v", names(tr))
	}
}

func TestChangesInsideClosedDirsAreIgnored(t *testing.T) {
	// Only open directories are read, so churn deep in the tree costs nothing.
	root := t.TempDir()
	write(t, root, "src/a.go", "")

	tr := newTree(root)
	tr.Refresh()
	write(t, root, "src/b.go", "")
	if tr.Refresh() {
		t.Error("a change inside a collapsed directory forced a repaint")
	}

	tr.Toggle("src")
	if got := len(names(tr)); got != 3 {
		t.Errorf("rows after opening src = %v", names(tr))
	}
}

func TestSetRootResetsState(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, a, "in-a.go", "")
	write(t, b, "in-b.go", "")

	tr := newTree(a)
	tr.Toggle("")
	tr.SetRoot(b)

	if tr.Root() != b {
		t.Errorf("root = %q, want %q", tr.Root(), b)
	}
	got := names(tr)
	if len(got) != 1 || got[0] != "in-b.go" {
		t.Errorf("rows after repointing = %v", got)
	}
}

func TestPollIntervalTightensWhenEventsAreUnreliable(t *testing.T) {
	tr := newTree(t.TempDir())
	reliable := tr.PollInterval()

	tr.mu.Lock()
	tr.env = Env{Kind: "docker", WatchReliable: false}
	tr.mu.Unlock()

	if tr.PollInterval() >= reliable {
		t.Errorf("poll interval %v should be shorter than %v when events do not arrive",
			tr.PollInterval(), reliable)
	}
}

func TestWatcherIsOptional(t *testing.T) {
	// Everything must work with no watcher at all, which is the WSL and Docker
	// reality even when fsnotify reports success.
	tr := newTree(t.TempDir())
	if tr.Changes() != nil {
		t.Error("a tree that was never watched should have no change channel")
	}
	tr.Close() // must not panic
	write(t, tr.Root(), "new.go", "")
	if !tr.Refresh() {
		t.Error("polling must work without a watcher")
	}
}

func TestWatcherFiresOnChange(t *testing.T) {
	root := t.TempDir()
	tr := newTree(root)
	tr.Watch()
	defer tr.Close()

	if tr.Changes() == nil {
		t.Skip("no watcher available on this platform")
	}
	write(t, root, "new.go", "hi")

	select {
	case <-tr.Changes():
	case <-time.After(3 * time.Second):
		// Not a failure: this is exactly the bind-mount case the poll covers.
		t.Log("no filesystem event arrived; the poll is what keeps this tree current")
	}
	if !tr.Refresh() {
		t.Error("the change was not visible even by polling")
	}
}

// TestParentRowLeadsOutOfTheRoot covers the gap that made a narrowed session a
// dead end: once the root was a subdirectory there was no way back up.
func TestParentRowLeadsOutOfTheRoot(t *testing.T) {
	root := t.TempDir()
	write(t, root, "sub/inner.go", "")

	tr := newTree(filepath.Join(root, "sub"))
	rows := tr.Rows()
	if len(rows) == 0 || !rows[0].IsParent() {
		t.Fatalf("the first row should lead up, got %+v", rows)
	}
	if !rows[0].Dir {
		t.Error("the parent row should behave as a directory")
	}
	if got := tr.Parent(); got != root {
		t.Errorf("Parent() = %q, want %q", got, root)
	}

	tr.SetRoot(tr.Parent())
	if tr.Root() != root {
		t.Fatalf("root = %q, want %q", tr.Root(), root)
	}
	if got := names(tr); len(got) != 1 || got[0] != "sub" {
		t.Errorf("rows after going up = %v", got)
	}
}

func TestFilesystemRootHasNoParentRow(t *testing.T) {
	tr := newTree("/")
	for _, r := range tr.Rows() {
		if r.IsParent() {
			t.Fatal("the filesystem root should not offer a way up")
		}
	}
	if got := tr.Parent(); got != "" {
		t.Errorf("Parent() at / = %q, want empty", got)
	}
}
