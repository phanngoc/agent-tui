package ui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/session"
)

// The session list is the only place that says what each conversation is
// about, and a title cut to one narrow line says very little: "sbi-fpaas-be:
// why does the…" is not a different session from "sbi-fpaas-be: why is the…".
// So a title wraps onto a second line rather than being truncated, and the
// line underneath carries what is otherwise invisible — which engine answers,
// how far the conversation has got, and when it last moved.
//
// That makes the height of a row depend on its title, which the mouse handler
// cannot guess. Both it and the renderer therefore lay the list out through
// sessionLines, so a click lands on the row that was drawn.

// titleLines is how many lines a title may wrap onto. Two is enough to tell
// two similar conversations apart; three starts pushing the file tree off the
// screen, and the tree is the pane that is actually browsed.
const titleLines = 2

// sessionLine is one rendered row, tagged with the session it belongs to.
type sessionLine struct {
	idx  int // index into the manager's list
	text string
}

// sessionLines renders the whole list. width is the usable width inside the
// pane's border.
func (m *Model) sessionLines(width int) []sessionLine {
	width = max(6, width)
	active := m.mgr.ActiveIndex()

	var out []sessionLine
	// Side chats are shown in the pane of the conversation they hang off, not
	// here: they belong to one rather than standing beside them. They are
	// skipped rather than filtered out, because the row carries the index the
	// manager knows it by — the selection, the active mark and every click
	// are in those terms, and renumbering would point all three at the wrong
	// conversation.
	for i, s := range m.mgr.All() {
		if s.SideOf != "" {
			continue
		}
		selected := m.focus == focusSessions && i == m.sessSel

		// The glyph column says what the conversation is doing; where you are
		// standing is the row's background. They used to share the column, and
		// a session that was both active and running lost the mark that said
		// where you were.
		mark := m.stateMark(s)
		style := m.st.Dim
		if i == active {
			style = m.st.Bold
		}

		head := wrapTitle(s.Label(), width-2, titleLines)
		for j, part := range head {
			prefix := mark + " "
			if j > 0 {
				prefix = "  "
			}
			row := prefix + style.Render(part)
			out = append(out, sessionLine{idx: i, text: m.rowBg(row, width, i == active, selected)})
		}
		out = append(out, sessionLine{
			idx:  i,
			text: m.rowBg("  "+m.sessionMeta(s, width-2), width, i == active, selected),
		})
	}
	return out
}

// rowBg tints a row for where the reader is standing.
//
// Two backgrounds rather than one, because they answer two questions that are
// often different: which conversation the prompt is talking to, and which one
// the cursor is over while you look for another. The selection is the stronger
// of the two, since it is the one that moves.
func (m *Model) rowBg(row string, width int, active, selected bool) string {
	switch {
	case selected:
		return m.st.SelRow.Render(padRight(stripANSI(row), width))
	case active:
		return m.st.ActiveRow.Render(padRight(row, width))
	}
	return row
}

// sessionMeta is the line under a title: what it is doing, and the things that
// distinguish two sessions with similar names.
//
// The state leads, in the state's own colour, because it is the reason to look
// at this line at all. It is the third time the state is said — shape, colour,
// word — which is the point: a glyph nobody has learned yet is a decoration.
func (m *Model) sessionMeta(s *session.Session, width int) string {
	st := m.sessionState(s)
	word := st.word()
	if st == stateWorking && s.Status != "" {
		word = s.Status
	}
	parts := make([]string, 0, 3)
	if e := m.reg.Get(s.Engine); e != nil {
		parts = append(parts, e.ID())
	}
	if n := turnCount(s); n > 0 {
		parts = append(parts, strconv.Itoa(n)+"↵")
	}
	if st != stateWorking && st != stateBlocked && !s.Updated.IsZero() {
		parts = append(parts, relTime(s.Updated))
	}

	// The state is measured after it has been cut, not before. Measuring the
	// word it might have been leaves a negative amount of room, which clamps
	// to a minimum and puts the line over the edge by exactly that minimum.
	word = truncate(word, width)
	out := m.stateStyle(st).Render(word)

	const gap = 2
	room := width - lipgloss.Width(word) - gap
	if tail := strings.Join(parts, " · "); tail != "" && room >= 4 {
		out += m.st.Faint.Render(strings.Repeat(" ", gap) + truncate(tail, room))
	}
	return out
}

