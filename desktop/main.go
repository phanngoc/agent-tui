// Command agent-tui-desktop is agent-tui in a window of its own.
//
// It is a native Windows 11 application written in Go, and it is a shell and
// nothing more: the agent, the panes, the keys are all agent-tui's, running
// unmodified in a pseudo-console. What this adds is everything a terminal
// would otherwise decide for it — the window, the font, how it is drawn —
// done properly, and done fast: drawn on the GPU through Gio, with a VT
// emulator in Go turning the core's output into a grid, and every row that
// has not changed replayed rather than redrawn.
package main

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"log"
	"os"
	"strings"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/io/transfer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

func main() {
	diagnostics()
	go func() {
		w := new(app.Window)
		st := loadState()
		w.Option(
			app.Title("agent-tui"),
			app.Size(unit.Dp(st.Width), unit.Dp(st.Height)),
			app.MinSize(unit.Dp(640), unit.Dp(400)),
		)
		if st.Maximized {
			w.Option(app.Maximized.Option())
		}
		s := newShell(w, st)
		if err := s.run(); err != nil {
			log.Println(err)
		}
		os.Exit(0)
	}()
	app.Main()
}

// shell is the window and what it holds.
type shell struct {
	w     *app.Window
	state *state

	term *Term
	grid *Grid
	core *Core
	err  error

	bin  string
	args []string
	dir  string

	ime       imeBuffer
	focused   bool
	asked     bool    // focus has been asked for
	styled    bool    // the window frame has been set up
	scrollAcc float32 // wheel travel not yet a whole notch
	scrollX   float32 // the same, sideways
	// notchEvents is how many wheel events one notch sends the core, which
	// scrolls three lines for each: Windows' lines-per-notch over three.
	notchEvents int
	lastCell    image.Point
	pressed     vt.MouseButton
	mode        app.WindowMode
	sizeDp      image.Point
	pxPerDp     float32
}

const pad = 6 // dp of margin around the grid

func newShell(w *app.Window, st *state) *shell {
	return &shell{
		w:     w,
		state: st,
		grid:  NewGrid(loadFaces(), oneDark),
		term:  NewTerm(120, 36, oneDark.Fg, oneDark.Bg),
		args:  os.Args[1:],
		dir:   startDir(),

		notchEvents: notchEvents(wheelLines()),
	}
}

// coreLinesPerWheel is how far the core scrolls for one wheel event.
const coreLinesPerWheel = 3

// notchEvents turns Windows' lines-per-notch into wheel events for the core:
// at the default of three, one. A setting of none is honoured as none.
func notchEvents(lines int) int {
	if lines <= 0 {
		return 0
	}
	return max(1, (lines+coreLinesPerWheel/2)/coreLinesPerWheel)
}

func (s *shell) start() {
	s.err = nil
	bin, err := findCore()
	if err != nil {
		s.err = err
		return
	}
	s.bin = bin
	s.core, s.err = StartCore(s.term, s.bin, s.args, s.dir, s.w.Invalidate)
}

