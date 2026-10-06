package server

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/term"
)

// The web terminal: a shell in a session's project, streamed to the page.
// Output goes out on an event stream (base64, since a terminal's bytes are
// not text), input and resizes come back as posts — the same transports as
// the rest of the admin, without a WebSocket.

type shellChoice struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	argv  []string `json:"-"`
	dir   string
}

// shellsFor lists the shells a project can open, the usual one first: the
// distribution's own shell for a project in WSL; PowerShell, cmd, PowerShell
// 7 and Git Bash, where present, for one on the host.
func shellsFor(root string) []shellChoice {
	if d, linux, ok := gateway.WSLPath(root); ok {
		return []shellChoice{{ID: "wsl", Label: d + " (WSL)", argv: []string{"wsl.exe", "-d", d, "--cd", linux}}}
	}
	if runtime.GOOS != "windows" {
		sh := os.Getenv("SHELL")
		if sh == "" {
			sh = "/bin/sh"
		}
		return []shellChoice{{ID: "sh", Label: filepath.Base(sh), argv: []string{sh, "-l"}, dir: root}}
	}
	out := []shellChoice{{ID: "powershell", Label: "PowerShell", argv: []string{"powershell.exe", "-NoLogo"}, dir: root}}
	if p, err := exec.LookPath("pwsh.exe"); err == nil {
		out = append(out, shellChoice{ID: "pwsh", Label: "PowerShell 7", argv: []string{p, "-NoLogo"}, dir: root})
	}
	out = append(out, shellChoice{ID: "cmd", Label: "Command Prompt", argv: []string{"cmd.exe"}, dir: root})
	for _, p := range []string{`C:\Program Files\Git\bin\bash.exe`, filepath.Join(os.Getenv("LOCALAPPDATA"), `Programs\Git\bin\bash.exe`)} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			out = append(out, shellChoice{ID: "bash", Label: "Git Bash", argv: []string{p, "--login", "-i"}, dir: root})
			break
		}
	}
	return out
}

func (s *Server) termRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/term/shells", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"shells": shellsFor(r.URL.Query().Get("root"))})
	})

	m.HandleFunc("GET /api/term", func(w http.ResponseWriter, r *http.Request) {
		out := []map[string]any{}
		for _, t := range s.Terms.List(r.URL.Query().Get("root")) {
			exited, code := t.State()
			out = append(out, map[string]any{"id": t.ID, "root": t.Root, "shell": t.Shell, "started": t.Started, "exited": exited, "code": code})
		}
		writeJSON(w, map[string]any{"terms": out})
	})

	m.HandleFunc("POST /api/term", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root  string `json:"root"`
			Shell string `json:"shell"`
			Cols  int    `json:"cols"`
			Rows  int    `json:"rows"`
		}
		if err := readJSON(r, &in); err != nil || in.Root == "" {
			fail(w, http.StatusBadRequest, errors.New("which project?"))
			return
		}
		shells := shellsFor(in.Root)
		pick := shells[0]
		for _, sh := range shells {
			if sh.ID == in.Shell {
				pick = sh
			}
		}
		t, err := s.Terms.Start(term.Spec{Root: in.Root, Dir: pick.dir, Shell: pick.Label, Argv: pick.argv, Cols: in.Cols, Rows: in.Rows})
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"id": t.ID, "shell": t.Shell})
	})

	get := func(w http.ResponseWriter, r *http.Request) (*term.Term, bool) {
		t, ok := s.Terms.Get(r.PathValue("id"))
		if !ok {
			fail(w, http.StatusNotFound, errors.New("no such terminal"))
		}
		return t, ok
	}

	m.HandleFunc("GET /api/term/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		t, ok := get(w, r)
		if !ok {
			return
		}
		fl, ok := w.(http.Flusher)
		if !ok {
			fail(w, http.StatusInternalServerError, errors.New("no streaming"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		replay, out, detach := t.Attach()
		defer detach()
		send := func(event string, b []byte) {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, base64.StdEncoding.EncodeToString(b))
			fl.Flush()
		}
		send("data", replay)
		for {
			select {
			case <-r.Context().Done():
				return
			case b, ok := <-out:
				if !ok {
					_, code := t.State()
					fmt.Fprintf(w, "event: exit\ndata: %d\n\n", code)
					fl.Flush()
					return
				}
				send("data", b)
			}
		}
	})

	m.HandleFunc("POST /api/term/{id}/input", func(w http.ResponseWriter, r *http.Request) {
		t, ok := get(w, r)
		if !ok {
			return
		}
		var in struct {
			Data string `json:"data"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if err := t.Write([]byte(in.Data)); err != nil {
			fail(w, http.StatusGone, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	m.HandleFunc("POST /api/term/{id}/resize", func(w http.ResponseWriter, r *http.Request) {
		t, ok := get(w, r)
		if !ok {
			return
		}
		var in struct {
			Cols int `json:"cols"`
			Rows int `json:"rows"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		_ = t.Resize(in.Cols, in.Rows)
		w.WriteHeader(http.StatusNoContent)
	})

	m.HandleFunc("DELETE /api/term/{id}", func(w http.ResponseWriter, r *http.Request) {
		s.Terms.Close(r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
	})
}
