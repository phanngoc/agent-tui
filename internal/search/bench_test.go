package search_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/fsx"
	"github.com/phanngoc/agent-tui/internal/search"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// corpus builds a ~3 MB tree of Go-shaped files once per benchmark binary, so
// the numbers below are comparable between runs and across machines.
func corpus(tb testing.TB) (string, []string) {
	tb.Helper()
	root := tb.TempDir()

	var body strings.Builder
	for j := 0; j < 60; j++ {
		fmt.Fprintf(&body, "func Handler%d(w int) error {\n\t// comment about %d\n"+
			"\tif w == %d {\n\t\treturn fmt.Errorf(\"needle %%d\", w)\n\t}\n\treturn nil\n}\n\n", j, j, j)
	}
	text := body.String()

	for i := 0; i < 400; i++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg%d", i%20))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		src := fmt.Sprintf("package p%d\n\nimport \"fmt\"\n\n%s", i%20, text)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%d.go", i)), []byte(src), 0o644); err != nil {
			tb.Fatal(err)
		}
	}

	ix := fsx.NewIndex(vfs.NewLocal(root), root, 0)
	ix.Build()
	return root, ix.Files()
}

func BenchmarkIndexBuild(b *testing.B) {
	root, _ := corpus(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix := fsx.NewIndex(vfs.NewLocal(root), root, 0)
		ix.Build()
		if ix.Len() != 400 {
			b.Fatalf("indexed %d files", ix.Len())
		}
	}
}

func BenchmarkFuzzyFind(b *testing.B) {
	root, _ := corpus(b)
	ix := fsx.NewIndex(vfs.NewLocal(root), root, 0)
	ix.Build()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if len(ix.Find("pkg7file", 50)) == 0 {
			b.Fatal("no hits")
		}
	}
}

func BenchmarkGrepLiteral(b *testing.B) {
	root, files := corpus(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := search.Run(context.Background(), root, files,
			search.Options{Query: "Errorf", Limit: 1 << 20})
		if len(r.Matches) == 0 {
			b.Fatal("no matches")
		}
	}
}

func BenchmarkGrepCaseInsensitive(b *testing.B) {
	root, files := corpus(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		search.Run(context.Background(), root, files,
			search.Options{Query: "errorf", Limit: 1 << 20})
	}
}

func BenchmarkGrepRegex(b *testing.B) {
	root, files := corpus(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		search.Run(context.Background(), root, files,
			search.Options{Query: `func Handler\d+\(`, Regex: true, Limit: 1 << 20})
	}
}
