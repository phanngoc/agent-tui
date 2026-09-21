package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

// talking gives a session something in it, so it is not simply new.
func talking(m *Model, title string) *session.Session {
	s := m.mgr.New()
	s.Title = title
	s.Append(session.Message{Role: session.RoleUser, Text: "hỏi"})
	s.Append(session.Message{Role: session.RoleAssistant, Text: "đáp"})
	return s
}

// Every state gets its own shape. Colour alone is not a signal: blocked,
// working and done were all a filled dot in the sidebar herdr had to change,
// and to a colourblind reader those three collapse into one.
func TestEveryStateHasItsOwnShape(t *testing.T) {
	seen := map[string]sessionState{}
	for _, st := range []sessionState{
		stateEmpty, stateIdle, stateUnseen, stateWorking, stateBlocked, stateFailed,
	} {
		if st == stateWorking {
			continue // a spinner, which is a shape that moves
		}
		g := st.glyph()
		if g == "" {
			t.Errorf("%s has no shape", st.word())
		}
		if was, dup := seen[g]; dup {
			t.Errorf("%s and %s are both %q", was.word(), st.word(), g)
		}
		seen[g] = st
	}
	// And each is one cell, or the column stops being a column.
	for g := range seen {
		if n := len([]rune(g)); n != 1 {
			t.Errorf("%q is %d runes wide", g, n)
		}
	}
}

// The word is said as well as drawn. A glyph nobody has learned yet is a
// decoration.
func TestEveryStateSaysItsName(t *testing.T) {
	for _, st := range []sessionState{
		stateEmpty, stateIdle, stateUnseen, stateWorking, stateBlocked, stateFailed,
	} {
		if strings.TrimSpace(st.word()) == "" {
			t.Errorf("state %d has no word", st)
		}
	}
}

// Waiting on the reader outranks everything: a session can be blocked and busy
// at once — the turn is running, and it is running because it is waiting — and
// the half you can do something about is the half worth showing.
func TestBlockedOutranksWorking(t *testing.T) {
	m := newTestModel(t)
	s := talking(m, "scan")
	s.Busy = true
	m.approvals = append(m.approvals, pendingApproval{sess: s, ev: agent.EvApproval{}})

	if got := m.sessionState(s); got != stateBlocked {
		t.Errorf("a session waiting on an approval reads as %q", got.word())
	}
	// A question put to the user blocks it just the same.
	m.approvals = nil
	m.choices = append(m.choices, pendingChoice{sess: s, ev: agent.EvChoice{}})
	if got := m.sessionState(s); got != stateBlocked {
		t.Errorf("a session waiting on a choice reads as %q", got.word())
	}
	// And an approval queued against another session does not block this one.
	other := talking(m, "khác")
	if got := m.sessionState(other); got == stateBlocked {
		t.Error("another session's question blocked this one")
	}
}

// A `!` command running is the session working, even though it does not make
// the session busy.
func TestAShellCommandCountsAsWorking(t *testing.T) {
	m := newTestModel(t)
	s := talking(m, "build")
	s.Running = 1

	if got := m.sessionState(s); got != stateWorking {
		t.Errorf("a session with a command in flight reads as %q", got.word())
	}
}

// The state worth having a list for: the answer arrived somewhere you were not.
func TestATurnEndingElsewhereIsMarkedUnseen(t *testing.T) {
	m := newTestModel(t)
	here := m.mgr.Active()
	there := talking(m, "ở nơi khác")
	m.mgr.Select(m.indexOf(here))

	m.markUnseen(there)
	if got := m.sessionState(there); got != stateUnseen {
		t.Errorf("a turn that finished elsewhere reads as %q", got.word())
	}

	// A turn finishing on the conversation you are looking at is not news.
	m.markUnseen(here)
	if here.Unseen {
		t.Error("the conversation on screen was marked unseen")
	}

	// And looking is what clears it.
	m.mgr.Select(m.indexOf(there))
	m.onSessionSwitch()
	if there.Unseen {
		t.Error("switching to it did not count as looking at it")
	}
}

