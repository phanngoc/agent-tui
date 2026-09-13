package ui

import (
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

func (m *Model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "agent-tui · " + m.projectName()
	v.BackgroundColor = m.st.P.Bg

	if !m.ready {
		v.SetContent(m.st.Dim.Render("terminal too small — needs at least 20x10"))
		return v
	}

	body := lipgloss.JoinVertical(lipgloss.Left,
		m.header(),
		m.panes(),
		m.inputBox(),
		m.statusBar(),
	)

	if m.compOpen {
		body = m.composeCompletion(body)
	}
	if m.overlay != overlayNone {
		body = m.composeOverlay(body)
	}
	v.SetContent(body)
	v.Cursor = m.cursor()
	return v
}

// header is the session tab strip.
func (m *Model) header() string {
	sessions := m.mgr.All()
	active := m.mgr.ActiveIndex()

	var sb strings.Builder
	sb.WriteString(m.st.Accent.Render(" ▪ "))
	sb.WriteString(m.st.Bold.Render(m.projectName()))
	sb.WriteString("  ")

	for i, s := range sessions {
		if i > 4 {
			sb.WriteString(m.st.Faint.Render(" +" + strconv.Itoa(len(sessions)-i)))
			break
		}
		label := truncate(s.Label(), 18)
		if s.Busy {
			label = m.spin.View() + " " + label
		}
		if i == active {
			sb.WriteString(m.st.TabOn.Render(label))
		} else {
			sb.WriteString(m.st.TabOff.Render(label))
		}
	}
	return clipLine(sb.String(), m.w)
}

// panes lays out the three side-by-side columns.
func (m *Model) panes() string {
	cols := make([]string, 0, 3)

	if m.sideW > 0 {
		cols = append(cols, m.leftColumn())
	}

	chatTitle := "transcript"
	if s := m.mgr.Active(); s.Busy && s.Status != "" {
		chatTitle = s.Status
	}
	m.chat.SetContent(m.transcript(max(10, m.chatW-2)))
	cols = append(cols, m.pane(m.chat.View(), chatTitle, m.chatW, m.bodyH, m.focus == focusChat))

	if m.prevW > 0 {
		cols = append(cols, m.pane(m.previewPane(), m.previewTitle(), m.prevW, m.bodyH, m.focus == focusPreview))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cols...)
}

// leftColumn stacks the session list over the project tree. The tree gets the
// remaining height because it is the pane that is actually browsed.
func (m *Model) leftColumn() string {
	// Shared with the mouse handler so a click lands on the row that was drawn.
	sessH, treeH := m.leftSplit()

	return lipgloss.JoinVertical(lipgloss.Left,
		m.pane(m.sessionsPane(), "sessions", m.sideW, sessH, m.focus == focusSessions),
		m.pane(m.explorerPane(treeH-2), m.explorerTitle(), m.sideW, treeH, m.focus == focusExplorer),
	)
}

// explorerTitle names the directory the active session's agent runs in, and
// says when we are inside a container or WSL, because that is the thing that
// explains why file events may not be arriving.
func (m *Model) explorerTitle() string {
	root := m.tree.Root()
	name := vfs.Base(root)
	// Inside the project, the relative path says more than the bare directory
	// name; outside it (a container path, say), the absolute path does.
	if rel := m.idx.Rel(root); rel != "" && rel != root && rel != "." {
		name = rel
	} else if f := m.tree.FS(); f != nil && !f.IsLocal() {
		name = root
	}
	if f := m.tree.FS(); f != nil && !f.IsLocal() {
		name = f.Label() + ":" + name
	} else if env := m.tree.Env().Label(); env != "" {
		name += "  " + env
	}
	return name
}

// explorerPane renders the visible slice of the tree.
func (m *Model) explorerPane(height int) string {
	rows := m.tree.Rows()
	if len(rows) == 0 {
		return m.st.Faint.Render("  (empty)")
	}
	height = max(1, height)

	// Keep the selection inside the window.
	if m.treeSel < m.treeTop {
		m.treeTop = m.treeSel
	}
	if m.treeSel >= m.treeTop+height {
		m.treeTop = m.treeSel - height + 1
	}
	m.treeTop = clamp(m.treeTop, 0, max(0, len(rows)-height))

	inner := max(4, m.sideW-2)
	var b strings.Builder
	for i := m.treeTop; i < len(rows) && i < m.treeTop+height; i++ {
		n := rows[i]
		indent := strings.Repeat("  ", min(n.Depth, 6))

		icon, style := " ", m.st.Dim
		switch {
		case n.IsParent():
			icon, style = "↰", m.st.Faint
		case n.Dir:
			icon, style = "▸", m.st.Accent
			if n.Open {
				icon = "▾"
			}
		}
		label := n.Name
		if n.Dir && !n.IsParent() {
			label += "/"
		}
		row := " " + indent + style.Render(icon) + " " + m.st.Dim.Render(label)
		if m.file != nil && !n.Dir && filepath.Join(m.tree.Root(), filepath.FromSlash(n.Rel)) == m.file.Abs {
			row = " " + indent + "  " + m.st.Good.Render(label)
		}
		if i == m.treeSel && m.focus == focusExplorer {
			row = m.st.SelRow.Render(padRight(" "+indent+icon+" "+label, inner))
		}
		b.WriteString(clipLine(row, inner))
		b.WriteByte('\n')
	}
	return b.String()
}

// pane wraps content in a bordered box with a title in the top border.
//
// Lip Gloss v2 counts the border inside Width and Height, so the box is sized
// to the full pane and the content is clipped to the space left inside it.
func (m *Model) pane(content, title string, w, h int, active bool) string {
	style := m.st.Pane
	ts := m.st.Title
	if active {
		style, ts = m.st.PaneActive, m.st.TitleOn
	}
	inner := max(1, w-2)

	box := style.Width(w).Height(max(3, h)).Render(clipBlock(content, inner, h-2))
	lines := strings.Split(box, "\n")
	if len(lines) > 0 {
		lines[0] = injectTitle(lines[0], ts.Render(" "+truncate(title, max(4, inner-6))+" "))
	}
	return strings.Join(lines, "\n")
}

// injectTitle overwrites the start of a top border run with a label.
func injectTitle(border, label string) string {
	lw := lipgloss.Width(label)
	bw := lipgloss.Width(border)
	if lw+4 >= bw {
		return border
	}
	// Rebuild as: first 2 border cells, the label, then the remaining border.
	plain := []rune(stripANSI(border))
	if len(plain) < 3 {
		return border
	}
	head := string(plain[:2])
	tail := string(plain[2+lw:])
	return head + label + tail
}

func (m *Model) previewTitle() string {
	if m.file == nil {
		return "preview"
	}
	t := m.file.Rel
	if m.file.Lang != "" {
		t += "  " + m.file.Lang
	}
	if m.file.Truncated {
		t += "  (truncated)"
	}
	return t
}

func (m *Model) previewPane() string {
	if m.file == nil {
		hint := []string{
			"",
			m.st.Dim.Render("  No file open."),
			"",
			"  " + m.st.StatusKey.Render(" ctrl+p ") + " " + m.st.Dim.Render("open a file"),
			"  " + m.st.StatusKey.Render(" ctrl+f ") + " " + m.st.Dim.Render("search contents"),
		}
		return strings.Join(hint, "\n")
	}
	body := m.prev.View()
	if m.finding {
		bar := m.st.Accent.Render("/") + m.findIn.View()
		count := m.st.Faint.Render("  " + strconv.Itoa(m.findHits) + " hits")
		body = clipBlock(body, m.prevW-2, m.prev.Height()-1) + "\n" + clipLine(bar+count, m.prevW-2)
	}
	return body
}

func (m *Model) sessionsPane() string {
	var b strings.Builder
	active := m.mgr.ActiveIndex()
	for i, s := range m.mgr.All() {
		if i >= m.bodyH-2 {
			break
		}
		mark := "  "
		style := m.st.Dim
		switch {
		case i == active:
			mark, style = m.st.Accent.Render("▸ "), m.st.Bold
		case m.focus == focusSessions && i == m.sessSel:
			mark = m.st.Faint.Render("· ")
		}
		label := truncate(s.Label(), max(6, m.sideW-6))
		row := mark + style.Render(label)
		if s.Busy {
			row = mark + m.spin.View() + " " + style.Render(truncate(s.Label(), max(4, m.sideW-8)))
		}
		if m.focus == focusSessions && i == m.sessSel {
			row = m.st.SelRow.Render(padRight(stripANSI(row), m.sideW-2))
		}
		b.WriteString(row)
		b.WriteByte('\n')

		// The engine is part of a session's identity: the same prompt behaves
		// differently depending on which agent answers it.
		if e := m.reg.Get(s.Engine); e != nil {
			b.WriteString("   " + m.st.Faint.Render(truncate("["+e.ID()+"]", max(4, m.sideW-5))))
			b.WriteByte('\n')
		}
	}
	if m.mgr.Len() == 0 {
		b.WriteString(m.st.Faint.Render("  none"))
	}
	return b.String()
}

func (m *Model) inputBox() string {
	style := m.st.Pane
	if m.focus == focusInput {
		style = m.st.PaneActive
	}
	return style.Width(max(3, m.w)).Render(m.input.View())
}

func (m *Model) statusBar() string {
	s := m.mgr.Active()
	left := make([]string, 0, 6)

	if s.Busy {
		left = append(left, m.spin.View()+" "+m.st.Accent.Render(orDefault(s.Status, "working")))
	} else if m.errText != "" {
		left = append(left, m.st.Bad.Render("✗ "+truncate(m.errText, max(20, m.w/2))))
	} else if m.notice != "" {
		left = append(left, m.st.Good.Render(m.notice))
	} else {
		left = append(left, m.st.Dim.Render(hintFor(m.focus)))
	}

	engineLabel := m.cfg.Model
	if e := m.reg.Get(s.Engine); e != nil {
		engineLabel = e.Label()
		if e.ID() == "api" {
			engineLabel = m.cfg.Model
		}
	}

	right := []string{m.modeBadge(s)}
	if n := m.tasks.LiveCount(); n > 0 {
		right = append(right, m.st.Accent.Render("●"+strconv.Itoa(n)+" running"))
	}
	right = append(right,
		m.st.Faint.Render(engineLabel),
		m.st.Faint.Render(tokens(s)),
		m.st.Faint.Render(strconv.Itoa(m.idx.Len())+" files"),
		m.st.StatusKey.Render(" f1 ")+m.st.Faint.Render(" help"),
	)

	l := strings.Join(left, "  ")
	r := strings.Join(right, m.st.Faint.Render(" · "))
	gap := m.w - lipgloss.Width(l) - lipgloss.Width(r) - 2
	if gap < 1 {
		return clipLine(" "+l, m.w)
	}
	return " " + l + strings.Repeat(" ", gap) + r + " "
}

// modeBadge shows what the agent may do, coloured by how much rope that is. It
// is always on screen, because a mode that acts without asking should never be
// a surprise.
func (m *Model) modeBadge(s *session.Session) string {
	mode := sessionMode(s)
	style := m.st.Dim
	switch mode {
	case agent.ModePlan:
		style = m.st.Good
	case agent.ModeAsk:
		style = m.st.Accent
	case agent.ModeFull:
		style = m.st.Bad
	case agent.ModeAuto:
		style = m.st.Warn
	}

	label := mode.Label()
	// An engine that cannot honour "ask" should not look as though it does.
	if mode.Confirms() {
		if e := m.reg.Get(s.Engine); e != nil && !e.CanAsk() {
			label += " ⚠"
			style = m.st.Warn
		}
	}
	return style.Render("▌" + label)
}

func tokens(s *session.Session) string {
	if s.InputTokens+s.OutputTokens == 0 {
		return "0 tok"
	}
	return compact(s.InputTokens) + "↓ " + compact(s.OutputTokens) + "↑"
}

func compact(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k"
	default:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + "M"
	}
}

