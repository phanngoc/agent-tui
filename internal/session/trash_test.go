package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// talked makes a conversation with something in it, saved.
func talked(m *Manager, text string) *Session {
	s := m.New()
	s.Append(Message{Role: RoleUser, Text: text})
	m.write(s.clone())
	return s
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Deleting takes a conversation out of the list and its file into the trash,
// and undoing puts both back where they were.
func TestDeleteGoesToTheTrashAndComesBack(t *testing.T) {
	dir, root := t.TempDir(), "/project"
	m := NewManager(dir, root, "m")
	t.Cleanup(m.Shutdown)
	m.Restore(10)
	a := talked(m, "a")
	b := talked(m, "b") // list: b, a, (the empty first one)
	file := m.path(b)

	d := m.Delete(b.ID)
	if m.IndexOf(b.ID) >= 0 {
		t.Fatal("the deleted conversation is still listed")
	}
	if exists(file) || !exists(filepath.Join(m.trashDir(), b.ID+".json")) {
		t.Fatal("the file did not move to the trash")
	}

	m.Undelete(d)
	if got := m.IndexOf(b.ID); got != 0 {
		t.Errorf("the conversation came back at %d, want where it stood (0)", got)
	}
	if m.Active() != b {
		t.Error("undoing did not put the cursor back on it")
	}
	if !exists(file) {
		t.Error("the file did not come back from the trash")
	}
	if m.IndexOf(a.ID) != 1 {
		t.Error("undoing moved the conversation beside it")
	}
}

// A save queued before the delete must not write the file straight back.
func TestAQueuedSaveDoesNotResurrectADeletedConversation(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, "/project", "m")
	s := talked(m, "doomed")
	m.Delete(s.ID)
	m.Save(s)
	m.write(s.clone())
	m.Shutdown()
	if exists(m.path(s)) {
		t.Error("a save after the delete wrote the conversation back")
	}
}

// A conversation's side chat goes with it, and comes back with it.
func TestDeleteTakesTheSideChat(t *testing.T) {
	m := NewManager(t.TempDir(), "/project", "m")
	t.Cleanup(m.Shutdown)
	parent := talked(m, "parent")
	side := m.Fork(parent)
	side.SideOf = parent.ID
	m.write(side.clone())

	d := m.Delete(parent.ID)
	if m.Get(side.ID) != nil {
		t.Error("the side chat outlived its conversation")
	}
	if len(d.Sessions()) != 2 {
		t.Errorf("the delete took %d sessions, want 2", len(d.Sessions()))
	}
	m.Undelete(d)
	if m.SideOf(parent.ID) != side {
		t.Error("the side chat did not come back with its conversation")
	}
}

// Deleting the last conversation leaves a fresh one, which undoing replaces.
func TestDeletingTheLastOneLeavesAFreshOne(t *testing.T) {
	m := NewManager(t.TempDir(), "/project", "m")
	t.Cleanup(m.Shutdown)
	only := talked(m, "only")
	d := m.Delete(only.ID)
	if m.Len() != 1 || len(m.Active().Messages) != 0 {
		t.Fatalf("after deleting the last one: %d sessions", m.Len())
	}
	m.Undelete(d)
	if m.Len() != 1 || m.Active() != only {
		t.Errorf("undoing left %d sessions, active %q", m.Len(), m.Active().Label())
	}
}

// The trash keeps a week.
func TestPurgeTrashEmptiesWhatIsOld(t *testing.T) {
	m := NewManager(t.TempDir(), "/project", "m")
	t.Cleanup(m.Shutdown)
	old, recent := talked(m, "old"), talked(m, "recent")
	m.Delete(old.ID, recent.ID)
	aged := filepath.Join(m.trashDir(), old.ID+".json")
	then := time.Now().Add(-TrashRetention - time.Hour)
	if err := os.Chtimes(aged, then, then); err != nil {
		t.Fatal(err)
	}

	m.PurgeTrash(TrashRetention)
	if exists(aged) {
		t.Error("a conversation deleted over a week ago is still in the trash")
	}
	if !exists(filepath.Join(m.trashDir(), recent.ID+".json")) {
		t.Error("a conversation deleted just now was purged")
	}
}

// Closing is not deleting: the next start does not restore a closed
// conversation, and opening it again — what /recall does — does.
func TestClosedConversationsStayClosedAcrossRestarts(t *testing.T) {
	dir, root := t.TempDir(), "/project"
	m := NewManager(dir, root, "m")
	m.Restore(10)
	kept, closed := talked(m, "kept"), talked(m, "closed")
	m.Close(m.IndexOf(closed.ID))
	m.Shutdown()

	m2 := NewManager(dir, root, "m")
	t.Cleanup(m2.Shutdown)
	m2.Restore(10)
	if m2.IndexOf(closed.ID) >= 0 {
		t.Error("a closed conversation came back on restart")
	}
	if m2.IndexOf(kept.ID) < 0 {
		t.Error("an open conversation did not come back")
	}
	s, _, err := m2.Open(closed.ID)
	if err != nil || s.Closed {
		t.Errorf("opening the closed conversation: err=%v closed=%v", err, s != nil && s.Closed)
	}
}

// Bump floats a conversation to the top and leaves the cursor where it was.
func TestBumpFloatsAConversationAndKeepsTheCursor(t *testing.T) {
	m := NewManager(t.TempDir(), "/project", "m")
	t.Cleanup(m.Shutdown)
	c := talked(m, "c")
	b := talked(m, "b")
	a := talked(m, "a") // list: a, b, c
	m.Select(m.IndexOf(b.ID))

	m.Bump(c)
	if m.IndexOf(c.ID) != 0 || m.IndexOf(a.ID) != 1 || m.IndexOf(b.ID) != 2 {
		t.Errorf("order after bump: c=%d a=%d b=%d", m.IndexOf(c.ID), m.IndexOf(a.ID), m.IndexOf(b.ID))
	}
	if m.Active() != b {
		t.Error("bumping another conversation moved the cursor off this one")
	}

	// A side chat's turn floats the conversation it belongs to.
	side := m.Fork(b)
	side.SideOf = b.ID
	m.Bump(side)
	if m.IndexOf(b.ID) != 0 {
		t.Errorf("the side chat's turn did not float its conversation: b=%d", m.IndexOf(b.ID))
	}
}
