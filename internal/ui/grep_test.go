package ui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// typeQuery types into the search box the way a user does, one key at a time.
func typeQuery(t *testing.T, m *Model, q string) {
	t.Helper()
	var last func() tea.Msg
	for _, r := range q {
		_, c := m.Update(key(string(r)))
		if c != nil {
			last = c
		}
	}
	if last == nil {
		return
	}
	m.Update(runUntil[grepMsg](t, last))
}

// TestSearchBoxKeepsEveryLetter guards a real bug: the result list took vim's
// h and l for navigation, so typing "Needle" lost the l out of the query.
func TestSearchBoxKeepsEveryLetter(t *testing.T) {
	m := newTestModel(t)
	press(t, m, "ctrl+g")

	const q = "abcdefghijklmnopqrstuvwxyz"
	for _, r := range q {
		m.Update(key(string(r)))
	}
	if got := m.grepIn.Value(); got != q {
		t.Errorf("the box holds %q, want every letter: %q", got, q)
	}
}

func TestSearchGroupsResultsByFile(t *testing.T) {
	m := newTestModel(t)
	// A word the fixture does not already contain, so the grouping is exact.
	mustWrite(t, filepath.Join(m.idx.Root(), "a.go"), "package a\n// zorple one\n// zorple two\n")
	mustWrite(t, filepath.Join(m.idx.Root(), "sub", "b.go"), "package b\n// zorple three\n")
	m.idx.Build()

	press(t, m, "ctrl+g")
	typeQuery(t, m, "zorple")

	if len(m.grepFiles) != 2 {
		t.Fatalf("grouped into %d files, want 2: %+v", len(m.grepFiles), m.grepFiles)
	}
	counts := map[string]int{}
	for _, f := range m.grepFiles {
		counts[f.path] = len(f.hits)
		if f.name == "" {
			t.Errorf("file %q has no display name", f.path)
		}
	}
	if counts["a.go"] != 2 || counts["sub/b.go"] != 1 {
		t.Errorf("hit counts = %v", counts)
	}

	out := stripANSI(m.View().Content)
	for _, want := range []string{"a.go", "b.go", "sub", "3 results in 2 files"} {
		if !strings.Contains(out, want) {
			t.Errorf("the panel is missing %q:\n%s", want, out)
		}
	}
}

func TestSearchFoldsAFile(t *testing.T) {
	m := newTestModel(t)
	mustWrite(t, filepath.Join(m.idx.Root(), "a.go"), "package a\n// needle one\n// needle two\n")
	m.idx.Build()

	press(t, m, "ctrl+g")
	typeQuery(t, m, "needle")

	rowsOpen := len(m.grepRows)
	m.grepSel = 0 // the file header
	m.grepKey(key("left"))
	if len(m.grepRows) >= rowsOpen {
		t.Errorf("folding did not hide the hits: %d rows then %d", rowsOpen, len(m.grepRows))
	}
	m.grepKey(key("right"))
	if len(m.grepRows) != rowsOpen {
		t.Errorf("unfolding did not bring them back: %d rows, want %d", len(m.grepRows), rowsOpen)
	}
}

func TestSearchOpensTheHitUnderTheCursor(t *testing.T) {
	m := newTestModel(t)
	mustWrite(t, filepath.Join(m.idx.Root(), "a.go"), "package a\n\n// needle here\n")
	m.idx.Build()

	press(t, m, "ctrl+g")
	typeQuery(t, m, "needle")

	// Row 0 is the file header; the first hit is next.
	m.grepKey(key("down"))
	hit, ok := m.selectedHit()
	if !ok {
		t.Fatalf("row %d is not a hit: %+v", m.grepSel, m.grepRows)
	}
	if hit.Line != 3 {
		t.Errorf("hit line = %d, want 3", hit.Line)
	}

	cmd := m.grepKey(key("enter"))
	if cmd == nil {
		t.Fatal("enter did not open the file")
	}
	m.Update(runUntil[fileMsg](t, cmd))
	if m.file == nil || m.file.Rel != "a.go" {
		t.Fatalf("preview shows %+v", m.file)
	}
	if m.fileLine != 3 {
		t.Errorf("opened at line %d, want 3", m.fileLine)
	}
}

