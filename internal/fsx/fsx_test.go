package fsx

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

func TestIndexBuildAndFind(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "main.go"), "package main")
	write(t, filepath.Join(root, "internal", "search", "search.go"), "package search")
	write(t, filepath.Join(root, "node_modules", "junk.js"), "nope")
	write(t, filepath.Join(root, "img.png"), "nope")

	ix := NewIndex(vfs.NewLocal(root), root, 0)
	ix.Build()

	if !ix.Ready() {
		t.Fatal("index should be ready after Build")
	}
	if got := ix.Len(); got != 2 {
		t.Fatalf("indexed %d files, want 2: %v", got, ix.Files())
	}

	hits := ix.Find("intsearch", 10)
	if len(hits) == 0 || hits[0].Path != "internal/search/search.go" {
		t.Errorf("fuzzy find returned %v", hits)
	}
	if len(hits[0].Indexes) == 0 {
		t.Error("expected matched-character indexes for highlighting")
	}

	// An empty query shows the head of the index rather than nothing.
	if all := ix.Find("", 10); len(all) != 2 {
		t.Errorf("empty query returned %d results, want 2", len(all))
	}
}

func TestIndexShortestPathsFirst(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "z.go"), "")
	write(t, filepath.Join(root, "a", "b", "c.go"), "")

	ix := NewIndex(vfs.NewLocal(root), root, 0)
	ix.Build()
	if files := ix.Files(); files[0] != "z.go" {
		t.Errorf("top-level file should sort first, got %v", files)
	}
}

func TestIndexRejectsEscapingPaths(t *testing.T) {
	root := t.TempDir()
	ix := NewIndex(vfs.NewLocal(root), root, 0)
	if got := ix.Rel("/somewhere/else/x.go"); got != "/somewhere/else/x.go" {
		t.Errorf("Rel of an outside path = %q, want it returned unchanged", got)
	}
	if got := ix.Abs("a/b.go"); got != filepath.Join(root, "a", "b.go") {
		t.Errorf("Abs = %q", got)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A file written after the walk is findable as soon as it is added by name,
// and an empty query lists it first.
func TestAddedFilesAreFoundWithoutAWalk(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "main.go"), "")
	ix := NewIndex(vfs.NewLocal(root), root, 0)
	ix.Build()

	ix.Add("docs/plan-push.md")
	if hits := ix.Find("planpush", 10); len(hits) != 1 || hits[0].Path != "docs/plan-push.md" {
		t.Errorf("an added file is not found: %v", hits)
	}
	if hits := ix.Find("", 10); len(hits) == 0 || hits[0].Path != "docs/plan-push.md" {
		t.Errorf("an empty query does not list the new file first: %v", hits)
	}
	ix.Add("docs/plan-push.md", "main.go")
	if n := ix.Len(); n != 2 {
		t.Errorf("adding known files duplicated them: %d files %v", n, ix.Files())
	}
}

// The next walk keeps an added file that is on disk and forgets one that is
// not.
func TestAWalkForgetsAddedFilesThatAreGone(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "kept.md"), "")
	ix := NewIndex(vfs.NewLocal(root), root, 0)
	ix.Build()
	ix.Add("kept.md", "gone.md")

	ix.Build()
	if hits := ix.Find("gone", 10); len(hits) != 0 {
		t.Errorf("a file not on disk survived the walk: %v", hits)
	}
	if hits := ix.Find("", 10); len(hits) == 0 || hits[0].Path != "kept.md" {
		t.Errorf("the kept file lost its place: %v", hits)
	}
}

// Fresh is what decides whether opening the finder walks: not after a walk,
// yes once a change is reported, and yes once the index is old.
func TestFreshness(t *testing.T) {
	root := t.TempDir()
	ix := NewIndex(vfs.NewLocal(root), root, 0)
	if ix.Fresh(time.Hour) {
		t.Error("an index never walked is fresh")
	}
	ix.Build()
	if !ix.Fresh(time.Hour) {
		t.Error("a just-walked index is not fresh")
	}
	if ix.Fresh(0) {
		t.Error("an index older than the bound is fresh")
	}
	ix.MarkStale()
	if ix.Fresh(time.Hour) {
		t.Error("an index marked stale is fresh")
	}
	ix.Build()
	if !ix.Fresh(time.Hour) || ix.Stale() {
		t.Error("walking did not clear the stale mark")
	}
}
