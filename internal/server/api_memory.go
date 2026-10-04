package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/claudecode"
	"github.com/phanngoc/agent-tui/internal/learn"
	"github.com/phanngoc/agent-tui/internal/memory"
)

func (s *Server) memoryRoutes(m *http.ServeMux) {
	store := func(r *http.Request, root string, scope memory.Scope) (*memory.Store, error) {
		st := memory.For(root).Store(scope)
		if st == nil {
			return nil, errors.New("pick a project for project memory")
		}
		return st, nil
	}

	// GET /api/memory?root=&scope=&type=&q= lists records; with q they are
	// ranked as recall would rank them, and carry their score.
	m.HandleFunc("GET /api/memory", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		bank := memory.For(q.Get("root"))
		var recs []memory.Record
		for _, st := range bank.Stores() {
			if sc := q.Get("scope"); sc != "" && string(st.Scope) != sc {
				continue
			}
			recs = append(recs, st.All()...)
		}
		if t := q.Get("type"); t != "" {
			kept := recs[:0]
			for _, r := range recs {
				if r.Type == t {
					kept = append(kept, r)
				}
			}
			recs = kept
		}
		if sess := q.Get("session"); sess != "" {
			kept := recs[:0]
			for _, r := range recs {
				if r.Session == sess {
					kept = append(kept, r)
				}
			}
			recs = kept
		}
		hits := []memory.Hit{}
		if text := strings.TrimSpace(q.Get("q")); text != "" {
			hits = append(hits, memory.Search(recs, text, 200)...)
		} else {
			for _, r := range recs {
				hits = append(hits, memory.Hit{Record: r})
			}
		}
		stats := []memory.Stats{}
		for _, st := range bank.Stores() {
			stats = append(stats, st.Stats())
		}
		writeJSON(w, map[string]any{"hits": hits, "stats": stats, "types": memory.Types})
	})

	m.HandleFunc("PUT /api/memory", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root   string        `json:"root"`
			Record memory.Record `json:"record"`
			// From is the scope a record is moving out of.
			From memory.Scope `json:"from"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		st, err := store(r, in.Root, in.Record.Scope)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if in.Record.Origin == "" {
			in.Record.Origin = "manual"
		}
		if in.From != "" && in.From != in.Record.Scope {
			if old, err := store(r, in.Root, in.From); err == nil {
				_, _ = old.Delete(in.Record.ID)
			}
		}
		rec, err := st.Put(in.Record)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		s.changed("memory", in.Root)
		writeJSON(w, rec)
	})

	m.HandleFunc("DELETE /api/memory", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		st, err := store(r, q.Get("root"), memory.Scope(q.Get("scope")))
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		ids := strings.Split(q.Get("id"), ",")
		n, err := st.Delete(ids...)
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		s.changed("memory", q.Get("root"))
		writeJSON(w, map[string]int{"deleted": n})
	})

	// The write log: every store, update, merge and delete, with what it
	// replaced — how a record came to say what it says.
	m.HandleFunc("GET /api/memory/log", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		id := q.Get("id")
		out := []memory.LogEntry{}
		for _, st := range memory.For(q.Get("root")).Stores() {
			if sc := q.Get("scope"); sc != "" && string(st.Scope) != sc {
				continue
			}
			for _, e := range st.Log(2000) {
				if id != "" && e.Record.ID != id && !contains(e.Targets, id) {
					continue
				}
				e.Record.Scope = st.Scope
				out = append(out, e)
			}
		}
		if len(out) > 300 {
			out = out[:300]
		}
		writeJSON(w, out)
	})

	m.HandleFunc("GET /api/memory/scenes", func(w http.ResponseWriter, r *http.Request) {
		type scope struct {
			Scope   memory.Scope   `json:"scope"`
			Persona string         `json:"persona"`
			Scenes  []memory.Scene `json:"scenes"`
			Dir     string         `json:"dir"`
		}
		out := []scope{}
		for _, st := range memory.For(r.URL.Query().Get("root")).Stores() {
			out = append(out, scope{Scope: st.Scope, Persona: st.Persona(), Scenes: nz(st.Scenes()), Dir: st.Dir})
		}
		writeJSON(w, out)
	})

	m.HandleFunc("PUT /api/memory/scenes", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root  string       `json:"root"`
			Scope memory.Scope `json:"scope"`
			Old   string       `json:"old_file"`
			Scene memory.Scene `json:"scene"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		st, err := store(r, in.Root, in.Scope)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		sc, err := st.PutScene(in.Scene)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if in.Old != "" && in.Old != sc.File {
			_ = st.DeleteScene(in.Old)
		}
		s.changed("memory", in.Root)
		writeJSON(w, sc)
	})

	m.HandleFunc("DELETE /api/memory/scenes", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		st, err := store(r, q.Get("root"), memory.Scope(q.Get("scope")))
		if err == nil {
			err = st.DeleteScene(q.Get("file"))
		}
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		s.changed("memory", q.Get("root"))
		w.WriteHeader(http.StatusNoContent)
	})

	m.HandleFunc("PUT /api/memory/persona", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root  string       `json:"root"`
			Scope memory.Scope `json:"scope"`
			Text  string       `json:"text"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		st, err := store(r, in.Root, in.Scope)
		if err == nil {
			err = st.SetPersona(in.Text)
		}
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		s.changed("memory", in.Root)
		w.WriteHeader(http.StatusNoContent)
	})

	m.HandleFunc("POST /api/memory/consolidate", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root string `json:"root"`
		}
		_ = readJSON(r, &in)
		ctx, cancel := context.WithTimeout(r.Context(), 9*time.Minute)
		defer cancel()
		if err := learn.Default().Consolidate(ctx, in.Root); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		s.changed("memory", in.Root)
		writeJSON(w, map[string]any{"ok": true})
	})

	m.HandleFunc("GET /api/learn", func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("root")
		acts := learn.Default().Activities(300)
		if root != "" {
			kept := acts[:0]
			for _, a := range acts {
				if a.Root == "" || a.Root == root {
					kept = append(kept, a)
				}
			}
			acts = kept
		}
		writeJSON(w, map[string]any{"status": learn.Default().Status(), "activity": acts,
			"enabled": root == "" || learn.Enabled(root),
			"knobs": map[string]any{
				"every_n_turns": learn.EveryN, "idle_minutes": learn.IdleAfter.Minutes(),
				"scene_interval_minutes": learn.SceneInterval.Minutes(), "skill_tool_calls": learn.SkillToolCalls,
				"recall_limit": memory.RecallLimit, "recall_threshold": memory.RecallThreshold,
			}})
	})

	// Import Claude Code memory notes as records.
	m.HandleFunc("POST /api/memory/import", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Install string       `json:"install"`
			Files   []string     `json:"files"` // project/file
			Scope   memory.Scope `json:"scope"`
			Root    string       `json:"root"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		inst, ok := claudecode.Find(r.Context(), in.Install)
		if !ok {
			fail(w, http.StatusNotFound, errors.New("no such Claude Code install"))
			return
		}
		st, err := store(r, in.Root, in.Scope)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		n := 0
		for _, f := range inst.Memory() {
			if !contains(in.Files, f.Project+"/"+f.File) {
				continue
			}
			typ := memory.TypeWorkFact
			switch f.Type {
			case "user":
				typ = memory.TypePersona
			case "feedback":
				typ = memory.TypeInstruction
			case "reference":
				typ = memory.TypeWorkArtifact
			}
			content := strings.TrimSpace(f.Description)
			if f.Body != "" {
				content = strings.TrimSpace(content + "\n" + f.Body)
			}
			if _, err := st.Put(memory.Record{Content: content, Type: typ, Priority: 80, Origin: "import",
				Scene: f.Name, Metadata: map[string]any{"source": in.Install + ":" + f.Project + "/" + f.File}}); err == nil {
				n++
			}
		}
		s.changed("memory", in.Root)
		writeJSON(w, map[string]int{"imported": n})
	})
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
