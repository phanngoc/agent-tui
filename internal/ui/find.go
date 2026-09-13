package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Find-in-file draws its own highlights rather than using the viewport's.
//
// The viewport's SetHighlights walks the ANSI-stripped content to advance a
// byte position but then indexes the *unstripped* content to count newlines, so
// on syntax-highlighted text the positions drift by however many escape bytes
// came before. Matches are therefore tracked per line, in plain-text columns,
// and spliced into the styled line at render time — no cross-line offset
// arithmetic exists to get wrong.

// findMatch is one hit, located by line and by column within that line.
type findMatch struct {
	line       int // 0-based index into the file's lines
	start, end int // columns in the line's plain text
}

// maxFindHits bounds the work a single keystroke can cause on a large file.
const maxFindHits = 2000

// applyFind locates q in the open file and repaints the preview.
func (m *Model) applyFind(q string) {
	m.findMatches, m.findSel, m.findHits = nil, 0, 0
	if m.file == nil || len(q) < 2 {
		m.restorePreview()
		return
	}

	// Smart case, the same rule the project search uses.
	fold := strings.ToLower(q) == q
	needle := q
	if fold {
		needle = strings.ToLower(q)
	}

	for i, line := range m.file.Plain {
		hay := line
		if fold {
			hay = strings.ToLower(line)
		}
		// Lowering can change byte length on some runes, which would shift the
		// columns; fall back to the line as written when that happens.
		if len(hay) != len(line) {
			hay = line
			if fold && !strings.Contains(line, q) {
				continue
			}
		}
		for at := 0; ; {
			j := strings.Index(hay[at:], needle)
			if j < 0 {
				break
			}
			start := at + j
			m.findMatches = append(m.findMatches, findMatch{
				line:  i,
				start: colOf(line, start),
				end:   colOf(line, start+len(needle)),
			})
			at = start + len(needle)
			if len(m.findMatches) >= maxFindHits {
				break
			}
		}
		if len(m.findMatches) >= maxFindHits {
			break
		}
	}

	m.findHits = len(m.findMatches)
	m.paintFind()
	if m.findHits > 0 {
		m.showFindMatch()
	}
}

// colOf converts a byte offset in a line to a display column.
func colOf(line string, byteOffset int) int {
	if byteOffset <= 0 {
		return 0
	}
	if byteOffset > len(line) {
		byteOffset = len(line)
	}
	return ansi.StringWidth(line[:byteOffset])
}

// paintFind rewrites the previewed lines that contain matches, leaving the rest
// exactly as the highlighter produced them.
func (m *Model) paintFind() {
	if m.file == nil {
		return
	}
	if len(m.findMatches) == 0 {
		m.restorePreview()
		return
	}

	lines := make([]string, len(m.file.Styled))
	copy(lines, m.file.Styled)

	// Splice from the right so earlier columns stay valid as we go.
	for i := len(m.findMatches) - 1; i >= 0; i-- {
		hit := m.findMatches[i]
		if hit.line >= len(lines) {
			continue
		}
		style := m.st.Match
		if i == m.findSel {
			style = m.st.MatchOn
		}
		lines[hit.line] = spliceStyle(lines[hit.line], hit.start, hit.end, style.Render)
	}
	m.prev.SetContentLines(lines)
}

// spliceStyle restyles the columns [start,end) of an ANSI-styled line.
func spliceStyle(line string, start, end int, render func(...string) string) string {
	width := ansi.StringWidth(line)
	if start >= end || start >= width {
		return line
	}
	if end > width {
		end = width
	}
	middle := ansi.Strip(ansi.Cut(line, start, end))
	return ansi.Cut(line, 0, start) + render(middle) + ansi.Cut(line, end, width)
}

// restorePreview puts the untouched highlighted lines back.
func (m *Model) restorePreview() {
	if m.file != nil {
		m.prev.SetContentLines(m.file.Styled)
	}
}

// showFindMatch scrolls to the selected hit and moves the caret line to it.
func (m *Model) showFindMatch() {
	if m.findSel < 0 || m.findSel >= len(m.findMatches) {
		return
	}
	line := m.findMatches[m.findSel].line
	m.fileLine = line + 1
	if line < m.prev.YOffset() || line >= m.prev.YOffset()+m.prev.Height() {
		m.centreOn(m.fileLine)
	}
}

// stepFind moves to the next or previous hit, wrapping around.
func (m *Model) stepFind(delta int) {
	if n := len(m.findMatches); n > 0 {
		m.findSel = ((m.findSel+delta)%n + n) % n
		m.paintFind()
		m.showFindMatch()
	}
}
