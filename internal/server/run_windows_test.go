//go:build windows

package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
)

// A command an answer suggested, run from beside it: its output streams and
// its exit code comes back, through the terminal's own stream. The command
// reaches the shell as one argument — the quotes in it are its own.
func TestRunACommandInAShell(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := New(config.Default(), "test", "")
	ts := httptest.NewServer(srv.guard(srv.mux))
	defer ts.Close()
	defer srv.Terms.CloseAll()
	root := t.TempDir()

	run := func(command string) (string, string) {
		body, _ := json.Marshal(map[string]any{"root": root, "shell": "powershell", "command": command, "cols": 120, "rows": 30})
		res, err := http.Post(ts.URL+"/api/term", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		var started struct{ ID string }
		_ = json.NewDecoder(res.Body).Decode(&started)
		res.Body.Close()
		if res.StatusCode != 200 || started.ID == "" {
			t.Fatalf("start: %d", res.StatusCode)
		}
		res, err = http.Get(ts.URL + "/api/term/" + started.ID + "/stream")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		done := make(chan []byte, 1)
		go func() { b, _ := io.ReadAll(res.Body); done <- b }()
		var b []byte
		select {
		case b = <-done:
		case <-time.After(60 * time.Second):
			t.Fatal("the run did not end")
		}
		out, code := decodeStream(t, string(b))

		// A run is not one of the project's terminals to type in, but is
		// listed with its command for the page that ran it.
		lr, _ := http.Get(ts.URL + "/api/term?root=" + root)
		var list struct {
			Terms []struct{ ID, Command string }
		}
		_ = json.NewDecoder(lr.Body).Decode(&list)
		lr.Body.Close()
		found := false
		for _, x := range list.Terms {
			found = found || (x.ID == started.ID && x.Command == command)
		}
		if !found {
			t.Fatalf("run not listed with its command: %+v", list.Terms)
		}
		return out, code
	}

	out, code := run(`Write-Output "agent-tui run: $(1+2)"`)
	if !strings.Contains(out, "agent-tui run: 3") || code != "0" {
		t.Fatalf("output %q, code %s", out, code)
	}
	if _, code := run("exit 3"); code != "3" {
		t.Fatalf("exit code %s; want 3", code)
	}
}

// decodeStream reads the terminal's event stream: its output, and the code
// it exited with.
func decodeStream(t *testing.T, s string) (out, code string) {
	t.Helper()
	var b strings.Builder
	for _, ev := range strings.Split(s, "\n\n") {
		name, data := "", ""
		for _, l := range strings.Split(ev, "\n") {
			if v, ok := strings.CutPrefix(l, "event: "); ok {
				name = v
			} else if v, ok := strings.CutPrefix(l, "data: "); ok {
				data = v
			}
		}
		switch name {
		case "data":
			raw, err := decodeB64(data)
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
		case "exit":
			code = data
		}
	}
	return b.String(), code
}

func decodeB64(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
