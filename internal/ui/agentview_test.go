package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/phanngoc/agent-tui/internal/session"
)

// A sub-agent at work is two lines folded — what it is, what it has used,
// what it is doing now — plus the last thing it said, with an agent it
// started at work under it. Opened with alt+o, every call shows. Once done,
// one line of its report replaces all that.
func TestSubAgentRendersLikeClaudeCode(t *testing.T) {
	m := newTestModel(t)
	child := &session.SubAgent{Type: "Explore", Description: "look in web/", State: "running", Activity: "Searching web/", Started: time.Now()}
	a := &session.SubAgent{Type: "general-purpose", Description: "Count the .go files", State: "running",
		Activity: "Reading go.mod", Summary: "Now reading go.mod to find the module.", ToolUses: 4, Tokens: 21200, Started: time.Now().Add(-9 * time.Second),
		Calls: []session.ToolCall{
			{ID: "1", Name: "Glob", Input: []byte(`{"pattern":"**/go.mod"}`), Done: true, Result: "go.mod"},
			{ID: "2", Name: "Agent", Input: []byte(`{"description":"look in web/"}`), Agent: child},
			{ID: "3", Name: "Read", Input: []byte(`{"file_path":"go.mod"}`)},
		}}
	render := func() string {
		var b strings.Builder
		m.renderTool(&b, session.ToolCall{ID: "t", Name: "Agent", Agent: a}, 120)
		return ansi.Strip(b.String())
	}
	out := render()
	for _, want := range []string{"general-purpose", "Count the .go files", "4 tool uses", "21.2k tokens", "· 9", "⎿ Reading go.mod",
		"Now reading go.mod to find the module.", "↳ ● Explore", "Searching web/"} {
		if !strings.Contains(out, want) {
			t.Errorf("folded running agent: missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "Glob") {
		t.Errorf("folded running agent lists its calls:\n%s", out)
	}

	m.showAllCalls = true
	out = render()
	for _, want := range []string{"✓ Glob", "Explore", "Read go.mod", "Now reading go.mod"} {
		if !strings.Contains(out, want) {
			t.Errorf("opened running agent: missing %q in\n%s", want, out)
		}
	}
	m.showAllCalls = false

	a.State, a.Activity, a.Summary, a.Duration = "done", "", "**There are 5 Go files.**\nDetails…", 12*time.Second
	child.State = "done"
	out = render()
	if !strings.Contains(out, "✓ general-purpose") || !strings.Contains(out, "⎿ There are 5 Go files.") || strings.Contains(out, "Read go.mod") || strings.Contains(out, "Explore") {
		t.Errorf("finished agent:\n%s", out)
	}

	s := &session.Session{Messages: []session.Message{{Role: session.RoleAssistant, Tools: []session.ToolCall{{ID: "t", Name: "Agent", Agent: &session.SubAgent{State: "running",
		Calls: []session.ToolCall{{Name: "Agent", Agent: &session.SubAgent{State: "running"}}}}}}}}}
	if n := runningAgents(s); n != 2 {
		t.Errorf("runningAgents = %d; want 2", n)
	}
}

// Agents started together are one block, a two-line row each, and none of
// them is folded away while it works, however long the turn.
func TestSubAgentsStartedTogetherAreOneGroup(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Append(session.Message{Role: session.RoleUser, Text: "map the APIs"})
	var earlier []session.ToolCall
	for i := 0; i < 8; i++ {
		earlier = append(earlier, session.ToolCall{ID: "r" + string(rune('a'+i)), Name: "Read", Input: []byte(`{"file_path":"x.go"}`), Done: true})
	}
	s.Append(session.Message{Role: session.RoleAssistant, Text: "Reading first.", Tools: earlier})
	agents := []session.ToolCall{
		{ID: "a1", Name: "Agent", Agent: &session.SubAgent{Type: "general-purpose", Description: "FE API calls", State: "running", Activity: "Reading fe_calls.json", Tokens: 41200}},
		{ID: "a2", Name: "Agent", Agent: &session.SubAgent{Type: "Explore", Description: "Spec group A", State: "done", Summary: "Found 12 endpoints.", Tokens: 30000}},
		{ID: "a3", Name: "Agent", Agent: &session.SubAgent{State: "running", Prompt: "Spec group E (sbicore)\nRetry the lookups", Activity: "Running Final line-number lookups"}},
	}
	s.Append(session.Message{Role: session.RoleAssistant, Tools: agents})
	m.invalidateChat()
	out := ansi.Strip(m.transcript(140))
	for _, want := range []string{"Running 2 of 3 agents · 71.2k tokens", "├─ ● general-purpose  FE API calls", "│    ⎿ Reading fe_calls.json",
		"├─ ✓ Explore  Spec group A", "⎿ Found 12 endpoints.", "└─ ● agent  Spec group E (sbicore)", "⎿ Running Final line-number lookups"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}

	// A long turn folds its earlier calls; the agents at work stay.
	for i := range agents {
		agents[i].Agent.State = "running"
	}
	plans := planCalls(s.Messages, 1, false)
	if plans[2].skip == 0 {
		t.Fatalf("expected the agents' message to be folded too: %+v", plans)
	}
	var b strings.Builder
	m.renderMessage(&b, &s.Messages[2], 140, plans[2])
	if got := strings.Count(ansi.Strip(b.String()), "● "); got < 3 {
		t.Errorf("a running agent was folded away:\n%s", ansi.Strip(b.String()))
	}
}
