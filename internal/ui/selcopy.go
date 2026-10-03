package ui

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// y with a selection copies the selection.
//
// y copies the answer in view for Slack, and a selection is the reader saying
// which part they mean — so when there is one in the transcript, it is what y
// copies. The difficulty is that a selection is of what was drawn: wrapped to
// the pane's width, bullets and headings already rendered, bold and links
// gone. Pasted as it is, every paragraph arrives broken at the column the pane
// happened to end on.
//
// So the selection is traced back to the markdown it was drawn from, and the
// lines it covers are what is formatted for Slack. Words are what the two have
// in common — rendering changes marks, spacing and case but not the words —
// so the trace matches the first and last few words of the selection against
// the words of the source, line by line. A selection inside one line copies
// exactly what was selected: half a sentence is not a reason to send the whole
// paragraph. And where the trace fails — a selection of tool output, which is
// not in the prose — the drawn text goes as it is.

// copySelection copies the transcript's selection, for Slack where it can be
// traced to its source. ok is false when there is no selection to copy.
func (m *Model) copySelection() (tea.Cmd, bool) {
	if !m.sel.on || m.sel.pane != focusChat {
		return nil, false
	}
	text := m.selectedText()
	if text == "" {
		return nil, false
	}
	a, b, _ := m.sel.span()
	if src, ok := traceSource(text, m.selectionSources(a.row, b.row)); ok {
		return m.copyForSlack(src, "the selection"), true
	}
	return m.copyText(text), true
}

// selectionSources is the prose of every message the rows a..b touch.
func (m *Model) selectionSources(from, to int) string {
	s := m.mgr.Active()
	starts := m.chatStarts
	if len(starts) != len(s.Messages) {
		return ""
	}
	var parts []string
	for i, st := range starts {
		end := int(^uint(0) >> 1)
		if i+1 < len(starts) {
			end = starts[i+1] - 1
		}
		if end < from || st > to {
			continue
		}
		if t := strings.TrimSpace(s.Messages[i].Text); t != "" {
			parts = append(parts, s.Messages[i].Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// srcWord is a word of the source and the line it is on.
type srcWord struct {
	w    string
	line int
}

// traceSource finds the source lines a drawn selection came from.
func traceSource(selected, src string) (string, bool) {
	sel := words(selected)
	if len(sel) == 0 || strings.TrimSpace(src) == "" {
		return "", false
	}
	lines := strings.Split(src, "\n")
	var stream []srcWord
	for i, l := range lines {
		for _, w := range words(l) {
			stream = append(stream, srcWord{w, i})
		}
	}

	// The start: the first few selected words, found in order. A selection
	// that begins on something the source does not have — a speaker's name, a
	// timestamp, a tool's line — is walked past until it reaches prose.
	const k = 4
	start, first := -1, 0
	for first < len(sel) && start < 0 {
		start = find(stream, sel[first:min(len(sel), first+k)], 0)
		if start < 0 {
			first++
		}
	}
	if start < 0 {
		return "", false
	}
	// The end, the same way from the other side. It is looked for near where
	// it should be — as many words on from the start as were selected — and
	// not merely anywhere after it, because a few common words recur and the
	// last place they do may be paragraphs past the selection. The source has
	// at least the words that were drawn (a table cell leaves its URL out, not
	// the other way round), so the search starts a little short of that.
	end, last := -1, len(sel)
	for last > first && end < 0 {
		tail := sel[max(first, last-k):last]
		expect := start + (last - len(tail) - first)
		at := find(stream, tail, max(start, expect-8))
		if at < 0 {
			at = find(stream, tail, start)
		}
		if at >= 0 {
			end = at + len(tail) - 1
		} else {
			last--
		}
	}
	if end < 0 {
		return "", false
	}

	lo, hi := stream[start].line, stream[end].line
	if lo == hi {
		// Inside one line, what was selected is what goes: the line's
		// formatting is not worth sending a sentence nobody chose.
		if len(sel) < len(words(lines[lo])) {
			return "", false
		}
	}
	return strings.Join(lines[lo:hi+1], "\n"), true
}

// words splits text into lower-cased runs of letters and digits.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// find is the first position at or after from where seq occurs in stream.
func find(stream []srcWord, seq []string, from int) int {
	for i := from; i+len(seq) <= len(stream); i++ {
		if matchAt(stream, seq, i) {
			return i
		}
	}
	return -1
}

func matchAt(stream []srcWord, seq []string, i int) bool {
	for j, w := range seq {
		if stream[i+j].w != w {
			return false
		}
	}
	return true
}
