package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, files map[string]string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	names := make([]string, 0, len(files))
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return root, names
}

func TestLiteralSearch(t *testing.T) {
	root, files := fixture(t, map[string]string{
		"a.go":    "package a\n\nfunc Hello() {}\nfunc hello2() {}\n",
		"b/c.txt": "nothing here\nhello again\n",
		"bin.dat": "abc\x00hello\n",
	})

	res := Run(context.Background(), root, files, Options{Query: "hello", Workers: 2})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	// Smart case is off (all-lowercase query), so Hello and hello both match,
	// but the NUL-containing file is skipped as binary.
	var got []string
	for _, m := range res.Matches {
		got = append(got, m.Path+":"+itoa(m.Line))
	}
	want := []string{"a.go:3", "a.go:4", "b/c.txt:2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("matches = %v, want %v", got, want)
	}
	if res.Files != 2 {
		t.Errorf("files with hits = %d, want 2", res.Files)
	}
}

func TestSmartCase(t *testing.T) {
	root, files := fixture(t, map[string]string{"a.go": "Hello\nhello\n"})

	lower := Run(context.Background(), root, files, Options{Query: "hello"})
	if len(lower.Matches) != 2 {
		t.Errorf("lowercase query matched %d lines, want 2", len(lower.Matches))
	}
	upper := Run(context.Background(), root, files, Options{Query: "Hello"})
	if len(upper.Matches) != 1 || upper.Matches[0].Line != 1 {
		t.Errorf("mixed-case query matched %v, want only line 1", upper.Matches)
	}
}

func TestRegexSearch(t *testing.T) {
	root, files := fixture(t, map[string]string{"a.go": "func One()\nvar two int\nfunc Three()\n"})
	res := Run(context.Background(), root, files, Options{Query: `func \w+\(`, Regex: true})
	if len(res.Matches) != 2 {
		t.Fatalf("matches = %d, want 2: %v", len(res.Matches), res.Matches)
	}
	if res.Matches[0].Line != 1 || res.Matches[1].Line != 3 {
		t.Errorf("lines = %d,%d want 1,3", res.Matches[0].Line, res.Matches[1].Line)
	}
}

func TestBadRegexReported(t *testing.T) {
	root, files := fixture(t, map[string]string{"a.go": "x"})
	if res := Run(context.Background(), root, files, Options{Query: "(", Regex: true}); res.Err == nil {
		t.Error("expected an error for an invalid pattern")
	}
}

func TestColumnsAccountForTabs(t *testing.T) {
	root, files := fixture(t, map[string]string{"a.go": "\t\tneedle\n"})
	res := Run(context.Background(), root, files, Options{Query: "needle"})
	if len(res.Matches) != 1 {
		t.Fatalf("want one match, got %d", len(res.Matches))
	}
	m := res.Matches[0]
	if m.Text != "        needle" {
		t.Errorf("text = %q, want tabs expanded", m.Text)
	}
	if m.Start != 8 {
		t.Errorf("start = %d, want 8", m.Start)
	}
}

func TestLimitTruncates(t *testing.T) {
	body := strings.Repeat("needle\n", 50)
	root, files := fixture(t, map[string]string{"a.go": body})
	res := Run(context.Background(), root, files, Options{Query: "needle", Limit: 10})
	if len(res.Matches) > 10 {
		t.Errorf("got %d matches, limit was 10", len(res.Matches))
	}
	if !res.Truncated {
		t.Error("expected Truncated to be set")
	}
}

func TestCancelledContextReturnsEarly(t *testing.T) {
	root, files := fixture(t, map[string]string{"a.go": "needle\n"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if res := Run(ctx, root, files, Options{Query: "needle"}); res.Err != nil {
		t.Errorf("a cancelled scan should return quietly, got %v", res.Err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
