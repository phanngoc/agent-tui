package ui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/phanngoc/agent-tui/internal/clipboard"
)

// Selecting text, because asking for the mouse took it away.
//
// A terminal selects text by dragging, and stops the moment a program turns on
// mouse reporting: the drag arrives here instead, and the terminal has nothing
// left to select with. Shift is the usual way back — most terminals hand the
// drag to themselves while it is held — but it is a convention, not a
// guarantee, and telling someone to hold a modifier to reach what their mouse
// could already do is not an answer. A program that takes the mouse owes the
// user a selection of its own.
//
// So a drag across a pane selects what it crosses, releasing puts it on the
// clipboard, and a double-click takes the word under the pointer — which is
// what most copying out of a transcript actually is: a path, an identifier, a
// commit hash.
//
// The coordinates are rows the pane drew, not lines of its content. The
// transcript soft-wraps, so one message is many rows and no content line
// number says where the pointer is; the row a pane drew is the only thing the
// renderer and the mouse both know. Rows are kept as they are drawn, so a
// selection that scrolled past still knows what it covered.

// selPoint is a cell in a pane: a row counted from the top of everything the
// pane could draw, and a column counted from the left of its content.
type selPoint struct{ row, col int }

func (p selPoint) before(q selPoint) bool {
	return p.row < q.row || (p.row == q.row && p.col < q.col)
}

// selection is a range being dragged, or one already made.
type selection struct {
	pane   focus
	anchor selPoint
	head   selPoint
	// dragging is true between the press and the release.
	dragging bool
	on       bool
	// rows is what the pane drew while the selection existed, by row. A drag
	// that scrolls covers rows that are no longer on screen, and this is where
	// they were kept as they went past.
	rows map[int]string
	// clickAt and clickWhen are the last press, so a second one in the same
	// place can be read as a double-click.
	clickAt   selPoint
	clickWhen time.Time
}

// dblClick is how close together two presses must be to be one gesture. Long
// enough for a deliberate double-click, short enough that two separate clicks
// on the same word stay two clicks.
const dblClick = 450 * time.Millisecond

// span returns the selection in reading order, and whether it covers anything.
func (s *selection) span() (a, b selPoint, ok bool) {
	if !s.on {
		return a, b, false
	}
	a, b = s.anchor, s.head
	if b.before(a) {
		a, b = b, a
	}
	return a, b, a != b
}

// ---- geometry ---------------------------------------------------------------

// paneBox is where a pane's content was drawn: the top-left cell inside its
// border, and the size of the area inside it. It is the one place that knows,
// so the renderer and the mouse cannot come to disagree about it.
func (m *Model) paneBox(f focus) (left, top, w, h int, ok bool) {
	switch f {
	case focusChat:
		// The cell the conversation is drawn in, which is the column until
		// the column is split. Selecting in an unfocused cell would be
		// selecting from a rendering rather than from the transcript.
		c := m.chatCell()
		left, w = c.x, c.w
		if c.h < 3 {
			return 0, 0, 0, 0, false
		}
		return left + 1, c.y + 1, w - 2, c.h - 2, true
	case focusBtw:
		if m.btwW == 0 {
			return 0, 0, 0, 0, false
		}
		left, w = m.sideW+m.chatW, m.btwW
	case focusPreview:
		if m.prevW == 0 {
			return 0, 0, 0, 0, false
		}
		left, w = m.sideW+m.chatW+m.btwW, m.prevW
	default:
		return 0, 0, 0, 0, false
	}
	if w < 3 || m.bodyH < 3 {
		return 0, 0, 0, 0, false
	}
	return left + 1, headerRows + 1, w - 2, m.bodyH - 2, true
}

// paneScroll is how far a pane has been scrolled, so a row can keep its name
// after the pane moves under it.
func (m *Model) paneScroll(f focus) (yoff, xoff int) {
	switch f {
	case focusChat:
		return m.chat.YOffset(), 0
	case focusPreview:
		return m.prev.YOffset(), m.prev.XOffset()
	}
	// The side chat is short by construction and does not scroll.
	return 0, 0
}

// selPointAt maps a screen cell onto a pane and a cell inside it.
func (m *Model) selPointAt(x, y int) (focus, selPoint, bool) {
	for _, f := range []focus{focusChat, focusBtw, focusPreview} {
		left, top, w, h, ok := m.paneBox(f)
		if !ok || x < left || x >= left+w || y < top || y >= top+h {
			continue
		}
		yoff, xoff := m.paneScroll(f)
		return f, selPoint{row: yoff + y - top, col: xoff + x - left}, true
	}
	return 0, selPoint{}, false
}

