package ui

import (
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

// The screen is laid out as one header row, then bodyH rows of panes, then the
// five-row input box and one status row. Everything the mouse needs to know is
// derived from that and from the widths computed in resize, so there is no
// second copy of the layout to fall out of step.
const (
	headerRows = 1
	inputRows  = 5
	statusRows = 1
)

// leftSplit is how the left column divides between the session list and the
// file tree. Both the renderer and the mouse handler call it, so a click always
// lands on the row that was drawn.
func (m *Model) leftSplit() (sessH, treeH int) {
	sessH = clamp(m.mgr.Len()*2+2, 4, m.bodyH/2)
	return sessH, m.bodyH - sessH
}

// inInputBox reports whether a screen cell is inside the prompt.
func (m *Model) inInputBox(x, y int) bool {
	top := headerRows + m.bodyH
	return y >= top && y < top+inputRows && x >= 0 && x < m.w
}

// paneAt reports which pane covers a screen cell, or -1 for the chrome.
func (m *Model) paneAt(x, y int) focus {
	if m.inInputBox(x, y) {
		return focusInput
	}
	if y < headerRows || y >= headerRows+m.bodyH || x < 0 || x >= m.w {
		return -1
	}
	if m.sideW > 0 && x < m.sideW {
		sessH, _ := m.leftSplit()
		if y < headerRows+sessH {
			return focusSessions
		}
		return focusExplorer
	}
	if x < m.sideW+m.chatW {
		return focusChat
	}
	if m.prevW > 0 {
		return focusPreview
	}
	return focusChat
}

// treeRowAt maps a screen row onto an index in the visible file tree, or -1
// when the point is on the pane's border.
func (m *Model) treeRowAt(y int) int {
	sessH, treeH := m.leftSplit()
	top := headerRows + sessH + 1 // past the explorer's top border
	if y < top || y >= top+treeH-2 {
		return -1
	}
	idx := m.treeTop + (y - top)
	if idx < 0 || idx >= len(m.tree.Rows()) {
		return -1
	}
	return idx
}

// sessionRowAt maps a screen row onto a session. Each session occupies two
// rows: its title and the engine it runs on.
func (m *Model) sessionRowAt(y int) int {
	top := headerRows + 1 // past the sessions box's top border
	if y < top {
		return -1
	}
	idx := (y - top) / 2
	if idx >= m.mgr.Len() {
		return -1
	}
	return idx
}

// onMouse routes a mouse event to whatever is under the pointer.
func (m *Model) onMouse(msg tea.MouseMsg) tea.Cmd {
	if !m.ready {
		return nil
	}
	e := msg.Mouse()

	switch msg.(type) {
	case tea.MouseWheelMsg:
		return m.onWheel(e)
	case tea.MouseClickMsg:
		if e.Button != tea.MouseLeft {
			return nil
		}
		return m.onClick(e)
	}
	return nil
}

// onWheel scrolls the pane the pointer is over, rather than every pane at once.
func (m *Model) onWheel(e tea.Mouse) tea.Cmd {
	const step = 3
	dir := 0
	switch e.Button {
	case tea.MouseWheelUp:
		dir = -step
	case tea.MouseWheelDown:
		dir = step
	default:
		return nil
	}

	scroll := func(vp *viewport.Model) {
		if dir > 0 {
			vp.ScrollDown(dir)
		} else {
			vp.ScrollUp(-dir)
		}
	}

	switch m.paneAt(e.X, e.Y) {
	case focusChat:
		scroll(&m.chat)
	case focusPreview:
		scroll(&m.prev)
		m.fileLine = m.prev.YOffset() + 1
	case focusExplorer:
		m.treeTop = clamp(m.treeTop+dir, 0, max(0, len(m.tree.Rows())-1))
	}
	return nil
}

// onClick focuses the pane under the pointer and acts on what was clicked.
func (m *Model) onClick(e tea.Mouse) tea.Cmd {
	pane := m.paneAt(e.X, e.Y)
	if pane < 0 {
		return nil
	}
	if m.overlay != overlayNone {
		return nil // a modal owns the screen
	}
	m.closeCompletion()

	switch pane {
	case focusExplorer:
		m.setFocus(focusExplorer)
		row := m.treeRowAt(e.Y)
		if row < 0 {
			return nil
		}
		m.treeSel = row
		node := m.tree.Rows()[row]
		switch {
		case node.IsParent():
			return m.goUp()
		case node.Dir:
			m.tree.Toggle(node.Rel)
			return nil
		default:
			// Clicking shows the file but leaves the caret in the tree, so the
			// next arrow key keeps browsing.
			return m.previewSelected()
		}

	case focusSessions:
		m.setFocus(focusSessions)
		if idx := m.sessionRowAt(e.Y); idx >= 0 {
			m.sessSel = idx
			m.mgr.Select(idx)
			m.onSessionSwitch()
		}
		return nil

	case focusInput:
		// Clicking the prompt returns the keyboard to it, which is the way
		// back from any pane without remembering a shortcut.
		m.setFocus(focusInput)
		return nil

	case focusChat, focusPreview:
		m.setFocus(pane)
		return nil
	}
	return nil
}
