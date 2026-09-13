package complete

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

func fixture(t *testing.T) (vfs.FS, string) {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{
		"internal/ui/view.go", "internal/ui/update.go", "internal/engine/api.go",
		"cmd/main.go", "README.md", "Makefile", ".hidden/secret.txt", "my docs/notes.md",
	} {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return vfs.NewLocal(root), root
}

func inserts(r Result) []string {
	out := make([]string, len(r.Candidates))
	for i, c := range r.Candidates {
		out[i] = c.Insert
	}
	return out
}

func TestTokenAt(t *testing.T) {
	for _, tc := range []struct {
		line   string
		cursor int
		want   Token
	}{
		{"cd int", 6, Token{Text: "int", Start: 3, End: 6}},
		{"cd ", 3, Token{Text: "", Start: 3, End: 3}},
		{"", 0, Token{Text: "", Start: 0, End: 0}},
		{"explain internal/ui/vi", 22, Token{Text: "internal/ui/vi", Start: 8, End: 22}},
		{"cd 'my do", 9, Token{Text: "my do", Start: 4, End: 9, Quote: '\''}},
		// The cursor mid-line completes the word it is inside, not the last one.
		{"cd int rest", 6, Token{Text: "int", Start: 3, End: 6}},
	} {
		if got := TokenAt(tc.line, tc.cursor); got != tc.want {
			t.Errorf("TokenAt(%q, %d) = %+v, want %+v", tc.line, tc.cursor, got, tc.want)
		}
	}
}

func TestKindFor(t *testing.T) {
	for line, want := range map[string]Kind{
		"cd in":          DirsOnly,
		"  cd ":          DirsOnly,
		"cd":             DirsOnly,
		"cdr something":  Anything,
		"explain foo.go": Anything,
		"":               Anything,
	} {
		if got := KindFor(line); got != want {
			t.Errorf("KindFor(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestCompletesDirectoriesForCD(t *testing.T) {
	fsys, root := fixture(t)
	r := Paths(context.Background(), fsys, root, "", TokenAt("cd ", 3), DirsOnly)

	got := inserts(r)
	want := []string{"cmd/", "internal/", "my docs/"}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want only directories %v", got, want)
	}
	// A path with a space has to come back usable.
	if got[2] != "'my docs/'" && got[2] != "my docs/" {
		t.Errorf("a directory with a space was not quoted: %q", got[2])
	}
}

func TestCompletesFilesAndDirectories(t *testing.T) {
	fsys, root := fixture(t)
	r := Paths(context.Background(), fsys, root, "", TokenAt("explain ", 8), Anything)

	var sawFile, sawDir bool
	for _, c := range r.Candidates {
		if c.Dir {
			sawDir = true
		} else {
			sawFile = true
		}
	}
	if !sawFile || !sawDir {
		t.Errorf("expected both files and directories: %v", inserts(r))
	}
	// Directories sort first, the way a shell lists them.
	if !r.Candidates[0].Dir {
		t.Errorf("directories should come first: %v", inserts(r))
	}
}

func TestExtendsToTheCommonPrefix(t *testing.T) {
	fsys, root := fixture(t)
	// internal/ui/view.go and internal/ui/update.go share "internal/u".
	r := Paths(context.Background(), fsys, root, "", TokenAt("cd internal/u", 13), DirsOnly)
	if len(r.Candidates) != 1 || r.Candidates[0].Insert != "internal/ui/" {
		t.Fatalf("candidates = %v", inserts(r))
	}
	if !r.Unambiguous() {
		t.Error("a single candidate should be unambiguous")
	}

	r = Paths(context.Background(), fsys, root, "", TokenAt("read internal/ui/", 17), Anything)
	if r.Common != "internal/ui/" {
		t.Errorf("common prefix = %q, want %q", r.Common, "internal/ui/")
	}
	if r.Extends() {
		t.Error("the common prefix adds nothing here, so Tab should open the menu instead")
	}

	r = Paths(context.Background(), fsys, root, "", TokenAt("read internal/ui/u", 18), Anything)
	if !r.Extends() || r.Common != "internal/ui/update.go" {
		t.Errorf("common = %q, extends = %v", r.Common, r.Extends())
	}
}

func TestHiddenFilesNeedAsking(t *testing.T) {
	fsys, root := fixture(t)

	plain := Paths(context.Background(), fsys, root, "", TokenAt("cd ", 3), DirsOnly)
	for _, c := range plain.Candidates {
		if c.Display == ".hidden/" {
			t.Fatalf("a dotfile was offered unasked: %v", inserts(plain))
		}
	}

	dotted := Paths(context.Background(), fsys, root, "", TokenAt("cd .", 4), DirsOnly)
	if len(dotted.Candidates) == 0 {
		t.Error("typing a dot should offer dotfiles")
	}
}

func TestTildeExpands(t *testing.T) {
	fsys, root := fixture(t)
	r := Paths(context.Background(), fsys, root, root, TokenAt("cd ~/int", 8), DirsOnly)
	if len(r.Candidates) != 1 {
		t.Fatalf("candidates = %v", inserts(r))
	}
}

func TestApply(t *testing.T) {
	line := "cd int rest"
	tok := TokenAt(line, 6)
	got, cursor := Apply(line, tok, "internal/")
	if got != "cd internal/ rest" {
		t.Errorf("line = %q", got)
	}
	if cursor != 12 {
		t.Errorf("cursor = %d, want 12", cursor)
	}
}

func TestMissingDirectoryIsNotAnError(t *testing.T) {
	fsys, root := fixture(t)
	r := Paths(context.Background(), fsys, root, "", TokenAt("cd nope/x", 9), DirsOnly)
	if len(r.Candidates) != 0 {
		t.Errorf("candidates = %v", inserts(r))
	}
}

func TestCompletesInsideAnyFilesystem(t *testing.T) {
	// The point of going through vfs: completing in a container has to list the
	// container's files, not the host's.
	fsys, root := fixture(t)
	remote := remoteWrapper{FS: fsys}
	r := Paths(context.Background(), remote, root, "", TokenAt("cd ", 3), DirsOnly)
	if len(r.Candidates) == 0 {
		t.Error("completion did not read through the filesystem it was given")
	}
}

type remoteWrapper struct{ vfs.FS }

func (remoteWrapper) IsLocal() bool { return false }
func (remoteWrapper) ID() string    { return "docker:test" }
