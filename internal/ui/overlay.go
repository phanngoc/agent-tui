package ui

import (
	"context"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/fsx"
	"github.com/phanngoc/agent-tui/internal/search"
	"github.com/phanngoc/agent-tui/internal/task"
	"github.com/phanngoc/agent-tui/internal/theme"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// composeCompletion floats the candidate list immediately above the prompt,
// without reflowing anything behind it.
func (m *Model) composeCompletion(base string) string {
	body := m.completionView()
	if body == "" {
		return base
	}
	h := lipgloss.Height(body)

	const inputBox, statusBar = 5, 1
	y := m.h - statusBar - inputBox - h
	if y < 1 {
		y = 1
	}
	x := max(0, (m.w-lipgloss.Width(body))/2)

	canvas := lipgloss.NewCanvas(m.w, m.h)
	canvas.Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(base).X(0).Y(0).Z(0),
		lipgloss.NewLayer(body).X(x).Y(y).Z(1),
	))
	return canvas.Render()
}

// Where an overlay's text input sits inside its box: past the left border and
// the two-space indent, and two rows down past the border and the title.
const (
	overlayTextX  = 1 + 2
	overlayInputY = 1 + 1
)

// composeOverlay floats the active overlay above the base layout.
func (m *Model) composeOverlay(base string) string {
	var body string
	switch m.overlay {
	case overlayFinder:
		body = m.finderView()
	case overlayGrep:
		body = m.grepView()
	case overlayApproval:
		body = m.approvalView()
	case overlayChoice:
		body = m.choiceView()
	case overlayTasks:
		body = m.tasksView()
	case overlayHelp:
		body = m.helpView()
	case overlayEngine:
		body = m.engineView()
	case overlayModel:
		body = m.modelView()
	case overlayGit:
		body = m.gitView()
	case overlayRename:
		body = m.renameView()
	case overlayTarget:
		body = m.targetView()
	default:
		return base
	}

	w := lipgloss.Width(body)
	h := lipgloss.Height(body)
	x := max(0, (m.w-w)/2)
	y := max(0, (m.h-h)/3)
	// Remembered so the real cursor can be placed on the overlay's own input.
	m.overlayX, m.overlayY = x, y

	canvas := lipgloss.NewCanvas(m.w, m.h)
	canvas.Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(base).X(0).Y(0).Z(0),
		lipgloss.NewLayer(body).X(x).Y(y).Z(1),
	))
	return canvas.Render()
}

// ---- fuzzy file finder -----------------------------------------------------

func (m *Model) refreshFinder() {
	m.finderHit = m.idx.Find(m.finderIn.Value(), m.finderRows())
	m.finderSel = 0
}

func (m *Model) finderRows() int { return clamp(m.h/2, 6, 18) }

func (m *Model) finderKey(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "enter":
		if m.finderSel < len(m.finderHit) {
			path := m.finderHit[m.finderSel].Path
			m.closeOverlay()
			m.showPreview = true
			m.resize(m.w, m.h)
			return m.loadFile(path, 1, true)
		}
		return nil
	case "down", "ctrl+n":
		m.finderSel = min(len(m.finderHit)-1, m.finderSel+1)
		return nil
	case "up", "ctrl+p":
		m.finderSel = max(0, m.finderSel-1)
		return nil
	}
	var cmd tea.Cmd
	before := m.finderIn.Value()
	m.finderIn, cmd = m.finderIn.Update(k)
	if m.finderIn.Value() != before {
		m.refreshFinder()
	}
	return cmd
}

func (m *Model) finderView() string {
	w := m.overlayWidth()
	inner := w - 2 // Lip Gloss v2 counts the border inside Width
	var b strings.Builder

	b.WriteString(m.st.Accent.Render("  Go to file") + "  " +
		m.st.Faint.Render(strconv.Itoa(m.idx.Len())+" indexed"))
	b.WriteString("\n  " + m.finderIn.View() + "\n\n")

	if len(m.finderHit) == 0 {
		b.WriteString(m.st.Faint.Render("  no match"))
	}
	for i, hit := range m.finderHit {
		path := highlightPath(m.st, hit, inner-4)
		row := "  " + path
		if i == m.finderSel {
			row = m.st.SelRow.Render(padRight(" ▸ "+stripANSI(path), inner))
		}
		b.WriteString(row + "\n")
	}
	b.WriteString("\n  " + m.st.Faint.Render("enter open · ↑↓ move · esc cancel"))
	return m.st.Overlay.Width(w).Render(b.String())
}

