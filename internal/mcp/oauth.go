package mcp

// Signing in to a remote MCP server, the way the MCP authorization spec has
// it: the server is an OAuth 2.1 resource; it names its authorization server
// in protected-resource metadata (RFC 9728); that server describes itself
// (RFC 8414) and registers clients on request (RFC 7591); and the sign-in is
// an authorization-code grant with PKCE, its redirect landing on a loopback
// port this process listens on for the minute it takes (RFC 8252).
//
// Claude Code does the same for its own servers and keeps its tokens to
// itself, so a server imported from it arrives here signed out. Borrowing its
// tokens is not an option: refreshing one rotates the refresh token and signs
// Claude Code out. So agent-tui registers as a client of its own and keeps its
// tokens in the config folder, readable by this user only, shared by the
// terminal and the gateway.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
)

// AuthRequiredError is a server refusing a request for want of a sign-in.
type AuthRequiredError struct {
	Server string
	Detail string
}

func (e *AuthRequiredError) Error() string {
	return "needs sign-in (OAuth): " + e.Detail
}

// NeedsAuth says err is a server asking to be signed in to.
func NeedsAuth(err error) bool {
	var a *AuthRequiredError
	return errors.As(err, &a)
}

// grant is what a sign-in leaves behind for one server.
type grant struct {
	Resource      string    `json:"resource"`
	Issuer        string    `json:"issuer,omitempty"`
	TokenEndpoint string    `json:"token_endpoint"`
	ClientID      string    `json:"client_id"`
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	Expiry        time.Time `json:"expiry,omitempty"`
	Scope         string    `json:"scope,omitempty"`
	At            time.Time `json:"at"`
}

var grantsMu sync.Mutex

func grantsPath() string { return filepath.Join(config.Dir(), "mcp-auth.json") }

// grantKey names a server's grant by its address without the query, which
// some servers use only to say who is calling.
func grantKey(u string) string {
	if p, err := url.Parse(u); err == nil {
		p.RawQuery, p.Fragment = "", ""
		return p.String()
	}
	return u
}

func loadGrants() map[string]grant {
	out := map[string]grant{}
	if b, err := os.ReadFile(grantsPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func saveGrants(g map[string]grant) error {
	b, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	tmp := grantsPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, grantsPath())
}

// SignedIn says a sign-in is kept for the server at u.
func SignedIn(u string) bool {
	grantsMu.Lock()
	defer grantsMu.Unlock()
	_, ok := loadGrants()[grantKey(u)]
	return ok
}

// SignOut forgets the sign-in for the server at u.
func SignOut(u string) error {
	grantsMu.Lock()
	defer grantsMu.Unlock()
	g := loadGrants()
	delete(g, grantKey(u))
	return saveGrants(g)
}

// accessToken returns a token for the server at u, refreshing it first when
// it has expired or force says the server just refused it. "" means there is
// no sign-in, or it could not be renewed.
func accessToken(ctx context.Context, u string, force bool) string {
	grantsMu.Lock()
	defer grantsMu.Unlock()
	all := loadGrants()
	g, ok := all[grantKey(u)]
	if !ok {
		return ""
	}
	fresh := g.Expiry.IsZero() || time.Until(g.Expiry) > time.Minute
	if fresh && !force {
		return g.AccessToken
	}
	if g.RefreshToken == "" {
		if force {
			return ""
		}
		return g.AccessToken
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {g.RefreshToken},
		"client_id": {g.ClientID}, "resource": {g.Resource}}
	t, err := tokenRequest(ctx, g.TokenEndpoint, form)
	if err != nil {
		return ""
	}
	g.AccessToken = t.AccessToken
	if t.RefreshToken != "" {
		g.RefreshToken = t.RefreshToken
	}
	g.Expiry = t.expiry()
	all[grantKey(u)] = g
	_ = saveGrants(all)
	return g.AccessToken
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

func (t tokenResponse) expiry() time.Time {
	if t.ExpiresIn <= 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
}

func tokenRequest(ctx context.Context, endpoint string, form url.Values) (tokenResponse, error) {
	var t tokenResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return t, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return t, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(b, &t)
	if resp.StatusCode >= 300 || t.AccessToken == "" {
		msg := t.Description
		if msg == "" {
			msg = t.Error
		}
		if msg == "" {
			msg = strings.TrimSpace(string(b))
		}
		return t, fmt.Errorf("token: http %d: %.300s", resp.StatusCode, msg)
	}
	return t, nil
}

// --- discovery ---------------------------------------------------------------

type resourceMeta struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

type serverMeta struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint"`
	ScopesSupported       []string `json:"scopes_supported"`
	CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
}

func getJSON(ctx context.Context, u string, v any) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v) == nil
}

