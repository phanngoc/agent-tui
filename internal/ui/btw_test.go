package ui

import (
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/session"
)

// withHistory gives the active conversation something to be asked beside.
func withHistory(m *Model) *session.Session {
	s := m.mgr.Active()
	s.Messages = append(s.Messages,
		session.Message{Role: session.RoleUser, Text: "tìm resolvePfid"},
		session.Message{Role: session.RoleAssistant, Text: "Nó ở onboarding.service.ts."},
	)
	m.invalidateChat()
	return s
}

// A long turn is exactly when a question occurs to you, and the two places to
// put it were both wrong: into the turn, where it waits and then lands in the
// context of everything after it, or into a new session, where the agent knows
// nothing about what you are both looking at.
func TestBtwForksTheConversationBeside(t *testing.T) {
	m := newTestModel(t)
	parent := withHistory(m)

	m.openBtw("")

	side := m.sideSession()
	if side == nil {
		t.Fatal("/btw made no side chat")
	}
	if side.SideOf != parent.ID {
		t.Errorf("the side chat belongs to %q, want %q", side.SideOf, parent.ID)
	}
	if len(side.Messages) != len(parent.Messages) {
		t.Errorf("the side chat carries %d messages, want the %d it was forked from",
			len(side.Messages), len(parent.Messages))
	}
	// The conversation you were in is the one you are still in.
	if m.mgr.Active() != parent {
		t.Error("/btw moved you out of the conversation")
	}
	if !m.showBtw || m.focus != focusBtw {
		t.Errorf("the pane is not open and focused: showBtw=%v focus=%v", m.showBtw, m.focus)
	}
}

// The main transcript never learns it happened.
func TestTheMainTranscriptDoesNotSeeTheSideChat(t *testing.T) {
	m := newTestModel(t)
	parent := withHistory(m)
	before := len(parent.Messages)

	m.openBtw("")
	m.sendTo(m.sideSession(), "cái pfid này là gì?")

	if got := len(parent.Messages); got != before {
		t.Errorf("the conversation gained %d messages from the aside", got-before)
	}
	if out := stripANSI(m.transcript(80)); strings.Contains(out, "cái pfid này là gì?") {
		t.Errorf("the aside is in the main transcript:\n%s", out)
	}
	if out := stripANSI(m.btwPane(60)); !strings.Contains(out, "cái pfid này là gì?") {
		t.Errorf("the aside is not in its own pane:\n%s", out)
	}
}

// The prompt serves both panes, and which one it is talking to is whichever
// has the caret.
func TestThePromptFollowsTheFocus(t *testing.T) {
	m := newTestModel(t)
	parent := withHistory(m)
	m.openBtw("")
	side := m.sideSession()

	if m.promptTarget() != side {
		t.Error("with the caret in the side chat, the prompt is still addressing the conversation")
	}
	m.setFocus(focusChat)
	if m.promptTarget() != parent {
		t.Error("with the caret in the transcript, the prompt is addressing the aside")
	}
}

// A side chat is a place: closing it hides the pane, and opening it again
// finds the same conversation rather than a fresh one.
func TestClosingTheSideChatKeepsIt(t *testing.T) {
	m := newTestModel(t)
	withHistory(m)
	m.openBtw("")
	side := m.sideSession()

	m.closeBtw()
	if m.showBtw || m.btwW != 0 {
		t.Errorf("the pane is still open: showBtw=%v width=%d", m.showBtw, m.btwW)
	}
	if m.focus == focusBtw {
		t.Error("the caret was left in a pane that is not there")
	}

	m.openBtw("")
	if m.sideSession() != side {
		t.Error("reopening made a second side chat")
	}
}

// It is not in the list of conversations: it belongs to one rather than
// standing beside them. The rows still carry the index the manager knows each
// session by, or the active mark and every click would point at the wrong one.
func TestTheSideChatIsNotListed(t *testing.T) {
	m := newTestModel(t)
	withHistory(m)
	parent := m.mgr.Active()
	m.openBtw("")

	lines := m.sessionLines(30)
	for _, l := range lines {
		if m.mgr.All()[l.idx].SideOf != "" {
			t.Errorf("row %q points at a side chat", stripANSI(l.text))
		}
	}
	// Cycling steps over it too.
	m.mgr.New()
	m.mgr.Cycle(1)
	m.mgr.Cycle(1)
	if m.mgr.Active().SideOf != "" {
		t.Error("cycling landed in a side chat")
	}
	_ = parent
}

// Closing a conversation takes its aside with it. Left behind, it is a session
// nothing lists and nothing can reach.
func TestClosingAConversationTakesItsSideChat(t *testing.T) {
	m := newTestModel(t)
	withHistory(m)
	m.mgr.New() // somewhere to land afterwards
	m.mgr.Select(1)
	m.openBtw("")

	m.mgr.Close(m.mgr.ActiveIndex())

	for _, s := range m.mgr.All() {
		if s.SideOf != "" {
			t.Error("the side chat outlived the conversation it belonged to")
		}
	}
}

