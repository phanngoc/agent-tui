package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/phanngoc/agent-tui/internal/session"
)

// What the turn is doing, where you are looking.
//
// A long turn used to be a spinner in the status bar and the name of the last
// tool beside it, for minutes: the tool had long finished and the model was
// thinking about its result, and nothing on screen could tell the two apart —
// nor either of them from a turn that had stopped answering. The question
// someone watching has is "where is it, and is it stuck", and the screen had
// the answer to neither.
//
// So the bottom of a running transcript carries one live line, the way Claude
// Code's own does:
//
//	⠋ thinking · 12s · ↓ ~1.2k tokens
//	    …so the guard in ekyc.service.ts rejects the second submit because
//
// — what it is doing, for how long it has been doing that (not the whole
// turn: the status bar has that), how much it has produced, and the last few
// lines of its thinking when the engine sends them. When nothing at all has
// arrived from the engine for a while, the line says that too, in the
// warning colour: a quiet minute is the stall you were wondering about.

// liveThinkingBytes is how much of the current thinking is kept: enough for
// the few lines shown, with room to wrap.
const liveThinkingBytes = 2 << 10

// thinkingRows is how many lines of it are shown.
const thinkingRows = 3

// quietAfter is how long without a word from the engine before the line says
// so. Shorter than a slow tool call, longer than the gap between two tokens.
const quietAfter = 20 * time.Second

// setPhase records what the turn is doing now, and restarts its clock only
// when that changed: a stream of deltas is one phase, not a thousand.
func setPhase(s *session.Session, what string) {
	if what == "" || s.Status == what {
		return
	}
	s.Status, s.PhaseAt = what, time.Now()
}

// hasRunningCall reports whether the newest answer has a call still running.
func hasRunningCall(s *session.Session) bool {
	for i := len(s.Messages) - 1; i >= 0; i-- {
		msg := s.Messages[i]
		if msg.Role != session.RoleAssistant {
			return false
		}
		for _, c := range msg.Tools {
			if !c.Done && !c.Denied {
				return true
			}
		}
		if len(msg.Tools) > 0 {
			return false
		}
	}
	return false
}

// feedToolOutput puts what a running command has printed under its call.
//
// Claude Code reports nothing of a command's output until it ends, but it
// writes it to a file as it goes and the task registry follows that file.
// Each time the registry moves, the output of a command that belongs to a
// call still running becomes that call's live output, the same as the
// built-in agent's own commands stream theirs.
func (m *Model) feedToolOutput() {
	for _, t := range m.tasks.All() {
		if t.ToolUse == "" || !t.Live() || !t.Following() {
			continue
		}
		s := m.mgr.Get(t.Owner)
		if s == nil || !s.Busy || !callRunning(s, t.ToolUse) {
			continue
		}
		lines := t.Tail(liveOutputRows)
		if len(lines) == 0 {
			continue
		}
		// Calls that run side by side share one clock; the one with output
		// to show is the one worth showing it under.
		s.OutputID = t.ToolUse
		s.Output = strings.Join(lines, "\n")
		if s == m.mgr.Active() {
			m.followChat()
		}
	}
}

// callRunning reports whether a call of the newest answer is still running.
func callRunning(s *session.Session, id string) bool {
	for i := len(s.Messages) - 1; i >= 0 && i >= len(s.Messages)-4; i-- {
		for _, c := range s.Messages[i].Tools {
			if c.ID == id {
				return !c.Done
			}
		}
	}
	return false
}

// activityLines is the live line and the thinking under it, for the bottom of
// a running transcript.
func (m *Model) activityLines(s *session.Session, width int) []string {
	if !s.Busy {
		return nil
	}
	now := time.Now()
	bar := m.st.AgentBar.Render("▎")

	what := orDefault(s.Status, "working")
	if !s.RunAt.IsZero() && s.Status != "" {
		// The status is the tool's name while one runs; said as what it is.
		what = "running " + s.Status
	}
	parts := []string{m.st.Warn.Render(m.spin.View()) + " " + m.st.Accent.Render(what)}
	if !s.PhaseAt.IsZero() {
		parts = append(parts, m.st.Dim.Render(running(s.PhaseAt)))
	}
	if n := streamedTokens(s); n > 0 {
		parts = append(parts, m.st.Dim.Render("↓ ~"+compactCount(n)+" tokens"))
	}
	if quiet := now.Sub(s.HeardAt); !s.HeardAt.IsZero() && quiet >= quietAfter && s.RunAt.IsZero() {
		// A running tool is allowed to be quiet: its clock is on its own line,
		// and a long build is not a stall. A turn with nothing running that
		// has said nothing for this long is the thing to look at.
		parts = append(parts, m.st.Warn.Render("no word from the engine for "+running(s.HeardAt)))
	}
	out := []string{bar + " " + clipLine(strings.Join(parts, m.st.Faint.Render(" · ")), width-2)}

	if s.Thinking != "" {
		text := strings.Join(strings.Fields(s.Thinking), " ")
		wrapped := strings.Split(ansi.Wordwrap(text, max(10, width-8), ""), "\n")
		if len(wrapped) > thinkingRows {
			wrapped = wrapped[len(wrapped)-thinkingRows:]
		}
		for _, l := range wrapped {
			out = append(out, bar+"     "+m.st.Faint.Italic(true).Render(l))
		}
	}
	return out
}

// streamedTokens is a rough count of what the turn has produced: text and
// tool input at about four bytes a token, plus the engine's own estimate of
// its thinking. Rough on purpose — it says whether something is happening
// and roughly how much, which is all a moving number is for.
func streamedTokens(s *session.Session) int {
	return s.Streamed/4 + s.ThinkTok
}

func compactCount(n int) string {
	if n < 1000 {
		return strconv.Itoa(n)
	}
	return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k"
}
