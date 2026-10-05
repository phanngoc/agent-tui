// Package kit assembles what a project adds to a turn — standing
// instructions, skills, memory and MCP servers — into agent.Extras, and
// records what it added so the admin can show, for any turn, exactly what the
// agent was given. Both the terminal app and the gateway build turns through
// here, so a turn means the same thing wherever it runs.
package kit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/mcp"
	"github.com/phanngoc/agent-tui/internal/memory"
	"github.com/phanngoc/agent-tui/internal/skill"
)

// KitServer is the name the skill and memory tools go by when served over MCP
// to a CLI engine.
const KitServer = "agent-tui"

// ServeMCP serves a project's skill and memory tools on stdio: the far end of
// the entry Extras adds for CLI engines.
func ServeMCP(ctx context.Context, root, sessionID string, r io.Reader, w io.Writer) error {
	k := For(root)
	k.Session = sessionID
	var tools []mcp.Served
	add := func(e agent.Extension) {
		var schema map[string]any
		if e.Def.OfTool != nil {
			b, _ := json.Marshal(e.Def.OfTool.InputSchema)
			_ = json.Unmarshal(b, &schema)
			tools = append(tools, mcp.Served{Name: e.Name, Description: e.Def.OfTool.Description.Value, InputSchema: schema, Run: e.Run})
		}
	}
	add(k.skillTool())
	for _, e := range k.memoryTools() {
		add(e)
	}
	if k.Root != "" {
		add(k.scheduleTool())
	}
	return mcp.Serve(ctx, r, w, KitServer, tools)
}

// Pool is the process's MCP connections, shared by every session.
var Pool = mcp.NewPool()

// Kit is one project's configuration, read fresh: everything here is a file
// the admin may have changed a moment ago.
type Kit struct {
	Root     string
	Prefs    config.Prefs
	Settings config.ProjectSettings
	Skills   skill.Store
	MCP      mcp.Store
	Memory   *memory.Bank
	// DryRun previews: recall does not count as a hit.
	DryRun bool
	// Session is the session the turn belongs to, when known: the schedule
	// tool needs it to pace a run or loop a conversation.
	Session string
}

// MCPStore is the server store of a project.
func MCPStore(root string) mcp.Store {
	st := mcp.Store{GlobalPath: filepath.Join(config.Dir(), "mcp.json")}
	if root != "" {
		st.ProjectPath = filepath.Join(config.ProjectDir(root), "mcp.json")
	}
	return st
}

// For reads a project's kit.
func For(root string) *Kit {
	k := &Kit{Root: root, Prefs: config.LoadPrefs(), Skills: skill.For(root), MCP: MCPStore(root),
		Memory: memory.For(root)}
	if root != "" {
		k.Settings = config.LoadProjectSettings(root)
	}
	return k
}

// Trace is what one turn was given, kept per session for the admin.
type Trace struct {
	At       time.Time  `json:"at"`
	Session  string     `json:"session"`
	Engine   string     `json:"engine"`
	Prompt   string     `json:"prompt"`
	Recalled []TraceHit `json:"recalled"`
	Standing int        `json:"standing"`
	Skills   []string   `json:"skills"`
	MCP      []string   `json:"mcp"`
	MCPError []string   `json:"mcp_errors,omitempty"`
	Tools    []string   `json:"tools"`
	Persona  bool       `json:"persona"`
	Doctrine bool       `json:"doctrine"`
	Chars    int        `json:"system_chars"`
	System   string     `json:"system"`
	Learning bool       `json:"learning"`
}

