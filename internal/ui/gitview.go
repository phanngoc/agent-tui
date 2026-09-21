package ui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/git"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// The history browser: commits down the left, the selected commit's diff down
// the right, after Sublime Merge.
//
// It reads through the session's filesystem, so pointing a session at a WSL
// distribution or a container browses that repository rather than a same-named
// one on the host — the same property that makes ! and the agent work there.
//
// Two panes are the whole design. A commit list that cannot show you the diff
// is a `git log` you have to leave, and a diff you reach by typing a hash is a
// `git show` you have to spell. Moving the selection loads the patch, so
// reading the history is arrow keys and nothing else.

// gitLimit is how far back the list reaches. Far enough to find the commit you
// half-remember, and short enough that opening it is not a wait.
const gitLimit = 300

// gitState is the browser's own state, nil until /git opens it.
type gitState struct {
	// root is the repository, which may be above the session's directory.
	root string
	fs   vfs.FS

	commits []git.Commit
	sel     int
	top     int

	staged, unstaged int

	// fileTop is the first file the pinned summary shows. A commit that
	// touches fifty of them does not get fifty rows of the pane, so the list
	// is a window onto them and this is where the window is.
	fileTop int
	// hover is the file the pointer is over, or -1. It is the summary's own
	// affordance: the rows are clickable, and nothing on a terminal says so
	// until something changes under the pointer.
	hover int
	// allFiles opens the whole file summary rather than its first few.
	allFiles bool

	// The right pane, for whichever commit is selected. head is the summary,
	// which stays put; body is the patch, which scrolls under it.
	head []string
	// builtW and builtRows are the shape the two were laid out for.
	builtW, builtRows int
	shown             string // the sha the diff belongs to
	files             []git.FileChange
	diff              []git.File
	body              []string // rendered right pane, one entry per line
	// marks are the lines in body where a file's patch begins. A commit that
	// touches forty files is unreadable a page at a time, and these are what
	// [ and ] move between.
	marks []int
	// rows maps a line of the file summary back to the file it names, so
	// clicking one goes to that file's patch. A summary you cannot click is a
	// table of contents with no page numbers.
	rows  map[int]int
	scrol int
	// bodyY is the content line the first body row was drawn on, recorded by
	// the view so the hit test uses the number that was drawn rather than one
	// counted by hand. The title ends with a newline of its own and the view
	// adds another, which put every click one row below what was under it.
	bodyY int

	onDiff  bool // focus is on the diff rather than the list
	loading bool
	err     string
	seq     int
}

// newGitState builds the browser's state with nothing hovered.
//
// It exists for that one field: hover is an index, and its zero value would
// otherwise mean the first file is under a pointer that has not moved yet.
func newGitState() *gitState { return &gitState{hover: -1} }

// gitLogMsg carries a loaded history.
type gitLogMsg struct {
	seq              int
	root             string
	fs               vfs.FS
	commits          []git.Commit
	staged, unstaged int
	err              string
}

// gitDiffMsg carries one commit's changes.
type gitDiffMsg struct {
	seq   int
	sha   string
	files []git.FileChange
	diff  []git.File
	err   string
}

// openGit starts the browser for the active session's repository.
func (m *Model) openGit() tea.Cmd {
	s := m.mgr.Active()
	fsys, dir := m.sessionFS(s), m.sessionCWD(s)

	if m.git == nil {
		m.git = newGitState()
	}
	m.git.seq++
	m.git.loading, m.git.err = true, ""
	m.git.fs = fsys
	m.overlay = overlayGit

	seq := m.git.seq
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		root, ok := git.Root(ctx, fsys, dir)
		if !ok {
			return gitLogMsg{seq: seq, err: dir + " is not inside a git repository"}
		}
		commits, err := git.Log(ctx, fsys, root, gitLimit)
		if err != nil {
			return gitLogMsg{seq: seq, root: root, err: err.Error()}
		}
		staged, unstaged, _ := git.Status(ctx, fsys, root)
		return gitLogMsg{
			seq: seq, root: root, fs: fsys, commits: commits,
			staged: staged, unstaged: unstaged,
		}
	}
}

