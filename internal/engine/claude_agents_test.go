package engine

import (
	"testing"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

// A real Claude Code 2.1.289 stream in which the main agent starts two
// sub-agents at once (an Explore and a general-purpose one): their calls stay
// off the main transcript, and each is followed from start to report.
func TestClaudeSubAgentsAreTrackedApart(t *testing.T) {
	events := decodeFile(t, &claudeDec{}, "claude_subagents.jsonl")

	// The main transcript holds the main agent's calls only.
	for _, e := range events {
		if a, ok := e.(agent.EvAssistant); ok {
			for _, c := range a.Message.Tools {
				if c.Name != "Agent" {
					t.Errorf("a sub-agent's %s call landed in the main transcript", c.Name)
				}
			}
		}
		if s, ok := e.(agent.EvToolStart); ok && s.Call.Name != "Agent" {
			t.Errorf("a sub-agent's %s call was started as the main agent's", s.Call.Name)
		}
		if task, ok := e.(agent.EvTask); ok {
			t.Errorf("a sub-agent was reported as a background command: %+v", task)
		}
	}

	last := map[string]session.SubAgent{}
	var states []string
	for _, e := range events {
		if s, ok := e.(agent.EvSubAgent); ok {
			last[s.ToolUse] = s.Agent
			states = append(states, s.Agent.State)
		}
	}
	if len(last) != 2 {
		t.Fatalf("followed %d agents; want 2", len(last))
	}
	if states[0] != "starting" {
		t.Errorf("an agent is first reported %q; want starting, as its call is made", states[0])
	}
	types := map[string]bool{}
	for id, a := range last {
		types[a.Type] = true
		if a.State != "done" || a.Ended.IsZero() {
			t.Errorf("%s: state %q, ended %v; want done", id, a.State, a.Ended)
		}
		if a.ID == "" || a.Description == "" || a.Prompt == "" || !a.Background || a.Depth != 1 {
			t.Errorf("%s: missing what task_started said: %+v", id, a)
		}
		if a.Tokens == 0 || a.ToolUses == 0 || a.Duration == 0 || a.LastTool == "" {
			t.Errorf("%s: missing progress: tokens %d, tool uses %d, %s, last %q", id, a.Tokens, a.ToolUses, a.Duration, a.LastTool)
		}
		if a.Summary == "" {
			t.Errorf("%s: no report", id)
		}
		if len(a.Calls) == 0 {
			t.Errorf("%s: none of its own calls were kept", id)
		}
		for _, c := range a.Calls {
			if !c.Done || c.Result == "" {
				t.Errorf("%s: call %s %s has no result", id, c.Name, c.ID)
			}
		}
	}
	if !types["Explore"] || !types["general-purpose"] {
		t.Errorf("types = %v; want Explore and general-purpose", types)
	}

	// The committed main message carries the agents on its Agent calls.
	for _, e := range events {
		if a, ok := e.(agent.EvAssistant); ok {
			for _, c := range a.Message.Tools {
				if c.Name == "Agent" && c.Agent == nil {
					t.Errorf("Agent call %s was committed without its agent", c.ID)
				}
			}
		}
	}
}
