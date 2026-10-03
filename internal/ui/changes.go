package ui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/git"
)

// What the agent changed, where nothing else is being shown.
//
// The preview with no file open said "No file open." and offered two keys.
// That is the pane beside a conversation in which an agent is editing the
// project, and the question the reader has is what it has changed. So, when
// no file is open, the pane shows the working tree's changes against the last
// commit — the way Claude Code does beside its own conversation:
//
//	7 files changed +85 -4
//
//	  internal/agent/mode.go               +21 -0
//	  internal/ui/queue.go                    new
//
//	internal/agent/mode.go
//	  90   90  // let a shell command run, while …
//	       93 + // The boundary is for changes, …
//
// ↑↓ picks a file, the wheel or PgUp/PgDn reads its diff, enter opens it at its
// first change, r reads the tree again, and q on an open file comes back here.
// It refreshes itself when a tool call or a turn ends, and when the session
// moves; the git work happens off the update loop, and none of it at all while
// a file is open or the preview is closed.
//
// The rows are drawn by the history browser's own line renderers, so a diff
// looks the same wherever it is shown.

type changesState struct {
	root    string // the repository the listing is of
	files   []git.FileChange
	sel     int
	listTop int
	loaded  bool // a listing has arrived (for root)
	err     string
	seq     int
	at      time.Time

	patchPath string
	patch     []git.File
	scroll    int

	// Where the view drew things, for clicks: the content row the file list
	// starts on, and how many file rows it drew.
	listY, listN int
}

type changesMsg struct {
	seq   int
	root  string
	files []git.FileChange
	err   error
}

type changesPatchMsg struct {
	seq   int
	path  string
	patch []git.File
	err   error
}

// changesWanted reports whether the pane is showing, or would show, the
// changes: preview open, no file in it, nothing being edited.
func (m *Model) changesWanted() bool {
	return m.prevW > 0 && m.file == nil && m.edit == nil
}

// refreshChanges reads the working tree again, if the listing is on screen and
// was last read at least minAge ago. It is cheap to call from anywhere for that
// reason.
func (m *Model) refreshChanges(minAge time.Duration) tea.Cmd {
	if !m.changesWanted() {
		return nil
	}
	c := &m.changes
	if time.Since(c.at) < minAge {
		return nil
	}
	c.at = time.Now()
	c.seq++
	seq := c.seq
	s := m.mgr.Active()
	fsys, dir := m.sessionFS(s), m.sessionCWD(s)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		root, ok := git.Root(ctx, fsys, dir)
		if !ok {
			return changesMsg{seq: seq}
		}
		files, err := git.WorkingFiles(ctx, fsys, root)
		return changesMsg{seq: seq, root: root, files: files, err: err}
	}
}

func (m *Model) applyChanges(msg changesMsg) tea.Cmd {
	c := &m.changes
	if msg.seq != c.seq {
		return nil
	}
	keep := ""
	if c.sel < len(c.files) {
		keep = c.files[c.sel].Path
	}
	c.root, c.files, c.loaded, c.err = msg.root, msg.files, true, ""
	if msg.err != nil {
		c.err = msg.err.Error()
	}
	// The same file stays selected when the list changes around it.
	c.sel = 0
	for i, f := range c.files {
		if f.Path == keep {
			c.sel = i
		}
	}
	return m.loadChangesPatch(true)
}

// loadChangesPatch reads the selected file's diff. Unless forced, a file
// whose diff is already shown is left alone, so moving back to it keeps its
// scroll.
func (m *Model) loadChangesPatch(force bool) tea.Cmd {
	c := &m.changes
	if c.sel >= len(c.files) {
		c.patch, c.patchPath = nil, ""
		return nil
	}
	f := c.files[c.sel]
	if !force && f.Path == c.patchPath {
		return nil
	}
	if f.Path != c.patchPath {
		c.scroll = 0
	}
	c.seq++
	seq := c.seq
	fsys, root := m.sessionFS(m.mgr.Active()), c.root
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := git.WorkingPatch(ctx, fsys, root, f)
		return changesPatchMsg{seq: seq, path: f.Path, patch: git.ParsePatch(out), err: err}
	}
}

func (m *Model) applyChangesPatch(msg changesPatchMsg) {
	c := &m.changes
	if msg.seq != c.seq {
		return
	}
	c.patchPath, c.patch = msg.path, msg.patch
	if msg.err != nil {
		c.err = msg.err.Error()
	}
}

// showingChanges reports whether the pane is drawing the changes now.
func (m *Model) showingChanges() bool {
	return m.changesWanted() && m.changes.loaded && m.changes.root != "" && len(m.changes.files) > 0
}

