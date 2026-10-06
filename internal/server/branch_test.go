package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/git"
)

func TestWorktreeBranchNames(t *testing.T) {
	at := time.Date(2026, 10, 6, 15, 4, 0, 0, time.Local)
	got := worktreeBranch("Fix the login bug, please — now!", at)
	if got != "agent/1006-1504-fix-the-login-bug" || !git.ValidBranch(got) {
		t.Fatalf("%q", got)
	}
	if got := worktreeBranch("sửa lỗi", at); !git.ValidBranch(got) {
		t.Fatalf("a prompt with no ASCII words gave %q", got)
	}
}

// The composer's branch menu over HTTP: list, switch, and a refusal git
// gives (a branch that does not exist) passed on.
func TestBranchAPI(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"commit", "-q", "--allow-empty", "-m", "x"}, {"branch", "feature"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	srv := New(config.Default(), "test", "")
	h := srv.guard(srv.mux)
	do := func(method, path, body string) (int, git.Branches) {
		r := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var b git.Branches
		_ = json.Unmarshal(w.Body.Bytes(), &b)
		return w.Code, b
	}
	q := "?root=" + filepath.ToSlash(root)
	if code, b := do("GET", "/api/git/branches"+q, ""); code != 200 || b.Current != "main" {
		t.Fatalf("branches: %d %+v", code, b)
	}
	body, _ := json.Marshal(map[string]any{"root": root, "branch": "feature"})
	if code, b := do("POST", "/api/git/switch", string(body)); code != 200 || b.Current != "feature" {
		t.Fatalf("switch: %d %+v", code, b)
	}
	body, _ = json.Marshal(map[string]any{"root": root, "branch": "nope"})
	if code, _ := do("POST", "/api/git/switch", string(body)); code != http.StatusConflict {
		t.Fatalf("switch to a missing branch: %d", code)
	}
}
