package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// promptWith puts the caret in the prompt with something in it, and says
// where its writable area begins.
func promptWith(t *testing.T, m *Model, text string) (left, top int) {
	t.Helper()
	m.setFocus(focusInput)
	m.input.SetValue(text)
	m.View()
	l, tp, _, _, ok := m.inputArea()
	if !ok {
		t.Fatalf("the prompt has no writable area at %dx%d", m.w, m.h)
	}
	return l, tp
}

// Select-all was bound to ctrl+g and this program had taken it for the file
// search before the prompt ever saw it, so the one gesture everybody tries
// first opened a search box instead.
func TestCtrlGSelectsThePrompt(t *testing.T) {
	m := newTestModel(t)
	const text = "so sánh session token và access token"
	promptWith(t, m, text)

	m.onKey(key("ctrl+g"))

	if m.overlay != overlayNone {
		t.Errorf("ctrl+g in the prompt opened overlay %v", m.overlay)
	}
	if got := m.input.SelectedText(); got != text {
		t.Errorf("it selected %q, want the whole prompt", got)
	}
}

// And it still opens the search from anywhere else, including from an empty
// prompt where there is nothing to select.
func TestCtrlGStillOpensTheSearch(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusInput)
	m.input.SetValue("")
	m.onKey(key("ctrl+g"))
	if m.overlay != overlayGrep {
		t.Errorf("ctrl+g on an empty prompt gave %v, want the search", m.overlay)
	}

	m2 := newTestModel(t)
	m2.setFocus(focusChat)
	m2.onKey(key("ctrl+g"))
	if m2.overlay != overlayGrep {
		t.Errorf("ctrl+g outside the prompt gave %v, want the search", m2.overlay)
	}
}

// A click puts the caret where it was clicked, rather than leaving it wherever
// it happened to be.
func TestClickingThePromptPlacesTheCaret(t *testing.T) {
	m := newTestModel(t)
	left, top := promptWith(t, m, "so sánh session token và access token")
	m.input.CursorEnd()

	m.onMouse(tea.MouseClickMsg{X: left + 9, Y: top, Button: tea.MouseLeft})

	if got := m.inputOffset(); got != 9 {
		t.Errorf("the caret went to %d, want 9", got)
	}
	if m.focus != focusInput {
		t.Errorf("clicking the prompt left the caret in %v", m.focus)
	}
}

// Dragging selects what it crossed. The textarea cannot be told where a
// selection begins, so this is the keyboard's own selection driven by the
// distance the pointer moved — which is the point: one selection, whichever
// way it was begun.
func TestDraggingThePromptSelects(t *testing.T) {
	m := newTestModel(t)
	const text = "so sánh session token và access token"
	left, top := promptWith(t, m, text)

	m.onMouse(tea.MouseClickMsg{X: left + 8, Y: top, Button: tea.MouseLeft})
	m.onMouse(tea.MouseMotionMsg{X: left + 15, Y: top, Button: tea.MouseLeft})

	if got := m.input.SelectedText(); got != "session" {
		t.Errorf("the drag selected %q, want session", got)
	}
	// Dragging back the other way shrinks it rather than starting again.
	m.onMouse(tea.MouseMotionMsg{X: left + 11, Y: top, Button: tea.MouseLeft})
	if got := m.input.SelectedText(); got != "ses" {
		t.Errorf("dragging back left %q, want ses", got)
	}

	m.onMouse(tea.MouseReleaseMsg{X: left + 11, Y: top, Button: tea.MouseLeft})
	if !m.input.HasSelection() {
		t.Error("letting go threw the selection away")
	}
}

// ctrl+c copies it, which is what ctrl+c already means in the transcript. A
// prompt where copy worked differently from everywhere else would be a second
// rule to learn for no reason.
func TestCtrlCCopiesThePromptSelection(t *testing.T) {
	m := newTestModel(t)
	promptWith(t, m, "so sánh session token và access token")
	m.onKey(key("ctrl+g"))

	cmd := m.onKey(key("ctrl+c"))

	if cmd == nil {
		t.Fatal("ctrl+c over a prompt selection copied nothing")
	}
	if !strings.Contains(m.notice, "copied") {
		t.Errorf("nothing said it was copied: %q", m.notice)
	}
	// And it is dropped on the way out, so the next press means what it
	// always did.
	if m.input.HasSelection() {
		t.Error("ctrl+c kept the selection, so pressing it again would copy it twice")
	}
}

// A click outside the writable area is not a click at its edge: the border and
// the prompt mark are chrome, and the caret should not jump because the
// pointer grazed them.
func TestClicksOutsideTheWritableAreaArePassedOver(t *testing.T) {
	m := newTestModel(t)
	left, top := promptWith(t, m, "một dòng")
	m.input.CursorStart()

	for _, p := range [][2]int{{left - 1, top}, {0, top}, {left, top - 1}} {
		if _, ok := m.inputAt(p[0], p[1]); ok {
			t.Errorf("%v is inside the writable area", p)
		}
	}
}

// The prompt wraps onto three rows, so an offset has to survive a newline
// rather than counting from the start of whichever row it lands on.
func TestAnOffsetCrossesTheRows(t *testing.T) {
	m := newTestModel(t)
	left, top := promptWith(t, m, "dòng một\ndòng hai")

	at, ok := m.inputAt(left+2, top+1)
	if !ok {
		t.Fatal("the second row is not inside the writable area")
	}
	// "dòng một" is 8 runes, the newline is one more, then two into the next.
	if want := 8 + 1 + 2; at != want {
		t.Errorf("the second row's third column is offset %d, want %d", at, want)
	}
}
