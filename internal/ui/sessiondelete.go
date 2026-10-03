package ui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/phanngoc/agent-tui/internal/session"
)

// Getting rid of conversations.
//
// What other tools do, and what was taken from them:
//
//   - A mail client deletes at once and offers Undo for a few seconds. Asking
//     first is the habit it replaced: a confirmation in front of every delete
//     is answered without being read, so it protects nothing and costs a
//     keystroke every time. Deleting here is immediate, and `u` brings it back.
//   - ChatGPT and Claude put a delete on every conversation in the sidebar,
//     shown when the pointer is over it, and keep "archive" apart from
//     "delete". Here a row under the pointer shows ✕, and closing (x, ctrl+w)
//     stays the non-destructive one: the conversation leaves the list and
//     /recall still finds it.
//   - Claude's conversation list selects several and deletes them in one go,
//     which is how a pile of finished sessions gets cleared. space marks rows;
//     d takes them all, and one u brings them all back.
//   - The trash of a desktop is what makes Undo outlive the program: the file
//     goes there rather than away, and is emptied after a week.
//   - The one thing a confirmation is still right for is what cannot be
//     undone. Deleting a conversation that is running stops its turn, and no
//     undo restarts it — so that delete takes a second press, said in place.

// sessDel is the session list's delete state.
type sessDel struct {
	marks map[string]bool // ids marked with space
	armed string          // ids of a running delete waiting for its second press
	last  *session.Deleted
	// hover is the index of the session under the pointer, when hovered.
	hover   int
	hovered bool
}

// deleteTargets are what d acts on: the marked conversations if there are
// any, and the one under the cursor if not.
func (m *Model) deleteTargets() []*session.Session {
	all := m.mgr.All()
	var out []*session.Session
	for _, s := range all {
		if m.del.marks[s.ID] && s.SideOf == "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 && m.sessSel >= 0 && m.sessSel < len(all) {
		out = append(out, all[m.sessSel])
	}
	return out
}

// deleteSessions deletes conversations, at once and undoably — unless one of
// them is running, which the delete would stop for good, and then the first
// press says so and the second does it.
func (m *Model) deleteSessions(list []*session.Session) tea.Cmd {
	if len(list) == 0 {
		return nil
	}
	ids := make([]string, len(list))
	var running []*session.Session
	for i, s := range list {
		ids[i] = s.ID
		if m.sessionRunning(s) {
			running = append(running, s)
		}
	}
	key := strings.Join(ids, ",")
	if len(running) > 0 && m.del.armed != key {
		m.del.armed = key
		what := "«" + running[0].Label() + "» is running"
		if len(running) > 1 {
			what = plural(len(running), "session") + " are running"
		}
		m.notice = what + " · d again stops it and deletes"
		return nil
	}
	m.del.armed = ""

	for _, s := range running {
		m.stopRun(s)
		if side := m.mgr.SideOf(s.ID); side != nil {
			m.stopRun(side)
		}
	}
	rec := m.mgr.Delete(ids...)
	if rec == nil {
		return nil
	}
	m.del.last = rec
	m.del.marks = nil
	for _, s := range rec.Sessions() {
		delete(m.drafts, s.ID)
	}

	if len(list) == 1 {
		m.notice = "deleted «" + list[0].Label() + "» · u brings it back"
	} else {
		m.notice = "deleted " + plural(len(list), "session") + " · u brings them back"
	}
	m.sessSel = clamp(m.sessSel, 0, max(0, m.mgr.Len()-1))
	return m.onSessionSwitch()
}

// undoDelete brings back what the last delete took.
func (m *Model) undoDelete() tea.Cmd {
	rec := m.del.last
	if rec == nil {
		m.notice = "nothing deleted to bring back"
		return nil
	}
	m.del.last = nil
	m.mgr.Undelete(rec)
	back := rec.Sessions()
	n := 0
	for _, s := range back {
		if s.SideOf == "" {
			n++
		}
	}
	if n == 1 {
		m.notice = "brought back «" + back[0].Label() + "»"
	} else {
		m.notice = "brought back " + plural(n, "session")
	}
	m.sessSel = m.mgr.ActiveIndex()
	return m.onSessionSwitch()
}

