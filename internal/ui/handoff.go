package ui

import (
	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

// Handing a conversation from one agent to another.
//
// It used to be impossible, and the comment in the engine picker said so: each
// engine keeps its own history on its own server, and there is no way to read
// one out of it or write one into it. That is still true, and it is also not
// the whole story — because the transcript on this side is complete, and it is
// the thing both engines are actually talking about.
//
// So a handoff is one operation with two renderings. The engine that is handed
// the transcript itself gets it through agent.Replay; the three that get one
// string of text get the same thing through session.Brief. What decides how
// much either needs is a single number per engine: how far it had got when it
// last spoke.

// handoffBrief is the catch-up the engine about to run is given: what happened
// in this conversation while it was not the one running it.
//
// It is derived, never stored. The transcript is the record and a brief is a
// rendering of it; appending one to the transcript would mean the next brief
// quoting the last, and the session file doubling at every switch.
func (m *Model) handoffBrief(s *session.Session, engineID string) string {
	// The final message is the prompt being sent, which the engine receives on
	// its own. A brief that contained it too would ask the same question twice.
	end := len(s.Messages) - 1
	if end <= 0 {
		return ""
	}
	seen := s.StateFor(engineID).Seen
	if seen >= end {
		return "" // it has seen everything; only the first turn after a switch pays
	}
	return session.Brief(s.Messages[seen:end], session.BriefLimit)
}

// switchEngine hands the session to another engine.
//
// The old code destroyed two things here: the engine's own session id, and the
// SDK reasoning context. Only the second had to go — signatures and a prompt
// cache belong to one conversation with one server and cannot be moved. The id
// belongs to the engine that earned it, and keeping it is what makes coming
// back a resume rather than a second cold start.
func (m *Model) switchEngine(to agent.Engine) {
	s := m.mgr.Active()
	if s.Engine == to.ID() {
		return
	}
	from := m.reg.Get(s.Engine)
	seen := s.StateFor(to.ID()).Seen

	s.Engine = to.ID()
	// ExternalID mirrors whichever engine is selected, for the session files
	// written before Engines existed and for the code that still reads it.
	s.ExternalID = s.StateFor(to.ID()).ExternalID
	// The reasoning context is the one thing that genuinely cannot travel, and
	// it cannot come back either: the transcript grew while another engine had
	// the session, so continuing from a history missing those turns would give
	// an engine that is confidently blind. Replay rebuilds it from the record.
	s.Live = nil

	if len(s.Messages) > 0 {
		m.logHandoff(from, to)
		m.notice = handoffNotice(to, s.ExternalID != "", len(s.Messages)-seen)
	}
	m.lastEngine = to.ID()
	m.mgr.Save(s)
}

// handoffNotice says what actually just happened, which is one of three things
// rather than the one thing the old message claimed.
func handoffNotice(to agent.Engine, resuming bool, missed int) string {
	switch {
	case resuming && missed > 0:
		return "switched to " + to.Label() + "; it resumes its own session and is caught up on " +
			plural(missed, "message")
	case resuming:
		return "switched to " + to.Label() + "; it resumes where it left off"
	}
	return "switched to " + to.Label() + "; it has not seen this conversation and gets a summary"
}

// logHandoff records the change the way logMove records a change of directory,
// and for the same reason: it is the same kind of event. After it, the thing
// answering is not the thing that answered before.
//
// Until now it left nothing behind at all — a notice the next keystroke wiped,
// and a transcript in which one agent's sentence is followed by another's with
// nothing in between to say so. Reopen the session tomorrow and the seam was
// invisible.
func (m *Model) logHandoff(from, to agent.Engine) {
	s := m.mgr.Active()
	run := &session.ShellRun{
		Command: "/engine " + to.ID(),
		Where:   m.sessionFS(s).Label(),
		Dir:     m.sessionCWD(s),
		Done:    true,
	}
	s.Append(session.Message{
		Role:  session.RoleUser,
		Text:  handoffContext(from, to),
		Shell: run,
	})
	m.grew(s)
}

// handoffContext is what the transcript shows and what the next engine reads.
// Both need the same thing said: everything above belongs to someone else.
func handoffContext(from, to agent.Engine) string {
	was := "another agent"
	if from != nil {
		was = from.Label()
	}
	return "I handed this session from " + was + " to " + to.Label() + ".\n\n" +
		"Everything above was done by " + was + ". " + to.Label() +
		" did not run any of it and has no memory of it; it is given a summary" +
		" with the next message."
}
