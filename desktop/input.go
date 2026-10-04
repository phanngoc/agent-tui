package main

import (
	"strconv"
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

// modifiedKey encodes a named key held with Ctrl or Shift, which the
// emulator does not: it matches keys exactly, has a case for Shift+Tab and
// none for the rest, and writes nothing for a key it has no case for. So
// Ctrl+Backspace, Ctrl+Delete, Ctrl+arrows and Shift+arrows — deleting and
// moving by word, selecting in the prompt — reached the core as nothing.
//
// They are written the way xterm writes them: CSI with a modifier parameter,
// 1 plus 1 for Shift, 2 for Alt and 4 for Ctrl. The pseudo-console reads
// those and hands the core the key with its modifiers.
//
// Backspace and Enter have no such form, and what does reach the core was
// measured rather than assumed — a probe in a pseudo-console, reading keys
// the way the core does:
//
//   - Ctrl+Backspace arrives as ctrl+h whatever is sent, which is also what
//     Windows Terminal delivers, and what the core reads as deleting a word.
//     Sent as the byte 0x08 it brings a stray ctrl+space along; sent in the
//     console's own encoding (win32-input-mode) it arrives alone.
//   - Shift or Ctrl on Enter does not survive at all: the core sees enter,
//     and sends. So they go as Alt+Enter, the newline the core already knows.
func modifiedKey(k uv.KeyPressEvent) (string, bool) {
	if k.Mod&(uv.ModCtrl|uv.ModShift) == 0 {
		return "", false // plain, or Alt alone: the emulator's ESC prefix is right
	}
	mod := 1
	if k.Mod&uv.ModShift != 0 {
		mod++
	}
	if k.Mod&uv.ModAlt != 0 {
		mod += 2
	}
	if k.Mod&uv.ModCtrl != 0 {
		mod += 4
	}
	m := strconv.Itoa(mod)
	switch k.Code {
	case uv.KeyUp:
		return "\x1b[1;" + m + "A", true
	case uv.KeyDown:
		return "\x1b[1;" + m + "B", true
	case uv.KeyRight:
		return "\x1b[1;" + m + "C", true
	case uv.KeyLeft:
		return "\x1b[1;" + m + "D", true
	case uv.KeyHome:
		return "\x1b[1;" + m + "H", true
	case uv.KeyEnd:
		return "\x1b[1;" + m + "F", true
	case uv.KeyInsert:
		return "\x1b[2;" + m + "~", true
	case uv.KeyDelete:
		return "\x1b[3;" + m + "~", true
	case uv.KeyPgUp:
		return "\x1b[5;" + m + "~", true
	case uv.KeyPgDown:
		return "\x1b[6;" + m + "~", true
	case uv.KeyF1:
		return "\x1b[1;" + m + "P", true
	case uv.KeyF2:
		return "\x1b[1;" + m + "Q", true
	case uv.KeyF3:
		return "\x1b[1;" + m + "R", true
	case uv.KeyF4:
		return "\x1b[1;" + m + "S", true
	case uv.KeyBackspace:
		if k.Mod&uv.ModCtrl == 0 {
			return "\x7f", true // Shift+Backspace is a backspace
		}
		return win32Key(0x08, 0x0e, 0x08, k.Mod), true
	case uv.KeyEnter:
		return "\x1b\r", true
	}
	if n, ok := fnTilde[k.Code]; ok {
		return "\x1b[" + strconv.Itoa(n) + ";" + m + "~", true
	}
	return "", false
}

// fnTilde are the function keys xterm writes as CSI n ~.
var fnTilde = map[rune]int{
	uv.KeyF5: 15, uv.KeyF6: 17, uv.KeyF7: 18, uv.KeyF8: 19,
	uv.KeyF9: 20, uv.KeyF10: 21, uv.KeyF12: 24,
}

// win32Key is one key press in win32-input-mode: virtual key, scan code,
// character, down, control-key state, repeat count.
func win32Key(vk, scan, char int, mods uv.KeyMod) string {
	const (
		leftAlt  = 0x0002
		leftCtrl = 0x0008
		shift    = 0x0010
	)
	state := 0
	if mods&uv.ModAlt != 0 {
		state |= leftAlt
	}
	if mods&uv.ModCtrl != 0 {
		state |= leftCtrl
	}
	if mods&uv.ModShift != 0 {
		state |= shift
	}
	return "\x1b[" + strconv.Itoa(vk) + ";" + strconv.Itoa(scan) + ";" + strconv.Itoa(char) +
		";1;" + strconv.Itoa(state) + ";1_"
}
