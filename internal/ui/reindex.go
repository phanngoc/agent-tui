package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// Keeping the file index current.
//
// It used to be walked once — at start, and when a session moved — so a file
// the agent wrote afterwards was not in ctrl+p until the program restarted.
// Walking on every change would be correct and expensive: on WSL or in a
// container a walk is a `find` through a process boundary, and a busy turn
// writes many files.
//
// So the index is kept current in three ways, cheapest first:
//
//   - a file the agent writes is added by name the moment the call finishes,
//     which is no walk at all and is most of what anyone looks for;
//   - anything else that may have changed the tree — a shell command, the end
//     of a turn, the explorer seeing a directory change — only marks the
//     index stale, which is a store;
//   - the walk happens when something is about to read the index and it is
//     stale or old: opening the finder or the content search. The old list
//     answers at once and the new one replaces it when it lands, with the
//     selection kept, so freshness never costs a wait.
//
// A finder already open is the exception: it is reading now, so a change
// walks straight away. Walks never overlap — one asked for while another runs
// makes that one go round again.

// indexMaxAge bounds how long a change nobody reported — an editor, a git
// checkout in another terminal — can be missing from the finder.
const indexMaxAge = 30 * time.Second

// freshenIndex walks the index if it may be out of date, for a reader about
// to look at it.
func (m *Model) freshenIndex() tea.Cmd {
	if m.idx.Fresh(indexMaxAge) {
		return nil
	}
	// A walk in progress already covers anything but a change reported
	// since it began.
	if m.idx.Building() && !m.idx.Stale() {
		return nil
	}
	return m.walkIndex()
}

// walkIndex walks quietly, in the background.
func (m *Model) walkIndex() tea.Cmd {
	idx := m.idx
	return func() tea.Msg {
		if !idx.Build() {
			return nil // the walk already running will report
		}
		return indexReadyMsg{n: idx.Len(), took: idx.Took(), quiet: true}
	}
}

// filesChanged notes that files may have appeared or gone. Only the finder,
// open and being read, is worth walking for now.
func (m *Model) filesChanged() tea.Cmd {
	m.idx.MarkStale()
	if m.overlay == overlayFinder || m.overlay == overlayGrep {
		return m.walkIndex()
	}
	return nil
}

// noteToolFiles keeps the index up with a finished tool call: what it wrote is
// added by name, and a call that may have written without saying what — a
// shell command — marks the index stale.
func (m *Model) noteToolFiles(s *session.Session, call session.ToolCall) tea.Cmd {
	if call.IsError || call.Denied {
		return nil
	}
	paths := call.WrittenPaths()
	if len(paths) == 0 {
		if isShellTool(call.Name) {
			return m.filesChanged()
		}
		return nil
	}
	// The index covers the active session's filesystem; a background session
	// working somewhere else writes to files it does not list.
	if fsID(m.sessionFS(s)) != fsID(m.idx.FS()) {
		return nil
	}
	root, cwd := m.idx.Root(), m.sessionCWD(s)
	var rels []string
	for _, p := range paths {
		if !vfs.IsAbs(p) {
			p = vfs.Join(cwd, p)
		}
		p = vfs.CleanPath(p)
		if vfs.Within(root, p) {
			if rel := vfs.Rel(root, p); rel != "" && rel != p {
				rels = append(rels, rel)
			}
		}
	}
	m.idx.Add(rels...)
	if len(rels) > 0 && m.overlay == overlayFinder {
		m.refreshFinderKeep()
	}
	return nil
}

func isShellTool(name string) bool {
	switch strings.ToLower(name) {
	case "bash", "shell", "command_execution", "run_command":
		return true
	}
	return false
}
