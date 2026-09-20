package ui

import (
	"os"
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
	// All motion rather than only motion with a button held: the history
	// browser's file rows underline as the pointer crosses them, which is the
	// only thing on a terminal that says a row can be clicked.
	//
	// Asking for the mouse at all takes the terminal's own drag-to-select
	// away, which is why there is a selection of our own in selection.go.
	v.MouseMode = tea.MouseModeAllMotion
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

	// The pane switches sit at the right end, where they stay put as the tab
	// strip grows. They are laid out from the column they will be drawn in, so
	// a click lands on the switch rather than near it.
	left := clipLine(sb.String(), m.w)
	gap := m.w - lipgloss.Width(left) - m.switchesWidth()
	if gap < 1 {
		m.toggles = m.toggles[:0]
		return left
	}
	return left + strings.Repeat(" ", gap) + m.paneSwitches(m.w-m.switchesWidth())
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
	chatBody := m.paintSelection(m.chat.View(), focusChat, m.chatW-2)
	cols = append(cols, m.pane(chatBody, chatTitle, m.chatW, m.bodyH, m.focus == focusChat))

	if m.btwW > 0 {
		body := m.paintSelection(m.btwPane(m.btwW-2), focusBtw, m.btwW-2)
		cols = append(cols, m.pane(body, m.btwTitle(), m.btwW, m.bodyH,
			m.focus == focusBtw))
	}
	if m.prevW > 0 {
		body := m.paintSelection(m.previewPane(), focusPreview, m.prevW-2)
		cols = append(cols, m.pane(body, m.previewTitle(), m.prevW, m.bodyH, m.focus == focusPreview))
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
		lines[0] = injectTitle(lines[0], ts.Render(" "+truncate(title, titleRoom(w))+" "))
	}
	return strings.Join(lines, "\n")
}

// titleRoom is how much of a title a pane of this width will draw. A title
// that knows the number can decide for itself what to give up; one that does
// not has its end cut off, and the end is where the part that changes lives.
func titleRoom(w int) int { return max(4, w-2-6) }

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

// previewTitle names the file and says what is true of the view of it.
//
// It fits itself rather than letting the pane cut its end off. What is at the
// end is what changes — the column the view starts at, whether the buffer is
// dirty — and what is at the start is a directory you already know you are in.
// So the path gives up its head, keeping the file name, and the rest stays.
func (m *Model) previewTitle() string {
	room := titleRoom(m.prevW)

	if m.edit != nil {
		suffix := "  editing"
		if m.edit.Dirty() {
			suffix += " ●"
		}
		return fitTitle(m.edit.rel, suffix, room)
	}
	if m.file == nil {
		return "preview"
	}
	// Say when the pane is not showing column one. Long lines are clipped
	// rather than wrapped, so without this the only sign that a line continues
	// is that it stops making sense at the right-hand edge.
	suffix := ""
	if x := m.prev.XOffset(); x > 0 {
		suffix += "  col " + strconv.Itoa(x+1)
	}
	if m.file.Truncated {
		suffix += "  (truncated)"
	}
	// The language is the first thing dropped when the pane is narrow: it is
	// the one part of the title you can also tell from the file name, and the
	// file name is the part that says which file this is. So it is kept only
	// while the name still fits whole beside it.
	want := max(minName, lipgloss.Width(vfs.Base(m.file.Rel)))
	if lang := m.file.Lang; lang != "" &&
		room-lipgloss.Width(suffix)-lipgloss.Width(lang)-2 >= want {
		suffix = "  " + lang + suffix
	}
	return fitTitle(m.file.Rel, suffix, room)
}

// minName is the least of a path worth keeping: below it a title says a file
// is open without saying which.
const minName = 12

// fitTitle keeps the suffix whole and takes the room out of the path's head.
func fitTitle(path, suffix string, room int) string {
	return truncateLeft(path, max(minName, room-lipgloss.Width(suffix))) + suffix
}

