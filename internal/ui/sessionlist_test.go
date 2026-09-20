package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/session"
)

func TestSlashRenameWithAName(t *testing.T) {
	m := newTestModel(t)

	m.input.SetValue("/rename fix the ranking in project search")
	m.inputKey(key("enter"))

	if got := m.mgr.Active().Title; got != "fix the ranking in project search" {
		t.Errorf("title = %q", got)
	}
	if m.overlay != overlayNone {
		t.Error("naming it directly still opened the editor")
	}
	if len(m.mgr.Active().Messages) != 0 {
		t.Error("/rename was sent to the agent")
	}
}

func TestSlashRenameOpensTheEditor(t *testing.T) {
	m := newTestModel(t)
	m.mgr.Active().Title = "existing name"

	m.input.SetValue("/rename")
	m.inputKey(key("enter"))

	if m.overlay != overlayRename {
		t.Fatalf("overlay = %v, want the rename editor", m.overlay)
	}
	// It has to open on the current name, so a correction is an edit rather
	// than retyping the whole thing.
	if got := m.renameIn.Value(); got != "existing name" {
		t.Errorf("editor opened with %q", got)
	}

	m.renameIn.SetValue("a better name")
	m.renameKey(key("enter"))

	if got := m.mgr.Active().Title; got != "a better name" {
		t.Errorf("title = %q", got)
	}
	if m.overlay != overlayNone {
		t.Error("the editor stayed open")
	}
}

func TestRenameEscapeKeepsTheOldName(t *testing.T) {
	m := newTestModel(t)
	m.mgr.Active().Title = "keep me"

	m.openRename()
	m.renameIn.SetValue("discard me")
	m.renameKey(key("esc"))

	if got := m.mgr.Active().Title; got != "keep me" {
		t.Errorf("title = %q, want the original", got)
	}
}

// TestRenameToEmptyHandsTheTitleBack is the way out of a bad rename: clearing
// it lets the next prompt suggest one again.
func TestRenameToEmptyHandsTheTitleBack(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Title = "a name I regret"

	m.setSessionName("   ")
	if s.Title != "" {
		t.Errorf("title = %q, want empty", s.Title)
	}
	if s.Label() != "new session" {
		t.Errorf("label = %q", s.Label())
	}

	s.Append(session.Message{Role: session.RoleUser, Text: "make search rank shorter paths"})
	if s.Title == "" {
		t.Error("a prompt after clearing did not suggest a name")
	}
}

// TestRenameActsOnTheHighlightedSession: the session list moves a cursor
// without switching, so e there must rename what is under it.
func TestRenameActsOnTheHighlightedSession(t *testing.T) {
	m := newTestModel(t)
	first := m.mgr.Active()
	first.Title = "first"

	m.mgr.New()
	m.onSessionSwitch()
	second := m.mgr.Active()
	second.Title = "second"

	// Move the cursor in the list to the other session without selecting it.
	m.setFocus(focusSessions)
	m.sessSel = 1
	m.setSessionName("renamed from the list")

	all := m.mgr.All()
	if all[1].Title != "renamed from the list" {
		t.Errorf("the highlighted session is named %q", all[1].Title)
	}
	if all[0].Title == "renamed from the list" {
		t.Error("the active session was renamed instead")
	}
}

func TestSessionsKeyERenames(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusSessions)
	m.sessionsKey("e")

	if m.overlay != overlayRename {
		t.Errorf("overlay = %v, want the rename editor", m.overlay)
	}
}

// TestSessionListShowsWhatDistinguishesSessions is the other half of the ask:
// a pane that only shows a truncated title cannot tell two of them apart.
func TestSessionListShowsWhatDistinguishesSessions(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Title = "why does the ranking put deeply nested paths first"
	s.Append(session.Message{Role: session.RoleUser, Text: "a question"})
	s.Append(session.Message{Role: session.RoleAssistant, Text: "an answer"})

	out := stripANSI(m.sessionsPane())

	// The title wraps rather than being cut at the pane's width.
	if !strings.Contains(out, "why does the ranking") {
		t.Errorf("the start of the title is missing:\n%s", out)
	}
	if !strings.Contains(out, "first") && !strings.Contains(out, "…") {
		t.Errorf("the title neither wrapped nor said it was cut:\n%s", out)
	}
	// And the detail line says which engine and how far along it is.
	if !strings.Contains(out, "1↵") {
		t.Errorf("the turn count is missing:\n%s", out)
	}
}

func TestWrapTitle(t *testing.T) {
	got := wrapTitle("one two three four five six", 10, 2)
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(got), got)
	}
	for _, l := range got {
		// Measured by display width, not bytes: the ellipsis is one column and
		// three bytes.
		if lipgloss.Width(l) > 10 {
			t.Errorf("line %q is %d columns, wider than asked", l, lipgloss.Width(l))
		}
	}
	if !strings.HasSuffix(got[1], "…") {
		t.Errorf("the cut was not marked: %q", got[1])
	}

	// A short title stays on one line and gains nothing.
	if got := wrapTitle("short", 20, 2); len(got) != 1 || got[0] != "short" {
		t.Errorf("wrapTitle(short) = %q", got)
	}
	if got := wrapTitle("", 20, 2); len(got) != 1 || got[0] != "" {
		t.Errorf("wrapTitle(empty) = %q", got)
	}
}

func TestTurnCountIgnoresShellRuns(t *testing.T) {
	s := &session.Session{}
	s.Append(session.Message{Role: session.RoleUser, Text: "a real question"})
	s.Append(session.Message{Role: session.RoleAssistant, Text: "an answer"})
	s.Append(session.Message{
		Role: session.RoleUser, Text: "ls",
		Shell: &session.ShellRun{Command: "ls"},
	})
	if got := turnCount(s); got != 1 {
		t.Errorf("turnCount = %d, want 1", got)
	}
}

// TestClicksLandOnTheRowThatWasDrawn is the invariant a variable row height
// puts at risk: the renderer and the mouse handler must agree.
func TestClicksLandOnTheRowThatWasDrawn(t *testing.T) {
	m := newTestModel(t)
	m.mgr.Active().Title = "a title long enough that it has to wrap onto a second line"
	m.mgr.New()
	m.onSessionSwitch()
	m.mgr.Active().Title = "short"

	lines := m.sessionLines(max(4, m.sideW-2))
	if len(lines) < 4 {
		t.Fatalf("only %d rows for two sessions", len(lines))
	}
	top := headerRows + 1
	for i, l := range lines {
		if got := m.sessionRowAt(top + i); got != l.idx {
			t.Errorf("row %d was drawn for session %d but clicks as %d", i, l.idx, got)
		}
	}
	// Past the end is nothing, not the last session.
	if got := m.sessionRowAt(top + len(lines)); got != -1 {
		t.Errorf("a click below the list hit session %d", got)
	}
	if got := m.sessionRowAt(0); got != -1 {
		t.Errorf("a click on the border hit session %d", got)
	}
}
