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
	"slices"
	"strings"

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
	// run is the shell running one command and ending, or nil when it
	// cannot be handed one faithfully (cmd parses its own command line).
	run func(command string) []string
}

// shellsFor lists the shells a project can open, the usual one first: the
// distribution's own shell for a project in WSL; PowerShell, cmd, PowerShell
// 7 and Git Bash, where present, for one on the host. A project in WSL can
// open the host's too, in the Windows home: what the agent asks to be run
// "on the Windows side" — a login helper, a .exe — has no other way there.
func shellsFor(root string) []shellChoice {
	if d, linux, ok := gateway.WSLPath(root); ok {
		wsl := shellChoice{ID: "wsl", Label: d + " (WSL)", argv: []string{"wsl.exe", "-d", d, "--cd", linux},
			// --exec keeps the command one argument: without it wsl.exe joins
			// what follows into a line its login shell parses a second time.
			// -i reads .bashrc, where PATH additions (nvm, pyenv) usually are.
			run: func(c string) []string {
				return []string{"wsl.exe", "-d", d, "--cd", linux, "--exec", "bash", "-lic", c}
			}}
		out := []shellChoice{wsl}
		for _, h := range hostShells(os.Getenv("USERPROFILE")) {
			h.ID, h.Label = "host-"+h.ID, h.Label+" (Windows)"
			out = append(out, h)
		}
		return out
	}
	if runtime.GOOS != "windows" {
		sh := os.Getenv("SHELL")
		if sh == "" {
			sh = "/bin/sh"
		}
		return []shellChoice{{ID: "sh", Label: filepath.Base(sh), argv: []string{sh, "-l"}, dir: root,
			run: func(c string) []string { return []string{sh, "-lc", c} }}}
	}
	return hostShells(root)
}

// hostShells are the Windows shells, started in dir.
func hostShells(dir string) []shellChoice {
	if runtime.GOOS != "windows" {
		return nil
	}
	out := []shellChoice{{ID: "powershell", Label: "PowerShell", argv: []string{"powershell.exe", "-NoLogo"}, dir: dir,
		run: func(c string) []string { return []string{"powershell.exe", "-NoLogo", "-Command", c} }}}
	if p, err := exec.LookPath("pwsh.exe"); err == nil {
		out = append(out, shellChoice{ID: "pwsh", Label: "PowerShell 7", argv: []string{p, "-NoLogo"}, dir: dir,
			run: func(c string) []string { return []string{p, "-NoLogo", "-Command", c} }})
	}
	out = append(out, shellChoice{ID: "cmd", Label: "Command Prompt", argv: []string{"cmd.exe"}, dir: dir})
	for _, p := range []string{`C:\Program Files\Git\bin\bash.exe`, filepath.Join(os.Getenv("LOCALAPPDATA"), `Programs\Git\bin\bash.exe`)} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			out = append(out, shellChoice{ID: "bash", Label: "Git Bash", argv: []string{p, "--login", "-i"}, dir: dir,
				run: func(c string) []string { return []string{p, "--login", "-i", "-c", c} }})
			break
		}
	}
	return out
}

func (s *Server) termRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/term/shells", func(w http.ResponseWriter, r *http.Request) {
		// run=1 lists the shells that can run one command (the Run button).
		shells := shellsFor(r.URL.Query().Get("root"))
		if r.URL.Query().Get("run") == "1" {
			shells = slices.DeleteFunc(shells, func(sh shellChoice) bool { return sh.run == nil })
		}
		writeJSON(w, map[string]any{"shells": shells})
	})

	m.HandleFunc("GET /api/term", func(w http.ResponseWriter, r *http.Request) {
		out := []map[string]any{}
		for _, t := range s.Terms.List(r.URL.Query().Get("root")) {
			exited, code := t.State()
			out = append(out, map[string]any{"id": t.ID, "root": t.Root, "shell": t.Shell, "started": t.Started, "exited": exited, "code": code, "command": t.Command})
		}
		writeJSON(w, map[string]any{"terms": out})
	})

	m.HandleFunc("POST /api/term", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root  string `json:"root"`
			Shell string `json:"shell"`
			Cols  int    `json:"cols"`
			Rows  int    `json:"rows"`
			// Command, when given, is run by the shell, which then ends:
			// a command an answer suggested, run from beside it.
			Command string `json:"command"`
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
		argv := pick.argv
		if in.Command = strings.TrimSpace(in.Command); in.Command != "" {
			if pick.run == nil {
				fail(w, http.StatusBadRequest, fmt.Errorf("%s cannot be handed a command; pick another shell", pick.Label))
				return
			}
			argv = pick.run(in.Command)
		}
		t, err := s.Terms.Start(term.Spec{Root: in.Root, Dir: pick.dir, Shell: pick.Label, Argv: argv, Cols: in.Cols, Rows: in.Rows, Command: in.Command})
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

	m.HandleFunc("/api/term/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
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
