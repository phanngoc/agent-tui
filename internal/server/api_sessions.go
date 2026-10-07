package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/kit"
	"github.com/phanngoc/agent-tui/internal/learn"
	"github.com/phanngoc/agent-tui/internal/session"
)

func (s *Server) sessionRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("root")
		q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
		out := []gateway.Summary{}
		for _, sum := range s.summaries() {
			if root != "" && sum.Root != root {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(sum.Title+" "+sum.Root+" "+sum.ID), q) {
				continue
			}
			out = append(out, sum)
		}
		writeJSON(w, out)
	})

	m.HandleFunc("GET /api/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		sess, err := gateway.Load(id)
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		sum := gateway.SummaryOf(sess)
		sum.Owner = s.Hub.Owner(id)
		live := s.Hub.LiveAll()[id]
		writeJSON(w, map[string]any{
			"summary":  sum,
			"session":  sess,
			"live":     live,
			"traces":   kit.Traces(id),
			"learning": learn.Default().Status().Sessions[id],
			"queue":    s.Runner.Queue(id),
		})
	})

	// unqueue takes back a message queued for a running turn, to edit or
	// drop: its text comes back.
	m.HandleFunc("POST /api/sessions/{id}/unqueue", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Item string `json:"item"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		text, ok := s.Runner.Unqueue(r.PathValue("id"), in.Item)
		if !ok {
			fail(w, http.StatusNotFound, errors.New("already sent"))
			return
		}
		writeJSON(w, map[string]string{"text": text})
	})

	m.HandleFunc("GET /api/sessions/{id}/trace", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, kit.Traces(r.PathValue("id")))
	})

	m.HandleFunc("POST /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root   string `json:"root"`
			Target string `json:"target"`
			CWD    string `json:"cwd"`
			Engine string `json:"engine"`
			Model  string `json:"model"`
			Mode   string `json:"mode"`
			Prompt string `json:"prompt"`
			// Worktree, when set, runs the conversation in a new git
			// worktree of the project, on a new branch from Base (the
			// current branch when empty), as the Claude app's worktree
			// option does: the main tree is left as it is.
			Worktree *struct {
				Base string `json:"base"`
			} `json:"worktree"`
			Files []session.Attachment `json:"files"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		files, err := gateway.CheckAttachments(in.Files)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(in.Prompt) == "" && len(files) == 0 {
			fail(w, http.StatusBadRequest, errors.New("a new conversation starts with a prompt"))
			return
		}
		branch := ""
		if in.Worktree != nil {
			branch = worktreeBranch(in.Prompt, time.Now())
			wt, err := s.worktreeFor(r.Context(), in.Root, branch, in.Worktree.Base, true)
			if err != nil {
				fail(w, http.StatusBadRequest, fmt.Errorf("making the worktree: %w", err))
				return
			}
			in.CWD = wt
		}
		sess, err := s.Runner.NewSession(in.Root, in.Target, in.CWD, in.Engine, in.Model, in.Mode, in.Prompt, files...)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]string{"id": sess.ID, "branch": branch, "cwd": in.CWD})
	})

	route := func(typ string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Text    string               `json:"text"`
				ID      string               `json:"id"`
				Verdict string               `json:"verdict"`
				Index   int                  `json:"index"`
				Now     bool                 `json:"now"`
				Files   []session.Attachment `json:"files"`
			}
			if r.ContentLength != 0 {
				if err := readJSON(r, &in); err != nil {
					fail(w, http.StatusBadRequest, err)
					return
				}
			}
			files, err := gateway.CheckAttachments(in.Files)
			if err != nil {
				fail(w, http.StatusBadRequest, err)
				return
			}
			cmd := gateway.Command{Type: typ, Session: r.PathValue("id"), Text: in.Text,
				ID: in.ID, Verdict: in.Verdict, Index: in.Index, Now: in.Now, Files: files, From: "web"}
			// Send now with nothing typed sends what is already queued.
			if typ == gateway.CmdPrompt && strings.TrimSpace(cmd.Text) == "" && !cmd.Now && len(files) == 0 {
				fail(w, http.StatusBadRequest, errors.New("empty prompt"))
				return
			}
			owner, err := s.Hub.Route(cmd)
			if err != nil {
				fail(w, http.StatusConflict, err)
				return
			}
			writeJSON(w, map[string]string{"owner": owner})
		}
	}
	// settings changes a session's model, mode or engine from its next turn,
	// wherever it is held: a terminal holding it applies it as if chosen
	// there.
	m.HandleFunc("PUT /api/sessions/{id}/settings", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Model  string `json:"model"`
			Mode   string `json:"mode"`
			Engine string `json:"engine"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if in.Model != "" {
			spec, ok := agent.ResolveModel(in.Model)
			if !ok {
				fail(w, http.StatusBadRequest, errors.New("no model called "+in.Model))
				return
			}
			in.Model = spec.ID
		}
		owner, err := s.Hub.Route(gateway.Command{Type: gateway.CmdSettings, Session: r.PathValue("id"),
			Model: in.Model, Mode: in.Mode, Engine: in.Engine, From: "web"})
		if err != nil {
			fail(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, map[string]string{"owner": owner})
	})
	m.HandleFunc("POST /api/sessions/{id}/prompt", route(gateway.CmdPrompt))
	m.HandleFunc("POST /api/sessions/{id}/cancel", route(gateway.CmdCancel))
	m.HandleFunc("POST /api/sessions/{id}/approve", route(gateway.CmdApprove))
	m.HandleFunc("POST /api/sessions/{id}/choose", route(gateway.CmdChoose))

	m.HandleFunc("POST /api/sessions/{id}/learn", func(w http.ResponseWriter, r *http.Request) {
		sess, err := gateway.Load(r.PathValue("id"))
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 9*time.Minute)
		defer cancel()
		if err := learn.Default().LearnNow(ctx, sess.Root, sess.ID, sess.Messages); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "activity": learn.Default().Activities(10)})
	})

	m.HandleFunc("GET /api/engines", func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("root")
		if root == "" {
			fail(w, http.StatusBadRequest, errors.New("root is required"))
			return
		}
		models := []map[string]any{}
		for _, m := range agent.Models {
			models = append(models, map[string]any{"id": m.ID, "label": m.Label})
		}
		writeJSON(w, map[string]any{
			"engines": s.Runner.Engines(root),
			"models":  models,
			"modes":   []string{"plan", "ask", "auto", "full"},
		})
	})
}

// worktreeBranch names a conversation's worktree branch from its first
// prompt: agent/<month-day>-<a few words>.
func worktreeBranch(prompt string, now time.Time) string {
	var words []string
	for _, f := range strings.Fields(strings.ToLower(prompt)) {
		var b strings.Builder
		for _, r := range f {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		if b.Len() > 0 {
			words = append(words, b.String())
		}
		if len(words) == 4 {
			break
		}
	}
	name := "agent/" + now.Format("0102-1504")
	if len(words) > 0 {
		name += "-" + strings.Join(words, "-")
	}
	if len(name) > 60 {
		name = strings.TrimRight(name[:60], "-")
	}
	return name
}
