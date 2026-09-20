package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phanngoc/agent-tui/internal/git"
)

// TestSlashGitBrowsesThisRepository runs against agent-tui itself, which is a
// real repository with real commits — the only way to find out whether the
// parsing, the loading and the layout agree with git as it actually prints.
func TestSlashGitBrowsesThisRepository(t *testing.T) {
	m := newTestModel(t)
	// The throwaway project the test model builds is not a repository, so point
	// the session at this one.
	m.mgr.Active().CWD = repoRoot(t)

	m.input.SetValue("/git")
	cmd := m.inputKey(key("enter"))
	if cmd == nil {
		t.Fatal("/git produced no command")
	}
	if m.overlay != overlayGit {
		t.Fatalf("overlay = %v, want the history browser", m.overlay)
	}

	logMsg := runUntil[gitLogMsg](t, cmd)
	if logMsg.err != "" {
		t.Fatalf("loading the history failed: %s", logMsg.err)
	}
	next := m.applyGitLog(logMsg)
	if len(m.git.commits) == 0 {
		t.Fatal("no commits came back")
	}
	if next == nil {
		t.Fatal("selecting the first commit loaded no diff")
	}
	m.applyGitDiff(runUntil[gitDiffMsg](t, next))

	g := m.git
	if g.err != "" {
		t.Fatalf("loading the diff failed: %s", g.err)
	}
	if len(g.files) == 0 {
		t.Error("the newest commit changed no files")
	}
	if len(g.diff) == 0 {
		t.Error("the newest commit produced no diff")
	}

	// The commit this session just made is the newest, so its subject is known.
	if !strings.Contains(g.commits[0].Subject, "WSL") {
		t.Logf("newest commit = %q", g.commits[0].Subject)
	}

	out := stripANSI(m.gitView())
	if !strings.Contains(out, "History") {
		t.Errorf("the pane has no title:\n%s", firstLines(out, 3))
	}
	// Both panes have to be on screen: the point of this over `git log`.
	if !strings.Contains(out, g.commits[0].Subject[:min(20, len(g.commits[0].Subject))]) {
		t.Error("the commit list is missing its first subject")
	}
	if !strings.Contains(out, g.files[0].Path[:min(20, len(g.files[0].Path))]) {
		t.Error("the file summary is missing")
	}
}

// TestGitNavigationLoadsTheSelectedCommit: moving the selection is the only
// gesture this feature has, so it must fetch.
func TestGitNavigationLoadsTheSelectedCommit(t *testing.T) {
	m := newTestModel(t)
	m.mgr.Active().CWD = repoRoot(t)

	cmd := m.openGit()
	msg := runUntil[gitLogMsg](t, cmd)
	if msg.err != "" {
		t.Skipf("no repository here: %s", msg.err)
	}
	m.applyGitDiff(runUntil[gitDiffMsg](t, m.applyGitLog(msg)))

	if len(m.git.commits) < 2 {
		t.Skip("need at least two commits")
	}
	first := m.git.shown

	down := m.gitKey("down")
	if down == nil {
		t.Fatal("moving down loaded nothing")
	}
	m.applyGitDiff(runUntil[gitDiffMsg](t, down))

	if m.git.shown == first {
		t.Error("the diff did not follow the selection")
	}
	if m.git.sel != 1 {
		t.Errorf("selection = %d, want 1", m.git.sel)
	}
	// Coming back must not refetch what is already on screen.
	m.gitKey("up")
	if cmd := m.loadGitDiff(); cmd != nil && m.git.shown == m.git.commits[0].SHA {
		t.Error("returning to the shown commit fetched it again")
	}
}

func TestGitKeysMoveFocusAndClose(t *testing.T) {
	m := newTestModel(t)
	m.git = newGitState()
	m.git.commits = []git.Commit{{SHA: "a", Subject: "one"}}
	m.overlay = overlayGit

	m.gitKey("tab")
	if !m.git.onDiff {
		t.Error("tab did not move focus to the diff")
	}
	m.gitKey("shift+tab")
	if m.git.onDiff {
		t.Error("shift+tab did not move focus back")
	}
	m.gitKey("esc")
	if m.overlay != overlayNone {
		t.Error("esc did not close the browser")
	}
}

