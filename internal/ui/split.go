package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/session"
)

// Watching more than one conversation at a time.
//
// Sessions here have always run at the same time — that is what made the list
// of them worth having — but only one was ever on screen, so watching two
// meant switching between them and trusting your memory for whichever was not
// in front of you. Which is the thing a second pane is for.
//
// The mechanism is one the side chat already proved: renderHeadOf draws any
// conversation into any box, without a viewport and without the cache. So a
// split is not a second transcript engine. It is the one transcript, in the
// cell holding the conversation you are talking to, and a rendering of each of
// the others beside it.
//
// That asymmetry is the design rather than a shortcut. One prompt, one caret,
// one conversation being addressed: the focused cell scrolls, selects, and
// takes what you type, exactly as the single pane always did, and the others
// are something you are keeping an eye on. Clicking one makes it the one you
// are talking to, which is the only gesture the arrangement needs.

// splitMax is how many conversations may share the column. Two is reading one
// while another works; four is a wall. More than four on a terminal is four
// columns of thirty and nothing legible in any of them.
const splitMax = 4

// splitMinW and splitMinH are what a cell needs to be worth drawing. Below
// them the split silently folds back to fewer cells rather than showing a
// column of borders with two words inside.
const (
	splitMinW = 34
	splitMinH = 8
)

// splitCells is how many conversations the column is actually showing, which
// is what was asked for and what there is room for, whichever is smaller.
func (m *Model) splitCells() int {
	want := clamp(m.split, 1, splitMax)
	// And by how many conversations there are to put in them. Asking for four
	// with two open used to halve the height anyway and leave the bottom row
	// empty — a grid drawn for conversations that do not exist.
	if n := m.listedCount(); want > n {
		want = max(1, n)
	}
	for want > 1 {
		cols, rows := splitGrid(want)
		if (m.chatW/cols) >= splitMinW && (m.bodyH/rows) >= splitMinH {
			break
		}
		want--
	}
	return want
}

// splitGrid lays n cells out. Two side by side, because a transcript is read
// down and cutting its height in half costs more than cutting its width; four
// as a square, because by then there is no arrangement that is not a
// compromise.
func splitGrid(n int) (cols, rows int) {
	switch {
	case n >= 4:
		return 2, 2
	case n >= 2:
		return 2, 1
	}
	return 1, 1
}

// splitSessions is the conversations the cells hold, the focused one first.
//
// It is derived rather than stored. A remembered assignment would have to be
// kept true through every close, fork and side chat, and the cost of getting
// that wrong is a pane showing a conversation that no longer exists; the cost
// of deriving it is that a cell's contents can move when the list does.
func (m *Model) splitSessions(n int) []*session.Session {
	active := m.mgr.Active()
	out := []*session.Session{active}
	if n <= 1 {
		return out
	}
	// The rest in the list's own order, so the second cell is the second
	// conversation and stays the second conversation.
	for _, s := range m.mgr.All() {
		if len(out) == n {
			break
		}
		if s == active || s.SideOf != "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// listedCount is how many conversations could go in a cell: the ones the
// session list shows, which is every one that is not an aside.
func (m *Model) listedCount() int {
	n := 0
	for _, s := range m.mgr.All() {
		if s.SideOf == "" {
			n++
		}
	}
	return n
}

// splitBox is where one cell was drawn, in screen cells.
type splitBox struct {
	sess           *session.Session
	x, y, w, h     int
	focused, first bool
}

// splitBoxes lays the column out. The renderer and the mouse both call it, so
// a click lands in the cell that was drawn rather than near it.
func (m *Model) splitBoxes() []splitBox {
	n := m.splitCells()
	cols, rows := splitGrid(n)
	sessions := m.splitSessions(n)

	left, top := m.sideW, headerRows
	out := make([]splitBox, 0, n)
	for i := 0; i < n && i < len(sessions); i++ {
		cx, cy := i%cols, i/cols
		// The last column and the last row take the remainder, so the cells
		// add up to the pane exactly rather than to within a rounding error.
		w := m.chatW / cols
		if cx == cols-1 {
			w = m.chatW - w*cx
		}
		h := m.bodyH / rows
		if cy == rows-1 {
			h = m.bodyH - h*cy
		}
		out = append(out, splitBox{
			sess:    sessions[i],
			x:       left + (m.chatW/cols)*cx,
			y:       top + (m.bodyH/rows)*cy,
			w:       w,
			h:       h,
			focused: sessions[i] == m.mgr.Active(),
			first:   i == 0,
		})
	}
	return out
}

// splitColumn draws the cells and stacks them into the column the transcript
// used to fill on its own.
func (m *Model) splitColumn(chatTitle string) string {
	boxes := m.splitBoxes()
	if len(boxes) <= 1 {
		body := m.paintSelection(m.chat.View(), focusChat, m.chatW-2)
		return m.pane(body, chatTitle, m.chatW, m.bodyH, m.focus == focusChat)
	}

	cells := make([]string, len(boxes))
	for i, b := range boxes {
		title, body := m.splitCell(b, chatTitle)
		cells[i] = m.pane(body, title, b.w, b.h, b.focused && m.focus == focusChat)
	}

	cols, _ := splitGrid(len(boxes))
	var rows []string
	for i := 0; i < len(cells); i += cols {
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, cells[i:min(i+cols, len(cells))]...))
	}
	return strings.Join(rows, "\n")
}

