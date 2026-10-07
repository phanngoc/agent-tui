package ui

import (
	"context"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/engine"
	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/session"
)

type conversationEngine struct {
	fakeEngine
	turns chan agent.Turn
}

func (e conversationEngine) Run(_ context.Context, turn agent.Turn, out chan<- agent.Event) {
	e.turns <- turn
	close(out)
}

func TestTerminalScheduledFreshContextGetsNewConversationIdentity(t *testing.T) {
	m := newTestModel(t)
	e := conversationEngine{fakeEngine: fakeEngine{id: "claude"}, turns: make(chan agent.Turn, 3)}
	m.reg = engine.NewRegistryWith(e)
	s := m.mgr.Active()
	s.Engine = "claude"
	s.Append(session.Message{Role: session.RoleUser, Text: "previous run"})
	s.SetExternalID("claude", "old-context")
	old := s.ConversationID()
	m.onGatewayCommand(gateway.Command{Type: gateway.CmdPrompt, Session: s.ID, Text: "scheduled run", Fresh: true, From: "schedule"})
	select {
	case turn := <-e.turns:
		if turn.ConversationID == "" || turn.ConversationID == old || turn.ConversationID != s.ConversationID() {
			t.Fatal("terminal failed to pass fresh conversation identity")
		}
		if turn.ExternalID != "" || len(turn.History) != 1 {
			t.Fatal("fresh schedule resumed old context")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn never started")
	}
	m.cancelSession(s)
}
