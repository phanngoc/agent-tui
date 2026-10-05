package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/phanngoc/agent-tui/internal/gateway"
)

// gatewayRoutes are what a terminal app talks to as a peer.
func (s *Server) gatewayRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/gateway/peers", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Kind string `json:"kind"`
			Root string `json:"root"`
			PID  int    `json:"pid"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		p := s.Hub.Join(in.Kind, in.Root, in.PID)
		writeJSON(w, map[string]string{"id": p.ID})
	})

	// Stopping is asked for, not signalled: on Windows there is no signal one
	// process can send another's console-less process short of killing it,
	// and a kill leaves turns half-saved. Turns the gateway is running would
	// be cancelled, so they have to be named and overridden.
	m.HandleFunc("POST /api/gateway/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if busy := s.Runner.Running(); len(busy) > 0 && r.URL.Query().Get("force") != "1" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "turns are running", "busy": busy})
			return
		}
		s.restart.Store(r.URL.Query().Get("restart") == "1")
		if r.URL.Query().Get("hold") == "1" {
			_ = gateway.MarkStopped()
		}
		writeJSON(w, map[string]bool{"ok": true})
		go func() {
			time.Sleep(100 * time.Millisecond)
			s.Stop()
		}()
	})

	m.HandleFunc("GET /api/gateway/peers", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.Hub.Peers())
	})

	m.HandleFunc("GET /api/gateway/peers/{id}/commands", func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.Hub.Peer(r.PathValue("id"))
		if !ok {
			fail(w, http.StatusNotFound, errors.New("unknown peer"))
			return
		}
		fl, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, ": connected\n\n")
		fl.Flush()
		cmds := s.Hub.Attach(p)
		defer s.Hub.Detach(p)
		ping := time.NewTicker(15 * time.Second)
		defer ping.Stop()
		for {
			select {
			case c := <-cmds:
				b, _ := json.Marshal(c)
				if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
					// Put it back for the reconnect.
					select {
					case cmds <- c:
					default:
					}
					return
				}
				fl.Flush()
			case <-ping.C:
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					return
				}
				fl.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})

	m.HandleFunc("POST /api/gateway/peers/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.Hub.Peer(r.PathValue("id"))
		if !ok {
			fail(w, http.StatusNotFound, errors.New("unknown peer"))
			return
		}
		var evs []gateway.Event
		if err := readJSON(r, &evs); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		for _, e := range evs {
			e.Origin = p.ID
			e.Seq = 0
			s.Hub.Publish(e)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	m.HandleFunc("POST /api/gateway/peers/{id}/sessions", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Sessions []string `json:"sessions"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if _, ok := s.Hub.Peer(r.PathValue("id")); !ok {
			fail(w, http.StatusNotFound, errors.New("unknown peer"))
			return
		}
		s.Hub.Hold(r.PathValue("id"), in.Sessions)
		w.WriteHeader(http.StatusNoContent)
	})
}
