package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/config"
)

// sectionNamed moves the page to a section by its title.
func sectionNamed(t *testing.T, m *Model, title string) {
	t.Helper()
	for i, s := range settingsSections() {
		if s.title == title {
			m.gotoSection(i)
			return
		}
	}
	t.Fatalf("no section %q", title)
}

// selectSetting moves the selection onto a setting by its label.
func selectSetting(t *testing.T, m *Model, label string) {
	t.Helper()
	for i, it := range m.settingsSection().items {
		if it.label == label {
			m.setSel = i
			return
		}
	}
	t.Fatalf("no setting %q in %s", label, m.settingsSection().title)
}

// ---- finding it --------------------------------------------------------------

// TestSettingsAreFoundWithoutKnowingACommand: F2, the ⚙ in the header, and the
// status line that names F2 — three ways in for someone who never read /help.
func TestSettingsAreFoundWithoutKnowingACommand(t *testing.T) {
	m := newTestModel(t)
	if out := stripANSI(m.View().Content); !strings.Contains(out, "f2  settings") {
		t.Errorf("the status line does not say how to open settings:\n%s", out)
	}

	press(t, m, "f2")
	if m.overlay != overlaySettings {
		t.Fatal("F2 did not open settings")
	}
	press(t, m, "f2")
	if m.overlay != overlayNone {
		t.Error("F2 on the page should close it")
	}

	m.View()
	gear, ok := m.switchFor(settingsSwitch)
	if !ok {
		t.Fatal("there is no ⚙ in the header")
	}
	if out := stripANSI(m.View().Content); !strings.Contains(strings.SplitN(out, "\n", 2)[0], "⚙") {
		t.Errorf("the ⚙ is not drawn in the header:\n%s", out)
	}
	clickAt(m, gear.x0+1, 0)
	if m.overlay != overlaySettings {
		t.Error("clicking ⚙ did not open settings")
	}
}

func TestSectionsByTabNumberAndClick(t *testing.T) {
	m := newTestModel(t)
	press(t, m, "f2")
	press(t, m, "tab")
	if m.settingsSection().title != "Agent" {
		t.Errorf("tab went to %s", m.settingsSection().title)
	}
	press(t, m, "shift+tab")
	press(t, m, "shift+tab")
	if m.settingsSection().title != "About" {
		t.Errorf("shift+tab from General should wrap to About, got %s", m.settingsSection().title)
	}
	press(t, m, "4")
	if m.settingsSection().title != "Layout" {
		t.Errorf("4 went to %s", m.settingsSection().title)
	}

	m.View()
	for _, h := range m.setHits {
		if h.kind == hitSection && h.idx == 2 {
			clickAt(m, m.overlayX+1+h.x0+1, m.overlayY+1+h.y)
		}
	}
	if m.settingsSection().title != "Appearance" {
		t.Errorf("clicking Appearance went to %s", m.settingsSection().title)
	}
}

// ---- what the settings do ----------------------------------------------------

// TestAgentDefaultsReachANewSession: the point of the Agent section.
func TestAgentDefaultsReachANewSession(t *testing.T) {
	m := newTestModel(t)
	m.openSettings()
	sectionNamed(t, m, "Agent")

	selectSetting(t, m, "Model")
	m.applySetting(m.settingsSection().items[m.setSel], "claude-haiku-4-5")
	selectSetting(t, m, "Mode")
	press(t, m, "right") // from config → plan, the first mode
	if m.prefs.Mode != agent.ModePlan.String() {
		t.Fatalf("mode saved as %q", m.prefs.Mode)
	}
	press(t, m, "esc")

	press(t, m, "ctrl+t")
	s := m.mgr.Active()
	if s.Model != "claude-haiku-4-5" || s.Mode != "plan" {
		t.Errorf("new session model=%q mode=%q", s.Model, s.Mode)
	}
	if p := config.LoadPrefs(); p.Model != "claude-haiku-4-5" || p.Mode != "plan" {
		t.Errorf("not saved: %+v", p)
	}
}

// TestNewSessionsKeepTheConfiguredMode is a bug the page brought to light: the
// mode in config.json reached the first session only, and every one made with
// ctrl+t after it was in auto.
func TestNewSessionsKeepTheConfiguredMode(t *testing.T) {
	m := newTestModel(t)
	m.cfg.Mode = "ask"
	press(t, m, "ctrl+t")
	if got := m.mgr.Active().Mode; got != "ask" {
		t.Errorf("a new session is in %q, config.json says ask", got)
	}
}

