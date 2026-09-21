package ui

import (
	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/session"
)

// What a conversation is doing, in one cell.
//
// The list used to say only one thing: a spinner while a turn ran, and a mark
// on whichever session was active — and the spinner overwrote the mark, so a
// session that was both lost the one that said where you were. Everything else
// a conversation can be doing was invisible until you switched to it.
//
// The shape of the fix is borrowed from herdr, and so is the reason for it.
// Its sidebar first drew the states as coloured dots and had to be changed:
// blocked, working and idle-unseen were all a filled dot, and to a colourblind
// reader those three collapse into one. So every state gets **its own shape**,
// and the colour is what it means on top of that, not instead of it. The word
// underneath says it a third time, because a glyph nobody has learned yet is a
// decoration.
//
// The states are not herdr's exactly, because agent-tui knows slightly
// different things. It has no notion of an agent that has gone quiet, and it
// does have turns that end in an error, which is worth a shape of its own.

// sessionState is what a conversation is doing.
type sessionState int

const (
	stateEmpty   sessionState = iota // nothing said yet
	stateIdle                        // finished, and you have seen it
	stateUnseen                      // finished while you were looking elsewhere
	stateWorking                     // a turn, or a `!` command, is running
	stateBlocked                     // waiting on you to answer something
	stateFailed                      // the last turn ended badly
)

// glyph is the shape for a state. One cell wide, every one of them different.
func (st sessionState) glyph() string {
	switch st {
	case stateBlocked:
		return "◉"
	case stateFailed:
		return "✗"
	case stateUnseen:
		return "●"
	case stateIdle:
		return "✓"
	}
	return "○" // empty, and whatever working falls back to without a spinner
}

// word is the state said in the one place a reader will look for it.
func (st sessionState) word() string {
	switch st {
	case stateBlocked:
		return "blocked"
	case stateFailed:
		return "failed"
	case stateUnseen:
		return "done"
	case stateWorking:
		return "working"
	case stateIdle:
		return "idle"
	}
	return "new"
}

// style is the colour the glyph and the word share, so the two read as one
// statement rather than two.
func (m *Model) stateStyle(st sessionState) lipgloss.Style {
	switch st {
	case stateBlocked, stateFailed:
		return m.st.Bad
	case stateWorking:
		return m.st.Warn
	case stateUnseen:
		return m.st.Good
	}
	return m.st.Faint
}

// sessionState works out what a conversation is doing.
//
// The order is the order of urgency, and it matters: a session can be blocked
// and busy at once — the turn is running, and it is running because it is
// waiting for you — and what you need to see is the half you can do something
// about.
func (m *Model) sessionState(s *session.Session) sessionState {
	switch {
	case m.awaitingUser(s):
		return stateBlocked
	case s.Busy, s.Running > 0:
		return stateWorking
	case s.LastErr != "":
		return stateFailed
	case len(s.Messages) == 0:
		return stateEmpty
	case s.Unseen:
		return stateUnseen
	}
	return stateIdle
}

// awaitingUser reports whether anything is queued against this conversation
// that only the reader can answer.
func (m *Model) awaitingUser(s *session.Session) bool {
	for i := range m.approvals {
		if m.approvals[i].sess == s {
			return true
		}
	}
	for i := range m.choices {
		if m.choices[i].sess == s {
			return true
		}
	}
	return false
}

// stateMark is the glyph column: the spinner while something runs, because a
// still shape cannot say "still going", and the state's own shape otherwise.
func (m *Model) stateMark(s *session.Session) string {
	st := m.sessionState(s)
	if st == stateWorking {
		return m.spin.View()
	}
	return m.stateStyle(st).Render(st.glyph())
}

// tabMark is the state glyph for the header strip, or nothing.
//
// Nothing, for a conversation that is merely finished or merely empty. The
// sidebar shows every state because it is a list you read; the tab strip is
// chrome you glance at, and a row of ticks across the top is not a glance, it
// is wallpaper. A mark up there appears only when something wants you — which
// is what makes it worth seeing out of the corner of an eye.
//
// On the active tab the mark keeps its shape and loses its colour: the tab is
// already inverted, so a state colour on that background fights the highlight
// instead of adding to it, and you are looking at that conversation anyway.
func (m *Model) tabMark(s *session.Session, active bool) string {
	st := m.sessionState(s)
	switch st {
	case stateWorking:
		if active {
			// The spinner is a shape that moves, which is the whole of what
			// it has to say; its colour on an inverted tab is the clash this
			// rule exists to avoid.
			return stripANSI(m.spin.View())
		}
		return m.spin.View()
	case stateBlocked, stateFailed, stateUnseen:
		if active {
			return st.glyph()
		}
		return m.stateStyle(st).Render(st.glyph())
	}
	return ""
}

// markUnseen records that a turn ended on a conversation nobody was looking
// at. It is the state that makes a list of conversations worth having: the
// answer arrived, and it arrived somewhere you were not.
//
// Runtime only. After a restart everything reads as idle, which is honest —
// what this tracks is whether you have looked since it happened, and a new
// process has no memory of that.
func (m *Model) markUnseen(s *session.Session) {
	if s != nil && s != m.mgr.Active() {
		s.Unseen = true
	}
}
