package fsx

import (
	"os"
	"path/filepath"
	"testing"

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