func hintFor(f focus) string {
	switch f {
	case focusPreview:
		return "preview · / find · n next · w wrap · tab switch pane"
	case focusChat:
		return "transcript · ↑↓ scroll · tab or ctrl+o switch pane"
	case focusExplorer:
		return "files · ↑↓ browse · click to open · ←→ fold · - up a level · r work here"
	case focusSessions:
		return "sessions · ↑↓ select · enter open · n new · f fork · d close"
	default:
		return "enter send · /help for commands · tab complete · ↑↓ history · ctrl+o next pane"
	}
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// Screen offsets of the prompt's text area inside its bordered box.
const (
	promptBorderX = 1
	promptBorderY = 1
)

// cursor places the terminal's real cursor on whichever input has focus.
//
// This is what makes an input method work: Vietnamese Telex and every other
// composing IME anchor to the cursor the terminal reports, so a drawn-on cursor
// leaves them with no insertion point and the keystrokes arrive uncomposed.
func (m *Model) cursor() *tea.Cursor {
	if !m.ready {
		return nil
	}

	// An overlay owns the keyboard while it is up.
	switch m.overlay {
	case overlayFinder:
		return offsetCursor(m.finderIn.Cursor(), m.overlayX+overlayTextX, m.overlayY+overlayInputY)
	case overlayGrep:
		return offsetCursor(m.grepIn.Cursor(), m.overlayX+overlayTextX, m.overlayY+overlayInputY)
	case overlayNone:
	default:
		return nil // pickers and prompts take keys, not text
	}

	if m.finding {
		// The find bar sits on the preview pane's last content row.
		x := m.sideW + m.chatW + 1 + 1 // pane border, then the "/" prefix
		y := headerRows + m.bodyH - 2
		return offsetCursor(m.findIn.Cursor(), x, y)
	}

	if m.focus == focusInput {
		return offsetCursor(m.input.Cursor(),
			promptBorderX, headerRows+m.bodyH+promptBorderY)
	}
	return nil
}

// offsetCursor moves a component-relative cursor into screen coordinates.
func offsetCursor(c *tea.Cursor, dx, dy int) *tea.Cursor {
	if c == nil {
		return nil
	}
	c.Position.X += dx
	c.Position.Y += dy
	return c
}
