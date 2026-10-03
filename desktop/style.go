package main

import (
	"image/color"
	"os"
	"path/filepath"
	"runtime/debug"

	"gioui.org/font"
	"gioui.org/font/opentype"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/image/font/gofont/gomono"
)

// Fonts and colours.
//
// The grid is drawn in Cascadia Mono, the face Windows 11 ships for exactly
// this — a terminal — so it is on every machine this runs on and costs nothing
// to bundle. Anything it has no glyph for falls through to Segoe UI Symbol,
// for the marks a TUI is full of, and — once the first one is drawn — to MS
// Gothic for Japanese, monospaced so a double-width character fills its two
// cells. Go Mono is compiled in and stands in only if Cascadia Mono is gone.

// monoFace is the typeface the grid asks for.
const monoFace = "Cascadia Mono"

// Font memory is most of this program's memory, so it is spent only when a
// face is needed. Parsing a face reads its every glyph outline, and MS Gothic
// is tens of thousands of them, three times over — the file holds three faces.
// Measured, that was 45 MB of a 56 MB heap for a screen with no Japanese on
// it. So the faces Latin text needs are loaded at start, and the CJK face is
// loaded the first time a CJK character is drawn — one face of the three.

// loadFaces reads the faces every screen needs. A face that is not there is
// skipped: a machine without one is a machine that draws a little less well,
// not one that fails to start.
func loadFaces() []font.FontFace {
	out := readFaces("CascadiaMono.ttf", 0)
	if len(out) == 0 {
		// Go Mono is compiled in, so the grid always has a monospaced face;
		// it is parsed only when the one it stands in for is missing.
		if f, err := opentype.Parse(gomono.TTF); err == nil {
			out = append(out, font.FontFace{Font: font.Font{Typeface: monoFace}, Face: f})
		}
	}
	return append(out, readFaces("seguisym.ttf", 0)...)
}

// loadCJK reads the face for Chinese, Japanese and Korean characters: MS
// Gothic, which is monospaced, so a double-width character fills its cells.
func loadCJK() []font.FontFace { return readFaces("msgothic.ttc", 1) }

// readFaces parses a font file from the Windows font folder, keeping the
// first limit faces of a collection, or all of them when limit is zero.
func readFaces(name string, limit int) []font.FontFace {
	b, err := os.ReadFile(filepath.Join(os.Getenv("WINDIR"), "Fonts", name))
	if err != nil {
		return nil
	}
	faces, err := opentype.ParseCollection(b)
	if err != nil {
		return nil
	}
	if limit > 0 && len(faces) > limit {
		faces = faces[:limit]
	}
	return faces
}

// isCJK reports whether a character is in the scripts the CJK face is for.
func isCJK(r rune) bool {
	switch {
	case r >= 0x2E80 && r <= 0x9FFF, // radicals, kana, CJK symbols, ideographs
		r >= 0xAC00 && r <= 0xD7AF, // Hangul
		r >= 0xF900 && r <= 0xFAFF, // compatibility ideographs
		r >= 0xFF00 && r <= 0xFFEF, // full-width forms
		r >= 0x20000 && r <= 0x3FFFF:
		return true
	}
	return false
}

// Palette is the screen's colours. Default foreground and background are the
// ones agent-tui's own default theme, One Dark, paints with, so the margin
// around the grid is the same colour as the grid.
type Palette struct {
	Fg, Bg, Cursor color.NRGBA
	ANSI           [16]color.NRGBA
}

var oneDark = Palette{
	Fg:     rgb(0xb9c0cc),
	Bg:     rgb(0x282c34),
	Cursor: rgb(0x61afef),
	ANSI: [16]color.NRGBA{
		rgb(0x282c34), rgb(0xe06c75), rgb(0x98c379), rgb(0xe5c07b),
		rgb(0x61afef), rgb(0xc678dd), rgb(0x56b6c2), rgb(0xabb2bf),
		rgb(0x5c6370), rgb(0xe06c75), rgb(0x98c379), rgb(0xe5c07b),
		rgb(0x61afef), rgb(0xc678dd), rgb(0x56b6c2), rgb(0xffffff),
	},
}

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

// resolve turns a cell's colour into a paintable one. Nil is the default; the
// sixteen named colours come from the palette, so a theme decides them; the
// rest — the 256-colour cube and 24-bit colour — are what they say they are.
func (p *Palette) resolve(c color.Color, def color.NRGBA) color.NRGBA {
	switch v := c.(type) {
	case nil:
		return def
	case ansi.BasicColor:
		if int(v) < len(p.ANSI) {
			return p.ANSI[v]
		}
	case ansi.IndexedColor:
		if int(v) < len(p.ANSI) {
			return p.ANSI[v]
		}
	case color.NRGBA:
		return v
	}
	r, g, b, a := c.RGBA()
	if a == 0 {
		return def
	}
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xff}
}

// dim halves a colour toward the background, for faint text.
func dim(c, bg color.NRGBA) color.NRGBA {
	mix := func(a, b uint8) uint8 { return uint8((uint16(a)*3 + uint16(b)*2) / 5) }
	return color.NRGBA{R: mix(c.R, bg.R), G: mix(c.G, bg.G), B: mix(c.B, bg.B), A: 0xff}
}

// freeMemory hands back to Windows what parsing a font left behind. Parsing
// allocates far more than it keeps, and the Go runtime returns freed memory
// to the system only gradually; after a one-off load like this, at once.
func freeMemory() { debug.FreeOSMemory() }
