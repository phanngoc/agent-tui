package ui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/git"
)

// gitFixture opens the browser on a history long enough to scroll, without
// touching a repository: the navigation is what is under test.
func gitFixture(t *testing.T, commits int) *Model {
	t.Helper()
	m := newTestModel(t)
	list := make([]git.Commit, commits)
	for i := range list {
		n := strconv.Itoa(i)
		list[i] = git.Commit{SHA: "sha" + n, Short: "sha" + n, Subject: "commit " + n, Author: "a"}
	}
	m.git = newGitState()
	m.git.commits, m.git.shown, m.git.root = list, "sha0", "/repo"
	m.overlay = overlayGit
	return m
}

// visibleCommits are the indices the commit column actually draws.
func visibleCommits(m *Model) []int {
	var out []int
	rows := m.gitListRows(m.gitListWidth(), m.gitRows())
	for i := m.git.top; i < len(m.git.commits) && len(out)*2 < len(rows); i++ {
		out = append(out, i)
	}
	return out
}

func sees(m *Model, i int) bool {
	for _, v := range visibleCommits(m) {
		if v == i {
			return true
		}
	}
	return false
}

// Moving down keeps the selection on screen.
//
// The column and its window used to be measured in different units — a commit
// takes two rows, and the window was counted in rows — so the selection carried
// on down for half a pane after it had left the bottom of it, and you were
// choosing commits you could not see.
func TestSelectionStaysOnScreenGoingDown(t *testing.T) {
	m := gitFixture(t, 120)
	for range 119 {
		m.gitKey("down")
		if !sees(m, m.git.sel) {
			t.Fatalf("commit %d is selected and not drawn (top=%d, fits %d)",
				m.git.sel, m.git.top, m.gitListCap())
		}
	}
	for range 119 {
		m.gitKey("up")
		if !sees(m, m.git.sel) {
			t.Fatalf("commit %d is selected and not drawn (top=%d)", m.git.sel, m.git.top)
		}
	}
}

// A page moves by what a page holds. Moving by the row count would skip half
// the history on every press, because each commit is two rows.
func TestPageMovesByWhatFits(t *testing.T) {
	m := gitFixture(t, 120)
	m.gitKey("pgdown")
	if got, want := m.git.sel, m.gitListCap(); got != want {
		t.Errorf("pgdown moved to %d, want %d — one screen of commits", got, want)
	}
	if !sees(m, m.git.sel) {
		t.Error("the selection is off screen after a page")
	}
}

// There is a commit of context past the selection where the history has one,
// so moving down shows where you are going and not only where you are.
func TestSelectionKeepsContextBelow(t *testing.T) {
	m := gitFixture(t, 120)
	for range 40 {
		m.gitKey("down")
	}
	if !sees(m, m.git.sel+1) {
		t.Errorf("nothing is drawn past the selection at %d (top=%d)", m.git.sel, m.git.top)
	}
	// At the end of the history there is nothing left to show, and the pane
	// must not scroll into blank rows trying.
	m.gitKey("G")
	if last := len(m.git.commits) - 1; !sees(m, last) || m.git.sel != last {
		t.Errorf("end of history: sel=%d top=%d", m.git.sel, m.git.top)
	}
}

// The diff stops with its last line at the bottom of the pane. Scrolling past
// the end is how you lose your place in a patch: nothing moves on screen except
// the content leaving it.
func TestDiffStopsAtItsLastLine(t *testing.T) {
	m := gitFixture(t, 1)
	body := make([]string, 200)
	for i := range body {
		body[i] = "line " + strconv.Itoa(i)
	}
	m.git.body, m.git.onDiff = body, true
	// The pane caches the shape it laid out for, so a hand-made body has to
	// say which shape it is, or the next draw rebuilds it from the commit.
	m.git.builtW, m.git.builtRows = 40, m.gitRows()

	for range 500 {
		m.gitKey("down")
	}
	if want := len(body) - m.diffRows(); m.git.scrol != want {
		t.Errorf("scrolled to %d, want %d — the last line at the bottom", m.git.scrol, want)
	}
	// The pane is full at rest, rather than showing one line and empty space.
	if got := len(m.gitDiffRows(40, m.gitRows())); got != m.gitRows() {
		t.Errorf("the pane drew %d of %d rows at the end", got, m.gitRows())
	}
	m.gitKey("G")
	if want := len(body) - m.diffRows(); m.git.scrol != want {
		t.Errorf("G went to %d, want %d", m.git.scrol, want)
	}
}

