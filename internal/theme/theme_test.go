package theme

import (
	"image/color"
	"math"
	"strings"
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
// The palette was once tuned by eye and shipped with comment text at 3.3:1 and
// the "faint" tone at 2.5:1, both of which disappear into the background on a
// real terminal. Measuring it here means a future edit has to stay legible.
//
// Two floors, and the difference between them is a judgement rather than a
// measurement. AAA is asked of the two tones that are read continuously — the
// prose of an answer and the prose beside it. Everything else is a colour
// applied to a word or two at a time, and AA is the standard for those; a
// syntax palette held to AAA has to be pastel, which is how the last one
// ended up bright enough to be tiring. Below AA is where it goes wrong, and
// that is the line this test exists to hold.
func TestPaletteIsReadable(t *testing.T) {
	p := Dark
	cases := []struct {
		name string
		fg   color.Color
		min  float64
	}{
		// Read continuously: AAA.
		{"Fg", p.Fg, 7},
		{"Text", p.Text, 7},
		{"Dim", p.Dim, 7},
		// A word at a time: AA, meant to be distinguishable rather than loud.
		{"Keyword", p.Keyword, 4.5},
		{"Type", p.Type, 4.5},
		{"String", p.String, 4.5},
		{"Number", p.Number, 4.5},
		{"Func", p.Func, 4.5},
		{"Punct", p.Punct, 4.5},
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

// TestDiffRowsAreReadable pins the tint on a changed row. It is a second
// background for text to sit on, so it gets measured like the first one.
func TestDiffRowsAreReadable(t *testing.T) {
	p := Dark
	for _, c := range []struct {
		name   string
		fg, bg color.Color
	}{
		{"added line on its tint", p.Good, p.AddBg},
		{"deleted line on its tint", p.Bad, p.DelBg},
		// A tint the eye cannot tell from the background is a tint that was
		// not worth drawing.
		{"the added tint against the window", p.AddBg, p.Bg},
		{"the deleted tint against the window", p.DelBg, p.Bg},
		// Selected text is read the same way the rest is, and the band behind
		// it has to be plainly a band: a selection you have to look for is a
		// selection you cannot trust you made.
		{"selected text on its band", p.Fg, p.Sel},
		{"the selection band against the window", p.Sel, p.Bg},
	} {
		got := contrast(c.fg, c.bg)
		switch {
		case strings.HasPrefix(c.name, "the "):
			if got < 1.06 {
				t.Errorf("%s is %.3f:1, too close to see", c.name, got)
			}
		case got < 4.5:
			t.Errorf("%s is %.2f:1, want at least 4.5:1", c.name, got)
		}
	}
}

// TestNothingIsGlaring is the other half of the palette's job. A ratio has a
// floor and no ceiling, and a screen read for hours is made hard by the top of
// the range as much as by the bottom: near-white on near-black measures
// beautifully and is a lamp.
func TestNothingIsGlaring(t *testing.T) {
	p := Dark
	if got := contrast(p.Fg, p.Bg); got > 12 {
		t.Errorf("the brightest tone is %.2f:1 against the background, a lamp to read by", got)
	}
	// And the prose sits below it, or the hierarchy is decoration: what is
	// brightest should be the part the answer is pointing at, not the answer.
	if contrast(p.Text, p.Bg) >= contrast(p.Fg, p.Bg) {
		t.Error("the conversation is read at the brightest tone on the screen")
	}
	if contrast(p.Text, p.Bg) <= contrast(p.Dim, p.Bg) {
		t.Error("the conversation is no brighter than the notes beside it")
	}
	// And the background itself is not black. The warmth is the point: it is
	// what the eye rests against between the words.
	if lum := relLuminance(p.Bg); lum < 0.01 {
		t.Errorf("the background has a relative luminance of %.4f, near enough to black", lum)
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
		"App":          &s.App,
		"Body":         &s.Body,
		"Dim":          &s.Dim,
		"Faint":        &s.Faint,
		"Accent":       &s.Accent,
		"Good":         &s.Good,
		"Warn":         &s.Warn,
		"Bad":          &s.Bad,
		"Title":        &s.Title,
		"TitleOn":      &s.TitleOn,
		"Gutter":       &s.Gutter,
		"GutterOn":     &s.GutterOn,
		"UserTag":      &s.UserTag,
		"AgentTag":     &s.AgentTag,
		"ToolTag":      &s.ToolTag,
		"ErrTag":       &s.ErrTag,
		"MatchChar":    &s.MatchChar,
		"Hover":        &s.Hover,
		"Select":       &s.Select,
		"MdHead":       &s.MdHead,
		"MdSub":        &s.MdSub,
		"MdRail":       &s.MdRail,
		"MdBold":       &s.MdBold,
		"MdItalic":     &s.MdItalic,
		"MdBoldItalic": &s.MdBoldItalic,
		"MdCode":       &s.MdCode,
		"MdLink":       &s.MdLink,
		"MdStrike":     &s.MdStrike,
		"MdQuote":      &s.MdQuote,
		"MdQuoteBar":   &s.MdQuoteBar,
		"MdRule":       &s.MdRule,
		"MdMark":       &s.MdMark,
		"MdTableHead":  &s.MdTableHead,
		"DiffAddRow":   &s.DiffAddRow,
		"DiffDelRow":   &s.DiffDelRow,
	} {
		if style.GetForeground() == nil {
			t.Errorf("style %s has no foreground; it would fall back to the terminal default", name)
		}
	}
}
