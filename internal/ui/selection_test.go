package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/phanngoc/agent-tui/internal/session"
)

// selecting drags from one cell to another in the transcript, the way the
// mouse would: a press, a frame so the pane records what it drew, motion, and
// a release.
func selecting(t *testing.T, m *Model, x1, y1, x2, y2 int) {
	t.Helper()
	m.View()
	m.onMouse(tea.MouseClickMsg{X: x1, Y: y1, Button: tea.MouseLeft})
	m.View()
	m.onMouse(tea.MouseMotionMsg{X: x2, Y: y2, Button: tea.MouseLeft})
	m.View()
	m.onMouse(tea.MouseReleaseMsg{X: x2, Y: y2, Button: tea.MouseLeft})
}

// chatTop is the first row inside the transcript's border, and chatLeft the
// first column: where the pane actually drew its content.
func chatBox(t *testing.T, m *Model) (left, top, w, h int) {
	t.Helper()
	left, top, w, h, ok := m.paneBox(focusChat)
	if !ok {
		t.Fatalf("the transcript has no box at %dx%d", m.w, m.h)
	}
	return left, top, w, h
}

// withReply puts something in the transcript worth copying out of it.
func withReply(m *Model, text string) {
	s := m.mgr.Active()
	s.Messages = append(s.Messages,
		session.Message{Role: session.RoleAssistant, Text: text})
	m.invalidateChat()
}

// Asking for the mouse takes the terminal's drag-to-select away, so a drag
// across the transcript has to select here instead.
func TestDraggingSelectsWhatItCrosses(t *testing.T) {
	m := newTestModel(t)
	withReply(m, "resolvePfid lives in onboarding.service.ts")
	left, top, _, _ := chatBox(t, m)

	m.View()
	m.onMouse(tea.MouseClickMsg{X: left, Y: top, Button: tea.MouseLeft})
	if !m.sel.on || m.sel.pane != focusChat {
		t.Fatalf("a press in the transcript started nothing: on=%v pane=%v", m.sel.on, m.sel.pane)
	}
	m.View()
	m.onMouse(tea.MouseMotionMsg{X: left + 12, Y: top + 1, Button: tea.MouseLeft})

	a, b, ok := m.sel.span()
	if !ok {
		t.Fatal("the drag selected nothing")
	}
	if a.row != 0 || b.row != 1 || b.col != 12 {
		t.Errorf("the selection runs %v→%v, want the first two rows out to column 12", a, b)
	}
}

// Releasing puts it on the clipboard. A selection in a terminal that is not on
// the clipboard is a highlight, not a selection.
func TestReleasingCopies(t *testing.T) {
	m := newTestModel(t)
	withReply(m, "resolvePfid lives in onboarding.service.ts")
	left, top, w, _ := chatBox(t, m)
	// The reply is not on the first row — a turn draws its own heading — so
	// the row it landed on is asked for rather than assumed.
	row, _ := findText(t, m, "resolvePfid")

	selecting(t, m, left, top+row, left+w-1, top+row)

	if !strings.Contains(m.notice, "copied") {
		t.Errorf("nothing said anything was copied: %q", m.notice)
	}
	if got := m.selectedText(); !strings.Contains(got, "resolvePfid") {
		t.Errorf("the selection reads %q, want the line that was dragged across", got)
	}
}

// What comes off the screen is text, not the escape sequences that drew it.
func TestTheCopiedTextIsPlain(t *testing.T) {
	m := newTestModel(t)
	withReply(m, "**resolvePfid** lives in `onboarding.service.ts`")
	left, top, w, _ := chatBox(t, m)

	selecting(t, m, left, top, left+w-1, top+2)

	got := m.selectedText()
	if got == "" {
		t.Fatal("the drag copied nothing")
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("the clipboard got escape sequences:\n%q", got)
	}
	if strings.HasSuffix(got, " ") {
		t.Errorf("the clipboard got the padding out to the pane's edge:\n%q", got)
	}
}

