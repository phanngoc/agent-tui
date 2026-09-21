package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/session"
)

// The side chat: a question you can ask without stopping what is running.
//
// A long turn is exactly when a question occurs to you, and until now the only
// places to put it were into the turn — where it waits, and where it lands in
// the context of everything after it — or into a new session, where the agent
// knows nothing about what you are both looking at.
//
// So `/btw` forks the conversation and puts the branch in a pane of its own.
// It is a real session: its own history, its own engine, and it runs at the
// same time, because sessions here already do. What makes it a side chat is
// that it belongs to the conversation it was asked beside — it is shown in
// that conversation's pane rather than in the list of them, and the main
// transcript never learns it happened.

// sideSession is the active conversation's side chat, or nil.
func (m *Model) sideSession() *session.Session {
	return m.mgr.SideOf(m.mgr.Active().ID)
}

// openBtw shows the side chat, making one if this conversation has none.
//
// The question, if there is one, goes straight to it: `/btw what is a pfid`
// should not need a second keystroke to send.
func (m *Model) openBtw(question string) tea.Cmd {
	parent := m.mgr.Active()
	if parent.SideOf != "" {
		m.notice = "already in a side chat"
		return nil
	}

	side := m.mgr.SideOf(parent.ID)
	if side == nil {
		if len(parent.Messages) == 0 {
			// Nothing to ask beside. A side chat of an empty conversation is
			// just a new one, and there is a key for that.
			m.notice = "nothing to ask about yet — ctrl+t starts a session"
			return nil
		}
		side = m.mgr.Fork(parent)
		side.SideOf, side.SideFrom = parent.ID, len(side.Messages)
		// Forking moves the manager's cursor to the branch, which is not what
		// a side chat is: the conversation you were in is the one you are
		// still in.
		m.mgr.Select(m.indexOf(parent))
		m.mgr.Save(side)
	}

	hadPreview := m.prevW > 0
	m.showBtw = true
	m.resize(m.w, m.h)
	m.setFocus(focusBtw)

	// It stands in the preview's column rather than beside it. Say so, because
	// a pane that disappears without a word reads as a fault: the preview is
	// not closed, it is waiting, and it is back the moment this one is.
	if question == "" {
		m.notice = "asking beside " + parent.Label()
		if hadPreview {
			m.notice += " — the preview is back when you close it"
		}
	}
	if question == "" {
		return nil
	}
	m.pushHistory("/btw " + question)
	return m.sendTo(side, question)
}

// closeBtw hides the pane. The conversation in it is kept: a side chat you
// closed and reopened is the same one, which is what makes it a place rather
// than a prompt.
func (m *Model) closeBtw() {
	if !m.showBtw {
		return
	}
	m.showBtw = false
	if m.focus == focusBtw {
		m.setFocus(focusInput)
	}
	m.resize(m.w, m.h)
}

// indexOf finds a session in the manager's order.
func (m *Model) indexOf(want *session.Session) int {
	for i, s := range m.mgr.All() {
		if s == want {
			return i
		}
	}
	return m.mgr.ActiveIndex()
}

// btwTitle names the pane and says what the side chat is doing.
func (m *Model) btwTitle() string {
	side := m.sideSession()
	if side == nil {
		return "btw"
	}
	// The aside is not in the session list or the tab strip — it belongs to
	// the conversation beside it rather than standing among them — so this
	// title is the only place its state is said, and it says it the same way
	// they would.
	st := m.sessionState(side)
	if st == stateIdle || st == stateEmpty {
		return "btw"
	}
	word := st.word()
	if st == stateWorking && side.Status != "" {
		word = side.Status
	}
	return "btw  " + st.glyph() + " " + word
}

// btwPane renders the side chat. It is not cached the way the main transcript
// is: a side chat is a handful of messages by construction, and the cache
// would have to be a second one because the two panes are drawn in the same
// frame as different conversations.
func (m *Model) btwPane(width int) string {
	side := m.sideSession()
	if side == nil || side.SideFrom >= len(side.Messages) {
		return m.st.Faint.Render(
			"\n  Ask anything here.\n\n  It carries this conversation's\n  context and leaves it alone.")
	}
	// Only what was said in the aside. The inherited half is what the agent
	// was given, and it is already on screen in the pane beside this one.
	view := *side
	view.Messages = side.Messages[min(side.SideFrom, len(side.Messages)):]
	return m.renderHeadOf(&view, max(10, width)).text
}
