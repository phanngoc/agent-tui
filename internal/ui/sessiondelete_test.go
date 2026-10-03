package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/session"
)

// listOf gives the model n conversations with something in them, the
// first one active, and puts the caret in the session list.
func listOf(m *Model, titles ...string) []*session.Session {
	var out []*session.Session
	for i := len(titles) - 1; i >= 0; i-- {
		s := m.mgr.New()
		s.Append(session.Message{Role: session.RoleUser, Text: titles[i]})
		out = append([]*session.Session{s}, out...)
	}
	m.onSessionSwitch()
	m.setFocus(focusSessions)
	m.sessSel = 0
	return out
}

// d deletes at once — no question in the way — and u brings it back.
func TestDeleteIsImmediateAndUndoable(t *testing.T) {
	m := newTestModel(t)
	ss := listOf(m, "push noti", "batch flow")

	press(t, m, "d")
	if m.mgr.IndexOf(ss[0].ID) >= 0 {
		t.Fatal("d did not delete the selected conversation")
	}
	if !strings.Contains(m.notice, "u brings it back") {
		t.Errorf("the notice does not say how to undo: %q", m.notice)
	}

	press(t, m, "u")
	if m.mgr.IndexOf(ss[0].ID) != 0 {
		t.Error("u did not bring the conversation back where it was")
	}
}

// Deleting a running conversation stops its turn, which nothing undoes — so
// that one delete asks, by taking a second press.
func TestDeletingARunningConversationTakesASecondPress(t *testing.T) {
	m := newTestModel(t)
	ss := listOf(m, "running", "idle")
	ss[0].Busy = true

	press(t, m, "d")
	if m.mgr.IndexOf(ss[0].ID) < 0 {
		t.Fatal("a running conversation was deleted on the first press")
	}
	if !strings.Contains(m.notice, "d again") {
		t.Errorf("the first press does not say what the second will do: %q", m.notice)
	}
	press(t, m, "d")
	if m.mgr.IndexOf(ss[0].ID) >= 0 {
		t.Fatal("the second press did not delete it")
	}
	if ss[0].Busy {
		t.Error("deleting left the turn running")
	}
}

// space marks several; d takes them all, and one u brings them all back.
func TestMarkedConversationsAreDeletedTogether(t *testing.T) {
	m := newTestModel(t)
	ss := listOf(m, "one", "two", "three")

	press(t, m, "space")
	press(t, m, "down")
	press(t, m, "down")
	press(t, m, "space")
	if out := stripANSI(m.View().Content); !strings.Contains(out, "✓") {
		t.Error("marked rows are not drawn as marked")
	}
	press(t, m, "d")
	if m.mgr.IndexOf(ss[0].ID) >= 0 || m.mgr.IndexOf(ss[2].ID) >= 0 {
		t.Fatal("not every marked conversation was deleted")
	}
	if m.mgr.IndexOf(ss[1].ID) < 0 {
		t.Fatal("an unmarked conversation was deleted")
	}
	press(t, m, "u")
	if m.mgr.IndexOf(ss[0].ID) < 0 || m.mgr.IndexOf(ss[2].ID) < 0 {
		t.Error("one u did not bring them all back")
	}
}

// esc clears the marks before it does anything else.
func TestEscClearsTheMarks(t *testing.T) {
	m := newTestModel(t)
	listOf(m, "one", "two")
	press(t, m, "space")
	press(t, m, "esc")
	if len(m.del.marks) != 0 {
		t.Error("esc left the marks")
	}
	if m.focus != focusSessions {
		t.Error("clearing the marks also moved the caret")
	}
}

// x closes: out of the list, still in the store for /recall.
func TestCloseKeepsTheConversation(t *testing.T) {
	m := newTestModel(t)
	ss := listOf(m, "keep me", "other")
	press(t, m, "x")
	if m.mgr.IndexOf(ss[0].ID) >= 0 {
		t.Fatal("x did not take the conversation out of the list")
	}
	if !strings.Contains(m.notice, "/recall") {
		t.Errorf("closing does not say where it went: %q", m.notice)
	}
	if s, _, err := m.mgr.Open(ss[0].ID); err != nil || s == nil {
		t.Errorf("the closed conversation is gone from the store: %v", err)
	}
}

// The row under the pointer shows ✕ on its first line, and clicking it
// deletes that conversation.
func TestTheHoveredRowDeletesFromItsCross(t *testing.T) {
	m := newTestModel(t)
	ss := listOf(m, "first", "second")
	m.View()

	// The second conversation's first line.
	lines := m.sessionLines(max(4, m.sideW-2))
	row := -1
	for i, l := range lines {
		if l.idx == m.mgr.IndexOf(ss[1].ID) {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatal("the second conversation is not in the list")
	}
	y := m.sessTopY() + 1 + row - m.sessTop
	x := m.colX(colSide) + m.sideW - 2

	m.Update(tea.MouseMotionMsg{X: x, Y: y})
	if !strings.Contains(m.sessionLines(max(4, m.sideW-2))[row].text, "✕") {
		t.Fatal("the hovered row has no ✕")
	}
	clickAt(m, x, y)
	if m.mgr.IndexOf(ss[1].ID) >= 0 {
		t.Error("clicking the ✕ did not delete the conversation")
	}
	if m.mgr.IndexOf(ss[0].ID) < 0 {
		t.Error("clicking the ✕ deleted the wrong conversation")
	}

	// Anywhere else on a row still opens it.
	m.Update(tea.MouseMotionMsg{X: m.colX(colSide) + 3, Y: y})
	clickAt(m, m.colX(colSide)+3, m.sessTopY()+1)
	if m.mgr.IndexOf(ss[0].ID) < 0 {
		t.Error("a click on a row's title deleted it")
	}
}

// /delete and /undo do the same from the prompt.
func TestDeleteAndUndoAsCommands(t *testing.T) {
	m := newTestModel(t)
	ss := listOf(m, "via slash", "other")
	m.setFocus(focusInput)
	m.runSlash("delete", "")
	if m.mgr.IndexOf(ss[0].ID) >= 0 {
		t.Fatal("/delete did not delete the active conversation")
	}
	m.runSlash("undo", "")
	if m.mgr.Active() != ss[0] {
		t.Error("/undo did not bring it back as the active conversation")
	}
}

// The conversation that just started running goes to the top of the list,
// and the list's cursor stays on the conversation it was on.
func TestARunningConversationFloatsToTheTop(t *testing.T) {
	m := newTestModel(t)
	ss := listOf(m, "first", "second", "third")
	m.sessSel = 1 // on "second"

	m.sendTo(ss[2], "chạy test")
	if got := m.mgr.IndexOf(ss[2].ID); got != 0 {
		t.Errorf("the conversation that started a turn is at %d, want the top", got)
	}
	if all := m.mgr.All(); all[m.sessSel] != ss[1] {
		t.Errorf("the list's cursor moved to %q", all[m.sessSel].Label())
	}
	if m.mgr.Active() != ss[0] {
		t.Error("a turn starting elsewhere changed the active conversation")
	}
}
