package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/browser"
	"github.com/phanngoc/agent-tui/internal/config"
)

// shotsDir is where screenshots of the browser are kept, for the agent to
// open and the admin to show.
func shotsDir() string { return filepath.Join(config.DataDir(), "browser", "shots") }

func (s *Server) browserRoutes(m *http.ServeMux) {
	// ---- the extension's side: /api/browser/ext/*, from its own origin ----

	ext := func(r *http.Request) (client, token string) {
		return r.Header.Get("X-Agent-Tui-Client"), r.Header.Get("X-Agent-Tui-Token")
	}
	authed := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		id, token := ext(r)
		if !s.Browser.Auth(id, token) {
			fail(w, http.StatusUnauthorized, errors.New("not approved: approve the extension on the admin's Browser page"))
			return "", false
		}
		return id, true
	}

	// hello: in with a token, handed one once approved, or asked to wait
	// with a code the admin shows beside the approve button.
	m.HandleFunc("POST /api/browser/ext/hello", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Name, Version string }
		_ = readJSON(r, &in)
		id, token := ext(r)
		status, newToken, code := s.Browser.Hello(id, in.Name, in.Version, token)
		if status == "pending" {
			s.changed("browser", "")
		}
		writeJSON(w, map[string]any{"status": status, "token": newToken, "code": code})
	})

	// next: a command, or nothing after browser.Poll.
	m.HandleFunc("POST /api/browser/ext/next", func(w http.ResponseWriter, r *http.Request) {
		id, ok := authed(w, r)
		if !ok {
			return
		}
		if c, ok := s.Browser.Next(r.Context(), id); ok {
			writeJSON(w, map[string]any{"command": c})
			return
		}
		writeJSON(w, map[string]any{})
	})

	m.HandleFunc("POST /api/browser/ext/result", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authed(w, r); !ok {
			return
		}
		var res browser.Result
		if err := readJSON(r, &res); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		s.Browser.Deliver(res)
		w.WriteHeader(http.StatusNoContent)
	})

	// ---- the admin's and the agent's side, loopback only ----

	m.HandleFunc("GET /api/browser/status", func(w http.ResponseWriter, r *http.Request) {
		st := s.Browser.Status()
		dir := browser.ExtensionDir()
		_, err := os.Stat(filepath.Join(dir, "manifest.json"))
		writeJSON(w, map[string]any{"connected": st.Connected, "clients": st.Clients, "pending": st.Pending,
			"extension_dir": dir, "extension_written": err == nil})
	})

	m.HandleFunc("POST /api/browser/approve", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID, Code string }
		_ = readJSON(r, &in)
		if err := s.Browser.Approve(firstNonEmpty(in.ID, in.Code)); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		s.changed("browser", "")
		writeJSON(w, map[string]any{"ok": true})
	})
	m.HandleFunc("POST /api/browser/reject", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID, Code string }
		_ = readJSON(r, &in)
		s.Browser.Reject(firstNonEmpty(in.ID, in.Code))
		s.changed("browser", "")
		writeJSON(w, map[string]any{"ok": true})
	})
	m.HandleFunc("POST /api/browser/revoke", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID string }
		_ = readJSON(r, &in)
		s.Browser.Revoke(in.ID)
		s.changed("browser", "")
		writeJSON(w, map[string]any{"ok": true})
	})

	// extension writes the extension's folder, pointed at this gateway, for
	// Chrome's "Load unpacked".
	m.HandleFunc("POST /api/browser/extension", func(w http.ResponseWriter, r *http.Request) {
		dir := browser.ExtensionDir()
		if err := browser.WriteExtension(dir, "http://"+r.Host); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"path": dir})
	})

	// do runs one action in the browser: the agent's tools and the admin's
	// try-it box come through here. A screenshot comes back as a file.
	m.HandleFunc("POST /api/browser/do", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action string          `json:"action"`
			Args   json.RawMessage `json:"args"`
		}
		if err := readJSON(r, &in); err != nil || in.Action == "" {
			fail(w, http.StatusBadRequest, errors.New("which action?"))
			return
		}
		if len(in.Args) == 0 {
			in.Args = json.RawMessage("{}")
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		res, err := s.Browser.Do(ctx, in.Action, in.Args)
		if err != nil {
			code := http.StatusBadGateway
			if errors.Is(err, browser.ErrOffline) {
				code = http.StatusServiceUnavailable
			}
			fail(w, code, err)
			return
		}
		data := map[string]any{}
		_ = json.Unmarshal(res.Data, &data)
		if du, ok := data["dataUrl"].(string); ok {
			path, err := saveShot(du)
			if err != nil {
				fail(w, http.StatusInternalServerError, err)
				return
			}
			delete(data, "dataUrl")
			data["path"] = path
			data["name"] = filepath.Base(path)
		}
		writeJSON(w, data)
	})

	// shot serves a screenshot by name, for the admin.
	m.HandleFunc("GET /api/browser/shot", func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Query().Get("name"))
		if !strings.HasSuffix(name, ".png") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		http.ServeFile(w, r, filepath.Join(shotsDir(), name))
	})
}

// saveShot keeps a data: URL's PNG among the screenshots, the oldest past a
// hundred removed.
func saveShot(dataURL string) (string, error) {
	_, b64, ok := strings.Cut(dataURL, ",")
	if !ok || !strings.HasPrefix(dataURL, "data:image/png") {
		return "", errors.New("not a PNG screenshot")
	}
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", err
	}
	dir := shotsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("shot-%s.png", time.Now().Format("20060102-150405.000")))
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	if old, _ := filepath.Glob(filepath.Join(dir, "shot-*.png")); len(old) > 100 {
		for _, o := range old[:len(old)-100] {
			_ = os.Remove(o)
		}
	}
	return path, nil
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}
