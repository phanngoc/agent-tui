package ui

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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

// A search hit is an address — a conversation and a message in it — so the
// transcript has to know where each message begins. The count is already being
// kept as the lines are written; this pins that it is kept correctly.
func TestEachMessageKnowsWhereItStarts(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	for i := 0; i < 8; i++ {
		s.Append(session.Message{Role: session.RoleUser, Text: "câu hỏi " + strconv.Itoa(i)})
		s.Append(session.Message{Role: session.RoleAssistant, Text: "trả lời " + strconv.Itoa(i)})
	}
	m.invalidateChat()

	h := m.renderHead(80)
	lines := strings.Split(h.text, "\n")
	if len(h.starts) != len(s.Messages) {
		t.Fatalf("%d starts for %d messages", len(h.starts), len(s.Messages))
	}
	for i := range s.Messages {
		at := h.starts[i]
		if at < 0 || at >= len(lines) {
			t.Fatalf("message %d starts at line %d of %d", i, at, len(lines))
		}
		// The block for a message opens with its own tag row.
		want := "you"
		if s.Messages[i].Role == session.RoleAssistant {
			want = "agent"
		}
		if got := stripANSI(lines[at]); !strings.Contains(got, want) {
			t.Errorf("message %d starts at %q, want the %s row", i, got, want)
		}
	}
	// And the newest exchange is not a second opinion about where things are.
	last := 0
	for i := range s.Messages {
		if s.Messages[i].Role == session.RoleUser {
			last = i
		}
	}
	if h.turn != h.starts[last] {
		t.Errorf("turn = %d, but the last question starts at %d", h.turn, h.starts[last])
	}
}

// And the transcript can be scrolled to one.
func TestShowMessageLandsOnIt(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	for i := 0; i < 20; i++ {
		s.Append(session.Message{Role: session.RoleUser, Text: "hỏi " + strconv.Itoa(i)})
		s.Append(session.Message{Role: session.RoleAssistant,
			Text: strings.Repeat("đáp "+strconv.Itoa(i)+" ", 20)})
	}
	m.invalidateChat()
	m.View()

	m.showMessage(6)
	if got := m.chat.YOffset(); got != m.chatStarts[6] {
		t.Errorf("the transcript is at line %d, want %d", got, m.chatStarts[6])
	}
	// Out of range falls back to the newest turn rather than to nowhere. The
	// viewport clamps an offset past the end, so the comparison is against
	// where landing on the newest turn actually puts it.
	m.showLatestTurn()
	want := m.chat.YOffset()
	m.showMessage(0)
	m.showMessage(9999)
	if got := m.chat.YOffset(); got != want {
		t.Errorf("an impossible message scrolled to %d, want the newest turn at %d", got, want)
	}
}

// bashCall builds a shell call with the given command.
func bashCall(cmd string, done bool) session.ToolCall {
	in, _ := json.Marshal(map[string]string{"command": cmd})
	t := session.ToolCall{ID: "t", Name: "Bash", Input: in, Done: done}
	if done {
		t.Result = "ok"
	}
	return t
}

func drawTool(m *Model, t session.ToolCall, width int) string {
	var b strings.Builder
	m.renderTool(&b, t, width)
	return b.String()
}

// The command being waited on is written out in full. Cutting it removes
// exactly the part that would have said what is being waited for.
func TestARunningCommandIsWrittenInFull(t *testing.T) {
	m := newTestModel(t)
	cmd := `NS=fpaas-2038 && kubectl exec -i deployment/workspace-backend -n $NS ` +
		`-- python3 - < /tmp/scratchpad/apply-pr-review-workflows.py`

	out := stripANSI(drawTool(m, bashCall(cmd, false), 74))
	// Every word of it survives, wherever the wrap put it.
	flat := strings.Join(strings.Fields(out), " ")
	for _, word := range strings.Fields(cmd) {
		if !strings.Contains(flat, word) {
			t.Errorf("the command lost %q:\n%s", word, out)
		}
	}
	if strings.Contains(out, "…") {
		t.Errorf("the command was cut anyway:\n%s", out)
	}
	if !strings.Contains(out, "⎿") {
		t.Errorf("nothing marks the lines below as belonging to the call:\n%s", out)
	}
}

// And a finished one stays a single line, because it is history and the
// summary is the part worth keeping.
func TestAFinishedCommandStaysOneLine(t *testing.T) {
	m := newTestModel(t)
	cmd := strings.Repeat("echo một lệnh khá dài && ", 8)

	out := drawTool(m, bashCall(cmd, true), 74)
	if n := strings.Count(out, "\n"); n != 1 {
		t.Errorf("a finished call took %d lines:\n%s", n, stripANSI(out))
	}
}

// Wrapped or not, it stays inside the pane.
func TestARunningCommandFitsThePane(t *testing.T) {
	m := newTestModel(t)
	cmd := "kubectl " + strings.Repeat("--một-cờ-rất-dài=giá-trị ", 12)

	for _, w := range []int{30, 48, 74, 120} {
		for i, l := range strings.Split(drawTool(m, bashCall(cmd, false), w), "\n") {
			if got := ansi.StringWidth(l); got > w {
				t.Errorf("width %d: line %d is %d columns", w, i, got)
			}
		}
	}
}

// One call may not fill the pane it is being watched in, and what it drops it
// says out loud.
func TestAVeryLongCommandIsCappedAndSaysSo(t *testing.T) {
	m := newTestModel(t)
	cmd := strings.Repeat("một-đoạn-dài-của-lệnh ", 200)

	out := stripANSI(drawTool(m, bashCall(cmd, false), 60))
	if n := strings.Count(out, "\n"); n > toolBodyRows+2 {
		t.Errorf("a running call took %d lines:\n%s", n, out)
	}
	if !strings.Contains(out, "more line") {
		t.Errorf("it cut the command without saying how much:\n%s", out)
	}
}
