package ui

import (
	"encoding/json"
	"os"
	"path/filepath"

	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/config"
)

// The panes are a layout you own rather than one you are given.
//
// Their widths used to be two formulas — a sixth of the terminal for the
// sidebar, forty-five percent of the rest for the preview — which is a
// reasonable guess and wrong for anything you are actually doing: reading a
// long answer wants the transcript wide, comparing two files wants the preview
// wide, and neither is a sixth of anything. So a width can be dragged, nudged
// from the keyboard, and is remembered.
//
// It is stored in columns rather than as a fraction. A fraction rescales
// tidily when the terminal does, and rescaling is exactly what you do not want:
// you set the sidebar wide enough for the longest path in the project, and that
// length has nothing to do with how wide the window is.

// Pane widths, in columns. The minimums are what the pane still says something
// at; below them it is a border with a hint of text inside.
const (
	sideMin = 16
	prevMin = 24
	// paneRoom is what the transcript keeps for itself whatever else happens.
	// It is the pane the conversation is in; the others are beside it.
	paneRoom = 32
)

// layout is what the user has set. A zero width means the pane has not been
// touched and keeps the default it is given.
type layout struct {
	Side    int  `json:"side,omitempty"`
	Preview int  `json:"preview,omitempty"`
	Hide    bool `json:"hide_sessions,omitempty"`
	HidePrv bool `json:"hide_preview,omitempty"`
}

func layoutPath() string { return filepath.Join(config.DataDir(), "layout.json") }

// loadLayout reads the saved widths. An unreadable file means the defaults,
// which is the same thing a first run means.
func loadLayout() layout {
	var l layout
	b, err := os.ReadFile(layoutPath())
	if err != nil {
		return layout{}
	}
	if json.Unmarshal(b, &l) != nil {
		return layout{}
	}
	return l
}