func (s *shell) run() error {
	var ops op.Ops
	started := false
	for {
		switch e := s.w.Event().(type) {
		case app.DestroyEvent:
			if s.core != nil {
				s.core.Stop()
			}
			s.state.save()
			return e.Err

		case app.Win32ViewEvent:
			if e.HWND != 0 && !s.styled {
				s.styled = true
				styleWindow(e.HWND, oneDark.Bg)
				if !s.state.Maximized {
					centerWindow(e.HWND)
				}
			}

		case app.ConfigEvent:
			s.mode = e.Config.Mode
			s.state.Maximized = e.Config.Mode == app.Maximized

		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			s.pxPerDp = gtx.Metric.PxPerDp
			if s.mode == app.Windowed && s.pxPerDp > 0 {
				s.state.Width = int(float32(e.Size.X) / s.pxPerDp)
				s.state.Height = int(float32(e.Size.Y) / s.pxPerDp)
			}
			s.grid.SetPx(gtx.Sp(unit.Sp(s.state.FontSize)))
			s.fit(gtx)
			if !started {
				started = true
				s.start()
			}
			s.frame(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

// fit sizes the grid to the window, and tells the core when that changed.
func (s *shell) fit(gtx layout.Context) {
	cw, ch := s.grid.CellSize()
	p := gtx.Dp(pad)
	cols := max(20, int(float32(gtx.Constraints.Max.X-2*p)/cw))
	rows := max(5, (gtx.Constraints.Max.Y-2*p)/ch)
	if s.term.Resize(cols, rows) && s.core != nil {
		s.core.Resize(cols, rows)
	}
}

func (s *shell) frame(gtx layout.Context) {
	// The window is the terminal's background, edge to edge.
	paint.FillShape(gtx.Ops, oneDark.Bg, clip.Rect{Max: gtx.Constraints.Max}.Op())

	p := gtx.Dp(pad)
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, s)
	key.InputHintOp{Tag: s, Hint: key.HintAny}.Add(gtx.Ops)
	area.Pop()

	if !s.asked {
		s.asked = true
		gtx.Execute(key.FocusCmd{Tag: s})
	}
	s.handle(gtx, p)

	off := op.Offset(image.Pt(p, p)).Push(gtx.Ops)
	s.grid.Layout(gtx, s.term, s.focused)
	off.Pop()

	s.placeIME(gtx, p)

	if s.err != nil {
		s.banner(gtx, "agent-tui could not start", s.err.Error(), "Enter  try again        Esc  close")
	} else if s.core != nil {
		if done, code := s.core.Exited(); done {
			s.banner(gtx, "agent-tui has exited", fmt.Sprintf("exit code %d", code), "Enter  start again        Esc  close")
		}
	}
}

// handle drains this frame's input.
func (s *shell) handle(gtx layout.Context, p int) {
	filters := append(keyFilters(s),
		pointer.Filter{
			Target: s,
			Kinds:  pointer.Press | pointer.Release | pointer.Drag | pointer.Move | pointer.Scroll,
			// Both axes: without a horizontal range Gio drops sideways
			// scrolling before it arrives, and shift+wheel is sideways.
			ScrollX: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20},
			ScrollY: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20},
		},
		transfer.TargetFilter{Target: s, Type: "application/text"},
	)
	for {
		ev, ok := gtx.Event(filters...)
		if !ok {
			return
		}
		switch e := ev.(type) {
		case key.FocusEvent:
			s.focused = e.Focus
		case key.Event:
			if e.State == key.Press {
				s.onKey(gtx, e)
			}
		case key.EditEvent:
			s.onEdit(gtx, e)
		case pointer.Event:
			s.onPointer(gtx, e, p)
		case transfer.DataEvent:
			if r := e.Open(); r != nil {
				b, _ := io.ReadAll(io.LimitReader(r, 4<<20))
				r.Close()
				if text := string(b); text != "" {
					s.term.Paste(strings.ReplaceAll(text, "\r\n", "\n"))
				} else {
					// Nothing as text: the clipboard may hold a picture, which
					// the core knows how to take when asked with its own key.
					s.term.Key(uv.KeyPressEvent{Code: 'v', Mod: uv.ModCtrl})
				}
			}
		}
	}
}

func (s *shell) exited() bool {
	if s.err != nil {
		return true
	}
	if s.core == nil {
		return false
	}
	done, _ := s.core.Exited()
	return done
}

func (s *shell) onKey(gtx layout.Context, e key.Event) {
	ctrl := e.Modifiers.Contain(key.ModCtrl)

	// The window's own keys, which the core never sees.
	switch {
	case e.Name == key.NameF11:
		if s.mode == app.Fullscreen {
			s.w.Option(app.Windowed.Option())
		} else {
			s.w.Option(app.Fullscreen.Option())
		}
		return
	case ctrl && (e.Name == "=" || e.Name == "+"):
		s.zoom(1)
		return
	case ctrl && e.Name == "-":
		s.zoom(-1)
		return
	case ctrl && e.Name == "0":
		s.state.FontSize = defaultFontSize
		s.w.Invalidate()
		return
	case ctrl && e.Name == "V":
		// Paste: text if there is text, otherwise the core's own ctrl+v,
		// which attaches the picture on the clipboard.
		gtx.Execute(clipboard.ReadCmd{Tag: s})
		return
	}

	if s.exited() {
		switch e.Name {
		case key.NameReturn, key.NameEnter:
			s.restart()
		case key.NameEscape:
			s.w.Perform(system.ActionClose)
		}
		return
	}

	k, ok := keyEvent(e)
	if !ok {
		return
	}
	s.ime.reset()
	s.syncIME(gtx)
	s.term.Key(k)
}

func (s *shell) onEdit(gtx layout.Context, e key.EditEvent) {
	if s.exited() {
		return
	}
	back, insert := s.ime.apply(e)
	if back > 0 {
		s.term.Text(strings.Repeat("\x7f", back))
	}
	if insert != "" {
		s.term.Text(insert)
	}
	s.syncIME(gtx)
}

// syncIME tells the input method what the buffer holds now and that the
// caret is at its end, which is what every later edit is measured against.
func (s *shell) syncIME(gtx layout.Context) {
	n := s.ime.len()
	gtx.Execute(key.SnippetCmd{Tag: s, Snippet: key.Snippet{
		Range: key.Range{Start: 0, End: n}, Text: string(s.ime.text),
	}})
	gtx.Execute(key.SelectionCmd{Tag: s, Range: key.Range{Start: n, End: n}})
}

// placeIME keeps the input method's window beside the caret the core drew.
func (s *shell) placeIME(gtx layout.Context, p int) {
	s.term.mu.Lock()
	cur := s.term.emu.CursorPosition()
	s.term.mu.Unlock()
	cw, ch := s.grid.CellSize()
	n := s.ime.len()
	pos := f32.Pt(float32(p)+float32(cur.X)*cw, float32(p+cur.Y*ch+ch))
	gtx.Execute(key.SelectionCmd{
		Tag: s, Range: key.Range{Start: n, End: n},
		Caret: key.Caret{Pos: pos, Ascent: float32(ch), Descent: 0},
	})
}

func (s *shell) onPointer(gtx layout.Context, e pointer.Event, p int) {
	if s.exited() {
		return
	}
	cw, ch := s.grid.CellSize()
	s.term.mu.Lock()
	cols, rows := s.term.cols, s.term.rows
	s.term.mu.Unlock()
	cell := image.Pt(
		clampInt(int((e.Position.X-float32(p))/cw), 0, cols-1),
		clampInt(int((e.Position.Y-float32(p))/float32(ch)), 0, rows-1),
	)
	m := uvMods(e.Modifiers)

	switch e.Kind {
	case pointer.Press:
		if !s.focused {
			gtx.Execute(key.FocusCmd{Tag: s})
		}
		if e.Buttons.Contain(pointer.ButtonSecondary) && e.Modifiers == 0 {
			// Right click pastes, the way Windows Terminal does.
			gtx.Execute(clipboard.ReadCmd{Tag: s})
			return
		}
		s.pressed = mouseButton(e.Buttons)
		s.term.Mouse(vt.MouseClick{X: cell.X, Y: cell.Y, Button: s.pressed, Mod: m})
	case pointer.Release:
		s.term.Mouse(vt.MouseRelease{X: cell.X, Y: cell.Y, Button: s.pressed, Mod: m})
		s.pressed = vt.MouseNone
	case pointer.Drag, pointer.Move:
		if cell == s.lastCell {
			return // motion within a cell says nothing new
		}
		s.term.Mouse(vt.MouseMotion{X: cell.X, Y: cell.Y, Button: s.pressed, Mod: m})
	case pointer.Scroll:
		// Scrolling moves in whole notches, at once, the way the console —
		// and so PowerShell — does: one notch, the lines Windows' mouse
		// settings say, in a single jump. The travel arrives in wheel units,
		// 120 a notch, not pixels; it used to be divided by a line's height
		// in pixels, which made one notch six steps, and turned the small
		// deltas of a high-resolution wheel or a touchpad into a drift. What
		// is less than a notch waits for the rest of it.
		s.wheel(cell, m, s.notchEvents*wheelSteps(&s.scrollAcc, e.Scroll.Y, wheelNotch))
		// Sideways. Gio turns shift+wheel into horizontal travel and drops the
		// shift, and a trackpad or a tilting wheel sends it directly; either
		// way it reaches the core as shift+wheel, which is what Windows
		// Terminal sends and what the core already scrolls sideways on. One
		// notch is one step.
		s.wheel(cell, m|uv.ModShift, wheelSteps(&s.scrollX, e.Scroll.X, wheelNotch))
	}
	s.lastCell = cell
}

func clampInt(v, lo, hi int) int { return max(lo, min(v, hi)) }

// wheelSteps adds travel to what has built up in acc and returns the whole
// steps it now makes: positive is down (or right), negative up (or left). What
// is less than a step stays in acc for the next event, so a trackpad's stream
// of fractions scrolls as far as a wheel's notches do.
func wheelSteps(acc *float32, travel, step float32) int {
	if step <= 0 {
		return 0
	}
	*acc += travel
	n := int(*acc / step)
	*acc -= float32(n) * step
	return n
}

// wheel sends n wheel steps at a cell: down for positive, up for negative.
func (s *shell) wheel(cell image.Point, mod uv.KeyMod, n int) {
	b := vt.MouseWheelDown
	if n < 0 {
		b, n = vt.MouseWheelUp, -n
	}
	for ; n > 0; n-- {
		s.term.Mouse(vt.MouseWheel{X: cell.X, Y: cell.Y, Button: b, Mod: mod})
	}
}

func (s *shell) zoom(by float32) {
	s.state.FontSize = max(8, min(36, s.state.FontSize+by))
	s.w.Invalidate()
}

func (s *shell) restart() {
	s.term.Close()
	s.term.mu.Lock()
	cols, rows := s.term.cols, s.term.rows
	s.term.mu.Unlock()
	s.term = NewTerm(cols, rows, oneDark.Fg, oneDark.Bg)
	s.grid.lastGen = ^uint64(0)
	s.ime.reset()
	s.start()
	s.w.Invalidate()
}

// banner is the card shown when the core is not running.
func (s *shell) banner(gtx layout.Context, title, detail, keys string) {
	max := gtx.Constraints.Max
	paint.FillShape(gtx.Ops, color.NRGBA{A: 0x99}, clip.Rect{Max: max}.Op())

	w, h := gtx.Dp(460), gtx.Dp(132)
	x, y := (max.X-w)/2, (max.Y-h)/2
	card := clip.UniformRRect(image.Rect(x, y, x+w, y+h), gtx.Dp(10))
	paint.FillShape(gtx.Ops, rgb(0x21252b), card.Op(gtx.Ops))
	paint.FillShape(gtx.Ops, oneDark.Cursor, clip.Rect{Min: image.Pt(x, y), Max: image.Pt(x+gtx.Dp(4), y+h)}.Op())

	lines := []struct {
		text string
		c    color.NRGBA
		dy   int
	}{
		{title, rgb(0xe4e8ef), 22},
		{truncateRunes(detail, 56), rgb(0xb9c0cc), 58},
		{keys, rgb(0x8f96a3), 98},
	}
	for _, l := range lines {
		s.label(gtx, l.text, l.c, x+gtx.Dp(24), y+gtx.Dp(unit.Dp(l.dy)))
	}
}

func (s *shell) label(gtx layout.Context, txt string, c color.NRGBA, x, y int) {
	off := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
	s.grid.text(gtx.Ops, txt, 0, cellStyle{fg: c})
	off.Pop()
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