func (m *Model) applyGitLog(msg gitLogMsg) tea.Cmd {
	g := m.git
	if g == nil || msg.seq != g.seq {
		return nil
	}
	g.loading = false
	if msg.err != "" {
		g.err = msg.err
		m.overlay = overlayNone
		m.notice = msg.err
		return nil
	}
	g.root, g.commits = msg.root, msg.commits
	g.staged, g.unstaged = msg.staged, msg.unstaged
	g.sel, g.top, g.shown = 0, 0, ""
	if len(g.commits) == 0 {
		g.err = "no commits yet"
		return nil
	}
	return m.loadGitDiff()
}

// loadGitDiff fetches the selected commit's changes, unless they are already
// on screen.
func (m *Model) loadGitDiff() tea.Cmd {
	g := m.git
	if g == nil || g.sel >= len(g.commits) {
		return nil
	}
	c := g.commits[g.sel]
	if g.shown == c.SHA {
		return nil
	}
	g.seq++
	seq, fsys, root, sha := g.seq, g.fs, g.root, c.SHA

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		files, err := git.Files(ctx, fsys, root, sha)
		if err != nil {
			return gitDiffMsg{seq: seq, sha: sha, err: err.Error()}
		}
		// One patch for the whole commit rather than one per file: a commit
		// that touches forty files would otherwise be forty launches, and at a
		// quarter of a second each into a distribution that is ten seconds of
		// arrow key.
		patch, err := git.Patch(ctx, fsys, root, sha, "")
		if err != nil {
			return gitDiffMsg{seq: seq, sha: sha, files: files, err: err.Error()}
		}
		return gitDiffMsg{seq: seq, sha: sha, files: files, diff: git.ParsePatch(patch)}
	}
}

func (m *Model) applyGitDiff(msg gitDiffMsg) {
	g := m.git
	if g == nil || msg.seq != g.seq {
		return // the selection moved on while this was loading
	}
	g.shown, g.files, g.diff = msg.sha, msg.files, msg.diff
	g.scrol = 0
	g.err = msg.err
	g.head, g.body, g.marks, g.rows = nil, nil, nil, nil
}

// ---- keys ------------------------------------------------------------------

func (m *Model) gitKey(key string) tea.Cmd {
	g := m.git
	if g == nil {
		m.overlay = overlayNone
		return nil
	}
	switch key {
	case "esc", "q", "ctrl+g":
		m.overlay = overlayNone
		return nil
	case "tab", "right", "l":
		g.onDiff = true
		return nil
	case "shift+tab", "left", "h":
		g.onDiff = false
		return nil
	case "r":
		return m.openGit()
	case "alt+down":
		m.scrollFiles(1)
		return nil
	case "alt+up":
		m.scrollFiles(-1)
		return nil
	case "f":
		g.allFiles = !g.allFiles
		g.head, g.body, g.marks, g.rows = nil, nil, nil, nil
		g.scrol = 0
		return nil
	}

	if g.onDiff {
		m.scrollDiff(key)
		return nil
	}

	moved := false
	page := m.gitListCap()
	switch key {
	case "down", "j", "ctrl+n":
		g.sel, moved = min(len(g.commits)-1, g.sel+1), true
	case "up", "k", "ctrl+p":
		g.sel, moved = max(0, g.sel-1), true
	case "pgdown", " ":
		g.sel, moved = min(len(g.commits)-1, g.sel+page), true
	case "pgup":
		g.sel, moved = max(0, g.sel-page), true
	case "g", "home":
		g.sel, moved = 0, true
	case "G", "end":
		g.sel, moved = max(0, len(g.commits)-1), true
	case "enter":
		g.onDiff = true
		return nil
	}
	if !moved {
		return nil
	}
	m.showSelectedCommit()
	return m.loadGitDiff()
}

