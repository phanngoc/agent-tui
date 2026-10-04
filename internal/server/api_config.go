package server

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/claudecode"
	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/kit"
	"github.com/phanngoc/agent-tui/internal/learn"
	"github.com/phanngoc/agent-tui/internal/mcp"
	"github.com/phanngoc/agent-tui/internal/skill"
)

// --- skills ---

func (s *Server) skillRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/skills", func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("root")
		list := skill.For(root).List()
		off := config.LoadProjectSettings(root).DisabledSkills
		for i := range list {
			list[i].Disabled = root != "" && config.Disabled(off, list[i].Name)
		}
		if list == nil {
			list = []skill.Skill{}
		}
		writeJSON(w, map[string]any{
			"skills":      list,
			"global_dir":  skill.GlobalDir(),
			"project_dir": skill.ProjectDirFor(root),
		})
	})

	m.HandleFunc("PUT /api/skills", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root     string      `json:"root"`
			OldName  string      `json:"old_name"`
			OldScope skill.Scope `json:"old_scope"`
			Skill    skill.Skill `json:"skill"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		st := skill.For(in.Root)
		saved, err := st.Save(in.Skill)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		// A rename or a move between scopes leaves no copy behind.
		if in.OldName != "" && (in.OldName != saved.Name || (in.OldScope != "" && in.OldScope != saved.Scope)) {
			scope := in.OldScope
			if scope == "" {
				scope = saved.Scope
			}
			_ = st.Delete(scope, in.OldName)
		}
		s.changed("skills", in.Root)
		writeJSON(w, saved)
	})

	m.HandleFunc("DELETE /api/skills", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if err := skill.For(q.Get("root")).Delete(skill.Scope(q.Get("scope")), q.Get("name")); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		s.changed("skills", q.Get("root"))
		w.WriteHeader(http.StatusNoContent)
	})

	// toggle switches a skill or MCP server off (or on) for one project.
	m.HandleFunc("POST /api/project/toggle", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root    string `json:"root"`
			Kind    string `json:"kind"` // skill|mcp
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		}
		if err := readJSON(r, &in); err != nil || in.Root == "" {
			fail(w, http.StatusBadRequest, errors.New("root, kind and name are required"))
			return
		}
		ps := config.LoadProjectSettings(in.Root)
		list := &ps.DisabledSkills
		if in.Kind == "mcp" {
			list = &ps.DisabledMCP
		}
		out := (*list)[:0:0]
		for _, n := range *list {
			if n != in.Name {
				out = append(out, n)
			}
		}
		if !in.Enabled {
			out = append(out, in.Name)
		}
		*list = out
		if err := config.SaveProjectSettings(in.Root, ps); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		s.changed(in.Kind, in.Root)
		writeJSON(w, ps)
	})

	m.HandleFunc("GET /api/claude/installs", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		type install struct {
			claudecode.Install
			Skills  []claudecode.SkillRef   `json:"skills"`
			Servers []claudecode.Server     `json:"servers"`
			Memory  []claudecode.MemoryFile `json:"memory"`
		}
		out := []install{}
		for _, in := range claudecode.Installs(ctx) {
			out = append(out, install{Install: in, Skills: nz(in.Skills()), Servers: nz(in.Servers()), Memory: nz(in.Memory())})
		}
		writeJSON(w, out)
	})

	m.HandleFunc("POST /api/skills/import", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Install string      `json:"install"`
			Name    string      `json:"name"`
			As      string      `json:"as"`
			Scope   skill.Scope `json:"scope"`
			Root    string      `json:"root"`
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
		for _, ref := range inst.Skills() {
			if ref.Name != in.Name {
				continue
			}
			name := in.As
			if name == "" {
				name = filepath.Base(ref.Dir)
			}
			sk, err := skill.For(in.Root).Copy(ref.Dir, in.Scope, name)
			if err != nil {
				fail(w, http.StatusBadRequest, err)
				return
			}
			s.changed("skills", in.Root)
			writeJSON(w, sk)
			return
		}
		fail(w, http.StatusNotFound, errors.New("no such skill in that install"))
	})
}

func nz[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

// --- MCP ---

func (s *Server) mcpRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/mcp", func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("root")
		st := kit.MCPStore(root)
		off := config.LoadProjectSettings(root).DisabledMCP
		type row struct {
			mcp.Server
			Off bool `json:"off"`
		}
		out := []row{}
		for _, sv := range st.List() {
			out = append(out, row{Server: sv, Off: root != "" && config.Disabled(off, sv.Name)})
		}
		writeJSON(w, map[string]any{"servers": out, "global_path": st.GlobalPath, "project_path": st.ProjectPath})
	})

	m.HandleFunc("PUT /api/mcp", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root     string     `json:"root"`
			OldName  string     `json:"old_name"`
			OldScope mcp.Scope  `json:"old_scope"`
			Server   mcp.Server `json:"server"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		st := kit.MCPStore(in.Root)
		if err := st.Save(in.Server); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if in.OldName != "" && (in.OldName != in.Server.Name || (in.OldScope != "" && in.OldScope != in.Server.Scope)) {
			scope := in.OldScope
			if scope == "" {
				scope = in.Server.Scope
			}
			_ = st.Delete(scope, in.OldName)
		}
		kit.Pool.Forget(in.Server)
		s.changed("mcp", in.Root)
		writeJSON(w, in.Server)
	})

	m.HandleFunc("DELETE /api/mcp", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if err := kit.MCPStore(q.Get("root")).Delete(mcp.Scope(q.Get("scope")), q.Get("name")); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		s.changed("mcp", q.Get("root"))
		w.WriteHeader(http.StatusNoContent)
	})

	// test connects to one definition — saved or not — and lists its tools.
	m.HandleFunc("POST /api/mcp/test", func(w http.ResponseWriter, r *http.Request) {
		var in mcp.Server
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if err := in.Validate(); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		kit.Pool.Forget(in)
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		writeJSON(w, kit.Pool.Statuses(ctx, []mcp.Server{in})[0])
	})

	m.HandleFunc("GET /api/mcp/status", func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("root")
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		active := kit.MCPStore(root).Active(config.LoadProjectSettings(root).DisabledMCP)
		writeJSON(w, kit.Pool.Statuses(ctx, active))
	})

	m.HandleFunc("POST /api/mcp/import", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Install string    `json:"install"`
			Name    string    `json:"name"`
			Project string    `json:"project"`
			Scope   mcp.Scope `json:"scope"`
			Root    string    `json:"root"`
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
		for _, sv := range inst.Servers() {
			if sv.Name != in.Name || sv.Project != in.Project {
				continue
			}
			def := mcp.Server{Name: sv.Name, Type: sv.Type, Command: sv.Command, Args: sv.Args,
				Env: sv.Env, URL: sv.URL, Headers: sv.Headers, Scope: in.Scope}
			if err := kit.MCPStore(in.Root).Save(def); err != nil {
				fail(w, http.StatusBadRequest, err)
				return
			}
			s.changed("mcp", in.Root)
			writeJSON(w, def)
			return
		}
		fail(w, http.StatusNotFound, errors.New("no such server in that install"))
	})
}

