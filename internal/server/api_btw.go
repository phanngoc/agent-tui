package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/session"
)

// /btw from the web: a question asked beside a conversation, in its side
// chat, while the conversation's own turn goes on untouched — the terminal's
// /btw, and Claude Code's.

// sideOf is the side chat a conversation has now, or "".
func (s *Server) sideOf(id string) string {
	for _, sum := range s.summaries() {
		if sum.SideOf == id && !sum.Closed {
			return sum.ID
		}
	}
	return ""
}

// sideFor is the side chat to ask in. One the conversation has moved on
// from was forked from less than there is now, and would answer about a
// conversation that no longer is: it goes to the trash and a fresh fork takes
// its place — unless it is still answering, when the question waits for it.
func (s *Server) sideFor(id string) (string, error) {
	parent, err := gateway.Load(id)
	if err != nil {
		return "", err
	}
	if parent.SideOf != "" {
		return "", errors.New("this is a side chat already: ask here directly")
	}
	if cur := s.sideOf(id); cur != "" {
		side, err := gateway.Load(cur)
		if err == nil && (side.SideFrom >= len(parent.Messages) || s.Hub.Busy(cur)) {
			return cur, nil
		}
		_, _ = s.Hub.Route(gateway.Command{Type: gateway.CmdDelete, Session: cur, From: "web"})
	}
	f, err := s.Runner.Side(id)
	if err != nil {
		return "", err
	}
	return f.ID, nil
}

func (s *Server) btwRoutes(m *http.ServeMux) {
	// The question, with any images, goes to the side chat; with none, the
	// side chat is only made ready. The answer streams as that session's
	// events.
	m.HandleFunc("POST /api/sessions/{id}/btw", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text  string               `json:"text"`
			Files []session.Attachment `json:"files"`
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
		side, err := s.sideFor(r.PathValue("id"))
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		out := map[string]string{"id": side}
		if strings.TrimSpace(in.Text) != "" || len(files) > 0 {
			owner, err := s.Hub.Route(gateway.Command{Type: gateway.CmdPrompt, Session: side, Text: in.Text, Files: files, From: "web"})
			if err != nil {
				fail(w, http.StatusConflict, err)
				return
			}
			out["owner"] = owner
		}
		writeJSON(w, out)
	})
	// Closing the side chat for good: the next /btw starts a fresh one.
	m.HandleFunc("DELETE /api/sessions/{id}/btw", func(w http.ResponseWriter, r *http.Request) {
		if cur := s.sideOf(r.PathValue("id")); cur != "" {
			if _, err := s.Hub.Route(gateway.Command{Type: gateway.CmdDelete, Session: cur, From: "web"}); err != nil {
				fail(w, http.StatusConflict, err)
				return
			}
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
}