// Asking beside a conversation that has not started is just starting one, and
// there is already a key for that.
func TestBtwNeedsSomethingToAskBeside(t *testing.T) {
	m := newTestModel(t)
	if cmd := m.openBtw(""); cmd != nil {
		t.Error("/btw on an empty conversation started a turn")
	}
	if m.sideSession() != nil {
		t.Error("/btw forked an empty conversation")
	}
	if m.notice == "" {
		t.Error("/btw refused without saying why")
	}
}

// The pane shows the aside, not the context it inherited. The agent is given
// all of it — that is what makes asking beside worth anything — but the reader
// has it already, in the pane next to this one.
func TestThePaneShowsOnlyTheAside(t *testing.T) {
	m := newTestModel(t)
	withHistory(m)
	m.openBtw("")
	side := m.sideSession()

	if side.SideFrom != len(side.Messages) {
		t.Fatalf("the aside starts at %d of %d inherited", side.SideFrom, len(side.Messages))
	}
	// Empty until something is asked in it.
	if out := stripANSI(m.btwPane(40)); !strings.Contains(out, "Ask anything here") {
		t.Errorf("a fresh aside is showing something:\n%s", out)
	}

	side.Messages = append(side.Messages,
		session.Message{Role: session.RoleUser, Text: "pfid là gì?"})

	out := stripANSI(m.btwPane(40))
	if !strings.Contains(out, "pfid là gì?") {
		t.Errorf("the question is not in the pane:\n%s", out)
	}
	if strings.Contains(out, "tìm resolvePfid") {
		t.Errorf("the pane repeats the conversation it was forked from:\n%s", out)
	}
	// The agent still gets the lot.
	if len(side.Messages) <= 1 {
		t.Error("the aside was not forked with its context")
	}
}

// The side chat stands in the preview's column rather than beside it. Four
// panes on a terminal is three columns of forty and nothing readable in any of
// them, and the two are the same kind of thing: something you consult beside
// the conversation, never both at once.
func TestTheSideChatStandsInForThePreview(t *testing.T) {
	for _, w := range []int{100, 120, 150, 220} {
		m := newTestModel(t)
		withHistory(m)
		m.resize(w, 30)
		if m.prevW == 0 {
			t.Fatalf("w=%d: no preview to begin with", w)
		}
		was := m.prevW
		m.openBtw("")
		m.resize(w, 30)

		switch {
		case m.prevW != 0:
			t.Errorf("w=%d: both panes are on screen at once", w)
		case m.btwW != was:
			t.Errorf("w=%d: the side chat took %d columns, want the preview's %d", w, m.btwW, was)
		case m.chatW < paneRoom:
			t.Errorf("w=%d: the transcript was squeezed to %d", w, m.chatW)
		}
		if got := m.sideW + m.chatW + m.btwW + m.prevW; got != w {
			t.Errorf("w=%d: the panes add up to %d", w, got)
		}
	}
}

// Nothing has to remember whether the preview was open, because it was never
// closed: it is standing aside, and closing the aside gives the column back.
func TestClosingTheAsideGivesTheColumnBack(t *testing.T) {
	m := newTestModel(t)
	withHistory(m)
	m.resize(160, 30)
	was := m.prevW

	m.openBtw("")
	m.resize(160, 30)
	m.closeBtw()

	if m.prevW != was {
		t.Errorf("the preview came back at %d columns, want the %d it had", m.prevW, was)
	}
	if m.btwW != 0 {
		t.Errorf("the side chat kept %d columns after closing", m.btwW)
	}
}

// And asking for the preview asks the aside to step out, since they are the
// same column. The conversation in it is kept, as closing it any other way
// keeps it.
func TestOpeningThePreviewStepsTheAsideOut(t *testing.T) {
	m := newTestModel(t)
	withHistory(m)
	m.resize(160, 30)
	m.openBtw("")
	m.resize(160, 30)
	side := m.sideSession()

	m.togglePreview() // ctrl+e: the preview was never closed, so this closes it
	m.togglePreview() // and this asks for it back
	if m.showBtw || m.btwW != 0 {
		t.Errorf("the aside is still in the column: showBtw=%v width=%d", m.showBtw, m.btwW)
	}
	if m.prevW == 0 {
		t.Error("the preview did not take the column back")
	}
	if m.sideSession() != side {
		t.Error("stepping out of the column ended the side chat")
	}
}

// The one width is dragged as one divider, whichever pane is standing in it.
func TestTheColumnKeepsItsWidthAcrossThePanes(t *testing.T) {
	m := newTestModel(t)
	withHistory(m)
	m.resize(160, 30)
	m.setPrevWidth(52)
	if m.prevW != 52 {
		t.Fatalf("the preview is %d columns after being dragged to 52", m.prevW)
	}
	m.openBtw("")
	m.resize(160, 30)
	if m.btwW != 52 {
		t.Errorf("the side chat is %d columns in a column dragged to 52", m.btwW)
	}
}