// highlightPath bolds the characters the fuzzy matcher actually matched.
func highlightPath(st *theme.Styles, hit fsx.Hit, w int) string {
	p := hit.Path
	if len(hit.Indexes) == 0 {
		return st.Dim.Render(truncate(p, w))
	}
	inSet := make(map[int]bool, len(hit.Indexes))
	for _, i := range hit.Indexes {
		inSet[i] = true
	}
	var b strings.Builder
	for i, r := range p {
		if inSet[i] {
			b.WriteString(st.MatchChar.Render(string(r)))
		} else {
			b.WriteString(st.Dim.Render(string(r)))
		}
	}
	return b.String()
}

// ---- project-wide content search ------------------------------------------

func (m *Model) grepKey(k tea.KeyPressMsg) tea.Cmd {
	// Only keys that cannot be typed are commands here: this overlay owns a
	// text box, and binding a bare letter would swallow it out of the query.
	switch k.String() {
	case "enter", "right":
		if hit, ok := m.selectedHit(); ok {
			m.closeOverlay()
			m.showPreview = true
			m.resize(m.w, m.h)
			return m.loadFile(hit.Path, hit.Line, true)
		}
		// On a file header, enter folds it.
		return m.foldSelected()
	case "left":
		return m.foldSelected()
	case "down", "ctrl+n":
		m.grepSel = min(len(m.grepRows)-1, m.grepSel+1)
		return nil
	case "up", "ctrl+p":
		m.grepSel = max(0, m.grepSel-1)
		return nil
	case "alt+a":
		// Exact case, the way a search panel's Aa button works.
		m.grepCase = !m.grepCase
		return m.rerunGrep()
	case "alt+r":
		m.grepRegex = !m.grepRegex
		return m.rerunGrep()
	}

	var cmd tea.Cmd
	before := m.grepIn.Value()
	m.grepIn, cmd = m.grepIn.Update(k)
	q := strings.TrimSpace(m.grepIn.Value())
	if q != before {
		if len(q) < 2 {
			m.setGrepResult(search.Result{})
			m.grepBusy = false
			return cmd
		}
		m.grepBusy = true
		return tea.Batch(cmd, m.runGrep(q))
	}
	return cmd
}

// foldSelected collapses or expands the file the cursor is inside.
func (m *Model) foldSelected() tea.Cmd {
	if m.grepSel < 0 || m.grepSel >= len(m.grepRows) {
		return nil
	}
	i := m.grepRows[m.grepSel].file
	m.grepFiles[i].collapsed = !m.grepFiles[i].collapsed
	// Put the cursor on the header so folding twice is symmetric.
	for r := range m.grepRows {
		if m.grepRows[r].file == i && m.grepRows[r].hit < 0 {
			m.grepSel = r
			break
		}
	}
	m.rebuildGrepRows()
	return nil
}

// rerunGrep repeats the current query after a toggle changed its meaning.
func (m *Model) rerunGrep() tea.Cmd {
	q := strings.TrimSpace(m.grepIn.Value())
	if len(q) < 2 {
		return nil
	}
	m.grepBusy = true
	return m.runGrep(q)
}

// setGrepResult installs a result and regroups it.
func (m *Model) setGrepResult(res search.Result) {
	m.grepRes = res
	m.grepFiles = groupMatches(res.Matches)
	m.grepSel, m.grepTop = 0, 0
	m.rebuildGrepRows()
}

func (m *Model) grepView() string {
	w := m.overlayWidth()
	rows := clamp(m.h/2, 8, 22)

	var b strings.Builder
	b.WriteString(m.st.Accent.Render("  Search") + "  " + m.grepToggles() + "\n")
	b.WriteString("  " + m.grepIn.View() + "\n")
	b.WriteString("  " + m.st.Faint.Render(m.grepSummary()) + "\n\n")

	if m.grepRes.Err != nil {
		b.WriteString("  " + m.st.Bad.Render(m.grepRes.Err.Error()) + "\n")
	}
	if tree := m.grepTreeView(w, rows); tree != "" {
		b.WriteString(tree + "\n")
	}

	b.WriteString("\n  " + m.st.Faint.Render(
		"enter open · ←→ fold · alt+a exact case · alt+r regex · esc close"))
	return m.st.Overlay.Width(w).Render(b.String())
}