// turnCount counts what the user actually asked, which tracks how far a
// conversation has got better than the total message count does.
func turnCount(s *session.Session) int {
	var n int
	for i := range s.Messages {
		if s.Messages[i].Role == session.RoleUser && s.Messages[i].Shell == nil {
			n++
		}
	}
	return n
}

// wrapTitle breaks a title on word boundaries, up to max lines, marking the
// last one with an ellipsis when there is more.
func wrapTitle(title string, width, maxLines int) []string {
	width = max(4, width)
	if title == "" {
		return []string{""}
	}
	wrapped := strings.Split(lipgloss.Wrap(title, width, " "), "\n")
	if len(wrapped) <= maxLines {
		return wrapped
	}
	wrapped = wrapped[:maxLines]
	last := wrapped[maxLines-1]
	wrapped[maxLines-1] = truncate(last+" …", width)
	return wrapped
}

func (m *Model) sessionsPane() string {
	lines := m.sessionLines(max(4, m.sideW-2))
	if len(lines) == 0 {
		return m.st.Faint.Render("  none")
	}
	limit := max(1, m.bodyH-2)
	if len(lines) > limit {
		lines = lines[:limit]
	}
	rows := make([]string, len(lines))
	for i, l := range lines {
		rows[i] = l.text
	}
	return strings.Join(rows, "\n")
}

// ---- renaming --------------------------------------------------------------

// openRename starts editing the selected session's name.
//
// A name derived from the first prompt is a guess, and a poor one when the
// first prompt was "ls". The session list is the only map of what is going on,
// so the name has to be something you can correct.
func (m *Model) openRename() tea.Cmd {
	s := m.renameTarget()
	if s == nil {
		return nil
	}
	m.overlay = overlayRename
	m.renameIn.SetValue(s.Title)
	m.renameIn.Focus()
	m.renameIn.CursorEnd()
	return nil
}

// renameTarget is the session the rename applies to: whichever the session
// list has highlighted, or the active one from anywhere else.
func (m *Model) renameTarget() *session.Session {
	all := m.mgr.All()
	if m.focus == focusSessions && m.sessSel >= 0 && m.sessSel < len(all) {
		return all[m.sessSel]
	}
	return m.mgr.Active()
}

// setSessionName applies a name. An empty one hands the title back to the
// first prompt, which is a useful way out of a bad rename.
func (m *Model) setSessionName(name string) {
	s := m.renameTarget()
	if s == nil {
		return
	}
	name = strings.TrimSpace(strings.ReplaceAll(name, "\n", " "))
	s.Title = name
	s.Dirty = true
	m.mgr.Save(s)

	if name == "" {
		m.notice = "name cleared; the next prompt will suggest one"
		return
	}
	m.notice = "renamed to " + name
}

func (m *Model) renameKey(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "enter":
		m.setSessionName(m.renameIn.Value())
		m.closeOverlay()
		return nil
	case "esc":
		m.closeOverlay()
		return nil
	}
	var cmd tea.Cmd
	m.renameIn, cmd = m.renameIn.Update(k)
	return cmd
}

func (m *Model) renameView() string {
	w := clamp(m.w*2/5, 30, 72)
	s := m.renameTarget()

	var b strings.Builder
	b.WriteString(m.st.Accent.Render("  Name this session") + "\n")
	b.WriteString("  " + m.renameIn.View() + "\n\n")
	if s != nil && s.Title == "" {
		b.WriteString("  " + m.st.Faint.Render("currently named after its first prompt") + "\n")
	}
	b.WriteString("  " + m.st.Faint.Render("enter save · empty clears it · esc cancel"))
	return m.st.Overlay.Width(w).Render(b.String())
}