// wellKnown lists where RFC 8414 and RFC 9728 put a document for an address
// with a path: inserted between the host and the path first, then at the root.
func wellKnown(base, name string) []string {
	p, err := url.Parse(base)
	if err != nil {
		return nil
	}
	origin := p.Scheme + "://" + p.Host
	path := strings.TrimSuffix(p.EscapedPath(), "/")
	var out []string
	if path != "" {
		out = append(out, origin+"/.well-known/"+name+path)
	}
	return append(out, origin+"/.well-known/"+name)
}

// probeChallenge asks the server, unauthenticated, for what it wants: a 401's
// WWW-Authenticate may name the resource metadata and the scope.
func probeChallenge(ctx context.Context, u string) (metaURL, scope string) {
	body := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"` + protocolVersion +
		`","capabilities":{},"clientInfo":{"name":"agent-tui","version":"1"}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(body))
	if err != nil {
		return "", ""
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", ""
	}
	resp.Body.Close()
	h := resp.Header.Get("WWW-Authenticate")
	return authParam(h, "resource_metadata"), authParam(h, "scope")
}

func authParam(h, name string) string {
	i := strings.Index(strings.ToLower(h), name+"=")
	if i < 0 {
		return ""
	}
	v := h[i+len(name)+1:]
	if strings.HasPrefix(v, `"`) {
		if j := strings.Index(v[1:], `"`); j >= 0 {
			return v[1 : j+1]
		}
		return ""
	}
	if j := strings.IndexAny(v, ", "); j >= 0 {
		v = v[:j]
	}
	return v
}

// discover finds the authorization server for the MCP server at u, and the
// resource and scope to ask it for.
func discover(ctx context.Context, u string) (serverMeta, string, string, error) {
	resource := grantKey(u)
	metaURL, scope := probeChallenge(ctx, u)
	var rm resourceMeta
	found := false
	cands := wellKnown(resource, "oauth-protected-resource")
	if metaURL != "" {
		cands = append([]string{metaURL}, cands...)
	}
	for _, c := range cands {
		if getJSON(ctx, c, &rm) && len(rm.AuthorizationServers) > 0 {
			found = true
			break
		}
	}
	issuer := ""
	if found {
		issuer = rm.AuthorizationServers[0]
		if rm.Resource != "" {
			resource = rm.Resource
		}
		if scope == "" {
			scope = strings.Join(rm.ScopesSupported, " ")
		}
	} else {
		// Servers from before resource metadata: the server's own origin is
		// its authorization server.
		p, _ := url.Parse(u)
		issuer = p.Scheme + "://" + p.Host
	}
	var sm serverMeta
	ok := false
	for _, c := range append(wellKnown(issuer, "oauth-authorization-server"), wellKnown(issuer, "openid-configuration")...) {
		if getJSON(ctx, c, &sm) && sm.AuthorizationEndpoint != "" && sm.TokenEndpoint != "" {
			ok = true
			break
		}
	}
	if !ok && getJSON(ctx, strings.TrimSuffix(issuer, "/")+"/.well-known/openid-configuration", &sm) &&
		sm.AuthorizationEndpoint != "" && sm.TokenEndpoint != "" {
		ok = true
	}
	if !ok {
		return sm, "", "", fmt.Errorf("no OAuth authorization server found for %s", u)
	}
	if scope == "" {
		scope = strings.Join(sm.ScopesSupported, " ")
	}
	return sm, resource, scope, nil
}

