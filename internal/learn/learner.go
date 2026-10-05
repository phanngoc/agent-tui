// Package learn is the agent's automatic learning: after turns it reads what
// was said, extracts what is worth remembering, merges it with what is already
// remembered, and from time to time consolidates it — the pipeline of
// TencentDB Agent Memory, run in-process against package memory.
//
// Scheduling follows TencentDB too. L1 extraction runs after N turns, where N
// warms up 1, 2, 4, 5 so a new session is learned from at once, or after ten
// idle minutes, whichever comes first. L2 scene consolidation follows L1, no
// more than once every fifteen minutes per store. L3 rewrites the persona when
// the scenes asked for it, when there is none yet, or after enough new
// memories. Skill review runs when a session has made enough tool calls to
// have done a real piece of work.
//
// Everything runs on one background goroutine, one model call at a time; a
// turn never waits for learning.
package learn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/memory"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/skill"
)

// Scheduling knobs.
const (
	EveryN         = 5
	IdleAfter      = 10 * time.Minute
	SceneInterval  = 15 * time.Minute
	SkillToolCalls = 10
	activityKeep   = 500
	jobTimeout     = 20 * time.Minute
)

// SessionState is the learner's bookmark in one session.
type SessionState struct {
	Root string `json:"root"`
	// Cursor is the first message L1 has not read.
	Cursor int `json:"cursor"`
	// Turns counts turns since the last L1 run; Threshold is how many
	// trigger the next.
	Turns     int    `json:"turns"`
	Threshold int    `json:"threshold"`
	Scene     string `json:"scene,omitempty"`
	// SkillCursor is the first message skill review has not read.
	SkillCursor int       `json:"skill_cursor"`
	Updated     time.Time `json:"updated"`
}

// StoreState is the learner's bookmark in one memory store.
type StoreState struct {
	LastScenes   time.Time `json:"last_scenes"`
	Pending      []string  `json:"pending,omitempty"` // records not yet in a scene
	SincePersona int       `json:"since_persona"`
	WantPersona  bool      `json:"want_persona,omitempty"`
}

type state struct {
	Sessions map[string]*SessionState `json:"sessions"`
	Stores   map[string]*StoreState   `json:"stores"`
}

// Activity is one thing the learner did, for the admin to show.
type Activity struct {
	At      time.Time `json:"at"`
	Stage   string    `json:"stage"` // extract|dedup|scenes|persona|skill|error|note
	Session string    `json:"session,omitempty"`
	Root    string    `json:"root,omitempty"`
	Detail  string    `json:"detail"`
	Error   string    `json:"error,omitempty"`
	// Records are the memory ids this step wrote, so a record can be traced
	// back to the run, and the run forward to its records.
	Records []string `json:"records,omitempty"`
	Scope   string   `json:"scope,omitempty"`
	// Dir is the memory store the step worked on, so the admin can name the
	// project and open what was written.
	Dir string `json:"dir,omitempty"`
}

type job struct {
	session string
	root    string
	msgs    []session.Message
	force   bool
	// scenesOnly skips extraction and consolidates stores now: those in
	// stores, or the project's and the global one.
	scenesOnly bool
	stores     []*memory.Store
	reports    *[]StoreReport
	done       chan error
}

// Learner runs the pipeline. One per process.
type Learner struct {
	dir string

	mu     sync.Mutex
	st     state
	timers map[string]*time.Timer
	last   map[string]job // newest snapshot per session, for the idle timer
	busy   string

	jobs chan job

	llmMu  sync.Mutex
	llm    LLM
	llmFor string
}

var (
	once    sync.Once
	current *Learner
)

// Default is the process's learner, started on first use.
func Default() *Learner {
	once.Do(func() {
		current = New(filepath.Join(config.DataDir(), "learn"))
		go current.loop()
	})
	return current
}

// New makes a learner keeping its state in dir. Call loop to run it; Default
// does both.
func New(dir string) *Learner {
	l := &Learner{dir: dir, timers: map[string]*time.Timer{}, last: map[string]job{},
		jobs: make(chan job, 64)}
	l.load()
	return l
}

