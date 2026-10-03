package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/session"
)

// typePrompt types text through the update loop, as keystrokes.
func typePrompt(m *Model, text string) {
	for _, r := range text {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// Half a question to one session stays with it: it used to follow you into the
// next, where enter sent it to the wrong agent.
func TestEachSessionKeepsItsOwnDraft(t *testing.T) {
	m := newTestModel(t)
	first := m.mgr.Active()
	typePrompt(m, "sửa lỗi push")

	press(t, m, "ctrl+t")
	if m.mgr.Active() == first {
		t.Fatal("ctrl+t did not open a session")
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("the new session's prompt holds %q", got)
	}
	second := m.mgr.Active()
	typePrompt(m, "deploy dev")

	m.mgr.Select(m.indexOf(first))
	m.Update(nil)
	m.onSessionSwitch()
	m.Update(nil)
	if got := m.input.Value(); got != "sửa lỗi push" {
		t.Errorf("back in the first session the prompt holds %q", got)
	}
	if !m.drafted(second) {
		t.Error("the session left with text typed to it is not marked as having a draft")
	}
	if out := stripANSI(m.sessionMeta(second, 40)); !strings.Contains(out, "✎ draft") {
		t.Errorf("the session list does not say a draft is waiting: %q", out)
	}
}

// The side chat goes on being talked to from the prompt: going to its pane
// addresses the prompt to it, typing puts the caret back in the box without
// changing that, and enter sends there.
func TestTheSideChatIsTypedToThroughThePrompt(t *testing.T) {
	m := newTestModel(t)
	parent := withHistory(m)
	typePrompt(m, "câu hỏi chính")

	m.openBtw("")
	side := m.sideSession()
	if got := m.input.Value(); got != "" {
		t.Errorf("the side chat's prompt holds the conversation's draft %q", got)
	}

	// Typing with the side chat in front goes into the box, for the aside.
	typePrompt(m, "pfid là gì")
	if m.focus != focusInput {
		t.Errorf("typing did not put the caret in the prompt: focus=%v", m.focus)
	}
	if m.promptTarget() != side {
		t.Fatal("with the caret back in the box, the prompt stopped addressing the aside")
	}
	if out := stripANSI(m.View().Content); !strings.Contains(out, "btw ▸") {
		t.Error("the prompt does not say it is talking to the side chat")
	}

	before := len(parent.Messages)
	m.Update(key("enter"))
	if last := side.Messages[len(side.Messages)-1]; last.Role != session.RoleUser || last.Text != "pfid là gì" {
		t.Errorf("the side chat's last message is %+v", last)
	}
	if len(parent.Messages) != before {
		t.Error("the conversation got the aside's question")
	}

	// Going back to the transcript addresses the conversation again, with
	// what was typed to it.
	m.setFocus(focusChat)
	m.Update(nil)
	if m.promptTarget() != parent {
		t.Error("going to the transcript left the prompt on the aside")
	}
	if got := m.input.Value(); got != "câu hỏi chính" {
		t.Errorf("the conversation's draft came back as %q", got)
	}
}

// Closing the side chat hands the prompt back to the conversation, and the
// aside's half-typed question waits for it to open again.
func TestClosingTheSideChatKeepsItsDraft(t *testing.T) {
	m := newTestModel(t)
	withHistory(m)
	m.openBtw("")
	typePrompt(m, "chưa xong")

	m.closeBtw()
	if got := m.input.Value(); got != "" {
		t.Errorf("after closing the side chat the prompt holds %q", got)
	}
	m.openBtw("")
	if got := m.input.Value(); got != "chưa xong" {
		t.Errorf("the side chat's draft came back as %q", got)
	}
}

// An image's marker is numbered within the prompt it was pasted into, so the
// image travels with that prompt's draft rather than staying with the box.
func TestAttachmentsTravelWithTheirDraft(t *testing.T) {
	m := newTestModel(t)
	first := m.mgr.Active()
	m.attach = []session.Attachment{{Path: "a.png"}}
	m.input.SetValue(attachMarker(1) + " xem ảnh")

	press(t, m, "ctrl+t")
	if len(m.attach) != 0 {
		t.Errorf("the new session's prompt has %d images staged", len(m.attach))
	}
	m.mgr.Select(m.indexOf(first))
	m.onSessionSwitch()
	m.Update(nil)
	if len(m.attach) != 1 {
		t.Errorf("the image did not come back with its prompt: %d staged", len(m.attach))
	}
}

// Esc while typing to the side chat closes it, and does not stop the turn
// running in the conversation beside it.
func TestEscWhileTypingToTheSideChatLeavesTheTurnAlone(t *testing.T) {
	m := newTestModel(t)
	parent := withHistory(m)
	m.openBtw("")
	typePrompt(m, "x")
	parent.Busy = true

	m.Update(key("esc"))
	if m.showBtw {
		t.Error("esc did not close the side chat")
	}
	if !parent.Busy {
		t.Error("esc stopped the conversation's turn")
	}
}