// scrollDiff moves the right pane.
func (m *Model) scrollDiff(key string) {
	g := m.git
	rows := m.diffRows()
	switch key {
	case "down", "j", "ctrl+n":
		g.scrol++
	case "up", "k", "ctrl+p":
		g.scrol--
	case "pgdown", " ":
		g.scrol += rows
	case "pgup":
		g.scrol -= rows
	case "g", "home":
		g.scrol = 0
	case "G", "end":
		g.scrol = len(g.body)
	case "]", "}", "n":
		// A commit that touches forty files is not readable a page at a time,
		// and paging is the only thing a plain scroll offers. These put the
		// next file at the top of the pane.
		g.scrol = nextMark(g.marks, g.scrol)
	case "[", "{", "N":
		g.scrol = prevMark(g.marks, g.scrol)
	}
	g.scrol = m.clampDiff(g.scrol)
}

// clampDiff stops the pane at the last line rather than letting it scroll into
// empty space. Scrolling past the end of a diff is how you lose your place in
// one: nothing moves on screen except the content leaving it.
func (m *Model) clampDiff(v int) int {
	return clamp(v, 0, max(0, len(m.git.body)-m.diffRows()))
}

func nextMark(marks []int, at int) int {
	for _, i := range marks {
		if i > at {
			return i
		}
	}
	if len(marks) == 0 {
		return at
	}
	return marks[len(marks)-1]
}

func prevMark(marks []int, at int) int {
	for i := len(marks) - 1; i >= 0; i-- {
		if marks[i] < at {
			return marks[i]
		}
	}
	return 0
}

// showSelectedCommit keeps the selection inside the commit column, with a
// commit of context either side of it where there is room.
//
// The column and the window used to be measured in different units: a commit
// occupies two rows, and the window was counted in rows, so the selection went
// on walking for half a screen after it had left the bottom of the pane.
func (m *Model) showSelectedCommit() {
	g := m.git
	cap := m.gitListCap()
	if lo := g.sel - gitMargin; lo < g.top {
		g.top = lo
	}
	if hi := g.sel + gitMargin; hi >= g.top+cap {
		g.top = hi - cap + 1
	}
	g.top = clamp(g.top, 0, max(0, len(g.commits)-cap))
}

// gitMargin is how many commits stay visible past the selection, so that moving
// down shows you where you are going rather than only where you are.
const gitMargin = 1

// gitListCap is how many commits fit the column, which is not how many rows it
// has: each one takes two.
func (m *Model) gitListCap() int { return max(1, m.gitRows()/2) }

// ---- layout ----------------------------------------------------------------

// gitWidth and gitHeight size the browser to the terminal rather than to its
// content: this is a place you read in, not a dialog you dismiss.
func (m *Model) gitWidth() int  { return clamp(m.w-4, 40, 220) }
func (m *Model) gitHeight() int { return clamp(m.h-4, 10, 60) }
func (m *Model) gitRows() int   { return max(1, m.gitHeight()-4) }

// gitListWidth is the commit column. Sublime Merge gives it about a third,
// which is enough for a subject and leaves the diff the room it needs.
func (m *Model) gitListWidth() int {
	return clamp(m.gitWidth()*36/100, 24, 56)
}

