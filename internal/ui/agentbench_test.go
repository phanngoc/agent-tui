package ui

import (
	"strconv"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/session"
)

// busyAgents is a long session whose last turn has six sub-agents at work,
// each eighty calls in: the screen from the report that started this.
func busyAgents(b *testing.B) (*Model, []*session.SubAgent) {
	m := loaded(b, 200, 8)
	s := m.mgr.Active()
	var agents []*session.SubAgent
	var tools []session.ToolCall
	for i := 0; i < 6; i++ {
		a := &session.SubAgent{Type: "general-purpose", Description: "Spec APIs group " + strconv.Itoa(i),
			State: "running", Activity: "Reading /tmp/ecmap/api/fe_calls.json", Tokens: 41200, ToolUses: 80,
			Summary: "Now reading the next part of the spec.", Started: time.Now().Add(-time.Minute)}
		for j := 0; j < 80; j++ {
			a.Calls = append(a.Calls, session.ToolCall{ID: strconv.Itoa(j), Name: "Read",
				Input: []byte(`{"file_path":"/tmp/ecmap/api/part` + strconv.Itoa(j) + `.json"}`), Done: true, Result: "ok"})
		}
		agents = append(agents, a)
		tools = append(tools, session.ToolCall{ID: "agent" + strconv.Itoa(i), Name: "Agent", Agent: a})
	}
	s.Append(session.Message{Role: session.RoleAssistant, Text: "Starting six agents.", Tools: tools})
	s.Busy = true
	m.invalidateChat()
	m.View()
	return m, agents
}

// A frame after a sub-agent reported progress: what every progress event costs.
func BenchmarkFrameSubAgentUpdate(b *testing.B) {
	m, agents := busyAgents(b)
	s := m.mgr.Active()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a := *agents[i%len(agents)]
		a.Activity = "Reading part " + strconv.Itoa(i)
		s.SetSubAgent("agent"+strconv.Itoa(i%len(agents)), a)
		m.invalidateChat()
		m.View()
	}
}
