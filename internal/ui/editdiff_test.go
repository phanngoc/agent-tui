package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/session"
)

func editCall(name string, in map[string]any) session.ToolCall {
	raw, _ := json.Marshal(in)
	return session.ToolCall{
		ID: "e", Name: name, Input: raw, Done: true,
		Result: "edited a.go (1 replacement(s))",
	}
}

// An edit says what it changed. "edited (1 replacement)" is the same report
// whether the right line was changed or the wrong one, and the lines are in
// the call's own arguments, so they cost nothing to show.
func TestAnEditShowsWhatItChanged(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Messages = append(s.Messages, session.Message{
		Role: session.RoleAssistant,
		Tools: []session.ToolCall{editCall("edit_file", map[string]any{
			"path":       "internal/ui/chat.go",
			"old_string": "if head {",
			"new_string": "if plan.head {",
		})},
	})
	m.invalidateChat()

	out := stripANSI(m.transcript(80))
	if !strings.Contains(out, "- if head {") {
		t.Errorf("the line that went is not shown:\n%s", out)
	}
	if !strings.Contains(out, "+ if plan.head {") {
		t.Errorf("the line that came is not shown:\n%s", out)
	}
}

// A write has no before: whatever was there is gone, and the file is now what
// is in the call.
func TestAWriteShowsWhatWasWritten(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Messages = append(s.Messages, session.Message{
		Role: session.RoleAssistant,
		Tools: []session.ToolCall{editCall("write_file", map[string]any{
			"path": "a.go", "content": "package a\n\nconst X = 1\n",
		})},
	})
	m.invalidateChat()

	out := stripANSI(m.transcript(80))
	if !strings.Contains(out, "+ package a") || !strings.Contains(out, "+ const X = 1") {
		t.Errorf("the written file is not shown:\n%s", out)
	}
	if strings.Contains(out, "- ") {
		t.Errorf("a write claimed to have removed something:\n%s", out)
	}
}

// A long edit is cut short, with both sides getting a share of the room: an
// edit that spent it all on what it removed would never say what it put there.
func TestALongEditIsCutShortOnBothSides(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Messages = append(s.Messages, session.Message{
		Role: session.RoleAssistant,
		Tools: []session.ToolCall{editCall("edit_file", map[string]any{
			"path":       "a.go",
			"old_string": strings.Repeat("old line\n", 20),
			"new_string": strings.Repeat("new line\n", 20),
		})},
	})
	m.invalidateChat()

	out := stripANSI(m.transcript(80))
	removed := strings.Count(out, "- old line")
	added := strings.Count(out, "+ new line")
	if removed == 0 || added == 0 {
		t.Errorf("one side of the edit was dropped entirely: -%d +%d\n%s", removed, added, out)
	}
	if removed+added > editRows {
		t.Errorf("%d lines shown, want at most %d", removed+added, editRows)
	}
	if !strings.Contains(out, "more lines") {
		t.Errorf("the edit does not say what it left out:\n%s", out)
	}

	// alt+o shows all of it, the same key that unfolds the calls.
	m.onKey(key("alt+o"))
	out = stripANSI(m.transcript(80))
	if got := strings.Count(out, "- old line"); got != 20 {
		t.Errorf("%d of 20 removed lines came back", got)
	}
	if got := strings.Count(out, "+ new line"); got != 20 {
		t.Errorf("%d of 20 added lines came back", got)
	}
}

// A call that failed, or was refused, changed nothing, and a call that is not
// an edit has nothing to show.
func TestOnlyASuccessfulEditIsShown(t *testing.T) {
	for _, tc := range []struct {
		name string
		call session.ToolCall
	}{
		{"a refused edit", func() session.ToolCall {
			c := editCall("edit_file", map[string]any{"old_string": "x", "new_string": "y"})
			c.Denied = true
			return c
		}()},
		{"a failed edit", func() session.ToolCall {
			c := editCall("edit_file", map[string]any{"old_string": "x", "new_string": "y"})
			c.IsError, c.Result = true, "old_string was not found"
			return c
		}()},
		{"a read", editCall("read_file", map[string]any{"path": "a.go"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t)
			s := m.mgr.Active()
			s.Messages = append(s.Messages, session.Message{
				Role: session.RoleAssistant, Tools: []session.ToolCall{tc.call},
			})
			m.invalidateChat()

			if out := stripANSI(m.transcript(80)); strings.Contains(out, "+ y") {
				t.Errorf("a change that did not happen is shown:\n%s", out)
			}
		})
	}
}

// The diff is laid out in columns like everything else, so it stays in them.
func TestTheEditDiffStaysInItsColumn(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Messages = append(s.Messages, session.Message{
		Role: session.RoleAssistant,
		Tools: []session.ToolCall{editCall("edit_file", map[string]any{
			"path":       "a.go",
			"old_string": "\t\t\tif err := doTheThing(ctx); err != nil { return fmt.Errorf(\"名寄せ: %w\", err) }",
			"new_string": "\t\t\tif err := doTheThing(ctx, opts); err != nil { return fmt.Errorf(\"名寄せ 失敗: %w\", err) }",
		})},
	})
	m.invalidateChat()

	for _, w := range []int{40, 60, 80, 120} {
		for i, l := range strings.Split(strings.TrimRight(m.transcript(w), "\n"), "\n") {
			if n := lipgloss.Width(l); n > w {
				t.Errorf("at width %d, row %d is %d columns:\n  %q", w, i, n, stripANSI(l))
			}
		}
	}
}
