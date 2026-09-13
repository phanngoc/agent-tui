package theme

import (
	"image/color"
	"math"
	"testing"
)

// relLuminance is the WCAG 2.1 relative luminance of a colour.
func relLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	lin := func(v uint32) float64 {
		f := float64(v>>8) / 255
		if f <= 0.04045 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// contrast is the WCAG 2.1 contrast ratio between two colours.
func contrast(fg, bg color.Color) float64 {
	a, b := relLuminance(fg), relLuminance(bg)
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

// TestPaletteIsReadable pins the contrast of every colour that renders text.
//
// The palette was previously tuned by eye and shipped with comment text at
// 3.3:1 and the "faint" tone at 2.5:1, both of which disappear into the
// background on a real terminal. Measuring it here means a future edit has to
// stay legible.
func TestPaletteIsReadable(t *testing.T) {
	p := Dark
	cases := []struct {
		name string
		fg   color.Color
		min  float64
	}{
		// Body copy and source text: AAA, because this is what gets read.
		{"Fg", p.Fg, 7},
		{"Keyword", p.Keyword, 7},
		{"Type", p.Type, 7},
		{"String", p.String, 7},
		{"Number", p.Number, 7},
		{"Func", p.Func, 7},
		{"Punct", p.Punct, 7},
		{"Dim", p.Dim, 7},
		// Supporting tones: AA, they are meant to recede but still be read.
		{"Faint", p.Faint, 4.5},
		{"Comment", p.Comment, 4.5},
		{"Accent", p.Accent, 4.5},
		{"Good", p.Good, 4.5},
		{"Warn", p.Warn, 4.5},
		{"Bad", p.Bad, 4.5},
	}
	for _, c := range cases {
		if got := contrast(c.fg, p.Bg); got < c.min {
			t.Errorf("%s contrast is %.2f:1 against the background, want at least %.1f:1",
				c.name, got, c.min)
		}
	}
}

// TestSelectedRowIsReadable checks the one place text sits on something other
// than the window background.
func TestSelectedRowIsReadable(t *testing.T) {
	p := Dark
	if got := contrast(p.Fg, p.Border); got < 4.5 {
		t.Errorf("selected-row text is %.2f:1 against its highlight, want at least 4.5:1", got)
	}
	if got := contrast(p.Dim, p.BgAlt); got < 4.5 {
		t.Errorf("status-bar text is %.2f:1 against the status bar, want at least 4.5:1", got)
	}
}

// TestEveryTextStyleSetsAForeground guards the actual bug from the screenshot:
// a style with no foreground inherits the terminal's default, which on a dark
// theme can be all but invisible.
func TestEveryTextStyleSetsAForeground(t *testing.T) {
	s := New(Dark)
	for name, style := range map[string]interface{ GetForeground() color.Color }{
		"App":       &s.App,
		"Body":      &s.Body,
		"Dim":       &s.Dim,
		"Faint":     &s.Faint,
		"Accent":    &s.Accent,
		"Good":      &s.Good,
		"Warn":      &s.Warn,
		"Bad":       &s.Bad,
		"Title":     &s.Title,
		"TitleOn":   &s.TitleOn,
		"Gutter":    &s.Gutter,
		"GutterOn":  &s.GutterOn,
		"UserTag":   &s.UserTag,
		"AgentTag":  &s.AgentTag,
		"ToolTag":   &s.ToolTag,
		"ErrTag":    &s.ErrTag,
		"MatchChar": &s.MatchChar,
	} {
		if style.GetForeground() == nil {
			t.Errorf("style %s has no foreground; it would fall back to the terminal default", name)
		}
	}
}
