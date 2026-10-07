package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/session"
)

// Sub-agents in the transcript, the way Claude Code shows them.
//
// Folded, which is how they start, an agent is two lines however much it has
// done: its type, its task and what it has used, then what it is doing now —
// and, while it works, the last thing it said, which is the nearest the CLI
// comes to streaming one. Agents started together are drawn as one group,
// a row each, so six of them take thirteen lines and not sixty:
//
//	▎  ● Running 2 agents · 62.4k tokens  ·  alt+o expands
//	▎    ├─ ● Explore  Find the Go module name · 4 tool uses · 21.2k tokens · 9s
//	▎    │    ⎿ Reading go.mod
//	▎    └─ ✓ general-purpose  Count the .go files · 9 tool uses · 41.2k tokens · 31s
//	▎         ⎿ There are 21 Go files in internal/engine.
//
// alt+o, the key that brings back folded calls, opens them: every call each
// agent made, its words, and more of its report. Agents an agent started sit
// under it, indented.

// agentReportRows is how much of a finished agent's report an opened one shows.
const agentReportRows = 6

// renderAgent draws one Agent call that is not part of a group.
func (m *Model) renderAgent(b *strings.Builder, t session.ToolCall, width int) {
	bar := m.st.AgentBar.Render("▎")
	if m.showAllCalls {
		m.agentBlock(b, bar, "  ", t.Agent, width, 0)
		return
	}
	m.agentCompact(b, bar, "  ", "    ", t.Agent, width, 0, true)
}

// renderAgentGroup draws Agent calls made side by side as one block.
func (m *Model) renderAgentGroup(b *strings.Builder, calls []session.ToolCall, width int) {
	bar := m.st.AgentBar.Render("▎")
	var running, failed int
	var tokens int64
	for _, c := range calls {
		if c.Agent.Running() {
			running++
		} else if c.Agent.State == "failed" {
			failed++
		}
		tokens += c.Agent.Tokens
	}
	icon, style := "●", m.st.Accent
	head := fmt.Sprintf("Running %d agents", len(calls))
	switch {
	case running > 0 && running < len(calls):
		head = fmt.Sprintf("Running %d of %d agents", running, len(calls))
	case running == 0:
		icon, style = "✓", m.st.Good
		head = fmt.Sprintf("%d agents finished", len(calls))
	}
	if failed > 0 {
		head += fmt.Sprintf(" · %d failed", failed)
	}
	if tokens > 0 {
		head += " · " + compactTokens(tokens) + " tokens"
	}
	hint := "alt+o expands"
	if m.showAllCalls {
		hint = "alt+o folds"
	}
	b.WriteString(clipLine(bar+"  "+style.Render(icon)+" "+m.st.Body.Render(head)+
		m.st.Faint.Render("  ·  "+hint), width))
	b.WriteByte('\n')

	for i, c := range calls {
		if m.showAllCalls {
			m.agentBlock(b, bar, "    ", c.Agent, width, 0)
			continue
		}
		lead, cont := "    ├─ ", "    │    "
		if i == len(calls)-1 {
			lead, cont = "    └─ ", "         "
		}
		m.agentCompact(b, bar, m.st.Faint.Render(lead), m.st.Faint.Render(cont), c.Agent, width, 0, false)
	}
}

// agentHead is an agent's first line: how it stands, its type, its task, and
// what it has used.
func (m *Model) agentHead(lead string, a *session.SubAgent, width int) string {
	icon, style := "●", m.st.Accent
	switch a.State {
	case "done":
		icon, style = "✓", m.st.Good
	case "failed":
		icon, style = "!", m.st.Bad
	case "stopped":
		icon, style = "■", m.st.Dim
	}
	head := lead + style.Render(icon) + " " + m.st.ToolTag.Render(firstNonBlank(a.Type, "agent"))
	tail := ""
	if stats := agentStats(a); stats != "" {
		tail = m.st.Faint.Render(" · " + stats)
	}
	// The stats are kept whole and the task gives way: how far along it is
	// matters more, line by line, than the rest of a task already on screen.
	room := width - lipgloss.Width(head) - lipgloss.Width(tail) - 2
	return clipLine(head+"  "+m.st.Dim.Render(truncate(agentTask(a), max(8, room)))+tail, width)
}

// agentTask is what an agent was asked to do, in a line.
func agentTask(a *session.SubAgent) string {
	return firstNonBlank(a.Description, firstLine(a.Prompt), "sub-agent")
}

// agentNow is an agent's second line: what it is doing, or how it ended.
func agentNow(a *session.SubAgent) string {
	if a.Running() {
		return firstNonBlank(a.Activity, firstLine(a.Summary), "starting…")
	}
	if r := firstLine(a.Summary); r != "" {
		return r
	}
	return a.State
}

// agentCompact draws an agent folded: its head, what it is doing now, with
// words the last thing it said, and the agents under it still at work. lead
// goes before the head and cont before each line under it.
func (m *Model) agentCompact(b *strings.Builder, bar, lead, cont string, a *session.SubAgent, width, depth int, words bool) {
	b.WriteString(m.agentHead(bar+lead, a, width))
	b.WriteByte('\n')
	now := agentNow(a)
	style := m.st.Dim
	if a.Running() {
		style = m.st.Body
	} else if a.State == "failed" {
		style = m.st.Bad
	}
	b.WriteString(clipLine(bar+cont+m.st.Faint.Render("⎿ ")+style.Render(truncate(now, max(8, width-lipgloss.Width(bar+cont)-3))), width))
	b.WriteByte('\n')
	if words && a.Running() {
		if said := firstLine(a.Summary); said != "" && said != now {
			b.WriteString(clipLine(bar+cont+"  "+m.st.Faint.Italic(true).Render(truncate(said, max(8, width-lipgloss.Width(bar+cont)-3))), width))
			b.WriteByte('\n')
		}
	}
	if depth >= 4 {
		return
	}
	for _, c := range a.Calls {
		if c.Agent != nil && c.Agent.Running() {
			m.agentCompact(b, bar, cont+m.st.Faint.Render("↳ "), cont+"  ", c.Agent, width, depth+1, false)
		}
	}
}

// agentBlock draws an agent opened: every call it made, with the agents
// those started nested under them, and what it said or reported.
func (m *Model) agentBlock(b *strings.Builder, bar, indent string, a *session.SubAgent, width, depth int) {
	b.WriteString(m.agentHead(bar+indent, a, width))
	b.WriteByte('\n')

	elbow := bar + indent + "  " + m.st.Faint.Render("⎿ ")
	sub := bar + indent + "    "
	room := max(8, width-lipgloss.Width(sub))
	if a.Running() {
		b.WriteString(clipLine(elbow+m.st.Body.Render(agentNow(a)), width))
		b.WriteByte('\n')
	}
	for _, c := range a.Calls {
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
	if a.Running() {
		if said := firstLine(a.Summary); said != "" {
			b.WriteString(clipLine(sub+m.st.Faint.Italic(true).Render(truncate(said, room)), width))
			b.WriteByte('\n')
		}
		return
	}
	// Finished: the head of its report.
	n := 0
	for _, l := range strings.Split(a.Summary, "\n") {
		l = strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "*#`"))
		if l == "" {
			continue
		}
		if n == agentReportRows {
			b.WriteString(sub + m.st.Faint.Render("…") + "\n")
			break
		}
		pre := sub
		if n == 0 {
			pre = elbow
		}
		b.WriteString(clipLine(pre+m.st.Dim.Render(truncate(l, room)), width))
		b.WriteByte('\n')
		n++
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
