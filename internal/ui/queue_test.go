package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
)

// typeAndSend types into the prompt and presses enter.
func typeAndSend(t *testing.T, m *Model, text string) {
	t.Helper()
	m.setFocus(focusInput)
	m.input.SetValue(text)
	press(t, m, "enter")
}

// TestPromptSentMidTurnIsQueued is the complaint: sending while the agent
// worked was refused. It is taken, shown under the turn, and the box clears.
func TestPromptSentMidTurnIsQueued(t *testing.T) {
	m := newTestModel(t)
	s, _ := busyTurn(m)
	typeAndSend(t, m, "also update the landing page")

	if len(s.Queued) != 1 || s.Queued[0].Text != "also update the landing page" {
		t.Fatalf("queued = %+v", s.Queued)
	}
	if m.input.Value() != "" {
		t.Errorf("the prompt kept %q", m.input.Value())
	}
	out := stripANSI(m.View().Content)
	if !strings.Contains(out, "queued") || !strings.Contains(out, "also update the landing page") {
		t.Errorf("the queued prompt is not shown:\n%s", out)
	}
}

// TestQueuedPromptGoesWhenTheTurnEnds: the next turn starts by itself.
func TestQueuedPromptGoesWhenTheTurnEnds(t *testing.T) {
	m := newTestModel(t)
	s, send := busyTurn(m)
	typeAndSend(t, m, "first follow-up")
	typeAndSend(t, m, "second follow-up")
	if len(s.Queued) != 2 {
		t.Fatalf("queued %d, want 2", len(s.Queued))
	}

	send(agent.EvDone{})
	if !s.Busy {
		t.Fatal("the queued prompt did not start a turn")
	}
	if last := s.Last(); last == nil || last.Role != session.RoleUser || last.Text != "first follow-up" {
		t.Errorf("the turn started with %+v", last)
	}
	if len(s.Queued) != 1 || s.Queued[0].Text != "second follow-up" {
		t.Errorf("left in the queue: %+v", s.Queued)
	}
}

// TestStoppingTheTurnGivesTheQueueBack: esc is not "send what I queued".
func TestStoppingTheTurnGivesTheQueueBack(t *testing.T) {
	m := newTestModel(t)
	s, _ := busyTurn(m)
	typeAndSend(t, m, "do this next")
	m.cancelRun()
	if len(s.Queued) != 0 {
		t.Errorf("the queue survived the stop: %+v", s.Queued)
	}
	if m.input.Value() != "do this next" {
		t.Errorf("the prompt holds %q, want the queued text back", m.input.Value())
	}
}

// TestFailedTurnGivesTheQueueBack: what to do after an error is the reader's
// call, so a failure sends nothing on.
func TestFailedTurnGivesTheQueueBack(t *testing.T) {
	m := newTestModel(t)
	s, send := busyTurn(m)
	typeAndSend(t, m, "then this")
	send(agent.EvDone{Err: errors.New("rate limited")})
	if s.Busy {
		t.Error("a failed turn sent the queued prompt on")
	}
	if m.input.Value() != "then this" {
		t.Errorf("the prompt holds %q", m.input.Value())
	}
}

func TestCancelledTurnSendsNothingOn(t *testing.T) {
	m := newTestModel(t)
	s, send := busyTurn(m)
	typeAndSend(t, m, "queued")
	send(agent.EvDone{Err: context.Canceled})
	if s.Busy {
		t.Error("a cancelled turn sent the queue on")
	}
}

// TestUpTakesTheLastQueuedBackToEdit, as in Claude Code.
func TestUpTakesTheLastQueuedBackToEdit(t *testing.T) {
	m := newTestModel(t)
	s, _ := busyTurn(m)
	typeAndSend(t, m, "one")
	typeAndSend(t, m, "two, with a typo")
	press(t, m, "up")
	if m.input.Value() != "two, with a typo" || len(s.Queued) != 1 {
		t.Errorf("prompt=%q queued=%+v", m.input.Value(), s.Queued)
	}
}

// TestQueuedImagesTravelWithTheirPrompt: an image pasted into a queued prompt
// goes with that prompt, not with whatever is typed next.
func TestQueuedImagesTravelWithTheirPrompt(t *testing.T) {
	m := newTestModel(t)
	s, send := busyTurn(m)
	stagePNG(t, m, 256)
	typeAndSend(t, m, m.input.Value()+"look at this")
	if len(m.attach) != 0 {
		t.Error("the image stayed staged for the next prompt")
	}
	send(agent.EvDone{})
	if last := s.Last(); last == nil || len(last.Files) != 1 {
		t.Errorf("the queued prompt went without its image: %+v", last)
	}
}
