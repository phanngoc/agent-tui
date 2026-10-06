package ui

import (
	"testing"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

// What the agent was handed mid-turn leaves the queue and enters the
// transcript where it was read, marked; what it was not handed stays
// queued, to go when the turn ends.
func TestSteeredPromptsLeaveTheQueue(t *testing.T) {
	m := newTestModel(t)
	s, send := busyTurn(m)
	s.Queued = []session.Queued{{Text: "use the v2 API"}, {Text: "and add a test"}}
	m.steerQ.put(s.ID, "use the v2 API")
	if got := m.steerQ.take(s.ID); len(got) != 1 {
		t.Fatalf("box %q", got)
	}
	send(agent.EvSteered{Texts: []string{"use the v2 API"}})
	if len(s.Queued) != 1 || s.Queued[0].Text != "and add a test" {
		t.Fatalf("queue %+v", s.Queued)
	}
	last := s.Messages[len(s.Messages)-1]
	if last.Role != session.RoleUser || !last.Steered || last.Text != "use the v2 API" {
		t.Fatalf("transcript ends with %+v", last)
	}
}

// Stopped to send now, the queue goes even though the turn did not end
// well.
func TestSendNowSendsTheQueueAfterTheStop(t *testing.T) {
	m := newTestModel(t)
	s, _ := busyTurn(m)
	s.Queued = []session.Queued{{Text: "do this instead"}}
	m.sendNow[s.ID] = true
	if cmd := m.nextQueued(s, false); cmd == nil {
		t.Fatal("the queue did not go after a send-now stop")
	}
	if len(s.Queued) != 0 || m.sendNow[s.ID] {
		t.Fatalf("queue %+v, flag %v", s.Queued, m.sendNow[s.ID])
	}
}