// A failed turn says so rather than reading as finished.
func TestAFailedTurnHasItsOwnState(t *testing.T) {
	m := newTestModel(t)
	s := talking(m, "migrate")
	s.LastErr = "rate limited"

	if got := m.sessionState(s); got != stateFailed {
		t.Errorf("a session whose last turn failed reads as %q", got.word())
	}
}

// An empty conversation is not idle — there is nothing for it to be idle
// about — and it is the one thing the list showed no difference for at all.
func TestAnEmptyConversationIsNotIdle(t *testing.T) {
	m := newTestModel(t)
	if got := m.sessionState(m.mgr.New()); got != stateEmpty {
		t.Errorf("a conversation with nothing in it reads as %q", got.word())
	}
}

// The glyph column says what a conversation is doing; the row's background
// says where you are standing. They used to share the column, and a session
// that was both active and running lost the mark that said where you were.
func TestRunningAndActiveAreSaidSeparately(t *testing.T) {
	m := newTestModel(t)
	s := talking(m, "đang chạy ngay đây")
	s.Busy = true
	m.mgr.Select(m.indexOf(s))
	m.setFocus(focusChat) // so the selection is not also on this row

	rows := m.sessionLines(34)
	var line string
	for _, r := range rows {
		if m.mgr.All()[r.idx] == s {
			line = r.text
			break
		}
	}
	if line == "" {
		t.Fatal("the active session is not in the list")
	}
	// Still marked as running…
	if got := stripANSI(line); !strings.HasPrefix(strings.TrimSpace(got), m.spin.View()) &&
		!strings.Contains(got, "đang chạy") {
		t.Fatalf("the row is not the one expected: %q", got)
	}
	// …and still marked as where you are.
	if !strings.Contains(line, "48;2;") {
		t.Errorf("the active row lost its background while it was running:\n%q", line)
	}
}

// The state leads the line under the title, in its own colour, because it is
// the reason to look at that line at all.
func TestTheMetaLineLeadsWithTheState(t *testing.T) {
	m := newTestModel(t)
	s := talking(m, "t")
	s.LastErr = "boom"

	got := stripANSI(m.sessionMeta(s, 40))
	if !strings.HasPrefix(got, "failed") {
		t.Errorf("the line under the title reads %q, want the state first", got)
	}
	// A working session says what it is doing rather than how long ago it
	// last moved, which is not the interesting number while it is moving.
	s.LastErr, s.Busy, s.Status = "", true, "running tests"
	if got := stripANSI(m.sessionMeta(s, 40)); !strings.HasPrefix(got, "running tests") {
		t.Errorf("a working session reads %q", got)
	}
	if strings.Contains(stripANSI(m.sessionMeta(s, 40)), "ago") {
		t.Error("a working session is still counting how long since it last moved")
	}
}

// Whatever it says, it says it inside the pane.
func TestTheMetaLineFitsTheSidebar(t *testing.T) {
	m := newTestModel(t)
	s := talking(m, "t")
	s.Busy, s.Status = true, strings.Repeat("một trạng thái rất dài ", 10)

	for _, w := range []int{12, 20, 34, 60} {
		if got := ansi.StringWidth(m.sessionMeta(s, w)); got > w {
			t.Errorf("width %d: the line is %d columns", w, got)
		}
	}
}

// The shape, the colour and the word are one statement, so the spinner has to
// be the same colour as the word it stands next to. Two signals that disagree
// about what colour "working" is are two signals.
func TestTheSpinnerWearsTheWorkingColour(t *testing.T) {
	m := newTestModel(t)
	want := m.st.Warn.Render("x")
	got := m.spin.View()

	code := func(s string) string {
		if i := strings.Index(s, "m"); i > 0 {
			return s[:i]
		}
		return s
	}
	if code(got) != code(want) {
		t.Errorf("the spinner is %q and the working label is %q", code(got), code(want))
	}
}
