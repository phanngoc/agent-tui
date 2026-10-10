package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
)

// The extension's whole conversation with the gateway, over HTTP as the
// extension holds it: asked to wait with a code, approved, handed a token
// once, then polling for commands and posting results — while a web page
// with no token, or from another origin, gets nowhere.
func TestBrowserExtensionProtocol(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := New(config.Default(), "test", "")
	ts := httptest.NewServer(srv.guard(srv.mux))
	defer ts.Close()
	const extOrigin = "chrome-extension://abcdefghijklmnop"

	post := func(path, origin, client, token string, body any, out any) int {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req.Header.Set("X-Agent-Tui-Client", client)
		req.Header.Set("X-Agent-Tui-Token", token)
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
	var hello struct{ Status, Token, Code string }

	// Asks, and waits with a code.
	post("/api/browser/ext/hello", extOrigin, "c1", "", map[string]string{"name": "Chrome/154", "version": "1.0.0"}, &hello)
	if hello.Status != "pending" || len(hello.Code) != 4 || hello.Token != "" {
		t.Fatalf("first hello: %+v", hello)
	}
	// Not approved: no commands.
	if code := post("/api/browser/ext/next", extOrigin, "c1", "", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("next before approval: %d", code)
	}
	// The extension's paths are the only ones its origin reaches.
	if code := post("/api/browser/approve", extOrigin, "c1", "", map[string]string{"code": hello.Code}, nil); code != http.StatusForbidden {
		t.Fatalf("the extension approved itself: %d", code)
	}
	if code := post("/api/browser/ext/hello", "https://evil.example", "c1", "", nil, nil); code != http.StatusForbidden {
		t.Fatalf("a web page reached the extension's paths: %d", code)
	}

	// The user approves the code in the admin; the next hello hands the token over, once.
	var st struct {
		Pending []struct{ ID, Code string } `json:"pending"`
	}
	r, _ := http.Get(ts.URL + "/api/browser/status")
	_ = json.NewDecoder(r.Body).Decode(&st)
	r.Body.Close()
	if len(st.Pending) != 1 || st.Pending[0].Code != hello.Code {
		t.Fatalf("pending: %+v", st)
	}
	if code := post("/api/browser/approve", "", "", "", map[string]string{"code": hello.Code}, nil); code != 200 {
		t.Fatalf("approve: %d", code)
	}
	post("/api/browser/ext/hello", extOrigin, "c1", "", nil, &hello)
	token := hello.Token
	if hello.Status != "ok" || len(token) < 32 {
		t.Fatalf("hello after approval: %+v", hello)
	}
	post("/api/browser/ext/hello", extOrigin, "c1", "", nil, &hello)
	if hello.Token != "" {
		t.Fatal("the token was handed over twice")
	}
	if b, _ := os.ReadFile(filepath.Join(config.DataDir(), "browser.json")); strings.Contains(string(b), token) {
		t.Fatal("the token itself was kept on disk")
	}

	// Offline until it polls: the agent's call says so.
	var fail struct{ Error string }
	if code := post("/api/browser/do", "", "", "", map[string]any{"action": "tabs"}, &fail); code != http.StatusServiceUnavailable || !strings.Contains(fail.Error, "not connected") {
		t.Fatalf("do while offline: %d %q", code, fail.Error)
	}

	// The extension polls; the agent's command reaches it; its result comes back.
	got := make(chan map[string]any, 1)
	go func() {
		var out map[string]any
		post("/api/browser/ext/next", extOrigin, "c1", token, nil, &out)
		got <- out
	}()
	time.Sleep(200 * time.Millisecond)
	done := make(chan map[string]any, 1)
	go func() {
		var out map[string]any
		post("/api/browser/do", "", "", "", map[string]any{"action": "snapshot", "args": map[string]any{"tab": 7}}, &out)
		done <- out
	}()
	var polled map[string]any
	select {
	case polled = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the command never reached the extension")
	}
	cmd, _ := polled["command"].(map[string]any)
	if cmd["action"] != "snapshot" || cmd["args"].(map[string]any)["tab"] != float64(7) {
		t.Fatalf("command: %+v", polled)
	}
	// A screenshot's data URL is kept as a file.
	png := "data:image/png;base64,iVBORw0KGgo="
	if code := post("/api/browser/ext/result", extOrigin, "c1", token, map[string]any{"id": cmd["id"], "ok": true,
		"data": map[string]any{"title": "Example", "dataUrl": png}}, nil); code != http.StatusNoContent {
		t.Fatalf("result: %d", code)
	}
	var res map[string]any
	select {
	case res = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent's call never got its result")
	}
	path, _ := res["path"].(string)
	if res["title"] != "Example" || res["dataUrl"] != nil || !strings.HasSuffix(path, ".png") {
		t.Fatalf("do result: %+v", res)
	}
	if b, err := os.ReadFile(path); err != nil || len(b) == 0 {
		t.Fatalf("screenshot file: %v", err)
	}

	// Revoked, the token stops working.
	post("/api/browser/revoke", "", "", "", map[string]string{"id": "c1"}, nil)
	if code := post("/api/browser/ext/next", extOrigin, "c1", token, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("next after revoke: %d", code)
	}
}
