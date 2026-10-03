package ui

import (
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/session"
)

// The screen is laid out as one header row, then bodyH rows of panes, then the
// five-row input box and one status row. Everything the mouse needs to know is
// derived from that and from the widths computed in resize, so there is no
// second copy of the layout to fall out of step.
const (
	headerRows = 1
	// The prompt: a rule on top, the textarea, and no rule under it — the
	// status line is the end of the screen.
	inputRows  = 4
	statusRows = 1
)

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
	if left := m.colX(colSide); m.sideW > 0 && x >= left && x < left+m.sideW {
		return m.sidebarPaneAt(y)
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
	if m.treeFold {
		return -1
	}
	_, treeH := m.leftSplit()
	top := m.treeTopY() + 1 // past the explorer's top border
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
	top := m.sessTopY() + 1 // past the sessions box's top border
	if y < top {
		return -1
	}
	// Ask the same layout the pane drew, because rows are no longer a fixed
	// height: a click on the second line of a wrapped title belongs to that
	// session, not to the next one.
	lines := m.sessionLines(max(4, m.sideW-2))
	// Past the scroll offset, because the list no longer starts at its first
	// row: the renderer records where the window began and the hit test reads
	// it, rather than each counting for itself.
	row := y - top + m.sessTop
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
			m.dragTo(e.X, e.Y)
			return nil
		}
		if m.grabbing {
			m.carry(e.X, e.Y)
			return nil
		}
		// A button held down is a drag, and over a pane a drag is a selection.
		if e.Button == tea.MouseLeft && m.inputDrag {
			m.extendInputSelect(e)
			return nil
		}
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
		// Over the transcript, a link under the pointer is underlined and its
		// target named in the status line.
		if m.overlay == overlayNone {
			m.hoverAt(e.X, e.Y)
			m.hoverSession(e.X, e.Y)
		}
		return nil
	case tea.MouseReleaseMsg:
		m.drag = dragNone
		if m.grabbing {
			m.drop(e.X, e.Y)
			return nil
		}
		if m.inputDrag {
			m.inputDrag = false
			return nil // the prompt keeps its selection; ctrl+c takes it
		}
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
		m.overlayWheel(dir)
		return nil
	}

	switch m.paneAt(e.X, e.Y) {
	case focusChat:
		scroll(&m.chat)
		m.noteChatScroll()
	case focusPreview:
		if m.showingChanges() {
			m.scrollChanges(dir)
			return nil
		}
		scroll(&m.prev)
		m.fileLine = m.prev.YOffset() + 1
	case focusExplorer:
		m.treeTop = clamp(m.treeTop+dir, 0, max(0, len(m.tree.Rows())-1))
	}
	return nil
}

// onClick focuses the pane under the pointer and acts on what was clicked.
func (m *Model) onClick(e tea.Mouse) tea.Cmd {
	switch m.overlay {
	case overlayGit:
		return m.gitClick(e.X, e.Y)
	case overlayGrep:
		return m.grepClick(e.X, e.Y)
	case overlayRecall:
		return m.recallClick(e.X, e.Y)
	case overlaySettings:
		return m.settingsClick(e.X, e.Y)
	case overlayNone:
	default:
		return nil // a picker or a prompt, with nothing to aim at
	}
	// A switch in the header opens or closes a pane.
	if pane, ok := m.switchAt(e.X, e.Y); ok {
		switch pane {
		case focusSessions:
			m.toggleSessions()
		case focusExplorer:
			m.switchFiles()
		case settingsSwitch:
			m.openSettings()
		default:
			m.togglePreview()
		}
		return nil
	}
	// A press on a pane's title picks the pane up. Where it is let go decides
	// whether it moved, and a press that never moved is a click on the title.
	if pane, ok := m.titleAt(e.X, e.Y); ok {
		m.grabbing = true
		m.grabbed = grab{pane: pane, x: e.X, y: e.Y}
		return nil
	}
	// A press on a divider takes hold of it until the button comes back up.
	// It is checked before the panes, because the column it is in belongs to
	// one of them and focusing that pane is not what a drag is for.
	if d := m.dividerAt(e.X, e.Y); d != dragNone {
		m.drag = d
		m.dragTo(e.X, e.Y)
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
		// The ✕ on the row under the pointer deletes that conversation, the
		// way the d key does — undoably, and asking once if it is running.
		if idx, ok := m.sessionDeleteAt(e.X, e.Y); ok {
			if all := m.mgr.All(); idx < len(all) {
				m.sessSel = idx
				return m.deleteSessions([]*session.Session{all[idx]})
			}
		}
		if idx := m.sessionRowAt(e.Y); idx >= 0 {
			m.sessSel = idx
			m.mgr.Select(idx)
			return m.onSessionSwitch()
		}
		return nil

	case focusInput:
		// Clicking the prompt returns the keyboard to it, which is the way
		// back from any pane without remembering a shortcut — and puts the
		// caret where the click was, rather than wherever it happened to be.
		m.setFocus(focusInput)
		m.beginInputSelect(e)
		return nil

	case focusChat, focusBtw, focusPreview:
		// A click in one of the other cells is the whole of what a split
		// needs for a gesture: it says which conversation you are talking to
		// now. Selecting starts in the cell that answers the prompt.
		// Ctrl turns a click on a link or a path into opening it.
		if pane == focusChat && e.Mod&tea.ModCtrl != 0 {
			if cmd, ok := m.openLinkAt(e.X, e.Y); ok {
				return cmd
			}
		}
		if pane == focusChat {
			if s := m.splitAt(e.X, e.Y); s != nil && s != m.mgr.Active() {
				m.setFocus(focusChat)
				m.mgr.Select(m.indexOf(s))
				return m.onSessionSwitch()
			}
		}
		m.setFocus(pane)
		// A click on a file in the changes listing picks it.
		if pane == focusPreview && m.showingChanges() && m.changesClick(e.Y) {
			return m.loadChangesPatch(false)
		}
		// A press in a pane of text is the start of a selection. Focusing and
		// selecting are not alternatives: you click into a pane to read it,
		// and the drag that would have selected in the terminal arrives here.
		m.beginSelect(e)
		return nil
	}
	return nil
}
