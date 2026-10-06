package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/remote"
	"github.com/phanngoc/agent-tui/internal/telegram"
)

// Remote access: the gateway reached through a Cloudflare tunnel, signed in
// (internal/remote).
//
// A request is remote when it came for a name that is not loopback, or
// carries Cloudflare's Cf-Connecting-Ip, which cloudflared adds to whatever
// it forwards. A remote request is let in only while remote access is on,
// only signed in, and never to what only this machine should do: change
// the remote settings, see the key, join as a terminal, stop the gateway.

type remoteState struct {
	mu      sync.Mutex
	set     remote.Settings
	limiter remote.Limiter
	tunnel  *remote.Manager
	local   string // the gateway's own http://127.0.0.1:port, for the tunnel
}

type ctxKey int

const remoteKey ctxKey = 1

// isRemote says r came through the tunnel.
func isRemote(r *http.Request) bool { v, _ := r.Context().Value(remoteKey).(bool); return v }

func (s *Server) remoteSettings() remote.Settings {
	s.remote.mu.Lock()
	defer s.remote.mu.Unlock()
	return s.remote.set
}

func (s *Server) remoteUpdate(f func(*remote.Settings)) (remote.Settings, error) {
	st, err := remote.Update(f)
	if err == nil {
		s.remote.mu.Lock()
		s.remote.set = st
		s.remote.mu.Unlock()
	}
	return st, err
}

// remoteOpen is what a remote request may reach before signing in.
func remoteOpen(r *http.Request) bool {
	switch r.URL.Path {
	case "/login", "/api/remote/login", "/favicon.ico", "/manifest.webmanifest", "/icon.svg",
		// Telegram, which proves itself with the webhook's secret header.
		telegram.WebhookPath:
		return true
	}
	return false
}

// localOnly is what a remote request may never reach, signed in or not.
func localOnly(r *http.Request) bool {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/gateway/peers"), p == "/api/gateway/shutdown":
		return true
	case p == "/api/remote/login", p == "/api/remote/logout", r.Method == http.MethodGet && p == "/api/remote":
		return false
	case strings.HasPrefix(p, "/api/remote"):
		return true
	case p == telegram.WebhookPath:
		return false
	case strings.HasPrefix(p, "/api/telegram") && r.Method != http.MethodGet:
		// The bot's token and who may use it are set where the gateway runs.
		return true
	}
	return false
}

// remoteGuard lets a remote request through, or answers it itself.
func (s *Server) remoteGuard(w http.ResponseWriter, r *http.Request, next http.Handler) {
	st := s.remoteSettings()
	if !st.Enabled {
		http.Error(w, remote.ErrOff.Error(), http.StatusForbidden)
		return
	}
	// A page of ours asks from its own origin; any other origin is a page
	// elsewhere trying its luck with the browser's cookie.
	if o := r.Header.Get("Origin"); o != "" && o != "https://"+r.Host && o != "http://"+r.Host {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	h := w.Header()
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	if localOnly(r) {
		fail(w, http.StatusForbidden, errors.New("only on the gateway's own machine"))
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), remoteKey, true))
	if remoteOpen(r) {
		next.ServeHTTP(w, r)
		return
	}
	if c, err := r.Cookie(remote.CookieName); err == nil && st.ValidSession(c.Value, time.Now()) {
		next.ServeHTTP(w, r)
		return
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") && st.CheckKey(strings.TrimPrefix(auth, "Bearer ")) {
		next.ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		fail(w, http.StatusUnauthorized, errors.New("sign in first"))
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("Cf-Connecting-Ip"); ip != "" {
		return ip
	}
	return r.RemoteAddr
}

// remoteView is what the admin is told; the key and the login link only on
// this machine.
func (s *Server) remoteView(r *http.Request) map[string]any {
	st := s.remoteSettings()
	ts := s.remote.tunnel.Status()
	out := map[string]any{
		"enabled": st.Enabled,
		"remote":  isRemote(r),
		"tunnel": map[string]any{
			"mode": st.Tunnel.Mode, "hostname": st.Tunnel.Hostname, "auto_start": st.Tunnel.AutoStart,
			"has_token": st.Tunnel.Token != "", "has_api_token": st.Tunnel.APIToken != "",
		},
		"status": ts,
	}
	if !isRemote(r) && st.Key != "" {
		out["key"] = st.Key
		if ts.URL != "" {
			out["login_url"] = remote.LoginURL(ts.URL, st.Key)
		}
	}
	return out
}

func (s *Server) startTunnel() {
	st := s.remoteSettings()
	s.remote.tunnel.Start(st.Tunnel, s.remote.local)
}

