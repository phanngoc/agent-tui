package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func pressAt(m *Model, x, y int) { m.onMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}) }
func moveTo(m *Model, x, y int) {
	m.onMouse(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft})
}
func release(m *Model, x, y int) { m.onMouse(tea.MouseReleaseMsg{X: x, Y: y}) }

// dragFrom presses, moves in a few steps and lets go, the way a hand does.
func dragFrom(m *Model, x0, y0, x1, y1 int) {
	pressAt(m, x0, y0)
	for i := 1; i <= 4; i++ {
		moveTo(m, x0+(x1-x0)*i/4, y0+(y1-y0)*i/4)
	}
	release(m, x1, y1)
}

// screenIsWhole checks the frame fills the terminal exactly, row for row: a
// column in the wrong place shows up as a line that is too long or too short.
func screenIsWhole(t *testing.T, m *Model, what string) {
	t.Helper()
	out := m.View().Content
	lines := strings.Split(out, "\n")
	if len(lines) != m.h {
		t.Errorf("%s: %d rows, want %d", what, len(lines), m.h)
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w != m.w {
			t.Errorf("%s: row %d is %d wide, want %d", what, i, w, m.w)
			return
		}
	}
}

// TestFilesTakeLessRoomByDefault: the tree is reached for, the session list
// is read. It used to be the other way round.
func TestFilesTakeLessRoomByDefault(t *testing.T) {
	m := newTestModel(t)
	sessH, treeH := m.leftSplit()
	if treeH >= sessH {
		t.Errorf("files %d rows, sessions %d: the tree should be the smaller", treeH, sessH)
	}
	if treeH < treeMin {
		t.Errorf("files %d rows, below the %d it needs", treeH, treeMin)
	}
	if sessH+treeH != m.bodyH {
		t.Errorf("the column does not add up: %d+%d != %d", sessH, treeH, m.bodyH)
	}
}

func TestClickingTheFilesTitleFoldsIt(t *testing.T) {
	m := newTestModel(t)
	y := m.treeTopY()
	pressAt(m, 3, y)
	release(m, 3, y)
	if !m.treeFold {
		t.Fatal("clicking the title did not fold the tree")
	}
	if _, treeH := m.leftSplit(); treeH != 1 {
		t.Errorf("a folded tree is %d rows, want its one title line", treeH)
	}
	out := stripANSI(m.View().Content)
	if !strings.Contains(out, "▸ ") {
		t.Errorf("the folded bar does not say it can open:\n%s", out)
	}
	screenIsWhole(t, m, "folded")
	if row := m.treeRowAt(m.treeTopY()); row != -1 {
		t.Error("a folded tree still answers clicks on rows it does not draw")
	}

	y = m.treeTopY()
	pressAt(m, 3, y)
	release(m, 3, y)
	if m.treeFold {
		t.Error("clicking the folded title did not open it")
	}
	if !loadLayout().TreeFold == m.treeFold {
		t.Error("the fold was not remembered")
	}
}

func TestDraggingTheRuleResizesTheFiles(t *testing.T) {
	m := newTestModel(t)
	_, before := m.leftSplit()
	rule := m.splitRuleY()
	x := m.sideW - 4 // on the rule, past its title
	if m.dividerAt(x, rule) != dragSplit {
		t.Fatalf("the rule at row %d is not a divider", rule)
	}
	dragFrom(m, x, rule, x, rule-5)
	if _, after := m.leftSplit(); after != before+5 {
		t.Errorf("files %d rows after dragging up 5, want %d", after, before+5)
	}
	if got := loadLayout().Tree; got != before+5 {
		t.Errorf("saved %d rows, want %d", got, before+5)
	}
	screenIsWhole(t, m, "resized")
}

// TestDraggingAFoldedTreeUpOpensIt: the line a pane folded into is the line
// it opens out of.
func TestDraggingAFoldedTreeUpOpensIt(t *testing.T) {
	m := newTestModel(t)
	m.toggleTreeFold()
	rule := m.splitRuleY()
	dragFrom(m, m.sideW-4, rule, m.sideW-4, rule-10)
	if m.treeFold {
		t.Fatal("dragging the folded line up left it folded")
	}
	if _, treeH := m.leftSplit(); treeH != 11 {
		t.Errorf("files %d rows, want 11", treeH)
	}
}

func TestDraggingTheSidebarToTheRight(t *testing.T) {
	m := newTestModel(t)
	dragFrom(m, 3, headerRows, m.w-5, headerRows+3)
	if !m.dock.SideRight {
		t.Fatalf("the sidebar did not move; notice %q", m.notice)
	}
	if m.colX(colSide) != m.w-m.sideW {
		t.Errorf("the sidebar starts at %d, want the right edge %d", m.colX(colSide), m.w-m.sideW)
	}
	if got := m.paneAt(m.w-3, headerRows+2); got != focusSessions {
		t.Errorf("the right edge is %v, want the sessions", got)
	}
	if got := m.paneAt(3, headerRows+3); got != focusChat {
		t.Errorf("the left edge is %v, want the transcript", got)
	}
	screenIsWhole(t, m, "sidebar right")
	if !loadLayout().Dock.SideRight {
		t.Error("the move was not remembered")
	}

	// And back, by its title where it is now.
	dragFrom(m, m.colX(colSide)+3, headerRows, 2, headerRows+3)
	if m.dock.SideRight {
		t.Error("dragging it back left did not move it")
	}
}