// splitCell renders one cell: the live transcript for the conversation being
// talked to, a drawn one for the rest.
func (m *Model) splitCell(b splitBox, chatTitle string) (title, body string) {
	if b.focused {
		return chatTitle, m.paintSelection(m.chat.View(), focusChat, b.w-2)
	}
	title = b.sess.Label()
	if st := m.sessionState(b.sess); st != stateIdle && st != stateEmpty {
		word := st.word()
		if st == stateWorking && b.sess.Status != "" {
			word = b.sess.Status
		}
		title = word + "  " + title
	}
	// The tail of it, because an unfocused cell is one you glance at and what
	// you glance for is the newest thing in it.
	h := m.renderHeadOf(b.sess, max(10, b.w-2))
	lines := strings.Split(h.text, "\n")
	if room := b.h - 2; len(lines) > room {
		lines = lines[len(lines)-room:]
	}
	return title, strings.Join(lines, "\n")
}

// chatCell is the box the conversation being talked to is drawn in — the
// whole column until the column is split, and one cell of it after.
//
// The viewport is sized to this rather than to the column, because it is the
// thing being scrolled and wrapped: sized to the column it would wrap to a
// width it is not drawn at, which is the same class of mistake as measuring a
// line one way and cutting it another.
func (m *Model) chatCell() splitBox {
	boxes := m.splitBoxes()
	for _, b := range boxes {
		if b.focused {
			return b
		}
	}
	if len(boxes) > 0 {
		return boxes[0]
	}
	return splitBox{x: m.sideW, y: headerRows, w: m.chatW, h: m.bodyH, focused: true}
}

func (m *Model) chatWidth() int  { return m.chatCell().w }
func (m *Model) chatHeight() int { return m.chatCell().h }

// splitAt is the conversation whose cell covers a screen cell, or nil.
func (m *Model) splitAt(x, y int) *session.Session {
	for _, b := range m.splitBoxes() {
		if x >= b.x && x < b.x+b.w && y >= b.y && y < b.y+b.h {
			return b.sess
		}
	}
	return nil
}

// setSplit changes how many conversations share the column.
func (m *Model) setSplit(n int) {
	m.split = clamp(n, 1, splitMax)
	got := m.splitCells()
	m.resize(m.w, m.h)

	switch {
	case got < m.split:
		m.notice = "not enough room for " + plural(m.split, "pane") +
			" — showing " + plural(got, "pane")
	case got == 1:
		m.notice = "one conversation"
	default:
		m.notice = plural(got, "conversation") + " side by side · click one to talk to it"
	}
}
