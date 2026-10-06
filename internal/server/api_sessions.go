package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/kit"
	"github.com/phanngoc/agent-tui/internal/learn"
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
		})
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
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(in.Prompt) == "" {
			fail(w, http.StatusBadRequest, errors.New("a new conversation starts with a prompt"))
			return
		}
		sess, err := s.Runner.NewSession(in.Root, in.Target, in.CWD, in.Engine, in.Model, in.Mode, in.Prompt)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]string{"id": sess.ID})
	})

	route := func(typ string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Text    string `json:"text"`
				ID      string `json:"id"`
				Verdict string `json:"verdict"`
				Index   int    `json:"index"`
			}
			if r.ContentLength != 0 {
				if err := readJSON(r, &in); err != nil {
					fail(w, http.StatusBadRequest, err)
					return
				}
			}
			cmd := gateway.Command{Type: typ, Session: r.PathValue("id"), Text: in.Text,
				ID: in.ID, Verdict: in.Verdict, Index: in.Index, From: "web"}
			if typ == gateway.CmdPrompt && strings.TrimSpace(cmd.Text) == "" {
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
