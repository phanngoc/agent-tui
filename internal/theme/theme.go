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
	// Text is the tone the conversation is read in, a step below Fg.
	//
	// Three greys with three jobs: Fg is emphasis and chrome — a heading, a
	// bolded phrase, a pane title — Text is the prose those sit in, and Dim is
	// what stands beside it, a timestamp or a path. Reading an answer should
	// not be reading at the brightest thing on the screen; what is brightest
	// should be the part the answer is pointing at.
	Text                    color.Color
	Accent, Good, Warn, Bad color.Color
	// Sel is behind text the mouse has selected: far enough from the
	// background to be unmistakable, near enough that a screenful of selected
	// text is not a lamp.
	Sel color.Color
	// Row is behind the conversation the prompt is talking to. It is a second
	// background beside Sel, because the two answer different questions —
	// which conversation is active, and which one the cursor is over — and
	// they are often not the same row.
	Row color.Color
	// AddBg and DelBg tint a whole changed row in a diff. A block of added
	// lines then has a shape you can take in without reading it, which is what
	// skimming a patch actually is.
	AddBg, DelBg color.Color

	// syntax
	Keyword, Type, String, Number, Comment, Func, Punct color.Color
}

// Dark is Monokai, softened for a screen that is read for hours.
//
// What it replaced was a cold near-black with bright cool text on it:
// excellent contrast by the numbers and tiring to sit in front of, because
// what makes a screen hard is not the ratio but the range — near-white at 14:1
// on near-black is a lamp. The background comes up to Monokai's warm grey and
// the foreground comes down to meet it, which puts body text around 11:1:
// still AAA, no longer a lamp.
//
// Every colour that renders text clears WCAG AA (4.5:1), and the two that are
// read continuously clear AAA (7:1); the ratios are asserted in this package's
// tests so a future edit cannot quietly make the UI unreadable again. The
// syntax colours sit at AA rather than AAA deliberately: Monokai's pink
// keyword is 3.9:1 on its own background, and dragging it to 7:1 turns it
// pastel and stops it being Monokai. AA is the floor that keeps it legible,
// and below it is where the palette before last had gone wrong.
var Dark = Palette{
	Bg:       lipgloss.Color("#272822"),
	BgAlt:    lipgloss.Color("#32332b"),
	Border:   lipgloss.Color("#43453c"),
	BorderOn: lipgloss.Color("#66d9ef"),
	Fg:       lipgloss.Color("#e4e1d6"),
	Text:     lipgloss.Color("#cac7bc"),
	Dim:      lipgloss.Color("#bab7a8"),
	Faint:    lipgloss.Color("#979383"),
	Accent:   lipgloss.Color("#66d9ef"),
	Sel:      lipgloss.Color("#4b5162"),
	Row:      lipgloss.Color("#32332b"),
	Good:     lipgloss.Color("#a6e22e"),
	Warn:     lipgloss.Color("#e6db74"),
	Bad:      lipgloss.Color("#ff6188"),
	// Dark enough that the tinted rows read as a band rather than as a
	// highlight, and that everything drawn on them keeps its contrast.
	AddBg: lipgloss.Color("#2b3a1e"),
	DelBg: lipgloss.Color("#4a2431"),

	Keyword: lipgloss.Color("#ff6188"),
	Type:    lipgloss.Color("#66d9ef"),
	String:  lipgloss.Color("#e6db74"),
	Number:  lipgloss.Color("#bd9cff"),
	Comment: lipgloss.Color("#9a9484"),
	Func:    lipgloss.Color("#a6e22e"),
	Punct:   lipgloss.Color("#c8c5b6"),
}

// Herdr is the palette herdr wears, and the default here.
//
// It is Catppuccin Mocha, which is what herdr ships as its dark theme, with
// the same roles kept apart: one background for the sidebar, a second for the
// row the prompt is talking to, a third for the row the cursor is over. Those
// three being distinct is what lets the glyph column say what a conversation
// is doing without also having to say where you are standing.
//
// It measures like the Monokai one below — every colour that renders text
// clears AA, the three that are read continuously clear AAA, and nothing
// reaches the ceiling — which is the point of keeping the tests palette-blind:
// a theme is a set of colours, not a licence.
var Herdr = Palette{
	Bg:       lipgloss.Color("#1e1e2e"), // base
	BgAlt:    lipgloss.Color("#181825"), // mantle, the sidebar
	Border:   lipgloss.Color("#45475a"), // surface1
	BorderOn: lipgloss.Color("#89b4fa"),
	Fg:       lipgloss.Color("#cdd6f4"), // text
	Text:     lipgloss.Color("#bac2de"), // subtext1
	Dim:      lipgloss.Color("#a6adc8"), // subtext0
	Faint:    lipgloss.Color("#9399b2"), // overlay2
	Accent:   lipgloss.Color("#89b4fa"), // blue
	Good:     lipgloss.Color("#a6e3a1"), // green
	Warn:     lipgloss.Color("#f9e2af"), // yellow
	Bad:      lipgloss.Color("#f38ba8"), // red
	Sel:      lipgloss.Color("#45475a"), // surface1, the cursor
	Row:      lipgloss.Color("#313244"), // surface0, the active conversation
	AddBg:    lipgloss.Color("#26332c"),
	DelBg:    lipgloss.Color("#3a2530"),

	Keyword: lipgloss.Color("#cba6f7"), // mauve
	Type:    lipgloss.Color("#f9e2af"), // yellow
	String:  lipgloss.Color("#a6e3a1"), // green
	Number:  lipgloss.Color("#fab387"), // peach
	Comment: lipgloss.Color("#9399b2"), // overlay2
	Func:    lipgloss.Color("#89b4fa"), // blue
	Punct:   lipgloss.Color("#bac2de"), // subtext1
}