func register(ctx context.Context, sm serverMeta, redirect string) (string, error) {
	if sm.RegistrationEndpoint == "" {
		return "", errors.New("the authorization server does not register clients itself; add an Authorization header instead")
	}
	b, _ := json.Marshal(map[string]any{
		"client_name":                "agent-tui",
		"redirect_uris":              []string{redirect},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sm.RegistrationEndpoint, strings.NewReader(string(b)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		ClientID string `json:"client_id"`
	}
	if resp.StatusCode >= 300 || json.Unmarshal(body, &out) != nil || out.ClientID == "" {
		return "", fmt.Errorf("client registration: http %d: %.300s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return out.ClientID, nil
}

// --- signing in --------------------------------------------------------------

// Login is a sign-in under way: URL is the page to open in a browser, and Wait
// returns once the browser has come back, or the sign-in has failed or timed
// out.
type Login struct {
	URL  string
	fin  chan struct{}
	err  error
	once sync.Once
}

// Wait blocks until the sign-in ends.
func (l *Login) Wait(ctx context.Context) error {
	select {
	case <-l.fin:
		return l.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Result says whether the sign-in has ended, and how, without waiting.
func (l *Login) Result() (bool, error) {
	select {
	case <-l.fin:
		return true, l.err
	default:
		return false, nil
	}
}

// loginTimeout is how long the browser has to come back.
const loginTimeout = 10 * time.Minute

func random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// BeginLogin starts signing in to s: it discovers the server's authorization
// server, registers agent-tui with it, listens on a loopback port for the
// browser to come back, and returns the address to send the browser to.
func BeginLogin(ctx context.Context, s Server) (*Login, error) {
	if s.Transport() == "stdio" {
		return nil, errors.New("only remote (http) servers sign in")
	}
	sm, resource, scope, err := discover(ctx, s.URL)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
	clientID, err := register(ctx, sm, redirect)
	if err != nil {
		ln.Close()
		return nil, err
	}
	verifier := random(48)
	sum := sha256.Sum256([]byte(verifier))
	state := random(24)
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirect},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"resource":              {resource},
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	sep := "?"
	if strings.Contains(sm.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	l := &Login{URL: sm.AuthorizationEndpoint + sep + q.Encode(), fin: make(chan struct{})}

	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second}
	finish := func(err error) {
		l.once.Do(func() {
			l.err = err
			close(l.fin)
			go func() {
				time.Sleep(500 * time.Millisecond)
				_ = srv.Close()
			}()
		})
	}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		qq := r.URL.Query()
		if qq.Get("state") != state {
			page(w, http.StatusBadRequest, "That sign-in link is stale", "Start the sign-in again from agent-tui.")
			return
		}
		if e := qq.Get("error"); e != "" {
			msg := strings.TrimSpace(e + ": " + qq.Get("error_description"))
			page(w, http.StatusBadRequest, "Not signed in", msg)
			finish(errors.New(msg))
			return
		}
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		t, err := tokenRequest(cctx, sm.TokenEndpoint, url.Values{
			"grant_type": {"authorization_code"}, "code": {qq.Get("code")}, "redirect_uri": {redirect},
			"client_id": {clientID}, "code_verifier": {verifier}, "resource": {resource},
		})
		if err != nil {
			page(w, http.StatusBadGateway, "Not signed in", err.Error())
			finish(err)
			return
		}
		grantsMu.Lock()
		all := loadGrants()
		all[grantKey(s.URL)] = grant{Resource: resource, Issuer: sm.Issuer, TokenEndpoint: sm.TokenEndpoint,
			ClientID: clientID, AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, Expiry: t.expiry(),
			Scope: t.Scope, At: time.Now().UTC()}
		err = saveGrants(all)
		grantsMu.Unlock()
		if err != nil {
			page(w, http.StatusInternalServerError, "Not signed in", err.Error())
			finish(err)
			return
		}
		page(w, http.StatusOK, "Signed in to "+s.Name, "agent-tui can use it now. You can close this tab.")
		finish(nil)
	})
	go func() { _ = srv.Serve(ln) }()
	go func() {
		time.Sleep(loginTimeout)
		finish(errors.New("the sign-in timed out"))
	}()
	return l, nil
}

func page(w http.ResponseWriter, code int, title, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%[1]s</title>
<body style="font:16px system-ui;margin:12vh auto;max-width:34rem;padding:0 1rem;color:#222">
<h2>%[1]s</h2><p>%[2]s</p></body>`, html.EscapeString(title), html.EscapeString(text))
}
