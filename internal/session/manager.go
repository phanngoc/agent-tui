package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Manager owns every open session plus the persistence of all of them. Saves
// run on a background goroutine so a slow disk never stalls a keystroke.
type Manager struct {
	dir   string
	root  string
	model string

	mu       sync.RWMutex
	sessions []*Session
	active   int

	saves   chan *Session
	stopped chan struct{}

	// closeMu guards the saves channel against a send racing its close.
	closeMu   sync.RWMutex
	closed    bool
	closeOnce sync.Once
}

func NewManager(dir, root, model string) *Manager {
	m := &Manager{
		dir:     filepath.Join(dir, "sessions"),
		root:    root,
		model:   model,
		saves:   make(chan *Session, 64),
		stopped: make(chan struct{}),
	}
	_ = os.MkdirAll(m.dir, 0o755)
	go m.saveLoop()
	return m
}

// Shutdown flushes queued saves and stops the writer goroutine. It is safe to
// call more than once, and Save is a no-op afterwards.
func (m *Manager) Shutdown() {
	m.closeOnce.Do(func() {
		m.closeMu.Lock()
		m.closed = true
		close(m.saves)
		m.closeMu.Unlock()
		<-m.stopped
	})
}

// Restore loads previously saved sessions for this project root, newest first,
// capped at limit. A project with no history gets one empty session.
func (m *Manager) Restore(limit int) {
	entries, _ := os.ReadDir(m.dir)
	var loaded []*Session
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(m.dir, e.Name()))
		if err != nil {
			continue
		}
		var s Session
		if json.Unmarshal(b, &s) != nil || s.Root != m.root || len(s.Messages) == 0 {
			continue
		}
		s.normalise()
		loaded = append(loaded, &s)
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].Updated.After(loaded[j].Updated) })
	if len(loaded) > limit {
		loaded = loaded[:limit]
	}

	m.mu.Lock()
	m.sessions = loaded
	m.mu.Unlock()

	if len(loaded) == 0 {
		m.New()
	}
}

// New creates a session, makes it active and returns it.
func (m *Manager) New() *Session {
	s := &Session{
		ID:      newID(),
		Root:    m.root,
		CWD:     m.root,
		Model:   m.model,
		Created: time.Now(),
		Updated: time.Now(),
	}
	m.mu.Lock()
	m.sessions = append([]*Session{s}, m.sessions...)
	m.active = 0
	m.mu.Unlock()
	return s
}

// Active returns the focused session, creating one if the list somehow emptied.
func (m *Manager) Active() *Session {
	m.mu.RLock()
	if m.active >= 0 && m.active < len(m.sessions) {
		s := m.sessions[m.active]
		m.mu.RUnlock()
		return s
	}
	m.mu.RUnlock()
	return m.New()
}

func (m *Manager) ActiveIndex() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

// SideOf returns the side chat belonging to a session, if it has one.
func (m *Manager) SideOf(id string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.sessions {
		if s.SideOf == id {
			return s
		}
	}
	return nil
}

// All returns a snapshot of the session list.
func (m *Manager) All() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Session, len(m.sessions))
	copy(out, m.sessions)
	return out
}

func (m *Manager) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// Select focuses a session by index, ignoring out-of-range values.
func (m *Manager) Select(i int) {
	m.mu.Lock()
	if i >= 0 && i < len(m.sessions) && m.sessions[i].SideOf == "" {
		m.active = i
	}
	m.mu.Unlock()
}

// settle moves the cursor off a side chat, and must be called with the lock
// held.
//
// The invariant it keeps is worth stating plainly: whatever is active is
// something the session list shows. An aside belongs to the conversation it
// hangs off and is deliberately absent from every list of conversations, so a
// cursor resting on one points at a session nothing draws — the transcript
// fills with it, its title says what it is doing, and every row of the list
// says idle. A screen that contradicts itself, from one integer.
func (m *Manager) settle() {
	if m.active >= 0 && m.active < len(m.sessions) && m.sessions[m.active].SideOf == "" {
		return
	}
	for i := m.active; i < len(m.sessions); i++ {
		if m.sessions[i].SideOf == "" {
			m.active = i
			return
		}
	}
	for i := min(m.active, len(m.sessions)-1); i >= 0; i-- {
		if m.sessions[i].SideOf == "" {
			m.active = i
			return
		}
	}
	m.active = 0
}

// Cycle steps to the next conversation, skipping the side chats: they are
// reached through the conversation they hang off, not by cycling past them.
func (m *Manager) Cycle(delta int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.sessions)
	if n == 0 || delta == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	for i, at := 0, m.active; i < n; i++ {
		at = ((at+step)%n + n) % n
		if m.sessions[at].SideOf == "" {
			m.active = at
			return
		}
	}
}

