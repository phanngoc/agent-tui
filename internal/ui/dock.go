package ui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// Panes that go where you put them.
//
// The layout used to be a fact: sessions over the file tree on the left, the
// transcript in the middle, the preview on the right, and the file tree given
// whatever height the session list did not need — which, on a day spent in
// conversations, is most of the screen spent on a tree nobody is browsing.
//
// Now every part of it is a choice, and every choice is made the way it is
// made in a window manager: with the mouse, on the thing itself.
//
//   - The rule between the session list and the file tree is a divider. Drag
//     it up or down.
//   - Click the file tree's title to fold it to one line; click it again, or
//     drag the line up, to open it.
//   - Drag a pane by its title to move it: the sidebar to the other edge of
//     the screen, the preview to the other side of the transcript, the file
//     tree above or below the session list. While you hold it the status line
//     says where it will land.
//
// Each has a typed spelling too, /layout, because a gesture you cannot name
// is one you cannot tell anyone about. All of it is remembered with the
// widths, in layout.json.

// col names one of the three columns the body is made of.
type col int

const (
	colSide col = iota // sessions and the file tree
	colChat            // the transcript
	colAux             // the preview, or the side chat in its place
)

// dock is where the panes stand. The zero value is the layout the program
// has always had, so a layout file written before this existed reads as it.
type dock struct {
	SideRight bool `json:"side_right,omitempty"` // the sidebar on the right edge
	AuxLeft   bool `json:"aux_left,omitempty"`   // the preview left of the transcript
	TreeTop   bool `json:"tree_top,omitempty"`   // the file tree above the sessions
}

// Heights in the sidebar, in rows. treeMin is a title, two rows of tree and
// room to see that it is one; sessMin is the same for the list.
const (
	treeMin = 4
	sessMin = 4
	// treeShare is the file tree's default part of the column, in percent.
	// It is the pane that is reached for, not lived in: a third is enough to
	// see where you are and click a file, and the session list is what is
	// read all day.
	treeShare = 30
)

// colOrder lists the columns on screen, left to right. A closed column is
// left out, so a neighbour is always one that was drawn.
func (m *Model) colOrder() []col {
	middle := []col{colChat, colAux}
	if m.dock.AuxLeft {
		middle = []col{colAux, colChat}
	}
	order := make([]col, 0, 3)
	if !m.dock.SideRight {
		order = append(order, colSide)
	}
	order = append(order, middle...)
	if m.dock.SideRight {
		order = append(order, colSide)
	}
	out := order[:0]
	for _, c := range order {
		if m.colW(c) > 0 {
			out = append(out, c)
		}
	}
	return out
}

// colW is a column's width, zero when it is closed.
func (m *Model) colW(c col) int {
	switch c {
	case colSide:
		return m.sideW
	case colChat:
		return m.chatW
	default:
		return m.prevW + m.btwW
	}
}

// colX is the screen column a column starts at.
func (m *Model) colX(c col) int {
	x := 0
	for _, o := range m.colOrder() {
		if o == c {
			return x
		}
		x += m.colW(o)
	}
	return x
}

// colLeans reports whether a column leans on a neighbour's rule — whether
// anything is drawn to its left.
func (m *Model) colLeans(c col) bool { return m.colX(c) > 0 }

// ---- the sidebar's two panes ------------------------------------------------

// leftSplit is how the sidebar divides between the session list and the file
// tree. Both the renderer and the mouse handler call it, so a click always
// lands on the row that was drawn.
func (m *Model) leftSplit() (sessH, treeH int) {
	if m.bodyH < sessMin+treeMin {
		treeH = m.bodyH / 2
		if m.treeFold {
			treeH = min(1, m.bodyH)
		}
		return m.bodyH - treeH, treeH
	}
	if m.treeFold {
		return m.bodyH - 1, 1
	}
	want := m.treeSet
	if want <= 0 {
		want = max(treeMin, m.bodyH*treeShare/100)
	}
	treeH = clamp(want, treeMin, m.bodyH-sessMin)
	return m.bodyH - treeH, treeH
}

// sessTop and treeTopY are the screen rows the two sidebar panes begin on.
func (m *Model) sessTopY() int {
	if m.dock.TreeTop {
		_, treeH := m.leftSplit()
		return headerRows + treeH
	}
	return headerRows
}

