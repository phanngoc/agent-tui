package engine

import (
	"strings"
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

// With CLAUDE_CODE_FORWARD_SUBAGENT_TEXT (2.1.292), a sub-agent's words
// between its calls arrive too: each says what it is about to do, and the
// latest is what it is shown saying until its report replaces it.
func TestClaudeSubAgentWords(t *testing.T) {
	var said []string
	var last session.SubAgent
	for _, e := range decodeFile(t, &claudeDec{}, "claude_subagent_words.jsonl") {
		if s, ok := e.(agent.EvSubAgent); ok {
			if last.Summary != s.Agent.Summary && s.Agent.Running() {
				said = append(said, s.Agent.Summary)
			}
			last = s.Agent
		}
		if a, ok := e.(agent.EvAssistant); ok && a.Message.Text != "" {
			for _, w := range said {
				if strings.Contains(a.Message.Text, w) {
					t.Errorf("a sub-agent's words landed in the main transcript: %q", w)
				}
			}
		}
	}
	if len(said) < 3 || !strings.Contains(said[1], "go.mod") {
		t.Errorf("words while running = %q; want one before each call", said)
	}
	if last.State != "done" || !strings.HasPrefix(last.Summary, "## Summary") || len(last.Calls) != 3 {
		t.Errorf("finished: %s, %d calls, report %q", last.State, len(last.Calls), last.Summary)
	}
}

// An agent first seen part way through — its call made in a turn before this
// one — still gets its type, and what it was asked when that is forwarded.
func TestClaudeSubAgentSeenLate(t *testing.T) {
	d := &claudeDec{}
	var got session.SubAgent
	emit := func(e agent.Event) {
		if s, ok := e.(agent.EvSubAgent); ok {
			got = s.Agent
		}
	}
	d.line([]byte(`{"type":"user","parent_tool_use_id":"toolu_x","message":{"role":"user","content":[{"type":"text","text":"Inventory the FE API calls"}]}}`), emit)
	d.line([]byte(`{"type":"system","subtype":"task_progress","task_id":"t1","tool_use_id":"toolu_x","description":"Reading fe_calls.json","subagent_type":"general-purpose","usage":{"total_tokens":100,"tool_uses":1,"duration_ms":10}}`), emit)
	if got.Type != "general-purpose" || got.Prompt != "Inventory the FE API calls" || got.Activity != "Reading fe_calls.json" {
		t.Errorf("late agent = %+v", got)
	}
}
