package ui

import (
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/engine"
)

// TestSlashModelOpensThePicker covers the reason this exists: /model used to
// fall through to "no command /model".
func TestSlashModelOpensThePicker(t *testing.T) {
	m := newTestModel(t)

	m.input.SetValue("/model")
	m.inputKey(key("enter"))

	if m.overlay != overlayModel {
		t.Fatalf("overlay = %v, want the model picker", m.overlay)
	}
	if strings.Contains(m.notice, "no command") {
		t.Errorf("notice = %q", m.notice)
	}
	if len(m.mgr.Active().Messages) != 0 {
		t.Error("/model was sent to the agent")
	}

	out := stripANSI(m.modelView())
	for _, want := range []string{"Opus 5", "Sonnet 5", "Haiku 4.5", "Fable 5.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the picker does not offer %q:\n%s", want, out)
		}
	}
}

// TestPickerOpensOnTheCurrentModel keeps the list from opening at the top and
// making the wrong row look chosen.
func TestPickerOpensOnTheCurrentModel(t *testing.T) {
	m := newTestModel(t)
	m.mgr.Active().Model = "claude-haiku-4-5"

	m.openModelPicker()
	rows := m.modelRows()
	if m.modelSel >= len(rows) || rows[m.modelSel].ID != "claude-haiku-4-5" {
		t.Errorf("picker opened on row %d (%v)", m.modelSel, rows)
	}
}

func TestSlashModelWithAName(t *testing.T) {
	m := newTestModel(t)

	m.input.SetValue("/model sonnet")
	m.inputKey(key("enter"))

	if m.overlay != overlayNone {
		t.Error("naming a model still opened the picker")
	}
	if got := m.mgr.Active().Model; got != "claude-sonnet-5" {
		t.Errorf("session model = %q, want claude-sonnet-5", got)
	}
	if !strings.Contains(m.notice, "Sonnet 5") {
		t.Errorf("notice = %q", m.notice)
	}
}

func TestSlashModelRejectsAnUnknownName(t *testing.T) {
	m := newTestModel(t)
	before := m.sessionModel(m.mgr.Active())

	m.input.SetValue("/model gpt-5")
	m.inputKey(key("enter"))

	if got := m.sessionModel(m.mgr.Active()); got != before {
		t.Errorf("session moved to %q", got)
	}
	if !strings.Contains(m.notice, "no model called") {
		t.Errorf("notice = %q", m.notice)
	}
	// The message has to name what is on offer, or it is a dead end.
	if !strings.Contains(m.notice, "Opus 5") {
		t.Errorf("notice does not list the choices: %q", m.notice)
	}
}

// TestModelIsPerSession is the whole point of hanging it off the session: two
// sessions run on different models at the same time.
func TestModelIsPerSession(t *testing.T) {
	m := newTestModel(t)
	first := m.mgr.Active()
	m.setModelByName("haiku")

	m.mgr.New()
	m.onSessionSwitch()
	second := m.mgr.Active()
	m.setModelByName("opus")

	if first.Model != "claude-haiku-4-5" {
		t.Errorf("the first session moved to %q", first.Model)
	}
	if second.Model != "claude-opus-5" {
		t.Errorf("the second session is on %q", second.Model)
	}
}

// TestChangingModelKeepsTheConversation separates this from switching engine,
// which cannot carry a conversation across.
func TestChangingModelKeepsTheConversation(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.ExternalID = "some-cli-session"

	m.input.SetValue("!echo hi")
	m.Update(runUntil[bangDoneMsg](t, m.inputKey(key("enter"))))
	before := len(s.Messages)

	m.setModelByName("sonnet")

	if len(s.Messages) != before {
		t.Errorf("the transcript changed: %d messages, was %d", len(s.Messages), before)
	}
	if s.ExternalID != "some-cli-session" {
		t.Error("the engine's own conversation was dropped")
	}
}

// TestModelSaysWhenTheEngineIgnoresIt: codex and opencode drive other
// providers, so a Claude model id means nothing to them. Silently accepting
// the choice would be worse than saying so.
func TestModelSaysWhenTheEngineIgnoresIt(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()

	for _, id := range []string{"", engine.IDAPI, engine.IDClaude} {
		s.Engine = id
		if why, ok := m.modelIgnored(s); ok {
			t.Errorf("engine %q was reported as ignoring the model: %s", id, why)
		}
	}

	s.Engine = engine.IDCodex
	why, ok := m.modelIgnored(s)
	if !ok {
		t.Fatal("codex was not reported as choosing its own model")
	}
	if !strings.Contains(why, "own model") {
		t.Errorf("explanation = %q", why)
	}

	s.Model = ""
	m.setModelByName("sonnet")
	if !strings.Contains(m.notice, "own model") {
		t.Errorf("choosing a model under codex said nothing: %q", m.notice)
	}
}

// TestSessionModelFallsBack keeps a session saved before models were
// selectable from running on an empty model id.
func TestSessionModelFallsBack(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Model = ""

	got := m.sessionModel(s)
	if got == "" {
		t.Fatal("a session with no model resolved to nothing")
	}
	if _, ok := agent.ResolveModel(got); !ok {
		t.Errorf("fallback %q is not a model", got)
	}
}

// TestPickerShowsAModelFromConfig: a model set in config.json that predates
// the catalogue must still appear, or the picker claims the session is on
// something it is not.
func TestPickerShowsAModelFromConfig(t *testing.T) {
	m := newTestModel(t)
	m.mgr.Active().Model = "claude-opus-4-8"

	rows := m.modelRows()
	found := false
	for _, r := range rows {
		if r.ID == "claude-opus-4-8" {
			found = true
		}
	}
	if !found {
		t.Errorf("the session's own model is missing from the picker: %v", rows)
	}
	if !strings.Contains(stripANSI(m.modelView()), "claude-opus-4-8") {
		t.Error("the picker does not show it")
	}
}
