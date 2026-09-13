package ui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/phanngoc/agent-tui/internal/search"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// Project search is shown the way an editor shows it: grouped under the file
// the hits came from, with a count per file, rather than as a flat list of
// path:line pairs. A flat list makes you read the same path over and over and
// gives no sense of where the weight is.

// grepFile is one file's worth of hits.
type grepFile struct {
	path      string
	dir       string
	name      string
	hits      []search.Match
	collapsed bool
}

// grepRow is a line in the rendered tree: either a file header or one hit.
type grepRow struct {
	file  int  // index into the grouped files
	hit   int  // index into that file's hits, or -1 for the header
	isDir bool // unused today; kept so the row type can carry a folder level
}

// groupMatches turns a flat result into per-file groups, preserving the order
// the search returned them in.
func groupMatches(matches []search.Match) []grepFile {
	var out []grepFile
	index := map[string]int{}
	for _, mt := range matches {
		i, seen := index[mt.Path]
		if !seen {
			i = len(out)
			index[mt.Path] = i
			out = append(out, grepFile{
				path: mt.Path,
				dir:  dirOf(mt.Path),
				name: vfs.Base(mt.Path),
			})
		}
		out[i].hits = append(out[i].hits, mt)
	}
	return out
}

func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return ""
}

// rebuildGrepRows flattens the groups into the rows the list actually shows,
// honouring which files are collapsed.
func (m *Model) rebuildGrepRows() {
	m.grepRows = m.grepRows[:0]
	for i := range m.grepFiles {
		m.grepRows = append(m.grepRows, grepRow{file: i, hit: -1})
		if m.grepFiles[i].collapsed {
			continue
		}
		for j := range m.grepFiles[i].hits {
			m.grepRows = append(m.grepRows, grepRow{file: i, hit: j})
		}
	}
	if m.grepSel >= len(m.grepRows) {
		m.grepSel = max(0, len(m.grepRows)-1)
	}
}

// selectedHit returns the match under the cursor, if the cursor is on one.
func (m *Model) selectedHit() (search.Match, bool) {
	if m.grepSel < 0 || m.grepSel >= len(m.grepRows) {
		return search.Match{}, false
	}
	row := m.grepRows[m.grepSel]
	if row.hit < 0 {
		return search.Match{}, false
	}
	return m.grepFiles[row.file].hits[row.hit], true
}

// grepTreeView renders the grouped results.
func (m *Model) grepTreeView(width, rows int) string {
	inner := width - 2
	var b strings.Builder

	if len(m.grepRows) == 0 {
		return ""
	}

	// Keep the selection in view.
	if m.grepSel < m.grepTop {
		m.grepTop = m.grepSel
	}
	if m.grepSel >= m.grepTop+rows {
		m.grepTop = m.grepSel - rows + 1
	}
	m.grepTop = clamp(m.grepTop, 0, max(0, len(m.grepRows)-rows))

	for i := m.grepTop; i < len(m.grepRows) && i < m.grepTop+rows; i++ {
		row := m.grepRows[i]
		f := m.grepFiles[row.file]
		selected := i == m.grepSel

		var line string
		if row.hit < 0 {
			line = m.grepHeader(f, inner)
		} else {
			line = m.grepHit(f.hits[row.hit], inner)
		}
		if selected {
			line = m.st.SelRow.Render(padRight(" "+stripANSI(line), inner))
		}
		b.WriteString(clipLine(line, inner) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// grepHeader is a file's row: a fold marker, its name, where it lives, and how
// many hits it holds.
func (m *Model) grepHeader(f grepFile, inner int) string {
	mark := "▾"
	if f.collapsed {
		mark = "▸"
	}
	count := m.st.Warn.Render(" " + strconv.Itoa(len(f.hits)) + " ")

	left := " " + m.st.Faint.Render(mark) + " " + m.st.Bold.Render(f.name)
	if f.dir != "" {
		left += "  " + m.st.Faint.Render(truncate(f.dir, max(8, inner/2)))
	}
	pad := inner - lipgloss.Width(left) - lipgloss.Width(count)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + count
}

// grepHit is one matching line, with the match itself picked out.
//
// The line is trimmed around the match so a hit deep inside a long line is
// still visible, the way a search panel does it.
func (m *Model) grepHit(hit search.Match, inner int) string {
	const gutter = 6
	room := max(12, inner-gutter-8)

	text, start, end := windowAround(hit.Text, hit.Start, hit.End, room)
	line := m.st.Faint.Render(pad(itoa(hit.Line), gutter-1) + " ")
	if start > 0 {
		line += m.st.Dim.Render(text[:start])
	}
	line += m.st.Match.Render(text[start:end])
	line += m.st.Dim.Render(text[end:])
	return "   " + line
}

// windowAround trims a line to room columns while keeping the match inside it,
// returning the adjusted offsets.
func windowAround(text string, start, end, room int) (string, int, int) {
	if ansi.StringWidth(text) <= room {
		return text, clampIdx(start, text), clampIdx(end, text)
	}
	const lead = 12
	from := 0
	if start > lead {
		from = start - lead
	}
	to := from + room
	if to > len(text) {
		to = len(text)
		from = max(0, to-room)
	}

	out := text[from:to]
	s, e := clampIdx(start-from, out), clampIdx(end-from, out)
	if from > 0 {
		out, s, e = "…"+out, s+3, e+3
	}
	if to < len(text) {
		out += "…"
	}
	return out, s, e
}

func clampIdx(i int, s string) int {
	if i < 0 {
		return 0
	}
	if i > len(s) {
		return len(s)
	}
	return i
}
