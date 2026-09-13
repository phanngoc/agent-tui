package explorer

import (
	"context"
	"os/exec"
	"sync/atomic"
	"testing"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

// fakeRemote is a filesystem that is not local, so the tree must treat it the
// way it treats a container: batch its reads and never try to watch it.
type fakeRemote struct {
	dirs     map[string][]vfs.DirEntry
	readDirs atomic.Int32 // batched calls
	readDir  atomic.Int32 // one-at-a-time calls
}

func (f *fakeRemote) ID() string                   { return "docker:fake" }
func (f *fakeRemote) Label() string                { return "fake" }
func (f *fakeRemote) IsLocal() bool                { return false }
func (f *fakeRemote) DefaultDir() string           { return "/app" }
func (f *fakeRemote) Health(context.Context) error { return nil }
func (f *fakeRemote) Stat(context.Context, string) (vfs.FileInfo, error) {
	return vfs.FileInfo{}, nil
}
func (f *fakeRemote) ReadFile(context.Context, string, int64) ([]byte, bool, error) {
	return nil, false, nil
}
func (f *fakeRemote) WriteFile(context.Context, string, []byte) error { return nil }
func (f *fakeRemote) ListFiles(context.Context, string, int) ([]string, error) {
	return nil, nil
}
func (f *fakeRemote) Grep(context.Context, string, vfs.GrepOptions) ([]vfs.GrepHit, bool, error) {
	return nil, false, nil
}
func (f *fakeRemote) Command(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

func (f *fakeRemote) ReadDir(_ context.Context, dir string) ([]vfs.DirEntry, error) {
	f.readDir.Add(1)
	return f.dirs[dir], nil
}

func (f *fakeRemote) ReadDirs(_ context.Context, dirs []string) map[string][]vfs.DirEntry {
	f.readDirs.Add(1)
	out := make(map[string][]vfs.DirEntry, len(dirs))
	for _, d := range dirs {
		out[d] = f.dirs[d]
	}
	return out
}

func newFakeRemote() *fakeRemote {
	return &fakeRemote{dirs: map[string][]vfs.DirEntry{
		"/app": {
			{Name: "src", Dir: true},
			{Name: "main.go", Size: 10, ModNano: 1},
		},
		"/app/src": {
			{Name: "app.go", Size: 20, ModNano: 2},
		},
	}}
}

func TestRemoteTreeReadsThroughTheFilesystem(t *testing.T) {
	fs := newFakeRemote()
	tr := New(fs, "/app")

	got := names(tr)
	if len(got) != 2 || got[0] != "src" || got[1] != "main.go" {
		t.Fatalf("rows = %v", got)
	}
	tr.Toggle("src")
	if got := names(tr); len(got) != 3 || got[1] != "src/app.go" {
		t.Fatalf("rows after expanding = %v", got)
	}
}

// TestRemoteTreeBatchesItsReads is the difference between a tree that keeps up
// and one that lags: every open directory must be fetched in one round trip.
func TestRemoteTreeBatchesItsReads(t *testing.T) {
	fs := newFakeRemote()
	tr := New(fs, "/app")
	tr.Toggle("src")

	before := fs.readDirs.Load()
	tr.Refresh()
	if got := fs.readDirs.Load() - before; got != 1 {
		t.Errorf("a refresh with two open directories made %d batched calls, want 1", got)
	}
	if n := fs.readDir.Load(); n != 0 {
		t.Errorf("the tree fell back to %d per-directory reads", n)
	}
}

func TestRemoteTreeIsNotWatched(t *testing.T) {
	// There are no inotify events to receive from inside a container, so the
	// tree must not pretend otherwise.
	tr := New(newFakeRemote(), "/app")
	tr.Watch()
	defer tr.Close()

	if tr.Changes() != nil {
		t.Error("a remote tree should have no change channel")
	}
	if tr.Env().WatchReliable {
		t.Error("a remote tree must not claim reliable events")
	}
	if tr.PollInterval() <= 0 {
		t.Error("a remote tree needs a polling interval")
	}
}

func TestRemoteTreeDetectsChanges(t *testing.T) {
	fs := newFakeRemote()
	tr := New(fs, "/app")
	if tr.Refresh() {
		t.Error("an unchanged remote tree reported a change")
	}

	fs.dirs["/app"] = append(fs.dirs["/app"], vfs.DirEntry{Name: "new.go", Size: 5, ModNano: 3})
	if !tr.Refresh() {
		t.Error("a new remote file was not detected")
	}
	if got := names(tr); len(got) != 3 {
		t.Errorf("rows = %v", got)
	}
}

func TestSwitchingFilesystemsResetsTheTree(t *testing.T) {
	local := t.TempDir()
	write(t, local, "host.go", "")

	tr := newTree(local)
	if got := names(tr); len(got) != 1 || got[0] != "host.go" {
		t.Fatalf("host rows = %v", got)
	}

	tr.SetFS(newFakeRemote(), "/app")
	if tr.FS().IsLocal() {
		t.Error("the tree is still on the local filesystem")
	}
	got := names(tr)
	if len(got) != 2 || got[1] != "main.go" {
		t.Errorf("container rows = %v", got)
	}

	tr.SetFS(vfs.NewLocal(local), local)
	if !tr.FS().IsLocal() {
		t.Error("the tree did not come back to the host")
	}
	if got := names(tr); len(got) != 1 || got[0] != "host.go" {
		t.Errorf("host rows after switching back = %v", got)
	}
}