// Close removes the session at i. The last remaining session is replaced by a
// fresh empty one rather than leaving the app with nothing to show.
func (m *Manager) Close(i int) {
	m.mu.Lock()
	if i < 0 || i >= len(m.sessions) {
		m.mu.Unlock()
		return
	}
	victim := m.sessions[i]
	// A side chat belongs to the conversation it hangs off, so it goes with
	// it. Left behind it would be a session nothing lists and nothing can
	// reach, which is a leak with a name.
	keep := m.sessions[:0]
	var orphans []*Session
	for j, s := range m.sessions {
		switch {
		case j == i:
		case s.SideOf == victim.ID:
			orphans = append(orphans, s)
		default:
			keep = append(keep, s)
		}
	}
	m.sessions = keep
	if i < len(m.sessions)+1 && m.active > i {
		m.active--
	}
	if m.active >= len(m.sessions) {
		m.active = len(m.sessions) - 1
	}
	if m.active < 0 {
		m.active = 0
	}
	// Closing renumbers everything, and the index it lands on is only an
	// index: it can come to rest on an aside, which no list draws.
	m.settle()
	empty := len(m.sessions) == 0
	m.mu.Unlock()

	if len(victim.Messages) == 0 {
		_ = os.Remove(m.path(victim))
	}
	for _, o := range orphans {
		_ = os.Remove(m.path(o))
	}
	if empty {
		m.New()
	}
}

// Save queues a session write. It never blocks: if the queue is full the
// session stays dirty and will be written by the next save.
func (m *Manager) Save(s *Session) {
	if s == nil || len(s.Messages) == 0 {
		return
	}
	snapshot := s.clone()

	m.closeMu.RLock()
	defer m.closeMu.RUnlock()
	if m.closed {
		return
	}
	select {
	case m.saves <- snapshot:
	default:
	}
}

// SaveAll flushes every dirty session synchronously, for shutdown.
func (m *Manager) SaveAll() {
	for _, s := range m.All() {
		if len(s.Messages) > 0 {
			m.write(s.clone())
		}
	}
}

func (m *Manager) saveLoop() {
	defer close(m.stopped)
	for s := range m.saves {
		m.write(s)
	}
}

func (m *Manager) write(s *Session) {
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	p := m.path(s)
	tmp := p + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, p)
	}
}

func (m *Manager) path(s *Session) string {
	return filepath.Join(m.dir, s.ID+".json")
}

// clone snapshots the persisted fields so the writer goroutine never races the
// UI goroutine that keeps mutating the live session.
//
// The copy has to reach inside each message: a tool call is filled in after its
// message is already in the transcript, so a shallow copy would leave the
// writer marshalling the very slice the UI is still writing to.
func (s *Session) clone() *Session {
	c := *s
	c.Messages = make([]Message, len(s.Messages))
	for i, m := range s.Messages {
		if len(m.Tools) > 0 {
			m.Tools = append([]ToolCall(nil), m.Tools...)
		}
		c.Messages[i] = m
	}
	c.Busy, c.Status, c.Partial, c.LastErr, c.Dirty = false, "", "", "", false
	return &c
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}

// Fork branches a session: the new one starts with the same transcript, engine
// and working directory, and its first turn continues the original's context
// rather than starting cold.
//
// The source is left exactly as it was, which is the point — a fork is for
// trying a different direction without losing the one you had.
func (m *Manager) Fork(src *Session) *Session {
	if src == nil {
		return m.New()
	}
	s := m.New()

	s.Engine, s.Model = src.Engine, src.Model
	s.CWD, s.Target = src.CWD, src.Target
	s.Title = forkTitle(src.Label())

	s.Messages = make([]Message, len(src.Messages))
	for i, msg := range src.Messages {
		if len(msg.Tools) > 0 {
			msg.Tools = append([]ToolCall(nil), msg.Tools...)
		}
		s.Messages[i] = msg
	}

	// The engine's own conversation is branched on the next turn; until then
	// the copied transcript is all there is.
	//
	// Only the engine that was running is carried over. Another engine's id in
	// here still points at the parent's own conversation, and resuming it
	// without a fork flag would write this session's turns into the one it
	// came from.
	s.ExternalID = src.ExternalID
	s.ForkPending = src.ExternalID != ""
	if src.ExternalID != "" {
		s.Engines = map[string]EngineState{
			src.Engine: {ExternalID: src.ExternalID, Seen: len(s.Messages)},
		}
	}
	s.Updated = time.Now()
	m.Save(s)
	return s
}

// forkTitle keeps the lineage visible without growing without bound.
func forkTitle(base string) string {
	const marker = " (fork"
	if i := strings.LastIndex(base, marker); i >= 0 {
		if n := strings.TrimSuffix(base[i+len(marker):], ")"); n != "" {
			if count, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
				return base[:i] + marker + " " + strconv.Itoa(count+1) + ")"
			}
		}
		return base[:i] + marker + " 2)"
	}
	return base + marker + ")"
}
