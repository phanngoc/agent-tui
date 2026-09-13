package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openForEdit previews a file and opens the editor on it.
func openForEdit(t *testing.T, m *Model, name, body string) string {
	t.Helper()
	p := filepath.Join(m.idx.Root(), name)
	mustWrite(t, p, body)
	m.Update(fileMsg{f: m.loader.Load(m.hostFS, p, name), line: 1})
	m.setFocus(focusPreview)
	m.onKey(key("e"))
	if m.edit == nil {
		t.Fatalf("the editor did not open: %q", m.notice)
	}
	return p
}

func typeIn(m *Model, text string) {
	for _, r := range text {
		if r == '\n' {
			m.onKey(key("enter"))
			continue
		}
		m.onKey(key(string(r)))
	}
}

func TestEditSavesThroughTheFilesystem(t *testing.T) {
	m := newTestModel(t)
	path := openForEdit(t, m, "edit.go", "package main\n")

	m.edit.ta.MoveToEnd()
	typeIn(m, "\nvar added = 1")

	if !m.edit.Dirty() {
		t.Fatal("typing did not mark the buffer dirty")
	}
	if got := stripANSI(m.View().Content); !strings.Contains(got, "●") {
		t.Errorf("the pane title does not show unsaved changes:\n%s", got)
	}

	m.onKey(key("ctrl+s"))
	if m.errText != "" {
		t.Fatalf("saving failed: %s", m.errText)
	}
	if m.edit.Dirty() {
		t.Error("the buffer is still dirty after saving")
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "var added = 1") {
		t.Errorf("the file on disk is %q", body)
	}
	// Text files end with a newline even though a textarea does not add one.
	if !strings.HasSuffix(string(body), "\n") {
		t.Errorf("the saved file has no trailing newline: %q", body)
	}
}

func TestEditReloadsWithHighlightingOnClose(t *testing.T) {
	m := newTestModel(t)
	openForEdit(t, m, "edit.go", "package main\n")

	m.edit.ta.MoveToEnd()
	typeIn(m, "\nfunc F() {}")
	m.onKey(key("ctrl+s"))

	cmd := m.onKey(key("esc"))
	if m.edit != nil {
		t.Fatal("esc did not close a saved buffer")
	}
	if cmd == nil {
		t.Fatal("closing did not reload the file")
	}
	m.Update(runUntil[fileMsg](t, cmd))

	if m.file == nil || !strings.Contains(strings.Join(m.file.Plain, "\n"), "func F()") {
		t.Fatalf("the preview did not pick up the edit: %+v", m.file)
	}
	// The highlighter runs again, so the styled lines carry colour.
	if !strings.Contains(strings.Join(m.file.Styled, ""), "\x1b[") {
		t.Error("the reloaded preview has no syntax colouring")
	}
}

// TestEditRefusesToDiscardSilently is the property that matters most: losing
// typing to a stray key is unforgivable in an editor.
func TestEditRefusesToDiscardSilently(t *testing.T) {
	m := newTestModel(t)
	openForEdit(t, m, "edit.go", "package main\n")
	m.edit.ta.MoveToEnd()
	typeIn(m, "\nunsaved work")

	m.onKey(key("esc"))
	if m.edit == nil {
		t.Fatal("the first esc threw away unsaved changes")
	}
	if !strings.Contains(m.notice, "unsaved") {
		t.Errorf("nothing explained why esc did not close: %q", m.notice)
	}

	m.onKey(key("esc"))
	if m.edit != nil {
		t.Error("a second esc should discard and close")
	}
}

func TestTypingAgainDisarmsTheDiscard(t *testing.T) {
	// Pressing esc, thinking better of it, then typing must not leave the next
	// esc armed to throw the work away.
	m := newTestModel(t)
	openForEdit(t, m, "edit.go", "package main\n")
	m.edit.ta.MoveToEnd()
	typeIn(m, "\nwork")

	m.onKey(key("esc"))
	typeIn(m, " more")
	m.onKey(key("esc"))
	if m.edit == nil {
		t.Error("esc discarded the buffer even though typing continued in between")
	}
}

func TestUndoAndRedo(t *testing.T) {
	m := newTestModel(t)
	openForEdit(t, m, "edit.go", "package main\n")
	m.edit.ta.MoveToEnd()

	typeIn(m, "\nalpha")
	withAlpha := m.edit.ta.Value()
	m.onKey(key("enter")) // a structural edit starts a new undo step
	typeIn(m, "beta")
	withBeta := m.edit.ta.Value()

	if withAlpha == withBeta {
		t.Fatal("setup: the two edits produced the same buffer")
	}

	m.onKey(key("ctrl+z"))
	if got := m.edit.ta.Value(); strings.Contains(got, "beta") {
		t.Errorf("undo left %q", got)
	}
	m.onKey(key("ctrl+y"))
	if got := m.edit.ta.Value(); got != withBeta {
		t.Errorf("redo produced %q, want %q", got, withBeta)
	}
}

