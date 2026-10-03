package ui

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

func finderPaths(m *Model) []string {
	var out []string
	for _, h := range m.finderHit {
		out = append(out, h.Path)
	}
	return out
}

func finderHas(m *Model, rel string) bool {
	for _, h := range m.finderHit {
		if h.Path == rel {
			return true
		}
	}
	return false
}

// A file the agent writes is in ctrl+p the moment the call finishes, without
// a walk: this is the case that used to need a restart.
func TestAFileTheAgentWritesIsFoundAtOnce(t *testing.T) {
	m := newTestModel(t)
	root := m.idx.Root()
	mustWrite(t, filepath.Join(root, "docs", "push-flow.md"), "# flow")

	feed(t, m, agent.EvToolDone{Call: session.ToolCall{
		ID: "w1", Name: "Write", Done: true,
		Input: json.RawMessage(`{"file_path": ` + jsonString(filepath.Join(root, "docs", "push-flow.md")) + `}`),
	}})
	if m.idx.Stale() {
		t.Error("a write the index was told about by name left it stale")
	}

	m.Update(key("ctrl+p"))
	typeIn(m, "pushflow")
	if !finderHas(m, "docs/push-flow.md") {
		t.Errorf("the written file is not in the finder: %v", finderPaths(m))
	}
}

// A relative path is the agent's directory's, as the built-in tools give it.
func TestARelativeWriteIsResolvedAgainstTheSession(t *testing.T) {
	m := newTestModel(t)
	feed(t, m, agent.EvToolDone{Call: session.ToolCall{
		ID: "w1", Name: "write_file", Done: true,
		Input: json.RawMessage(`{"path": "notes/todo.md"}`),
	}})
	if hits := m.idx.Find("todo", 5); len(hits) == 0 || hits[0].Path != "notes/todo.md" {
		t.Errorf("hits = %v", hits)
	}
}

// A file made some other way — a shell command, an editor — is found when the
// finder opens: the turn's end marks the index stale, opening walks it in the
// background, and the list fills in when the walk lands.
func TestTheFinderCatchesUpWithFilesMadeOtherwise(t *testing.T) {
	m := newTestModel(t)
	root := m.idx.Root()
	mustWrite(t, filepath.Join(root, "scripts", "seed-users.sql"), "")
	feed(t, m, agent.EvDone{})

	cmd := m.onKey(key("ctrl+p"))
	if cmd == nil {
		t.Fatal("opening the finder over a stale index did not walk it")
	}
	m.Update(runUntil[indexReadyMsg](t, cmd))
	typeIn(m, "seedusers")
	if !finderHas(m, "scripts/seed-users.sql") {
		t.Errorf("the new file is not in the finder after the walk: %v", finderPaths(m))
	}
	if m.notice != "" && m.notice[0] >= '0' && m.notice[0] <= '9' {
		t.Errorf("a catch-up walk announced itself: %q", m.notice)
	}
}

// Opening the finder over an index that is current walks nothing: freshness
// is paid for when something changed, not on every keystroke.
func TestAFreshIndexIsNotWalkedAgain(t *testing.T) {
	m := newTestModel(t)
	if cmd := m.onKey(key("ctrl+p")); cmd != nil {
		t.Error("opening the finder over a fresh index started a walk")
	}
}

// The walk landing under an open finder keeps the selection on its file.
func TestARefreshKeepsTheSelection(t *testing.T) {
	m := newTestModel(t)
	m.Update(key("ctrl+p"))
	if len(m.finderHit) < 2 {
		t.Fatalf("too few files to move between: %v", finderPaths(m))
	}
	m.Update(key("down"))
	was := m.finderHit[m.finderSel].Path

	m.idx.Add("aaa-new.md") // lands at the head of the list
	m.Update(indexReadyMsg{quiet: true})
	if got := m.finderHit[m.finderSel].Path; got != was {
		t.Errorf("the selection moved from %q to %q", was, got)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
