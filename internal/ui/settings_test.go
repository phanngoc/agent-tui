package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/phanngoc/agent-tui/internal/config"
)

func TestSettingsPageSavesTheChoice(t *testing.T) {
	m := newTestModel(t)
	m.runSlash("settings", "")
	if m.overlay != overlaySettings {
		t.Fatal("/settings did not open the page")
	}
	if out := stripANSI(m.View().Content); !strings.Contains(out, "New sessions start in") {
		t.Fatalf("page not drawn:\n%s", out)
	}

	press(t, m, "down") // where the last session left off
	press(t, m, "enter")
	if m.overlay != overlayNone {
		t.Error("choosing should close the page")
	}
	if got := config.LoadPrefs().StartMode(); got != config.StartLast {
		t.Errorf("saved %q, want last", got)
	}
}

func TestSettingsRefusesAFolderThatIsNotThere(t *testing.T) {
	m := newTestModel(t)
	m.openSettings()
	press(t, m, "down")
	press(t, m, "down")
	press(t, m, "enter") // this folder: starts typing
	if !m.setEditing {
		t.Fatal("choosing 'this folder' should ask for one")
	}
	m.setIn.SetValue(filepath.Join(m.hostRoot(), "no-such-dir"))
	out := stripANSI(press(t, m, "enter"))
	if m.overlay != overlaySettings || !strings.Contains(out, "not a folder") {
		t.Errorf("a missing folder was accepted, or not explained:\n%s", out)
	}
	if config.LoadPrefs().StartMode() == config.StartFixed {
		t.Error("a missing folder was saved")
	}
}

func TestNewSessionStartsInTheFixedFolder(t *testing.T) {
	m := newTestModel(t)
	dir := filepath.Join(m.hostRoot(), "internal")
	m.openSettings()
	press(t, m, "down")
	press(t, m, "down")
	press(t, m, "enter")
	m.setIn.SetValue(dir)
	press(t, m, "enter")
	if m.overlay != overlayNone {
		t.Fatalf("a real folder was refused: %s", m.setErr)
	}

	press(t, m, "ctrl+t")
	if got := m.mgr.Active().CWD; got != dir {
		t.Errorf("new session in %q, want %q", got, dir)
	}
	if m.tree.Root() != dir {
		t.Errorf("the tree stayed on %q", m.tree.Root())
	}
}

func TestNewSessionPicksUpWhereTheLastLeftOff(t *testing.T) {
	m := newTestModel(t)
	dir := filepath.Join(m.hostRoot(), "internal", "core")
	m.setSessionRoot(dir)
	m.setStart(config.StartLast, "")

	m.runSlash("new", "")
	if got := m.mgr.Active().CWD; got != dir {
		t.Errorf("new session in %q, want the last session's %q", got, dir)
	}
}

// TestNewSessionDefaultsToTheLaunchFolder is the behaviour before the setting
// existed, and what nobody who never opens the page should notice changing.
func TestNewSessionDefaultsToTheLaunchFolder(t *testing.T) {
	m := newTestModel(t)
	m.setSessionRoot(filepath.Join(m.hostRoot(), "internal"))

	m.runSlash("new", "")
	if got := m.mgr.Active().CWD; got != m.hostRoot() {
		t.Errorf("new session in %q, want the launch folder %q", got, m.hostRoot())
	}
}
