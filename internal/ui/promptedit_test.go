package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/session"
)

func prompted(t *testing.T, text string) *Model {
	t.Helper()
	m := newTestModel(t)
	m.setFocus(focusInput)
	m.input.SetValue(text)
	m.input.CursorEnd()
	return m
}

// ctrl+backspace deletes the word before the caret — and so does ctrl+h,
// which is what Windows delivers for it.
func TestCtrlBackspaceDeletesAWord(t *testing.T) {
	for _, k := range []string{"ctrl+backspace", "ctrl+h", "alt+backspace"} {
		m := prompted(t, "sửa lỗi push noti")
		m.Update(key(k))
		if got := m.input.Value(); got != "sửa lỗi push " {
			t.Errorf("%s left %q", k, got)
		}
	}
}

// ctrl+delete deletes the word after the caret.
func TestCtrlDeleteDeletesTheNextWord(t *testing.T) {
	m := prompted(t, "push noti now")
	m.input.SetCursorColumn(5)
	m.Update(key("ctrl+delete"))
	if got := m.input.Value(); got != "push  now" {
		t.Errorf("ctrl+delete left %q", got)
	}
}

// In a prompt with text, ctrl+w deletes a word the way a shell does, rather
// than closing the session; in an empty one it still closes.
func TestCtrlWInAPromptDeletesAWord(t *testing.T) {
	m := prompted(t, "deploy dev now")
	before := m.mgr.Len()
	m.Update(key("ctrl+w"))
	if got := m.input.Value(); got != "deploy dev " {
		t.Errorf("ctrl+w left %q", got)
	}
	if m.mgr.Len() != before {
		t.Error("ctrl+w closed the session with text in the prompt")
	}
}

// ctrl+u clears everything, attachments too, and ctrl+z brings it all back.
func TestClearingIsUndoable(t *testing.T) {
	m := prompted(t, attachMarker(1)+" xem ảnh này, rồi sửa lỗi")
	m.attach = []session.Attachment{{Path: "a.png"}}

	m.Update(key("ctrl+u"))
	if m.input.Value() != "" || len(m.attach) != 0 {
		t.Fatalf("ctrl+u left %q and %d images", m.input.Value(), len(m.attach))
	}
	if !strings.Contains(m.notice, "ctrl+z") {
		t.Errorf("clearing does not say how to undo: %q", m.notice)
	}
	m.Update(key("ctrl+z"))
	if got := m.input.Value(); !strings.HasSuffix(got, "rồi sửa lỗi") || len(m.attach) != 1 {
		t.Errorf("ctrl+z brought back %q with %d images", got, len(m.attach))
	}
	m.Update(key("ctrl+y"))
	if m.input.Value() != "" {
		t.Errorf("ctrl+y did not redo the clear: %q", m.input.Value())
	}
}

// A run of word deletions is one step back, as a run of typing is in an
// editor.
func TestARunOfWordDeletionsIsOneUndo(t *testing.T) {
	m := prompted(t, "một hai ba bốn")
	m.Update(key("ctrl+backspace"))
	m.Update(key("ctrl+backspace"))
	m.Update(key("ctrl+backspace"))
	if got := m.input.Value(); got != "một " {
		t.Fatalf("three word deletions left %q", got)
	}
	m.Update(key("ctrl+z"))
	if got := m.input.Value(); got != "một hai ba bốn" {
		t.Errorf("one ctrl+z brought back %q", got)
	}
}

// ctrl+c with something typed clears it, the way Claude Code does, instead
// of quitting with the prompt; the next ctrl+c quits.
func TestCtrlCClearsBeforeItQuits(t *testing.T) {
	m := prompted(t, "một prompt dài đang gõ dở")
	if cmd := m.onKey(key("ctrl+c")); cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("ctrl+c quit with text in the prompt")
		}
	}
	if m.input.Value() != "" {
		t.Fatalf("ctrl+c did not clear the prompt: %q", m.input.Value())
	}
	cmd := m.onKey(key("ctrl+c"))
	if cmd == nil {
		t.Fatal("ctrl+c on an empty prompt did nothing")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Error("ctrl+c on an empty prompt did not quit")
	}
}

// Sending forgets what was deleted: ctrl+z does not reach into the last prompt.
func TestSendingForgetsTheUndo(t *testing.T) {
	m := prompted(t, "abc def")
	m.Update(key("ctrl+backspace"))
	m.Update(key("enter"))
	m.Update(key("ctrl+z"))
	if m.input.Value() != "" {
		t.Errorf("ctrl+z after sending brought back %q", m.input.Value())
	}
}

// The hint under the prompt says how to take text out once there is some.
func TestTheHintNamesTheDeletions(t *testing.T) {
	m := prompted(t, "x")
	if h := m.hintFor(focusInput); !strings.Contains(h, "ctrl+u clear") || !strings.Contains(h, "ctrl+z undo") {
		t.Errorf("hint = %q", h)
	}
}
