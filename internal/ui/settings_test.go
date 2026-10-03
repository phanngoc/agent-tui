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

	// The choice is a value on its row: → moves it on, and it is saved then
	// and there, with the page left open for the next change.
	out := stripANSI(press(t, m, "right"))
	if m.overlay != overlaySettings {
		t.Error("changing a value should not close the page")
	}
	if got := config.LoadPrefs().StartMode(); got != config.StartLast {
		t.Errorf("saved %q, want last", got)
	}
	if !strings.Contains(out, "✓") {
		t.Errorf("the footer does not say it was saved:\n%s", out)
	}
}

func TestSettingsRefusesAFolderThatIsNotThere(t *testing.T) {
	m := newTestModel(t)
	m.openSettings()
	press(t, m, "right")
	press(t, m, "right") // this folder, with none typed yet: starts typing
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
	press(t, m, "right")
	press(t, m, "right")
	m.setIn.SetValue(dir)
	press(t, m, "enter")
	if m.setEditing || m.setErr != "" {
		t.Fatalf("a real folder was refused: %s", m.setErr)
	}
	if p := config.LoadPrefs(); p.StartMode() != config.StartFixed || p.StartPath != dir {
		t.Fatalf("saved %+v", p)
	}
	press(t, m, "esc")

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
