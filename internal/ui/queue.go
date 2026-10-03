package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/session"
)

// Sending while the agent works.
//
// A prompt sent mid-turn used to be refused — "still working" — and the
// thought had to be held until the turn ended. That is the moment you most
// often have something to add: you have just seen where the agent is going.
// So it is taken, the way Claude Code takes it: queued under the running turn,
// shown there, and sent as the next turn the moment this one ends.
//
// A queued prompt is never lost. Stopping the turn, or a turn that fails, puts
// what was queued back in the prompt to be edited or sent again; ↑ in an empty
// prompt takes the last one back out of the queue to change it.
//
// It works the same for every engine, because it does not depend on any of
// them accepting input mid-turn: it waits for the turn and starts another.

// queuePrompt holds a prompt, and the images attached to it, until the
// session's running turn ends.
func (m *Model) queuePrompt(s *session.Session, text string) {
	files := m.takeAttachments(fsID(m.sessionFS(s)), text)
	s.Queued = append(s.Queued, session.Queued{Text: text, Files: files})
	n := len(s.Queued)
	m.notice = "queued — it goes when this turn ends · ↑ edits it · esc stops the turn"
	if n > 1 {
		m.notice = plural(n, "prompt") + " queued — each goes when the turn before it ends"
	}
}

// nextQueued starts the next queued prompt, if the turn that just ended
// finished well. A turn that failed or was stopped leaves the queue to the
// reader: what to do next depends on why it stopped.
func (m *Model) nextQueued(s *session.Session, ok bool) tea.Cmd {
	if len(s.Queued) == 0 {
		return nil
	}
	if !ok {
		// Back into the prompt only if the prompt is this session's; a
		// background session keeps its queue, shown, until it is looked at.
		if s == m.promptTarget() {
			m.unqueue(s)
		}
		return nil
	}
	q := s.Queued[0]
	s.Queued = s.Queued[1:]
	if s == m.mgr.Active() {
		m.toBottom()
	}
	return m.startTurn(s, q.Text, q.Files)
}

// unqueue puts everything queued back in the prompt, ahead of anything typed
// since, with its images staged again.
func (m *Model) unqueue(s *session.Session) {
	if len(s.Queued) == 0 {
		return
	}
	var texts []string
	for _, q := range s.Queued {
		texts = append(texts, q.Text)
		m.attach = append(m.attach, q.Files...)
	}
	s.Queued = nil
	if typed := strings.TrimSpace(m.input.Value()); typed != "" {
		texts = append(texts, typed)
	}
	m.input.SetValue(strings.Join(texts, "\n"))
	m.input.CursorEnd()
	m.notice = "the queued prompt is back in the box — enter sends it"
}

// editLastQueued takes the newest queued prompt back into an empty prompt.
func (m *Model) editLastQueued() bool {
	s := m.promptTarget()
	if s == nil || len(s.Queued) == 0 || m.input.Value() != "" {
		return false
	}
	q := s.Queued[len(s.Queued)-1]
	s.Queued = s.Queued[:len(s.Queued)-1]
	m.attach = append(m.attach, q.Files...)
	m.input.SetValue(q.Text)
	m.input.CursorEnd()
	m.notice = "taken out of the queue — enter queues it again"
	return true
}

// queuedLines draws what is waiting, under the running turn.
func (m *Model) queuedLines(s *session.Session, width int) []string {
	var out []string
	for _, q := range s.Queued {
		text := strings.Join(strings.Fields(q.Text), " ")
		head := m.st.Faint.Render("  queued  ")
		out = append(out, m.st.AgentBar.Render("▎")+head+
			m.st.Dim.Render(truncate(text, max(10, width-14))))
	}
	return out
}