func TestSearchTogglesChangeTheQuery(t *testing.T) {
	m := newTestModel(t)
	mustWrite(t, filepath.Join(m.idx.Root(), "a.go"), "package a\n// Needle\n// needle\n")
	m.idx.Build()

	press(t, m, "ctrl+g")
	typeQuery(t, m, "needle")
	both := len(m.grepRes.Matches)
	if both < 2 {
		t.Fatalf("lowercase should match both spellings, got %d", both)
	}

	// Aa: exact case.
	if cmd := m.grepKey(key("alt+a")); cmd != nil {
		m.Update(runUntil[grepMsg](t, cmd))
	}
	if !m.grepCase {
		t.Fatal("alt+a did not turn exact case on")
	}
	if got := len(m.grepRes.Matches); got >= both {
		t.Errorf("exact case matched %d, want fewer than %d", got, both)
	}
	if out := stripANSI(m.View().Content); !strings.Contains(out, "Aa") {
		t.Errorf("the toggles are not shown:\n%s", out)
	}
}

func TestSearchHighlightsTheMatchInContext(t *testing.T) {
	m := newTestModel(t)
	long := "package a\n// " + strings.Repeat("x", 200) + " needle " + strings.Repeat("y", 200) + "\n"
	mustWrite(t, filepath.Join(m.idx.Root(), "a.go"), long)
	m.idx.Build()

	press(t, m, "ctrl+g")
	typeQuery(t, m, "needle")

	if len(m.grepFiles) == 0 || len(m.grepFiles[0].hits) == 0 {
		t.Fatal("no hits")
	}
	hit := m.grepFiles[0].hits[0]
	if got := hit.Text[hit.Start:hit.End]; got != "needle" {
		t.Errorf("the recorded offsets cover %q, want needle", got)
	}
	// A hit buried in a long line still has to be visible.
	out := stripANSI(m.View().Content)
	if !strings.Contains(out, "needle") {
		t.Errorf("the match is not on screen:\n%s", out)
	}
}

func TestWindowAroundKeepsTheMatch(t *testing.T) {
	text := strings.Repeat("a", 100) + "MATCH" + strings.Repeat("b", 100)
	start := 100
	out, s, e := windowAround(text, start, start+5, 40)
	if out[s:e] != "MATCH" {
		t.Errorf("the window cut %q, want MATCH", out[s:e])
	}
	// Measured in columns, not bytes: each ellipsis is one column but three
	// bytes, and the assertion is about what fits on screen.
	if w := ansi.StringWidth(out); w > 42 {
		t.Errorf("the window is %d columns wide, want about 40 plus ellipses", w)
	}

	// A short line is returned untouched.
	short := "a MATCH b"
	out, s, e = windowAround(short, 2, 7, 40)
	if out != short || out[s:e] != "MATCH" {
		t.Errorf("short line became %q with %q", out, out[s:e])
	}
}

