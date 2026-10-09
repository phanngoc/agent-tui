package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/session"
)

// maxRefs is how many links one conversation may have pinned.
const maxRefs = 50

func (s *Server) boardRoutes(m *http.ServeMux) {
	// GET /api/sessions/{id}/refs is where a conversation came from and what
	// it pointed at: the links pinned to it, then every link said in it,
	// first said first, each with who said it and the words around it.
	m.HandleFunc("GET /api/sessions/{id}/refs", func(w http.ResponseWriter, r *http.Request) {
		sess, err := gateway.Load(r.PathValue("id"))
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		type pinned struct {
			session.Ref
			Kind  string `json:"kind"`
			Label string `json:"label"`
		}
		pins := []pinned{}
		for _, ref := range sess.Refs {
			kind, label := session.Classify(ref.URL)
			pins = append(pins, pinned{Ref: ref, Kind: kind, Label: label})
		}
		found := sess.Links()
		if found == nil {
			found = []session.Link{}
		}
		var origin *session.Link
		if o, ok := sess.Origin(); ok {
			origin = &o
		}
		writeJSON(w, map[string]any{"pinned": pins, "found": found, "origin": origin})
	})
}

// cleanRefs checks pinned links: web addresses only, each once, titled in a
// line, no more than maxRefs. A link with no time is stamped now.
func cleanRefs(in []session.Ref) ([]session.Ref, error) {
	if len(in) > maxRefs {
		return nil, fmt.Errorf("at most %d links", maxRefs)
	}
	out := make([]session.Ref, 0, len(in))
	seen := map[string]bool{}
	for _, r := range in {
		r.URL = strings.TrimSpace(r.URL)
		u, err := url.Parse(r.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, errors.New("not a web link: " + r.URL)
		}
		if seen[r.URL] {
			continue
		}
		seen[r.URL] = true
		r.Title = strings.Join(strings.Fields(r.Title), " ")
		if len([]rune(r.Title)) > 200 {
			return nil, errors.New("a link's title is at most 200 characters")
		}
		if r.Added.IsZero() {
			r.Added = time.Now().UTC()
		}
		out = append(out, r)
	}
	return out, nil
}
