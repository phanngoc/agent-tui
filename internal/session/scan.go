package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Reading every conversation, without opening any of them.
//
// The transcript is the only record here that is complete, durable and neutral
// between engines — so it is the only thing worth searching. But searching it
// must not mean loading it: a session is the thing you read and go on talking
// to, and that is a different object from the thing you look through.
//
// So there are two projections of the same file. Session is all of it. Entry is
// the header plus the prose, which is eight per cent of the bytes; decoding into
// a narrowed struct makes encoding/json skip the tool results rather than
// allocate them and throw them away.
//
// Restore cannot serve this. It filters by project root, caps at a limit, and
// replaces the whole session list — all three correct for starting up, all three
// wrong for "show me that conversation again".

// Entry is one conversation as searching sees it.
type Entry struct {
	ID      string
	Title   string
	Root    string
	Updated time.Time
	// Texts and Roles are parallel and indexed by message position. Empty
	// strings are kept rather than compacted: the index is the address the
	// transcript will be scrolled to, so it may not shift.
	Texts []string
	Roles []string
	// Live marks an entry taken from an open session, which may hold messages
	// that have not reached the disk yet.
	Live bool
}

// scanDoc is the session file seen through a slit.
//
// Its tags have to track Session's. A rename there and not here yields no hits
// rather than an error, which is why scan_test round-trips a real Session
// through json.Marshal and back through this.
type scanDoc struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Root     string    `json:"root"`
	SideOf   string    `json:"side_of"`
	Updated  time.Time `json:"updated"`
	Messages []struct {
		Role string `json:"role"`
		Text string `json:"text"`
	} `json:"messages"`
}

// LiveEntries snapshots the open conversations.
//
// It must be called from the goroutine that owns them: nothing else may read
// Session.Messages while a turn is appending to it, which is the same hazard
// clone() exists for. The disk copy of an open session is always behind — a
// queued save can be dropped — so the live one wins wherever both exist.
func (m *Manager) LiveEntries() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Entry, 0, len(m.sessions))
	for _, s := range m.sessions {
		if s.SideOf != "" || len(s.Messages) == 0 {
			continue
		}
		out = append(out, entryOf(s.ID, s.Title, s.Root, s.Updated, s.Messages, true))
	}
	return out
}

// DiskEntries reads every session in the store, from every project. Ids in skip
// are left out, because a live copy of them has already been taken.
//
// It touches only the filesystem, so it is safe on any goroutine.
func (m *Manager) DiskEntries(ctx context.Context, skip map[string]bool) []Entry {
	names, err := os.ReadDir(m.dir)
	if err != nil {
		return nil
	}
	out := make([]Entry, 0, len(names))
	for _, e := range names {
		if ctx != nil && ctx.Err() != nil {
			return out
		}
		// The same filter Restore uses, and the reason the .tmp files write
		// leaves behind never show up as conversations.
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(m.dir, e.Name()))
		if err != nil {
			continue
		}
		var d scanDoc
		// One unreadable file may not blind the search.
		if json.Unmarshal(b, &d) != nil || d.ID == "" || skip[d.ID] || d.SideOf != "" {
			continue
		}
		en := Entry{ID: d.ID, Title: d.Title, Root: d.Root, Updated: d.Updated}
		for _, msg := range d.Messages {
			en.Texts = append(en.Texts, msg.Text)
			en.Roles = append(en.Roles, msg.Role)
		}
		if len(en.Texts) == 0 {
			continue
		}
		out = append(out, en)
	}
	return out
}

func entryOf(id, title, root string, updated time.Time, msgs []Message, live bool) Entry {
	e := Entry{ID: id, Title: title, Root: root, Updated: updated, Live: live}
	for i := range msgs {
		e.Texts = append(e.Texts, msgs[i].Text)
		e.Roles = append(e.Roles, msgs[i].Role)
	}
	return e
}

// ErrNoSession means there is no saved conversation with that id.
var ErrNoSession = errors.New("no such session")

// IndexOf is the position of a session by id, or -1.
func (m *Manager) IndexOf(id string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i, s := range m.sessions {
		if s.ID == id {
			return i
		}
	}
	return -1
}

// Open loads one saved conversation and puts it at the front of the list,
// whatever project it belongs to.
//
// The lookup and the insert happen under one lock on purpose. Two *Session for
// one id is the worst thing that can go wrong here — both would be saved, each
// clobbering the other through the same write — and holding the lock across
// both makes that unrepresentable rather than merely unlikely.
func (m *Manager) Open(id string) (*Session, int, error) {
	if !safeID(id) {
		return nil, -1, ErrNoSession
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, s := range m.sessions {
		if s.ID == id {
			return s, i, nil
		}
	}
	b, err := os.ReadFile(filepath.Join(m.dir, id+".json"))
	if err != nil {
		return nil, -1, ErrNoSession
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil || s.ID == "" {
		return nil, -1, ErrNoSession
	}
	s.normalise()
	m.sessions = append([]*Session{&s}, m.sessions...)
	if m.active >= 0 {
		m.active++
	}
	return &s, 0, nil
}

// safeID rejects anything that could reach outside the store. The ids this
// writes are safe by construction, but the one that arrives here came back from
// a search result rather than from newID.
func safeID(id string) bool {
	if id == "" || len(id) > 128 || id == "." || id == ".." {
		return false
	}
	return !strings.ContainsAny(id, `/\:`) && !strings.Contains(id, "..")
}
