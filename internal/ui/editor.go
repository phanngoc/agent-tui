package ui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/theme"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// Editing limits. A textarea re-wraps its whole buffer on every keystroke, so a
// very large file would turn typing into a slideshow. Refusing up front with a
// reason beats letting someone discover it halfway through an edit.
const (
	maxEditLines = 5000
	maxEditBytes = 512 << 10
)

// editKind groups keystrokes for undo. Runs of ordinary typing collapse into
// one step; anything structural starts a new one, which is roughly what an
// editor's undo is expected to do.
type editKind int

const (
	editNone editKind = iota
	editType
	editStructural
)

// editStep is a point the buffer can be returned to.
type editStep struct {
	value string
	row   int
	col   int
}

// editor turns the preview pane into a writable buffer.
//
// While it is open the pane shows plain text: the syntax highlighter produces
// ANSI, and a textarea would treat those escapes as characters to edit. Colour
// comes back when the file is saved and reloaded.
type editor struct {
	ta  textarea.Model
	abs string
	rel string
	fs  vfs.FS

	original string
	undo     []editStep
	redo     []editStep
	lastKind editKind
}

// newEditor prepares a buffer for a file, or explains why it cannot.
func newEditor(st *theme.Styles, fs vfs.FS, abs, rel, body string, w, h int) (*editor, string) {
	switch {
	case strings.IndexByte(body, 0) >= 0:
		return nil, rel + " is a binary file"
	case len(body) > maxEditBytes:
		return nil, rel + " is too large to edit here"
	case strings.Count(body, "\n") > maxEditLines:
		return nil, rel + " has more than " + itoa(maxEditLines) + " lines; too large to edit here"
	}

	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = true
	ta.CharLimit = 0
	ta.MaxHeight = 0
	ta.SetStyles(editorStyles(st))
	ta.SetVirtualCursor(false)
	ta.SetWidth(max(10, w))
	ta.SetHeight(max(3, h))
	ta.SetValue(body)
	ta.MoveToBegin()
	ta.Focus()

	return &editor{ta: ta, abs: abs, rel: rel, fs: fs, original: body}, ""
}

// Dirty reports whether the buffer differs from what is on disk.
func (e *editor) Dirty() bool { return e.ta.Value() != e.original }

// snapshot records the current buffer if this edit starts a new undo step.
func (e *editor) snapshot(kind editKind) {
	if kind == editType && e.lastKind == editType {
		return // keep collapsing a run of typing
	}
	e.undo = append(e.undo, e.state())
	if len(e.undo) > 200 {
		e.undo = e.undo[1:]
	}
	e.redo = nil
	e.lastKind = kind
}

func (e *editor) state() editStep {
	li := e.ta.LineInfo()
	return editStep{value: e.ta.Value(), row: e.ta.Line(), col: li.StartColumn + li.ColumnOffset}
}

func (e *editor) restore(s editStep) {
	e.ta.SetValue(s.value)
	for e.ta.Line() > s.row {
		e.ta.CursorUp()
	}
	for e.ta.Line() < s.row {
		e.ta.CursorDown()
	}
	e.ta.SetCursorColumn(s.col)
	e.lastKind = editNone
}

// Undo steps back one change.
func (e *editor) Undo() bool {
	if len(e.undo) == 0 {
		return false
	}
	e.redo = append(e.redo, e.state())
	step := e.undo[len(e.undo)-1]
	e.undo = e.undo[:len(e.undo)-1]
	e.restore(step)
	return true
}

// Redo steps forward again.
func (e *editor) Redo() bool {
	if len(e.redo) == 0 {
		return false
	}
	e.undo = append(e.undo, e.state())
	step := e.redo[len(e.redo)-1]
	e.redo = e.redo[:len(e.redo)-1]
	e.restore(step)
	return true
}

// Save writes the buffer through the session's filesystem, so editing a file
// inside a container writes it inside that container.
func (e *editor) Save() error {
	body := e.ta.Value()
	// Text files end with a newline; a textarea does not add one.
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := e.fs.WriteFile(ctx, e.abs, []byte(body)); err != nil {
		return err
	}
	e.original = e.ta.Value()
	return nil
}

// editorStyles keeps the buffer legible: every field that renders text gets a
// colour, for the same reason the prompt does.
func editorStyles(st *theme.Styles) textarea.Styles {
	base := textarea.DefaultDarkStyles()
	p := st.P

	for _, s := range []*textarea.StyleState{&base.Focused, &base.Blurred} {
		s.Base = lipgloss.NewStyle()
		s.Text = lipgloss.NewStyle().Foreground(p.Fg)
		s.CursorLine = lipgloss.NewStyle().Foreground(p.Fg)
		s.CursorLineNumber = lipgloss.NewStyle().Foreground(p.Accent)
		s.LineNumber = lipgloss.NewStyle().Foreground(p.Faint)
		s.EndOfBuffer = lipgloss.NewStyle().Foreground(p.Bg)
		s.Placeholder = lipgloss.NewStyle().Foreground(p.Faint)
		s.Prompt = lipgloss.NewStyle().Foreground(p.Faint)
		s.Selection = lipgloss.NewStyle().Foreground(p.Fg).Background(p.Border)
	}
	base.Blurred.Text = lipgloss.NewStyle().Foreground(p.Dim)
	base.Cursor = textarea.CursorStyle{Color: p.Accent, Shape: tea.CursorBar, Blink: true}
	return base
}
