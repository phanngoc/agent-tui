package memory

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
)

// Bank is the memory a session sees: the global store and its project's.
type Bank struct {
	Global  *Store
	Project *Store // nil without a project
}

var stores sync.Map // dir -> *Store; one per directory, so one lock per file

func shared(dir string, scope Scope) *Store {
	v, _ := stores.LoadOrStore(dir, Open(dir, scope))
	return v.(*Store)
}

// GlobalDir is where global memory lives.
func GlobalDir() string { return filepath.Join(config.DataDir(), "memory") }

// ProjectDir is where a project's memory lives.
func ProjectDir(root string) string { return filepath.Join(config.ProjectDataDir(root), "memory") }

// For returns the bank of a project; an empty root gives a global-only one.
func For(root string) *Bank {
	b := &Bank{Global: shared(GlobalDir(), Global)}
	if root != "" {
		b.Project = shared(ProjectDir(root), Project)
	}
	return b
}

// Store picks a scope's store, nil when there is none.
func (b *Bank) Store(scope Scope) *Store {
	if scope == Project {
		return b.Project
	}
	return b.Global
}

// Stores lists the stores that exist, global first.
func (b *Bank) Stores() []*Store {
	if b.Project == nil {
		return []*Store{b.Global}
	}
	return []*Store{b.Global, b.Project}
}

// Records is every record of both scopes.
func (b *Bank) Records() []Record {
	var out []Record
	for _, s := range b.Stores() {
		out = append(out, s.All()...)
	}
	return out
}

// Find looks a record up in either scope.
func (b *Bank) Find(id string) (Record, *Store, bool) {
	for _, s := range b.Stores() {
		if r, ok := s.Get(id); ok {
			return r, s, true
		}
	}
	return Record{}, nil, false
}

// Recall settings, TencentDB's defaults.
const (
	RecallLimit     = 5
	RecallThreshold = 0.3
	// alwaysCap bounds the rules that go into every prompt, so a memory
	// that grew carelessly cannot crowd out the conversation.
	alwaysCap = 30
)

// Recall finds the records relevant to a prompt, leaving out those that are
// in every prompt anyway, and counts the hit.
func (b *Bank) Recall(query string) []Hit {
	hits := b.Peek(query)
	ids := map[Scope][]string{}
	for _, h := range hits {
		ids[h.Record.Scope] = append(ids[h.Record.Scope], h.Record.ID)
	}
	for _, s := range b.Stores() {
		s.Touch(ids[s.Scope])
	}
	return hits
}

// Peek is Recall without counting it: for a preview.
func (b *Bank) Peek(query string) []Hit {
	var pool []Record
	for _, r := range b.Records() {
		if !r.Always() {
			pool = append(pool, r)
		}
	}
	return Recall(pool, query, RecallLimit, RecallThreshold)
}

// SystemContext is the stable part of what memory adds to a system prompt:
// the persona, the project's doctrine, the rules that always apply, and a
// map of the scenes the agent can open with memory_read. It changes only when
// learning runs, so it does not break the prompt cache turn to turn.
func (b *Bank) SystemContext() string {
	var out strings.Builder
	if p := b.Global.Persona(); p != "" {
		out.WriteString("<user-persona>\n" + p + "\n</user-persona>\n\n")
	}
	if b.Project != nil {
		if p := b.Project.Persona(); p != "" {
			out.WriteString("<project-doctrine>\n" + p + "\n</project-doctrine>\n\n")
		}
	}

	var rules []string
	for _, r := range b.Records() {
		if r.Always() && len(rules) < alwaysCap {
			rules = append(rules, "- ["+string(r.Scope)+"|"+r.Type+"] "+r.Content)
		}
	}
	if len(rules) > 0 {
		out.WriteString("<standing-memories>\nThese always apply:\n" + strings.Join(rules, "\n") + "\n</standing-memories>\n\n")
	}

	var nav strings.Builder
	for _, s := range b.Stores() {
		for _, sc := range s.Scenes() {
			fmt.Fprintf(&nav, "- %s/%s (heat %d, updated %s): %s\n", s.Scope, sc.File, sc.Heat,
				sc.Updated.Format("2006-01-02"), sc.Summary)
		}
	}
	if nav.Len() > 0 {
		out.WriteString("<scene-navigation>\nConsolidated notes from earlier work; open one with memory_read when it bears on the task.\n" +
			nav.String() + "</scene-navigation>\n\n")
	}
	if out.Len() == 0 {
		return ""
	}
	out.WriteString(`<memory-tools-guide>
The above is what you have learned in earlier sessions. Relevant memories for each prompt are recalled for you.
Use memory_search when you need something specific from the past (at most 3 searches per turn), memory_read to open a scene,
and memory_save when the user asks you to remember something, or states a lasting preference or rule. (Where tools come
from MCP servers, these are mcp__agent-tui__memory_search and so on; the skill tool likewise.)
Memories are references, not the current state of the code: check before relying on one that may be stale.
</memory-tools-guide>`)
	return out.String()
}

// TurnContext renders the memories recalled for one prompt.
func TurnContext(hits []Hit) string {
	if len(hits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<relevant-memories>\nRecalled from earlier sessions as reference — not the current task or state:\n")
	for _, h := range hits {
		r := h.Record
		label := string(r.Scope) + "|" + r.Type
		if r.Scene != "" {
			label += "|" + r.Scene
		}
		fmt.Fprintf(&b, "- [%s] %s (%s)\n", label, r.Content, r.Updated.Local().Format("2006-01-02"))
	}
	b.WriteString("</relevant-memories>")
	return b.String()
}

// Stats summarise a store for the admin.
type Stats struct {
	Scope   Scope          `json:"scope"`
	Records int            `json:"records"`
	ByType  map[string]int `json:"by_type"`
	Scenes  int            `json:"scenes"`
	Persona bool           `json:"persona"`
	Updated time.Time      `json:"updated"`
	Dir     string         `json:"dir"`
}

// Stats counts what a store holds.
func (s *Store) Stats() Stats {
	st := Stats{Scope: s.Scope, ByType: map[string]int{}, Dir: s.Dir}
	for _, r := range s.All() {
		st.Records++
		st.ByType[r.Type]++
		if r.Updated.After(st.Updated) {
			st.Updated = r.Updated
		}
	}
	st.Scenes = len(s.Scenes())
	st.Persona = s.Persona() != ""
	return st
}
