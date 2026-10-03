package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/agent"
)

// streamingTranscript is a long conversation with a turn still streaming, so
// there is somewhere to scroll to and something arriving.
func streamingTranscript(t *testing.T) (*Model, func(string)) {
	t.Helper()
	m := newTestModel(t)
	agentSays(m, strings.Repeat("an earlier line of the answer\n\n", 60))
	_, send := busyTurn(m)
	stream := func(text string) {
		send(agent.EvTextDelta{Text: text})
		m.View() // the transcript reaches the viewport while drawing
	}
	stream("the new answer begins\n\n")
	return m, stream
}

func wheelOverChat(m *Model, b tea.MouseButton, times int) {
	left, top, _, h, _ := m.paneBox(focusChat)
	for i := 0; i < times; i++ {
		m.onMouse(tea.MouseWheelMsg{X: left + 2, Y: top + h/2, Button: b})
	}
	m.View()
}

func TestStreamingFollowsTheBottom(t *testing.T) {
	m, stream := streamingTranscript(t)
	for i := 0; i < 5; i++ {
		stream("another line of the answer\n\n")
	}
	if !m.chat.AtBottom() {
		t.Error("a streaming answer was not followed while reading at the bottom")
	}
}

// TestScrollingUpIsNotUndoneByTheStream is the complaint: wheel up to reread
// something, and the next token pulled the view back down.
func TestScrollingUpIsNotUndoneByTheStream(t *testing.T) {
	m, stream := streamingTranscript(t)
	wheelOverChat(m, tea.MouseWheelUp, 5)
	at := m.chat.YOffset()
	if m.chat.AtBottom() {
		t.Fatal("the wheel did not scroll up")
	}

	for i := 0; i < 10; i++ {
		stream("more of the answer arriving\n\n")
	}
	if m.chat.YOffset() != at {
		t.Errorf("the stream moved the view from %d to %d after the reader scrolled up", at, m.chat.YOffset())
	}
	if out := stripANSI(m.View().Content); !strings.Contains(out, "newer below") {
		t.Errorf("nothing says there is more below:\n%s", out)
	}
}

// TestScrollingBackDownFollowsAgain: reaching the bottom by hand is the way
// back to following, as in every chat app.
func TestScrollingBackDownFollowsAgain(t *testing.T) {
	m, stream := streamingTranscript(t)
	wheelOverChat(m, tea.MouseWheelUp, 3)
	wheelOverChat(m, tea.MouseWheelDown, 40)
	if !m.chat.AtBottom() {
		t.Fatal("did not get back to the bottom")
	}
	stream("a line after coming back\n\n")
	stream("and another\n\n")
	if !m.chat.AtBottom() {
		t.Error("back at the bottom, the stream is no longer followed")
	}
	if out := stripANSI(m.View().Content); strings.Contains(out, "newer below") {
		t.Error("following again, but the title still says there is more below")
	}
}

func TestCtrlEndAndEndGoBackToTheBottom(t *testing.T) {
	m, stream := streamingTranscript(t)
	wheelOverChat(m, tea.MouseWheelUp, 5)
	press(t, m, "ctrl+end")
	stream("after ctrl+end\n\n")
	if !m.chat.AtBottom() || m.chatAway {
		t.Error("ctrl+end did not go back to following")
	}

	wheelOverChat(m, tea.MouseWheelUp, 5)
	m.setFocus(focusChat)
	press(t, m, "end")
	stream("after end\n\n")
	if !m.chat.AtBottom() {
		t.Error("end in the transcript did not go back to following")
	}
}

// TestSendingAPromptFollows: asking something is wanting to see the answer.
func TestSendingAPromptFollows(t *testing.T) {
	m, _ := streamingTranscript(t)
	wheelOverChat(m, tea.MouseWheelUp, 5)
	m.mgr.Active().Busy = false
	m.input.SetValue("!echo hi")
	press(t, m, "enter")
	if m.chatAway {
		t.Error("sending did not go back to following")
	}
}

func TestSwitchingSessionFollows(t *testing.T) {
	m, _ := streamingTranscript(t)
	wheelOverChat(m, tea.MouseWheelUp, 5)
	press(t, m, "ctrl+t")
	if m.chatAway {
		t.Error("a new session opened scrolled away")
	}
}