// ByName picks a palette. An unknown name falls back to the default rather
// than failing to start: a typo in a config file is not worth a dead terminal,
// and the name is echoed back in the status line anyway.
func ByName(name string) Palette {
	switch name {
	case "monokai":
		return Dark
	}
	return Herdr
}

// Names are the palettes ByName knows, for the config reference and for
// anything that offers a choice.
var Names = []string{"herdr", "monokai"}

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
	// Hover marks the row the pointer is on. Underlined rather than filled:
	// a row that lights up under the pointer competes with the row that is
	// actually selected, and only one of those is a decision.
	Hover          lipgloss.Style
	Match, MatchOn lipgloss.Style

	// Diff colours. The line carries the colour as foreground; the span that
	// actually changed inverts it, so the eye lands on the word rather than on
	// the fact that the line is one of the two colours.
	DiffAdd, DiffDel     lipgloss.Style
	DiffAddOn, DiffDelOn lipgloss.Style
	// The row variants carry the tint, so the gutter, the sign, the text and
	// the padding out to the edge are one band instead of three.
	DiffAddRow, DiffDelRow lipgloss.Style
	DiffHunk, DiffMeta     lipgloss.Style
	Ref, RefHead           lipgloss.Style

	// Markdown, as the transcript renders it. The agent writes markdown and the
	// terminal cannot, so every construct needs a colour to become instead of a
	// piece of punctuation to be read.
	MdHead, MdSub, MdRail               lipgloss.Style
	MdBold, MdItalic, MdBoldItalic      lipgloss.Style
	MdCode, MdLink, MdStrike            lipgloss.Style
	MdQuote, MdQuoteBar, MdRule, MdMark lipgloss.Style
	MdTableHead                         lipgloss.Style

	ActiveRow lipgloss.Style
	Overlay   lipgloss.Style
	// Select is text the mouse has selected. It is a background rather than a
	// tint: a selection that let the syntax show through would have to be dark
	// enough not to fight it, which is dark enough not to be seen.
	Select    lipgloss.Style
	SelRow    lipgloss.Style
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
	s.Body = base.Foreground(p.Text)
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

	s.Hover = base.Foreground(p.Fg).Underline(true)
	s.Gutter = base.Foreground(p.Faint)
	s.GutterOn = base.Foreground(p.Accent).Bold(true)
	s.DiffAdd = base.Foreground(p.Good)
	s.DiffDel = base.Foreground(p.Bad)
	s.DiffAddRow = base.Foreground(p.Good).Background(p.AddBg)
	s.DiffDelRow = base.Foreground(p.Bad).Background(p.DelBg)
	s.DiffAddOn = base.Foreground(p.Bg).Background(p.Good).Bold(true)
	s.DiffDelOn = base.Foreground(p.Bg).Background(p.Bad).Bold(true)
	s.DiffHunk = base.Foreground(p.Accent)
	s.DiffMeta = base.Foreground(p.Faint).Italic(true)
	s.Ref = base.Foreground(p.Bg).Background(p.Dim)
	s.RefHead = base.Foreground(p.Bg).Background(p.Accent).Bold(true)

	s.Match = base.Foreground(p.Bg).Background(p.Warn)
	s.MatchOn = base.Foreground(p.Bg).Background(p.Accent).Bold(true)

	// A heading keeps the rail-and-colour vocabulary the rest of the UI uses, so
	// a section title in a reply reads like a section title in a pane.
	s.MdHead = base.Foreground(p.Accent).Bold(true)
	// A deep heading keeps its case but takes the accent anyway. In a pane of
	// dense prose, bold alone does not separate a section title from a bolded
	// phrase inside a paragraph, and the section title is the thing being
	// looked for.
	s.MdSub = base.Foreground(p.Accent).Bold(true)
	s.MdRail = base.Foreground(p.Accent)
	s.MdBold = base.Foreground(p.Fg).Bold(true)
	s.MdItalic = base.Foreground(p.Fg).Italic(true)
	s.MdBoldItalic = base.Foreground(p.Fg).Bold(true).Italic(true)
	// Inline code borrows the preview's type colour: same idea, same tone.
	s.MdCode = base.Foreground(p.Type)
	s.MdLink = base.Foreground(p.Accent).Underline(true)
	s.MdStrike = base.Foreground(p.Faint).Strikethrough(true)
	s.MdQuote = base.Foreground(p.Dim).Italic(true)
	s.MdQuoteBar = base.Foreground(p.Faint)
	s.MdRule = base.Foreground(p.Faint)
	s.MdMark = base.Foreground(p.Accent)
	s.MdTableHead = base.Foreground(p.Accent).Bold(true)

	s.Overlay = base.Border(lipgloss.RoundedBorder()).BorderForeground(p.Accent).Background(p.BgAlt)
	s.ActiveRow = base.Background(p.Row)
	s.Select = base.Foreground(p.Fg).Background(p.Sel)
	s.SelRow = base.Foreground(p.Fg).Background(p.Border)
	s.SelRowDim = base.Foreground(p.Dim)
	s.MatchChar = base.Foreground(p.Accent).Bold(true)
	s.Placeholder = base.Foreground(p.Faint)
	return s
}