// --- settings ---

func (s *Server) settingsRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("root")
		models := []map[string]any{}
		for _, m := range agent.Models {
			models = append(models, map[string]any{"id": m.ID, "label": m.Label, "note": m.Note})
		}
		out := map[string]any{
			"prefs":  config.LoadPrefs(),
			"config": config.Load(),
			"models": models,
			"modes":  []string{"plan", "ask", "auto", "full"},
			"paths": map[string]string{
				"prefs":         filepath.Join(config.DataDir(), "prefs.json"),
				"config":        filepath.Join(config.Dir(), "config.json"),
				"global_skills": skill.GlobalDir(),
				"global_mcp":    filepath.Join(config.Dir(), "mcp.json"),
				"data":          config.DataDir(),
			},
			"learn_default_model": learn.DefaultModel,
		}
		if root != "" {
			out["project"] = config.LoadProjectSettings(root)
			out["project_paths"] = map[string]string{
				"settings": filepath.Join(config.ProjectDir(root), "settings.json"),
				"skills":   skill.ProjectDirFor(root),
				"mcp":      filepath.Join(config.ProjectDir(root), "mcp.json"),
				"memory":   filepath.Join(config.ProjectDataDir(root), "memory"),
			}
			out["engines"] = s.Runner.Engines(root)
		}
		writeJSON(w, out)
	})

	m.HandleFunc("PUT /api/settings/global", func(w http.ResponseWriter, r *http.Request) {
		// Read-modify-write over the stored file, so fields this page does
		// not know about (last_root, written by the terminal) survive.
		p := config.LoadPrefs()
		if err := readJSON(r, &p); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if err := config.SavePrefs(p); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		s.changed("settings", "")
		writeJSON(w, p)
	})

	m.HandleFunc("PUT /api/settings/project", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root     string                 `json:"root"`
			Settings config.ProjectSettings `json:"settings"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if !config.IsDir(in.Root) {
			fail(w, http.StatusBadRequest, errors.New("not a folder: "+in.Root))
			return
		}
		if err := config.SaveProjectSettings(in.Root, in.Settings); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		s.changed("settings", in.Root)
		writeJSON(w, in.Settings)
	})

	// context previews what a prompt in a project would be given, without
	// running anything: the whole system addition, recalled memories and
	// all — the answer to "what does the agent actually see?".
	m.HandleFunc("POST /api/context/preview", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Root   string `json:"root"`
			Engine string `json:"engine"`
			Prompt string `json:"prompt"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		k := kit.For(in.Root)
		k.DryRun = true
		_, tr := k.Extras(ctx, in.Engine, in.Prompt)
		writeJSON(w, tr)
	})
}
