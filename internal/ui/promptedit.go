package ui

import (
	"github.com/phanngoc/agent-tui/internal/session"
)

// Deleting in the prompt, quickly, and taking it back.
//
// What people already have in their fingers, and where it comes from:
//
//   - Every text field on Windows and Linux: ctrl+backspace deletes the word
//     before the caret, ctrl+delete the word after it. (alt on a Mac, and the
//     same here, since a terminal sends alt+backspace for option+delete.)
//   - readline, so every shell: ctrl+w the word before, alt+d the word after,
//     ctrl+u the line. ctrl+w was taken here for closing the session — which
//     is a session closed by a shell habit mid-sentence — so in a prompt that
//     has text it deletes the word, and only an empty prompt closes.
//   - Claude Code and the shells: ctrl+c with something typed clears it rather
//     than quitting. Here it quit, and a long prompt went with the program.
//     Now the first ctrl+c clears and the next one does what it always did.
//   - Select-all then delete, from every editor: ctrl+g selects the prompt and
//     backspace takes the selection.
//
// And from editors rather than shells: all of it can be undone. A shell's
// ctrl+u puts the line in a kill ring for ctrl+y, which nobody remembers; an
// editor's ctrl+z is the one thing everybody does. Each deletion takes a
// snapshot first — a run of word deletions is one snapshot, the way an editor
// groups a run of typing — and ctrl+z steps back through them, ctrl+y forward.

// promptSnap is the prompt as it was before a deletion.
type promptSnap struct {
	text   string
	at     int // the caret, in runes from the start
	attach []session.Attachment
}

const maxPromptUndo = 32

// wordKill reports whether a key deletes a word in the prompt.
func wordKill(key string) bool {
	switch key {
	case "ctrl+backspace", "alt+backspace", "ctrl+w",
		"ctrl+delete", "alt+delete", "alt+d":
		return true
	}
	return false
}

// snapPrompt records the prompt before a deletion. A run of one kind of
// deletion is one step back, not one per keystroke.
func (m *Model) snapPrompt(kind string) {
	if kind != "" && kind == m.promptRun {
		return
	}
	m.promptRun = kind
	m.promptUndo = append(m.promptUndo, m.promptNow())
	if len(m.promptUndo) > maxPromptUndo {
		m.promptUndo = m.promptUndo[1:]
	}
	m.promptRedo = nil
}

func (m *Model) promptNow() promptSnap {
	return promptSnap{text: m.input.Value(), at: m.inputOffset(), attach: m.attach}
}

func (m *Model) restorePrompt(p promptSnap) {
	m.input.ClearSelection()
	m.input.SetValue(p.text)
	m.moveInputTo(p.at)
	m.attach = p.attach
	m.closeCompletion()
	m.promptRun = ""
}

// clearPrompt empties the prompt, undoably. It reports whether there was
// anything to clear.
func (m *Model) clearPrompt() bool {
	if m.input.Value() == "" && len(m.attach) == 0 {
		return false
	}
	m.snapPrompt("")
	m.input.Reset()
	m.dropAttachments()
	m.closeCompletion()
	m.notice = "prompt cleared · ctrl+z brings it back"
	return true
}

// undoPrompt steps back to before the last deletion.
func (m *Model) undoPrompt() bool {
	n := len(m.promptUndo)
	if n == 0 {
		return false
	}
	p := m.promptUndo[n-1]
	m.promptUndo = m.promptUndo[:n-1]
	m.promptRedo = append(m.promptRedo, m.promptNow())
	m.restorePrompt(p)
	return true
}

// redoPrompt steps forward again.
func (m *Model) redoPrompt() bool {
	n := len(m.promptRedo)
	if n == 0 {
		return false
	}
	p := m.promptRedo[n-1]
	m.promptRedo = m.promptRedo[:n-1]
	m.promptUndo = append(m.promptUndo, m.promptNow())
	m.restorePrompt(p)
	return true
}

// forgetPromptEdits drops the undo history, when the prompt stops being the
// one it was about: sent, or swapped for another conversation's draft.
func (m *Model) forgetPromptEdits() {
	m.promptUndo, m.promptRedo, m.promptRun = nil, nil, ""
}
