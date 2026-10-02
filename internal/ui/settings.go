package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/config"
)

// Where a new session begins.
//
// It used to be one answer: the folder agent-tui was opened in. That is right
// when you cd into a project and type tui, and wrong when the terminal always
// opens in your home directory, or when the work is in one project all week
// and every session starts with the same /cd. So it is a setting, with three
// answers, and the page that sets it is /settings.
//
// The same setting answers two questions — which folder the app opens on, and
// which folder a session made with ctrl+t starts in — because to the reader
// they are one question: where does a new conversation begin.

type startChoice struct{ id, label string }

var startChoices = []startChoice{
	{config.StartLaunch, "the folder tui was opened in"},
	{config.StartLast, "where the last session left off"},
	{config.StartFixed, "this folder"},
}

// newSession starts an empty session in the folder the settings say, and
// switches to it.
func (m *Model) newSession() tea.Cmd {
	from := m.mgr.Active()
	s := m.mgr.New()
	s.Engine = m.lastEngine

	note := "new session"
	switch m.prefs.StartMode() {
	case config.StartLast:
		// The session you were just in is the last one. Its target comes too:
		// a directory inside a container is not a directory on the host.
		if from != nil && from != s {
			s.Target, s.CWD = from.Target, m.sessionCWD(from)
		}
	case config.StartFixed:
		if dir, err := config.ExpandDir(m.prefs.StartPath); err == nil && config.IsDir(dir) {
			s.CWD = dir
		} else {
			note = "new session — the start folder in /settings is not a folder, so it starts here"
		}
	}
	cmd := m.onSessionSwitch()
	m.notice = note
	return cmd
}

func (m *Model) openSettings() {
	m.overlay = overlaySettings
	m.setEditing, m.setErr = false, ""
	m.setSel = 0
	for i, c := range startChoices {
		if c.id == m.prefs.StartMode() {
			m.setSel = i
		}
	}
	m.setIn.SetValue(m.prefs.StartPath)
	m.setIn.Blur()
}

func (m *Model) settingsKey(k tea.KeyPressMsg) tea.Cmd {
	if m.setEditing {
		switch k.String() {
		case "enter":
			m.chooseStartFolder()
			return nil
		case "esc":
			m.setEditing, m.setErr = false, ""
			m.setIn.Blur()
			return nil
		}
		var cmd tea.Cmd
		m.setIn, cmd = m.setIn.Update(k)
		m.setErr = ""
		return cmd
	}

	switch k.String() {
	case "down", "ctrl+n", "j":
		m.setSel = min(len(startChoices)-1, m.setSel+1)
	case "up", "ctrl+p", "k":
		m.setSel = max(0, m.setSel-1)
	case "enter", "space":
		c := startChoices[m.setSel]
		if c.id != config.StartFixed {
			if m.setStart(c.id, m.prefs.StartPath) {
				m.closeOverlay()
			}
			return nil
		}
		// A folder has to be typed before it can be chosen. Offer the one this
		// session is in: it is usually the one you meant.
		if strings.TrimSpace(m.setIn.Value()) == "" {
			m.setIn.SetValue(m.sessionCWD(m.mgr.Active()))
		}
		m.setEditing = true
		m.setIn.Focus()
		m.setIn.CursorEnd()
	case "esc", "q":
		m.closeOverlay()
	}
	return nil
}

// chooseStartFolder accepts the typed folder, or says why it cannot.
func (m *Model) chooseStartFolder() {
	typed := strings.TrimSpace(m.setIn.Value())
	dir, err := config.ExpandDir(typed)
	if err != nil || !config.IsDir(dir) {
		m.setErr = "not a folder: " + orDefault(typed, "(empty)")
		return
	}
	m.setIn.SetValue(dir)
	if m.setStart(config.StartFixed, dir) {
		m.closeOverlay()
	}
}

// setStart saves the choice. It reads the file again first, because the copy
// held here may be older than what another window has written since.
func (m *Model) setStart(mode, path string) bool {
	p := config.LoadPrefs()
	p.StartDir, p.StartPath = mode, path
	if err := config.SavePrefs(p); err != nil {
		m.setErr = "could not save: " + err.Error()
		return false
	}
	m.prefs = p
	m.notice = "new sessions start in " + m.startDetail(mode)
	return true
}

// startDetail says, concretely, where a choice leads.
func (m *Model) startDetail(id string) string {
	switch id {
	case config.StartLast:
		if m.prefs.LastRoot != "" {
			return "the session you are leaving · on startup " + m.prefs.LastRoot
		}
		return "the session you are leaving"
	case config.StartFixed:
		if p := strings.TrimSpace(m.prefs.StartPath); p != "" {
			return p
		}
		return "type a folder"
	}
	return m.hostRoot()
}

// settingsInputRow is the line the folder input sits on, inside the border.
// It is a constant so the cursor can be placed without measuring the page.
const settingsInputRow = 6

// settingsInputPrefix is what is drawn before the input on its line.
const settingsInputPrefix = "     › "

func (m *Model) settingsView() string {
	w := clamp(m.w*3/5, 52, 96)
	inner := w - 2

	var b strings.Builder
	b.WriteString(m.st.Accent.Render("  Settings") + "\n\n")
	b.WriteString(gutter + m.st.Bold.Render(" New sessions start in") + "\n")

	current := m.prefs.StartMode()
	for i, c := range startChoices {
		mark := "  "
		if c.id == current {
			mark = m.st.Good.Render(" ✓")
		}
		label := padRight(c.label, 33)
		room := max(8, inner-len(label)-6)
		detail := truncateLeft(m.startDetail(c.id), room)
		row := mark + "  " + m.st.Dim.Render(label) + " " + m.st.Faint.Render(detail)
		if i == m.setSel && !m.setEditing {
			row = m.st.SelRow.Render(padRight(" ▸  "+label+" "+detail, inner))
		}
		b.WriteString(row + "\n")
	}

	// Always drawn, so the rows below it do not jump when editing starts and
	// the cursor's row is fixed.
	if m.setEditing {
		m.setIn.SetWidth(max(10, inner-lipgloss.Width(settingsInputPrefix)-1))
		b.WriteString(m.st.Accent.Render(settingsInputPrefix) + m.setIn.View() + "\n")
	} else {
		b.WriteString("\n")
	}
	if m.setErr != "" {
		b.WriteString("     " + m.st.Bad.Render(truncate(m.setErr, inner-6)) + "\n")
	} else {
		b.WriteString("\n")
	}

	hint := "enter choose · ↑↓ move · esc close"
	if m.setEditing {
		hint = "enter save · ~ is your home · esc back"
	}
	b.WriteString(gutter + m.st.Faint.Render(" "+hint) + "\n")
	b.WriteString(gutter + m.st.Faint.Render(" tui -C <dir> still opens <dir>, whatever this says"))
	return m.st.Overlay.Width(w).Render(b.String())
}

// settingsCursor places the real cursor on the folder input while it is being
// typed into, so an input method has somewhere to compose.
func (m *Model) settingsCursor() *tea.Cursor {
	if !m.setEditing {
		return nil
	}
	return offsetCursor(m.setIn.Cursor(),
		m.overlayX+1+lipgloss.Width(settingsInputPrefix), m.overlayY+1+settingsInputRow)
}