// TraceHit is one recalled memory.
type TraceHit struct {
	ID      string  `json:"id"`
	Scope   string  `json:"scope"`
	Type    string  `json:"type"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// Hook returns the Extras function for a turn: the caller sets it on the turn
// and the engine calls it on its own goroutine.
func Hook(root, sessionID, engineID, prompt string) func(context.Context) agent.Extras {
	return func(ctx context.Context) agent.Extras {
		k := For(root)
		k.Session = sessionID
		x, tr := k.Extras(ctx, engineID, prompt)
		tr.Session = sessionID
		SaveTrace(tr)
		return x
	}
}

// Extras builds the turn's additions and the trace of them.
func (k *Kit) Extras(ctx context.Context, engineID, prompt string) (agent.Extras, Trace) {
	tr := Trace{At: time.Now().UTC(), Engine: engineID, Prompt: clip(prompt, 400),
		Recalled: []TraceHit{}, Skills: []string{}, MCP: []string{}, Tools: []string{},
		Learning: k.Prefs.LearnOn(k.Settings)}
	var x agent.Extras
	var sys strings.Builder

	// Standing instructions: the user's, then the project's, then the
	// repository's own AGENTS.md if it has one.
	if s := strings.TrimSpace(k.Prefs.Instructions); s != "" {
		sys.WriteString("<user-instructions>\n" + s + "\n</user-instructions>\n\n")
	}
	if s := strings.TrimSpace(k.Settings.Instructions); s != "" {
		sys.WriteString("<project-instructions>\n" + s + "\n</project-instructions>\n\n")
	}
	if k.Root != "" {
		if b, err := os.ReadFile(filepath.Join(k.Root, "AGENTS.md")); err == nil && len(b) > 0 {
			sys.WriteString("<agents-md>\n" + clip(string(b), 8000) + "\n</agents-md>\n\n")
		}
	}

	// Skills: a listing in the prompt, the bodies behind a tool.
	skills := k.Skills.Active(k.Settings.DisabledSkills)
	if len(skills) > 0 {
		sys.WriteString("<available_skills>\nSkills are procedures written for tasks like these. When one is even partly relevant, load it with the skill tool before starting, and follow it.\n")
		for _, s := range skills {
			fmt.Fprintf(&sys, "- %s (%s): %s\n", s.Name, s.Scope, clip(s.Description, 300))
			tr.Skills = append(tr.Skills, s.Name)
		}
		sys.WriteString("</available_skills>\n\n")
		x.Tools = append(x.Tools, k.skillTool())
	}

	// Memory: the stable part, then what this prompt recalls.
	mem := k.Memory.SystemContext()
	if mem != "" {
		sys.WriteString(mem + "\n\n")
	}
	tr.Persona = k.Memory.Global.Persona() != ""
	tr.Doctrine = k.Memory.Project != nil && k.Memory.Project.Persona() != ""
	for _, r := range k.Memory.Records() {
		if r.Always() {
			tr.Standing++
		}
	}
	if strings.TrimSpace(prompt) != "" {
		recall := k.Memory.Recall
		if k.DryRun {
			recall = k.Memory.Peek
		}
		hits := recall(prompt)
		if t := memory.TurnContext(hits); t != "" {
			sys.WriteString(t + "\n")
		}
		for _, h := range hits {
			tr.Recalled = append(tr.Recalled, TraceHit{ID: h.Record.ID, Scope: string(h.Record.Scope),
				Type: h.Record.Type, Content: h.Record.Content, Score: h.Score})
		}
	}
	x.Tools = append(x.Tools, k.memoryTools()...)
	if k.Root != "" {
		x.Tools = append(x.Tools, k.scheduleTool())
		// Without this, asked to "run it every hour" an agent reaches for
		// what it knows — a crontab entry, a timer, a loop script — which
		// runs out of sight, outside the app the user is looking at.
		sys.WriteString(schedulingGuide(engineID == "api" || engineID == ""))
	}
	native := engineID == "api" || engineID == ""

	// MCP: the claude engine runs the servers itself; the built-in one
	// connects to them here.
	servers := k.MCP.Active(k.Settings.DisabledMCP)
	for _, s := range servers {
		tr.MCP = append(tr.MCP, s.Name)
	}
	if len(servers) > 0 {
		x.MCPServers = mcp.ClaudeConfig(mcp.WithSignIn(ctx, servers))
		if native {
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			refs := Pool.Tools(cctx, servers)
			cancel()
			got := map[string]bool{}
			for _, r := range refs {
				got[r.Server] = true
				x.Tools = append(x.Tools, mcpTool(servers, r))
			}
			for _, s := range servers {
				if !got[s.Name] {
					tr.MCPError = append(tr.MCPError, s.Name)
				}
			}
		}
	}

	// A CLI engine cannot call this process's tools directly, so they are
	// offered to it the way it takes any tool: as an MCP server — this same
	// binary, serving the skill and memory tools over stdio.
	if !native {
		if self, err := os.Executable(); err == nil {
			if x.MCPServers == nil {
				x.MCPServers = map[string]any{}
			}
			args := []string{"kit-mcp", "-root", k.Root}
			if k.Session != "" {
				args = append(args, "-session", k.Session)
			}
			if addr, err := gatewayAddr(); err == nil {
				args = append(args, "-gateway", addr)
			}
			x.MCPServers[KitServer] = map[string]any{"type": "stdio", "command": self, "args": args}
		}
		for _, t := range x.Tools {
			if !strings.HasPrefix(t.Name, "mcp__") {
				tr.Tools = append(tr.Tools, "mcp__"+KitServer+"__"+t.Name)
			}
		}
		x.Tools = nil
	} else {
		for _, t := range x.Tools {
			tr.Tools = append(tr.Tools, t.Name)
		}
	}
	x.System = strings.TrimSpace(sys.String())
	tr.System, tr.Chars = x.System, len(x.System)
	return x, tr
}

func (k *Kit) skillTool() agent.Extension {
	return agent.Extension{
		Name: "skill",
		Def: toolDef("skill", "Load a skill's full instructions by name, from the available_skills list.",
			map[string]any{"name": map[string]any{"type": "string", "description": "The skill's name."}}, "name"),
		Run: func(_ context.Context, in json.RawMessage) (string, bool) {
			var a struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(in, &a)
			sk, ok := k.Skills.Get(a.Name)
			if !ok || config.Disabled(k.Settings.DisabledSkills, a.Name) {
				return "no skill named " + a.Name, true
			}
			out := "# Skill: " + sk.Name + "\n\n" + sk.Body
			if len(sk.Files) > 0 {
				out += "\n\nFiles in this skill's folder (" + filepath.Dir(sk.Path) + "): " + strings.Join(sk.Files, ", ")
			}
			return out, false
		},
	}
}

func (k *Kit) memoryTools() []agent.Extension {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	return []agent.Extension{
		{
			Name: "memory_search",
			Def: toolDef("memory_search", "Search long-term memory from earlier sessions (facts, decisions, methods, the user's preferences).",
				map[string]any{"query": str("What to look for, in keywords.")}, "query"),
			Run: func(_ context.Context, in json.RawMessage) (string, bool) {
				var a struct {
					Query string `json:"query"`
				}
				_ = json.Unmarshal(in, &a)
				hits := memory.Search(k.Memory.Records(), a.Query, 8)
				if len(hits) == 0 {
					return "nothing found", false
				}
				var b strings.Builder
				for _, h := range hits {
					r := h.Record
					fmt.Fprintf(&b, "- [%s|%s|%s] %s (updated %s)\n", r.ID, r.Scope, r.Type, r.Content, r.Updated.Format("2006-01-02"))
				}
				return b.String(), false
			},
		},
		{
			Name: "memory_read",
			Def: toolDef("memory_read", "Open a scene block listed in scene-navigation, e.g. \"project/search-ranking.md\".",
				map[string]any{"path": str("scope/file.md as listed.")}, "path"),
			Run: func(_ context.Context, in json.RawMessage) (string, bool) {
				var a struct {
					Path string `json:"path"`
				}
				_ = json.Unmarshal(in, &a)
				scope, file, ok := strings.Cut(a.Path, "/")
				if !ok {
					scope, file = "project", a.Path
				}
				st := k.Memory.Store(memory.Scope(scope))
				if st == nil {
					return "no " + scope + " memory", true
				}
				sc, err := st.Scene(file)
				if err != nil {
					return err.Error(), true
				}
				return "# " + sc.Summary + "\n\n" + sc.Body, false
			},
		},
		{
			Name: "memory_save",
			Def: toolDef("memory_save", "Save something to long-term memory when the user asks you to remember it, or states a lasting rule or preference.",
				map[string]any{
					"content":  str("The memory, self-contained: who or what, and the fact, rule or method."),
					"type":     map[string]any{"type": "string", "enum": memory.Types},
					"scope":    map[string]any{"type": "string", "enum": []string{"global", "project"}, "description": "global: about the user, everywhere; project: about this codebase."},
					"priority": map[string]any{"type": "integer", "description": "0-100; -1 for an absolute rule."},
				}, "content", "type"),
			Run: func(_ context.Context, in json.RawMessage) (string, bool) {
				var a struct {
					Content  string `json:"content"`
					Type     string `json:"type"`
					Scope    string `json:"scope"`
					Priority *int   `json:"priority"`
				}
				_ = json.Unmarshal(in, &a)
				scope := memory.Scope(a.Scope)
				if scope != memory.Global && scope != memory.Project {
					scope = memory.DefaultScope(a.Type)
				}
				st := k.Memory.Store(scope)
				if st == nil {
					st = k.Memory.Global
				}
				p := 80
				if a.Priority != nil {
					p = *a.Priority
				}
				r, err := st.Put(memory.Record{Content: a.Content, Type: a.Type, Priority: p, Origin: "agent"})
				if err != nil {
					return err.Error(), true
				}
				return "saved " + r.ID + " to " + string(r.Scope) + " memory", false
			},
		},
	}
}

func mcpTool(servers []mcp.Server, r mcp.ToolRef) agent.Extension {
	schema := map[string]any{"type": "object", "properties": map[string]any{}}
	if len(r.Tool.InputSchema) > 0 {
		var s map[string]any
		if json.Unmarshal(r.Tool.InputSchema, &s) == nil {
			schema = s
		}
	}
	name := r.QualifiedName()
	desc := r.Tool.Description
	if desc == "" {
		desc = "Tool " + r.Tool.Name + " of MCP server " + r.Server
	}
	return agent.Extension{
		Name:     name,
		Mutating: true,
		Def:      rawToolDef(name, clip(desc, 1024), schema),
		Run: func(ctx context.Context, in json.RawMessage) (string, bool) {
			return Pool.CallOn(ctx, servers, r.Server, r.Tool.Name, in)
		},
	}
}

func toolDef(name, desc string, props map[string]any, required ...string) anthropic.ToolUnionParam {
	return rawToolDef(name, desc, map[string]any{"type": "object", "properties": props, "required": required})
}

func rawToolDef(name, desc string, schema map[string]any) anthropic.ToolUnionParam {
	in := anthropic.ToolInputSchemaParam{}
	if p, ok := schema["properties"]; ok {
		in.Properties = p
	}
	if r, ok := schema["required"].([]string); ok {
		in.Required = r
	} else if r, ok := schema["required"].([]any); ok {
		for _, v := range r {
			if s, ok := v.(string); ok {
				in.Required = append(in.Required, s)
			}
		}
	}
	extra := map[string]any{}
	for k, v := range schema {
		if k != "properties" && k != "required" && k != "type" {
			extra[k] = v
		}
	}
	if len(extra) > 0 {
		in.ExtraFields = extra
	}
	return anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
		Name:        name,
		Description: anthropic.String(desc),
		InputSchema: in,
	}}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

var traceMu sync.Mutex

func traceDir() string { return filepath.Join(config.DataDir(), "trace") }

// SaveTrace appends a turn's trace to its session's file.
func SaveTrace(tr Trace) {
	if tr.Session == "" {
		return
	}
	b, err := json.Marshal(tr)
	if err != nil {
		return
	}
	traceMu.Lock()
	defer traceMu.Unlock()
	_ = os.MkdirAll(traceDir(), 0o755)
	f, err := os.OpenFile(filepath.Join(traceDir(), tr.Session+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// Traces returns a session's traces, oldest first.
func Traces(sessionID string) []Trace {
	out := []Trace{}
	if strings.ContainsAny(sessionID, `/\:`) || strings.Contains(sessionID, "..") {
		return out
	}
	b, err := os.ReadFile(filepath.Join(traceDir(), sessionID+".jsonl"))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var t Trace
		if json.Unmarshal([]byte(line), &t) == nil {
			out = append(out, t)
		}
	}
	return out
}