// A plain click is not a selection. Clicking into a pane to read it should not
// put a space on the clipboard and announce it.
func TestAPlainClickCopiesNothing(t *testing.T) {
	m := newTestModel(t)
	withReply(m, "resolvePfid lives in onboarding.service.ts")
	left, top, _, _ := chatBox(t, m)

	selecting(t, m, left, top, left, top)

	if m.sel.on {
		t.Error("a click left a selection behind")
	}
	if strings.Contains(m.notice, "copied") {
		t.Errorf("a click claimed to copy something: %q", m.notice)
	}
}

// A double-click takes the word under the pointer, because what gets copied
// out of a transcript is usually one: a path, an identifier, a hash.
func TestDoubleClickTakesTheWord(t *testing.T) {
	m := newTestModel(t)
	withReply(m, "see onboarding.service.ts for it")
	left, top, _, _ := chatBox(t, m)

	m.View()
	// Find the word on the row the reply landed on, so the test does not
	// depend on where the renderer put its rail.
	row, col := findText(t, m, "onboarding.service.ts")
	m.onMouse(tea.MouseClickMsg{X: left + col + 4, Y: top + row, Button: tea.MouseLeft})
	m.View()
	m.onMouse(tea.MouseReleaseMsg{X: left + col + 4, Y: top + row, Button: tea.MouseLeft})
	m.View()
	m.onMouse(tea.MouseClickMsg{X: left + col + 4, Y: top + row, Button: tea.MouseLeft})
	m.View()
	m.onMouse(tea.MouseReleaseMsg{X: left + col + 4, Y: top + row, Button: tea.MouseLeft})

	if got := m.selectedText(); got != "onboarding.service.ts" {
		t.Errorf("the double-click took %q, want the whole path", got)
	}
}

// findText locates a string among the rows the transcript drew, as a row and
// column on the screen.
func findText(t *testing.T, m *Model, want string) (row, col int) {
	t.Helper()
	m.View()
	for r, line := range strings.Split(m.chat.View(), "\n") {
		if i := strings.Index(ansi.Strip(line), want); i >= 0 {
			return r, i
		}
	}
	t.Fatalf("%q was not drawn in the transcript", want)
	return 0, 0
}

// The selection is drawn where it was made, over content that had already been
// rendered and cached. It must not change what the text says.
func TestThePaintedSelectionKeepsTheText(t *testing.T) {
	m := newTestModel(t)
	withReply(m, "resolvePfid lives in onboarding.service.ts")
	left, top, w, _ := chatBox(t, m)
	m.View()

	before := ansi.Strip(m.chat.View())
	m.onMouse(tea.MouseClickMsg{X: left, Y: top, Button: tea.MouseLeft})
	m.onMouse(tea.MouseMotionMsg{X: left + 10, Y: top + 1, Button: tea.MouseLeft})
	painted := m.paintSelection(m.chat.View(), focusChat, w)

	if got := ansi.Strip(painted); got != before {
		t.Errorf("painting the selection changed the text:\n%q\nwant\n%q", got, before)
	}
	if !strings.Contains(painted, "\x1b") {
		t.Error("the selection was not drawn at all")
	}
	for i, line := range strings.Split(painted, "\n") {
		if got := ansi.StringWidth(line); got > w {
			t.Errorf("row %d is %d columns wide in a pane of %d", i, got, w)
		}
	}
}

// Esc drops it. It is the most local thing on the screen, so it goes before
// the rungs that cost a turn.
func TestEscDropsTheSelectionFirst(t *testing.T) {
	m := newTestModel(t)
	withReply(m, "resolvePfid lives in onboarding.service.ts")
	left, top, w, _ := chatBox(t, m)
	m.mgr.Active().Busy = true

	selecting(t, m, left, top, left+w-1, top)
	m.onKey(key("esc"))

	if m.sel.on {
		t.Error("esc left the selection behind")
	}
	if !m.mgr.Active().Busy {
		t.Error("esc stopped the turn when there was a selection to drop first")
	}
}

