package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/phanngoc/agent-tui/internal/lsp"
)

// The editor's language servers: started per editor connection, piped both
// ways — messages out on an event stream, in by post — and stopped when the
// page that started one goes away.

// lspGrace is how long a server outlives its page's stream, for a reconnect.
const lspGrace = 20 * time.Second

func (s *Server) lspRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/lsp/start", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root   string `json:"root"`
			Server string `json:"server"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		p, err := filesOf(in.Root)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if in.Server == "" {
			in.Server = lsp.TypeScript.ID
		}
		// The first start installs the server, which takes a while.
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		sess, err := lsp.Start(ctx, lsp.Spec{Server: in.Server, Root: in.Root, FS: p.fs, Dir: p.dir})
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		s.LSP.Add(sess)
		// A server nobody connects to does not linger.
		go func() {
			time.Sleep(time.Minute)
			if !s.lspAttached(sess.ID) {
				s.LSP.Close(sess.ID)
			}
		}()
		writeJSON(w, map[string]any{"id": sess.ID, "server": sess.Server, "root_uri": sess.RootURI, "initialization_options": sess.InitOptions})
	})

	m.HandleFunc("GET /api/lsp/check", func(w http.ResponseWriter, r *http.Request) {
		p, err := filesOf(r.URL.Query().Get("root"))
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if err := lsp.Check(ctx, lsp.Spec{Server: lsp.TypeScript.ID, Root: r.URL.Query().Get("root"), FS: p.fs, Dir: p.dir}); err != nil {
			writeJSON(w, map[string]any{"ok": false, "reason": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	m.HandleFunc("/api/lsp/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.LSP.Get(r.PathValue("id"))
		if !ok {
			fail(w, http.StatusNotFound, errors.New("no such language server"))
			return
		}
		fl, ok := w.(http.Flusher)
		if !ok {
			fail(w, http.StatusInternalServerError, errors.New("no streaming"))
			return
		}
		s.lspAttach(sess.ID, 1)
		defer func() {
			s.lspAttach(sess.ID, -1)
			go func() {
				time.Sleep(lspGrace)
				if !s.lspAttached(sess.ID) {
					s.LSP.Close(sess.ID)
				}
			}()
		}()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		_, _ = io.WriteString(w, ": connected\n\n")
		fl.Flush()
		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ping.C:
				_, _ = io.WriteString(w, ": ping\n\n")
				fl.Flush()
			case msg, ok := <-sess.Messages():
				if !ok {
					fmt.Fprintf(w, "event: exit\ndata: %s\n\n", jsonString(sess.Err()))
					fl.Flush()
					return
				}
				// One message, one line: an event's data field ends at a
				// newline, and a server is free to pretty-print.
				var one bytes.Buffer
				if json.Compact(&one, msg) == nil {
					msg = one.Bytes()
				}
				fmt.Fprintf(w, "data: %s\n\n", msg)
				fl.Flush()
			}
		}
	})

	m.HandleFunc("POST /api/lsp/{id}/send", func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.LSP.Get(r.PathValue("id"))
		if !ok {
			fail(w, http.StatusNotFound, errors.New("no such language server"))
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if err := sess.Send(body); err != nil {
			fail(w, http.StatusGone, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	m.HandleFunc("DELETE /api/lsp/{id}", func(w http.ResponseWriter, r *http.Request) {
		s.LSP.Close(r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) lspAttach(id string, d int) {
	s.lspMu.Lock()
	s.lspPages[id] += d
	if s.lspPages[id] <= 0 {
		delete(s.lspPages, id)
	}
	s.lspMu.Unlock()
}

func (s *Server) lspAttached(id string) bool {
	s.lspMu.Lock()
	defer s.lspMu.Unlock()
	return s.lspPages[id] > 0
}

func jsonString(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}