// saveLayout writes the widths atomically, so an interrupted write cannot
// leave a file that parses to something other than what was there.
func (m *Model) saveLayout() {
	l := layout{
		Side: m.sideSet, Preview: m.prevSet,
		Hide: !m.showSessions, HidePrv: !m.showPreview,
	}
	b, err := json.Marshal(l)
	if err != nil {
		return
	}
	path := layoutPath()
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

// sideWidth and prevWidth resolve a pane's width: what was set if anything was,
// and the default otherwise, both clamped to what the terminal can give.
func (m *Model) sideWidth(w int) int {
	if !m.showSessions || w < 90 {
		return 0
	}
	want := clamp(w/6, 22, 34)
	if m.sideSet > 0 {
		want = m.sideSet
	}
	return clamp(want, sideMin, max(sideMin, w/2))
}

// prevWidth sizes the preview out of what is left once the sidebar and the
// side chat have taken theirs.
//
// It is the pane that gives way, and it gives way by shrinking before it gives
// way by leaving. The order is not arbitrary: the sidebar is a fixed strip, the
// side chat was opened a moment ago and on purpose, the transcript is what the
// window is for — the preview is the one showing a file you can reopen with a
// keystroke, and the one whose content does not change while you are not
// looking at it.
// The column to the right of the transcript holds one pane, not two. The
// preview is in it, and the side chat takes its place while one is open.
//
// They were made to sit side by side first, and four panes on a terminal is
// three columns of forty and nothing readable in any of them. The two are also
// the same kind of thing — something you consult beside the conversation — and
// you are never consulting both at once. So the side chat borrows the column
// and gives it back when it closes, which is why nothing has to remember
// whether the preview was open: it was never closed.
func (m *Model) prevWidth(w, side int) int {
	if !m.showPreview || m.showBtw {
		return 0
	}
	return m.sideColumn(w, side)
}

// sideColumn is how wide that column is, whichever pane is standing in it.
// One width, because there is one divider on the screen and it is the same
// line either way.
func (m *Model) sideColumn(w, side int) int {
	room := w - side - paneRoom
	if room < prevMin {
		return 0
	}
	want := clamp((w-side)*45/100, 38, 90)
	if m.prevSet > 0 {
		want = m.prevSet
	}
	return clamp(want, prevMin, room)
}

// btwAsk is what the side chat asks for. Wide enough to read an answer in,
// narrow enough that it is plainly an aside rather than a second transcript.
const btwAsk = 36

// btwWidth is the side chat's column. It is not resizable and not remembered:
// it is a place you open for one question and close again, and a pane like
// that wants one fewer decision attached to it.
//
// It is measured before the preview rather than out of what the preview left,
// which is how it used to vanish without a word on any terminal that was not
// wide: the preview had already taken forty-five percent, and what remained
// was under this pane's minimum.
func (m *Model) btwWidth(w, side int) int {
	if !m.showBtw {
		return 0
	}
	return m.sideColumn(w, side)
}

// setSideWidth and setPrevWidth record a width the user chose and re-lay the
// screen around it.
func (m *Model) setSideWidth(cols int) {
	m.sideSet = clamp(cols, sideMin, max(sideMin, m.w/2))
	m.resize(m.w, m.h)
	m.saveLayout()
}

func (m *Model) setPrevWidth(cols int) {
	m.prevSet = clamp(cols, prevMin, max(prevMin, m.w-m.sideW-paneRoom))
	m.resize(m.w, m.h)
	m.saveLayout()
}

// nudgeWidth is the keyboard's half of resizing.
//
// The arrow pushes a divider in the direction of the arrow, which is the rule
// that holds wherever you are standing: alt+→ always moves a line right. Which
// line depends on the focus — the sidebar's edge when the caret is in it, the
// preview's otherwise, including from the transcript, which has no width of
// its own and grows by the preview giving way.
func (m *Model) nudgeWidth(by int) {
	switch m.focus {
	case focusSessions, focusExplorer:
		if m.sideW == 0 {
			m.notice = "the sessions pane is closed — ctrl+b opens it"
			return
		}
		m.setSideWidth(m.sideW + by)
	default:
		col := m.prevW + m.btwW
		if col == 0 {
			m.notice = "the preview is closed — ctrl+e opens it"
			return
		}
		// The column's edge is its left one, so pushing it right takes width
		// from it and gives it to whatever is on the other side.
		m.setPrevWidth(col - by)
	}
}

// ---- dragging the dividers -------------------------------------------------

type dragging int

const (
	dragNone dragging = iota
	dragSide
	dragPreview
)

// dividerAt reports which divider a column is on, allowing a cell either side:
// a one-column target is a target you miss.
func (m *Model) dividerAt(x, y int) dragging {
	if y < headerRows || y >= headerRows+m.bodyH {
		return dragNone
	}
	if m.sideW > 0 && x == m.sideW-1 {
		return dragSide
	}
	if col := m.prevW + m.btwW; col > 0 && x == m.w-col-1 {
		return dragPreview
	}
	return dragNone
}

// A divider is one column now, and it is the rule you can see.
//
// It was two: the right border of the pane before it and the left border of
// the pane after. Panes share a rule since — one line between them rather than
// two — so there is only one column to be on, and the column after it is the
// first column of text. Claiming that one took hold of the divider instead of
// the word under the pointer, which is how this was wrong the last two times.

// dragTo moves the divider being held to the column the pointer is in.
// dragTo moves the divider the mouse has hold of to a column.
//
// The column is where the rule should end up, and a pane's rule is its own
// last column — panes share one line now, so the rule between the sidebar and
// the transcript is the sidebar's right border at sideW-1. Dropping it on
// column x therefore makes the pane x+1 wide.
func (m *Model) dragTo(x int) {
	switch m.drag {
	case dragSide:
		m.setSideWidth(x + 1)
	case dragPreview:
		m.setPrevWidth(m.w - x - 1)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// toggleSessions and togglePreview open and close a pane, from the key or from
// the header switch. Both remember the choice: a pane you closed should still
// be closed tomorrow.
func (m *Model) toggleSessions() {
	m.showSessions = !m.showSessions
	m.resize(m.w, m.h)
	m.saveLayout()
}

func (m *Model) togglePreview() {
	m.showPreview = !m.showPreview
	// They share the column, so asking for one is asking the other to step
	// out. The side chat is kept, as closing it any other way keeps it.
	if m.showPreview && m.showBtw {
		m.closeBtw()
	}
	m.resize(m.w, m.h)
	m.saveLayout()
}

// ---- the header's pane switches --------------------------------------------

// toggleHit is where a switch was drawn, so a click lands on the one that was
// drawn rather than on where it would be if nothing had wrapped.
type toggleHit struct {
	x0, x1 int
	pane   focus
}

// paneSwitches draws the switches and records where they went.
//
// They live in the header rather than on the panes themselves because a closed
// pane has no title bar to click: an × that can only close is half a switch,
// and the other half would be a key you have to remember.
func (m *Model) paneSwitches(at int) string {
	m.toggles = m.toggles[:0]
	out := ""
	for _, sw := range []struct {
		pane focus
		name string
		on   bool
	}{
		{focusSessions, "sessions", m.showSessions},
		{focusPreview, "preview", m.showPreview},
	} {
		mark, style := "▫", m.st.Faint
		if sw.on {
			mark, style = "▪", m.st.Accent
		}
		label := " " + mark + " " + sw.name + " "
		m.toggles = append(m.toggles, toggleHit{
			x0: at, x1: at + lipgloss.Width(label), pane: sw.pane,
		})
		at += lipgloss.Width(label)
		out += style.Render(label)
	}
	return out
}

// switchAt reports which switch a click landed on.
func (m *Model) switchAt(x, y int) (focus, bool) {
	if y >= headerRows {
		return 0, false
	}
	for _, t := range m.toggles {
		if x >= t.x0 && x < t.x1 {
			return t.pane, true
		}
	}
	return 0, false
}

// switchesWidth is how much room the switches need, which the header has to
// know before it draws them in order to put them at its right edge.
func (m *Model) switchesWidth() int {
	// Measured, not counted: the marks are multi-byte and a byte count would
	// put the switches two columns off the edge they are meant to sit on.
	return lipgloss.Width(" ▪ sessions ") + lipgloss.Width(" ▪ preview ")
}

// switchFor finds a drawn switch, for the tests and for anything that needs to
// point at one.
func (m *Model) switchFor(pane focus) (toggleHit, bool) {
	for _, t := range m.toggles {
		if t.pane == pane {
			return t, true
		}
	}
	return toggleHit{}, false
}