func (s *Server) remoteRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprint(w, loginPage)
	})
	m.HandleFunc("POST /api/remote/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Key string `json:"key"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		st := s.remoteSettings()
		now := time.Now()
		who := clientIP(r)
		if !s.remote.limiter.Allow(who, now) {
			fail(w, http.StatusTooManyRequests, errors.New("too many wrong keys; try again in a few minutes"))
			return
		}
		if !st.CheckKey(in.Key) {
			s.remote.limiter.Fail(who, now)
			fail(w, http.StatusUnauthorized, errors.New("that is not the key"))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: remote.CookieName, Value: st.NewSession(now), Path: "/",
			MaxAge: int(remote.SessionLife.Seconds()), HttpOnly: true, Secure: isRemote(r), SameSite: http.SameSiteLaxMode})
		writeJSON(w, map[string]any{"ok": true})
	})
	m.HandleFunc("POST /api/remote/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: remote.CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: isRemote(r), SameSite: http.SameSiteLaxMode})
		writeJSON(w, map[string]any{"ok": true})
	})
	m.HandleFunc("GET /api/remote", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.remoteView(r)) })
	m.HandleFunc("PUT /api/remote", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Enabled   *bool   `json:"enabled"`
			Mode      *string `json:"mode"`
			Hostname  *string `json:"hostname"`
			Token     *string `json:"token"`
			APIToken  *string `json:"api_token"`
			AutoStart *bool   `json:"auto_start"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		st, err := s.remoteUpdate(func(st *remote.Settings) {
			if in.Enabled != nil {
				st.Enabled = *in.Enabled
			}
			if in.Mode != nil {
				st.Tunnel.Mode = *in.Mode
			}
			if in.Hostname != nil {
				st.Tunnel.Hostname = strings.TrimSpace(*in.Hostname)
			}
			// Secrets are written, never read back: empty leaves one as it is.
			if in.Token != nil && strings.TrimSpace(*in.Token) != "" {
				st.Tunnel.Token = strings.TrimSpace(*in.Token)
			}
			if in.APIToken != nil && strings.TrimSpace(*in.APIToken) != "" {
				st.Tunnel.APIToken = strings.TrimSpace(*in.APIToken)
			}
			if in.AutoStart != nil {
				st.Tunnel.AutoStart = *in.AutoStart
			}
		})
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		if !st.Enabled {
			s.remote.tunnel.Stop()
		}
		s.changed("remote", "")
		writeJSON(w, s.remoteView(r))
	})
	m.HandleFunc("POST /api/remote/tunnel/start", func(w http.ResponseWriter, r *http.Request) {
		if _, err := s.remoteUpdate(func(st *remote.Settings) { st.Enabled = true }); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		s.startTunnel()
		writeJSON(w, s.remoteView(r))
	})
	m.HandleFunc("POST /api/remote/tunnel/stop", func(w http.ResponseWriter, r *http.Request) {
		s.remote.tunnel.Stop()
		writeJSON(w, s.remoteView(r))
	})
	m.HandleFunc("POST /api/remote/rotate", func(w http.ResponseWriter, r *http.Request) {
		st, err := remote.Rotate()
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		s.remote.mu.Lock()
		s.remote.set = st
		s.remote.mu.Unlock()
		writeJSON(w, s.remoteView(r))
	})
}

// loginPage signs a phone in: the key comes in the link's fragment, which no
// server ever sees, or is typed.
const loginPage = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="color-scheme" content="light dark"><title>agent-tui · sign in</title>
<style>
:root{--bg:#fafafa;--fg:#111;--muted:#666;--card:#fff;--line:#ddd;--acc:#111;--accfg:#fff}
@media (prefers-color-scheme:dark){:root{--bg:#0a0a0a;--fg:#eee;--muted:#999;--card:#161616;--line:#2a2a2a;--acc:#eee;--accfg:#111}}
*{box-sizing:border-box}body{margin:0;min-height:100dvh;display:grid;place-items:center;background:var(--bg);color:var(--fg);font:16px/1.45 system-ui,-apple-system,sans-serif;padding:16px}
form{width:100%;max-width:380px;background:var(--card);border:1px solid var(--line);border-radius:14px;padding:22px}
h1{font-size:19px;margin:0 0 6px}p{color:var(--muted);margin:0 0 16px;font-size:14px}
input{width:100%;font:inherit;padding:12px;border-radius:10px;border:1px solid var(--line);background:transparent;color:inherit}
button{width:100%;margin-top:12px;font:inherit;font-weight:600;padding:12px;border-radius:10px;border:0;background:var(--acc);color:var(--accfg)}
#err{color:#e5484d;font-size:14px;min-height:20px;margin-top:10px}
</style></head><body>
<form id="f"><h1>agent-tui</h1><p>Open the link from the Remote page on your computer, or paste its access key.</p>
<input id="k" name="key" autocomplete="current-password" placeholder="access key" autocapitalize="off" spellcheck="false">
<button>Sign in</button><div id="err"></div></form>
<script>
const f=document.getElementById('f'),k=document.getElementById('k'),err=document.getElementById('err');
async function go(key){err.textContent='';const r=await fetch('/api/remote/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({key})});
if(r.ok){history.replaceState(null,'','/login');location.replace('/');return}
try{err.textContent=(await r.json()).error}catch{err.textContent='sign-in failed'}}
f.onsubmit=e=>{e.preventDefault();go(k.value.trim())};
const m=location.hash.match(/key=([^&]+)/);if(m){history.replaceState(null,'','/login');go(decodeURIComponent(m[1]))}
</script></body></html>`
