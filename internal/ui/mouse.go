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
	// A title may wrap, so the height comes from the rows that will actually
	// be drawn rather than from a count of sessions.
	rows := len(m.sessionLines(max(4, m.sideW-2)))
	sessH = clamp(rows+2, 4, m.bodyH/2)
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
	// The three panes to the right of the sidebar are asked where they were
	// drawn rather than measured again here. They used to be measured here,
	// and the arithmetic did not know about the side chat: with one open,
	// every click in it landed in the preview.
	for _, f := range []focus{focusChat, focusBtw, focusPreview} {
		if left, _, w, _, ok := m.paneBox(f); ok && x >= left-1 && x < left+w+1 {
			return f
		}
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

// sessionRowAt maps a screen row onto a session. A session occupies as many
// rows as its title wraps onto, plus the line of detail underneath.
func (m *Model) sessionRowAt(y int) int {
	top := headerRows + 1 // past the sessions box's top border
	if y < top {
		return -1
	}
	// Ask the same layout the pane drew, because rows are no longer a fixed
	// height: a click on the second line of a wrapped title belongs to that
	// session, not to the next one.
	lines := m.sessionLines(max(4, m.sideW-2))
	row := y - top
	if row < 0 || row >= len(lines) {
		return -1
	}
	return lines[row].idx
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
	case tea.MouseMotionMsg:
		if m.drag != dragNone {
			m.dragTo(e.X)
			return nil
		}
		// A button held down is a drag, and over a pane a drag is a selection.
		if e.Button == tea.MouseLeft && m.sel.dragging {
			m.extendSelect(e)
			return nil
		}
		// Motion with no button held is the pointer passing over things. The
		// only thing that reacts is the history browser's file list, whose
		// rows are clickable and say nothing about it until they change.
		if m.overlay == overlayGit {
			m.hoverFile(e.X, e.Y)
		}
		return nil
	case tea.MouseReleaseMsg:
		m.drag = dragNone
		return m.endSelect()
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
	dir, across := 0, false
	switch {
	case e.Button == tea.MouseWheelUp:
		dir = -step
	case e.Button == tea.MouseWheelDown:
		dir = step
	// A trackpad swiped sideways, or a wheel tilted, or shift held over an
	// ordinary one — three spellings of the same gesture, and the preview is
	// the pane with something to the side to reach.
	case e.Button == tea.MouseWheelLeft:
		dir, across = -step, true
	case e.Button == tea.MouseWheelRight:
		dir, across = step, true
	default:
		return nil
	}
	if e.Mod&tea.ModShift != 0 {
		across = true
	}
	if across {
		if m.overlay == overlayNone && m.paneAt(e.X, e.Y) == focusPreview {
			m.prev.SetXOffset(m.prev.XOffset() + dir)
		}
		return nil
	}

	scroll := func(vp *viewport.Model) {
		if dir > 0 {
			vp.ScrollDown(dir)
		} else {
			vp.ScrollUp(-dir)
		}
	}

	// An overlay owns the screen, so the pane underneath it is not what the
	// pointer is over even though that is where the arithmetic would land.
	if m.overlay == overlayGit {
		m.gitWheel(e.X, e.Y, dir)
		return nil
	}
	if m.overlay != overlayNone {
		return nil
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
	if m.overlay == overlayGit {
		return m.gitClick(e.X, e.Y)
	}
	if m.overlay != overlayNone {
		return nil // a modal owns the screen
	}
	// A switch in the header opens or closes a pane.
	if pane, ok := m.switchAt(e.X, e.Y); ok {
		if pane == focusSessions {
			m.toggleSessions()
		} else {
			m.togglePreview()
		}
		return nil
	}
	// A press on a divider takes hold of it until the button comes back up.
	// It is checked before the panes, because the column it is in belongs to
	// one of them and focusing that pane is not what a drag is for.
	if d := m.dividerAt(e.X, e.Y); d != dragNone {
		m.drag = d
		m.dragTo(e.X)
		return nil
	}
	pane := m.paneAt(e.X, e.Y)
	if pane < 0 {
		return nil
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
			return m.onSessionSwitch()
		}
		return nil

	case focusInput:
		// Clicking the prompt returns the keyboard to it, which is the way
		// back from any pane without remembering a shortcut.
		m.setFocus(focusInput)
		return nil

	case focusChat, focusBtw, focusPreview:
		m.setFocus(pane)
		// A press in a pane of text is the start of a selection. Focusing and
		// selecting are not alternatives: you click into a pane to read it,
		// and the drag that would have selected in the terminal arrives here.
		m.beginSelect(e)
		return nil
	}
	return nil
}
