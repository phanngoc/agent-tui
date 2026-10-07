package session

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Deleting a conversation, and taking it back.
//
// A conversation here is private and local: nobody else loses it, and the
// person deleting it is the only one who could be surprised. So deleting does
// not ask first — a dialog in front of every delete is one that gets answered
// without being read — it acts at once and offers the way back, the way a mail
// client does. The file goes to a trash folder rather than away, which is what
// makes the way back real: it survives the program closing, and the trash is
// emptied of anything older than a week the next time it starts.
//
// Closing is a different thing and stays one. A closed conversation leaves the
// list and stays in the store, where /recall finds it; a deleted one leaves
// both.

// TrashRetention is how long a deleted conversation can be brought back.
const TrashRetention = 7 * 24 * time.Hour

// Deleted is one delete, kept so it can be undone: the conversations it took
// and where in the list each one stood.
type Deleted struct {
	items []deletedItem
}

type deletedItem struct {
	s   *Session
	idx int
}

// Sessions are the conversations a delete took, side chats included.
func (d *Deleted) Sessions() []*Session {
	if d == nil {
		return nil
	}
	out := make([]*Session, len(d.items))
	for i, it := range d.items {
		out[i] = it.s
	}
	return out
}

func (m *Manager) trashDir() string { return filepath.Join(m.dir, "trash") }

// Delete takes conversations out of the list and their files out of the store,
// into the trash. A conversation's side chat goes with it. The returned record
// undoes it.
func (m *Manager) Delete(ids ...string) *Deleted {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}

	m.mu.Lock()
	for _, s := range m.sessions {
		if s.SideOf != "" && want[s.SideOf] {
			want[s.ID] = true
		}
	}
	d := &Deleted{}
	activeID := ""
	if m.active >= 0 && m.active < len(m.sessions) {
		activeID = m.sessions[m.active].ID
	}
	keep := m.sessions[:0:0]
	for i, s := range m.sessions {
		if want[s.ID] {
			d.items = append(d.items, deletedItem{s: s, idx: i})
			continue
		}
		keep = append(keep, s)
	}
	if len(d.items) == 0 {
		m.mu.Unlock()
		return nil
	}
	m.sessions = keep
	// The cursor stays on the conversation it was on if that one survived,
	// and otherwise lands where the deleted one stood.
	m.active = -1
	for i, s := range m.sessions {
		if s.ID == activeID {
			m.active = i
		}
	}
	if m.active < 0 {
		m.active = max(0, min(d.items[0].idx, len(m.sessions)-1))
	}
	if len(m.sessions) > 0 {
		m.settle()
	}
	if m.gone == nil {
		m.gone = map[string]bool{}
	}
	for _, it := range d.items {
		m.gone[it.s.ID] = true
	}
	empty := len(m.sessions) == 0
	m.mu.Unlock()

	// A save already queued would write the file straight back; gone is what
	// the writer checks, and it is set before the files move.
	_ = os.MkdirAll(m.trashDir(), 0o755)
	now := time.Now()
	for _, it := range d.items {
		to := filepath.Join(m.trashDir(), it.s.ID+".json")
		if os.Rename(m.path(it.s), to) == nil {
			// Renaming keeps the file's time, which is when it was last
			// saved; the trash counts from when it arrived.
			_ = os.Chtimes(to, now, now)
		}
	}
	if empty {
		m.New()
	}
	return d
}

// Undelete puts a delete back: each conversation where it stood, and its file
// back in the store.
func (m *Manager) Undelete(d *Deleted) {
	if d == nil || len(d.items) == 0 {
		return
	}
	items := append([]deletedItem(nil), d.items...)
	sort.Slice(items, func(i, j int) bool { return items[i].idx < items[j].idx })

	for _, it := range items {
		_ = os.Rename(filepath.Join(m.trashDir(), it.s.ID+".json"), m.path(it.s))
	}

	m.mu.Lock()
	// An untouched conversation made because the delete emptied the list is
	// a placeholder, and the conversations coming back replace it.
	if len(m.sessions) == 1 && len(m.sessions[0].Messages) == 0 {
		m.sessions = m.sessions[:0]
	}
	for _, it := range items {
		at := min(it.idx, len(m.sessions))
		m.sessions = append(m.sessions, nil)
		copy(m.sessions[at+1:], m.sessions[at:])
		m.sessions[at] = it.s
		delete(m.gone, it.s.ID)
	}
	m.active = min(items[0].idx, len(m.sessions)-1)
	m.settle()
	m.mu.Unlock()

	for _, it := range items {
		m.Save(it.s)
	}
}

// Forget marks a conversation deleted for this manager without it having to
// be in the list: a gateway runs conversations it never lists, and a save of
// one still queued must not write it back out of the trash.
func (m *Manager) Forget(id string) {
	m.mu.Lock()
	if m.gone == nil {
		m.gone = map[string]bool{}
	}
	m.gone[id] = true
	m.mu.Unlock()
}

// Revive undoes Forget, for a conversation brought back from the trash.
func (m *Manager) Revive(id string) {
	m.mu.Lock()
	delete(m.gone, id)
	m.mu.Unlock()
}

// TrashFile moves a conversation's file in the store at dir (the data
// directory) to the trash, as Delete does for one in a manager's list.
func TrashFile(dir, id string) error {
	store := filepath.Join(dir, "sessions")
	_ = os.MkdirAll(filepath.Join(store, "trash"), 0o755)
	to := filepath.Join(store, "trash", id+".json")
	if err := os.Rename(filepath.Join(store, id+".json"), to); err != nil {
		return err
	}
	now := time.Now()
	_ = os.Chtimes(to, now, now)
	return nil
}

// UntrashFile brings a conversation's file back from the trash.
func UntrashFile(dir, id string) error {
	store := filepath.Join(dir, "sessions")
	if _, err := os.Stat(filepath.Join(store, id+".json")); err == nil {
		return nil
	}
	return os.Rename(filepath.Join(store, "trash", id+".json"), filepath.Join(store, id+".json"))
}

// PurgeTrash empties the trash of anything deleted longer ago than maxAge.
func (m *Manager) PurgeTrash(maxAge time.Duration) {
	entries, err := os.ReadDir(m.trashDir())
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(m.trashDir(), e.Name()))
		}
	}
}
