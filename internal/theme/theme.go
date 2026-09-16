// Package theme holds the single source of truth for colours and styles.
// Styles are built once at startup and reused; nothing here allocates per frame.
package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Palette is a dark, low-contrast scheme tuned for long sessions.
type Palette struct {
	Bg, BgAlt, Border, BorderOn color.Color
	Fg, Dim, Faint              color.Color
	Accent, Good, Warn, Bad     color.Color

	// syntax
	Keyword, Type, String, Number, Comment, Func, Punct color.Color
}

// Dark is tuned for contrast against its own background rather than by eye.
// Every colour used for text clears WCAG AA (4.5:1), body text clears AAA (7:1),
// and the ratios are asserted in the package's tests so a future palette edit
// cannot quietly make the UI unreadable again.
var Dark = Palette{
	Bg:       lipgloss.Color("#0d1017"),
	BgAlt:    lipgloss.Color("#161c26"),
	Border:   lipgloss.Color("#2e3646"),
	BorderOn: lipgloss.Color("#5b90ff"),
	Fg:       lipgloss.Color("#dbe2ee"),
	Dim:      lipgloss.Color("#a7b4c9"),
	Faint:    lipgloss.Color("#78859c"),
	Accent:   lipgloss.Color("#5b90ff"),
	Good:     lipgloss.Color("#5cd6ae"),
	Warn:     lipgloss.Color("#edc255"),
	Bad:      lipgloss.Color("#ff7b84"),

	Keyword: lipgloss.Color("#d3a3f0"),
	Type:    lipgloss.Color("#8fe3d3"),
	String:  lipgloss.Color("#cbeda0"),
	Number:  lipgloss.Color("#fb9d80"),
	Comment: lipgloss.Color("#8593a6"),
	Func:    lipgloss.Color("#93b6ff"),
	Punct:   lipgloss.Color("#aabbd4"),
}

// Styles are pre-rendered lipgloss styles used across the UI.
type Styles struct {
	P Palette

	App        lipgloss.Style
	Pane       lipgloss.Style
	PaneActive lipgloss.Style
	Title      lipgloss.Style
	TitleOn    lipgloss.Style

	Body                                lipgloss.Style
	Dim, Faint, Accent, Good, Warn, Bad lipgloss.Style
	Bold                                lipgloss.Style

	Status    lipgloss.Style
	StatusKey lipgloss.Style

	TabOn, TabOff lipgloss.Style

	UserTag, AgentTag, ToolTag, ErrTag, ShellTag lipgloss.Style
	UserBar, AgentBar, ToolBar, ShellBar         lipgloss.Style

	Gutter, GutterOn lipgloss.Style
	Match, MatchOn   lipgloss.Style

	Overlay     lipgloss.Style
	SelRow      lipgloss.Style
	SelRowDim   lipgloss.Style
	MatchChar   lipgloss.Style
	Placeholder lipgloss.Style
}

// New builds every style once.
func New(p Palette) *Styles {
	s := &Styles{P: p}
	base := lipgloss.NewStyle()

	// Everything that renders text starts from the foreground colour. Leaving
	// it unset would fall back to the terminal's default, which on a dark theme
	// is often barely visible against this background.
	s.App = base.Foreground(p.Fg)
	s.Body = base.Foreground(p.Fg)
	s.Pane = base.Border(lipgloss.RoundedBorder()).BorderForeground(p.Border)
	s.PaneActive = base.Border(lipgloss.RoundedBorder()).BorderForeground(p.BorderOn)
	s.Title = base.Foreground(p.Dim).Bold(true)
	s.TitleOn = base.Foreground(p.Accent).Bold(true)

	s.Dim = base.Foreground(p.Dim)
	s.Faint = base.Foreground(p.Faint)
	s.Accent = base.Foreground(p.Accent)
	s.Good = base.Foreground(p.Good)
	s.Warn = base.Foreground(p.Warn)
	s.Bad = base.Foreground(p.Bad)
	s.Bold = base.Bold(true)

	s.Status = base.Foreground(p.Dim).Background(p.BgAlt)
	s.StatusKey = base.Foreground(p.Fg).Background(p.BgAlt).Bold(true)

	s.TabOn = base.Foreground(p.Bg).Background(p.Accent).Bold(true).Padding(0, 1)
	s.TabOff = base.Foreground(p.Dim).Padding(0, 1)

	s.UserTag = base.Foreground(p.Accent).Bold(true)
	s.AgentTag = base.Foreground(p.Good).Bold(true)
	s.ToolTag = base.Foreground(p.Warn).Bold(true)
	s.ErrTag = base.Foreground(p.Bad).Bold(true)
	s.UserBar = base.Foreground(p.Accent)
	s.AgentBar = base.Foreground(p.Good)
	s.ToolBar = base.Foreground(p.Faint)
	// A command the user ran is neither the user talking nor the agent working,
	// so it gets a rail of its own rather than borrowing one of theirs.
	s.ShellTag = base.Foreground(p.Warn).Bold(true)
	s.ShellBar = base.Foreground(p.Warn)

	s.Gutter = base.Foreground(p.Faint)
	s.GutterOn = base.Foreground(p.Accent).Bold(true)
	s.Match = base.Foreground(p.Bg).Background(p.Warn)
	s.MatchOn = base.Foreground(p.Bg).Background(p.Accent).Bold(true)

	s.Overlay = base.Border(lipgloss.RoundedBorder()).BorderForeground(p.Accent).Background(p.BgAlt)
	s.SelRow = base.Foreground(p.Fg).Background(p.Border)
	s.SelRowDim = base.Foreground(p.Dim)
	s.MatchChar = base.Foreground(p.Accent).Bold(true)
	s.Placeholder = base.Foreground(p.Faint)
	return s
}