// A commit that touches forty files is not readable a page at a time, so ] and
// [ put the next and previous file at the top of the pane.
func TestBracketsJumpBetweenFiles(t *testing.T) {
	m := gitFixture(t, 1)
	m.git.body = make([]string, 300)
	m.git.marks = []int{10, 80, 150}
	m.git.onDiff = true

	for _, want := range []int{10, 80, 150, 150} {
		m.gitKey("]")
		if m.git.scrol != want {
			t.Fatalf("] went to %d, want %d", m.git.scrol, want)
		}
	}
	for _, want := range []int{80, 10, 0, 0} {
		m.gitKey("[")
		if m.git.scrol != want {
			t.Fatalf("[ went to %d, want %d", m.git.scrol, want)
		}
	}
}

// The wheel scrolls what the pointer is over. Before this it reached past the
// browser and scrolled the transcript underneath, which is not even on screen.
func TestWheelScrollsThePaneUnderThePointer(t *testing.T) {
	m := gitCommitWith(t, 3)
	for i := range 118 {
		m.git.commits = append(m.git.commits, git.Commit{
			SHA: "x" + strconv.Itoa(i), Subject: "commit", Author: "a",
		})
	}
	m.overlayX, m.overlayY = 2, 1

	listX := m.overlayX + 2
	diffX := listX + m.gitListWidth() + 3
	// Below the pinned summary, which has a scroll of its own.
	y := gitTop(m) + m.pinnedRows() + 1

	before := m.chat.YOffset()

	m.gitWheel(diffX, y, 3)
	if m.git.scrol == 0 {
		t.Error("the wheel over the diff did not scroll it")
	}
	if m.git.top != 0 {
		t.Error("the wheel over the diff moved the commit column")
	}

	m.gitWheel(listX, y, 3)
	if m.git.top == 0 {
		t.Error("the wheel over the list did not scroll it")
	}
	// Reading is not choosing: the wheel must not load a diff per notch.
	if m.git.sel != 0 {
		t.Errorf("the wheel moved the selection to %d", m.git.sel)
	}
	if m.chat.YOffset() != before {
		t.Error("the wheel reached the transcript behind the browser")
	}
}

// Clicking a commit selects it, including on the author line underneath the
// subject — both rows are that commit.
func TestClickSelectsTheCommitUnderThePointer(t *testing.T) {
	m := gitFixture(t, 120)
	m.overlayX, m.overlayY = 2, 1
	listX := m.overlayX + 2
	top := gitTop(m)

	if cmd := m.gitClick(listX, top+6); cmd == nil {
		t.Fatal("clicking a commit loaded no diff")
	}
	if m.git.sel != 3 {
		t.Errorf("clicked the fourth commit's subject, selected %d", m.git.sel)
	}
	m.gitClick(listX, top+7) // the author line of the same commit
	if m.git.sel != 3 {
		t.Errorf("clicking the author line selected %d", m.git.sel)
	}

	// Clicking the diff moves the keyboard there, the way tab does.
	m.gitClick(listX+m.gitListWidth()+3, top+1)
	if !m.git.onDiff {
		t.Error("clicking the diff did not focus it")
	}
}

// The footer has to name the keys it now has, or they are keys nobody finds.
func TestBrowserNamesItsKeys(t *testing.T) {
	m := gitFixture(t, 3)
	out := stripANSI(m.gitView())
	for _, want := range []string{"[ ]", "tab", "esc"} {
		if !strings.Contains(out, want) {
			t.Errorf("the footer does not mention %q:\n%s", want, out)
		}
	}
}

// Clicking a name in the file summary goes to that file's patch. Until now the
// summary was a list you read and then went looking for, stepping with [ and ]
// until the file you had already chosen came round.
func TestClickingAFileGoesToItsPatch(t *testing.T) {
	m := gitFixture(t, 1)
	m.git.files = []git.FileChange{
		{Path: "a.go", Added: 10}, {Path: "b.go", Added: 20}, {Path: "c.go", Added: 5},
	}
	m.git.diff = []git.File{
		{Path: "a.go", Hunks: []git.Hunk{{Header: "h", Lines: manyLines(40)}}},
		{Path: "b.go", Hunks: []git.Hunk{{Header: "h", Lines: manyLines(40)}}},
		{Path: "c.go", Hunks: []git.Hunk{{Header: "h", Lines: manyLines(40)}}},
	}
	m.overlayX, m.overlayY = 2, 1
	diffX := m.overlayX + 2 + m.gitListWidth() + 3
	top := gitTop(m)

	// Draw it: the summary records which of its lines names which file as it
	// is built, the same way the header's switches record where they went.
	m.gitDiffRows(m.gitWidth()-2-m.gitListWidth()-3, m.gitRows())

	row, ok := -1, false
	for line, i := range m.git.rows {
		if i == 2 { // the third file
			row, ok = line, true
		}
	}
	if !ok {
		t.Fatal("the summary recorded no rows")
	}

	m.gitClick(diffX, top+row)

	if m.git.scrol != m.git.marks[2] {
		t.Errorf("clicking the third file went to line %d, want %d",
			m.git.scrol, m.git.marks[2])
	}
	if !m.git.onDiff {
		t.Error("clicking a file did not move the keyboard to the diff")
	}
}