// TestUnavailableChoicesAreShownButSkipped: an engine that is not installed is
// still listed — so you know it exists — but ←→ steps over it.
func TestUnavailableChoicesAreShownButSkipped(t *testing.T) {
	m := newTestModel(t)
	m.openSettings()
	sectionNamed(t, m, "Agent")
	selectSetting(t, m, "Engine")
	it, _ := m.settingsItem()
	for i := 0; i < 8; i++ {
		press(t, m, "right")
		for _, c := range it.choices(m) {
			if c.id == m.prefs.Engine && c.off != "" {
				t.Fatalf("stepped onto %s, which is not available: %s", c.id, c.off)
			}
		}
	}
}

func TestThemeChangesAtOnceAndIsKept(t *testing.T) {
	m := newTestModel(t)
	m.openSettings()
	sectionNamed(t, m, "Appearance")
	before := m.themeName()
	press(t, m, "right")
	after := m.themeName()
	if after == before {
		t.Fatal("the theme did not change")
	}
	if got := config.LoadPrefs().Theme; got != after {
		t.Errorf("saved theme %q, showing %q", got, after)
	}
}

func TestLayoutFromTheSettingsPage(t *testing.T) {
	m := newTestModel(t)
	m.openSettings()
	sectionNamed(t, m, "Layout")
	selectSetting(t, m, "Sidebar")
	press(t, m, "right")
	if !m.dock.SideRight || !loadLayout().Dock.SideRight {
		t.Error("the sidebar did not move right, or it was not kept")
	}
	selectSetting(t, m, "File tree")
	press(t, m, "right")
	if !m.treeFold {
		t.Error("the file tree did not fold")
	}
	selectSetting(t, m, "Reset layout")
	press(t, m, "enter")
	if m.dock != (dock{}) || m.treeFold {
		t.Errorf("reset left dock=%+v fold=%v", m.dock, m.treeFold)
	}
}

func TestAboutCopiesAPath(t *testing.T) {
	m := newTestModel(t)
	m.openSettings()
	sectionNamed(t, m, "About")
	if cmd := m.activateSetting(); cmd == nil {
		t.Error("enter on a path did not copy it")
	}
	if !strings.Contains(m.setSaid, "prefs.json") {
		t.Errorf("footer = %q", m.setSaid)
	}
}

// ---- the mouse ---------------------------------------------------------------

func TestClickingTheValueChangesIt(t *testing.T) {
	m := newTestModel(t)
	m.openSettings()
	sectionNamed(t, m, "Layout")
	selectSetting(t, m, "Files")
	m.View()
	for _, h := range m.setHits {
		if h.kind == hitNext && h.idx == m.setSel && h.y >= 0 {
			clickAt(m, m.overlayX+1+h.x0+1, m.overlayY+1+h.y)
			break
		}
	}
	if !m.dock.TreeTop {
		t.Error("clicking the value did not change it")
	}
}

// ---- how it looks ------------------------------------------------------------

// TestSettingsPageFitsEveryTerminal: every line inside the frame, at the
// smallest size anyone runs this in and at a large one.
func TestSettingsPageFitsEveryTerminal(t *testing.T) {
	m := newTestModel(t)
	for _, size := range [][2]int{{80, 24}, {100, 30}, {200, 60}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.openSettings()
		for i := range settingsSections() {
			m.gotoSection(i)
			out := m.settingsView()
			w, _ := m.settingsSize()
			lines := strings.Split(out, "\n")
			if len(lines) > size[1] {
				t.Errorf("%dx%d %s: %d lines", size[0], size[1], m.settingsSection().title, len(lines))
			}
			for n, l := range lines {
				if lipgloss.Width(l) != w {
					t.Errorf("%dx%d %s line %d is %d wide, want %d:\n%s",
						size[0], size[1], m.settingsSection().title, n, lipgloss.Width(l), w, stripANSI(out))
					break
				}
			}
		}
	}
}

// TestPageKeepsItsHeight: moving between sections must not resize the box.
func TestPageKeepsItsHeight(t *testing.T) {
	m := newTestModel(t)
	m.openSettings()
	height := -1
	for i := range settingsSections() {
		m.gotoSection(i)
		h := strings.Count(m.settingsView(), "\n")
		if height >= 0 && h != height {
			t.Errorf("%s is %d lines, the others %d", m.settingsSection().title, h, height)
		}
		height = h
	}
}
