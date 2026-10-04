package main

import (
	"image/color"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// Term is the screen the core draws on.
//
// The core is agent-tui itself, unchanged, running in a pseudo-console. What it
// prints is VT: escape sequences that move a cursor around a grid of cells.
// This holds the emulator that turns those sequences back into the grid, and
// the one lock that two goroutines meet at — the reader feeding it output and
// the frame drawing it.
//
// Everything that touches the emulator's state goes through mu, including the
// encoding of keys and mouse events: how a key is encoded depends on modes the
// core has set, and those are written by the reader.
type Term struct {
	mu   sync.Mutex
	emu  *vt.Emulator
	cols int
	rows int

	title         string
	cursorVisible bool

	// gen counts writes, so a frame that has nothing new to draw can tell.
	gen uint64
}

// NewTerm makes a screen of the given size in the given colours.
func NewTerm(cols, rows int, fg, bg color.Color) *Term {
	t := &Term{cols: cols, rows: rows, cursorVisible: true}
	t.emu = vt.NewEmulator(cols, rows)
	t.emu.SetDefaultForegroundColor(fg)
	t.emu.SetDefaultBackgroundColor(bg)
	t.emu.SetCallbacks(vt.Callbacks{
		// Called from inside Write, with mu already held by the writer.
		Title:            func(s string) { t.title = s },
		CursorVisibility: func(v bool) { t.cursorVisible = v },
	})
	return t
}

// Write feeds output from the core.
func (t *Term) Write(p []byte) {
	t.mu.Lock()
	_, _ = t.emu.Write(p)
	t.gen++
	t.mu.Unlock()
}

// Read returns what the screen has to say back to the core: keys, mouse
// events, and answers to the queries the core makes about the terminal. It
// blocks, and it reads a pipe of its own, so it takes no lock.
func (t *Term) Read(p []byte) (int, error) { return t.emu.Read(p) }

// Resize changes the grid. The pseudo-console is resized separately, by the
// caller, so that the core is told after the grid it will draw on exists.
func (t *Term) Resize(cols, rows int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if cols == t.cols && rows == t.rows {
		return false
	}
	t.cols, t.rows = cols, rows
	t.emu.Resize(cols, rows)
	t.gen++
	return true
}

// Key, Mouse and Paste encode input under the lock, for the reason above.
func (t *Term) Key(k uv.KeyPressEvent) {
	t.mu.Lock()
	if seq, ok := modifiedKey(k); ok {
		t.emu.SendText(seq)
	} else {
		t.emu.SendKey(k)
	}
	t.mu.Unlock()
}

func (t *Term) Mouse(m vt.Mouse) {
	t.mu.Lock()
	t.emu.SendMouse(m)
	t.mu.Unlock()
}

func (t *Term) Text(s string) {
	t.mu.Lock()
	t.emu.SendText(s)
	t.mu.Unlock()
}

func (t *Term) Paste(s string) {
	t.mu.Lock()
	t.emu.Paste(s)
	t.mu.Unlock()
}

// Close ends the emulator, which ends a Read that is waiting on it.
func (t *Term) Close() { _ = t.emu.Close() }