func (m *Model) gitView() string {
	g := m.git
	w, h := m.gitWidth(), m.gitHeight()
	if g == nil {
		return m.st.Overlay.Width(w).Render("  no repository")
	}

	inner := w - 2
	listW := m.gitListWidth()
	// A body row is " " + list + " " + separator + " " + diff, so the two panes
	// share what is left of the inner width after those four columns. It was
	// three for a long time, which made every row one column too wide: not
	// wide enough to notice by measuring a line — lipgloss wrapped the extra
	// column and padded both halves back to the full width — but the wrapped
	// remainder landed at the left edge of the box, on top of the commit list.
	diffW := inner - listW - 4

	var b strings.Builder
	b.WriteString(m.gitTitle(inner) + "\n")
	// Where the rows begin, for the hit test. Counted from what was written
	// rather than from the structure, which is how it drifted: the title ends
	// with a newline of its own and this adds another.
	g.bodyY = strings.Count(b.String(), "\n")

	rows := m.gitRows()
	list := m.gitListRows(listW, rows)
	diff := m.gitDiffRows(diffW, rows)

	sep := m.st.Faint.Render("│")
	for i := 0; i < rows; i++ {
		l, d := "", ""
		if i < len(list) {
			l = list[i]
		}
		if i < len(diff) {
			d = diff[i]
		}
		b.WriteString(" " + padRight(l, listW) + " " + sep + " " + padRight(d, diffW) + "\n")
	}

	b.WriteString(m.st.Faint.Render(
		"  ↑↓ move · tab pane · click a file · alt+↑↓ file list · [ ] next file · esc close"))
	_ = h
	return m.st.Overlay.Width(w).Render(b.String())
}

func (m *Model) gitTitle(inner int) string {
	g := m.git
	left := m.st.Accent.Render("  History")
	if g.root != "" {
		left += "  " + m.st.Faint.Render(vfs.Base(g.root))
	}
	if f := g.fs; f != nil && !f.IsLocal() {
		left += "  " + m.st.Faint.Render(f.Label())
	}

	var right string
	switch {
	case g.loading:
		right = m.st.Faint.Render("loading…")
	case g.err != "":
		right = m.st.Bad.Render(truncate(g.err, inner/2))
	case g.staged+g.unstaged > 0:
		right = m.st.Warn.Render(plural(g.staged+g.unstaged, "uncommitted file"))
	default:
		right = m.st.Faint.Render(plural(len(g.commits), "commit"))
	}

	gap := inner - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if gap < 1 {
		return left + "\n"
	}
	return left + strings.Repeat(" ", gap) + right + "\n"
}

// gitListRows renders the commit column.
//
// Each commit takes two lines, the way Sublime Merge lays them out: the
// subject, then who and when underneath. One line would fit more commits and
// make every one of them harder to tell apart.
func (m *Model) gitListRows(w, rows int) []string {
	g := m.git
	out := make([]string, 0, rows)

	for i := g.top; i < len(g.commits) && len(out) < rows; i++ {
		c := g.commits[i]
		selected := i == g.sel

		mark := m.st.Faint.Render("│")
		if c.Merge() {
			mark = m.st.Accent.Render("⑂")
		}
		if selected {
			mark = m.st.Accent.Render("▸")
		}

		subject := truncate(c.Subject, max(4, w-2))
		if selected {
			subject = m.st.Bold.Render(subject)
		} else {
			subject = m.st.Body.Render(subject)
		}
		out = append(out, mark+" "+subject)
		if len(out) >= rows {
			break
		}

		meta := c.Author + "  " + relTime(c.When)
		line := "  " + m.st.Faint.Render(truncate(meta, max(4, w-2)))
		if refs := m.gitRefs(c, w-lipgloss.Width(stripANSI(line))-2); refs != "" {
			line += " " + refs
		}
		out = append(out, line)
	}
	return out
}

// gitRefs renders the branch and tag labels on a commit, if they fit.
func (m *Model) gitRefs(c git.Commit, room int) string {
	if len(c.Refs) == 0 || room < 6 {
		return ""
	}
	var parts []string
	for _, r := range c.Refs {
		name := r
		style := m.st.Ref
		if rest, ok := strings.CutPrefix(r, "HEAD -> "); ok {
			name, style = rest, m.st.RefHead
		}
		if lipgloss.Width(name)+2 > room {
			break
		}
		parts = append(parts, style.Render(" "+name+" "))
		room -= lipgloss.Width(name) + 3
	}
	return strings.Join(parts, " ")
}

