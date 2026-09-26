package ui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/session"
)

// conversations opens n of them and leaves the first one active.
func conversations(m *Model, n int) []*session.Session {
	out := []*session.Session{m.mgr.Active()}
	out[0].Title = "cuộc đầu tiên"
	out[0].Append(session.Message{Role: session.RoleUser, Text: "hỏi"})
	for i := 1; i < n; i++ {
		out = append(out, talking(m, "cuộc số "+strconv.Itoa(i)))
	}
	m.mgr.Select(m.indexOf(out[0]))
	m.invalidateChat()
	return out
}

// The cells add up to the column. A split that is a column short leaves a
// stripe of whatever was behind it; a split that is a column over pushes the
// pane beside it off the screen — and both look like a rendering bug rather
// than an arithmetic one.
func TestTheCellsAddUpToTheColumn(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {150, 34}, {180, 44}, {200, 50}} {
		m := newTestModel(t)
		conversations(m, 4)
		m.resize(size[0], size[1])

		for _, n := range []int{1, 2, 4} {
			m.setSplit(n)
			boxes := m.splitBoxes()
			cols, rows := splitGrid(len(boxes))

			// Every row of cells spans the column, and every column of them
			// spans the body.
			for r := 0; r < rows && r*cols < len(boxes); r++ {
				wide := 0
				for c := 0; c < cols && r*cols+c < len(boxes); c++ {
					wide += boxes[r*cols+c].w
				}
				if wide != m.chatW {
					t.Errorf("%dx%d split %d: row %d is %d columns, want %d",
						size[0], size[1], n, r, wide, m.chatW)
				}
			}
			tall := 0
			for r := 0; r < rows && r*cols < len(boxes); r++ {
				tall += boxes[r*cols].h
			}
			if tall != m.bodyH {
				t.Errorf("%dx%d split %d: the cells are %d rows, want %d",
					size[0], size[1], n, tall, m.bodyH)
			}
			// And what it draws is that size, not merely that arithmetic.
			drawn := strings.Split(m.splitColumn("transcript", seams{}), "\n")
			if len(drawn) != m.bodyH {
				t.Errorf("%dx%d split %d: drew %d rows, want %d",
					size[0], size[1], n, len(drawn), m.bodyH)
			}
		}
	}
}

// Asking for more than fits folds back rather than drawing a wall of borders
// with two words inside — and says which it did.
func TestASplitTooBigForTheTerminalFoldsBack(t *testing.T) {
	m := newTestModel(t)
	conversations(m, 4)
	m.resize(90, 20) // not room for four

	m.setSplit(4)
	if got := m.splitCells(); got >= 4 {
		t.Errorf("a 90x20 terminal drew %d cells", got)
	}
	if !strings.Contains(m.notice, "not enough room") {
		t.Errorf("it folded back without saying so: %q", m.notice)
	}
}

// And asking for more cells than there are conversations folds back too. It
// used to halve the height anyway and leave the bottom row empty — a grid
// drawn for conversations that do not exist.
func TestASplitWiderThanTheListFoldsBack(t *testing.T) {
	m := newTestModel(t)
	conversations(m, 2)
	m.resize(180, 44)

	m.setSplit(4)
	if got := m.splitCells(); got != 2 {
		t.Errorf("two conversations were drawn in %d cells", got)
	}
	for _, b := range m.splitBoxes() {
		if b.h != m.bodyH {
			t.Errorf("a cell is %d rows of %d — the grid kept a row for nobody",
				b.h, m.bodyH)
		}
	}
}

// Clicking a cell says which conversation you are talking to. It is the only
// gesture the arrangement needs, and without it a split is a wall you cannot
// reach into.
func TestClickingACellTalksToThatConversation(t *testing.T) {
	m := newTestModel(t)
	all := conversations(m, 4)
	m.resize(180, 44)
	m.setSplit(2)
	m.View()

	var other splitBox
	for _, b := range m.splitBoxes() {
		if !b.focused {
			other = b
			break
		}
	}
	if other.sess == nil {
		t.Fatal("the split has only one cell")
	}
	want := other.sess

	clickAt(m, other.x+other.w/2, other.y+other.h/2)

	if m.mgr.Active() != want {
		t.Errorf("the click is talking to %q, want %q", m.mgr.Active().Label(), want.Label())
	}
	if m.focus != focusChat {
		t.Errorf("the click left the caret in %v", m.focus)
	}
	// And the cell it moved to is now the focused one.
	if m.chatCell().sess != want {
		t.Error("the transcript did not move into the cell that was clicked")
	}
	_ = all
}

