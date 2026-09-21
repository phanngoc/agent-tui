package ui

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/session"
)

// step is one round trip of a turn: the agent says something, or calls a tool,
// or both.
func step(text string, commands ...string) session.Message {
	msg := session.Message{Role: session.RoleAssistant, Text: text}
	for i, c := range commands {
		in, _ := json.Marshal(map[string]any{"command": c})
		msg.Tools = append(msg.Tools, session.ToolCall{
			ID: "t" + strconv.Itoa(i), Name: "Bash", Input: in, Done: true,
			Result: "a\nb\nc\n", Elapsed: 120 * time.Millisecond,
		})
	}
	return msg
}

// A turn is one block however many round trips it took.
//
// The agent answers, calls a tool, answers again, and each of those is a
// message — so heading every one of them turned a turn that read eight files
// into eight blocks of a single line each, and the answer at the end was the
// ninth.
func TestATurnIsOneBlock(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Messages = append(s.Messages, session.Message{Role: session.RoleUser, Text: "tìm resolvePfid"})
	for i := range 8 {
		s.Messages = append(s.Messages, step("", "grep -n resolvePfid file"+strconv.Itoa(i)))
	}
	s.Messages = append(s.Messages, step("Nó ở onboarding.service.ts:1299."))
	m.invalidateChat()

	out := stripANSI(m.transcript(80))
	if n := strings.Count(out, "agent"); n != 1 {
		t.Errorf("a turn of 9 steps drew %d headings, want 1:\n%s", n, out)
	}
	// The repetition went, not the work: the turn keeps its last few calls and
	// says how many it folded, and alt+o brings every one of them back.
	if n := strings.Count(out, "Bash"); n != callsShown {
		t.Errorf("%d calls are shown, want the last %d:\n%s", n, callsShown, out)
	}
	m.onKey(key("alt+o"))
	if n := strings.Count(stripANSI(m.transcript(80)), "Bash"); n != 8 {
		t.Errorf("%d of 8 calls came back:\n%s", n, stripANSI(m.transcript(80)))
	}
	m.onKey(key("alt+o"))
	if !strings.Contains(out, "onboarding.service.ts:1299") {
		t.Errorf("the answer is missing:\n%s", out)
	}
	// And it reads as one block: no blank line inside it.
	body := out[strings.Index(out, "agent"):]
	if strings.Contains(body, "\n\n") {
		t.Errorf("the turn has a gap in it:\n%s", body)
	}
}

// What the user does ends a turn. Two turns are two blocks, or the transcript
// stops being a record of a conversation.
func TestWhatTheUserDoesStartsANewBlock(t *testing.T) {
	for _, tc := range []struct {
		name string
		mid  session.Message
	}{
		{"a question", session.Message{Role: session.RoleUser, Text: "và cái kia?"}},
		{"a ! command", session.Message{
			Role:  session.RoleUser,
			Shell: &session.ShellRun{Command: "git status", Where: "host", Done: true, Output: "clean"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t)
			s := m.mgr.Active()
			s.Messages = append(s.Messages,
				step("trước", "ls"), tc.mid, step("sau", "ls"))
			m.invalidateChat()

			out := stripANSI(m.transcript(80))
			if n := strings.Count(out, "agent"); n != 2 {
				t.Errorf("%d headings across two turns, want 2:\n%s", n, out)
			}
		})
	}
}

// A call is one line. It stopped being one when the outcome was added to the
// end of it without taking the room from anywhere, so every call in a busy
// turn spilled a fragment of itself onto the row below.
func TestACallIsOneLine(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Messages = append(s.Messages, step("",
		`cd /home/phanngoc/workspace && grep -n "名寄せ" fpaas-internal-work/docs/*.md`,
		`sed -n '1299,1400p' src/modules/onboarding/onboarding.service.ts`,
		`grep -n "async resolvePfid\|private async merge\|public readonly" src/**/*.ts`,
	))
	m.invalidateChat()

	for _, w := range []int{40, 60, 76, 120} {
		for i, l := range strings.Split(strings.TrimRight(m.transcript(w), "\n"), "\n") {
			if n := lipgloss.Width(l); n > w {
				t.Errorf("at width %d, row %d is %d columns:\n  %q", w, i, n, stripANSI(l))
			}
		}
	}
	// The part that says how it went survives the squeeze; the command is what
	// gives up room, because a truncated command still says what was run.
	out := stripANSI(m.transcript(60))
	if n := strings.Count(out, "3 lines"); n != 3 {
		t.Errorf("%d of 3 calls kept their outcome at 60 columns:\n%s", n, out)
	}
}