func manyLines(n int) []git.Line {
	out := make([]git.Line, n)
	for i := range out {
		out[i] = git.Line{Kind: git.Added, New: i + 1, Text: "line"}
	}
	return out
}

// The diff counts are written with an ASCII hyphen. U+2212 MINUS SIGN is East
// Asian Ambiguous: Go counts it as one column and a terminal may draw it as
// two, which put "−10" from the file summary into the commit list beside it.
func TestTheFileSummaryHoldsNoAmbiguousGlyphs(t *testing.T) {
	m := gitFixture(t, 1)
	m.git.files = []git.FileChange{{Path: "a.go", Added: 10, Deleted: 3}}
	m.git.diff = []git.File{{Path: "a.go", Hunks: []git.Hunk{{
		Header: "h",
		Lines:  []git.Line{{Kind: git.Deleted, Old: 1, Text: "gone"}},
	}}}}

	out := stripANSI(strings.Join(m.gitDiffRows(60, m.gitRows()), "\n"))
	for _, bad := range []string{"−", "→"} {
		if strings.Contains(out, bad) {
			t.Errorf("the diff pane draws %q, whose width the terminal decides:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "-3") {
		t.Errorf("the deletion count went missing with the glyph:\n%s", out)
	}
}

// gitCommitWith fills the browser with one commit of n files, each with a
// patch long enough to scroll the others off.
func gitCommitWith(t *testing.T, n int) *Model {
	t.Helper()
	m := gitFixture(t, 1)
	for i := range n {
		name := "pkg/file" + strconv.Itoa(i) + ".go"
		m.git.files = append(m.git.files, git.FileChange{Path: name, Added: 10, Deleted: 2})
		m.git.diff = append(m.git.diff, git.File{
			Path: name, Hunks: []git.Hunk{{Header: "func x()", Lines: manyLines(30)}},
		})
	}
	return m
}

// The file summary stays put while the patch moves under it. It is the thing
// you navigate from: click a file, land in its patch, and the list of the
// others is still there to click next.
func TestTheFileSummaryStaysPut(t *testing.T) {
	m := gitCommitWith(t, 4)
	w := m.gitWidth() - 2 - m.gitListWidth() - 3
	m.gitDiffRows(w, m.gitRows())

	if m.pinnedRows() == 0 {
		t.Fatal("a four-file summary was not pinned")
	}
	m.showFile(3) // the last file, far down the patch
	rows := m.gitDiffRows(w, m.gitRows())

	out := stripANSI(strings.Join(rows, "\n"))
	for i := range 4 {
		if !strings.Contains(out, "pkg/file"+strconv.Itoa(i)+".go") {
			t.Errorf("file %d left the summary after jumping to the last one:\n%s", i, out)
		}
	}
	// And the patch under it is the one that was asked for.
	body := stripANSI(strings.Join(rows[m.pinnedRows():], "\n"))
	if !strings.Contains(body, "pkg/file3.go") {
		t.Errorf("the pane did not go to the file:\n%s", body)
	}
}

// Clicking a file works from anywhere, including when the patch under the
// summary has been scrolled a long way down.
func TestClickingAFileWorksWhileScrolled(t *testing.T) {
	m := gitCommitWith(t, 4)
	w := m.gitWidth() - 2 - m.gitListWidth() - 3
	m.gitDiffRows(w, m.gitRows())
	m.showFile(3)

	m.overlayX, m.overlayY = 2, 1
	diffX := m.overlayX + 2 + m.gitListWidth() + 3
	top := gitTop(m)

	row := -1
	for line, i := range m.git.rows {
		if i == 1 {
			row = line
		}
	}
	if row < 0 {
		t.Fatal("the summary recorded no rows")
	}
	m.gitClick(diffX, top+row)

	if m.git.scrol != m.git.marks[1] {
		t.Errorf("clicking the second file went to %d, want %d", m.git.scrol, m.git.marks[1])
	}
}

// A summary taller than half the pane is not pinned. Pressing `f` on a commit
// that touches forty files is a request to read the list, and a pinned list
// filling the screen would leave nothing to pin it over.
func TestALongSummaryIsNotPinned(t *testing.T) {
	m := gitCommitWith(t, 40)
	m.git.allFiles = true
	w := m.gitWidth() - 2 - m.gitListWidth() - 3
	m.gitDiffRows(w, m.gitRows())

	if m.pinnedRows() != 0 {
		t.Errorf("a forty-file summary pinned %d of %d rows", m.pinnedRows(), m.gitRows())
	}
	// It scrolls with the patch instead, so it is still reachable, and the
	// marks still point at the files they name.
	m.scrollDiff("]")
	if m.git.scrol != m.git.marks[0] {
		t.Errorf("] went to %d, want the first file at %d", m.git.scrol, m.git.marks[0])
	}
	rows := m.gitDiffRows(w, m.gitRows())
	if !strings.Contains(stripANSI(strings.Join(rows, "\n")), "pkg/file0.go") {
		t.Error("the first file's patch is not on screen")
	}
}

// The two panes and the furniture between them add up to the width they were
// given, exactly.
//
// A row one column too wide is not something measuring a line will find:
// lipgloss wraps the extra column and pads both halves back to the full width,
// so every line still measures right. What gives it away is that there are
// more lines than rows — and where the wrapped remainder lands is the left
// edge of the box, on top of the commit list.
func TestTheBrowserRowsAddUp(t *testing.T) {
	m := gitFixture(t, 3)
	inner := m.gitWidth() - 2
	listW := m.gitListWidth()
	diffW := inner - listW - 4

	// " " + list + " " + separator + " " + diff
	if got := 1 + listW + 1 + 1 + 1 + diffW; got != inner {
		t.Errorf("a body row is %d columns inside a %d column box", got, inner)
	}
}

// The same thing from the outside: content long enough to fill both panes must
// not produce more lines than the browser has rows.
func TestLongContentDoesNotAddRows(t *testing.T) {
	short := gitFixture(t, 3)
	short.git.files = []git.FileChange{{Path: "a.go", Added: 1}}

	long := gitFixture(t, 3)
	for i := range 6 {
		long.git.files = append(long.git.files, git.FileChange{
			Path:    "dashboard/src/app/api/projects/[projectId]/latency/route" + strconv.Itoa(i) + ".ts",
			Added:   247,
			Deleted: 236,
		})
	}
	long.git.commits[0].Subject = strings.Repeat("a very long commit subject ", 6)

	a := len(strings.Split(short.gitView(), "\n"))
	b := len(strings.Split(long.gitView(), "\n"))
	if a != b {
		t.Errorf("the browser drew %d lines for short content and %d for long: a row wrapped", a, b)
	}
}

// A commit that touches fifty files does not get fifty rows of a pane that is
// meant to be showing a patch, so the summary is a window onto them — and a
// window you cannot move is a list with most of it missing.
func TestTheFileSummaryScrolls(t *testing.T) {
	m := gitCommitWith(t, 14)
	w := m.gitWidth() - 2 - m.gitListWidth() - 3
	seen := func() string {
		return stripANSI(strings.Join(m.gitDiffRows(w, m.gitRows()), "\n"))
	}

	out := seen()
	if !strings.Contains(out, "pkg/file0.go") {
		t.Fatalf("the window does not start at the first file:\n%s", out)
	}
	if strings.Contains(out, "pkg/file13.go") {
		t.Errorf("all fourteen files are pinned at once:\n%s", out)
	}
	if !strings.Contains(out, "more files below") {
		t.Errorf("nothing says there are more:\n%s", out)
	}

	for range 6 {
		m.gitKey("alt+down")
	}
	out = seen()
	if !strings.Contains(out, "pkg/file13.go") {
		t.Errorf("scrolling down did not reach the last file:\n%s", out)
	}
	if !strings.Contains(out, "more files above") {
		t.Errorf("nothing says what was scrolled past:\n%s", out)
	}

	// It stops at the ends rather than scrolling into nothing.
	for range 20 {
		m.gitKey("alt+down")
	}
	if want := len(m.git.files) - gitFilesShown; m.git.fileTop != want {
		t.Errorf("the window ran to %d, want %d", m.git.fileTop, want)
	}
	for range 40 {
		m.gitKey("alt+up")
	}
	if m.git.fileTop != 0 {
		t.Errorf("the window came back to %d, want 0", m.git.fileTop)
	}
}

// Scrolling the list is reading the list, not choosing from it: the patch
// under the summary stays where it was.
func TestScrollingTheFileListLeavesThePatchAlone(t *testing.T) {
	m := gitCommitWith(t, 14)
	w := m.gitWidth() - 2 - m.gitListWidth() - 3
	m.gitDiffRows(w, m.gitRows())
	m.showFile(3)
	was := m.git.scrol

	m.gitKey("alt+down")
	m.gitDiffRows(w, m.gitRows())

	if m.git.scrol != was {
		t.Errorf("scrolling the summary moved the patch from %d to %d", was, m.git.scrol)
	}
	// And the names still point at the right files after the window moved.
	for line, i := range m.git.rows {
		if i != m.git.fileTop+line-firstFileRow(m) {
			t.Errorf("row %d names file %d, want %d", line, i, m.git.fileTop+line-firstFileRow(m))
		}
	}
}

// firstFileRow is the line the first file name is on.
func firstFileRow(m *Model) int {
	first := 1 << 30
	for line := range m.git.rows {
		first = min(first, line)
	}
	return first
}

// With the browser open, alt+↑↓ belong to the summary. Switching sessions
// behind an overlay is not what they would mean while it is on screen.
func TestAltArrowsBelongToTheBrowserWhileItIsOpen(t *testing.T) {
	m := gitCommitWith(t, 14)
	m.mgr.New() // a second session to switch to, if anything tried
	was := m.mgr.ActiveIndex()

	m.onKey(key("alt+down"))

	if m.mgr.ActiveIndex() != was {
		t.Error("alt+down switched sessions behind the browser")
	}
	if m.git.fileTop == 0 {
		t.Error("alt+down did not move the summary's window")
	}
}

// gitTop is where the browser drew its first body row. It is asked of the view
// rather than counted, for the same reason the hit test asks: the number moved
// once already, and every click moved with it.
func gitTop(m *Model) int {
	m.gitView()
	return m.overlayY + 1 + m.git.bodyY
}

// A row that can be clicked says so before it is clicked. Nothing on a
// terminal indicates that, so the pointer crossing a file underlines it.
func TestHoveringAFileUnderlinesIt(t *testing.T) {
	m := gitCommitWith(t, 4)
	m.overlayX, m.overlayY = 2, 1
	diffX := m.overlayX + 2 + m.gitListWidth() + 3
	top := gitTop(m)

	row := -1
	for line, i := range m.git.rows {
		if i == 1 {
			row = line
		}
	}
	if row < 0 {
		t.Fatal("the summary recorded no rows")
	}

	plain := m.gitView()
	m.onMouse(tea.MouseMotionMsg{X: diffX, Y: top + row})
	if m.git.hover != 1 {
		t.Fatalf("the pointer is over file 1, hover is %d", m.git.hover)
	}
	if m.gitView() == plain {
		t.Error("hovering a file changed nothing on screen")
	}

	// Off the list again and it goes back.
	m.onMouse(tea.MouseMotionMsg{X: diffX, Y: top + m.gitRows() - 1})
	if m.git.hover != -1 {
		t.Errorf("the pointer left the list and hover is %d", m.git.hover)
	}
	if m.gitView() != plain {
		t.Error("the underline outlived the pointer")
	}
}

// Hovering rebuilds the summary and leaves the patch alone: the pointer moves
// a great deal more often than the commit does.
func TestHoveringDoesNotRebuildThePatch(t *testing.T) {
	m := gitCommitWith(t, 4)
	m.overlayX, m.overlayY = 2, 1
	top := gitTop(m)
	diffX := m.overlayX + 2 + m.gitListWidth() + 3

	body := m.git.body
	m.onMouse(tea.MouseMotionMsg{X: diffX, Y: top + m.pinnedRows() - 1})

	if &body[0] != &m.git.body[0] {
		t.Error("hovering rebuilt the patch")
	}
}

// fillGit finishes a hand-made browser state the way newGitState would, for
// the fields whose zero value is not "none".
func fillGit(g *gitState) *gitState {
	g.hover = -1
	return g
}