// The viewport is sized to the cell it is drawn in. Sized to the column it
// would wrap to a width it is not drawn at, which is the same mistake as
// measuring a line one way and cutting it another.
func TestTheTranscriptIsSizedToItsCell(t *testing.T) {
	m := newTestModel(t)
	conversations(m, 4)
	m.resize(180, 44)

	m.setSplit(1)
	whole := m.chat.Width()
	m.setSplit(2)
	half := m.chat.Width()

	if half >= whole {
		t.Errorf("split in two, the transcript is still %d columns of %d", half, whole)
	}
	// Its cell's writable width, which is not always the cell minus two: a
	// pane that leans on its neighbour's rule has a column more of it.
	want, _ := m.chatInner()
	if half != want {
		t.Errorf("the transcript is %d columns, want its cell's %d", half, want)
	}
	for i, l := range strings.Split(m.chat.View(), "\n") {
		if got := len([]rune(stripANSI(l))); got > want {
			t.Errorf("line %d is %d columns in a cell of %d", i, got, want)
		}
	}
}

// Selecting happens in the cell that answers the prompt. The others are a
// rendering, and dragging across one would be selecting from a picture of a
// conversation rather than from it.
func TestSelectionFollowsTheFocusedCell(t *testing.T) {
	m := newTestModel(t)
	conversations(m, 4)
	m.resize(180, 44)
	m.setSplit(2)
	m.View()

	left, top, w, h, ok := m.paneBox(focusChat)
	if !ok {
		t.Fatal("the transcript has no box")
	}
	// The focused cell's writable area. Not the cell minus a border on each
	// side: a pane that leans on its neighbour's rule has no left border of
	// its own, so its text begins a column earlier and runs a column further.
	cell := m.chatCell()
	wantW, wantH := m.chatInner()
	wantX := cell.x
	if m.paneHasLeftRule(focusChat) {
		wantX++
	}
	if left != wantX || top != cell.y+1 || w != wantW || h != wantH {
		t.Errorf("selection is aimed at %d,%d %dx%d; the focused cell's text is %d,%d %dx%d",
			left, top, w, h, wantX, cell.y+1, wantW, wantH)
	}
	// A press inside it starts a selection; one in the cell beside it does not.
	m.onMouse(tea.MouseClickMsg{X: left + 1, Y: top + 1, Button: tea.MouseLeft})
	if !m.sel.on {
		t.Error("a press in the focused cell started nothing")
	}
}

// /split with no number is a toggle, so the thing that opened it closes it.
func TestSplitWithNoNumberPutsTheColumnBack(t *testing.T) {
	m := newTestModel(t)
	conversations(m, 4)
	m.resize(180, 44)

	m.runSlash("split", "2")
	if m.splitCells() != 2 {
		t.Fatalf("/split 2 drew %d cells", m.splitCells())
	}
	m.runSlash("split", "")
	if got := m.splitCells(); got != 1 {
		t.Errorf("/split again left %d cells", got)
	}
}

// Where a pane's content starts has to be where the renderer put it. A pane
// that leans on its neighbour's rule has no left border of its own, so the
// column the text begins on moves — and every click and every drag is aimed
// through the same arithmetic.
func TestPaneBoxFindsTheTextTheRendererDrew(t *testing.T) {
	m := newTestModel(t)
	conversations(m, 2)
	m.resize(120, 30)
	m.View()

	rows := strings.Split(stripANSI(m.View().Content), "\n")
	for _, f := range []focus{focusChat, focusPreview} {
		left, top, w, _, ok := m.paneBox(f)
		if !ok {
			continue
		}
		if top >= len(rows) {
			t.Fatalf("pane %v claims row %d of %d", f, top, len(rows))
		}
		row := []rune(rows[top])
		if left >= len(row) {
			t.Fatalf("pane %v claims column %d of %d", f, left, len(row))
		}
		// The cell just before the content is the rule; the content itself is
		// not one. If the box is a column out, one of these is wrong.
		if got := row[left]; got == '│' {
			t.Errorf("pane %v: column %d is still the rule, so the box is a column short",
				f, left)
		}
		if left > 0 && row[left-1] != '│' {
			t.Errorf("pane %v: column %d is %q, not the rule that should precede the text",
				f, left-1, string(row[left-1]))
		}
		if left+w > len(row) {
			t.Errorf("pane %v: %d columns from %d runs past the %d drawn", f, w, left, len(row))
		}
	}
}
