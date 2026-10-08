package server

import (
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/session"
)

// /btw asks in a side chat: one made on demand, kept while the conversation
// stays where it was, and made afresh once it has moved on.
func TestBtwSideChat(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	m := session.NewManager(config.DataDir(), root, "")
	s := m.New()
	s.Append(session.Message{Role: session.RoleUser, Text: "hello", At: time.Now()})
	s.Append(session.Message{Role: session.RoleAssistant, Text: "hi", At: time.Now()})
	m.SaveNow(s)
	m.Shutdown()

	srv := New(config.Default(), "test", "")
	first, err := srv.sideFor(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	side, err := gateway.Load(first)
	if err != nil || side.SideOf != s.ID || side.SideFrom != 2 || len(side.Messages) != 2 {
		t.Fatalf("side chat: %+v %v", side, err)
	}
	if got := srv.sideOf(s.ID); got != first {
		t.Fatalf("sideOf = %q, want %q", got, first)
	}
	if again, _ := srv.sideFor(s.ID); again != first {
		t.Fatalf("asked again with nothing new: %q, want the same %q", again, first)
	}
	if _, err := srv.sideFor(first); err == nil {
		t.Fatal("made a side chat of a side chat")
	}

	// The conversation moves on: the old side chat would not know it.
	parent, _ := gateway.Load(s.ID)
	parent.Append(session.Message{Role: session.RoleUser, Text: "next", At: time.Now()})
	pm := session.NewManager(config.DataDir(), root, "")
	pm.SaveNow(parent)
	pm.Shutdown()
	fresh, err := srv.sideFor(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh == first {
		t.Fatal("kept a side chat forked before the conversation moved on")
	}
	if _, err := gateway.Load(first); err == nil {
		t.Fatal("the stale side chat is still there")
	}
	if got, _ := gateway.Load(fresh); got.SideFrom != 3 {
		t.Fatalf("fresh side chat forked from %d messages, want 3", got.SideFrom)
	}
}
