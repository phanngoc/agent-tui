package ignore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIgnoreDefaults(t *testing.T) {
	ig := &Set{}
	cases := []struct {
		path  string
		isDir bool
		skip  bool
	}{
		{".git", true, true},
		{"node_modules", true, true},
		{"src/node_modules", true, true},
		{"internal/ui", true, false},
		{"logo.png", false, true},
		{"main.go", false, false},
		{"a/b/thing.dylib", false, true},
	}
	for _, c := range cases {
		if got := ig.Match(c.path, c.isDir); got != c.skip {
			t.Errorf("Match(%q, dir=%v) = %v, want %v", c.path, c.isDir, got, c.skip)
		}
	}
}

func TestIgnoreFromGitignore(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".gitignore"), "*.tmp\n/dist\nbuild/\n!keep.tmp\n# comment\n")

	ig := Load(root)
	cases := []struct {
		path  string
		isDir bool
		skip  bool
	}{
		{"a.tmp", false, true},
		{"nested/b.tmp", false, true},
		{"keep.tmp", false, false},
		{"dist", true, true},
		{"dist/app.js", false, true},
		{"build", true, true},
		{"src/main.go", false, false},
	}
	for _, c := range cases {
		if got := ig.Match(c.path, c.isDir); got != c.skip {
			t.Errorf("Match(%q, dir=%v) = %v, want %v", c.path, c.isDir, got, c.skip)
		}
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
