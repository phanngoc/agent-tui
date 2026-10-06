package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/remote"
)

// Through the tunnel the gateway is shut until remote access is on, and then
// open only signed in, and never to what only this machine should do.
func TestRemoteRequestsNeedASignIn(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := New(config.Default(), "test", "")
	h := srv.guard(srv.mux)
	const host = "agent.example.org"
	do := func(method, path, body string, set func(*http.Request)) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
		r.Header.Set("Cf-Connecting-Ip", "203.0.113.9")
		if set != nil {
			set(r)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	if w := do("GET", "/api/health", "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("remote access off: %d", w.Code)
	}
	st, err := srv.remoteUpdate(func(s *remote.Settings) { s.Enabled = true })
	if err != nil {
		t.Fatal(err)
	}
	if w := do("GET", "/api/health", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("not signed in: %d", w.Code)
	}
	if w := do("GET", "/sessions", "", nil); w.Code != http.StatusFound || w.Header().Get("Location") != "/login" {
		t.Fatalf("a page not signed in: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := do("GET", "/login", "", nil); w.Code != http.StatusOK {
		t.Fatalf("the sign-in page: %d", w.Code)
	}
	if w := do("POST", "/api/remote/login", `{"key":"wrong"}`, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong key: %d", w.Code)
	}
	w := do("POST", "/api/remote/login", `{"key":"`+st.Key+`"}`, nil)
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == remote.CookieName {
			cookie = c
		}
	}
	if w.Code != http.StatusOK || cookie == nil || !cookie.HttpOnly || !cookie.Secure {
		t.Fatalf("sign-in: %d %+v", w.Code, cookie)
	}
	signed := func(r *http.Request) { r.AddCookie(cookie) }
	if w := do("GET", "/api/health", "", signed); w.Code != http.StatusOK {
		t.Fatalf("signed in: %d", w.Code)
	}
	var view map[string]any
	_ = json.Unmarshal(do("GET", "/api/remote", "", signed).Body.Bytes(), &view)
	if _, leaked := view["key"]; leaked || view["remote"] != true {
		t.Fatalf("the remote view gave away the key: %v", view)
	}
	for _, p := range []string{"/api/remote/rotate", "/api/remote/tunnel/stop", "/api/gateway/shutdown", "/api/gateway/peers"} {
		if w := do("POST", p, "{}", signed); w.Code != http.StatusForbidden {
			t.Errorf("%s from the tunnel: %d", p, w.Code)
		}
	}
	if w := do("PUT", "/api/remote", `{"enabled":false}`, signed); w.Code != http.StatusForbidden {
		t.Errorf("remote settings from the tunnel: %d", w.Code)
	}
	evil := func(r *http.Request) { signed(r); r.Header.Set("Origin", "https://evil.example") }
	if w := do("GET", "/api/health", "", evil); w.Code != http.StatusForbidden {
		t.Errorf("another origin with the cookie: %d", w.Code)
	}
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+st.Key) }
	if w := do("GET", "/api/health", "", bearer); w.Code != http.StatusOK {
		t.Errorf("the key as a bearer token: %d", w.Code)
	}

	// On this machine nothing changes, and the key is shown.
	r := httptest.NewRequest("GET", "http://127.0.0.1:7788/api/remote", nil)
	lw := httptest.NewRecorder()
	h.ServeHTTP(lw, r)
	_ = json.Unmarshal(lw.Body.Bytes(), &view)
	if lw.Code != http.StatusOK || view["key"] != st.Key {
		t.Fatalf("local: %d %v", lw.Code, view)
	}
}
