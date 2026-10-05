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
	// store finds the store a write means: by its folder (dir) when the page
	// knows it — a record listed from every project carries its own — or by
	// project and scope.
	store := func(r *http.Request, root string, scope memory.Scope) (*memory.Store, error) {
		if dir := r.URL.Query().Get("dir"); dir != "" {
			if st, ok := memory.AtDir(dir); ok {
				return st, nil
			}
			return nil, errors.New("no memory is kept in " + dir)
		}
		st := memory.For(root).Store(scope)
		if st == nil {
			return nil, errors.New("pick a project for project memory")
		}
		return st, nil
	}

	type hit struct {
		memory.Hit
		Project     string `json:"project,omitempty"`
		ProjectName string `json:"project_name"`
		Dir         string `json:"dir"`
	}
	type stats struct {
		memory.Stats
		Project     string `json:"project,omitempty"`
		ProjectName string `json:"project_name"`
	}

	// GET /api/memory?root=&all=&scope=&type=&q= lists records — of the
	// global store and a project's, or with all (or no project picked) of
	// every project — each with the project it belongs to. With q they are
	// ranked as recall would rank them, and carry their score.
	m.HandleFunc("GET /api/memory", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		root := q.Get("root")
		refs := s.memoryStores(root, root == "" || q.Get("all") == "1")
		var recs []memory.Record
		owner := map[string]storeRef{}
		st := []stats{}
		for _, ref := range refs {
			st = append(st, stats{Stats: ref.Store.Stats(), Project: ref.Root, ProjectName: ref.Name})
			if sc := q.Get("scope"); sc != "" && string(ref.Store.Scope) != sc {
				continue
			}
			for _, rec := range ref.Store.All() {
				owner[ref.Store.Dir+"|"+rec.ID] = ref
				recs = append(recs, rec)
			}
		}
		keep := func(f func(memory.Record) bool) {
			kept := recs[:0]
			for _, r := range recs {
				if f(r) {
					kept = append(kept, r)
				}
			}
			recs = kept
		}
		if t := q.Get("type"); t != "" {
			keep(func(r memory.Record) bool { return r.Type == t })
		}
		if sess := q.Get("session"); sess != "" {
			keep(func(r memory.Record) bool { return r.Session == sess })
		}
		var found []memory.Hit
		if text := strings.TrimSpace(q.Get("q")); text != "" {
			found = memory.Search(recs, text, 200)
		} else {
			for _, r := range recs {
				found = append(found, memory.Hit{Record: r})
			}
		}
		// Records keep no note of their store; find it again by id, which
		// is unique, scope by scope.
		dirOf := func(rec memory.Record) storeRef {
			for _, ref := range refs {
				if ref.Store.Scope != rec.Scope {
					continue
				}
				if o, ok := owner[ref.Store.Dir+"|"+rec.ID]; ok {
					return o
				}
			}
			return storeRef{}
		}
		hits := []hit{}
		for _, h := range found {
			ref := dirOf(h.Record)
			dir := ""
			if ref.Store != nil {
				dir = ref.Store.Dir
			}
			hits = append(hits, hit{Hit: h, Project: ref.Root, ProjectName: ref.Name, Dir: dir})
		}
		writeJSON(w, map[string]any{"hits": hits, "stats": st, "types": memory.Types, "all": root == "" || q.Get("all") == "1"})
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
		stores := memory.For(q.Get("root")).Stores()
		if dir := q.Get("dir"); dir != "" {
			st, ok := memory.AtDir(dir)
			if !ok {
				fail(w, http.StatusBadRequest, errors.New("no memory is kept in "+dir))
				return
			}
			stores = []*memory.Store{st}
		}
		for _, st := range stores {
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
			Scope       memory.Scope   `json:"scope"`
			Persona     string         `json:"persona"`
			PersonaPrev string         `json:"persona_prev,omitempty"`
			Scenes      []memory.Scene `json:"scenes"`
			Dir         string         `json:"dir"`
			Project     string         `json:"project,omitempty"`
			ProjectName string         `json:"project_name"`
			Records     int            `json:"records"`
		}
		root := r.URL.Query().Get("root")
		out := []scope{}
		for _, ref := range s.memoryStores(root, root == "") {
			st := ref.Store
			out = append(out, scope{Scope: st.Scope, Persona: st.Persona(), PersonaPrev: st.PersonaPrev(), Scenes: nz(st.Scenes()), Dir: st.Dir,
				Project: ref.Root, ProjectName: ref.Name, Records: len(st.All())})
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

	// consolidate rebuilds scenes and personas now, on what the page shows:
	// the global store and the project's, or every store when no project is
	// picked. It answers with what each store came out with.
	m.HandleFunc("POST /api/memory/consolidate", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root string `json:"root"`
			All  bool   `json:"all"`
		}
		_ = readJSON(r, &in)
		refs := s.memoryStores(in.Root, in.Root == "" || in.All)
		stores := make([]*memory.Store, 0, len(refs))
		names := map[string]storeRef{}
		for _, ref := range refs {
			stores = append(stores, ref.Store)
			names[ref.Store.Dir] = ref
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
		defer cancel()
		reports, err := learn.Default().Consolidate(ctx, stores)
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		type report struct {
			learn.StoreReport
			Project     string `json:"project,omitempty"`
			ProjectName string `json:"project_name"`
		}
		out := []report{}
		for _, rep := range reports {
			ref := names[rep.Dir]
			out = append(out, report{StoreReport: rep, Project: ref.Root, ProjectName: ref.Name})
		}
		s.changed("memory", in.Root)
		writeJSON(w, map[string]any{"reports": out})
	})

	m.HandleFunc("GET /api/learn", func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("root")
		acts := learn.Default().Activities(300)
		// Each store by its folder, so an entry can say which project it
		// was about and link to what it wrote.
		type storeInfo struct {
			Name  string `json:"name"`
			Root  string `json:"root,omitempty"`
			Scope string `json:"scope"`
		}
		stores := map[string]storeInfo{}
		for _, ref := range s.memoryStores("", true) {
			stores[ref.Store.Dir] = storeInfo{Name: ref.Name, Root: ref.Root, Scope: string(ref.Store.Scope)}
		}
		if root != "" {
			kept := acts[:0]
			for _, a := range acts {
				info, known := stores[a.Dir]
				switch {
				case a.Dir != "" && known && info.Scope == "project" && info.Root != root:
				case a.Root != "" && a.Root != root:
				default:
					kept = append(kept, a)
				}
			}
			acts = kept
		}
		writeJSON(w, map[string]any{"status": learn.Default().Status(), "activity": acts, "stores": stores,
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
