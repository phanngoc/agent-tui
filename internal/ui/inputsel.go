package ui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Selecting inside the prompt.
//
// Most of it was already there and unreachable. The textarea binds shift and
// the arrows to a selection, and alt+shift to a word of one, and both worked;
// what did not was everything else you would try. Select-all was bound to
// ctrl+g, which this program had taken for the file search before the prompt
// ever saw it. Copy was bound to a chord many terminals do not send. And the
// mouse did nothing at all, because the prompt is the one pane with no box the
// pointer could be asked about.
//
// So: give ctrl+g back while the caret is in the prompt, make ctrl+c mean copy
// there as it already does in the transcript, and give the box to the mouse.
//
// The mouse part is built on a constraint worth naming. The textarea exposes
// no way to begin a selection at a point — only the keys that extend one from
// the cursor. Rather than reach inside it, a drag is expressed in its own
// terms: put the cursor where the press was, then extend by the difference
// each time the pointer moves. It is the same selection the keyboard makes,
// which is the point — one selection, one set of rules, however it was begun.

// inputBox is where the prompt's text was drawn: the cell the first character
// of the first row sits on, and the size of the writable area.
//
// The prompt repeats a two-column mark on every row, and the box has a border,
// so neither the border nor the mark is part of what a click can land in.
func (m *Model) inputArea() (left, top, w, h int, ok bool) {
	if !m.ready || m.w < 8 {
		return 0, 0, 0, 0, false
	}
	top = headerRows + m.bodyH + 1 + m.attachRows()
	mark := lipgloss.Width(m.input.Prompt)
	left = 1 + mark
	w = m.w - 2 - mark
	h = inputRows - 2 - m.attachRows()
	if w < 1 || h < 1 {
		return 0, 0, 0, 0, false
	}
	return left, top, w, h, true
}

// inputAt maps a screen cell onto an offset in the prompt's text, and reports
// whether the cell is inside the writable area at all.
//
// Offsets rather than a row and a column, because that is what a drag has to
// do arithmetic on: the distance between two points is a number of characters,
// and the keys that extend a selection move by one character each.
func (m *Model) inputAt(x, y int) (int, bool) {
	left, top, w, h, ok := m.inputArea()
	if !ok || x < left || y < top || y >= top+h {
		return 0, false
	}
	row := y - top
	col := clamp(x-left, 0, w)

	lines := m.inputLines()
	if row >= len(lines) {
		// Past the last line is the end of the text, which is where a click
		// below the last word should put the caret.
		return m.inputLen(), true
	}
	at := 0
	for i := 0; i < row; i++ {
		at += len([]rune(lines[i])) + 1 // the newline counts as a step
	}
	return at + min(col, len([]rune(lines[row]))), true
}

// inputOffset is where the caret is now, in the same units.
func (m *Model) inputOffset() int {
	lines := m.inputLines()
	row := clamp(m.input.Line(), 0, max(0, len(lines)-1))
	at := 0
	for i := 0; i < row; i++ {
		at += len([]rune(lines[i])) + 1
	}
	return at + m.input.LineInfo().ColumnOffset
}

func (m *Model) inputLines() []string { return splitLines(m.input.Value()) }

func (m *Model) inputLen() int {
	n := 0
	for i, l := range m.inputLines() {
		if i > 0 {
			n++
		}
		n += len([]rune(l))
	}
	return n
}

// beginInputSelect puts the caret where the press was and remembers it, so the
// motion that follows has something to measure from.
func (m *Model) beginInputSelect(e tea.Mouse) bool {
	at, ok := m.inputAt(e.X, e.Y)
	if !ok {
		return false
	}
	m.input.ClearSelection()
	m.moveInputTo(at)
	m.inputDrag, m.inputHead = true, at
	return true
}

// extendInputSelect grows the selection to wherever the pointer has got to.
//
// By the difference, not by rebuilding it: the textarea has no way to be told
// where a selection begins, only keys that extend one from the caret. Stepping
// the difference keeps that cost proportional to how far the pointer moved
// rather than to how much is selected.
func (m *Model) extendInputSelect(e tea.Mouse) {
	if !m.inputDrag {
		return
	}
	at, ok := m.inputAt(e.X, e.Y)
	if !ok {
		return
	}
	step := tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift}
	if at < m.inputHead {
		step = tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift}
	}
	for n := abs(at - m.inputHead); n > 0; n-- {
		m.input, _ = m.input.Update(step)
	}
	m.inputHead = at
}

// moveInputTo puts the caret at an offset, in the only terms the textarea
// takes: a row, then a column within it.
func (m *Model) moveInputTo(at int) {
	lines := m.inputLines()
	row, left := 0, at
	for row < len(lines)-1 && left > len([]rune(lines[row])) {
		left -= len([]rune(lines[row])) + 1
		row++
	}
	for m.input.Line() > row {
		m.input.CursorUp()
	}
	for m.input.Line() < row {
		m.input.CursorDown()
	}
	m.input.SetCursorColumn(clamp(left, 0, len([]rune(lines[min(row, len(lines)-1)]))))
}

// copyInput puts the prompt's selection on the clipboard.
//
// Through this program's own clipboard rather than the textarea's: the one it
// bundles writes with a library that has no base64 path for Windows, which is
// where a console code page mangles anything that is not ASCII — and what is
// typed here is routinely Vietnamese.
func (m *Model) copyInput() tea.Cmd {
	if !m.input.HasSelection() {
		return nil
	}
	return m.copyText(m.input.SelectedText())
}

func splitLines(s string) []string {
	out := []string{""}
	for _, r := range s {
		if r == '\n' {
			out = append(out, "")
			continue
		}
		out[len(out)-1] += string(r)
	}
	return out
}
