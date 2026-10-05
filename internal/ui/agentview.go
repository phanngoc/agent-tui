package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/session"
)

// Sub-agents in the transcript, the way Claude Code shows them: the agent's
// type and what it was asked to do, then what it is doing now with what it
// has used so far, and its last few calls; once it is done, one line of what
// it found. Agents it started in turn sit under it, indented.
//
//	▎  ● Explore  Find the Go module name
//	▎    ⎿ Reading go.mod · 4 tool uses · 21.2k tokens · 9s
//	▎      ✓ Glob **/go.mod
//	▎      ⋯ Read go.mod

// agentCallsShown is how many of a running agent's latest calls are listed.
const agentCallsShown = 3

func (m *Model) renderAgent(b *strings.Builder, t session.ToolCall, width int) {
	bar := m.st.AgentBar.Render("▎")
	m.agentBlock(b, bar, "  ", t.Agent, width, 0)
}

func (m *Model) agentBlock(b *strings.Builder, bar, indent string, a *session.SubAgent, width, depth int) {
	icon, style := "●", m.st.Accent
	switch a.State {
	case "done":
		icon, style = "✓", m.st.Good
	case "failed":
		icon, style = "!", m.st.Bad
	case "stopped":
		icon, style = "■", m.st.Dim
	}
	stats := agentStats(a)
	head := bar + indent + style.Render(icon) + " " + m.st.ToolTag.Render(firstNonBlank(a.Type, "agent"))
	tail := ""
	if !a.Running() && stats != "" {
		tail = "  " + m.st.Faint.Render(stats)
	}
	room := width - lipgloss.Width(head) - lipgloss.Width(tail) - 2
	b.WriteString(clipLine(head+"  "+m.st.Dim.Render(truncate(a.Description, max(8, room)))+tail, width))
	b.WriteByte('\n')

	elbow := bar + indent + "  " + m.st.Faint.Render("⎿ ")
	sub := bar + indent + "    "
	if a.Running() {
		what := firstNonBlank(a.Activity, "starting…")
		if a.State == "starting" && a.Activity == "" {
			what = "starting…"
		}
		line := m.st.Body.Render(what)
		if stats != "" {
			line += m.st.Faint.Render(" · " + stats)
		}
		b.WriteString(clipLine(elbow+line, width))
		b.WriteByte('\n')
		calls := a.Calls
		if len(calls) > agentCallsShown {
			calls = calls[len(calls)-agentCallsShown:]
		}
		for _, c := range calls {
			if c.Agent != nil && depth < 4 {
				m.agentBlock(b, bar, indent+"    ", c.Agent, width, depth+1)
				continue
			}
			mark, ms := "⋯", m.st.Dim
			if c.Done && c.IsError {
				mark, ms = "!", m.st.Bad
			} else if c.Done {
				mark, ms = "✓", m.st.Good
			}
			line := sub + ms.Render(mark) + " " + m.st.Dim.Render(c.Name)
			if s := c.Summary(); s != "" {
				line += " " + m.st.Faint.Render(s)
			}
			b.WriteString(clipLine(line, width))
			b.WriteByte('\n')
		}
		return
	}
	// Finished: its report, in a line; agents under it, each in its own.
	for _, c := range a.Calls {
		if c.Agent != nil && depth < 4 {
			m.agentBlock(b, bar, indent+"    ", c.Agent, width, depth+1)
		}
	}
	if r := firstLine(a.Summary); r != "" {
		b.WriteString(clipLine(elbow+m.st.Dim.Render(r), width))
		b.WriteByte('\n')
	}
}

// agentStats is "4 tool uses · 21.2k tokens · 9s", leaving out what is not
// known yet.
func agentStats(a *session.SubAgent) string {
	var parts []string
	n := a.ToolUses
	if n == 0 {
		n = len(a.Calls)
	}
	if n > 0 {
		parts = append(parts, plural(n, "tool use"))
	}
	if a.Tokens > 0 {
		parts = append(parts, compactTokens(a.Tokens)+" tokens")
	}
	d := a.Duration
	if a.Running() && !a.Started.IsZero() {
		d = max(d, time.Since(a.Started))
	}
	if d >= time.Second {
		parts = append(parts, shortDur(d.Round(time.Second)))
	}
	return strings.Join(parts, " · ")
}

func compactTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "*#`"))
		if l != "" {
			return l
		}
	}
	return ""
}

// runningAgents counts the sub-agents still at work in a session.
func runningAgents(s *session.Session) int {
	n := 0
	var walk func(a *session.SubAgent)
	walk = func(a *session.SubAgent) {
		if a.Running() {
			n++
		}
		for _, c := range a.Calls {
			if c.Agent != nil {
				walk(c.Agent)
			}
		}
	}
	for i := len(s.Messages) - 1; i >= 0 && i >= len(s.Messages)-6; i-- {
		for _, t := range s.Messages[i].Tools {
			if t.Agent != nil {
				walk(t.Agent)
			}
		}
	}
	return n
}