// ctrl+c copies when there is something selected, which is what it means
// everywhere else. It is only safe in front of quitting because it drops the
// selection on the way out: the second press does what the first always did.
func TestCtrlCCopiesThenGoesBackToStopping(t *testing.T) {
	m := newTestModel(t)
	withReply(m, "resolvePfid lives in onboarding.service.ts")
	left, top, w, _ := chatBox(t, m)
	m.mgr.Active().Busy = true

	selecting(t, m, left, top, left+w-1, top)
	if cmd := m.onKey(key("ctrl+c")); cmd == nil {
		t.Error("ctrl+c over a selection copied nothing")
	}
	if m.sel.on {
		t.Error("ctrl+c kept the selection, so the next press would copy it again")
	}
	if !m.mgr.Active().Busy {
		t.Error("ctrl+c stopped the turn instead of copying")
	}

	m.onKey(key("ctrl+c"))
	if m.mgr.Active().Busy {
		t.Error("the second ctrl+c did not stop the turn")
	}
}

// A drag that runs off the bottom scrolls, or nothing longer than the window
// could be selected at all.
func TestDraggingPastTheEdgeScrolls(t *testing.T) {
	m := newTestModel(t)
	withReply(m, strings.Repeat("resolvePfid lives in onboarding.service.ts\n\n", 40))
	left, top, _, h := chatBox(t, m)
	m.chat.GotoTop()
	m.View()

	before := m.chat.YOffset()
	m.onMouse(tea.MouseClickMsg{X: left, Y: top, Button: tea.MouseLeft})
	for i := 0; i < 5; i++ {
		m.View()
		m.onMouse(tea.MouseMotionMsg{X: left + 5, Y: top + h + 2, Button: tea.MouseLeft})
	}
	if m.chat.YOffset() <= before {
		t.Errorf("the transcript stayed at row %d while the drag ran past its edge", m.chat.YOffset())
	}
	if lines := strings.Count(m.selectedText(), "\n") + 1; lines <= h {
		t.Errorf("the selection is %d rows, no more than the %d on screen", lines, h)
	}
}

// A click in the side chat is a click in the side chat. The panes to the right
// of the sidebar used to be measured in the mouse handler, and the arithmetic
// did not know the side chat was there.
func TestAClickLandsInTheSideChat(t *testing.T) {
	m := newTestModel(t)
	withHistory(m)
	m.resize(160, 30)
	m.openBtw("")
	m.resize(160, 30)
	if m.btwW == 0 {
		t.Skip("no room for the side chat at this width")
	}

	left, top, w, _, _ := m.paneBox(focusBtw)
	if got := m.paneAt(left+w/2, top+1); got != focusBtw {
		t.Errorf("a click in the middle of the side chat landed in pane %v", got)
	}
	// And the preview takes the same column back when the aside closes, so the
	// same cell has to point at it instead.
	m.closeBtw()
	m.resize(160, 30)
	left, top, w, _, _ = m.paneBox(focusPreview)
	if got := m.paneAt(left+w/2, top+1); got != focusPreview {
		t.Errorf("a click in the middle of the preview landed in pane %v", got)
	}
}

// A double-click on a Vietnamese word takes the word, not the first ASCII run
// in it. The transcript is often Vietnamese, and a word rule written for
// English would cut every accented letter out of it.
func TestWordsAreNotOnlyASCII(t *testing.T) {
	line := "  lỗi ở resolvePfid rồi"
	lo, hi, ok := wordSpan(line, 4)
	if !ok {
		t.Fatal("no word under the pointer")
	}
	if got := string([]rune(line)[lo:hi]); got != "lỗi" {
		t.Errorf("the word is %q, want lỗi", got)
	}
}

// The rails the transcript draws sit against text without being part of it.
func TestARailIsNotPartOfTheWord(t *testing.T) {
	line := "│ resolvePfid"
	lo, hi, ok := wordSpan(line, 4)
	if !ok {
		t.Fatal("no word under the pointer")
	}
	if got := string([]rune(line)[lo:hi]); got != "resolvePfid" {
		t.Errorf("the word is %q, want resolvePfid", got)
	}
}
