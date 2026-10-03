package main

import (
	"strings"
	"unicode/utf8"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// Input, from the window to the core.
//
// Keys that mean something by themselves — arrows, Enter, Escape, function
// keys, and anything held with Ctrl or Alt — are taken as keys and encoded as
// a terminal would encode them. Everything else is text, and text arrives the
// way an input method delivers it: as edits to a short buffer.
//
// That buffer is what makes Vietnamese work. Telex and VNI do not type "ê",
// they type "e", then "e" again, and replace the first with "ê"; a Japanese
// input method composes a whole word and then commits it. Each edit is turned
// into what a person would have typed to make the same change — backspaces
// for what was replaced, then the new text — which is exactly what the core's
// prompt understands, because it is how Unikey has always done it.

// specialKeys are the named keys taken as keys whatever the modifiers.
var specialKeys = map[key.Name]rune{
	key.NameUpArrow:        uv.KeyUp,
	key.NameDownArrow:      uv.KeyDown,
	key.NameLeftArrow:      uv.KeyLeft,
	key.NameRightArrow:     uv.KeyRight,
	key.NameReturn:         uv.KeyEnter,
	key.NameEnter:          uv.KeyEnter,
	key.NameEscape:         uv.KeyEscape,
	key.NameTab:            uv.KeyTab,
	key.NameDeleteBackward: uv.KeyBackspace,
	key.NameDeleteForward:  uv.KeyDelete,
	key.NameHome:           uv.KeyHome,
	key.NameEnd:            uv.KeyEnd,
	key.NamePageUp:         uv.KeyPgUp,
	key.NamePageDown:       uv.KeyPgDown,
	key.NameF1:             uv.KeyF1, key.NameF2: uv.KeyF2, key.NameF3: uv.KeyF3,
	key.NameF4: uv.KeyF4, key.NameF5: uv.KeyF5, key.NameF6: uv.KeyF6,
	key.NameF7: uv.KeyF7, key.NameF8: uv.KeyF8, key.NameF9: uv.KeyF9,
	key.NameF10: uv.KeyF10, key.NameF12: uv.KeyF12,
}

// chordKeys are the keys taken as keys only with Ctrl or Alt held; without,
// they are text.
const chordKeys = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789[]\\;',./-=`"

// keyFilters lists every key the screen takes as a key.
func keyFilters(tag event.Tag) []event.Filter {
	all := key.ModCtrl | key.ModShift | key.ModAlt
	fs := []event.Filter{key.FocusFilter{Target: tag}}
	for name := range specialKeys {
		fs = append(fs, key.Filter{Focus: tag, Name: name, Optional: all})
	}
	// F11 is the window's own: full screen.
	fs = append(fs, key.Filter{Focus: tag, Name: key.NameF11, Optional: all})
	fs = append(fs,
		key.Filter{Focus: tag, Name: key.NameSpace, Required: key.ModCtrl, Optional: all},
		key.Filter{Focus: tag, Name: key.NameSpace, Required: key.ModAlt, Optional: all},
	)
	for _, r := range chordKeys {
		n := key.Name(string(r))
		fs = append(fs,
			key.Filter{Focus: tag, Name: n, Required: key.ModCtrl, Optional: all},
			key.Filter{Focus: tag, Name: n, Required: key.ModAlt, Optional: key.ModShift},
		)
	}
	return fs
}

func uvMods(m key.Modifiers) uv.KeyMod {
	var out uv.KeyMod
	if m.Contain(key.ModCtrl) {
		out |= uv.ModCtrl
	}
	if m.Contain(key.ModAlt) {
		out |= uv.ModAlt
	}
	if m.Contain(key.ModShift) {
		out |= uv.ModShift
	}
	return out
}

// keyEvent turns a pressed key into the terminal's idea of it. ok is false
// for keys the screen does not pass on.
func keyEvent(e key.Event) (uv.KeyPressEvent, bool) {
	mods := uvMods(e.Modifiers)
	if code, ok := specialKeys[e.Name]; ok {
		return uv.KeyPressEvent{Code: code, Mod: mods}, true
	}
	if e.Name == key.NameSpace {
		return uv.KeyPressEvent{Code: uv.KeySpace, Mod: mods}, true
	}
	if r, size := utf8.DecodeRuneInString(string(e.Name)); size == len(e.Name) && size > 0 {
		// Ctrl+letter is a control code, which is the letter in lower case;
		// shift is part of the chord, not of the letter.
		return uv.KeyPressEvent{Code: []rune(strings.ToLower(string(r)))[0], Mod: mods}, true
	}
	return uv.KeyPressEvent{}, false
}

// imeBuffer is the text an input method is editing: the last few characters
// typed, which is as far back as any composition reaches.
type imeBuffer struct {
	text []rune
}

const imeKeep = 32

// apply performs an edit and returns what to send to make the same change on
// the core's side: one backspace for each character replaced, then the text.
func (b *imeBuffer) apply(e key.EditEvent) (backspaces int, insert string) {
	start, end := clampRange(e.Range.Start, e.Range.End, len(b.text))
	// What is replaced is everything from start to the end of the buffer —
	// the caret is always at the end of what has been typed here — and the
	// tail after end is typed again.
	tail := string(b.text[end:])
	backspaces = len(b.text) - start
	insert = e.Text + tail

	next := append([]rune{}, b.text[:start]...)
	next = append(next, []rune(e.Text)...)
	next = append(next, []rune(tail)...)
	if len(next) > imeKeep {
		next = next[len(next)-imeKeep:]
	}
	b.text = next
	return backspaces, insert
}

// reset forgets the buffer: a key that is not text — Enter, an arrow — moves
// the caret somewhere the buffer no longer describes.
func (b *imeBuffer) reset() { b.text = b.text[:0] }

func (b *imeBuffer) len() int { return len(b.text) }

func clampRange(s, e, n int) (int, int) {
	if s > e {
		s, e = e, s
	}
	s = max(0, min(s, n))
	e = max(0, min(e, n))
	return s, e
}

// mouseButton maps a pointer's buttons onto the terminal's.
func mouseButton(b pointer.Buttons) vt.MouseButton {
	switch {
	case b.Contain(pointer.ButtonPrimary):
		return vt.MouseLeft
	case b.Contain(pointer.ButtonSecondary):
		return vt.MouseRight
	case b.Contain(pointer.ButtonTertiary):
		return vt.MouseMiddle
	}
	return vt.MouseNone
}
