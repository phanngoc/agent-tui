package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/engine"
	"github.com/phanngoc/agent-tui/internal/session"
)

// The model belongs to the session, not to the app, for the same reason the
// engine and the mode do: a question worth Opus and a rename worth Haiku are
// both happening, and making one of them wait on the other's setting is the
// thing multiple sessions exist to avoid.
//
// Changing it does not reset the conversation. The model reads the transcript
// it is handed, so unlike switching engine — which hands the turn to a
// different program with its own server-side history — this one carries over.

// sessionModel is the model a session runs on, falling back to the configured
// default for a session saved before there was a choice.
func (m *Model) sessionModel(s *session.Session) string {
	if s.Model != "" {
		return s.Model
	}
	if m.cfg.Model != "" {
		return m.cfg.Model
	}
	return agent.DefaultModel
}

// setModel records a model against the active session.
func (m *Model) setModel(spec agent.Model) {
	s := m.mgr.Active()
	if m.sessionModel(s) == spec.ID {
		m.notice = "already on " + spec.Label
		return
	}
	s.Model = spec.ID
	m.mgr.Save(s)
	m.invalidateChat()

	note := "this session now runs on " + spec.Label
	// An engine that answers from its own binary does not take our model, and
	// silently ignoring the choice would be worse than saying so.
	if why, ok := m.modelIgnored(s); ok {
		note += " — but " + why
	}
	m.notice = note
}

// modelIgnored explains when the session's engine will not use the chosen
// model. Only the built-in agent and Claude Code speak Claude model ids; the
// other CLIs drive different providers entirely and are configured their own
// way.
func (m *Model) modelIgnored(s *session.Session) (string, bool) {
	switch s.Engine {
	case "", engine.IDAPI, engine.IDClaude:
		return "", false
	}
	label := s.Engine
	if e := m.reg.Get(s.Engine); e != nil {
		label = e.Label()
	}
	return label + " chooses its own model; switch engine for this to take effect", true
}

// setModelByName switches model without opening the picker.
func (m *Model) setModelByName(name string) tea.Cmd {
	spec, ok := agent.ResolveModel(name)
	if !ok {
		m.notice = "no model called " + name + " — try " + strings.Join(modelNames(), ", ")
		return nil
	}
	m.setModel(spec)
	return nil
}

func modelNames() []string {
	out := make([]string, len(agent.Models))
	for i, spec := range agent.Models {
		out[i] = spec.Label
	}
	return out
}

// modelRows is the picker's list: the catalogue, plus whatever the session is
// already on when that is something else. A model set in the config file has
// to be visible, or the picker would claim the session is on a model it is not.
func (m *Model) modelRows() []agent.Model {
	rows := append([]agent.Model(nil), agent.Models...)
	cur := m.sessionModel(m.mgr.Active())
	for _, spec := range rows {
		if spec.ID == cur {
			return rows
		}
	}
	return append(rows, agent.Model{
		ID: cur, Label: cur, Note: "from your configuration", Effort: true, Adaptive: true,
	})
}

// openModelPicker selects the row the session is already on, so the list opens
// where the user is rather than at the top.
func (m *Model) openModelPicker() {
	m.overlay = overlayModel
	cur := m.sessionModel(m.mgr.Active())
	m.modelSel = 0
	for i, spec := range m.modelRows() {
		if spec.ID == cur {
			m.modelSel = i
		}
	}
}

func (m *Model) modelKey(key string) tea.Cmd {
	rows := m.modelRows()
	switch key {
	case "down", "ctrl+n", "j":
		m.modelSel = min(len(rows)-1, m.modelSel+1)
	case "up", "ctrl+p", "k":
		m.modelSel = max(0, m.modelSel-1)
	case "enter":
		if m.modelSel < len(rows) {
			m.setModel(rows[m.modelSel])
		}
		m.overlay = overlayNone
	}
	return nil
}

func (m *Model) modelView() string {
	w := clamp(m.w*3/5, 46, 92)
	inner := w - 2

	var b strings.Builder
	b.WriteString(m.st.Accent.Render("  Model for this session") + "\n\n")

	cur := m.sessionModel(m.mgr.Active())
	for i, spec := range m.modelRows() {
		mark, name := "  ", m.st.Dim.Render(spec.Label)
		if spec.ID == cur {
			mark, name = m.st.Good.Render(" ✓"), m.st.Bold.Render(spec.Label)
		}
		row := mark + " " + name + "  " + m.st.Faint.Render(truncate(spec.Note, inner-22))
		if i == m.modelSel {
			row = m.st.SelRow.Render(padRight(" ▸ "+stripANSI(spec.Label+"  "+spec.Note), inner))
		}
		b.WriteString(row + "\n")
	}

	if why, ok := m.modelIgnored(m.mgr.Active()); ok {
		b.WriteString("\n  " + m.st.Warn.Render(truncate(why, inner-2)) + "\n")
	}
	b.WriteString("\n  " + m.st.Faint.Render("enter select · ↑↓ move · esc cancel"))
	return m.st.Overlay.Width(w).Render(b.String())
}
