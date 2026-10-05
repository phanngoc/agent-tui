package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/schedule"
	"github.com/phanngoc/agent-tui/internal/session"
)

// EvSchedule is a scheduled run finishing or being skipped.
const EvSchedule = "schedule.run"

// schedHost runs the scheduler's work through this gateway: a new session in
// the Runner, or a prompt routed to whoever holds the session — the Runner,
// or a terminal, which then runs it on screen.
type schedHost struct {
	s    *Server
	mu   sync.Mutex
	last map[string]string // session → its latest answer, from the event stream
}

func (h *schedHost) Start(j *schedule.Job, prompt string) (string, error) {
	if j.Session == schedule.SessionSame && j.SessionID != "" {
		if _, err := gateway.Load(j.SessionID); err != nil {
			return "", errors.New("its session is gone")
		}
		h.forget(j.SessionID)
		_, err := h.s.Hub.Route(gateway.Command{Type: gateway.CmdPrompt, Session: j.SessionID, Text: prompt, From: "schedule"})
		return j.SessionID, err
	}
	sess, err := h.s.Runner.NewSession(j.Root, "", "", j.Engine, j.Model, j.Mode, prompt)
	if err != nil {
		return "", err
	}
	h.forget(sess.ID)
	return sess.ID, nil
}

func (h *schedHost) forget(id string) {
	h.mu.Lock()
	delete(h.last, id)
	h.mu.Unlock()
}

func (h *schedHost) Busy(id string) bool { return h.s.Hub.Busy(id) }

func (h *schedHost) Cancel(id string) {
	_, _ = h.s.Hub.Route(gateway.Command{Type: gateway.CmdCancel, Session: id, From: "schedule"})
}

func (h *schedHost) Result(id string) string {
	h.mu.Lock()
	t, ok := h.last[id]
	h.mu.Unlock()
	if ok {
		return t
	}
	if s, err := gateway.Load(id); err == nil {
		for i := len(s.Messages) - 1; i >= 0; i-- {
			if s.Messages[i].Role == session.RoleAssistant && strings.TrimSpace(s.Messages[i].Text) != "" {
				return s.Messages[i].Text
			}
		}
	}
	return ""
}

// Gate runs the job's gate in its project: in the distribution for a WSL
// folder, else in the host's shell.
func (h *schedHost) Gate(ctx context.Context, j *schedule.Job) (string, bool, error) {
	var cmd *exec.Cmd
	if d, linux, ok := gateway.WSLPath(j.Root); ok {
		cmd = exec.CommandContext(ctx, "wsl.exe", "-d", d, "--cd", linux, "--", "sh", "-lc", j.Gate)
	} else if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", j.Gate)
		cmd.Dir = j.Root
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", j.Gate)
		cmd.Dir = j.Root
	}
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(out), true, nil
}

// watchSchedule feeds the scheduler what it needs from the event stream: the
// answers of the sessions it runs, and the end of their turns.
func (s *Server) watchSchedule(ctx context.Context) {
	_, ch, cancel := s.Hub.Subscribe(s.Hub.Seq())
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			switch e.Type {
			case gateway.EvMessage:
				var d gateway.MessageData
				if json.Unmarshal(e.Data, &d) == nil && d.Message.Role == session.RoleAssistant && strings.TrimSpace(d.Message.Text) != "" {
					if _, ok := s.Sched.ActiveJob(e.Session); ok {
						s.schedHost.mu.Lock()
						s.schedHost.last[e.Session] = d.Message.Text
						s.schedHost.mu.Unlock()
					}
				}
			case gateway.EvTurnDone:
				var d gateway.TurnData
				_ = json.Unmarshal(e.Data, &d)
				s.Sched.TurnDone(e.Session, d.Error)
			}
		}
	}
}

// jobView is a job as pages show it.
type jobView struct {
	*schedule.Job
	Describe string `json:"describe"`
}

func view(j *schedule.Job) jobView { return jobView{Job: j, Describe: j.Describe()} }