func (l *Learner) statePath() string { return filepath.Join(l.dir, "state.json") }

func (l *Learner) load() {
	l.st = state{Sessions: map[string]*SessionState{}, Stores: map[string]*StoreState{}}
	if b, err := os.ReadFile(l.statePath()); err == nil {
		_ = json.Unmarshal(b, &l.st)
	}
	if l.st.Sessions == nil {
		l.st.Sessions = map[string]*SessionState{}
	}
	if l.st.Stores == nil {
		l.st.Stores = map[string]*StoreState{}
	}
}

// save must be called with mu held.
func (l *Learner) save() {
	_ = config.WriteJSON(l.statePath(), l.st)
}

func (l *Learner) sess(id, root string) *SessionState {
	s := l.st.Sessions[id]
	if s == nil {
		s = &SessionState{Root: root, Threshold: 1}
		l.st.Sessions[id] = s
	}
	if s.Threshold < 1 {
		s.Threshold = 1
	}
	if root != "" {
		s.Root = root
	}
	return s
}

func (l *Learner) store(dir string) *StoreState {
	s := l.st.Stores[dir]
	if s == nil {
		s = &StoreState{}
		l.st.Stores[dir] = s
	}
	return s
}

// Enabled reports whether learning is on for a project.
func Enabled(root string) bool {
	return config.LoadPrefs().LearnOn(config.LoadProjectSettings(root))
}

func skillsOn() bool {
	p := config.LoadPrefs()
	return p.LearnSkills == nil || *p.LearnSkills
}

// Notify tells the learner a turn finished. msgs is the whole transcript; it
// is copied, so the caller may go on changing its own.
func (l *Learner) Notify(root, sessionID string, msgs []session.Message) {
	if sessionID == "" || !Enabled(root) {
		return
	}
	j := job{session: sessionID, root: root, msgs: append([]session.Message(nil), msgs...)}
	l.mu.Lock()
	s := l.sess(sessionID, root)
	s.Turns++
	s.Updated = time.Now().UTC()
	l.last[sessionID] = j
	due := s.Turns >= s.Threshold
	if t := l.timers[sessionID]; t != nil {
		t.Stop()
	}
	if !due {
		l.timers[sessionID] = time.AfterFunc(IdleAfter, func() {
			l.mu.Lock()
			j, ok := l.last[sessionID]
			l.mu.Unlock()
			if ok {
				l.enqueue(j)
			}
		})
	}
	l.save()
	l.mu.Unlock()
	if due {
		l.enqueue(j)
	}
}