// grepToggles shows which switches are on, the way a search panel does.
func (m *Model) grepToggles() string {
	on := func(label string, active bool) string {
		if active {
			return m.st.StatusKey.Render(" " + label + " ")
		}
		return m.st.Faint.Render(" " + label + " ")
	}
	return on("Aa", m.grepCase) + " " + on(".*", m.grepRegex)
}

// grepSummary is the count line: how much was found, and where.
func (m *Model) grepSummary() string {
	switch {
	case m.grepBusy:
		return "searching…"
	case strings.TrimSpace(m.grepIn.Value()) == "":
		return "type at least two characters"
	case len(m.grepRes.Matches) == 0:
		return "no results"
	}
	s := plural(len(m.grepRes.Matches), "result") + " in " + plural(len(m.grepFiles), "file")
	if m.grepRes.Truncated {
		s += " (capped)"
	}
	return s
}

// ---- tool approval ---------------------------------------------------------

func (m *Model) approvalView() string {
	w := clamp(m.w*3/4, 40, 100)
	if len(m.approvals) == 0 {
		return ""
	}
	call := m.approvals[0].ev.Call

	var b strings.Builder
	head := "  Allow this tool call?"
	if n := len(m.approvals) - 1; n > 0 {
		head += "   " + m.st.Faint.Render("("+strconv.Itoa(n)+" more waiting)")
	}
	b.WriteString(m.st.Warn.Render(head) + "\n")
	if reason := m.approvals[0].ev.Reason; reason != "" {
		b.WriteString("  " + m.st.Faint.Render(truncate(reason, w-6)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString("  " + m.st.ToolTag.Render(call.Name) + "\n")

	for _, line := range prettyInput(call.Input, w-6) {
		b.WriteString("  " + m.st.Dim.Render(line) + "\n")
	}
	b.WriteString("\n  " +
		m.st.Good.Render("[y] allow") + "   " +
		m.st.Bad.Render("[n] deny") + "   " +
		m.st.Dim.Render("[a] allow all this run") + "   " +
		m.st.Faint.Render("[ctrl+c] stop"))
	return m.st.Overlay.Width(w).Render(b.String())
}

// ---- help ------------------------------------------------------------------

type binding struct{ key, desc string }

// keyColumn is the display width reserved for key names in the help overlay.
const keyColumn = 14

var helpGroups = []struct {
	title string
	keys  []binding
}{
	{"Prompt", []binding{
		{"enter", "send message"},
		{"tab", "complete a path, then cycle the candidates"},
		{"@path", "point at a file; the menu opens as you type"},
		{"ctrl+v", "attach the image on the clipboard  ·  /paste does the same"},
		{"ctrl+u", "clear the prompt and anything attached to it"},
		{"shift+tab", "cycle mode: plan → ask → auto → full"},
		{"↑  ↓", "recall earlier prompts"},
		{"!<cmd>", "run a command where this session works; the agent sees it"},
		{"!wsl", "move this session into WSL  ·  !exit comes back"},
		{"cd <dir>", "move this session to another directory"},
		{"alt+enter", "newline"},
	}},
	{"Session", []binding{
		{"esc", "drop a selection  ·  or close what is open, stop the agent, go back"},
		{"ctrl+c", "copy a selection  ·  or stop the agent, or quit when idle"},
		{"drag", "select text in a pane  ·  releasing copies it"},
		{"double-click", "select the word under the pointer"},
		{"ctrl+t", "new session"},
		{"alt+t", "fork this session — same history, separate branch"},
		{"/btw", "ask beside this one, in a pane, without interrupting it"},
		{"ctrl+r", "choose the engine (built-in, claude, codex, opencode)"},
		{"/model", "choose the model this session runs on"},
		{"/git", "browse the history: ↑↓ commit · tab pane · alt+↑↓ file list"},
		{"/rename", "name this session yourself"},
		{"ctrl+d", "work on the host, in a container, or in WSL"},
		{"ctrl+k", "background commands, and their output"},
		{"ctrl+w", "close session"},
		{"alt+1…9", "jump to session"},
		{"alt+↑/↓", "previous / next session"},
	}},
	{"Find", []binding{
		{"ctrl+p", "fuzzy-find a file"},
		{"ctrl+f", "search file contents (or find in file)"},
		{"/", "find in the previewed file"},
		{"n / N", "next / previous hit"},
	}},
	{"Editing", []binding{
		{"e", "edit the previewed file"},
		{"ctrl+s", "save"},
		{"ctrl+z", "undo  ·  ctrl+y redo"},
		{"esc", "close; again to discard unsaved changes"},
	}},
	{"Session list", []binding{
		{"↑  ↓", "move the cursor without switching"},
		{"enter", "switch to the one under the cursor"},
		{"e", "give it a name"},
		{"n / f", "start one / fork one"},
		{"d", "close it"},
	}},
	{"Files", []binding{
		{"↑  ↓", "browse; the file under the cursor is shown as you move"},
		{"click", "open a file, fold a directory, or go up"},
		{"wheel", "scroll whichever pane the pointer is over"},
		{"enter", "open a file, or fold a directory"},
		{"←  →", "fold / unfold"},
		{"-", "go up one directory (or pick the ↰ row)"},
		{"r", "run this session's agent in the selected directory"},
		{"R", "back to the project root"},
		{"cd <dir>", "same thing, typed into the prompt"},
	}},
	{"Session list", []binding{
		{"n", "new session"},
		{"f", "fork the highlighted session"},
		{"enter", "switch to it"},
		{"d", "close it"},
	}},
	{"Layout", []binding{
		{"ctrl+o", "cycle panes (tab belongs to the prompt)"},
		{"click", "focus any pane, including the prompt"},
		{"ctrl+b", "toggle the session sidebar  ·  or click its switch up top"},
		{"ctrl+e", "toggle the preview pane  ·  or click its switch up top"},
		{"alt+o", "show every tool call a turn made, not just its last few"},
		{"alt+← →", "resize: the arrow pushes the nearest divider that way"},
		{"drag", "or take hold of a divider with the mouse"},
		{"w", "toggle soft wrap in the preview"},
		{"←  →", "scroll a long line sideways  ·  0 back to column one"},
		{"shift+wheel", "the same with the mouse"},
		{"g / G", "top / bottom of the preview"},
	}},
}

func (m *Model) helpView() string {
	w := clamp(m.w*3/4, 46, 96)
	var b strings.Builder
	b.WriteString(m.st.Accent.Render("  Keyboard shortcuts") + "\n")
	for _, g := range helpGroups {
		b.WriteString("\n  " + m.st.Bold.Render(g.title) + "\n")
		for _, k := range g.keys {
			// Key names contain multi-byte glyphs ("alt+1…9", "alt+↑/↓"), so the
			// column has to be padded by display width, not byte length.
			pad := max(1, keyColumn-lipgloss.Width(k.key))
			b.WriteString("    " + m.st.StatusKey.Render(" "+k.key+" ") +
				strings.Repeat(" ", pad) + m.st.Dim.Render(k.desc) + "\n")
		}
	}
	b.WriteString("\n  " + m.st.Bold.Render("Commands") +
		m.st.Faint.Render("  — type them in the prompt; tab completes them") + "\n")
	for _, c := range slashCmds {
		name := "/" + c.name
		if c.arg != "" {
			name += " " + c.arg
		}
		pad := max(1, keyColumn+4-lipgloss.Width(name))
		b.WriteString("    " + m.st.Accent.Render(name) +
			strings.Repeat(" ", pad) + m.st.Dim.Render(c.desc) + "\n")
	}

	b.WriteString("\n  " + m.st.Faint.Render("any key to close"))
	return m.st.Overlay.Width(w).Render(b.String())
}

// ---- engine picker ---------------------------------------------------------

func (m *Model) engineKey(key string) tea.Cmd {
	engines := m.reg.All()
	switch key {
	case "down", "ctrl+n", "j":
		m.engineSel = min(len(engines)-1, m.engineSel+1)
	case "up", "ctrl+p", "k":
		m.engineSel = max(0, m.engineSel-1)
	case "enter":
		if m.engineSel < len(engines) {
			e := engines[m.engineSel]
			if !e.Available() {
				m.notice = e.Label() + " is " + e.Detail()
				return nil
			}
			s := m.mgr.Active()
			if s.Engine != e.ID() {
				// A conversation cannot be handed from one agent to another
				// mid-flight: each keeps its own server-side history.
				s.Engine = e.ID()
				s.ExternalID = ""
				s.Live = nil
				if len(s.Messages) > 0 {
					m.notice = "switched to " + e.Label() + "; it starts from a fresh context"
				}
			}
			m.lastEngine = e.ID()
			m.mgr.Save(s)
			m.overlay = overlayNone
		}
	}
	return nil
}

func (m *Model) engineView() string {
	w := clamp(m.w*3/5, 44, 84)
	inner := w - 2

	var b strings.Builder
	b.WriteString(m.st.Accent.Render("  Engine for this session") + "\n\n")

	for i, e := range m.reg.All() {
		mark, name := "  ", m.st.Dim.Render(e.Label())
		switch {
		case !e.Available():
			name = m.st.Faint.Render(e.Label())
		case e.ID() == m.mgr.Active().Engine:
			mark, name = m.st.Good.Render(" ✓"), m.st.Bold.Render(e.Label())
		}
		row := mark + " " + name + "  " + m.st.Faint.Render(truncate(e.Detail(), inner-20))
		if i == m.engineSel {
			row = m.st.SelRow.Render(padRight(" ▸ "+stripANSI(e.Label()+"  "+e.Detail()), inner))
		}
		b.WriteString(row + "\n")
	}

	b.WriteString("\n  " + m.st.Faint.Render("enter select · ↑↓ move · esc cancel"))
	return m.st.Overlay.Width(w).Render(b.String())
}

// ---- filesystem target picker ----------------------------------------------

// refreshTargets lists the host, every running container, and every registered
// WSL distribution. Enumerating both shells out, so it happens when the picker
// opens rather than on every frame.
//
// A distribution is listed without being started and without its home being
// probed, because either would mean booting every registered distribution just
// to draw a menu. Its row therefore carries no working directory; choosing it
// goes through enterWSL, which resolves one once there is a reason to.
func (m *Model) refreshTargets() {
	list := []target{{
		id: "host", label: "host", fs: m.hostFS,
		detail: m.hostFS.DefaultDir(), workdir: m.hostFS.DefaultDir(),
	}}
	for _, c := range vfs.Containers(context.Background()) {
		fs := vfs.NewDocker(c.Name, c.Image, c.Workdir)
		m.fsCache[fs.ID()] = fs
		list = append(list, target{
			id: fs.ID(), label: c.Name, fs: fs,
			detail: c.Image + "  " + c.Workdir, workdir: c.Workdir,
		})
	}
	for _, d := range vfs.Distros(context.Background()) {
		detail := "wsl  " + strings.ToLower(d.State)
		if d.Default {
			detail += "  (default)"
		}
		list = append(list, target{
			id: "wsl:" + d.Name, label: d.Name, detail: detail, distro: d.Name,
		})
	}
	m.targets = list

	m.targetSel = 0
	want := m.mgr.Active().Target
	if want == "" {
		want = "host"
	}
	for i, t := range list {
		if t.id == want {
			m.targetSel = i
		}
	}
}

func (m *Model) targetKey(key string) tea.Cmd {
	switch key {
	case "down", "ctrl+n", "j":
		m.targetSel = min(len(m.targets)-1, m.targetSel+1)
		return nil
	case "up", "ctrl+p", "k":
		m.targetSel = max(0, m.targetSel-1)
		return nil
	case "r":
		m.refreshTargets()
		return nil
	case "enter":
		if m.targetSel >= len(m.targets) {
			return nil
		}
		return m.chooseTarget(m.targets[m.targetSel])
	}
	return nil
}

// chooseTarget switches to a picked row, starting a WSL distribution first
// when that is what was picked.
func (m *Model) chooseTarget(t target) tea.Cmd {
	if t.fs == nil {
		m.overlay = overlayNone
		return m.enterWSL(t.distro)
	}
	return m.useTarget(t)
}

// useTarget repoints the active session at a filesystem: the tree, the preview,
// the fuzzy index, search and the agent all follow it.
func (m *Model) useTarget(t target) tea.Cmd {
	if err := t.fs.Health(context.Background()); err != nil {
		m.notice = err.Error()
		return nil
	}

	// Remember the resolved filesystem, so coming back to this session later
	// reuses it instead of re-resolving and falling back to the host.
	m.fsCache[t.id] = t.fs

	s := m.mgr.Active()
	s.Target, s.CWD = t.id, t.workdir
	// A conversation cannot carry over to a different filesystem: the paths it
	// has been talking about do not mean the same thing there.
	s.ExternalID, s.Live = "", nil
	m.mgr.Save(s)

	m.overlay = overlayNone
	m.file = nil
	m.prev.SetContent("")
	m.tree.SetFS(t.fs, t.workdir)
	m.treeSel, m.treeTop = 0, 0
	m.idx.Retarget(t.fs, t.workdir)
	m.grepRes = search.Result{}
	m.status = "indexing " + t.label + "…"
	m.notice = "session now works in " + t.label
	// Changing filesystem is the largest move there is — it resets the
	// engine's own conversation two lines above — so the transcript says so.
	m.logMove(moveCommand(t), "now working in "+t.label+" at "+t.workdir, false)

	return m.buildIndex()
}

func (m *Model) targetView() string {
	w := clamp(m.w*3/5, 46, 92)
	inner := w - 2

	var b strings.Builder
	b.WriteString(m.st.Accent.Render("  Where this session works") + "\n\n")

	if len(m.targets) == 1 {
		b.WriteString("  " + m.st.Faint.Render("no running containers found") + "\n")
	}
	active := m.mgr.Active().Target
	if active == "" {
		active = "host"
	}
	for i, t := range m.targets {
		mark, name := "  ", m.st.Dim.Render(t.label)
		if t.id == active {
			mark, name = m.st.Good.Render(" ✓"), m.st.Bold.Render(t.label)
		}
		row := mark + " " + name + "  " + m.st.Faint.Render(truncate(t.detail, inner-22))
		if i == m.targetSel {
			row = m.st.SelRow.Render(padRight(" ▸ "+stripANSI(t.label+"  "+t.detail), inner))
		}
		b.WriteString(row + "\n")
	}

	b.WriteString("\n  " + m.st.Faint.Render("enter select · r refresh · ↑↓ move · esc cancel"))
	return m.st.Overlay.Width(w).Render(b.String())
}

// ---- completion menu -------------------------------------------------------

// completionView lists what Tab could not decide between. It sits directly
// above the prompt, the way a shell prints its candidates under the cursor.
func (m *Model) completionView() string {
	cs := m.comp.Candidates
	if len(cs) == 0 {
		return ""
	}

	width := clamp(m.w-4, 20, 120)
	inner := width - 2

	// Lay the candidates out in columns, as a shell does, so a long listing
	// does not push the rest of the screen away.
	colW := 0
	for _, c := range cs {
		colW = max(colW, lipgloss.Width(c.Display))
	}
	colW += 2
	cols := max(1, inner/colW)
	const maxRows = 8
	rows := (len(cs) + cols - 1) / cols
	shown := cs
	truncated := false
	if rows > maxRows {
		rows = maxRows
		if n := rows * cols; n < len(cs) {
			shown, truncated = cs[:n], true
		}
	}

	var b strings.Builder
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			i := c*rows + r
			if i >= len(shown) {
				continue
			}
			cand := shown[i]
			style := m.st.Dim
			if cand.Dir {
				style = m.st.Accent
			}
			cell := padRight(cand.Display, colW)
			if i == m.compSel {
				cell = m.st.SelRow.Render(padRight(cand.Display, colW))
			} else {
				cell = style.Render(cell)
			}
			b.WriteString(cell)
		}
		b.WriteByte('\n')
	}

	foot := strconv.Itoa(len(cs)) + " matches · tab next · esc dismiss"
	if truncated {
		foot = strconv.Itoa(len(cs)) + " matches (showing " + strconv.Itoa(len(shown)) +
			") · tab next · esc dismiss"
	}
	b.WriteString(m.st.Faint.Render(foot))

	return m.st.Overlay.Width(width).Render(strings.TrimRight(b.String(), "\n"))
}

// ---- the agent's own question ----------------------------------------------

// choiceView renders an option box. The agent is blocked on it, so it says
// which session is waiting and offers a way out that is not an answer.
func (m *Model) choiceView() string {
	if len(m.choices) == 0 {
		return ""
	}
	head := m.choices[0]
	w := clamp(m.w*3/4, 46, 96)
	inner := w - 2

	var b strings.Builder
	title := "  " + head.sess.Label() + " is asking"
	if n := len(m.choices) - 1; n > 0 {
		title += "   " + m.st.Faint.Render("("+strconv.Itoa(n)+" more waiting)")
	}
	b.WriteString(m.st.Accent.Render(title) + "\n\n")

	for _, line := range strings.Split(lipgloss.Wrap(head.ev.Question, inner-4, " "), "\n") {
		b.WriteString("  " + m.st.Body.Render(line) + "\n")
	}
	b.WriteString("\n")

	for i, o := range head.ev.Options {
		num := m.st.Faint.Render(strconv.Itoa(i+1) + ".")
		label := m.st.Dim.Render(o.Label)
		if i == m.choiceSel {
			label = m.st.Bold.Render(o.Label)
		}
		row := "  " + num + " " + label
		if i == m.choiceSel {
			row = m.st.SelRow.Render(padRight(" ▸ "+strconv.Itoa(i+1)+". "+o.Label, inner))
		}
		b.WriteString(row + "\n")
		if o.Detail != "" {
			for _, line := range strings.Split(lipgloss.Wrap(o.Detail, inner-8, " "), "\n") {
				b.WriteString("       " + m.st.Faint.Render(line) + "\n")
			}
		}
	}

	b.WriteString("\n  " + m.st.Faint.Render("1-9 or ↑↓ then enter · esc to answer nothing"))
	return m.st.Overlay.Width(w).Render(b.String())
}

// ---- background commands ---------------------------------------------------

func (m *Model) tasksKey(key string) tea.Cmd {
	all := m.tasks.All()

	// Reading one task's output is a second level; esc steps back out of it
	// rather than closing the whole thing.
	if m.taskOpen != "" {
		switch key {
		case "esc", "left", "h":
			m.taskOpen = ""
		case "x", "ctrl+c":
			if t := m.tasks.Get(m.taskOpen); t != nil {
				t.Stop()
			}
		}
		return nil
	}

	switch key {
	case "up", "k":
		m.taskSel = max(0, m.taskSel-1)
	case "down", "j":
		m.taskSel = min(len(all)-1, m.taskSel+1)
	case "enter", "right", "l":
		if m.taskSel < len(all) {
			m.taskOpen = all[m.taskSel].ID
		}
	case "x":
		if m.taskSel < len(all) {
			all[m.taskSel].Stop()
		}
	}
	return nil
}

// tasksView lists background commands, or one command's output when opened.
func (m *Model) tasksView() string {
	w := clamp(m.w*4/5, 50, 110)
	inner := w - 2

	if m.taskOpen != "" {
		return m.taskOutputView(w, inner)
	}

	all := m.tasks.All()
	var b strings.Builder
	b.WriteString(m.st.Accent.Render("  Background commands") + "\n\n")

	if len(all) == 0 {
		b.WriteString("  " + m.st.Faint.Render("nothing running") + "\n")
	}
	for i, t := range all {
		mark, style := taskMark(m.st, t)
		row := "  " + mark + " " + style.Render(truncate(t.Label, inner-30)) +
			"  " + m.st.Faint.Render(shortDur(t.Elapsed()))
		if i == m.taskSel {
			row = m.st.SelRow.Render(padRight(" ▸ "+stripANSI(mark)+" "+t.Label, inner))
		}
		b.WriteString(row + "\n")
	}

	b.WriteString("\n  " + m.st.Faint.Render("enter read · x stop · esc close"))
	return m.st.Overlay.Width(w).Render(b.String())
}

func (m *Model) taskOutputView(w, inner int) string {
	t := m.tasks.Get(m.taskOpen)
	if t == nil {
		m.taskOpen = ""
		return m.tasksView()
	}
	rows := clamp(m.h/2, 6, 20)

	mark, style := taskMark(m.st, t)
	var b strings.Builder
	b.WriteString("  " + mark + " " + style.Render(truncate(t.Label, inner-24)) +
		"  " + m.st.Faint.Render(shortDur(t.Elapsed())) + "\n\n")

	lines := t.Tail(rows)
	if len(lines) == 0 {
		b.WriteString("  " + m.st.Faint.Render("no output yet") + "\n")
	}
	for _, l := range lines {
		b.WriteString("  " + m.st.Dim.Render(truncate(l, inner-4)) + "\n")
	}

	b.WriteString("\n  " + m.st.Faint.Render("esc back · x stop"))
	return m.st.Overlay.Width(w).Render(b.String())
}

// taskMark is the status glyph and the colour that goes with it.
func taskMark(st *theme.Styles, t *task.Task) (string, lipgloss.Style) {
	switch t.State() {
	case task.Done:
		return st.Good.Render("✓"), st.Dim
	case task.Failed:
		return st.Bad.Render("!"), st.Bad
	case task.Stopped:
		return st.Faint.Render("■"), st.Faint
	default:
		return st.Accent.Render("●"), st.Body
	}
}
