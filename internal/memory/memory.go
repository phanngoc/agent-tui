// Package memory is the agent's long-term memory, in the layered shape of
// TencentDB Agent Memory:
//
//   - L0, the conversation itself, is the sessions this app already saves.
//   - L1 records are atoms: one self-contained fact, preference, rule or method
//     each, extracted from the conversation and deduplicated as they arrive.
//   - L2 scenes are a handful of narrative Markdown blocks that consolidate the
//     records into the situations they belong to.
//   - L3 is one short persona: who the user is and how they work, distilled
//     from the scenes.
//
// Each layer exists twice. Global memory is about the user and goes with them
// into every project; project memory is about one codebase. Both are kept in
// the data directory, never in the repository.
//
// This package only stores and searches. Extraction and consolidation, which
// need a model, are in package learn.
package memory

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Scope says which memory a record belongs to.
type Scope string

const (
	Global  Scope = "global"
	Project Scope = "project"
)

// Record types. The first three describe a person, the work_ ones a project;
// they are the chat and code vocabularies of the extraction prompts.
const (
	TypePersona      = "persona"       // a stable trait, preference or habit of the user
	TypeInstruction  = "instruction"   // a standing rule for the agent
	TypeEpisodic     = "episodic"      // something that happened, with when
	TypeWorkFact     = "work_fact"     // a decision, requirement, constraint, risk, result
	TypeWorkTask     = "work_task"     // something to do, with owner and status
	TypeWorkMethod   = "work_method"   // how things are done here: SOP, principle, anti-pattern
	TypeWorkArtifact = "work_artifact" // a file, document, endpoint or resource that matters
)

// Types lists every record type.
var Types = []string{TypePersona, TypeInstruction, TypeEpisodic,
	TypeWorkFact, TypeWorkTask, TypeWorkMethod, TypeWorkArtifact}

// ValidType reports whether t is a record type.
func ValidType(t string) bool {
	for _, v := range Types {
		if v == t {
			return true
		}
	}
	return false
}

// DefaultScope is where a record of this type belongs when nobody said: what
// describes the user is global, what describes the work is the project's.
func DefaultScope(t string) Scope {
	switch t {
	case TypePersona, TypeInstruction:
		return Global
	}
	return Project
}