// changesKey handles the keys the listing takes. ok is false for the rest.
func (m *Model) changesKey(key string) (tea.Cmd, bool) {
	c := &m.changes
	switch key {
	case "up", "k":
		if c.sel > 0 {
			c.sel--
		}
		return m.loadChangesPatch(false), true
	case "down", "j":
		if c.sel < len(c.files)-1 {
			c.sel++
		}
		return m.loadChangesPatch(false), true
	case "pgdown", "space", "ctrl+d":
		c.scroll += max(1, m.prev.Height()/2)
		return nil, true
	case "pgup", "ctrl+u":
		c.scroll = max(0, c.scroll-max(1, m.prev.Height()/2))
		return nil, true
	case "r":
		return m.refreshChanges(0), true
	case "enter":
		return m.openChangedFile(), true
	}
	return nil, false
}

// openChangedFile opens the selected file in the preview, at its first
// change.
func (m *Model) openChangedFile() tea.Cmd {
	c := &m.changes
	if c.sel >= len(c.files) {
		return nil
	}
	f := c.files[c.sel]
	if f.Binary {
		m.notice = f.Path + " is a binary file"
		return nil
	}
	line := 1
	if f.Path == c.patchPath {
	first:
		for _, pf := range c.patch {
			for _, h := range pf.Hunks {
				for _, l := range h.Lines {
					if l.Kind == git.Added && l.New > 0 {
						line = l.New
						break first
					}
				}
			}
		}
	}
	abs := c.root + "/" + f.Path
	m.notice = "q comes back to the changes"
	return m.loadFileAt(abs, f.Path, line, false)
}

// scrollChanges moves the diff, for the wheel.
func (m *Model) scrollChanges(by int) {
	m.changes.scroll = max(0, m.changes.scroll+by)
}

// changesClick selects the file row under a click in the pane.
func (m *Model) changesClick(y int) bool {
	_, top, _, _, ok := m.paneBox(focusPreview)
	if !ok {
		return false
	}
	row := y - top - m.changes.listY
	if row < 0 || row >= m.changes.listN {
		return false
	}
	m.changes.sel = m.changes.listTop + row
	return true
}

// changesView draws the listing and the selected file's diff into a pane of
// w columns and h rows.
func (m *Model) changesView(w, h int) string {
	c := &m.changes
	added, deleted, fresh := 0, 0, 0
	for _, f := range c.files {
		added, deleted = added+f.Added, deleted+f.Deleted
		if f.Untracked {
			fresh++
		}
	}
	head := m.st.Bold.Render(plural(len(c.files), "file")+" changed") + "  " +
		m.st.DiffAdd.Render("+"+strconv.Itoa(added)) + " " +
		m.st.DiffDel.Render("-"+strconv.Itoa(deleted))
	if fresh > 0 {
		head += m.st.Faint.Render("  · " + strconv.Itoa(fresh) + " new")
	}
	out := []string{truncate(head, w), ""}

	// The list takes up to a third of the pane, scrolled to keep the
	// selection in it; the diff gets the rest.
	rows := min(len(c.files), max(3, h/3))
	if c.sel < c.listTop {
		c.listTop = c.sel
	}
	if c.sel >= c.listTop+rows {
		c.listTop = c.sel - rows + 1
	}
	c.listTop = clamp(c.listTop, 0, max(0, len(c.files)-rows))
	c.listY, c.listN = len(out), 0
	for i := c.listTop; i < len(c.files) && i < c.listTop+rows; i++ {
		f := c.files[i]
		var line string
		if f.Untracked {
			counts := m.st.DiffAdd.Render("new")
			room := max(4, w-lipgloss.Width(counts)-3)
			line = "  " + padRight(m.st.Dim.Render(truncate(f.Path, room)), room) + " " + counts
		} else {
			line = m.gitFileLine(f, w, false)
		}
		if i == c.sel {
			line = m.st.SelRow.Render(padRight(stripANSI(line), w))
		}
		out = append(out, line)
		c.listN++
	}
	if len(c.files) > rows {
		out = append(out, m.st.Faint.Render(truncate("  "+strconv.Itoa(len(c.files)-rows)+" more · ↑↓ to reach them", w)))
	}

	out = append(out, m.st.Faint.Render(strings.Repeat("─", w)))
	if c.sel < len(c.files) {
		out = append(out, m.st.Accent.Render(truncate(c.files[c.sel].Path, w)))
	}

	var diff []string
	switch {
	case c.sel >= len(c.files):
	case c.patchPath != c.files[c.sel].Path:
		diff = []string{m.st.Faint.Render("  reading the diff…")}
	case len(c.patch) == 0 || c.files[c.sel].Binary:
		diff = []string{m.st.Faint.Render("  no text to show (binary, or only a mode change)")}
	default:
		for _, pf := range c.patch {
			for _, hk := range pf.Hunks {
				diff = append(diff, m.st.DiffHunk.Render(truncate("@@ "+hk.Header, w)))
				for _, l := range hk.Lines {
					diff = append(diff, m.gitDiffLine(l, w))
				}
			}
		}
	}
	room := max(1, h-len(out))
	c.scroll = clamp(c.scroll, 0, max(0, len(diff)-room))
	end := min(len(diff), c.scroll+room)
	out = append(out, diff[c.scroll:end]...)
	return strings.Join(out, "\n")
}
