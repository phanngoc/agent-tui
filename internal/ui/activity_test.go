package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

// busyTurn starts a turn by hand, the way submitting a prompt does, without
// running an engine.
func busyTurn(m *Model) (*session.Session, func(agent.Event)) {
	s := m.mgr.Active()
	s.Append(session.Message{Role: session.RoleUser, Text: "điều tra bug", At: time.Now()})
	s.Busy, s.Status, s.Started = true, "thinking", time.Now()
	s.PhaseAt, s.HeardAt = s.Started, s.Started
	ch := make(chan agent.Event, 1)
	return s, func(e agent.Event) { m.Update(agentMsg{sess: s, ch: ch, ev: e}) }
}

func liveScreen(m *Model) string { return stripANSI(m.View().Content) }

// TestActivityLineSaysWhatAndHowLong: the live line is where the eye is, and
// it says what the turn is doing and for how long it has been doing it.
func TestActivityLineSaysWhatAndHowLong(t *testing.T) {
	m := newTestModel(t)
	_, send := busyTurn(m)
	send(agent.EvThinkingDelta{Text: "The guard in ekyc.service.ts rejects a second submit ", Tokens: 120})
	send(agent.EvThinkingDelta{Text: "because isStalePending needs ai_verification_result to be null."})

	out := liveScreen(m)
	for _, want := range []string{"thinking", "↓ ~120 tokens", "isStalePending needs ai_verification_result"} {
		if !strings.Contains(out, want) {
			t.Errorf("the activity line is missing %q:\n%s", want, out)
		}
	}

	// Answering ends the thinking it showed.
	send(agent.EvTextDelta{Text: "Kết luận: "})
	out = liveScreen(m)
	if strings.Contains(out, "isStalePending") {
		t.Error("the thinking stayed on screen once the answer began")
	}
	if !strings.Contains(out, "writing") {
		t.Errorf("the phase did not move on to writing:\n%s", out)
	}
}

// TestFinishedToolHandsBackToTheModel is the bug in the screenshot: both calls
// were done and the status still named the last one, for minutes.
func TestFinishedToolHandsBackToTheModel(t *testing.T) {
	m := newTestModel(t)
	s, send := busyTurn(m)
	call := session.ToolCall{ID: "toolu_1", Name: "mcp__shizuka__github_search_issues", Input: json.RawMessage(`{"query":"backoffice api"}`)}
	send(agent.EvAssistant{Message: session.Message{Role: session.RoleAssistant, Tools: []session.ToolCall{call}, At: time.Now()}})
	send(agent.EvToolStart{Call: call})
	if s.Status != call.Name {
		t.Fatalf("status while running = %q", s.Status)
	}
	if out := liveScreen(m); !strings.Contains(out, "⋯") {
		t.Errorf("a running call is not drawn as running:\n%s", out)
	}

	done := call
	done.Done, done.Result = true, "3 issues"
	send(agent.EvToolDone{Call: done})
	if s.Status != "waiting for the model" {
		t.Errorf("status after the call finished = %q, want the wait for the model", s.Status)
	}
}

// TestQuietEngineIsCalledOut: no word from the engine for a long while, with
// nothing running, is what a stall looks like — and the line says so.
func TestQuietEngineIsCalledOut(t *testing.T) {
	m := newTestModel(t)
	s, _ := busyTurn(m)
	s.Status = "waiting for the model"
	s.HeardAt = time.Now().Add(-45 * time.Second)
	if out := liveScreen(m); !strings.Contains(out, "no word from the engine for 45s") {
		t.Errorf("a 45s silence is not called out:\n%s", out)
	}

	// A running tool is allowed its silence: a build is not a stall.
	s.RunAt = time.Now().Add(-45 * time.Second)
	if out := liveScreen(m); strings.Contains(out, "no word from the engine") {
		t.Error("a long-running tool was reported as a stall")
	}
}

// TestRunningCommandOutputShowsUnderItsCall: Claude Code's commands write to
// a file as they go; the lines show under the call that is running them.
func TestRunningCommandOutputShowsUnderItsCall(t *testing.T) {
	m := newTestModel(t)
	s, send := busyTurn(m)
	call := session.ToolCall{ID: "toolu_9", Name: "Bash", Input: json.RawMessage(`{"command":"go test ./..."}`)}
	send(agent.EvAssistant{Message: session.Message{Role: session.RoleAssistant, Tools: []session.ToolCall{call}, At: time.Now()}})
	send(agent.EvToolStart{Call: call})

	path := filepath.Join(t.TempDir(), "b1.output")
	os.WriteFile(path, []byte("ok  pkg/a 1.2s\nok  pkg/b 0.4s\n"), 0o644)
	send(agent.EvTask{ID: "b1", Label: "go test", State: "running", Live: []string{path}, ToolUse: "toolu_9"})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(s.Output, "pkg/b") {
		m.feedToolOutput()
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(s.Output, "ok  pkg/b 0.4s") {
		t.Fatalf("the command's output never reached its call: %q", s.Output)
	}
	if out := liveScreen(m); !strings.Contains(out, "ok  pkg/a 1.2s") {
		t.Errorf("the output is not on screen:\n%s", out)
	}
	m.tasks.Update("b1", "done", "")
}

func TestIdleTranscriptHasNoActivityLine(t *testing.T) {
	m := newTestModel(t)
	if out := liveScreen(m); strings.Contains(out, "↓ ~") || strings.Contains(out, "waiting for the model") {
		t.Errorf("an idle session shows activity:\n%s", out)
	}
}
