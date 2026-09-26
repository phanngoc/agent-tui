package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// press, move and release are one drag, which is three events and not one.
func drag(m *Model, from, to, y int) {
	m.onMouse(tea.MouseClickMsg{X: from, Y: y, Button: tea.MouseLeft})
	m.onMouse(tea.MouseMotionMsg{X: to, Y: y, Button: tea.MouseLeft})
	m.onMouse(tea.MouseReleaseMsg{X: to, Y: y, Button: tea.MouseLeft})
}

// The divider between two panes is a thing you can take hold of. The widths
// were two formulas before this, and a formula is a reasonable guess about a
// layout and wrong about the one you are working in.
func TestDraggingADividerResizesThePane(t *testing.T) {
	m := newTestModel(t)
	before := m.sideW
	if before == 0 {
		t.Skip("the test terminal is too narrow for a sidebar")
	}

	// The rule is the sidebar's own right border — panes share one line — so
	// the column to take hold of is that one, not the one after it.
	drag(m, before-1, before+9, headerRows+2)

	if m.sideW != before+10 {
		t.Errorf("the sidebar is %d columns, want %d", m.sideW, before+10)
	}
	// The transcript gives up exactly what the sidebar took: the row is still
	// the width of the terminal.
	if got := m.sideW + m.chatW + m.prevW; got != m.w {
		t.Errorf("the panes add up to %d, want %d", got, m.w)
	}
}

// The preview's divider is its left edge, so dragging it left makes it wider.
func TestDraggingThePreviewDivider(t *testing.T) {
	m := newTestModel(t)
	if m.prevW == 0 {
		t.Skip("the test terminal is too narrow for a preview")
	}
	edge := m.w - m.prevW

	drag(m, edge-1, edge-13, headerRows+2)

	if m.prevW <= 0 || m.w-m.prevW != edge-12 {
		t.Errorf("the preview's edge is at %d, want %d", m.w-m.prevW, edge-12)
	}
}

// A pane cannot be dragged out of existence, and cannot take the transcript
// with it: the conversation is what the window is for.
func TestDraggingIsClamped(t *testing.T) {
	m := newTestModel(t)
	if m.sideW == 0 {
		t.Skip("no sidebar to drag")
	}

	drag(m, m.sideW, 0, headerRows+2)
	if m.sideW < sideMin {
		t.Errorf("the sidebar shrank to %d, below the %d minimum", m.sideW, sideMin)
	}

	drag(m, m.sideW, m.w-1, headerRows+2)
	if m.chatW < paneRoom {
		t.Errorf("the transcript was squeezed to %d, below the %d it keeps", m.chatW, paneRoom)
	}
	if got := m.sideW + m.chatW + m.prevW; got != m.w {
		t.Errorf("the panes add up to %d, want %d", got, m.w)
	}
}

// Taking hold of a divider is not clicking the pane it is drawn in.
func TestPressingADividerDoesNotMoveTheFocus(t *testing.T) {
	m := newTestModel(t)
	if m.sideW == 0 {
		t.Skip("no sidebar to drag")
	}
	m.setFocus(focusInput)

	m.onMouse(tea.MouseClickMsg{X: m.sideW - 1, Y: headerRows + 2, Button: tea.MouseLeft})

	if m.focus != focusInput {
		t.Errorf("pressing the divider moved the focus to %v", m.focus)
	}
	if m.drag != dragSide {
		t.Error("pressing the divider did not take hold of it")
	}
	m.onMouse(tea.MouseReleaseMsg{X: m.sideW - 1, Y: headerRows + 2})
	if m.drag != dragNone {
		t.Error("the divider is still held after the button came up")
	}
}

// The keyboard moves the same edges, because everything else in this app has a
// key and a terminal over ssh may have no mouse at all.
func TestArrowsResizeTheFocusedPane(t *testing.T) {
	m := newTestModel(t)
	if m.sideW == 0 || m.prevW == 0 {
		t.Skip("the test terminal is too narrow")
	}

	m.setFocus(focusExplorer)
	side := m.sideW
	m.onKey(key("alt+right"))
	if m.sideW <= side {
		t.Errorf("alt+right left the sidebar at %d", m.sideW)
	}
	m.onKey(key("alt+left"))
	if m.sideW != side {
		t.Errorf("alt+left did not undo alt+right: %d, want %d", m.sideW, side)
	}

	// From the transcript the edge in play is the preview's. The arrow pushes
	// it in the arrow's direction, so right hands the room to the transcript.
	m.setFocus(focusChat)
	chat, prev := m.chatW, m.prevW
	m.onKey(key("alt+right"))
	if m.chatW <= chat || m.prevW >= prev {
		t.Errorf("alt+right gave the transcript %d and the preview %d, want %d and less than %d",
			m.chatW, m.prevW, chat, prev)
	}
	m.onKey(key("alt+left"))
	if m.chatW != chat {
		t.Errorf("alt+left did not undo alt+right: %d, want %d", m.chatW, chat)
	}
}

// The switches are in the header because a closed pane has no title bar to
// click: an × that can only close is half a switch.
func TestHeaderSwitchesOpenAndClosePanes(t *testing.T) {
	m := newTestModel(t)
	m.header() // the switches are laid out as they are drawn

	sw, ok := m.switchFor(focusPreview)
	if !ok {
		t.Fatal("no preview switch was drawn")
	}
	m.onMouse(tea.MouseClickMsg{X: sw.x0 + 1, Y: 0, Button: tea.MouseLeft})
	if m.showPreview {
		t.Error("clicking the switch did not close the preview")
	}
	if m.prevW != 0 {
		t.Errorf("the preview is closed and still %d columns wide", m.prevW)
	}

	m.header()
	sw, _ = m.switchFor(focusPreview)
	m.onMouse(tea.MouseClickMsg{X: sw.x0 + 1, Y: 0, Button: tea.MouseLeft})
	if !m.showPreview {
		t.Error("clicking the switch did not open the preview again")
	}
}

// The header says which panes are open, so the switches are readable before
// you click one.
func TestHeaderSaysWhichPanesAreOpen(t *testing.T) {
	m := newTestModel(t)
	out := stripANSI(m.header())
	for _, want := range []string{"sessions", "preview"} {
		if !strings.Contains(out, want) {
			t.Errorf("the header does not name the %s pane:\n%s", want, out)
		}
	}
	open := strings.Count(out, "▪")
	m.togglePreview()
	if got := strings.Count(stripANSI(m.header()), "▪"); got != open-1 {
		t.Errorf("closing a pane left %d switches lit, want %d", got, open-1)
	}
}

// A layout is a decision. Asking for it again every morning is not a default,
// it is an interruption.
func TestTheLayoutSurvivesARestart(t *testing.T) {
	m := newTestModel(t)
	if m.sideW == 0 {
		t.Skip("no sidebar to set")
	}
	m.setSideWidth(28)
	m.togglePreview()

	// This is what a fresh model reads at startup. XDG_DATA_HOME points at
	// this test's own directory, so it is this file and nobody else's.
	got := loadLayout()
	if got.Side != 28 {
		t.Errorf("the saved sidebar is %d columns, want 28", got.Side)
	}
	if !got.HidePrv {
		t.Error("the closed preview was not remembered")
	}

}