// gitDiffRows renders the right pane: the pinned summary, then the patch
// scrolled under it.
func (m *Model) gitDiffRows(w, rows int) []string {
	g := m.git
	// Rebuilt when the pane changes shape as well as when the commit does: the
	// lines are laid out for a width, and the summary is pinned or not
	// depending on how much height there is to pin it over.
	if g.body == nil && g.head == nil || g.builtW != w || g.builtRows != rows {
		g.head, g.body = m.buildGitPanes(w)
		g.builtW, g.builtRows = w, rows
	}
	if len(g.head)+len(g.body) == 0 {
		return nil
	}

	pin := m.pinnedRows()
	g.scrol = m.clampDiff(g.scrol)
	end := min(len(g.body), g.scrol+max(0, rows-pin))

	out := make([]string, 0, rows)
	out = append(out, g.head[:pin]...)
	return append(out, g.body[g.scrol:end]...)
}

// pinnedRows is how much of the summary stays on screen while the patch moves
// under it.
//
// It is what makes the summary a place you navigate from rather than a page
// you scroll away: click a file, land in its patch, and the list of the others
// is still there to click next. It gives up the pin when the summary is more
// than half the pane — pressing `f` on a commit that touches forty files is a
// request to read the list, not to be left with three rows of diff.
func (m *Model) pinnedRows() int {
	if m.git == nil {
		return 0
	}
	return len(m.git.head)
}

// diffRows is the room the patch itself has.
func (m *Model) diffRows() int { return max(1, m.gitRows()-m.pinnedRows()) }

// buildGitPanes renders the right pane in two halves: the summary that stays
// put, and the patch that scrolls under it. Both are flat lines, which is what
// makes scrolling a slice rather than a layout pass.
func (m *Model) buildGitPanes(w int) (head, body []string) {
	g := m.git
	head = m.buildGitSummary(w)
	if g.sel >= len(g.commits) || g.shown != g.commits[g.sel].SHA {
		return head, nil
	}
	body = m.buildGitPatch(w, head)

	// The summary gives up the pin when there would be no patch left to pin it
	// over — a short pane, or `f` on a commit that touches forty files, which
	// is a request to read the list rather than to be left with three rows of
	// diff. It goes back to scrolling with the patch, which moves every mark.
	if m.gitRows()-len(head) < patchRoom {
		for i := range g.marks {
			g.marks[i] += len(head)
		}
		return nil, append(head, body...)
	}
	return head, body
}

// patchRoom is the least the patch keeps for itself. Below it the summary is
// not pinned: a table of contents that fills the page is not a heading, it is
// the page.
const patchRoom = 8

// buildGitSummary renders the half that stays put. It is built on its own so
// that moving the pointer over it costs a dozen lines rather than a re-render
// of a patch that may be thousands.
func (m *Model) buildGitSummary(w int) []string {
	g := m.git
	if g.sel >= len(g.commits) {
		return nil
	}
	c := g.commits[g.sel]
	out := make([]string, 0, 16)

	out = append(out, m.st.Bold.Render(truncate(c.Subject, w)))
	out = append(out, m.st.Faint.Render(truncate(
		c.Short+"  "+c.Author+"  "+c.When.Format("2006-01-02 15:04"), w)))

	if g.shown != c.SHA {
		return append(out, "", m.st.Faint.Render("loading…"))
	}

	// The files come before the message, which is not how Sublime Merge orders
	// it and is the right call in a terminal: what tells you whether this is
	// the commit you were looking for is which files it touched, and a commit
	// with a properly written message would push that off the bottom of the
	// pane. The message is still here, directly underneath.
	out = append(out, "", m.gitTotals(w))

	// Only the first few files, unless asked. A commit that touches forty of
	// them would otherwise open on forty lines of names and no diff at all —
	// the summary would have pushed off the screen the thing it summarises.
	// A window onto the files rather than all of them: fifty names would be
	// fifty rows of a pane that is meant to be showing a patch. The window
	// scrolls, so the ones below are a key away rather than out of reach.
	shown, from := g.files, 0
	if !g.allFiles && len(shown) > gitFilesShown {
		from = clamp(g.fileTop, 0, len(shown)-gitFilesShown)
		g.fileTop = from
		shown = shown[from : from+gitFilesShown]
	}
	if from > 0 {
		out = append(out, m.st.Faint.Render(truncate(
			"  "+plural(from, "more file")+" above", w)))
	}
	g.rows = make(map[int]int, len(shown))
	for i, f := range shown {
		g.rows[len(out)] = from + i
		out = append(out, m.gitFileLine(f, w, from+i == g.hover))
	}
	if rest := len(g.files) - from - len(shown); rest > 0 {
		out = append(out, m.st.Faint.Render(truncate(
			"  "+plural(rest, "more file")+" below  ·  alt+down to scroll, f for all", w)))
	}

	return out
}

