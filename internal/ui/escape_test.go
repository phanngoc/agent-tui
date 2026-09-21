package ui

import (
	"context"
	"strings"
	"testing"
)

// busy puts the active session into a running turn with something to cancel.
func busy(t *testing.T, m *Model) context.Context {
	t.Helper()
	s := m.mgr.Active()
	s.Busy, s.Status = true, "thinking"
	ctx, cancel := context.WithCancel(context.Background())
	if m.runs == nil {
		m.runs = map[string]context.CancelFunc{}
	}
	m.runs[s.ID] = cancel
	return ctx
}

// esc stops the agent. It is the key reached for when a turn has gone wrong,
// and it used to do nothing at all there: the panes are still on screen
// afterwards, and the tokens are not.
func TestEscapeStopsTheAgent(t *testing.T) {
	m := newTestModel(t)
	ctx := busy(t, m)

	m.onKey(key("esc"))

	if m.mgr.Active().Busy {
		t.Error("the session is still running")
	}
	if ctx.Err() == nil {
		t.Error("the turn's context was not cancelled")
	}
	if len(m.runs) != 0 {
		t.Errorf("%d runs are still tracked", len(m.runs))
	}
}

// What is open closes first. Those are modes you opened yourself, and esc is
// how you get out of them; the turn is still there to stop with a second press.
func TestEscapeClosesWhatIsOpenBeforeStopping(t *testing.T) {
	m := newTestModel(t)
	busy(t, m)
	m.overlay = overlayFinder

	m.onKey(key("esc"))
	if m.overlay != overlayNone {
		t.Fatal("the overlay did not close")
	}
	if !m.mgr.Active().Busy {
		t.Error("closing an overlay also stopped the agent")
	}

	m.onKey(key("esc"))
	if m.mgr.Active().Busy {
		t.Error("the second esc did not stop the agent")
	}
}

// Stopping comes before moving the focus back, because one of them is
// recoverable and the other is a turn you are paying for.
func TestEscapeStopsBeforeReturningFocus(t *testing.T) {
	m := newTestModel(t)
	busy(t, m)
	m.setFocus(focusExplorer)

	m.onKey(key("esc"))
	if m.mgr.Active().Busy {
		t.Error("esc moved the focus instead of stopping the agent")
	}
	if m.focus != focusExplorer {
		t.Error("esc moved the focus as well")
	}

	m.onKey(key("esc"))
	if m.focus != focusInput {
		t.Error("the second esc did not return the focus to the prompt")
	}
}

// With nothing running, esc keeps doing what it did.
func TestEscapeIsUnchangedWhenIdle(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusChat)

	m.onKey(key("esc"))
	if m.focus != focusInput {
		t.Error("esc no longer returns the focus to the prompt")
	}
}

// The way out is named while there is something to get out of.
func TestTheStatusBarNamesTheStopKey(t *testing.T) {
	m := newTestModel(t)
	if out := stripANSI(m.statusBar()); strings.Contains(out, "esc to stop") {
		t.Errorf("an idle session offers a way to stop nothing:\n%s", out)
	}
	busy(t, m)
	if out := stripANSI(m.statusBar()); !strings.Contains(out, "esc to stop") {
		t.Errorf("a running turn does not say how to stop it:\n%s", out)
	}
}