// Record is one L1 memory.
type Record struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Type    string `json:"type"`
	// Priority is 0-100, how much this matters; -1 marks an absolute rule
	// that is always in the prompt.
	Priority int    `json:"priority"`
	Scene    string `json:"scene,omitempty"`
	Scope    Scope  `json:"scope"`
	// Session and Sources say where it was learned: a session id and the
	// indexes of the messages in it.
	Session  string         `json:"session,omitempty"`
	Sources  []int          `json:"sources,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	// Timestamps is every time this was observed, merges included.
	Timestamps []time.Time `json:"timestamps,omitempty"`
	Created    time.Time   `json:"created"`
	Updated    time.Time   `json:"updated"`
	Version    int         `json:"version"`
	// Origin is learned, manual, agent (the agent saved it on request) or
	// import.
	Origin string `json:"origin,omitempty"`
	// Pinned records are always in the prompt, like a -1 priority.
	Pinned bool `json:"pinned,omitempty"`
	// Hits counts how often recall picked this record.
	Hits    int       `json:"hits,omitempty"`
	LastHit time.Time `json:"last_hit,omitempty"`
}

// Always reports whether a record goes into every prompt rather than only when
// recall picks it.
func (r Record) Always() bool { return r.Pinned || r.Priority < 0 }

// NewID makes a record id.
func NewID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "mem_" + time.Now().UTC().Format("20060102") + "_" + hex.EncodeToString(b[:])
}

// Store is one scope's memory on disk:
//
//	records.json         the L1 records
//	log/YYYY-MM-DD.jsonl every write, append-only, for audit and recovery
//	scenes/*.md          the L2 scene blocks
//	persona.md           the L3 persona
type Store struct {
	Dir   string
	Scope Scope

	mu      sync.Mutex
	records []Record
	mtime   time.Time
	loaded  bool
}

// Open returns the store in dir, creating nothing until the first write.
func Open(dir string, scope Scope) *Store { return &Store{Dir: dir, Scope: scope} }

func (s *Store) path() string { return filepath.Join(s.Dir, "records.json") }

// load re-reads the file when another process (the TUI, the admin server)
// changed it since this one last did.
func (s *Store) load() {
	st, err := os.Stat(s.path())
	if err != nil {
		if !s.loaded {
			s.records, s.loaded = nil, true
		}
		return
	}
	if s.loaded && st.ModTime().Equal(s.mtime) {
		return
	}
	b, err := os.ReadFile(s.path())
	if err != nil {
		return
	}
	var recs []Record
	if json.Unmarshal(b, &recs) != nil {
		return
	}
	for i := range recs {
		recs[i].Scope = s.Scope
	}
	s.records, s.mtime, s.loaded = recs, st.ModTime(), true
}

func (s *Store) save() error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path()); err != nil {
		return err
	}
	if st, err := os.Stat(s.path()); err == nil {
		s.mtime = st.ModTime()
	}
	return nil
}

func (s *Store) logOp(op string, r Record, extra map[string]any) {
	dir := filepath.Join(s.Dir, "log")
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	entry := map[string]any{"at": time.Now().UTC(), "op": op, "record": r}
	for k, v := range extra {
		entry[k] = v
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, time.Now().Format("2006-01-02")+".jsonl"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// All returns a copy of every record, newest first.
func (s *Store) All() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	out := append([]Record(nil), s.records...)
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// Get finds a record by id.
func (s *Store) Get(id string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	for _, r := range s.records {
		if r.ID == id {
			return r, true
		}
	}
	return Record{}, false
}

// Put inserts a record, or replaces the one with its id.
func (s *Store) Put(r Record) (Record, error) {
	if strings.TrimSpace(r.Content) == "" {
		return r, errors.New("a memory needs content")
	}
	if !ValidType(r.Type) {
		return r, fmt.Errorf("unknown memory type %q", r.Type)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	now := time.Now().UTC()
	r.Scope = s.Scope
	if r.ID == "" {
		r.ID = NewID()
	}
	if r.Created.IsZero() {
		r.Created = now
	}
	r.Updated = now
	if len(r.Timestamps) == 0 {
		r.Timestamps = []time.Time{now}
	}
	op := "store"
	for i := range s.records {
		if s.records[i].ID == r.ID {
			r.Version = s.records[i].Version + 1
			r.Hits, r.LastHit = s.records[i].Hits, s.records[i].LastHit
			s.records[i] = r
			op = "update"
			s.logOp(op, r, nil)
			return r, s.save()
		}
	}
	if r.Version == 0 {
		r.Version = 1
	}
	s.records = append(s.records, r)
	s.logOp(op, r, nil)
	return r, s.save()
}

// Replace removes the targets and stores r in their place: the update and
// merge of consolidation. r's version continues the highest of theirs.
func (s *Store) Replace(targets []string, r Record, op string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	now := time.Now().UTC()
	gone := map[string]bool{}
	for _, id := range targets {
		gone[id] = true
	}
	ver, hits := 0, 0
	created := now
	kept := s.records[:0]
	for _, old := range s.records {
		if gone[old.ID] {
			ver = max(ver, old.Version)
			hits += old.Hits
			if old.Created.Before(created) {
				created = old.Created
			}
			continue
		}
		kept = append(kept, old)
	}
	s.records = kept
	r.Scope = s.Scope
	if r.ID == "" {
		r.ID = NewID()
	}
	r.Created, r.Updated, r.Version, r.Hits = created, now, ver+1, hits
	if len(r.Timestamps) == 0 {
		r.Timestamps = []time.Time{now}
	}
	s.records = append(s.records, r)
	s.logOp(op, r, map[string]any{"targets": targets})
	return r, s.save()
}

// Delete removes records by id and reports how many went.
func (s *Store) Delete(ids ...string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	gone := map[string]bool{}
	for _, id := range ids {
		gone[id] = true
	}
	n := 0
	kept := s.records[:0]
	for _, r := range s.records {
		if gone[r.ID] {
			n++
			s.logOp("delete", r, nil)
			continue
		}
		kept = append(kept, r)
	}
	s.records = kept
	if n == 0 {
		return 0, nil
	}
	return n, s.save()
}

// Touch records that recall used these records.
func (s *Store) Touch(ids []string) {
	if len(ids) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	now := time.Now().UTC()
	hit := false
	for i := range s.records {
		if want[s.records[i].ID] {
			s.records[i].Hits++
			s.records[i].LastHit = now
			hit = true
		}
	}
	if hit {
		_ = s.save()
	}
}

// LogEntry is one line of the write log.
type LogEntry struct {
	At      time.Time `json:"at"`
	Op      string    `json:"op"`
	Record  Record    `json:"record"`
	Targets []string  `json:"targets,omitempty"`
}

// Log returns the most recent writes, newest first.
func (s *Store) Log(limit int) []LogEntry {
	files, _ := filepath.Glob(filepath.Join(s.Dir, "log", "*.jsonl"))
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	var out []LogEntry
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			var e LogEntry
			if json.Unmarshal([]byte(lines[i]), &e) == nil {
				out = append(out, e)
				if len(out) >= limit {
					return out
				}
			}
		}
	}
	return out
}
