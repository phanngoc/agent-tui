package ui

import (
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/session"
)

// screen draws a whole frame at a size and hands back its rows.
func screen(t *testing.T, w, h int) (*Model, []string) {
	t.Helper()
	m := newTestModel(t)
	m.resize(w, h)
	s := m.mgr.Active()
	s.Title = "về limit cpu"
	s.Append(session.Message{Role: session.RoleUser, Text: "sao limit cpu chạm trần?"})
	s.Append(session.Message{Role: session.RoleAssistant, Text: "Không chu kỳ nào bị throttle."})
	talking(m, "deploy pipeline")
	m.mgr.Select(m.indexOf(s))
	m.invalidateChat()
	return m, strings.Split(stripANSI(m.View().Content), "\n")
}

// A rounded box is a card — it says "this is a thing, sitting on a surface" —
// and a terminal divided into panes is not a surface with things on it. It is
// one surface, ruled into parts.
func TestNothingIsRounded(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 30}, {180, 44}} {
		_, rows := screen(t, size[0], size[1])
		for i, l := range rows {
			if n := strings.IndexAny(l, "╭╮╰╯"); n >= 0 {
				t.Errorf("%dx%d row %d has a rounded corner at column %d: %s",
					size[0], size[1], i, n, l)
			}
		}
	}
}

// Panes share a rule rather than each drawing its own. Two lines where one
// will do is the difference between a screen that reads as structure and one
// that reads as clutter.
func TestPanesShareOneRule(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 30}, {180, 44}} {
		_, rows := screen(t, size[0], size[1])
		for i, l := range rows {
			r := []rune(l)
			for c := 0; c+1 < len(r); c++ {
				// Two verticals touching, or a box closing and another opening
				// against it, is a seam that was not taken.
				if r[c] == '│' && r[c+1] == '│' {
					t.Errorf("%dx%d row %d: two rules at column %d", size[0], size[1], i, c)
				}
				if (r[c] == '┐' || r[c] == '┘') && (r[c+1] == '┌' || r[c+1] == '└') {
					t.Errorf("%dx%d row %d: two corners at column %d", size[0], size[1], i, c)
				}
			}
		}
	}
}

// And the frame is exactly the terminal: every row its full width, and as many
// rows as it has. A screen a row short leaves whatever was there before; a row
// over scrolls the top away.
func TestTheFrameIsExactlyTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {92, 20}, {120, 30}, {180, 44}} {
		_, rows := screen(t, size[0], size[1])
		if len(rows) != size[1] {
			t.Errorf("%dx%d: drew %d rows", size[0], size[1], len(rows))
		}
		for i, l := range rows {
			if n := len([]rune(l)); n > size[0] {
				t.Errorf("%dx%d row %d is %d columns", size[0], size[1], i, n)
			}
		}
	}
}

// The prompt has a rule above it and none below: the status line is the end of
// the screen, and a border drawn to separate the last thing from the edge is a
// row of the conversation spent on nothing.
func TestThePromptHasNoRuleUnderIt(t *testing.T) {
	m, rows := screen(t, 100, 24)
	// The last row is the status line, and the one above it is the last row of
	// the prompt — which should be text, not a rule.
	last := rows[len(rows)-2]
	if strings.ContainsAny(last, "└┘─") && !strings.Contains(last, "❯") {
		t.Errorf("there is still a rule under the prompt: %q", last)
	}
	// And the geometry the mouse uses agrees with what was drawn.
	_, top, _, h, ok := m.inputArea()
	if !ok {
		t.Fatal("the prompt has no writable area")
	}
	if top+h > len(rows)-1 {
		t.Errorf("the prompt's rows run to %d, past the %d drawn", top+h, len(rows)-1)
	}
}
