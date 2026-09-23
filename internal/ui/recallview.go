package ui

import (
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// The list of conversations a search found.
//
// It borrows the project search's shape — a foldable group per source, the
// matching line with the match picked out — because it is the same reading
// task, and a second vocabulary for it would be one to learn for nothing. What
// differs is what a group is and what a row says: a conversation rather than a
// file, and who said it rather than which line it was on.

func (m *Model) recallView() string {
	w := m.overlayWidth()
	inner := w - 2

	var b strings.Builder
	b.WriteString(m.st.Accent.Render("  Search every conversation") + "\n")
	b.WriteString("  " + m.recallIn.View() + "\n")
	b.WriteString("  " + m.recallToggles() + "\n\n")
	b.WriteString("  " + m.recallSummary() + "\n\n")
	// Where the list begins, recorded as the view goes.
	m.recallBodyY = strings.Count(b.String(), "\n")
	b.WriteString(m.recallList(inner))
	b.WriteString("\n  " + m.st.Faint.Render(
		"enter open · ←→ fold · alt+a case · alt+r regex · esc close"))
	return m.st.Overlay.Width(w).Render(b.String())
}

func (m *Model) recallToggles() string {
	mark := func(on bool, label string) string {
		if on {
			return m.st.Accent.Render("[" + label + "]")
		}
		return m.st.Faint.Render("[" + label + "]")
	}
	return mark(m.recallCase, "Aa") + " " + mark(m.recallRegex, ".*")
}

func (m *Model) recallSummary() string {
	switch {
	case m.recallBusy:
		return m.st.Faint.Render("reading conversations…")
	case m.recallRes.err != nil:
		return m.st.Bad.Render(m.recallRes.err.Error())
	case strings.TrimSpace(m.recallIn.Value()) == "":
		return m.st.Faint.Render(plural(len(m.corpus), "conversation") + " to search")
	case m.recallRes.total == 0:
		return m.st.Faint.Render("nothing found")
	}
	projects := map[string]bool{}
	for _, c := range m.recallRes.convs {
		projects[filepath.Base(c.entry.Root)] = true
	}
	return m.st.Dim.Render(plural(m.recallRes.total, "result") + " in " +
		plural(len(m.recallRes.convs), "conversation") + " across " +
		plural(len(projects), "project"))
}

// recallRows is how many rows of the list fit, leaving room for the box, the
// input, the toggles, the summary and the footer.
func (m *Model) recallHeight() int { return clamp(m.h/2, 5, 18) }

func (m *Model) recallList(inner int) string {
	m.recallDrawn = 0
	if len(m.recallRows) == 0 {
		return ""
	}
	limit := m.recallHeight()
	// Keep the selection in view, the same arithmetic the project search uses.
	if m.recallSel < m.recallTop {
		m.recallTop = m.recallSel
	}
	if m.recallSel >= m.recallTop+limit {
		m.recallTop = m.recallSel - limit + 1
	}
	m.recallTop = clamp(m.recallTop, 0, max(0, len(m.recallRows)-limit))

	var b strings.Builder
	for i := m.recallTop; i < len(m.recallRows) && i < m.recallTop+limit; i++ {
		r := m.recallRows[i]
		c := m.recallRes.convs[r.conv]
		line := m.recallHeader(c, inner)
		if r.hit >= 0 {
			line = m.recallHit(c.hits[r.hit], inner)
		}
		if i == m.recallSel {
			line = m.st.SelRow.Render(padRight(" "+stripANSI(line), inner))
		}
		m.recallDrawn++
		b.WriteString(line + "\n")
	}
	return b.String()
}

// recallHeader names the conversation: what it was called, which project it
// belongs to, and when it last moved. The project is always shown — it is the
// one column that says this is not the file search.
func (m *Model) recallHeader(c convoConv, inner int) string {
	fold := "▾ "
	if c.collapsed {
		fold = "▸ "
	}
	// A live conversation may hold messages the disk has never seen, and
	// "these results include what you have not saved" is a real difference.
	mark := ""
	if c.entry.Live {
		mark = m.st.Accent.Render("● ")
	}
	count := m.st.Warn.Render(strconv.Itoa(len(c.hits)))
	project := filepath.Base(c.entry.Root)
	when := relTime(c.entry.Updated)

	tail := m.st.Faint.Render(project+"  "+when+"  ") + count
	room := max(8, inner-lipgloss.Width(stripANSI(fold+mark))-lipgloss.Width(stripANSI(tail))-3)
	title := m.st.Bold.Render(truncate(orDefault(c.entry.Title, "untitled"), room))

	gap := max(1, inner-lipgloss.Width(stripANSI(fold+mark+title+tail))-1)
	return m.st.Faint.Render(fold) + mark + title + strings.Repeat(" ", gap) + tail
}

// recallHit is one matching line, tagged with who said it. The transcript
// labels messages with the same two styles, so the list and the place it opens
// speak the same vocabulary.
func (m *Model) recallHit(h convoHit, inner int) string {
	who, style := "you  ", m.st.UserTag
	if h.role != "user" {
		who, style = "agent", m.st.AgentTag
	}
	room := max(12, inner-12)
	text, start, end := windowAround(h.Text, h.Start, h.End, room)
	body := m.st.Dim.Render(text[:start]) +
		m.st.Match.Render(text[start:end]) +
		m.st.Dim.Render(text[end:])
	return "   " + style.Render(who) + " " + body
}
