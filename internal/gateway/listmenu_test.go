package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/session"
)

// The web list's menu, on a session the gateway holds: rename, pin, archive,
// fork, delete and restore.
func TestListMenuOnAGatewaySession(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	m := session.NewManager(config.DataDir(), root, "")
	s := m.New()
	s.Title = "first"
	s.Append(session.Message{Role: session.RoleUser, Text: "hello", At: time.Now()})
	s.Append(session.Message{Role: session.RoleAssistant, Text: "hi", At: time.Now()})
	m.SaveNow(s)
	m.Shutdown()

	h := NewHub()
	r := NewRunner(h, config.Default())
	h.Local = r

	name, yes := "  Dev2   migration ", true
	if _, err := h.Route(Command{Type: CmdSettings, Session: s.ID, Title: &name, Pinned: &yes, Archived: &yes}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Dev2 migration" || !got.Pinned || !got.Closed || len(got.Messages) != 2 {
		t.Fatalf("after settings: title %q pinned %v closed %v messages %d", got.Title, got.Pinned, got.Closed, len(got.Messages))
	}

	// A chapter on the answer; one out of range is ignored; unpinning removes it.
	for _, c := range []ChapterMark{{At: 1, On: true}, {At: 0, On: true}, {At: 9, On: true}, {At: 0, On: false}} {
		if _, err := h.Route(Command{Type: CmdSettings, Session: s.ID, Chapter: &c}); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := Load(s.ID); len(got.Chapters) != 1 || got.Chapters[0] != 1 {
		t.Fatalf("chapters: %v", got.Chapters)
	}

	f, err := r.Fork(s.ID, -1)
	if err != nil {
		t.Fatal(err)
	}
	fork, err := Load(f.ID)
	if err != nil || len(fork.Messages) != 2 || fork.ID == s.ID || fork.Root != root || fork.Closed || fork.Pinned || len(fork.Chapters) != 1 {
		t.Fatalf("fork: %+v %v", fork, err)
	}
	// From the first message: the transcript up to it, and no engine
	// conversation to resume, since that one goes on past it.
	src, _ := Load(s.ID)
	src.ExternalID = "engine-conv"
	src.Engines = map[string]session.EngineState{"claude": {ExternalID: "engine-conv", Seen: 2}}
	sm := session.NewManager(config.DataDir(), root, "")
	sm.SaveNow(src)
	sm.Shutdown()
	part, err := r.Fork(s.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := Load(part.ID); len(p.Messages) != 1 || p.ExternalID != "" || len(p.Engines) != 0 || p.ForkPending || len(p.Chapters) != 0 {
		t.Fatalf("fork from here: %d messages, external %q, engines %v, pending %v, chapters %v", len(p.Messages), p.ExternalID, p.Engines, p.ForkPending, p.Chapters)
	}
	if _, err := r.Fork(s.ID, 5); err == nil {
		t.Fatal("forked from a message that is not there")
	}

	if _, err := h.Route(Command{Type: CmdDelete, Session: s.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(s.ID); !errors.Is(err, session.ErrNoSession) {
		t.Fatalf("still there after delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.DataDir(), "sessions", "trash", s.ID+".json")); err != nil {
		t.Fatalf("not in the trash: %v", err)
	}
	if err := r.Restore(s.ID); err != nil {
		t.Fatal(err)
	}
	if back, err := Load(s.ID); err != nil || back.Title != "Dev2 migration" {
		t.Fatalf("restore: %+v %v", back, err)
	}
	if _, err := h.Route(Command{Type: CmdDelete, Session: "nope"}); err == nil {
		t.Fatal("deleting a session that is not there succeeded")
	}
}