func (m *Model) treeTopY() int {
	if m.dock.TreeTop {
		return headerRows
	}
	sessH, _ := m.leftSplit()
	return headerRows + sessH
}

// splitRuleY is the row of the rule between the two sidebar panes: the top
// border of the lower one, which is also its title.
func (m *Model) splitRuleY() int {
	if m.dock.TreeTop {
		return m.sessTopY()
	}
	return m.treeTopY()
}

// sidebarPaneAt reports which sidebar pane a screen row is in.
func (m *Model) sidebarPaneAt(y int) focus {
	if y < m.splitRuleY() {
		if m.dock.TreeTop {
			return focusExplorer
		}
		return focusSessions
	}
	if m.dock.TreeTop {
		return focusSessions
	}
	return focusExplorer
}

// setTreeRows records a height for the file tree, opening it if it was
// folded: dragging the line of a folded pane up is how it is opened.
func (m *Model) setTreeRows(rows int) {
	m.treeFold = false
	m.treeSet = clamp(rows, treeMin, max(treeMin, m.bodyH-sessMin))
	m.resize(m.w, m.h)
	m.saveLayout()
}

// toggleTreeFold folds the file tree to its title, or opens it again.
func (m *Model) toggleTreeFold() {
	m.treeFold = !m.treeFold
	if m.treeFold && m.focus == focusExplorer {
		m.setFocus(focusSessions)
	}
	m.resize(m.w, m.h)
	m.saveLayout()
	if m.treeFold {
		m.notice = "files folded — click its title or /layout files open to bring it back"
	} else {
		m.notice = "files open"
	}
}

// leftColumn stacks the two sidebar panes in the order the dock says.
func (m *Model) leftColumn() string {
	sessH, treeH := m.leftSplit()
	seam := seams{left: m.colLeans(colSide), bottom: true}

	sess := m.paneSeam(m.sessionsPane(), "sessions", m.sideW, sessH,
		m.lit(focusSessions), seam)
	var tree string
	if m.treeFold {
		tree = m.foldedBar(m.treeTitle(), m.sideW, seam)
	} else {
		tree = m.paneSeam(m.explorerPane(treeH-2), m.treeTitle(), m.sideW, treeH,
			m.lit(focusExplorer), seam)
	}
	if m.dock.TreeTop {
		return lipgloss.JoinVertical(lipgloss.Left, tree, sess)
	}
	return lipgloss.JoinVertical(lipgloss.Left, sess, tree)
}

// foldedBar is a pane reduced to its title: the top rule, and nothing under
// it. It is the first line of the pane it stands for, so it is the same line
// in the same place, and the pane opens out of it rather than appearing.
func (m *Model) foldedBar(title string, w int, seam seams) string {
	box := m.renderPane("", title, w, 3, false, seam)
	if i := strings.IndexByte(box, '\n'); i >= 0 {
		return box[:i]
	}
	return box
}

// ---- dividers ---------------------------------------------------------------

// dividerAt reports which divider a cell is on: one of the rules between
// columns, or the rule between the sidebar's two panes.
func (m *Model) dividerAt(x, y int) dragging {
	if y < headerRows || y >= headerRows+m.bodyH {
		return dragNone
	}
	order := m.colOrder()
	for i := 0; i+1 < len(order); i++ {
		l, r := order[i], order[i+1]
		if x == m.colX(l)+m.colW(l)-1 {
			m.dragPair = [2]col{l, r}
			if l == colSide || r == colSide {
				return dragSide
			}
			return dragPreview
		}
	}
	if m.sideW > 0 && y == m.splitRuleY() {
		left := m.colX(colSide)
		if x >= left && x < left+m.sideW && !m.onSidebarTitle(x, y) {
			return dragSplit
		}
	}
	return dragNone
}

// dragTo moves the divider the mouse has hold of to a cell.
//
// A rule between columns is the left one's last column, so dropping it on x
// makes the left column end at x. Whichever of the two is not the transcript
// is the one resized: the transcript is what is left over, and always was.
func (m *Model) dragTo(x, y int) {
	switch m.drag {
	case dragSplit:
		if m.dock.TreeTop {
			m.setTreeRows(y - headerRows)
		} else {
			m.setTreeRows(headerRows + m.bodyH - y)
		}
		return
	case dragSide, dragPreview:
	default:
		return
	}
	l, r := m.dragPair[0], m.dragPair[1]
	set := func(c col, w int) {
		if c == colSide {
			m.setSideWidth(w)
		} else {
			m.setPrevWidth(w)
		}
	}
	if l != colChat {
		set(l, x+1-m.colX(l))
		return
	}
	set(r, m.colX(r)+m.colW(r)-(x+1))
}