// closeSession takes a conversation out of the list and leaves it in the store.
func (m *Model) closeSession(i int) tea.Cmd {
	all := m.mgr.All()
	if i < 0 || i >= len(all) {
		return nil
	}
	s := all[i]
	m.mgr.Close(i)
	if len(s.Messages) > 0 {
		m.notice = "closed «" + s.Label() + "» · /recall finds it · d in the list deletes"
	} else {
		m.notice = "session closed"
	}
	m.sessSel = clamp(m.sessSel, 0, max(0, m.mgr.Len()-1))
	return m.onSessionSwitch()
}

// toggleMark marks or unmarks the conversation under the cursor.
func (m *Model) toggleMark() {
	all := m.mgr.All()
	if m.sessSel < 0 || m.sessSel >= len(all) {
		return
	}
	id := all[m.sessSel].ID
	if m.del.marks == nil {
		m.del.marks = map[string]bool{}
	}
	if m.del.marks[id] {
		delete(m.del.marks, id)
	} else {
		m.del.marks[id] = true
	}
	m.del.armed = ""
	if n := len(m.del.marks); n > 0 {
		m.notice = plural(n, "session") + " marked · d deletes · esc clears"
	} else {
		m.notice = ""
	}
}

// sessionRunning is whether deleting would stop something: a turn, a `!`
// command, or the side chat's turn.
func (m *Model) sessionRunning(s *session.Session) bool {
	if s.Busy || s.Running > 0 {
		return true
	}
	side := m.mgr.SideOf(s.ID)
	return side != nil && side.Busy
}

// stopRun cancels one session's turn, whichever session it is.
func (m *Model) stopRun(s *session.Session) {
	if cancel, ok := m.runs[s.ID]; ok {
		cancel()
		delete(m.runs, s.ID)
	}
	m.dropApprovals(s)
	m.dropChoices(s)
	s.Busy, s.Status = false, ""
}

// hoverSession tracks which conversation the pointer is over, for its ✕.
func (m *Model) hoverSession(x, y int) {
	idx, ok := -1, false
	if m.paneAt(x, y) == focusSessions {
		if i := m.sessionRowAt(y); i >= 0 {
			idx, ok = i, true
		}
	}
	m.del.hover, m.del.hovered = idx, ok
}

// sessionDeleteAt reports whether a click at x, y is on the ✕ of a row.
//
// The ✕ is drawn in the last column of a conversation's first line, so it is
// that column and that line — not the row's other lines, where a click opens
// the conversation as it always did.
func (m *Model) sessionDeleteAt(x, y int) (int, bool) {
	if !m.del.hovered {
		return -1, false
	}
	left := m.colX(colSide)
	if x < left+m.sideW-3 || x > left+m.sideW-2 {
		return -1, false
	}
	top := m.sessTopY() + 1
	lines := m.sessionLines(max(4, m.sideW-2))
	row := y - top + m.sessTop
	if row < 0 || row >= len(lines) {
		return -1, false
	}
	idx := lines[row].idx
	if idx != m.del.hover || (row > 0 && lines[row-1].idx == idx) {
		return -1, false
	}
	return idx, true
}

// withDeleteMark puts the ✕ at the end of a row the pointer is over.
func (m *Model) withDeleteMark(row string, width int) string {
	if w := lipgloss.Width(row); w > width-2 {
		row = ansi.Truncate(row, width-2, "")
	}
	return row + strings.Repeat(" ", max(0, width-1-lipgloss.Width(row))) + m.st.Bad.Render("✕")
}

// delKey is the part of the list's cache key that delete state draws.
func (m *Model) delKey() string {
	var b strings.Builder
	if m.del.hovered {
		b.WriteString("h" + strconv.Itoa(m.del.hover))
	}
	// In list order, not the map's: a key that differs between two frames
	// of the same list is a cache that never hits.
	for _, s := range m.mgr.All() {
		if m.del.marks[s.ID] {
			b.WriteString("|" + s.ID)
		}
	}
	return b.String()
}