func TestDraggingThePreviewLeftOfTheTranscript(t *testing.T) {
	m := newTestModel(t)
	if m.prevW == 0 {
		t.Skip("no preview at this size")
	}
	dragFrom(m, m.colX(colAux)+3, headerRows, m.colX(colChat)+2, headerRows+5)
	if !m.dock.AuxLeft {
		t.Fatalf("the preview did not move; notice %q", m.notice)
	}
	if m.colX(colAux) != m.sideW || m.colX(colChat) != m.sideW+m.prevW {
		t.Errorf("columns at side=%d aux=%d chat=%d", m.colX(colSide), m.colX(colAux), m.colX(colChat))
	}
	if got := m.paneAt(m.colX(colAux)+5, headerRows+3); got != focusPreview {
		t.Errorf("the preview's new place is %v", got)
	}
	screenIsWhole(t, m, "preview left")
}

func TestDraggingFilesAboveSessions(t *testing.T) {
	m := newTestModel(t)
	dragFrom(m, 3, m.treeTopY(), 3, headerRows+2)
	if !m.dock.TreeTop {
		t.Fatalf("the files did not move up; notice %q", m.notice)
	}
	if m.treeTopY() != headerRows {
		t.Errorf("files start on row %d, want the top", m.treeTopY())
	}
	if got := m.paneAt(3, headerRows+2); got != focusExplorer {
		t.Errorf("the top of the sidebar is %v, want the files", got)
	}
	if row := m.treeRowAt(headerRows + 1); row != m.treeTop {
		t.Errorf("the first tree row maps to %d, want %d", row, m.treeTop)
	}
	screenIsWhole(t, m, "files on top")
}

// TestCarryingSaysWhereItWillLand: while a pane is held, the status line is
// the drop preview.
func TestCarryingSaysWhereItWillLand(t *testing.T) {
	m := newTestModel(t)
	pressAt(m, 3, headerRows)
	moveTo(m, m.w-6, headerRows+4)
	if !strings.Contains(m.notice, "release: sidebar to the right edge") {
		t.Errorf("notice while carrying = %q", m.notice)
	}
	release(m, 4, headerRows) // let go where it started
	if m.dock.SideRight {
		t.Error("dropping it back where it was moved it anyway")
	}
}

// TestEveryArrangementDrawsWhole goes through every combination of the dock
// and the fold and checks the frame and the hit-testing agree.
func TestEveryArrangementDrawsWhole(t *testing.T) {
	m := newTestModel(t)
	for _, d := range []dock{
		{}, {SideRight: true}, {AuxLeft: true}, {TreeTop: true},
		{SideRight: true, AuxLeft: true}, {SideRight: true, TreeTop: true},
		{AuxLeft: true, TreeTop: true}, {SideRight: true, AuxLeft: true, TreeTop: true},
	} {
		for _, fold := range []bool{false, true} {
			m.dock, m.treeFold = d, fold
			m.applyDock()
			name := m.layoutSummary()
			screenIsWhole(t, m, name)
			for _, f := range []focus{focusChat, focusPreview} {
				left, top, w, h, ok := m.paneBox(f)
				if !ok {
					continue
				}
				if got := m.paneAt(left+w/2, top+h/2); got != f {
					t.Errorf("%s: the middle of %v is reported as %v", name, f, got)
				}
			}
			mid := m.colX(colSide) + m.sideW/2
			if got := m.paneAt(mid, m.sessTopY()+1); got != focusSessions {
				t.Errorf("%s: the session list's first row is %v", name, got)
			}
		}
	}
}

func TestLayoutCommand(t *testing.T) {
	m := newTestModel(t)
	m.runSlash("layout", "sidebar right")
	m.runSlash("layout", "preview left")
	m.runSlash("layout", "files top")
	if m.dock != (dock{SideRight: true, AuxLeft: true, TreeTop: true}) {
		t.Errorf("dock = %+v", m.dock)
	}
	m.runSlash("layout", "files 9")
	if _, treeH := m.leftSplit(); treeH != 9 {
		t.Errorf("files %d rows, want 9", treeH)
	}
	m.runSlash("layout", "files fold")
	if !m.treeFold {
		t.Error("/layout files fold did not fold")
	}
	m.runSlash("layout", "")
	if !strings.Contains(m.notice, "sidebar right") || !strings.Contains(m.notice, "folded") {
		t.Errorf("summary = %q", m.notice)
	}
	m.runSlash("layout", "reset")
	if m.dock != (dock{}) || m.treeFold || m.treeSet != 0 {
		t.Errorf("reset left dock=%+v fold=%v rows=%d", m.dock, m.treeFold, m.treeSet)
	}
	m.runSlash("layout", "sideways")
	if !strings.Contains(m.notice, "layout: say") {
		t.Errorf("an unknown argument should explain itself: %q", m.notice)
	}
}

// TestFoldedTreeLeavesTheFocusCycle: there is nothing in one line to tab to.
func TestFoldedTreeLeavesTheFocusCycle(t *testing.T) {
	m := newTestModel(t)
	m.toggleTreeFold()
	m.setFocus(focusInput)
	for i := 0; i < 8; i++ {
		m.cycleFocus(1)
		if m.focus == focusExplorer {
			t.Fatal("tab reached a folded tree")
		}
	}
}

func TestFilesSwitchInTheHeader(t *testing.T) {
	m := newTestModel(t)
	m.View()
	sw, ok := m.switchFor(focusExplorer)
	if !ok {
		t.Fatal("no files switch in the header")
	}
	clickAt(m, sw.x0+1, 0)
	if !m.treeFold {
		t.Error("the switch did not fold the files")
	}
}
