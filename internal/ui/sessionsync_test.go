package ui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestStartupFollowsARestoredSession is the bug behind an empty explorer: a
// session restored inside a distribution came back with the tree built on the
// host and aimed at a POSIX path, which reads as an empty directory. The pane
// title gave it away — "workspace" rather than "Ubuntu-24.04:workspace",
// because the filesystem behind it was the local one.
func TestStartupFollowsARestoredSession(t *testing.T) {
	wslOrSkip(t)
	m := newTestModel(t)

	// Put the session inside the distribution, the way a restored one arrives.
	ready := runUntil[wslReadyMsg](t, m.enterWSL(""))
	if ready.err != "" {
		t.Skipf("WSL is registered but not startable here: %s", ready.err)
	}
	_, next := m.Update(ready)
	m.Update(runUntil[indexReadyMsg](t, next))

	s := m.mgr.Active()
	target, cwd := s.Target, s.CWD
	if !strings.HasPrefix(target, "wsl:") {
		t.Fatalf("session target = %q", target)
	}

	// Rebuild the tree exactly the way startup used to: the host filesystem,
	// pointed at whatever directory the session was saved in.
	m.tree.SetFS(m.hostFS, cwd)
	if rows := m.tree.Rows(); len(rows) > 1 {
		t.Fatalf("the host could read %q after all; this test proves nothing", cwd)
	}

	// Starting up has to notice and fix it.
	m.Update(runUntil[indexReadyMsg](t, m.showActiveSession()))

	if f := m.tree.FS(); f == nil || f.IsLocal() {
		t.Fatal("the tree stayed on the host for a session inside a distribution")
	}
	if m.tree.Root() != cwd {
		t.Errorf("tree root = %q, want %q", m.tree.Root(), cwd)
	}
	if m.idx.Root() != cwd {
		t.Errorf("index root = %q, want %q", m.idx.Root(), cwd)
	}
	if !strings.Contains(m.explorerTitle(), ":") {
		t.Errorf("the pane title does not name the filesystem: %q", m.explorerTitle())
	}
	// And it must list something rather than only the way out.
	if rows := m.tree.Rows(); len(rows) <= 1 {
		t.Errorf("the explorer is empty: %d rows", len(rows))
	}
	_ = target
}

// TestSwitchingSessionsMovesTheIndexToo: the tree moved and the index did not,
// so ctrl+p kept searching the project the previous session was in.
func TestSwitchingSessionsMovesTheIndexToo(t *testing.T) {
	m := newTestModel(t)
	root := m.idx.Root()

	m.input.SetValue("cd internal")
	m.Update(runUntil[indexReadyMsg](t, m.inputKey(key("enter"))))
	sub := filepath.Join(root, "internal")
	if m.idx.Root() != sub {
		t.Fatalf("cd left the index at %q", m.idx.Root())
	}

	// A new session starts at the project root; everything must follow it back.
	m.mgr.New()
	cmd := m.onSessionSwitch()
	if cmd == nil {
		t.Fatal("switching session produced no reindex")
	}
	m.Update(runUntil[indexReadyMsg](t, cmd))

	if m.tree.Root() != root {
		t.Errorf("tree root = %q, want %q", m.tree.Root(), root)
	}
	if m.idx.Root() != root {
		t.Errorf("the finder still points at %q, want %q", m.idx.Root(), root)
	}
	if m.sessionCWD(m.mgr.Active()) != m.tree.Root() {
		t.Errorf("the prompt would run in %q while the explorer shows %q",
			m.sessionCWD(m.mgr.Active()), m.tree.Root())
	}
}

// TestSwitchingBackAndForthKeepsPanesTogether walks two sessions in different
// directories, which is the shape the desync actually showed up in.
func TestSwitchingBackAndForthKeepsPanesTogether(t *testing.T) {
	m := newTestModel(t)
	root := m.idx.Root()
	sub := filepath.Join(root, "internal")

	m.input.SetValue("cd internal")
	m.Update(runUntil[indexReadyMsg](t, m.inputKey(key("enter"))))

	m.mgr.New()
	m.Update(runUntil[indexReadyMsg](t, m.onSessionSwitch()))

	for _, want := range []string{sub, root, sub} {
		m.mgr.Cycle(1)
		if cmd := m.onSessionSwitch(); cmd != nil {
			m.Update(runUntil[indexReadyMsg](t, cmd))
		}
		if m.tree.Root() != m.idx.Root() {
			t.Fatalf("tree %q and index %q disagree", m.tree.Root(), m.idx.Root())
		}
		if m.tree.Root() != m.sessionCWD(m.mgr.Active()) {
			t.Fatalf("panes at %q, session at %q",
				m.tree.Root(), m.sessionCWD(m.mgr.Active()))
		}
		_ = want
	}
}

// TestSwitchingToTheSameSessionDoesNoWork keeps the reindex from firing on
// every keystroke that happens to call through here.
func TestSwitchingToTheSameSessionDoesNoWork(t *testing.T) {
	m := newTestModel(t)
	// Init batches in watchTasks, which blocks on the task registry by design;
	// draining it here would never return. The panes are already in sync from
	// newTestModel, which is all this needs.
	if cmd := m.onSessionSwitch(); cmd != nil {
		t.Error("switching to the session already shown rebuilt the index")
	}
}

// TestDeferredWorkReachesTheRuntime covers the path that cannot return a
// command: the approval queue pulls a session to the front from inside the
// event pump, and the reindex that needs still has to run.
func TestDeferredWorkReachesTheRuntime(t *testing.T) {
	m := newTestModel(t)

	var ran bool
	m.defer_(func() tea.Msg { ran = true; return nil })
	if len(m.deferred) != 1 {
		t.Fatalf("deferred = %d", len(m.deferred))
	}

	_, cmd := m.Update(tickMsg{})
	if cmd == nil {
		t.Fatal("Update dropped the deferred work")
	}
	drain(cmd)
	if !ran {
		t.Error("the deferred command never ran")
	}
	if len(m.deferred) != 0 {
		t.Error("the queue was not cleared")
	}
	// A nil command must not pile up.
	m.defer_(nil)
	if len(m.deferred) != 0 {
		t.Error("a nil command was queued")
	}
}

// drain runs a command and everything a batch holds.
func drain(cmd tea.Cmd) {
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		if batch, ok := c().(tea.BatchMsg); ok {
			queue = append(queue, batch...)
		}
	}
}
