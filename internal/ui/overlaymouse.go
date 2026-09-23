package ui

import (
	tea "charm.land/bubbletea/v2"
)

// Clicking a result.
//
// The two search overlays list things you are meant to pick one of, and until
// now the only way to pick was to walk the cursor down to it. The mouse went
// nowhere: onClick handed every overlay but the history browser back an
// untouched nil, so a click on a line that plainly looked clickable did
// nothing at all — which reads as the program being broken rather than as the
// feature being absent.
//
// A click does what enter does, for the same row. Not a lesser version of it:
// picking a result is one decision whichever way you make it, and a mouse that
// only moved the cursor would leave you reaching for the keyboard to finish a
// gesture you had already finished.
//
// The row a click lands on is worked out from where the view said it drew,
// never from counting the lines it should have drawn. That is the arrangement
// the history browser already uses, and it exists because the other way was
// wrong twice: a title that gained a line, and a view that grew a blank one,
// each time moving every row by one and sending every click to its neighbour.

// overlayRow maps a screen cell onto a row of a list drawn inside an overlay.
//
// bodyY is the line within the overlay's content that the list started on, and
// top is the number of rows it drew; both are recorded by the view as it went.
func (m *Model) overlayRow(x, y, bodyY, drawn, scroll int) (int, bool) {
	top := m.overlayY + 1 + bodyY
	if y < top || y >= top+drawn {
		return 0, false
	}
	w := m.overlayWidth()
	if x < m.overlayX+1 || x >= m.overlayX+w-1 {
		return 0, false
	}
	return scroll + y - top, true
}

// grepClick picks the file-content result under the pointer.
func (m *Model) grepClick(x, y int) tea.Cmd {
	row, ok := m.overlayRow(x, y, m.grepBodyY, m.grepDrawn, m.grepTop)
	if !ok || row >= len(m.grepRows) {
		return nil
	}
	m.grepSel = row
	// A header is a fold, and clicking one can mean nothing else. Both cases
	// go through the same call enter does, which is the point: one row, one
	// decision, however it was made.
	return m.openSelectedHit()
}

// recallClick picks the conversation result under the pointer.
func (m *Model) recallClick(x, y int) tea.Cmd {
	row, ok := m.overlayRow(x, y, m.recallBodyY, m.recallDrawn, m.recallTop)
	if !ok || row >= len(m.recallRows) {
		return nil
	}
	m.recallSel = row
	if m.recallRows[row].hit < 0 {
		m.foldRecall(!m.recallRes.convs[m.recallRows[row].conv].collapsed)
		return nil
	}
	if hit, e, ok := m.selectedRecall(); ok {
		return m.openRecallHit(hit, e)
	}
	return nil
}

// overlayWheel scrolls a list inside an overlay without moving the selection.
// The wheel is for reading, and a list that re-selected as it scrolled would
// make reading past a result the same as choosing it.
func (m *Model) overlayWheel(dir int) bool {
	switch m.overlay {
	case overlayGrep:
		m.grepTop = clampRow(m.grepTop+dir, len(m.grepRows))
		return true
	case overlayRecall:
		m.recallTop = clampRow(m.recallTop+dir, len(m.recallRows))
		return true
	}
	return false
}
