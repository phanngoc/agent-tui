package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/config"
)

// The editor saves without overwriting a file changed under it, creates,
// renames and deletes, lists and searches the project, and reads git's view
// of it.
func TestEditorFileAPI(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	gitRun := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	gitRun("init", "-q")
	gitRun("add", ".")
	gitRun("commit", "-qm", "first")

	srv := New(config.Default(), "test", "")
	ts := httptest.NewServer(srv.guard(srv.mux))
	defer ts.Close()
	call := func(method, path string, body any, out any) int {
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		req.Header.Set("Content-Type", "application/json")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if out != nil {
			_ = json.NewDecoder(r.Body).Decode(out)
		}
		return r.StatusCode
	}
	q := func(v url.Values) string { v.Set("root", root); return "?" + v.Encode() }

	var file struct {
		Text  string
		Mtime int64
	}
	call("GET", "/api/files/read"+q(url.Values{"path": {"main.go"}}), nil, &file)
	// The page compares it as a JavaScript number, exact only below 2^53.
	if file.Mtime <= 0 || file.Mtime >= 1<<53 {
		t.Fatalf("mtime %d is not exact in JavaScript", file.Mtime)
	}
	var saved struct{ Modified int64 }
	if c := call("PUT", "/api/files/write", map[string]any{"root": root, "path": "main.go", "text": "package main\n\nfunc main() { println(1) }\n", "base": file.Mtime}, &saved); c != 200 || saved.Modified == 0 {
		t.Fatalf("save: %d %+v", c, saved)
	}
	// A stale base is refused, and force overrides it.
	if c := call("PUT", "/api/files/write", map[string]any{"root": root, "path": "main.go", "text": "x", "base": file.Mtime - 1}, nil); c != http.StatusConflict {
		t.Fatalf("stale save: %d", c)
	}
	if c := call("PUT", "/api/files/write", map[string]any{"root": root, "path": "main.go", "text": "package main\n\nfunc main() { println(2) }\n", "base": 1, "force": true}, nil); c != 200 {
		t.Fatalf("forced save: %d", c)
	}

	if c := call("POST", "/api/files/create", map[string]any{"root": root, "path": "pkg/util", "dir": true}, nil); c != 200 {
		t.Fatalf("mkdir: %d", c)
	}
	if c := call("POST", "/api/files/create", map[string]any{"root": root, "path": "pkg/util/a.go"}, nil); c != 200 {
		t.Fatalf("create: %d", c)
	}
	if c := call("POST", "/api/files/create", map[string]any{"root": root, "path": "pkg/util/a.go"}, nil); c != http.StatusConflict {
		t.Fatalf("create twice: %d", c)
	}
	if c := call("POST", "/api/files/rename", map[string]any{"root": root, "from": "pkg/util/a.go", "to": "pkg/b.go"}, nil); c != 200 {
		t.Fatalf("rename: %d", c)
	}
	if _, err := os.Stat(filepath.Join(root, "pkg", "b.go")); err != nil {
		t.Fatal("renamed file missing")
	}
	if c := call("POST", "/api/files/delete", map[string]any{"root": root, "path": "pkg/util"}, nil); c != http.StatusNoContent {
		t.Fatalf("delete: %d", c)
	}
	if c := call("POST", "/api/files/delete", map[string]any{"root": root, "path": ""}, nil); c != http.StatusBadRequest {
		t.Fatalf("deleting the project: %d", c)
	}

	var all struct{ Files []string }
	call("GET", "/api/files/all"+q(url.Values{}), nil, &all)
	if strings.Join(all.Files, ",") != "main.go,pkg/b.go" && strings.Join(all.Files, ",") != "pkg/b.go,main.go" {
		t.Fatalf("all files: %v", all.Files)
	}
	var found struct {
		Hits []struct {
			Path string
			Line int
		}
	}
	call("GET", "/api/files/search"+q(url.Values{"q": {"println"}, "include": {"*.go"}}), nil, &found)
	if len(found.Hits) != 1 || found.Hits[0].Path != "main.go" || found.Hits[0].Line != 3 {
		t.Fatalf("search: %+v", found.Hits)
	}

	var st struct {
		Repo    bool
		Branch  string
		Changes []struct{ Path, Status string }
	}
	call("GET", "/api/git/status"+q(url.Values{}), nil, &st)
	got := map[string]string{}
	for _, c := range st.Changes {
		got[c.Path] = c.Status
	}
	if !st.Repo || got["main.go"] != " M" || got["pkg/b.go"] != "??" || st.Branch == "" {
		t.Fatalf("status: %+v", st)
	}
	var head struct{ Text string }
	call("GET", "/api/git/head"+q(url.Values{"path": {"main.go"}}), nil, &head)
	if head.Text != "package main\n\nfunc main() {}\n" {
		t.Fatalf("HEAD text: %q", head.Text)
	}
}

func TestMatchGlobs(t *testing.T) {
	for p, want := range map[string]bool{"a/b/c.go": true, "web/x/y.tsx": true, "README.md": false} {
		if got := matchGlobs("*.go, web/**", p); got != want {
			t.Errorf("%s: %v", p, got)
		}
	}
}
