package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/session"
)

// moves are the records a session kept of being moved.
func moves(m *Model) []*session.ShellRun {
	var out []*session.ShellRun
	for i := range m.mgr.Active().Messages {
		if r := m.mgr.Active().Messages[i].Shell; r != nil {
			out = append(out, r)
		}
	}
	return out
}

// Every way of moving leaves the same record, because they are the same event:
// after any of them, a path the agent was given means somewhere else.
func TestEveryMoveIsRecorded(t *testing.T) {
	root := ""
	for _, tc := range []struct {
		name string
		do   func(t *testing.T, m *Model)
	}{
		{"!cd", func(t *testing.T, m *Model) {
			m.input.SetValue("!cd internal")
			m.Update(runUntil[indexReadyMsg](t, m.inputKey(key("enter"))))
		}},
		{"a bare cd", func(t *testing.T, m *Model) {
			m.input.SetValue("cd internal")
			m.Update(runUntil[indexReadyMsg](t, m.inputKey(key("enter"))))
		}},
		{"/cd", func(t *testing.T, m *Model) {
			m.input.SetValue("/cd internal")
			m.Update(runUntil[indexReadyMsg](t, m.inputKey(key("enter"))))
		}},
		{"r in the tree", func(t *testing.T, m *Model) {
			m.setFocus(focusExplorer)
			for i, row := range m.tree.Rows() {
				if row.Dir && strings.HasPrefix(row.Rel, "internal") {
					m.treeSel = i
					break
				}
			}
			m.Update(runUntil[indexReadyMsg](t, m.explorerKey("r")))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t)
			root = m.idx.Root()
			tc.do(t, m)

			want := filepath.Join(root, "internal")
			if got := m.mgr.Active().CWD; got != want {
				t.Fatalf("session cwd = %q, want %q", got, want)
			}
			got := moves(m)
			if len(got) != 1 {
				t.Fatalf("the move left %d records, want 1", len(got))
			}
			if !strings.Contains(got[0].Command, want) {
				t.Errorf("the record does not say where it went: %q", got[0].Command)
			}
			// And where it came from, which the command line cannot say.
			if !strings.Contains(got[0].Output, root) {
				t.Errorf("the record does not say where it came from: %q", got[0].Output)
			}
			// The agent reads Text, so the move has to be legible there too.
			text := m.mgr.Active().Messages[0].Text
			if !strings.Contains(text, "moved this session") || !strings.Contains(text, want) {
				t.Errorf("what the agent reads does not describe the move:\n%s", text)
			}
		})
	}
}

// Moving somewhere that is not there is worth recording too. The path is
// usually the agent's own suggestion, and it should learn that it was wrong.
func TestAFailedMoveIsRecorded(t *testing.T) {
	m := newTestModel(t)
	before := m.mgr.Active().CWD

	m.input.SetValue("!cd nowhere-at-all")
	m.inputKey(key("enter"))

	if got := m.mgr.Active().CWD; got != before {
		t.Errorf("a failed cd moved the session to %q", got)
	}
	got := moves(m)
	if len(got) != 1 {
		t.Fatalf("the failure left %d records, want 1", len(got))
	}
	if got[0].Exit == 0 {
		t.Error("the record says the move succeeded")
	}
	if !strings.Contains(got[0].Output, "not a directory") {
		t.Errorf("the record does not say what went wrong: %q", got[0].Output)
	}
}

// Moving nowhere is not a move. Pressing `r` on the directory the session is
// already in would otherwise fill the transcript with nothing happening.
func TestMovingToWhereItAlreadyIsSaysNothing(t *testing.T) {
	m := newTestModel(t)
	here := m.mgr.Active().CWD

	m.setSessionRoot(here)

	if got := moves(m); len(got) != 0 {
		t.Errorf("staying put left %d records: %+v", len(got), got)
	}
}

// The record renders as what it was: a line you could have typed, in the
// transcript beside the commands you did type.
func TestAMoveRendersAsACommand(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("!cd internal")
	m.Update(runUntil[indexReadyMsg](t, m.inputKey(key("enter"))))

	out := stripANSI(m.transcript(100))
	if !strings.Contains(out, "$") || !strings.Contains(out, "cd ") {
		t.Errorf("the move is not shown as a command:\n%s", out)
	}
	if !strings.Contains(out, "from ") {
		t.Errorf("the move does not say where it came from:\n%s", out)
	}
}