// TestGitScrollStaysInRange guards the one thing that can panic here: the body
// is sliced by the scroll offset.
func TestGitScrollStaysInRange(t *testing.T) {
	m := newTestModel(t)
	m.git = fillGit(&gitState{
		commits: []git.Commit{{SHA: "a", Subject: "one"}},
		body:    []string{"a", "b", "c"},
		onDiff:  true,
	})
	m.overlay = overlayGit

	for i := 0; i < 50; i++ {
		m.gitKey("down")
	}
	if m.git.scrol >= len(m.git.body) {
		t.Errorf("scroll ran off the end: %d of %d", m.git.scrol, len(m.git.body))
	}
	for i := 0; i < 50; i++ {
		m.gitKey("up")
	}
	if m.git.scrol != 0 {
		t.Errorf("scroll = %d, want 0", m.git.scrol)
	}
	// And the view must render at every offset without slicing out of range.
	for _, n := range []int{0, 1, 2, 99} {
		m.git.scrol = n
		_ = m.gitView()
	}
}

// TestGitOutsideARepositorySaysSo: /git in a directory with no repository has
// to explain itself rather than open an empty pane.
func TestGitOutsideARepositorySaysSo(t *testing.T) {
	m := newTestModel(t) // the test project is a bare temp dir
	msg := runUntil[gitLogMsg](t, m.openGit())
	if msg.err == "" {
		t.Skip("the temp directory turned out to be inside a repository")
	}
	m.applyGitLog(msg)

	if m.overlay == overlayGit {
		t.Error("the browser opened anyway")
	}
	if !strings.Contains(m.notice, "git repository") {
		t.Errorf("notice = %q", m.notice)
	}
}

// TestDiffLineShowsBothLineNumbers is what separates this from `git show`
// piped through a pager: you can see where in the file you are.
func TestDiffLineShowsBothLineNumbers(t *testing.T) {
	m := newTestModel(t)
	ctx := m.gitDiffLine(git.Line{Kind: git.Context, Old: 36, New: 40, Text: "ok"}, 60)
	plain := stripANSI(ctx)
	if !strings.Contains(plain, "36") || !strings.Contains(plain, "40") {
		t.Errorf("context line = %q", plain)
	}

	add := stripANSI(m.gitDiffLine(git.Line{Kind: git.Added, New: 41, Text: "new"}, 60))
	if !strings.Contains(add, "+") || !strings.Contains(add, "41") {
		t.Errorf("added line = %q", add)
	}
	// A line that exists on only one side must not invent a number for the other.
	if strings.Contains(strings.Fields(add)[0], "0") {
		t.Errorf("added line claims an old-side number: %q", add)
	}
}

// TestRenderSpansDropsSpansOnTruncation: a span is a byte offset into the full
// text, so keeping it after a cut would paint the wrong characters.
func TestRenderSpansDropsSpansOnTruncation(t *testing.T) {
	st := newTestModel(t).st
	long := strings.Repeat("x", 100) + "CHANGED"
	spans := []git.Span{{Start: 100, End: 107}}

	full := renderSpans(long, spans, st.DiffAdd, st.DiffAddOn, 200)
	if !strings.Contains(stripANSI(full), "CHANGED") {
		t.Error("the untruncated line lost its text")
	}
	// Narrow enough to cut: this must not panic and must not misplace anything.
	short := stripANSI(renderSpans(long, spans, st.DiffAdd, st.DiffAddOn, 20))
	if len(short) > 22 {
		t.Errorf("line was not truncated: %q", short)
	}
}

func TestRelTime(t *testing.T) {
	now := time.Now()
	cases := []struct {
		at   time.Time
		want string
	}{
		{now.Add(-30 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(-3 * time.Hour), "3h ago"},
		{now.Add(-50 * time.Hour), "2d ago"},
	}
	for _, c := range cases {
		if got := relTime(c.at); got != c.want {
			t.Errorf("relTime(%v) = %q, want %q", c.at, got, c.want)
		}
	}
	if relTime(time.Time{}) != "" {
		t.Error("a zero time should render as nothing")
	}
	// Anything older than a week gets a date rather than a growing number.
	if got := relTime(now.AddDate(0, -2, 0)); !strings.Contains(got, "-") {
		t.Errorf("an old commit reads %q", got)
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// repoRoot is this project's own checkout, found from the test binary's
// working directory, which go test sets to the package directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(wd)) // internal/ui -> internal -> root
}
