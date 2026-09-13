package preview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/vfs"

	"github.com/phanngoc/agent-tui/internal/highlight"
)

func newLoader() *Loader {
	c := lipgloss.Color
	sc := highlight.NewScheme(c("#dbe2ee"), c("#d3a3f0"), c("#8fe3d3"),
		c("#cbeda0"), c("#fb9d80"), c("#8593a6"), c("#93b6ff"), c("#aabbd4"))
	return NewLoader(sc, 1, 4) // 1 KB limit, so truncation is easy to exercise
}

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStyledAndPlainStayAligned(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "a.go", "package a\n\nfunc F() {\n\treturn\n}\n")

	f := newLoader().Load(vfs.NewLocal(dir), p, "a.go")
	if f.Err != nil {
		t.Fatal(f.Err)
	}
	if len(f.Styled) != len(f.Plain) {
		t.Fatalf("styled has %d lines, plain has %d", len(f.Styled), len(f.Plain))
	}
	// In-file search maps offsets in Plain onto the viewport's highlight
	// ranges, so the two must agree character-for-character.
	for i := range f.Styled {
		if got, want := stripSGR(f.Styled[i]), f.Plain[i]; got != want {
			t.Errorf("line %d: styled strips to %q, plain is %q", i, got, want)
		}
	}
	if f.Lang != "Go" {
		t.Errorf("lang = %q", f.Lang)
	}
}

func TestBinaryFilesBecomeHexDumps(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "blob.bin", "abc\x00\x01\x02def")

	f := newLoader().Load(vfs.NewLocal(dir), p, "blob.bin")
	if !f.Binary {
		t.Fatal("expected the file to be detected as binary")
	}
	if len(f.Styled) == 0 || !strings.Contains(f.Styled[0], "00000000") {
		t.Errorf("hex dump missing its offset column: %v", f.Styled)
	}
}

func TestTruncationStopsOnALineBoundary(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("some fairly long line of text here\n", 200) // ~6.8 KB
	p := write(t, dir, "big.txt", body)

	f := newLoader().Load(vfs.NewLocal(dir), p, "big.txt")
	if !f.Truncated {
		t.Fatal("expected Truncated for a file over the limit")
	}
	if last := f.Plain[len(f.Plain)-1]; last != "" && !strings.HasSuffix(last, "here") {
		t.Errorf("truncation cut mid-line: %q", last)
	}
}

func TestCacheReturnsTheSameObject(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "a.go", "package a\n")
	l := newLoader()

	first := l.Load(vfs.NewLocal(dir), p, "a.go")
	if second := l.Load(vfs.NewLocal(dir), p, "a.go"); first != second {
		t.Error("a repeat load should hit the cache and return the same file")
	}

	// Rewriting the file changes size and mtime, which must bust the entry.
	write(t, dir, "a.go", "package a\n\nvar changed = true\n")
	if third := l.Load(vfs.NewLocal(dir), p, "a.go"); third == first {
		t.Error("the cache did not notice the file changed")
	} else if len(third.Plain) <= len(first.Plain) {
		t.Errorf("reloaded content looks stale: %v", third.Plain)
	}
}

func TestMissingFileReportsAnError(t *testing.T) {
	dir := t.TempDir()
	f := newLoader().Load(vfs.NewLocal(dir), filepath.Join(dir, "nope.go"), "nope.go")
	if f.Err == nil {
		t.Error("expected an error for a missing file")
	}
	if f.LineCount() != 0 {
		t.Error("a failed load should have no lines")
	}
}

func TestDirectoryIsRejected(t *testing.T) {
	dir := t.TempDir()
	if f := newLoader().Load(vfs.NewLocal(dir), dir, "."); f.Err == nil {
		t.Error("expected an error when loading a directory")
	}
}

func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