// ---- the gesture ------------------------------------------------------------

// beginSelect starts a selection where the button went down, or takes the word
// under it when the same spot was pressed a moment ago.
func (m *Model) beginSelect(e tea.Mouse) bool {
	pane, at, ok := m.selPointAt(e.X, e.Y)
	if !ok {
		return false
	}
	// The press that came before, not the selection it made: a first click
	// selects nothing and is cleared on release, and it is still the first
	// half of the double-click that follows it.
	double := !m.sel.clickWhen.IsZero() && m.sel.pane == pane &&
		m.sel.clickAt == at && time.Since(m.sel.clickWhen) < dblClick

	// A new press starts a new record, carrying over only the row under the
	// pointer — which a double-click needs, to find the word it is on. The
	// rest belonged to the selection that just ended, and a map that is never
	// emptied grows for as long as the session does.
	rows := map[int]string{}
	if line, ok := m.sel.rows[at.row]; ok && m.sel.pane == pane {
		rows[at.row] = line
	}
	m.sel = selection{
		pane: pane, anchor: at, head: at, dragging: true, on: true,
		rows: rows, clickAt: at, clickWhen: time.Now(),
	}
	if double {
		if lo, hi, found := m.wordAt(at); found {
			m.sel.anchor = selPoint{row: at.row, col: lo}
			m.sel.head = selPoint{row: at.row, col: hi}
			m.sel.dragging = false
		}
	}
	return true
}

// extendSelect moves the loose end, scrolling the pane when the pointer is
// dragged past its edge — otherwise nothing longer than the window could be
// selected at all.
func (m *Model) extendSelect(e tea.Mouse) {
	if !m.sel.dragging {
		return
	}
	left, top, w, h, ok := m.paneBox(m.sel.pane)
	if !ok {
		return
	}
	switch {
	case e.Y < top:
		m.scrollPane(m.sel.pane, -1)
	case e.Y >= top+h:
		m.scrollPane(m.sel.pane, 1)
	}
	yoff, xoff := m.paneScroll(m.sel.pane)
	m.sel.head = selPoint{
		row: yoff + clamp(e.Y-top, 0, h-1),
		col: xoff + clamp(e.X-left, 0, w),
	}
}

// scrollPane moves a pane by a row, for a drag that has run off its edge.
func (m *Model) scrollPane(f focus, dir int) {
	switch f {
	case focusChat:
		if dir > 0 {
			m.chat.ScrollDown(1)
		} else {
			m.chat.ScrollUp(1)
		}
	case focusPreview:
		if dir > 0 {
			m.prev.ScrollDown(1)
		} else {
			m.prev.ScrollUp(1)
		}
	}
}

// endSelect finishes the gesture and copies what was selected.
//
// On release rather than on a keystroke, because a selection in a terminal
// that is not on the clipboard is not a selection, it is a highlight: there is
// no menu to reach for, and no second gesture anyone would think to make.
func (m *Model) endSelect() tea.Cmd {
	if !m.sel.on {
		return nil
	}
	m.sel.dragging = false
	text := m.selectedText()
	if text == "" {
		// A plain click. Not a selection, and not worth leaving a mark for.
		m.sel.on = false
		return nil
	}
	return m.copyText(text)
}

// copyText puts text on the clipboard twice over: through whatever tool this
// machine has, and through the terminal itself. The second is how it works
// over ssh, where no local tool can reach the clipboard you are looking at;
// the first is how it works in a terminal that ignores the escape sequence,
// which is most of the ones that are not new.
func (m *Model) copyText(text string) tea.Cmd {
	m.notice = "copied " + plural(strings.Count(text, "\n")+1, "line")
	return tea.Batch(
		tea.SetClipboard(text),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := clipboard.Write(ctx, text); err != nil {
				return copyFailedMsg{err: err}
			}
			return nil
		},
	)
}

// copyFailedMsg says the clipboard could not be reached. It is only a notice:
// the escape sequence may well have worked, and a failure here is no reason to
// throw the selection away.
type copyFailedMsg struct{ err error }

// clearSelection drops the selection, reporting whether there was one. Esc
// reaches it first: it is the most local thing on the screen, and the least
// costly to be wrong about.
func (m *Model) clearSelection() bool {
	if !m.sel.on {
		return false
	}
	m.sel = selection{}
	return true
}

// ---- the text ---------------------------------------------------------------