func TestTypingRunsCollapseIntoOneUndoStep(t *testing.T) {
	m := newTestModel(t)
	openForEdit(t, m, "edit.go", "x\n")
	m.edit.ta.MoveToEnd()

	before := m.edit.ta.Value()
	typeIn(m, "abcdefgh") // one continuous run

	m.onKey(key("ctrl+z"))
	if got := m.edit.ta.Value(); got != before {
		t.Errorf("one undo left %q, want the whole run gone (%q)", got, before)
	}
}

func TestEditRefusesFilesItCannotHandle(t *testing.T) {
	m := newTestModel(t)

	// Binary.
	p := filepath.Join(m.idx.Root(), "blob.bin")
	mustWrite(t, p, "abc\x00def")
	m.Update(fileMsg{f: m.loader.Load(m.hostFS, p, "blob.bin"), line: 1})
	m.setFocus(focusPreview)
	m.onKey(key("e"))
	if m.edit != nil {
		t.Error("a binary file should not open in the editor")
	}
	if !strings.Contains(m.notice, "binary") {
		t.Errorf("notice = %q", m.notice)
	}

	// Too many lines.
	big := filepath.Join(m.idx.Root(), "big.txt")
	mustWrite(t, big, strings.Repeat("line\n", maxEditLines+10))
	m.Update(fileMsg{f: m.loader.Load(m.hostFS, big, "big.txt"), line: 1})
	m.onKey(key("e"))
	if m.edit != nil {
		t.Error("a very large file should not open in the editor")
	}
}

func TestEditKeepsTheTerminalCursor(t *testing.T) {
	// An editor without a real cursor cannot take composed input, and you
	// cannot see where you are typing.
	m := newTestModel(t)
	openForEdit(t, m, "edit.go", "package main\n")

	c := m.cursor()
	if c == nil {
		t.Fatal("the editor reports no terminal cursor")
	}
	if c.Position.X < m.sideW+m.chatW || c.Position.Y < headerRows {
		t.Errorf("the cursor at %+v is not inside the preview pane", c.Position)
	}
}

func TestEditWritesWhereTheSessionPoints(t *testing.T) {
	// Editing a file the session reached through another filesystem has to
	// write it back through that same filesystem.
	m := newTestModel(t)
	other, fs := otherRoot(t)
	m.useTarget(target{id: fs.ID(), label: "demo", fs: fs, workdir: other})

	path := filepath.Join(other, "elsewhere.go")
	m.Update(fileMsg{f: m.loader.Load(fs, path, "elsewhere.go"), line: 1})
	m.setFocus(focusPreview)
	m.onKey(key("e"))
	if m.edit == nil {
		t.Fatalf("the editor did not open: %q", m.notice)
	}
	if m.edit.fs.ID() != fs.ID() {
		t.Errorf("the editor writes through %q, want %q", m.edit.fs.ID(), fs.ID())
	}

	m.edit.ta.MoveToEnd()
	typeIn(m, "\n// touched")
	m.onKey(key("ctrl+s"))

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "// touched") {
		t.Errorf("the file was not written: %q", body)
	}
}

// TestQuitIsGuardedWhileEditing: losing unsaved work to a reflexive ctrl+c is
// the worst thing an editor can do.
func TestQuitIsGuardedWhileEditing(t *testing.T) {
	m := newTestModel(t)
	openForEdit(t, m, "edit.go", "package main\n")
	m.edit.ta.MoveToEnd()
	typeIn(m, "\nunsaved")

	if cmd := m.onKey(key("ctrl+c")); cmd != nil {
		t.Error("ctrl+c quit with unsaved changes in the buffer")
	}
	if !strings.Contains(m.notice, "unsaved") {
		t.Errorf("nothing warned about the unsaved work: %q", m.notice)
	}

	// Once saved, it quits like normal.
	m.onKey(key("ctrl+s"))
	if cmd := m.onKey(key("ctrl+c")); cmd == nil {
		t.Error("ctrl+c did nothing on a saved buffer")
	}
}

func TestTypingReachesTheBufferNotTheApp(t *testing.T) {
	// Almost every key is text while editing; a stray shortcut must not fire.
	m := newTestModel(t)
	openForEdit(t, m, "edit.go", "x\n")
	before := m.mgr.Len()

	m.edit.ta.MoveToEnd()
	typeIn(m, "t") // ctrl+t makes a session; plain t must not
	if m.mgr.Len() != before {
		t.Error("typing t created a session")
	}
	if !strings.Contains(m.edit.ta.Value(), "t") {
		t.Errorf("the character never reached the buffer: %q", m.edit.ta.Value())
	}

	// e would otherwise open the editor; / would start a find.
	typeIn(m, "e/")
	if m.finding {
		t.Error("typing / started a find instead of inserting it")
	}
	if got := m.edit.ta.Value(); !strings.Contains(got, "te/") {
		t.Errorf("buffer = %q", got)
	}
}