// LearnNow runs extraction on a session at once, whatever the counters say,
// and waits for it.
func (l *Learner) LearnNow(ctx context.Context, root, sessionID string, msgs []session.Message) error {
	j := job{session: sessionID, root: root, msgs: append([]session.Message(nil), msgs...),
		force: true, done: make(chan error, 1)}
	l.enqueue(j)
	select {
	case err := <-j.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// StoreReport says what consolidating one store did.
type StoreReport struct {
	Dir          string `json:"dir"`
	Scope        string `json:"scope"`
	Records      int    `json:"records"`
	Folded       int    `json:"folded"`
	ScenesBefore int    `json:"scenes_before"`
	ScenesAfter  int    `json:"scenes_after"`
	Persona      bool   `json:"persona"`
	Note         string `json:"note,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Consolidate runs scenes and persona now on each store given, and waits. A
// store that fails does not stop the others; its report says why.
func (l *Learner) Consolidate(ctx context.Context, stores []*memory.Store) ([]StoreReport, error) {
	var reports []StoreReport
	j := job{scenesOnly: true, force: true, stores: stores, reports: &reports, done: make(chan error, 1)}
	l.enqueue(j)
	select {
	case err := <-j.done:
		return reports, err
	case <-ctx.Done():
		return reports, ctx.Err()
	}
}

func (l *Learner) enqueue(j job) {
	select {
	case l.jobs <- j:
	default:
		l.record(Activity{Stage: "note", Session: j.session, Root: j.root, Detail: "queue full; this turn will be learned with the next"})
		if j.done != nil {
			j.done <- errors.New("the learner is busy; try again shortly")
		}
	}
}

// Status is what the admin shows about the learner.
type Status struct {
	Model    string                   `json:"model"`
	Error    string                   `json:"error,omitempty"`
	Busy     string                   `json:"busy,omitempty"`
	Queue    int                      `json:"queue"`
	Sessions map[string]*SessionState `json:"sessions"`
	Stores   map[string]*StoreState   `json:"stores"`
}

func (l *Learner) Status() Status {
	st := Status{Queue: len(l.jobs)}
	if m, err := l.model(); err != nil {
		st.Error = err.Error()
	} else {
		st.Model = m.Name()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	st.Busy = l.busy
	b, _ := json.Marshal(l.st)
	var cp state
	_ = json.Unmarshal(b, &cp)
	st.Sessions, st.Stores = cp.Sessions, cp.Stores
	return st
}

func (l *Learner) model() (LLM, error) {
	want := config.LoadPrefs().LearnModel
	l.llmMu.Lock()
	defer l.llmMu.Unlock()
	if l.llm != nil && l.llmFor == want {
		return l.llm, nil
	}
	m, err := NewLLM(want)
	if err != nil {
		return nil, err
	}
	l.llm, l.llmFor = m, want
	return m, nil
}

func (l *Learner) loop() {
	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case j := <-l.jobs:
			err := l.run(j)
			if j.done != nil {
				j.done <- err
			}
		case <-tick.C:
			// Records that arrived while scenes were on cooldown are
			// consolidated once it is over.
			l.mu.Lock()
			var due []string
			for dir, s := range l.st.Stores {
				if len(s.Pending) > 0 && time.Since(s.LastScenes) >= SceneInterval {
					due = append(due, dir)
				}
			}
			l.mu.Unlock()
			for _, dir := range due {
				_, _ = l.runStore(context.Background(), storeAt(dir), false)
			}
		}
	}
}

// storeAt finds the memory store for a directory.
func storeAt(dir string) *memory.Store {
	if dir == memory.GlobalDir() {
		return memory.For("").Global
	}
	return memory.Open(dir, memory.Project)
}

func (l *Learner) run(j job) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	defer cancel()
	l.mu.Lock()
	l.busy = j.session
	if j.session == "" {
		l.busy = "consolidating memory"
	}
	l.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("learner panic: %v", r)
		}
		if err != nil {
			l.record(Activity{Stage: "error", Session: j.session, Root: j.root, Error: err.Error()})
		}
		l.mu.Lock()
		l.busy = ""
		l.mu.Unlock()
	}()

	llm, err := l.model()
	if err != nil {
		return err
	}
	l.llmMu.Lock()
	l.llm = llm
	l.llmMu.Unlock()

	bank := memory.For(j.root)
	if j.scenesOnly {
		stores := j.stores
		if stores == nil {
			stores = bank.Stores()
		}
		for _, st := range stores {
			rep, err := l.runStore(ctx, st, true)
			if err != nil {
				rep.Error = err.Error()
			}
			if j.reports != nil {
				*j.reports = append(*j.reports, rep)
			}
		}
		return nil
	}
	if !j.force && !Enabled(j.root) {
		return nil
	}

	l.mu.Lock()
	s := l.sess(j.session, j.root)
	if s.Cursor > len(j.msgs) {
		s.Cursor = 0 // the transcript was rewritten (a fork, a deletion)
	}
	cursor, prev := s.Cursor, s.Scene
	l.mu.Unlock()

	extracted := 0
	for cursor < len(j.msgs) {
		end := min(cursor+batchSize, len(j.msgs))
		newMsgs := toMsgs(j.msgs, cursor, end)
		bg := toMsgs(j.msgs, max(0, cursor-background*2), cursor)
		if len(bg) > background {
			bg = bg[len(bg)-background:]
		}
		if len(newMsgs) > 0 {
			mems, scene, err := l.extract(ctx, prev, bg, newMsgs)
			if err != nil {
				return fmt.Errorf("extract: %w", err)
			}
			prev = scene
			extracted += len(mems)
			if err := l.write(ctx, bank, j, mems); err != nil {
				return err
			}
		}
		cursor = end
		l.mu.Lock()
		s.Cursor, s.Scene = cursor, prev
		l.save()
		l.mu.Unlock()
	}

	l.mu.Lock()
	s.Turns = 0
	s.Threshold = min(s.Threshold*2, EveryN)
	if t := l.timers[j.session]; t != nil {
		t.Stop()
		delete(l.timers, j.session)
	}
	skillFrom := s.SkillCursor
	l.save()
	l.mu.Unlock()
	if extracted == 0 {
		l.record(Activity{Stage: "extract", Session: j.session, Root: j.root, Detail: "nothing worth keeping in the new messages"})
	}

	for _, st := range bank.Stores() {
		if _, err := l.runStore(ctx, st, false); err != nil {
			l.record(Activity{Stage: "error", Root: j.root, Error: "scenes: " + err.Error()})
		}
	}

	if skillsOn() {
		calls := 0
		for _, m := range j.msgs[min(skillFrom, len(j.msgs)):] {
			calls += len(m.Tools)
		}
		if calls >= SkillToolCalls || (j.force && calls > 0) {
			store := skill.For(j.root)
			res, err := l.reviewSkill(ctx, store, j.msgs, skillFrom)
			if err != nil {
				l.record(Activity{Stage: "error", Session: j.session, Root: j.root, Error: "skill review: " + err.Error()})
			} else {
				l.record(Activity{Stage: "skill", Session: j.session, Root: j.root, Detail: res})
				l.mu.Lock()
				s.SkillCursor = len(j.msgs)
				l.save()
				l.mu.Unlock()
			}
		}
	}
	return nil
}

// write splits memories by scope and consolidates each part into its store.
func (l *Learner) write(ctx context.Context, bank *memory.Bank, j job, mems []extracted) error {
	by := map[memory.Scope][]extracted{}
	for _, m := range mems {
		sc := m.scopeOf()
		if bank.Store(sc) == nil {
			sc = memory.Global
		}
		by[sc] = append(by[sc], m)
	}
	for _, scope := range []memory.Scope{memory.Global, memory.Project} {
		part := by[scope]
		if len(part) == 0 {
			continue
		}
		st := bank.Store(scope)
		out := l.consolidate(ctx, st, j.session, part)
		var ids []string
		var lines []string
		for _, r := range out.Records {
			ids = append(ids, r.ID)
			lines = append(lines, "["+r.Type+"] "+r.Content)
		}
		l.mu.Lock()
		ss := l.store(st.Dir)
		ss.Pending = append(ss.Pending, ids...)
		ss.SincePersona += len(ids)
		l.save()
		l.mu.Unlock()
		l.record(Activity{Stage: "extract", Session: j.session, Root: j.root, Records: ids, Scope: string(scope),
			Detail: fmt.Sprintf("%s: %d stored, %d updated, %d merged, %d skipped\n%s", scope,
				out.Stored, out.Updated, out.Merged, out.Skipped, strings.Join(lines, "\n"))})
	}
	return nil
}

// runStore runs L2 and L3 for one store when they are due (or now, forced).
func (l *Learner) runStore(ctx context.Context, st *memory.Store, force bool) (rep StoreReport, err error) {
	all := st.All()
	rep = StoreReport{Dir: st.Dir, Scope: string(st.Scope), Records: len(all), ScenesBefore: len(st.Scenes())}
	defer func() { rep.ScenesAfter = len(st.Scenes()) }()
	if len(all) == 0 {
		rep.Note = "no memories yet"
		return rep, nil
	}
	l.mu.Lock()
	ss := l.store(st.Dir)
	pending := append([]string(nil), ss.Pending...)
	due := len(pending) > 0 && time.Since(ss.LastScenes) >= SceneInterval
	l.mu.Unlock()

	var recs []memory.Record
	if force {
		// A forced run reconsiders everything not yet in a scene, or, when
		// nothing is pending, the newest records.
		if len(pending) == 0 {
			recs = all[:min(len(all), 30)]
		}
		want := map[string]bool{}
		for _, id := range pending {
			want[id] = true
		}
		for _, r := range all {
			if want[r.ID] {
				recs = append(recs, r)
			}
		}
	} else if due {
		for _, id := range pending {
			if r, ok := st.Get(id); ok {
				recs = append(recs, r)
			}
		}
	}

	if len(recs) > 0 {
		sort.Slice(recs, func(i, j int) bool { return recs[i].Updated.Before(recs[j].Updated) })
		ask, err := l.scenes(ctx, st, recs)
		if err != nil {
			return rep, err
		}
		rep.Folded = len(recs)
		l.mu.Lock()
		ss.LastScenes = time.Now().UTC()
		ss.Pending = nil
		ss.WantPersona = ss.WantPersona || ask
		l.save()
		l.mu.Unlock()
		names := []string{}
		for _, sc := range st.Scenes() {
			names = append(names, strings.TrimSuffix(sc.File, ".md"))
		}
		l.record(Activity{Stage: "scenes", Scope: string(st.Scope), Dir: st.Dir,
			Detail: fmt.Sprintf("folded %d memories into %d scenes\n%s", len(recs), len(names), strings.Join(names, " · "))})
	}

	l.mu.Lock()
	want := force || ss.WantPersona || ss.SincePersona >= personaEvery ||
		(st.Persona() == "" && len(st.Scenes()) > 0)
	l.mu.Unlock()
	if !want || len(st.Scenes()) == 0 {
		return rep, nil
	}
	before := st.Persona()
	if err := l.persona(ctx, st); err != nil {
		return rep, err
	}
	after := st.Persona()
	rep.Persona = true
	l.mu.Lock()
	ss.SincePersona, ss.WantPersona = 0, false
	l.save()
	l.mu.Unlock()
	// The global store's L3 is the user's persona; a project's is its
	// doctrine — the same step, written about a codebase instead of a person.
	what := "user persona (who you are and how you work)"
	if st.Scope == memory.Project {
		what = "project doctrine (how this codebase works)"
	}
	verb := "rewrote the "
	if before == "" {
		verb = "wrote the first "
	}
	l.record(Activity{Stage: "persona", Scope: string(st.Scope), Dir: st.Dir,
		Detail: fmt.Sprintf("%s%s — %d → %d characters, from %d scenes", verb, what, len([]rune(before)), len([]rune(after)), len(st.Scenes()))})
	return rep, nil
}

func (l *Learner) note(s string) { l.record(Activity{Stage: "note", Detail: s}) }

func (l *Learner) record(a Activity) {
	if a.At.IsZero() {
		a.At = time.Now().UTC()
	}
	b, err := json.Marshal(a)
	if err != nil {
		return
	}
	_ = os.MkdirAll(l.dir, 0o755)
	f, err := os.OpenFile(filepath.Join(l.dir, "activity.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// Activities returns the newest entries first.
func (l *Learner) Activities(limit int) []Activity {
	b, err := os.ReadFile(filepath.Join(l.dir, "activity.jsonl"))
	if err != nil {
		return []Activity{}
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > activityKeep*2 {
		// Trim the file now and then so it does not grow without end.
		keep := strings.Join(lines[len(lines)-activityKeep:], "\n") + "\n"
		_ = os.WriteFile(filepath.Join(l.dir, "activity.jsonl"), []byte(keep), 0o600)
		lines = lines[len(lines)-activityKeep:]
	}
	out := []Activity{}
	for i := len(lines) - 1; i >= 0 && len(out) < limit; i-- {
		var a Activity
		if json.Unmarshal([]byte(lines[i]), &a) == nil {
			out = append(out, a)
		}
	}
	return out
}