// buildGitPatch renders the half that scrolls: the commit message, then the
// diff. The message is here rather than in the summary because it runs to as
// many paragraphs as its author felt like, and pinning that would leave no
// pane to pin it over.
func (m *Model) buildGitPatch(w int, head []string) []string {
	g := m.git
	c := g.commits[g.sel]
	out := make([]string, 0, 64)

	if c.Body != "" {
		for _, l := range strings.Split(c.Body, "\n") {
			out = append(out, m.st.Dim.Render(truncate(l, w)))
		}
	}

	g.marks = g.marks[:0]
	for _, f := range g.diff {
		if len(out) > 0 {
			out = append(out, "")
		}
		g.marks = append(g.marks, len(out))
		out = append(out, m.st.Accent.Render(truncate(f.Path, w)))
		if f.Binary {
			out = append(out, m.st.Faint.Render("  binary file"))
			continue
		}
		for _, h := range f.Hunks {
			hdr := "@@ " + h.Header
			out = append(out, m.st.DiffHunk.Render(truncate(hdr, w)))
			for _, l := range h.Lines {
				out = append(out, m.gitDiffLine(l, w))
			}
		}
	}
	return out
}

// gitFileLine is one row of the file summary, with its counts.
// gitFilesShown is how much of the file summary opens by default. Enough to
// see the shape of a commit, few enough that the diff is still on screen under
// it; `f` shows the rest.
const gitFilesShown = 8

// gitTotals is the line a patch opens with: how much of the repository this
// commit moved, before any of the detail.
func (m *Model) gitTotals(w int) string {
	g := m.git
	added, deleted := 0, 0
	for _, f := range g.files {
		added, deleted = added+f.Added, deleted+f.Deleted
	}
	return truncate(m.st.Dim.Render(plural(len(g.files), "file")+" changed")+"  "+
		m.st.DiffAdd.Render("+"+strconv.Itoa(added))+" "+
		m.st.DiffDel.Render("-"+strconv.Itoa(deleted)), w)
}

// gitFileLine is one row of the file summary, with its counts.
//
// The counts are flushed right so they form a column. A ragged edge of numbers
// is a column you have to read; a straight one is a column you can scan, which
// is the whole job of this list.
func (m *Model) gitFileLine(f git.FileChange, w int, hover bool) string {
	counts := ""
	switch {
	case f.Binary:
		counts = m.st.Faint.Render("bin")
	default:
		counts = m.st.DiffAdd.Render("+"+strconv.Itoa(f.Added)) + " " +
			m.st.DiffDel.Render("-"+strconv.Itoa(f.Deleted))
	}
	name := f.Path
	if f.Old != "" {
		name = f.Old + " -> " + f.Path
	}
	style := m.st.Dim
	if hover {
		style = m.st.Hover
	}
	room := max(4, w-lipgloss.Width(counts)-3)
	return "  " + padRight(style.Render(truncate(name, room)), room) + " " + counts
}

