package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/vfs"
)

// A shell prompt says where you are standing. So does this one, because a
// session moves — `!cd`, `!wsl`, `r` in the tree — and the answer to "where is
// this about to run" was otherwise only on a pane you can hide.
func TestPromptSaysWhereTheSessionStands(t *testing.T) {
	m := newTestModel(t)
	if got := m.promptPath(); got != m.mgr.Active().CWD {
		t.Errorf("promptPath() = %q, want the session's directory %q", got, m.mgr.Active().CWD)
	}
}

// Home is written the way a shell writes it. The literal path is longer, says
// less, and is the same on every line of the screen.
func TestPromptAbbreviatesHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this machine")
	}
	m := newTestModel(t)
	s := m.mgr.Active()

	s.CWD = home
	if got := m.promptPath(); got != "~" {
		t.Errorf("at home promptPath() = %q, want ~", got)
	}

	s.CWD = filepath.Join(home, "workspace", "project")
	if got := m.promptPath(); got != "~/workspace/project" {
		t.Errorf("under home promptPath() = %q, want ~/workspace/project", got)
	}

	// Somewhere else keeps its own name: shortening it to ~ would be a lie.
	s.CWD = m.idx.Root()
	if got := m.promptPath(); strings.HasPrefix(got, "~") {
		t.Errorf("outside home promptPath() = %q", got)
	}
}

// `~/workspace` means two different places depending on which side of WSL you
// are on, so the one that is not this machine says which it is.
func TestPromptNamesTheFilesystemWhenItIsNotThisOne(t *testing.T) {
	m := newTestModel(t)
	fs := &fakePromptFS{home: "/home/phanngoc", label: "Ubuntu-24.04"}
	m.fsCache[fs.ID()] = fs
	s := m.mgr.Active()
	s.Target, s.CWD = fs.ID(), "/home/phanngoc/workspace"

	got := m.promptPath()
	if !strings.HasPrefix(got, "Ubuntu-24.04") {
		t.Errorf("promptPath() = %q, want it to name the distribution", got)
	}
	if !strings.HasSuffix(got, "~/workspace") {
		t.Errorf("promptPath() = %q, want the far side's home abbreviated too", got)
	}
}

// A path that will not fit loses its beginning. Cutting the other end leaves
// every directory in a repository looking like every other one.
func TestPromptKeepsTheEndOfALongPath(t *testing.T) {
	long := "/home/phanngoc/workspace/sbi-fpaas-be/src/modules/shinsei-authz-probe"
	got := truncateLeft(long, 30)

	if n := lipgloss.Width(got); n > 30 {
		t.Errorf("truncateLeft gave %d columns, want at most 30: %q", n, got)
	}
	if !strings.HasSuffix(got, "shinsei-authz-probe") {
		t.Errorf("the end of the path was cut off: %q", got)
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("nothing marks what was cut: %q", got)
	}
	if got := truncateLeft("short", 30); got != "short" {
		t.Errorf("a path that fits was changed to %q", got)
	}
}

// The prompt box is still a box. A title that does not fit its frame is a
// border that wraps, and the row below it is the one you type into.
func TestPromptBoxStaysARectangle(t *testing.T) {
	m := newTestModel(t)
	m.mgr.Active().CWD = "/home/phanngoc/" + strings.Repeat("deeply-nested/", 20) + "end"

	lines := strings.Split(m.inputBox(), "\n")
	if len(lines) < 3 {
		t.Fatalf("the prompt box drew %d lines", len(lines))
	}
	for i, l := range lines {
		if n := lipgloss.Width(l); n != m.w {
			t.Errorf("prompt row %d is %d columns, want %d:\n  %q", i, n, m.w, stripANSI(l))
		}
	}
	if !strings.Contains(stripANSI(lines[0]), "end") {
		t.Errorf("the title lost the part that identifies the directory:\n  %q", stripANSI(lines[0]))
	}
}

// fakePromptFS is a filesystem somewhere that is not this machine, with a home
// of its own.
type fakePromptFS struct {
	vfs.FS
	home, label string
}

func (f *fakePromptFS) ID() string                   { return "wsl:" + f.label }
func (f *fakePromptFS) Label() string                { return f.label }
func (f *fakePromptFS) IsLocal() bool                { return false }
func (f *fakePromptFS) DefaultDir() string           { return f.home }
func (f *fakePromptFS) Health(context.Context) error { return nil }

// A paste had nowhere to land: the default branch of the update loop forwards
// to the spinner and the two viewports, none of which holds text.
func TestPasteReachesThePrompt(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("xem ")

	m.Update(tea.PasteMsg{Content: "internal/ui/view.go:207"})

	if got := m.input.Value(); got != "xem internal/ui/view.go:207" {
		t.Errorf("the prompt holds %q", got)
	}
}

// It follows the keyboard: whatever would have taken a keystroke takes the
// paste.
func TestPasteFollowsTheKeyboard(t *testing.T) {
	m := newTestModel(t)
	m.overlay = overlayGrep
	m.grepIn.Focus() // as opening the overlay does
	m.grepIn.SetValue("")

	m.Update(tea.PasteMsg{Content: "resolvePfid"})

	if got := m.grepIn.Value(); got != "resolvePfid" {
		t.Errorf("the search box holds %q", got)
	}
	if m.input.Value() != "" {
		t.Errorf("the paste also went to the prompt: %q", m.input.Value())
	}
}

// A long path keeps its name and the state of the view; it is the directory
// you are already in that gives up the room.
func TestThePreviewTitleKeepsWhatChanges(t *testing.T) {
	m := newTestModel(t)
	m.resize(150, 30)
	long := filepath.Join("dashboard", "src", "components", "dashboard", "create-project-dialog.tsx")
	p := filepath.Join(m.idx.Root(), long)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Repeat("const x = 1"+strings.Repeat("!", 200)+"\n", 20)), 0o644); err != nil {
		t.Fatal(err)
	}
	m.Update(runUntil[fileMsg](t, m.loadFile(filepath.ToSlash(long), 1, true)))
	m.setFocus(focusPreview)
	for range 10 {
		m.previewKey(key("right"))
	}

	title := m.previewTitle()
	if lipgloss.Width(title) > titleRoom(m.prevW) {
		t.Errorf("the title is %d columns in a %d column frame: %q",
			lipgloss.Width(title), titleRoom(m.prevW), title)
	}
	if !strings.Contains(title, "col ") {
		t.Errorf("the column the pane starts at was cut off: %q", title)
	}
	if !strings.Contains(title, "create-project-dialog.tsx") {
		t.Errorf("the file name was cut off: %q", title)
	}

	// Back at column one it says nothing about columns.
	m.previewKey(key("0"))
	if strings.Contains(m.previewTitle(), "col ") {
		t.Errorf("a pane at column one is still reporting one: %q", m.previewTitle())
	}
}