// ---- moving panes by their titles ------------------------------------------

// grab is a pane being carried by its title.
type grab struct {
	pane  focus
	x, y  int // where the press was
	moved bool
}

// titleAt reports which pane's title a cell is on. A title is the top rule
// of a pane, and the part of it with the name in it — the rest of a rule is a
// divider, and taking hold of one is resizing, not moving.
func (m *Model) titleAt(x, y int) (focus, bool) {
	if y < headerRows || y >= headerRows+m.bodyH {
		return 0, false
	}
	if m.sideW > 0 {
		left := m.colX(colSide)
		if x >= left && x < left+m.sideW {
			if !m.onSidebarTitle(x, y) {
				return 0, false
			}
			return m.sidebarPaneAt(y), true
		}
	}
	if y != headerRows {
		return 0, false
	}
	if w := m.colW(colAux); w > 0 {
		pane, title := focusPreview, m.previewTitle()
		if m.btwW > 0 {
			pane, title = focusBtw, m.btwTitle()
		}
		if left := m.colX(colAux); x >= left && x < left+titleEnd(title, w) {
			return pane, true
		}
	}
	return 0, false
}

// titleEnd is how far into a rule its title reaches: the two cells of rule
// before it, then the label as renderPane fits and draws it. Past that is the
// rule itself, which is a divider wherever there is one.
func titleEnd(title string, w int) int {
	label := " " + truncate(title, titleRoom(w)) + " "
	return min(w, 2+lipgloss.Width(label))
}

// onSidebarTitle reports whether a cell is on the title of one of the
// sidebar's panes.
func (m *Model) onSidebarTitle(x, y int) bool {
	var title string
	switch y {
	case m.sessTopY():
		title = "sessions"
	case m.treeTopY():
		title = m.treeTitle()
	default:
		return false
	}
	left := m.colX(colSide)
	return x >= left && x < left+titleEnd(title, m.sideW)
}

// treeTitle is the file tree's title, with the mark that says it folds.
func (m *Model) treeTitle() string {
	if m.treeFold {
		return "▸ " + m.explorerTitle()
	}
	return "▾ " + m.explorerTitle()
}

// dropPlan says what releasing a carried pane at a cell would do, and does it
// when apply is true. An empty description means nothing would change.
func (m *Model) dropPlan(g grab, x, y int, apply bool) string {
	switch g.pane {
	case focusSessions, focusExplorer:
		inSide := m.sideW > 0 && x >= m.colX(colSide) && x < m.colX(colSide)+m.sideW
		if inSide {
			// Within the sidebar: the half it is dropped in is where it goes.
			mid := headerRows + m.bodyH/2
			top := y < mid
			treeTop := (g.pane == focusExplorer) == top
			if treeTop == m.dock.TreeTop {
				return ""
			}
			if apply {
				m.dock.TreeTop = treeTop
				m.applyDock()
			}
			if treeTop {
				return "files above sessions"
			}
			return "sessions above files"
		}
		right := x >= m.w/2
		if right == m.dock.SideRight {
			return ""
		}
		if apply {
			m.dock.SideRight = right
			m.applyDock()
		}
		if right {
			return "sidebar to the right edge"
		}
		return "sidebar to the left edge"

	case focusPreview, focusBtw:
		chatMid := m.colX(colChat) + m.chatW/2
		left := x < chatMid
		if left == m.dock.AuxLeft {
			return ""
		}
		if apply {
			m.dock.AuxLeft = left
			m.applyDock()
		}
		if left {
			return "preview left of the transcript"
		}
		return "preview right of the transcript"
	}
	return ""
}

// carry follows the pointer while a pane is held, saying where it would land.
func (m *Model) carry(x, y int) {
	g := m.grabbed
	if !g.moved && abs(x-g.x)+abs(y-g.y) < 2 {
		return // a click wobbles; a drag goes somewhere
	}
	g.moved = true
	m.grabbed = g
	if plan := m.dropPlan(g, x, y, false); plan != "" {
		m.notice = "release: " + plan
	} else {
		m.notice = "moving " + paneName(g.pane) + " — drop it where you want it"
	}
}