// gitDiffLine draws one diff line: a gutter with both line numbers, the sign,
// and the text with the changed span picked out.
func (m *Model) gitDiffLine(l git.Line, w int) string {
	if l.Kind == git.Meta {
		return m.st.DiffMeta.Render(truncate("  "+l.Text, w))
	}

	const gutter = 9 // "1234 5678"
	num := func(n int) string {
		if n == 0 {
			return "    "
		}
		return padLeft(strconv.Itoa(n), 4)
	}

	// A changed row is tinted end to end — gutter, sign, text and the padding
	// out to the edge — so a run of additions is a band the eye can measure
	// without reading a word of it. A context line is left on the background
	// it shares with everything else, which is what makes the bands stand out.
	sign, style, on := " ", m.st.Body, m.st.Body
	gut := m.st.Gutter
	switch l.Kind {
	case git.Added:
		sign, style, on, gut = "+", m.st.DiffAddRow, m.st.DiffAddOn, m.st.DiffAddRow
	case git.Deleted:
		sign, style, on, gut = "-", m.st.DiffDelRow, m.st.DiffDelOn, m.st.DiffDelRow
	}

	room := max(4, w-gutter-2)
	line := gut.Render(num(l.Old)+" "+num(l.New)) + style.Render(" "+sign) +
		renderSpans(l.Text, l.Spans, style, on, room)
	if l.Kind == git.Context {
		return line
	}
	if n := w - lipgloss.Width(line); n > 0 {
		line += style.Render(strings.Repeat(" ", n))
	}
	return line
}

// renderSpans paints a line, brightening the parts that actually changed.
//
// Truncation happens on the raw text before any styling, because a span is a
// byte range into that text and slicing a styled string would cut escape
// sequences in half.
func renderSpans(text string, spans []git.Span, base, on lipgloss.Style, w int) string {
	raw := truncate(text, w)
	if len(spans) == 0 || len(raw) != len(text) {
		// Truncated lines lose their spans rather than risk an offset that no
		// longer points where it did.
		return base.Render(raw)
	}
	var b strings.Builder
	at := 0
	for _, s := range spans {
		if s.Start < at || s.End > len(raw) || s.Start >= s.End {
			continue
		}
		b.WriteString(base.Render(raw[at:s.Start]))
		b.WriteString(on.Render(raw[s.Start:s.End]))
		at = s.End
	}
	b.WriteString(base.Render(raw[at:]))
	return b.String()
}

func padLeft(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// relTime is how long ago, in the shortest form that still says it.
func relTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	case d < 7*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/24)) + "d ago"
	}
	return t.Format("2006-01-02")
}

// ---- mouse -----------------------------------------------------------------

// gitHit maps a screen cell onto the browser: which pane it is over and which
// of that pane's rows.
//
// The arithmetic follows the same layout gitView draws — the overlay's border,
// then the title, then the body rows, each of which opens with a space — so a
// click lands on the line that was drawn rather than near it.
func (m *Model) gitHit(x, y int) (onDiff bool, row int, ok bool) {
	if m.git == nil {
		return false, 0, false
	}
	// Past the border and whatever the view put above the rows.
	top := m.overlayY + 1 + m.git.bodyY
	rows := m.gitRows()
	if y < top || y >= top+rows {
		return false, 0, false
	}
	listL := m.overlayX + 2
	listW := m.gitListWidth()
	diffL := listL + listW + 3 // the gap, the separator, and its gap
	switch {
	case x >= diffL && x < m.overlayX+m.gitWidth()-1:
		return true, y - top, true
	case x >= listL && x < listL+listW:
		return false, y - top, true
	}
	return false, 0, false
}

