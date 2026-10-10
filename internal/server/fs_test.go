package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/config"
)

// The folder picker makes a folder where it is, and refuses names a system
// it reaches would not take.
func TestPickerMakesAFolder(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	parent := t.TempDir()
	srv := New(config.Default(), "test", "")
	ts := httptest.NewServer(srv.guard(srv.mux))
	defer ts.Close()
	mkdir := func(parent, name string) (int, string) {
		b, _ := json.Marshal(map[string]string{"parent": parent, "name": name})
		r, err := http.Post(ts.URL+"/api/fs/mkdir", "application/json", strings.NewReader(string(b)))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out struct{ Path, Error string }
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out.Path + out.Error
	}

	code, got := mkdir(parent, "  new-project  ")
	if code != 200 || filepath.Clean(got) != filepath.Join(parent, "new-project") {
		t.Fatalf("mkdir: %d %q", code, got)
	}
	if st, err := os.Stat(filepath.Join(parent, "new-project")); err != nil || !st.IsDir() {
		t.Fatal("the folder was not made")
	}
	if code, _ := mkdir(parent, "new-project"); code != http.StatusConflict {
		t.Fatalf("an existing folder: %d", code)
	}
	for _, bad := range []string{"", "..", "a/b", `a\b`, "x:y", "trailing.", "CON", "com1.txt"} {
		if code, msg := mkdir(parent, bad); code != http.StatusBadRequest {
			t.Errorf("name %q: %d %q", bad, code, msg)
		}
	}
	if code, _ := mkdir("relative", "x"); code != http.StatusBadRequest {
		t.Error("a relative parent was taken")
	}
	if code, _ := mkdir(filepath.Join(parent, "missing"), "x"); code != http.StatusBadRequest {
		t.Error("a missing parent was taken")
	}
}
