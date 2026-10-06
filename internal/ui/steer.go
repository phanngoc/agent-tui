package ui

import (
	"sync"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

// Steering: a prompt queued while the agent works reaches it within the
// turn, as soon as the running tool calls finish, when the engine can take
// it (agent.Steerer) — Claude Code's way with queued messages. The queue the
// reader sees is the session's; the engine's goroutine takes from this box,
// which holds the same texts behind a lock, and the turn reports what it
// took (agent.EvSteered) for the queue to let go of.
type steerBox struct {
	mu sync.Mutex
	q  map[string][]string
}

func (b *steerBox) put(id, text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.q == nil {
		b.q = map[string][]string{}
	}
	b.q[id] = append(b.q[id], text)
}

func (b *steerBox) take(id string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	q := b.q[id]
	delete(b.q, id)
	return q
}

// steers says the session's engine takes messages mid-turn.
func (m *Model) steers(s *session.Session) bool {
	st, ok := m.reg.Get(s.Engine).(agent.Steerer)
	return ok && st.CanSteer()
}

// steered lets the queue go of what the agent was handed mid-turn and puts
// it in the transcript, where the agent read it.
func (m *Model) steered(s *session.Session, texts []string) {
	for _, t := range texts {
		for i, q := range s.Queued {
			if q.Text == t && len(q.Files) == 0 {
				s.Queued = append(s.Queued[:i:i], s.Queued[i+1:]...)
				break
			}
		}
		s.Append(session.Message{Role: session.RoleUser, Text: t, Steered: true})
	}
	m.notice = "handed to the agent mid-turn"
	m.invalidateChat()
	m.mgr.Save(s)
}