// gitWheel scrolls whichever pane the pointer is over, without moving the
// selection: the wheel is for reading, and a list that re-selected as it
// scrolled would load a diff for every notch.
func (m *Model) gitWheel(x, y, dir int) {
	g := m.git
	onDiff, _, ok := m.gitHit(x, y)
	if !ok {
		return
	}
	if onDiff {
		// The pointer decides which of the two lists it is over: the summary is
		// pinned above the patch and scrolls on its own.
		if _, row, _ := m.gitHit(x, y); row < m.pinnedRows() {
			m.scrollFiles(dir / 3)
			return
		}
		g.scrol = m.clampDiff(g.scrol + dir)
		return
	}
	// A commit is two rows, so a notch of three rows is a commit and a half.
	// Rounding it to one keeps the column moving at the speed of its content.
	step := max(1, dir/3)
	if dir < 0 {
		step = min(-1, dir/3)
	}
	g.top = clamp(g.top+step, 0, max(0, len(g.commits)-m.gitListCap()))
}

// gitClick selects the commit under the pointer, or moves the keyboard to the
// diff when the pointer is in it.
func (m *Model) gitClick(x, y int) tea.Cmd {
	g := m.git
	onDiff, row, ok := m.gitHit(x, y)
	if !ok {
		return nil
	}
	if onDiff {
		g.onDiff = true
		// A click on a name in the file summary goes to that file's patch.
		// The summary is pinned above the patch, so the rows it occupies are
		// not scrolled and the ones below it are.
		line := row
		if pin := m.pinnedRows(); row >= pin {
			line = g.scrol + row - pin
		}
		if i, ok := m.fileRowAt(line); ok {
			m.showFile(i)
		}
		return nil
	}
	g.onDiff = false
	// Two rows per commit, and both of them belong to it: clicking the author
	// line is still clicking that commit.
	i := g.top + row/2
	if i < 0 || i >= len(g.commits) {
		return nil
	}
	g.sel = i
	m.showSelectedCommit()
	return m.loadGitDiff()
}

// showFile puts a file's patch at the top of the pane.
//
// The summary lists what the commit touched; until now it was a list you read
// and then went looking for, paging or stepping with [ and ] until the file
// you had already picked came round. Clicking the name goes there.
func (m *Model) showFile(i int) {
	g := m.git
	if i < 0 || i >= len(g.marks) {
		// A file with no hunks — a rename with no edits, a binary — has a row
		// in the summary and nothing in the patch to go to.
		return
	}
	g.scrol = m.clampDiff(g.marks[i])
	g.onDiff = true
}

// fileRowAt reports which file a line of the rendered pane names, if it names
// one.
func (m *Model) fileRowAt(line int) (int, bool) {
	i, ok := m.git.rows[line]
	return i, ok
}

// scrollFiles moves the window the file summary shows.
//
// The summary is pinned above the patch, so it cannot simply grow: a commit
// that touches fifty files would leave no pane to pin it over. It gets a
// window instead, and this is what moves it.
func (m *Model) scrollFiles(by int) {
	g := m.git
	if g == nil || len(g.files) <= gitFilesShown || g.allFiles {
		return
	}
	top := clamp(g.fileTop+by, 0, len(g.files)-gitFilesShown)
	if top == g.fileTop {
		return
	}
	g.fileTop = top
	// The summary is laid out at build time, so moving its window rebuilds it.
	// The patch below is untouched: scrolling the list is reading the list,
	// not choosing from it.
	g.head, g.body, g.marks, g.rows = nil, nil, nil, nil
}

// hoverFile marks the file the pointer is over, so the summary says it can be
// clicked before it is clicked.
//
// Only the summary is rebuilt: it is a dozen lines, where the patch under it
// may be thousands, and the pointer moves a great deal more often than the
// commit does.
func (m *Model) hoverFile(x, y int) {
	g := m.git
	if g == nil {
		return
	}
	want := -1
	if _, row, ok := m.gitHit(x, y); ok && row < m.pinnedRows() {
		if i, isFile := m.fileRowAt(row); isFile {
			want = i
		}
	}
	if want == g.hover {
		return
	}
	g.hover = want
	if g.builtW > 0 {
		g.head = m.buildGitSummary(g.builtW)
	}
}
