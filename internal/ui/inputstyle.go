package ui

import (
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/theme"
)

// The bubbles text components ship defaults that leave the focused Text style
// with no foreground at all, so whatever the user types falls through to the
// terminal's own foreground colour. On the textarea that lands on a cursor-line
// background of ANSI black, and the text disappears completely.
//
// Both helpers below therefore set a colour on every field that renders
// anything, rather than accepting a default for some of them.

// textareaStyles builds the prompt's styling from the theme.
func textareaStyles(st *theme.Styles) textarea.Styles {
	p := st.P
	base := lipgloss.NewStyle()

	state := func(text, prompt lipgloss.Style) textarea.StyleState {
		return textarea.StyleState{
			Base: base,
			Text: text,
			// No background here: a tinted cursor line is what made the typed
			// line unreadable, and it buys nothing in a three-row prompt.
			CursorLine:       text,
			CursorLineNumber: base.Foreground(p.Dim),
			LineNumber:       base.Foreground(p.Faint),
			// The filler past the end of the buffer is meant to be invisible,
			// so it is painted in the background colour deliberately.
			EndOfBuffer: base.Foreground(p.Bg),
			Placeholder: base.Foreground(p.Faint),
			Prompt:      prompt,
			Selection:   base.Foreground(p.Fg).Background(p.Border),
		}
	}

	return textarea.Styles{
		Focused: state(base.Foreground(p.Fg), base.Foreground(p.Accent)),
		Blurred: state(base.Foreground(p.Dim), base.Foreground(p.Faint)),
		Cursor:  textarea.CursorStyle{Color: p.Accent, Shape: tea.CursorBar, Blink: true},
	}
}

// textinputStyles does the same for the single-line inputs behind the pickers
// and the find-in-file bar.
func textinputStyles(st *theme.Styles) textinput.Styles {
	p := st.P
	base := lipgloss.NewStyle()

	state := func(text, prompt lipgloss.Style) textinput.StyleState {
		return textinput.StyleState{
			Text:        text,
			Prompt:      prompt,
			Placeholder: base.Foreground(p.Faint),
			Suggestion:  base.Foreground(p.Faint),
		}
	}

	return textinput.Styles{
		Focused: state(base.Foreground(p.Fg), base.Foreground(p.Accent)),
		Blurred: state(base.Foreground(p.Dim), base.Foreground(p.Faint)),
		Cursor:  textinput.CursorStyle{Color: p.Accent, Shape: tea.CursorBar, Blink: true},
	}
}