// A result you can plainly click used to do nothing at all: onClick handed
// every overlay but the history browser back an untouched nil. A line that
// looks clickable and is not reads as the program being broken, not as the
// feature being absent.
func TestClickingASearchResultOpensIt(t *testing.T) {
	m := newTestModel(t)
	press(t, m, "ctrl+g")
	typeQuery(t, m, "Needle")
	if len(m.grepRows) < 2 {
		t.Fatalf("not enough results to click: %d rows", len(m.grepRows))
	}
	m.View()

	// The row the view says it drew, not the row a second count believes is
	// there — which is the whole reason the view records it.
	want := -1
	var row, screenY int
	for i := m.grepTop; i < m.grepTop+m.grepDrawn && i < len(m.grepRows); i++ {
		if m.grepRows[i].hit >= 0 {
			row, screenY = i, m.overlayY+1+m.grepBodyY+(i-m.grepTop)
			want = i
			break
		}
	}
	if want < 0 {
		t.Fatal("no hit row was drawn")
	}
	hit := m.grepFiles[m.grepRows[row].file].hits[m.grepRows[row].hit]

	cmd := clickAt(m, m.overlayX+4, screenY)
	if cmd == nil {
		t.Fatal("the click opened nothing")
	}
	m.Update(runUntil[fileMsg](t, cmd))

	if m.grepSel != row {
		t.Errorf("the click selected row %d, want %d", m.grepSel, row)
	}
	if m.overlay != overlayNone {
		t.Error("the overlay stayed open over the file it opened")
	}
	if !m.showPreview {
		t.Error("the preview was not opened to show it")
	}
	// The file it asked for is the one the row named, at the line it named.
	if m.fileLine != hit.Line {
		t.Errorf("the preview was sent to line %d, want %d", m.fileLine, hit.Line)
	}
}

// Clicking a file header folds it, because that is the only thing a header
// can mean — and it does not go opening whatever happens to be under it.
func TestClickingAFileHeaderFoldsIt(t *testing.T) {
	m := newTestModel(t)
	press(t, m, "ctrl+g")
	typeQuery(t, m, "Needle")
	m.View()

	row := -1
	for i := m.grepTop; i < m.grepTop+m.grepDrawn && i < len(m.grepRows); i++ {
		if m.grepRows[i].hit < 0 {
			row = i
			break
		}
	}
	if row < 0 {
		t.Skip("no header was drawn")
	}
	file := m.grepRows[row].file
	was := m.grepFiles[file].collapsed

	clickAt(m, m.overlayX+4, m.overlayY+1+m.grepBodyY+(row-m.grepTop))

	if m.grepFiles[file].collapsed == was {
		t.Error("clicking the header did not fold the file")
	}
	if m.overlay != overlayGrep {
		t.Error("clicking a header closed the search")
	}
}

// A click outside the list is not a click on its last row.
func TestClickingOutsideTheListPicksNothing(t *testing.T) {
	m := newTestModel(t)
	press(t, m, "ctrl+g")
	typeQuery(t, m, "Needle")
	m.View()
	was := m.grepSel

	clickAt(m, m.overlayX+4, m.overlayY) // the title
	clickAt(m, m.overlayX+4, m.overlayY+1+m.grepBodyY+m.grepDrawn+1)
	clickAt(m, 0, m.overlayY+1+m.grepBodyY) // outside the box

	if m.grepSel != was {
		t.Errorf("a click off the list moved the cursor to %d", m.grepSel)
	}
	if m.overlay != overlayGrep {
		t.Error("a click off the list closed the search")
	}
}

// The wheel reads without choosing: a list that re-selected as it scrolled
// would make reading past a result the same as picking it.
func TestTheWheelScrollsTheResultsWithoutPicking(t *testing.T) {
	m := newTestModel(t)
	press(t, m, "ctrl+g")
	typeQuery(t, m, "Needle")
	m.View()
	if len(m.grepRows) == 0 {
		t.Fatal("no results to scroll")
	}
	was := m.grepSel

	m.onMouse(tea.MouseWheelMsg{X: m.overlayX + 4, Y: m.overlayY + 4, Button: tea.MouseWheelDown})

	if m.grepSel != was {
		t.Errorf("the wheel moved the selection to %d", m.grepSel)
	}
	// It scrolls as far as there is anywhere to scroll to, and no further.
	if want := clampRow(3, len(m.grepRows)); m.grepTop != want {
		t.Errorf("the wheel scrolled to row %d, want %d of %d rows",
			m.grepTop, want, len(m.grepRows))
	}
}