func (s *Server) scheduleRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/schedules", func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("root")
		jobs, err := s.Sched.Store.List()
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		out := []jobView{}
		for _, j := range jobs {
			if root == "" || strings.EqualFold(filepath.Clean(j.Root), filepath.Clean(root)) {
				out = append(out, view(j))
			}
		}
		writeJSON(w, map[string]any{"jobs": out})
	})

	save := func(w http.ResponseWriter, r *http.Request, id string) {
		var in schedule.Job
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if in.Kind == "" {
			in.Kind = schedule.KindTask
		}
		if in.Session == "" {
			in.Session = schedule.SessionNew
		}
		in.Root = filepath.Clean(in.Root)
		if err := in.Validate(); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if id != "" {
			old, ok := s.Sched.Store.Get(id)
			if !ok {
				fail(w, http.StatusNotFound, errors.New("no such job"))
				return
			}
			in.ID, in.State, in.Origin = id, old.State, old.Origin
			if in.Session == schedule.SessionSame && in.SessionID == "" {
				in.SessionID = old.SessionID
			}
		} else if in.Origin == "" {
			in.Origin = "web"
		}
		if err := s.Sched.Store.Put(&in); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		s.Sched.Saved(in.ID)
		j, _ := s.Sched.Store.Get(in.ID)
		writeJSON(w, view(j))
	}
	m.HandleFunc("POST /api/schedules", func(w http.ResponseWriter, r *http.Request) { save(w, r, "") })
	m.HandleFunc("PUT /api/schedules/{id}", func(w http.ResponseWriter, r *http.Request) { save(w, r, r.PathValue("id")) })

	m.HandleFunc("DELETE /api/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		j, ok := s.Sched.Store.Get(r.PathValue("id"))
		if !ok {
			fail(w, http.StatusNotFound, errors.New("no such job"))
			return
		}
		if j.State.Running != "" {
			s.schedHost.Cancel(j.State.Running)
		}
		if err := s.Sched.Store.Delete(j.ID); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		s.changed("schedule", j.Root)
		w.WriteHeader(http.StatusNoContent)
	})

	m.HandleFunc("POST /api/schedules/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Sched.RunNow(r.PathValue("id")); err != nil {
			fail(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})

	m.HandleFunc("POST /api/schedules/{id}/enable", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		j, err := s.Sched.Store.Update(r.PathValue("id"), func(j *schedule.Job) bool {
			j.Enabled = in.Enabled
			if in.Enabled {
				j.State.LastError = ""
			}
			return true
		})
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		s.Sched.Saved(j.ID)
		j, _ = s.Sched.Store.Get(j.ID)
		writeJSON(w, view(j))
	})

	m.HandleFunc("GET /api/schedules/{id}/runs", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"runs": nz(s.Sched.Store.Runs(r.PathValue("id"), 100))})
	})

	// next is the agent pacing its own job, or stopping it, from inside a
	// scheduled run.
	m.HandleFunc("POST /api/schedules/next", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Session string `json:"session"`
			In      string `json:"in"`
			Reason  string `json:"reason"`
			Stop    bool   `json:"stop"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		var d time.Duration
		if !in.Stop {
			var err error
			if d, err = schedule.ParseDuration(in.In); err != nil {
				fail(w, http.StatusBadRequest, err)
				return
			}
		}
		j, err := s.Sched.Propose(in.Session, d, in.Reason, in.Stop)
		if err != nil {
			fail(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, view(j))
	})

	m.HandleFunc("GET /api/schedules/checklist", func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("root")
		if root == "" {
			fail(w, http.StatusBadRequest, errors.New("which project?"))
			return
		}
		b, _ := os.ReadFile(schedule.ChecklistPath(root))
		writeJSON(w, map[string]string{"path": schedule.ChecklistPath(root), "text": string(b)})
	})

	m.HandleFunc("PUT /api/schedules/checklist", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root string `json:"root"`
			Text string `json:"text"`
		}
		if err := readJSON(r, &in); err != nil || in.Root == "" {
			fail(w, http.StatusBadRequest, errors.New("root and text, please"))
			return
		}
		p := schedule.ChecklistPath(in.Root)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		if err := os.WriteFile(p, []byte(in.Text), 0o644); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
