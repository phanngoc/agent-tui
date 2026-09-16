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

	// The right pane, for whichever commit is selected.
	shown string // the sha the diff belongs to
	files []git.FileChange
	diff  []git.File
	body  []string // rendered right pane, one entry per line
	scrol int

	onDiff  bool // focus is on the diff rather than the list
	loading bool
	err     string
	seq     int
}

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
		m.git = &gitState{}
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
	g.body = nil
}

// ---- keys ------------------------------------------------------------------

func (m *Model) gitKey(key string) tea.Cmd {
	g := m.git
	if g == nil {
		m.overlay = overlayNone
		return nil
	}
	page := max(1, m.gitRows()-2)

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
	}

	if g.onDiff {
		switch key {
		case "down", "j", "ctrl+n":
			g.scrol++
		case "up", "k", "ctrl+p":
			g.scrol = max(0, g.scrol-1)
		case "pgdown", " ":
			g.scrol += page
		case "pgup":
			g.scrol = max(0, g.scrol-page)
		case "g", "home":
			g.scrol = 0
		case "G", "end":
			g.scrol = max(0, len(g.body)-page)
		}
		g.scrol = clamp(g.scrol, 0, max(0, len(g.body)-1))
		return nil
	}

	moved := false
	switch key {
	case "down", "j", "ctrl+n":
		g.sel, moved = min(len(g.commits)-1, g.sel+1), true
	case "up", "k", "ctrl+p":
		g.sel, moved = max(0, g.sel-1), true
	case "pgdown":
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
	// Keep the selection inside the window.
	rows := m.gitRows()
	if g.sel < g.top {
		g.top = g.sel
	}
	if g.sel >= g.top+rows {
		g.top = g.sel - rows + 1
	}
	return m.loadGitDiff()
}

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
	diffW := inner - listW - 3 // the separator column and its padding

	var b strings.Builder
	b.WriteString(m.gitTitle(inner) + "\n")

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
		"  ↑↓ commit · tab diff · pgup/pgdn scroll · r reload · esc close"))
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

// gitDiffRows renders the right pane, building it once per selection.
func (m *Model) gitDiffRows(w, rows int) []string {
	g := m.git
	if g.body == nil {
		g.body = m.buildGitBody(w)
	}
	if len(g.body) == 0 {
		return nil
	}
	g.scrol = clamp(g.scrol, 0, max(0, len(g.body)-1))
	end := min(len(g.body), g.scrol+rows)
	return g.body[g.scrol:end]
}

// buildGitBody renders the commit message, the file summary and the diff into
// flat lines, which is what makes scrolling a slice rather than a layout pass.
func (m *Model) buildGitBody(w int) []string {
	g := m.git
	if g.sel >= len(g.commits) {
		return nil
	}
	c := g.commits[g.sel]
	out := make([]string, 0, 64)

	out = append(out, m.st.Bold.Render(truncate(c.Subject, w)))
	out = append(out, m.st.Faint.Render(truncate(
		c.Short+"  "+c.Author+"  "+c.When.Format("2006-01-02 15:04"), w)))

	if g.shown != c.SHA {
		out = append(out, "", m.st.Faint.Render("loading…"))
		return out
	}

	// The files come before the message, which is not how Sublime Merge orders
	// it and is the right call in a terminal: what tells you whether this is
	// the commit you were looking for is which files it touched, and a commit
	// with a properly written message would push that off the bottom of the
	// pane. The message is still here, directly underneath.
	out = append(out, "")
	for _, f := range g.files {
		out = append(out, m.gitFileLine(f, w))
	}

	if c.Body != "" {
		out = append(out, "")
		for _, l := range strings.Split(c.Body, "\n") {
			out = append(out, m.st.Dim.Render(truncate(l, w)))
		}
	}

	for _, f := range g.diff {
		out = append(out, "", m.st.Accent.Render(truncate(f.Path, w)))
		if f.Binary {
			out = append(out, m.st.Faint.Render("  binary file"))
			continue
		}
		for _, h := range f.Hunks {
			head := "@@ " + h.Header
			out = append(out, m.st.DiffHunk.Render(truncate(head, w)))
			for _, l := range h.Lines {
				out = append(out, m.gitDiffLine(l, w))
			}
		}
	}
	return out
}

// gitFileLine is one row of the file summary, with its counts.
func (m *Model) gitFileLine(f git.FileChange, w int) string {
	counts := ""
	switch {
	case f.Binary:
		counts = m.st.Faint.Render("bin")
	default:
		counts = m.st.DiffDel.Render("-"+strconv.Itoa(f.Deleted)) + " " +
			m.st.DiffAdd.Render("+"+strconv.Itoa(f.Added))
	}
	name := f.Path
	if f.Old != "" {
		name = f.Old + " → " + f.Path
	}
	room := w - lipgloss.Width(stripANSI(counts)) - 3
	return "  " + m.st.Dim.Render(truncate(name, max(4, room))) + "  " + counts
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
	g := m.st.Gutter.Render(num(l.Old) + " " + num(l.New))

	sign, style, on := " ", m.st.Body, m.st.Body
	switch l.Kind {
	case git.Added:
		sign, style, on = "+", m.st.DiffAdd, m.st.DiffAddOn
	case git.Deleted:
		sign, style, on = "-", m.st.DiffDel, m.st.DiffDelOn
	}

	room := max(4, w-gutter-2)
	text := renderSpans(l.Text, l.Spans, style, on, room)
	return g + " " + style.Render(sign) + text
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