// selectedText is what the selection covers, as plain text.
func (m *Model) selectedText() string {
	a, b, ok := m.sel.span()
	if !ok {
		return ""
	}
	out := make([]string, 0, b.row-a.row+1)
	for row := a.row; row <= b.row; row++ {
		line, drawn := m.sel.rows[row]
		if !drawn {
			continue
		}
		lo, hi := 0, -1
		if row == a.row {
			lo = a.col
		}
		if row == b.row {
			hi = b.col
		}
		out = append(out, strings.TrimRight(cutCells(line, lo, hi), " "))
	}
	// A drag that landed on nothing but padding is not worth putting on
	// anyone's clipboard.
	text := strings.Join(out, "\n")
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return text
}

// cutCells takes columns [lo, hi) of a drawn row as plain text. A negative hi
// means to the end of it.
func cutCells(line string, lo, hi int) string {
	if hi < 0 {
		return ansi.Strip(ansi.TruncateLeft(line, lo, ""))
	}
	if hi <= lo {
		return ""
	}
	return ansi.Strip(ansi.Cut(line, lo, hi))
}

// wordAt finds the run of word characters around a column, for a double-click.
func (m *Model) wordAt(at selPoint) (lo, hi int, ok bool) {
	line, drawn := m.sel.rows[at.row]
	if !drawn {
		return 0, 0, false
	}
	return wordSpan(ansi.Strip(line), at.col)
}

func wordSpan(line string, col int) (lo, hi int, ok bool) {
	r := []rune(line)
	if col < 0 || col >= len(r) || !isWordRune(r[col]) {
		return 0, 0, false
	}
	lo, hi = col, col+1
	for lo > 0 && isWordRune(r[lo-1]) {
		lo--
	}
	for hi < len(r) && isWordRune(r[hi]) {
		hi++
	}
	return lo, hi, true
}

// isWordRune says what a double-click takes. Slashes, dots and colons are part
// of a word here, because what gets double-clicked in a transcript is a path,
// a dotted name or a package line far more often than an English word.
func isWordRune(r rune) bool {
	switch r {
	case '_', '-', '.', '/', '\\', '@', ':', '~', '+', '#':
		return true
	}
	switch {
	case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		return true
	case r <= 0x7f:
		return false
	}
	// Anything else non-ASCII is a letter as far as this is concerned — the
	// transcript is often Vietnamese — except the glyphs the UI draws rails
	// and marks with, which sit against text without being part of it.
	return !isMarkRune(r)
}

func isMarkRune(r rune) bool {
	switch {
	case r >= 0x2500 && r <= 0x257f: // box drawing
		return true
	case r == '·', r == '▸', r == '▪', r == '…', r == '│', r == '⎿':
		return true
	}
	return false
}

// ---- drawing ----------------------------------------------------------------

// paintSelection draws the selection over a pane that has already been
// rendered, and records the rows it sees on the way past.
//
// An overlay rather than part of the content, because the content is cached:
// the transcript is rebuilt only when the conversation changes, and a
// selection changes on every frame of a drag. Painting afterwards keeps the
// two apart, and means the selection knows nothing about markdown, diffs or
// syntax — it colours cells.
func (m *Model) paintSelection(view string, pane focus, width int) string {
	if !m.sel.on || m.sel.pane != pane {
		return view
	}
	if m.sel.rows == nil {
		m.sel.rows = map[int]string{}
	}
	yoff, xoff := m.paneScroll(pane)
	lines := strings.Split(view, "\n")
	a, b, any := m.sel.span()

	for i, line := range lines {
		row := yoff + i
		m.sel.rows[row] = line
		if !any || row < a.row || row > b.row {
			continue
		}
		lo, hi := 0, width
		if row == a.row {
			lo = a.col - xoff
		}
		if row == b.row {
			hi = b.col - xoff
		}
		lines[i] = m.paintRow(line, clamp(lo, 0, width), clamp(hi, 0, width))
	}
	return strings.Join(lines, "\n")
}

// paintRow puts the selection colour on columns [lo, hi) of one drawn row.
//
// The selected span loses its own colours first. A selection that let the
// syntax show through would have to be a tint, and a tint dark enough not to
// fight the colours under it is one you cannot see; every terminal does the
// same thing for the same reason.
func (m *Model) paintRow(line string, lo, hi int) string {
	if hi <= lo {
		return line
	}
	mid := ansi.Strip(ansi.Cut(line, lo, hi))
	// Pad to the width of the span, so a selection running past the end of a
	// short line still reads as a block rather than as a ragged edge.
	if n := hi - lo - ansi.StringWidth(mid); n > 0 {
		mid += strings.Repeat(" ", n)
	}
	return ansi.Truncate(line, lo, "") +
		m.st.Select.Render(mid) +
		ansi.TruncateLeft(line, hi, "")
}
