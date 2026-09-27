package ui

import (
	"sort"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"

	"github.com/phanngoc/agent-tui/internal/highlight"
	"github.com/phanngoc/agent-tui/internal/theme"
)

// Changing the colours without leaving.
//
// A theme is the one part of a tool that is purely a matter of taste, and
// taste is found by looking rather than by reasoning: you change a hex value,
// you look, you change it again. A theme you have to restart to see is a theme
// you tune three times and then stop tuning.
//
// So `/theme <name>` rebuilds the styles in place. What that costs is every
// cached frame in the program, which is the point of saying it out loud here:
// the caches are keyed on content and size, not on colour, so a palette that
// changed underneath them would be a screen half in each.

// setTheme switches the palette, or says why it cannot.
func (m *Model) setTheme(name string) {
	if name == "" {
		m.notice = "theme: " + m.themeName() + " · " + strings.Join(m.themeChoices(), " ")
		return
	}
	p, err := theme.Resolve(name)
	if err != nil {
		// The reader wrote the file; the reader can fix it. What they need is
		// which colour and by how much, which is what Resolve says.
		m.notice = "theme: " + err.Error()
		return
	}
	m.applyTheme(name, p)
	m.notice = "theme: " + name
}

// applyTheme rebuilds everything that was built from the old palette.
//
// Styles are made once at startup and held by the things that use them, so
// swapping the palette means handing the new ones to each of those again —
// and then throwing away every cached frame, because a cache keyed on content
// and size knows nothing about colour and would hand back the old one.
func (m *Model) applyTheme(name string, p theme.Palette) {
	m.themeSet = name
	m.st = theme.New(p)
	m.sc = highlight.NewScheme(p.Fg, p.Keyword, p.Type, p.String,
		p.Number, p.Comment, p.Func, p.Punct)
	m.loader.Recolour(m.sc)

	m.spin = spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(m.st.Warn))
	ta := textareaStyles(m.st)
	m.input.SetStyles(ta)
	in := textinputStyles(m.st)
	for _, ti := range []*textinput.Model{&m.finderIn, &m.grepIn, &m.findIn, &m.renameIn, &m.recallIn} {
		ti.SetStyles(in)
	}

	m.forgetFrames()
}

// forgetFrames drops every remembered rendering. Colour is not in any of their
// keys, and it is cheaper to draw one frame again than to put it in all of
// them for the one moment it changes.
func (m *Model) forgetFrames() {
	m.invalidateChat()
	m.chatSet, m.chatVer = "", m.chatVer+1
	m.chatViewOut, m.promptOut = "", ""
	m.sessKey, m.sessRows = "", nil
	m.paneOut = nil
}

// themeName is the palette in use.
func (m *Model) themeName() string {
	if m.themeSet != "" {
		return m.themeSet
	}
	if m.cfg.Theme != "" {
		return m.cfg.Theme
	}
	return theme.Names[0]
}

// themeChoices is every theme that can be asked for: the ones compiled in and
// the ones on this machine.
func (m *Model) themeChoices() []string {
	out := append([]string(nil), theme.Names...)
	out = append(out, theme.Installed()...)
	sort.Strings(out)
	return out
}
