package ui

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/theme"
)

// transcript renders the whole conversation as Warp-style blocks: a coloured
// rail down the left, a header, then wrapped body text.
//
// Committed messages and the streaming tail are cached separately. A streaming
// turn emits a text delta every few milliseconds, and re-wrapping a long
// session on each one would dominate the update loop; splitting the cache makes
// a delta cost only what the unfinished turn costs.
func (m *Model) transcript(width int) string {
	s := m.mgr.Active()

	key := s.ID + "|" + strconv.Itoa(len(s.Messages)) + "|" +
		strconv.Itoa(width) + "|" + toolStateKey(s)
	if key != m.chatKey {
		m.chatCache = m.renderHead(width)
		m.chatKey = key
	}
	if s.Partial == "" && s.LastErr == "" {
		return m.chatCache
	}

	var b strings.Builder
	b.Grow(len(m.chatCache) + len(s.Partial) + 256)
	b.WriteString(m.chatCache)
	if s.Partial != "" {
		if len(s.Messages) > 0 {
			b.WriteByte('\n')
		}
		m.block(&b, m.st.AgentBar, m.st.AgentTag.Render("agent"), "", s.Partial, width)
	}
	if s.LastErr != "" {
		b.WriteByte('\n')
		m.block(&b, m.st.Bad, m.st.ErrTag.Render("error"), "", s.LastErr, width)
	}
	return b.String()
}

// renderHead renders every committed message. Its result only changes when a
// turn completes or a tool call finishes.
func (m *Model) renderHead(width int) string {
	s := m.mgr.Active()

	var b strings.Builder
	b.Grow(1024 + len(s.Messages)*160)

	if len(s.Messages) == 0 {
		b.WriteString(m.welcome(width))
	}
	for i := range s.Messages {
		if i > 0 {
			b.WriteByte('\n')
		}
		m.renderMessage(&b, &s.Messages[i], width)
	}
	return b.String()
}

// toolStateKey changes whenever a tool call flips from running to done, so the
// cache invalidates exactly when the transcript actually changes.
func toolStateKey(s *session.Session) string {
	var done, total int
	for i := range s.Messages {
		for _, t := range s.Messages[i].Tools {
			total++
			if t.Done {
				done++
			}
		}
	}
	return strconv.Itoa(done) + "/" + strconv.Itoa(total)
}

func (m *Model) renderMessage(b *strings.Builder, msg *session.Message, width int) {
	stamp := m.st.Faint.Render(msg.At.Format("15:04"))

	if msg.Role == session.RoleUser {
		m.block(b, m.st.UserBar, m.st.UserTag.Render("you"), stamp, msg.Text, width)
		return
	}

	head := m.st.AgentTag.Render("agent")
	m.block(b, m.st.AgentBar, head, stamp, msg.Text, width)

	for _, t := range msg.Tools {
		m.renderTool(b, t, width)
	}
	if msg.Err != "" {
		m.block(b, m.st.Bad, m.st.ErrTag.Render("error"), "", msg.Err, width)
	}
}

// renderTool draws one tool call as a compact single line plus, for failures,
// the first few lines of output.
func (m *Model) renderTool(b *strings.Builder, t session.ToolCall, width int) {
	icon, style := "⋯", m.st.Dim
	switch {
	case t.Chosen != "":
		icon, style = "◆", m.st.Accent
	case t.Denied:
		icon, style = "✗", m.st.Bad
	case t.Done && t.IsError:
		icon, style = "!", m.st.Bad
	case t.Done:
		icon, style = "✓", m.st.Good
	}

	line := "  " + style.Render(icon) + " " + m.st.ToolTag.Render(t.Name)
	if sum := t.Summary(); sum != "" {
		line += "  " + m.st.Dim.Render(truncate(sum, max(10, width-len(t.Name)-14)))
	}
	if t.Done && t.Elapsed > 0 {
		line += "  " + m.st.Faint.Render(shortDur(t.Elapsed))
	}
	b.WriteString(line)
	b.WriteByte('\n')

	if t.Done && (t.IsError || t.Denied) && t.Result != "" {
		for i, l := range strings.Split(t.Result, "\n") {
			if i >= 4 {
				b.WriteString("    " + m.st.Faint.Render("…") + "\n")
				break
			}
			if strings.TrimSpace(l) == "" {
				continue
			}
			b.WriteString("    " + m.st.Faint.Render(truncate(l, max(10, width-6))) + "\n")
		}
	}
}

// block writes a rail-prefixed paragraph.
func (m *Model) block(b *strings.Builder, rail lipgloss.Style, tag, stamp, body string, width int) {
	bar := rail.Render("▎")
	head := bar + " " + tag
	if stamp != "" {
		head += "  " + stamp
	}
	b.WriteString(head)
	b.WriteByte('\n')

	if strings.TrimSpace(body) == "" {
		return
	}
	wrapped := lipgloss.Wrap(body, max(10, width-2), " ")
	for _, l := range strings.Split(wrapped, "\n") {
		// Styled explicitly rather than left to the terminal default, which is
		// what made message text wash out against a dark background.
		b.WriteString(bar + " " + m.st.Body.Render(l) + "\n")
	}
}

func (m *Model) welcome(width int) string {
	st := m.st
	rows := []string{
		st.Accent.Render("agent-tui") + st.Dim.Render("  ·  "+m.projectName()),
		"",
		st.Dim.Render("Ask a question, or start with a shortcut:"),
		"",
		kv(st, "ctrl+p", "fuzzy-find a file"),
		kv(st, "ctrl+f", "search file contents"),
		kv(st, "/new", "start a session  ·  /fork to branch this one"),
		kv(st, "ctrl+t", "the same, as a shortcut"),
		kv(st, "tab", "move between panes"),
		kv(st, "f1", "all shortcuts"),
	}
	_ = width
	return strings.Join(rows, "\n") + "\n"
}

func kv(st *theme.Styles, k, v string) string {
	return "  " + st.StatusKey.Render(" "+k+" ") + " " + st.Dim.Render(v)
}

func truncate(s string, w int) string {
	if w <= 1 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	if len(r) > w-1 {
		r = r[:w-1]
	}
	return string(r) + "…"
}

func shortDur(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return "<1ms"
	case d < time.Second:
		return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
	default:
		return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + "s"
	}
}
