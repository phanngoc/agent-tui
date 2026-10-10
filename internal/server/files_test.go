package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
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
		if !q.Has("root") {
			q.Set("root", root)
		}
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

	// A file outside the project opens from its own folder, named absolutely
	// or relative to a session folder outside the project.
	elsewhere := t.TempDir()
	_ = os.WriteFile(filepath.Join(elsewhere, "reply-drafts.md"), []byte("# Drafts\n"), 0o644)
	for _, q := range []url.Values{
		{"p": {filepath.Join(elsewhere, "reply-drafts.md") + ":3"}},
		{"p": {"reply-drafts.md"}, "cwd": {elsewhere}},
	} {
		var res struct {
			Root, Path string
			Outside    bool
		}
		get("/api/files/resolve", q, &res)
		if !res.Outside || filepath.Clean(res.Root) != filepath.Clean(elsewhere) || res.Path != "reply-drafts.md" {
			t.Errorf("resolve %v = %+v; want reply-drafts.md in %s", q, res, elsewhere)
			continue
		}
		var file struct{ Text string }
		get("/api/files/read", url.Values{"root": {res.Root}, "path": {res.Path}}, &file)
		if file.Text != "# Drafts\n" {
			t.Errorf("read the outside file: %q", file.Text)
		}
	}
	var none struct{ Path string }
	get("/api/files/resolve", url.Values{"p": {filepath.Join(elsewhere, "missing.md")}}, &none)
	if none.Path != "" {
		t.Errorf("a missing outside file resolved to %q", none.Path)
	}

	// The home's: "~/keys/test/private.pem", and a folder an agent names
	// relative to it, "Documents\keys\public.jwk.json".
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, "keys", "test"), 0o755)
	_ = os.MkdirAll(filepath.Join(home, "Documents", "keys"), 0o755)
	_ = os.WriteFile(filepath.Join(home, "keys", "test", "private.pem"), []byte("pem"), 0o600)
	_ = os.WriteFile(filepath.Join(home, "Documents", "keys", "public.jwk.json"), []byte("{}"), 0o644)
	for p, want := range map[string]string{
		"~/keys/test/private.pem":        filepath.Join(home, "keys", "test"),
		`Documents\keys\public.jwk.json`: filepath.Join(home, "Documents", "keys"),
		"Documents/keys/public.jwk.json": filepath.Join(home, "Documents", "keys"),
	} {
		var res struct {
			Root, Path string
			Outside    bool
		}
		get("/api/files/resolve", url.Values{"p": {p}}, &res)
		if !res.Outside || filepath.Clean(res.Root) != filepath.Clean(want) || res.Path != path.Base(strings.ReplaceAll(p, `\`, "/")) {
			t.Errorf("resolve %q = %+v; want it in %s", p, res, want)
		}
	}
	get("/api/files/resolve", url.Values{"p": {"~/keys/none.pem"}}, &none)
	if none.Path != "" {
		t.Errorf("a missing home file resolved to %q", none.Path)
	}

	// A path an agent shortened in the middle,
	// "/tmp/claude-1000/.../tasks/w1mzdj41i.output": the file under that
	// folder that ends the same way — outside the project, in it, or the
	// home's.
	tasks := filepath.Join(elsewhere, "-home-x-repo", "7009bc78", "tasks")
	_ = os.MkdirAll(tasks, 0o755)
	_ = os.WriteFile(filepath.Join(tasks, "w1mzdj41i.output"), []byte("{}"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "output", "deep.md"), []byte("#"), 0o644)
	_ = os.MkdirAll(filepath.Join(home, "w", "repo", "src"), 0o755)
	_ = os.WriteFile(filepath.Join(home, "w", "repo", "src", "a.go"), []byte("package a"), 0o644)
	for p, want := range map[string]struct{ root, path string }{
		filepath.Join(elsewhere, "...", "tasks", "w1mzdj41i.output") + ":2": {tasks, "w1mzdj41i.output"},
		filepath.Join(root, "…", "deep.md"):                                 {"", "output/deep.md"},
		"~/w/.../src/a.go":                                                  {filepath.Join(home, "w", "repo", "src"), "a.go"},
	} {
		var res struct {
			Root, Path string
			Outside    bool
		}
		get("/api/files/resolve", url.Values{"p": {p}}, &res)
		if res.Path != want.path || res.Outside != (want.root != "") || want.root != "" && filepath.Clean(res.Root) != filepath.Clean(want.root) {
			t.Errorf("resolve %q = %+v; want %s in %q", p, res, want.path, want.root)
		}
	}
	get("/api/files/resolve", url.Values{"p": {filepath.Join(elsewhere, "...", "tasks", "none.output")}}, &none)
	if none.Path != "" {
		t.Errorf("a missing shortened path resolved to %q", none.Path)
	}
}

func TestElision(t *testing.T) {
	for in, want := range map[string][2]string{
		"/tmp/claude-1000/.../tasks/a.output": {"/tmp/claude-1000", "tasks/a.output"},
		`C:\Users\x\...\b\c.md`:               {`C:\Users\x`, "b/c.md"},
		"src/…/a.go":                          {"src", "a.go"},
	} {
		h, tl, ok := elision(in)
		if !ok || h != want[0] || tl != want[1] {
			t.Errorf("elision(%q) = %q, %q, %v; want %q", in, h, tl, ok, want)
		}
	}
	for _, in := range []string{"/tmp/a/b.go", ".../a.go", "/a/.../", "/a/.../b/.../c.go", "../x/y.go"} {
		if _, _, ok := elision(in); ok {
			t.Errorf("elision(%q) took it for shortened", in)
		}
	}
}

func TestMntPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("a host drive is a Windows thing")
	}
	for in, want := range map[string]string{
		"/mnt/c/Users/x/Documents/a.json": `C:\Users\x\Documents\a.json`,
		"/mnt/d":                          `D:\`,
	} {
		if got, ok := mntPath(in); !ok || got != want {
			t.Errorf("mntPath(%q) = %q %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"/mnt/wsl/x", "/home/x", "/mnt/"} {
		if _, ok := mntPath(in); ok {
			t.Errorf("mntPath(%q) took it for a drive", in)
		}
	}
}

// A path outside a WSL project opens from its folder's \\wsl.localhost share.
func TestOutsideRootWSL(t *testing.T) {
	root := `\\wsl.localhost\Ubuntu-24.04\home\me\fpaas-be`
	for in, want := range map[string][2]string{
		"/home/me/notes/reply-drafts.md":                    {`\\wsl.localhost\Ubuntu-24.04\home\me\notes`, "reply-drafts.md"},
		`\\wsl$\Debian\tmp\a.md`:                            {`\\wsl.localhost\Debian\tmp`, "a.md"},
		"//wsl.localhost/Ubuntu-24.04/home/me/x/../y/b.md":  {`\\wsl.localhost\Ubuntu-24.04\home\me\y`, "b.md"},
		joinAbs(`\\wsl.localhost\Ubuntu-24.04\tmp`, "c.md"): {`\\wsl.localhost\Ubuntu-24.04\tmp`, "c.md"},
	} {
		dir, name, ok := outsideRoot(root, in)
		if !ok || dir != want[0] || name != want[1] {
			t.Errorf("outsideRoot(%q) = %q, %q, %v; want %q, %q", in, dir, name, ok, want[0], want[1])
		}
	}
	if _, _, ok := outsideRoot(root, "notes/a.md"); ok {
		t.Error("a relative path is not outside anything")
	}
}
