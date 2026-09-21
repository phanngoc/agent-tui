package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

// feed applies one agent event to the active session, the way the pump does.
func feed(t *testing.T, m *Model, ev agent.Event) {
	t.Helper()
	m.Update(agentMsg{sess: m.mgr.Active(), ev: ev})
}

// A call the model is still writing is on screen while it is written. Until
// this, a turn spent composing a file to write looked like a turn doing
// nothing at all.
func TestTranscriptShowsACallBeingWritten(t *testing.T) {
	m := newTestModel(t)

	feed(t, m, agent.EvToolPending{Calls: []session.ToolCall{{
		ID: "t1", Name: "write_file", Input: json.RawMessage(`{"path": "internal/ui/ch`),
	}}})

	out := stripANSI(m.transcript(80))
	if !strings.Contains(out, "write_file") || !strings.Contains(out, "internal/ui/ch") {
		t.Errorf("the half-written call is not on screen:\n%s", out)
	}
}

// The finished turn carries the same calls, so the half-written copies go: two
// lines for one call would read as two calls.
func TestCommittedTurnReplacesThePendingCalls(t *testing.T) {
	m := newTestModel(t)
	feed(t, m, agent.EvToolPending{Calls: []session.ToolCall{{
		ID: "t1", Name: "read_file", Input: json.RawMessage(`{"path":"a.go"}`),
	}}})
	feed(t, m, agent.EvAssistant{Message: session.Message{
		Role: session.RoleAssistant, Text: "đọc file",
		Tools: []session.ToolCall{{
			ID: "t1", Name: "read_file", Input: json.RawMessage(`{"path":"a.go"}`),
		}},
	}})

	if got := len(m.mgr.Active().Calls); got != 0 {
		t.Errorf("%d pending calls survived the committed turn", got)
	}
	if n := strings.Count(stripANSI(m.transcript(80)), "read_file"); n != 1 {
		t.Errorf("read_file appears %d times, want once", n)
	}
}

// A command says nothing until it exits, so what it prints is forwarded and
// shown under the turn while it runs.
func TestRunningCommandOutputIsOnScreen(t *testing.T) {
	m := newTestModel(t)
	feed(t, m, agent.EvAssistant{Message: session.Message{
		Role: session.RoleAssistant,
		Tools: []session.ToolCall{{
			ID: "t1", Name: "bash", Input: json.RawMessage(`{"command":"go test ./..."}`),
		}},
	}})
	feed(t, m, agent.EvToolOutput{ID: "t1", Text: "ok  internal/agent  3.0s\n"})
	feed(t, m, agent.EvToolOutput{ID: "t1", Text: "--- FAIL: TestFoo\n"})

	out := stripANSI(m.transcript(80))
	if !strings.Contains(out, "--- FAIL: TestFoo") {
		t.Errorf("the running command's output is not on screen:\n%s", out)
	}
	if !strings.Contains(out, "go test ./...") {
		t.Errorf("the command it belongs to is not on screen:\n%s", out)
	}
}

// Once the call is done its result is on it, and the line says how much there
// was. Keeping the live copy too would show the same output twice.
func TestFinishedCommandDropsItsLiveOutput(t *testing.T) {
	m := newTestModel(t)
	call := session.ToolCall{
		ID: "t1", Name: "bash", Input: json.RawMessage(`{"command":"go build ./..."}`),
	}
	feed(t, m, agent.EvAssistant{Message: session.Message{
		Role: session.RoleAssistant, Tools: []session.ToolCall{call},
	}})
	feed(t, m, agent.EvToolOutput{ID: "t1", Text: "one\ntwo\nthree\n"})

	call.Done, call.Result = true, "one\ntwo\nthree\n"
	feed(t, m, agent.EvToolDone{Call: call})

	s := m.mgr.Active()
	if s.Output != "" || s.OutputID != "" {
		t.Errorf("live output survived the call: %q", s.Output)
	}
	out := stripANSI(m.transcript(80))
	if strings.Contains(out, "two") {
		t.Errorf("the output is still being shown line by line:\n%s", out)
	}
	// What it left behind is the count, which is what the line is for.
	if !strings.Contains(out, "3 lines") {
		t.Errorf("the finished call does not say what came back:\n%s", out)
	}
}

// A turn that ends anywhere — cancelled, failed, out of steps — leaves nothing
// half-written on screen.
func TestEndingATurnClearsWhatWasInFlight(t *testing.T) {
	m := newTestModel(t)
	feed(t, m, agent.EvToolPending{Calls: []session.ToolCall{{
		ID: "t1", Name: "bash", Input: json.RawMessage(`{"command":"sleep 9`),
	}}})
	feed(t, m, agent.EvToolOutput{ID: "t1", Text: "working\n"})
	feed(t, m, agent.EvDone{})

	s := m.mgr.Active()
	if len(s.Calls) != 0 || s.Output != "" {
		t.Errorf("after the turn: %d calls, output %q", len(s.Calls), s.Output)
	}
}

// Only the tail is kept. A build that prints for a minute must not grow the
// session in memory for as long as it runs.
func TestLiveOutputIsBounded(t *testing.T) {
	m := newTestModel(t)
	for range 200 {
		feed(t, m, agent.EvToolOutput{ID: "t1", Text: strings.Repeat("x", 200) + "\n"})
	}
	if got := len(m.mgr.Active().Output); got > liveOutputBytes {
		t.Errorf("kept %d bytes, want at most %d", got, liveOutputBytes)
	}
}