// drop ends a carry. A press that never moved is a click on the title, which
// folds the file tree and does nothing to the other panes.
func (m *Model) drop(x, y int) {
	g := m.grabbed
	m.grabbing = false
	if !g.moved {
		if g.pane == focusExplorer {
			m.toggleTreeFold()
		} else {
			m.notice = ""
		}
		return
	}
	if plan := m.dropPlan(g, x, y, true); plan != "" {
		m.notice = "moved: " + plan
		return
	}
	m.notice = paneName(g.pane) + " stays where it was"
}

func paneName(f focus) string {
	switch f {
	case focusSessions:
		return "sessions"
	case focusExplorer:
		return "files"
	case focusBtw:
		return "side chat"
	}
	return "preview"
}

// applyDock re-lays the screen after a pane moved, and remembers it.
func (m *Model) applyDock() {
	m.resize(m.w, m.h)
	m.forgetFrames()
	m.saveLayout()
}

// ---- /layout ----------------------------------------------------------------

// layoutCommand is the typed half of all of the above.
func (m *Model) layoutCommand(arg string) {
	f := strings.Fields(strings.ToLower(arg))
	if len(f) == 0 {
		m.notice = m.layoutSummary() + "  ·  /layout sidebar|preview left|right · files top|bottom|fold|open|<rows> · reset"
		return
	}
	what, how := f[0], ""
	if len(f) > 1 {
		how = f[1]
	}
	switch what {
	case "reset":
		m.dock, m.treeSet, m.treeFold = dock{}, 0, false
		m.sideSet, m.prevSet = 0, 0
		m.applyDock()
		m.notice = "layout reset: " + m.layoutSummary()
		return
	case "sidebar", "sessions":
		switch how {
		case "left", "right":
			m.dock.SideRight = how == "right"
			m.applyDock()
			m.notice = m.layoutSummary()
			return
		}
	case "preview":
		switch how {
		case "left", "right":
			m.dock.AuxLeft = how == "left"
			m.applyDock()
			m.notice = m.layoutSummary()
			return
		}
	case "files", "tree":
		switch how {
		case "top", "bottom":
			m.dock.TreeTop = how == "top"
			m.applyDock()
			m.notice = m.layoutSummary()
			return
		case "fold", "close", "hide":
			if !m.treeFold {
				m.toggleTreeFold()
			}
			return
		case "open", "show", "unfold":
			if m.treeFold {
				m.toggleTreeFold()
			}
			return
		}
		if n, err := strconv.Atoi(how); err == nil {
			m.setTreeRows(n)
			_, treeH := m.leftSplit()
			m.notice = "files: " + plural(treeH, "row")
			return
		}
	}
	m.notice = "layout: say sidebar left|right, preview left|right, files top|bottom|fold|open|<rows>, or reset"
}

// layoutSummary says where everything is, in the order it is on screen.
func (m *Model) layoutSummary() string {
	side := "sidebar left"
	if m.dock.SideRight {
		side = "sidebar right"
	}
	aux := "preview right"
	if m.dock.AuxLeft {
		aux = "preview left"
	}
	files := "files below sessions"
	if m.dock.TreeTop {
		files = "files above sessions"
	}
	if m.treeFold {
		files += ", folded"
	} else if _, treeH := m.leftSplit(); treeH > 0 {
		files += ", " + plural(treeH, "row")
	}
	return side + " · " + aux + " · " + files
}

// auxText is where the preview column's own rows begin: its first column, or
// one in when nothing stands to its left and it draws its own border there.
func (m *Model) auxText() int {
	if m.colLeans(colAux) {
		return m.colX(colAux)
	}
	return m.colX(colAux) + 1
}

// switchFiles is the header's "files" switch. The tree lives in the sidebar,
// so asking for it with the sidebar closed opens the sidebar too.
func (m *Model) switchFiles() {
	if !m.showSessions {
		m.toggleSessions()
		if m.treeFold {
			m.toggleTreeFold()
		}
		return
	}
	m.toggleTreeFold()
}

// lit says a pane is drawn highlighted: it has the keyboard, or it is the one
// being carried, so the hand can see what it is holding.
func (m *Model) lit(f focus) bool {
	if m.grabbing && m.grabbed.moved && m.grabbed.pane == f {
		return true
	}
	return m.focus == f
}
