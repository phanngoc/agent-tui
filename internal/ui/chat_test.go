package ui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/session"
)

// longSession fills the active session with n exchanges, each answer taller
// than the pane, so the transcript is far longer than the viewport.
func longSession(t *testing.T, m *Model, n int) *session.Session {
	t.Helper()
	s := m.mgr.Active()
	for i := range n {
		// Separate paragraphs, not one wrapped block: the renderer joins
		// consecutive lines, and the answer has to be taller than the pane for
		// where it starts to be a different place from where it ends.
		s.Messages = append(s.Messages,
			session.Message{Role: session.RoleUser, Text: "câu hỏi " + strconv.Itoa(i)},
			session.Message{Role: session.RoleAssistant,
				Text: strings.Repeat("dòng trả lời "+strconv.Itoa(i)+"\n\n", 40)},
		)
	}
	m.invalidateChat()
	return s
}

// visibleLines is what the reader actually sees in the transcript, with the
// padding the viewport draws around it taken out.
func visibleLines(m *Model) []string {
	var out []string
	for _, l := range strings.Split(stripANSI(m.chat.View()), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimRight(l, " "))
		}
	}
	return out
}

// opensOn checks that the transcript is showing an exchange from its top: the
// tag line first, then what the user said.
func opensOn(t *testing.T, m *Model, question string) {
	t.Helper()
	lines := visibleLines(m)
	if len(lines) < 2 {
		t.Fatal("the transcript is empty")
	}
	if !strings.Contains(lines[0], "you") || !strings.Contains(lines[1], question) {
		t.Errorf("the transcript opens on %q / %q, want the %q exchange",
			lines[0], lines[1], question)
	}
}

// Opening a session lands on the question that started the newest exchange,
// not on the last line of the answer to it. The bottom of a long reply is the
// least useful line in it: it is the end of something you have not read.
func TestSessionOpensAtTheNewestExchange(t *testing.T) {
	m := newTestModel(t)
	longSession(t, m, 8)
	m.showLatestTurn()

	opensOn(t, m, "câu hỏi 7")
	if strings.Contains(stripANSI(m.chat.View()), "câu hỏi 6") {
		t.Error("the exchange before it should be above the fold, not on screen")
	}
	if m.chat.YOffset() == 0 {
		t.Error("the transcript opened at the top of the whole history")
	}
	// The answer to it has to be on screen, otherwise landing on the question
	// only trades one useless line for another.
	if !strings.Contains(stripANSI(m.chat.View()), "dòng trả lời 7") {
		t.Error("the newest answer should be visible under its question")
	}
}

// A session switch positions the incoming transcript, not the one that was on
// screen a moment ago: the offset is clamped against content, and the content
// is only handed to the viewport a frame later.
func TestSwitchingIntoALongSessionLandsOnItsNewestExchange(t *testing.T) {
	m := newTestModel(t)
	longSession(t, m, 8)

	// A new session goes to the front of the list, pushing the long one to 1.
	short := m.mgr.New()
	short.Messages = append(short.Messages,
		session.Message{Role: session.RoleUser, Text: "ngắn"},
		session.Message{Role: session.RoleAssistant, Text: "xong"},
	)
	m.onSessionSwitch()

	m.mgr.Select(1)
	m.onSessionSwitch()

	opensOn(t, m, "câu hỏi 7")
}

// A session short enough to fit the pane has no newest exchange to scroll to:
// all of it is already on screen, and the offset clamps to zero.
func TestShortSessionIsNotScrolled(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Messages = append(s.Messages,
		session.Message{Role: session.RoleUser, Text: "chào"},
		session.Message{Role: session.RoleAssistant, Text: "chào bạn"},
	)
	m.invalidateChat()
	m.showLatestTurn()

	if got := m.chat.YOffset(); got != 0 {
		t.Errorf("a session that fits should not scroll, got offset %d", got)
	}
	if out := stripANSI(m.chat.View()); !strings.Contains(out, "chào bạn") {
		t.Errorf("the whole exchange should be on screen:\n%s", out)
	}
}

// The restored session is placed by the first layout — before it there is no
// pane to measure against — and a later resize leaves the reader where they
// were rather than yanking them back.
func TestFirstLayoutPlacesTheTranscript(t *testing.T) {
	m := newTestModel(t)
	longSession(t, m, 8)

	m.placedChat = false
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	placed := m.chat.YOffset()
	if placed == 0 {
		t.Fatal("the first layout left the transcript at the oldest message")
	}

	m.chat.GotoTop()
	m.Update(tea.WindowSizeMsg{Width: 158, Height: 44})
	if got := m.chat.YOffset(); got != 0 {
		t.Errorf("a resize moved the reader from %d to %d", 0, got)
	}
}

// A `!` command is the newest thing the user did, so the transcript opens on
// its output the same way it opens on an answer.
func TestShellRunCountsAsTheNewestExchange(t *testing.T) {
	m := newTestModel(t)
	longSession(t, m, 8)
	s := m.mgr.Active()
	s.Messages = append(s.Messages, session.Message{
		Role: session.RoleUser,
		Shell: &session.ShellRun{
			Where: "host", Command: "git status", Done: true,
			Output: "nothing to commit",
		},
	})
	m.invalidateChat()
	m.showLatestTurn()

	out := stripANSI(m.chat.View())
	if !strings.Contains(out, "git status") || !strings.Contains(out, "nothing to commit") {
		t.Errorf("the newest `!` run should be on screen:\n%s", out)
	}
}
