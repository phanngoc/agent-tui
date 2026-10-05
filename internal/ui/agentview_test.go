package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/phanngoc/agent-tui/internal/session"
)

// A sub-agent at work shows what it is doing, what it has used and its last
// calls, with an agent it started nested under it; once done, one line of
// its report replaces all that.
func TestSubAgentRendersLikeClaudeCode(t *testing.T) {
	m := newTestModel(t)
	child := &session.SubAgent{Type: "Explore", Description: "look in web/", State: "running", Activity: "Searching web/", Started: time.Now()}
	a := &session.SubAgent{Type: "general-purpose", Description: "Count the .go files", State: "running",
		Activity: "Reading go.mod", ToolUses: 4, Tokens: 21200, Started: time.Now().Add(-9 * time.Second),
		Calls: []session.ToolCall{
			{ID: "1", Name: "Glob", Input: []byte(`{"pattern":"**/go.mod"}`), Done: true, Result: "go.mod"},
			{ID: "2", Name: "Agent", Input: []byte(`{"description":"look in web/"}`), Agent: child},
			{ID: "3", Name: "Read", Input: []byte(`{"file_path":"go.mod"}`)},
		}}
	var b strings.Builder
	m.renderTool(&b, session.ToolCall{ID: "t", Name: "Agent", Agent: a}, 120)
	out := ansi.Strip(b.String())
	for _, want := range []string{"general-purpose", "Count the .go files", "⎿ Reading go.mod", "4 tool uses", "21.2k tokens", "· 9", "✓ Glob", "Explore", "Searching web/", "Read go.mod"} {
		if !strings.Contains(out, want) {
			t.Errorf("running agent: missing %q in\n%s", want, out)
		}
	}

	a.State, a.Activity, a.Summary, a.Duration = "done", "", "**There are 5 Go files.**\nDetails…", 12*time.Second
	child.State = "done"
	b.Reset()
	m.renderTool(&b, session.ToolCall{ID: "t", Name: "Agent", Agent: a}, 120)
	out = ansi.Strip(b.String())
	if !strings.Contains(out, "✓ general-purpose") || !strings.Contains(out, "⎿ There are 5 Go files.") || strings.Contains(out, "Read go.mod") {
		t.Errorf("finished agent:\n%s", out)
	}

	s := &session.Session{Messages: []session.Message{{Role: session.RoleAssistant, Tools: []session.ToolCall{{ID: "t", Name: "Agent", Agent: &session.SubAgent{State: "running",
		Calls: []session.ToolCall{{Name: "Agent", Agent: &session.SubAgent{State: "running"}}}}}}}}}
	if n := runningAgents(s); n != 2 {
		t.Errorf("runningAgents = %d; want 2", n)
	}
}
