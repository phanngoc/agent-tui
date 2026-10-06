package ui

import (
	"testing"

	"github.com/phanngoc/agent-tui/internal/gateway"
)

// A model and mode chosen on the web for a session held here are applied to
// it, as if chosen here.
func TestSettingsFromTheWebApplyToTheSession(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Model, s.Mode = "claude-sonnet-5-5", "auto"
	m.onGatewayCommand(gateway.Command{Type: gateway.CmdSettings, Session: s.ID, Model: "claude-opus-5-5", Mode: "plan", From: "web"})
	if s.Model != "claude-opus-5-5" || s.Mode != "plan" {
		t.Fatalf("model %q mode %q", s.Model, s.Mode)
	}
	if m.notice == "" {
		t.Fatal("nothing said the session changed")
	}
}
