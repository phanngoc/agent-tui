package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeOAuth is a remote MCP server behind OAuth, laid out as Datadog's is:
// protected-resource metadata under the server's path, the authorization
// server's metadata, dynamic registration, PKCE, and a token that expires.
type fakeOAuth struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	clients map[string]string // client id → redirect
	codes   map[string]string // code → challenge
	tokens  map[string]bool
	refresh map[string]bool
	issued  int
}

func newFakeOAuth(t *testing.T) *fakeOAuth {
	f := &fakeOAuth{t: t, clients: map[string]string{}, codes: map[string]string{},
		tokens: map[string]bool{}, refresh: map[string]bool{}}
	m := http.NewServeMux()
	m.HandleFunc("/.well-known/oauth-protected-resource/v1/mcp", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": f.srv.URL + "/v1/mcp", "authorization_servers": []string{f.srv.URL + "/v1/mcp"}})
	})
	m.HandleFunc("/.well-known/oauth-authorization-server/v1/mcp", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": f.srv.URL + "/v1/mcp",
			"authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": f.srv.URL + "/token",
			"registration_endpoint": f.srv.URL + "/register", "scopes_supported": []string{"mcp_all"},
			"code_challenge_methods_supported": []string{"S256"}})
	})
	m.HandleFunc("POST /register", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RedirectURIs []string `json:"redirect_uris"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		id := "client-" + string(rune('a'+len(f.clients)))
		f.clients[id] = in.RedirectURIs[0]
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"client_id": id})
	})
	// The browser's part: the user approves, and is sent back with a code.
	m.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f.mu.Lock()
		redirect := f.clients[q.Get("client_id")]
		f.codes["code1"] = q.Get("code_challenge")
		f.mu.Unlock()
		if redirect != q.Get("redirect_uri") || q.Get("scope") != "mcp_all" || q.Get("code_challenge_method") != "S256" {
			http.Error(w, "bad authorize request", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, redirect+"?code=code1&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	m.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if f.codes[r.Form.Get("code")] != base64.RawURLEncoding.EncodeToString(sum[:]) {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
		case "refresh_token":
			if !f.refresh[r.Form.Get("refresh_token")] {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
		}
		f.issued++
		tok, ref := "tok"+string(rune('0'+f.issued)), "ref"+string(rune('0'+f.issued))
		f.tokens[tok], f.refresh[ref] = true, true
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": tok, "refresh_token": ref, "expires_in": 3600})
	})
	m.HandleFunc("POST /v1/mcp", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errors":["Unauthorized"]}`))
			return
		}
		var req struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		res := map[string]any{"serverInfo": map[string]string{"name": "fake", "version": "1"}}
		if req.Method == "tools/list" {
			res = map[string]any{"tools": []map[string]any{{"name": "ping"}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": res})
	})
	f.srv = httptest.NewServer(m)
	t.Cleanup(f.srv.Close)
	return f
}

// Signed out, the server asks for a sign-in; signing in takes the browser
// through the authorization server and back; signed in, the server answers;
// a token the server stops accepting is renewed with the refresh token; and
// the claude engine is handed the token as a header.
func TestSignInToAnOAuthServer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	f := newFakeOAuth(t)
	s := Server{Name: "dd", URL: f.srv.URL + "/v1/mcp?referrer=x"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, err := Connect(ctx, s); !NeedsAuth(err) {
		t.Fatalf("signed out: Connect = %v; want a sign-in asked for", err)
	}

	l, err := BeginLogin(ctx, s)
	if err != nil {
		t.Fatal(err)
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

	c, err := Connect(ctx, s)
	if err != nil {
		t.Fatalf("signed in: %v", err)
	}
	if tools, err := c.Tools(ctx); err != nil || len(tools) != 1 {
		t.Fatalf("tools = %v, %v", tools, err)
	}

	// The server forgets the token: the next request renews it and goes on.
	f.mu.Lock()
	f.tokens = map[string]bool{}
	f.mu.Unlock()
	if c, err = Connect(ctx, s); err != nil {
		t.Fatalf("after the token was revoked: %v", err)
	}
	if f.issued != 2 {
		t.Fatalf("tokens issued = %d; want 2 (sign-in, then refresh)", f.issued)
	}

	got := WithSignIn(ctx, []Server{s})[0].Headers["Authorization"]
	if !strings.HasPrefix(got, "Bearer tok") {
		t.Fatalf("claude engine header = %q", got)
	}
	own := Server{Name: "x", URL: s.URL, Headers: map[string]string{"Authorization": "Bearer mine"}}
	if WithSignIn(ctx, []Server{own})[0].Headers["Authorization"] != "Bearer mine" {
		t.Fatal("a definition's own Authorization header was replaced")
	}

	if err := SignOut(s.URL); err != nil || SignedIn(s.URL) {
		t.Fatalf("SignOut: %v", err)
	}
}

func TestAuthParam(t *testing.T) {
	h := `Bearer error="invalid_token", resource_metadata="https://x/.well-known/oauth-protected-resource", scope="a b"`
	if got := authParam(h, "resource_metadata"); got != "https://x/.well-known/oauth-protected-resource" {
		t.Fatalf("resource_metadata = %q", got)
	}
	if got := authParam(h, "scope"); got != "a b" {
		t.Fatalf("scope = %q", got)
	}
}
