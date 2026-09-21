package ui

import (
	"encoding/json"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/highlight"
	"github.com/phanngoc/agent-tui/internal/session"
)

// What an edit actually changed, under the call that made it.
//
// "✓ edit_file chat.go edited (1 replacement)" says a file was written and
// nothing about what is now in it, which is the only part worth reading: an
// agent that edits the wrong line reports exactly the same success as one that
// edits the right one. The lines it removed and the lines it put there are in
// the call's own arguments, so they cost nothing to show.

// editRows is how much of an edit is shown before it is cut short. Split
// between the two sides, so a replacement shows some of what went and some of
// what came. alt+o shows all of it, the same key that unfolds the calls.
const editRows = 8

// editOf pulls the before and after out of a call's arguments.
//
// The spellings differ between the built-in tools and the CLIs they stand in
// for — edit_file and Edit, write_file and Write — and are matched the same
// way Summary matches them, case-insensitively and by both names.
func editOf(t session.ToolCall) (before, after []string, ok bool) {
	if !t.Done || t.IsError || t.Denied || len(t.Input) == 0 {
		return nil, nil, false
	}
	var in struct {
		Old     string `json:"old_string"`
		New     string `json:"new_string"`
		Content string `json:"content"`
	}
	if json.Unmarshal(t.Input, &in) != nil {
		return nil, nil, false
	}

	switch strings.ToLower(t.Name) {
	case "edit_file", "edit":
		if in.Old == "" && in.New == "" {
			return nil, nil, false
		}
		return splitEdit(in.Old), splitEdit(in.New), true
	case "write_file", "write":
		if in.Content == "" {
			return nil, nil, false
		}
		// A write has no before: whatever was there is gone, and the file is
		// now what is in the call.
		return nil, splitEdit(in.Content), true
	}
	return nil, nil, false
}

// splitEdit turns one side of an edit into lines, without the empty one a
// trailing newline leaves behind.
func splitEdit(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// renderEdit draws the change under the call. all lifts the cap, for the same
// reason and by the same key as unfolding the calls themselves.
func (m *Model) renderEdit(b *strings.Builder, t session.ToolCall, width int, all bool) {
	before, after, ok := editOf(t)
	if !ok {
		return
	}

	// The two sides share the budget, so a replacement shows both of them
	// rather than spending the whole allowance on whichever came first.
	room := editRows
	if all {
		room = len(before) + len(after)
	}
	share := max(1, room/2)
	if len(before) == 0 || len(after) == 0 {
		share = room // only one side to spend it on
	}

	m.editSide(b, before, min(share, len(before)), len(before), "-", m.st.DiffDelRow, width)
	m.editSide(b, after, min(share, len(after)), len(after), "+", m.st.DiffAddRow, width)
}

// editSide draws one side of the change and says what it left out.
func (m *Model) editSide(b *strings.Builder, lines []string, show, total int,
	sign string, style lipgloss.Style, width int) {

	bar := m.st.AgentBar.Render("▎")
	for i := 0; i < show; i++ {
		line := bar + "    " + style.Render(sign+" "+
			truncate(highlight.ExpandTabs(lines[i]), max(8, width-8)))
		// Tinted end to end, so a block of additions is a band rather than a
		// row of coloured words — the same shape the history browser draws.
		if n := width - lipgloss.Width(line); n > 0 {
			line += style.Render(strings.Repeat(" ", n))
		}
		b.WriteString(clipLine(line, width))
		b.WriteByte('\n')
	}
	if rest := total - show; rest > 0 {
		b.WriteString(bar + "    " + m.st.Faint.Render(
			"… "+strconv.Itoa(rest)+" more "+noun(rest, "line")) + "\n")
	}
}
