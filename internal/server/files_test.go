package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/phanngoc/agent-tui/internal/config"
)

// The explorer lists and reads a project's files, never outside it, and finds
// the file a path in a conversation means.
func TestProjectExplorer(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "output"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "output", "balance-missing-sequence.md"), []byte("# Sequence\n```mermaid\nsequenceDiagram\n```\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "logo.bin"), []byte{0, 1, 2, 3}, 0o644)
	srv := New(config.Default(), "test", "")
	ts := httptest.NewServer(srv.guard(srv.mux))
	defer ts.Close()
	get := func(path string, q url.Values, out any) int {
		q.Set("root", root)
		r, err := http.Get(ts.URL + path + "?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if out != nil {
			_ = json.NewDecoder(r.Body).Decode(out)
		}
		return r.StatusCode
	}

	var list struct {
		Entries []fileEntry `json:"entries"`
	}
	get("/api/files/list", url.Values{}, &list)
	if len(list.Entries) != 3 || !list.Entries[0].Dir || list.Entries[0].Name != "output" {
		t.Fatalf("root listing (folders first, .git hidden): %+v", list.Entries)
	}

	var file struct {
		Path, Text string
		Binary     bool
	}
	get("/api/files/read", url.Values{"path": {"output/balance-missing-sequence.md"}}, &file)
	if file.Text == "" || file.Path != "output/balance-missing-sequence.md" {
		t.Fatalf("read: %+v", file)
	}
	get("/api/files/read", url.Values{"path": {"logo.bin"}}, &file)
	if !file.Binary {
		t.Fatal("a binary file was read as text")
	}
	if code := get("/api/files/read", url.Values{"path": {"../../etc/passwd"}}, nil); code == http.StatusOK {
		t.Fatal("read outside the project")
	}

	for in, want := range map[string]string{
		"output/balance-missing-sequence.md": "output/balance-missing-sequence.md",
		"`balance-missing-sequence.md`":      "output/balance-missing-sequence.md",
		"main.go:1":                          "main.go",
		filepath.Join(root, "output", "balance-missing-sequence.md"): "output/balance-missing-sequence.md",
	} {
		var res struct {
			Path       string   `json:"path"`
			Candidates []string `json:"candidates"`
		}
		get("/api/files/resolve", url.Values{"p": {in}}, &res)
		if res.Path != want {
			t.Errorf("resolve %q = %q (%v); want %q", in, res.Path, res.Candidates, want)
		}
	}
}