func (m *Model) previewPane() string {
	if m.edit != nil {
		return m.edit.ta.View()
	}
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

func (m *Model) inputBox() string {
	style, ts := m.st.Pane, m.st.Title
	if m.focus == focusInput {
		style, ts = m.st.PaneActive, m.st.TitleOn
	}
	body := m.input.View()
	if m.attachRows() > 0 {
		body = m.attachBar() + "\n" + body
	}

	// Where the session stands goes on the frame rather than on a line of its
	// own. A shell puts it next to the caret because that is where you are
	// looking when you type; the caret here is a textarea whose prompt repeats
	// on every row, so the border is the nearest place that says it once.
	box := style.Width(max(3, m.w)).Render(body)
	lines := strings.Split(box, "\n")
	if len(lines) > 0 {
		inner := max(1, m.w-2)
		lines[0] = injectTitle(lines[0],
			ts.Render(" "+truncateLeft(m.promptPath(), max(4, inner-6))+" "))
	}
	return strings.Join(lines, "\n")
}

// promptPath is where the session stands, written the way a shell prompt
// writes it: the home directory as ~, and the filesystem named when it is not
// this machine's, because `~/workspace` means two different places depending
// on which side of WSL you are on.
func (m *Model) promptPath() string {
	s := m.mgr.Active()
	fsys := m.sessionFS(s)
	dir := m.sessionCWD(s)

	home := ""
	if fsys.IsLocal() {
		home, _ = os.UserHomeDir()
	} else {
		home = fsys.DefaultDir()
	}
	if home != "" && home != "/" && vfs.Within(home, dir) {
		if rel := vfs.Rel(home, dir); rel != "" && rel != "." {
			dir = "~/" + filepath.ToSlash(rel)
		} else {
			dir = "~"
		}
	}
	if !fsys.IsLocal() {
		return fsys.Label() + "  " + dir
	}
	return dir
}

func (m *Model) statusBar() string {
	s := m.mgr.Active()
	left := make([]string, 0, 6)

	if s.Busy {
		// The way out is named while there is something to get out of. A turn
		// that has gone wrong is watched rather than stopped when the key that
		// stops it is not written anywhere on the screen.
		// How long it has been at it. A turn that has been thinking for seven
		// minutes and one that has been thinking for seven seconds read the
		// same without it, and only one of them is worth interrupting.
		line := m.spin.View() + " " + m.st.Accent.Render(orDefault(s.Status, "working"))
		if el := running(s.Started); el != "" {
			line += m.st.Dim.Render("  " + el)
		}
		left = append(left, line+m.st.Faint.Render("  esc to stop"))
	} else if m.errText != "" {
		left = append(left, m.st.Bad.Render("✗ "+truncate(m.errText, max(20, m.w/2))))
	} else if m.notice != "" {
		left = append(left, m.st.Good.Render(m.notice))
	} else {
		left = append(left, m.st.Dim.Render(m.hintFor(m.focus)))
	}

	// The model is what this slot has always shown, and it is now per-session,
	// so it has to be read from the session rather than from the config. An
	// engine that picks its own model shows its name instead, because ours
	// would be a claim about something it is not doing.
	engineLabel := agent.ModelFor(m.sessionModel(s)).Label
	if _, ignored := m.modelIgnored(s); ignored {
		if e := m.reg.Get(s.Engine); e != nil {
			engineLabel = e.Label()
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

func (m *Model) hintFor(f focus) string {
	switch f {
	case focusPreview:
		if m.edit != nil {
			return "editing · ctrl+s save · ctrl+z undo · esc close"
		}
		return "preview · e edit · / find · n next · w wrap · ctrl+o switch pane"
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

	if m.edit != nil && m.focus == focusPreview {
		return offsetCursor(m.edit.ta.Cursor(), m.sideW+m.chatW+1, headerRows+1)
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
