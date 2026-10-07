package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTokenLogin is a remote MCP server without OAuth, laid out as the
// workspace's Shizuka gateway is: the handshake needs no token, everything
// else does, and the 401 names a login page (relative to the host) that sends
// the browser back to the loopback port with a token.
type fakeTokenLogin struct {
	srv    *httptest.Server
	mu     sync.Mutex
	tokens map[string]bool
}

func newFakeTokenLogin(t *testing.T) *fakeTokenLogin {
	f := &fakeTokenLogin{tokens: map[string]bool{}}
	m := http.NewServeMux()
	m.HandleFunc("GET /workspace/api/auth/cli-login", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		redirect := q.Get("redirect_uri")
		if !strings.HasPrefix(redirect, "http://127.0.0.1:") || q.Get("client") != "agent-tui" {
			http.Error(w, "bad login request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.tokens["jwt1"] = true
		f.mu.Unlock()
		back := url.Values{"token": {"jwt1"}, "state": {q.Get("state")}, "expires_in": {"2592000"}}
		http.Redirect(w, r, redirect+"?"+back.Encode(), http.StatusFound)
	})
	m.HandleFunc("POST /workspace/api/mcp", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		f.mu.Unlock()
		if !ok && req.Method != "initialize" && req.ID != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="shizuka-mcp", login_uri="/workspace/api/auth/cli-login"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32001,"message":"authentication required"}}`))
			return
		}
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		res := map[string]any{"serverInfo": map[string]string{"name": "shizuka", "version": "1"}}
		if req.Method == "tools/list" {
			res = map[string]any{"tools": []map[string]any{{"name": "list_projects"}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": res})
	})
	f.srv = httptest.NewServer(m)
	t.Cleanup(f.srv.Close)
	return f
}

// An expired header of the definition's own is a sign-in asked for; signing
// in takes the browser to the server's login page and back with a token; the
// stale header is dropped from the saved definition so the token is used.
func TestSignInThroughALoginPage(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	f := newFakeTokenLogin(t)
	st := Store{GlobalPath: filepath.Join(t.TempDir(), "mcp.json")}
	s := Server{Name: "shizuka", Type: "http", URL: f.srv.URL + "/workspace/api/mcp",
		Headers: map[string]string{"Authorization": "Bearer expired", "X-Other": "1"}, Scope: Global}
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	c, err := Connect(ctx, s)
	if err == nil {
		_, err = c.Tools(ctx)
	}
	if !NeedsAuth(err) {
		t.Fatalf("expired header: err = %v; want a sign-in asked for", err)
	}

	l, err := BeginLogin(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(l.URL, f.srv.URL+"/workspace/api/auth/cli-login?") {
		t.Fatalf("login URL = %q", l.URL)
	}
	resp, err := http.Get(l.URL) // the browser, following the redirect home
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("callback page: http %d", resp.StatusCode)
	}
	if err := l.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if !SignedIn(s.URL) {
		t.Fatal("the sign-in was not kept")
	}

	if dropped, err := st.DropAuthorization(Global, "shizuka"); err != nil || !dropped {
		t.Fatalf("DropAuthorization = %v, %v", dropped, err)
	}
	saved := st.List()[0]
	if _, has := saved.Headers["Authorization"]; has || saved.Headers["X-Other"] != "1" {
		t.Fatalf("saved headers = %v", saved.Headers)
	}
	c, err = Connect(ctx, saved)
	if err != nil {
		t.Fatalf("signed in: %v", err)
	}
	if tools, err := c.Tools(ctx); err != nil || len(tools) != 1 {
		t.Fatalf("tools = %v, %v", tools, err)
	}
	if got := WithSignIn(ctx, []Server{saved})[0].Headers["Authorization"]; got != "Bearer jwt1" {
		t.Fatalf("claude engine header = %q", got)
	}
}

// A server with neither OAuth nor a login page still says so.
func TestSignInWithoutAnyWayIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := BeginLogin(ctx, Server{Name: "x", URL: srv.URL + "/mcp"}); err == nil ||
		!strings.Contains(err.Error(), "no OAuth authorization server") {
		t.Fatalf("err = %v", err)
	}
}
