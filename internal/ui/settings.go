package ui

import (
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/config"
)

// The settings page.
//
// Every choice this program lets you make used to be reachable one way: by a
// command you had to know. The model was /model, the engine ctrl+r, the theme
// /theme — and forgotten on restart — the layout /layout, the start folder
// /settings. A setting you cannot find is a setting you do not have, and a
// list of commands is not a place you can look. So they are on one page,
// opened with F2, with the ⚙ in the header, or with /settings.
//
// The page is laid out the way settings pages are everywhere people already
// know them from: sections down the left, the settings of one section on the
// right, each with its name, its value, and one line saying what it does.
// Nothing needs saving — a change is made and kept the moment it is chosen,
// and the footer says so — and a change that can be seen, like a theme, is
// seen at once.
//
// Keys: ↑↓ move, ←→ change the value, enter chooses (or runs an action, or
// edits a folder), tab and shift+tab or 1–5 change section, esc closes. The
// mouse does the same: click a section, click a setting, click its value or
// the ‹ › beside it.

// ---- what the page holds ---------------------------------------------------

type setKind int

const (
	kindChoice setKind = iota // one of a list, changed with ←→
	kindAction                // something to do, with enter
)

type setChoice struct {
	id, label string
	note      string // what this choice means, shown when it is the value
	off       string // why it cannot be chosen here, if it cannot
}

type setItem struct {
	label string
	help  string // what the setting is for, when the value has no note
	kind  setKind

	choices func(m *Model) []setChoice
	value   func(m *Model) string // the current choice's id
	set     func(m *Model, id string) string

	// For an action: its button text, what it does, and doing it.
	button string
	run    func(m *Model) string
}

type setSection struct {
	title string
	blurb string
	items []setItem
}

func settingsSections() []setSection {
	return []setSection{
		{
			title: "General",
			blurb: "Where work begins.",
			items: []setItem{startFolderItem()},
		},
		{
			title: "Agent",
			blurb: "Defaults for a session made with ctrl+t or /new. A session's own choice — ctrl+r, /model, shift+tab — still changes just that session.",
			items: []setItem{engineItem(), modelItem(), modeItem()},
		},
		{
			title: "Appearance",
			blurb: "How it looks. Changes show at once.",
			items: []setItem{themeItem()},
		},
		{
			title: "Layout",
			blurb: "Where the panes stand. Dragging a pane by its title does the same.",
			items: layoutItems(),
		},
		{
			title: "About",
			blurb: "Where agent-tui keeps things. Enter copies the path.",
			items: aboutItems(),
		},
	}
}

// ---- General ---------------------------------------------------------------

// Where a new session begins: the folder agent-tui was opened in, wherever the
// last session left off, or one folder chosen here. The same choice decides
// the folder the app opens on when no -C is given — to the reader they are
// one question: where does a new conversation begin.

type startChoice struct{ id, label string }

var startChoices = []startChoice{
	{config.StartLaunch, "where tui was opened"},
	{config.StartLast, "where the last session was"},
	{config.StartFixed, "a folder you choose"},
}

func startFolderItem() setItem {
	return setItem{
		label: "New sessions start in",
		kind:  kindChoice,
		choices: func(m *Model) []setChoice {
			out := make([]setChoice, len(startChoices))
			for i, c := range startChoices {
				out[i] = setChoice{id: c.id, label: c.label, note: m.startDetail(c.id)}
			}
			return out
		},
		value: func(m *Model) string { return m.prefs.StartMode() },
		set: func(m *Model, id string) string {
			if id == config.StartFixed {
				// A folder has to be typed before it can be chosen; one that
				// was typed before is kept, and enter changes it.
				if strings.TrimSpace(m.prefs.StartPath) == "" {
					m.editStartFolder()
					return ""
				}
			}
			if m.setStart(id, m.prefs.StartPath) {
				return "new sessions start in " + m.startDetail(id)
			}
			return ""
		},
	}
}

// ---- Agent -----------------------------------------------------------------

func engineItem() setItem {
	return setItem{
		label: "Engine",
		kind:  kindChoice,
		choices: func(m *Model) []setChoice {
			out := []setChoice{{id: "", label: "the one used last",
				note: "a new session runs on whatever the last one switched to"}}
			for _, e := range m.reg.All() {
				c := setChoice{id: e.ID(), label: e.Label(), note: e.Detail()}
				if !e.Available() {
					c.off = e.Detail()
				}
				out = append(out, c)
			}
			return out
		},
		value: func(m *Model) string { return m.prefs.Engine },
		set: func(m *Model, id string) string {
			m.savePrefs(func(p *config.Prefs) { p.Engine = id })
			if id == "" {
				return "new sessions use the engine used last"
			}
			return "new sessions run on " + m.reg.Get(id).Label()
		},
	}
}

func modelItem() setItem {
	return setItem{
		label: "Model",
		kind:  kindChoice,
		choices: func(m *Model) []setChoice {
			def := agent.ModelFor(orDefault(m.cfg.Model, agent.DefaultModel))
			out := []setChoice{{id: "", label: "from config · " + def.Label,
				note: "the model config.json or -model names"}}
			for _, md := range agent.Models {
				out = append(out, setChoice{id: md.ID, label: md.Label, note: md.Note})
			}
			return out
		},
		value: func(m *Model) string { return m.prefs.Model },
		set: func(m *Model, id string) string {
			m.savePrefs(func(p *config.Prefs) { p.Model = id })
			if id == "" {
				return "new sessions use the model from config"
			}
			return "new sessions run on " + agent.ModelFor(id).Label
		},
	}
}

func modeItem() setItem {
	return setItem{
		label: "Mode",
		kind:  kindChoice,
		choices: func(m *Model) []setChoice {
			def := m.configMode()
			out := []setChoice{{id: "", label: "from config · " + def.Label(),
				note: def.Detail()}}
			for _, md := range agent.All {
				c := setChoice{id: md.String(), label: md.Label(), note: md.Detail()}
				if md == agent.ModeFull {
					c.note += " — use with care"
				}
				out = append(out, c)
			}
			return out
		},
		value: func(m *Model) string { return m.prefs.Mode },
		set: func(m *Model, id string) string {
			m.savePrefs(func(p *config.Prefs) { p.Mode = id })
			if id == "" {
				return "new sessions use the mode from config"
			}
			return "new sessions start in " + agent.ParseMode(id).Label()
		},
	}
}

// configMode is the mode config.json and the flags give a new session.
func (m *Model) configMode() agent.Mode {
	if m.cfg.AutoApprove {
		return agent.ModeFull
	}
	return agent.ParseMode(m.cfg.Mode)
}

// ---- Appearance ------------------------------------------------------------

func themeItem() setItem {
	return setItem{
		label: "Theme",
		kind:  kindChoice,
		choices: func(m *Model) []setChoice {
			var out []setChoice
			for _, n := range m.themeChoices() {
				out = append(out, setChoice{id: n, label: n,
					note: "drop a theme file in " + filepath.Join(config.Dir(), "themes") + " to add one"})
			}
			return out
		},
		value: func(m *Model) string { return m.themeName() },
		set: func(m *Model, id string) string {
			m.setTheme(id)
			if m.themeName() != id {
				return m.notice // the theme was refused; say why
			}
			m.savePrefs(func(p *config.Prefs) { p.Theme = id })
			return "theme: " + id
		},
	}
}

// ---- Layout ----------------------------------------------------------------

func layoutItems() []setItem {
	return []setItem{
		{
			label: "Sidebar",
			kind:  kindChoice,
			choices: func(*Model) []setChoice {
				return []setChoice{
					{id: "left", label: "left", note: "sessions and files on the left edge"},
					{id: "right", label: "right", note: "sessions and files on the right edge"},
				}
			},
			value: func(m *Model) string { return pick(m.dock.SideRight, "right", "left") },
			set: func(m *Model, id string) string {
				m.dock.SideRight = id == "right"
				m.applyDock()
				return "sidebar on the " + id
			},
		},
		{
			label: "Preview",
			kind:  kindChoice,
			choices: func(*Model) []setChoice {
				return []setChoice{
					{id: "right", label: "right of the transcript", note: "the file you open sits beside the conversation"},
					{id: "left", label: "left of the transcript", note: "the file you open sits between the sidebar and the conversation"},
				}
			},
			value: func(m *Model) string { return pick(m.dock.AuxLeft, "left", "right") },
			set: func(m *Model, id string) string {
				m.dock.AuxLeft = id == "left"
				m.applyDock()
				return "preview " + id + " of the transcript"
			},
		},
		{
			label: "Files",
			kind:  kindChoice,
			choices: func(*Model) []setChoice {
				return []setChoice{
					{id: "below", label: "below the sessions", note: "the session list on top, the tree under it"},
					{id: "above", label: "above the sessions", note: "the tree on top, the session list under it"},
				}
			},
			value: func(m *Model) string { return pick(m.dock.TreeTop, "above", "below") },
			set: func(m *Model, id string) string {
				m.dock.TreeTop = id == "above"
				m.applyDock()
				return "files " + id + " the sessions"
			},
		},
		{
			label: "File tree",
			kind:  kindChoice,
			choices: func(*Model) []setChoice {
				return []setChoice{
					{id: "open", label: "open", note: "drag the rule above it to make it taller or shorter"},
					{id: "folded", label: "folded to one line", note: "click its title to open it for a moment"},
				}
			},
			value: func(m *Model) string { return pick(m.treeFold, "folded", "open") },
			set: func(m *Model, id string) string {
				if (id == "folded") != m.treeFold {
					m.toggleTreeFold()
				}
				return "file tree " + id
			},
		},
		{
			label:  "Reset layout",
			kind:   kindAction,
			button: "reset",
			help:   "widths, positions and the file tree back to how they started",
			run: func(m *Model) string {
				m.layoutCommand("reset")
				return "layout reset"
			},
		},
	}
}

func pick(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

// ---- About -----------------------------------------------------------------

func aboutItems() []setItem {
	path := func(label, help string, at func() string) setItem {
		return setItem{
			label:  label,
			kind:   kindAction,
			button: "copy",
			help:   help,
			run: func(m *Model) string {
				p := at()
				m.setCopy = p
				return "copied " + p
			},
			value: func(*Model) string { return at() },
		}
	}
	return []setItem{
		path("Settings file", "what this page saves", func() string { return filepath.Join(config.DataDir(), "prefs.json") }),
		path("Config file", "the file you write by hand; this page outranks it", func() string { return filepath.Join(config.Dir(), "config.json") }),
		path("Sessions folder", "every conversation, one file each", func() string { return filepath.Join(config.DataDir(), "sessions") }),
		path("Themes folder", "drop a theme file here to add one", func() string { return filepath.Join(config.Dir(), "themes") }),
	}
}

// ---- saving ----------------------------------------------------------------

// savePrefs changes the saved settings. It reads the file again first: the
// copy held here may be older than one another window has written since.
func (m *Model) savePrefs(change func(*config.Prefs)) {
	p := config.LoadPrefs()
	change(&p)
	if err := config.SavePrefs(p); err != nil {
		m.setErr = "could not save: " + err.Error()
		return
	}
	m.prefs = p
}

// newSession starts an empty session with the defaults the settings say, in
// the folder they say, and switches to it.
func (m *Model) newSession() tea.Cmd {
	from := m.mgr.Active()
	s := m.mgr.New()

	s.Engine = m.lastEngine
	if id := m.prefs.Engine; id != "" && m.reg.Has(id) {
		s.Engine = id
	}
	if m.prefs.Model != "" {
		s.Model = m.prefs.Model
	}
	// The mode used to be set for the first session alone, so every session
	// made after it was in auto whatever config.json said.
	if m.prefs.Mode != "" {
		s.Mode = agent.ParseMode(m.prefs.Mode).String()
	} else {
		s.Mode = m.configMode().String()
	}

	note := "new session"
	switch m.prefs.StartMode() {
	case config.StartLast:
		// The session you were just in is the last one. Its target comes too:
		// a directory inside a container is not a directory on the host.
		if from != nil && from != s {
			s.Target, s.CWD = from.Target, m.sessionCWD(from)
		}
	case config.StartFixed:
		if dir, err := config.ExpandDir(m.prefs.StartPath); err == nil && config.IsDir(dir) {
			s.CWD = dir
		} else {
			note = "new session — the start folder in settings is not a folder, so it starts here"
		}
	}
	cmd := m.onSessionSwitch()
	m.notice = note
	return cmd
}

// setStart saves where new sessions start.
func (m *Model) setStart(mode, path string) bool {
	m.savePrefs(func(p *config.Prefs) { p.StartDir, p.StartPath = mode, path })
	return m.setErr == ""
}

// startDetail says, concretely, where a choice leads.
func (m *Model) startDetail(id string) string {
	switch id {
	case config.StartLast:
		if m.prefs.LastRoot != "" {
			return "the session you are leaving · on startup " + m.prefs.LastRoot
		}
		return "the session you are leaving"
	case config.StartFixed:
		if p := strings.TrimSpace(m.prefs.StartPath); p != "" {
			return p + "  · enter to change"
		}
		return "type a folder"
	}
	return m.hostRoot()
}

// editStartFolder opens the folder input under the start folder setting.
func (m *Model) editStartFolder() {
	if strings.TrimSpace(m.setIn.Value()) == "" {
		m.setIn.SetValue(orDefault(m.prefs.StartPath, m.sessionCWD(m.mgr.Active())))
	}
	m.setEditing = true
	m.setErr = ""
	m.setIn.Focus()
	m.setIn.CursorEnd()
}

// chooseStartFolder accepts the typed folder, or says why it cannot.
func (m *Model) chooseStartFolder() {
	typed := strings.TrimSpace(m.setIn.Value())
	dir, err := config.ExpandDir(typed)
	if err != nil || !config.IsDir(dir) {
		m.setErr = "not a folder: " + orDefault(typed, "(empty)")
		return
	}
	m.setIn.SetValue(dir)
	if m.setStart(config.StartFixed, dir) {
		m.setEditing = false
		m.setIn.Blur()
		m.setSaid = "new sessions start in " + dir
	}
}

// ---- moving around ---------------------------------------------------------

// openSettings shows the page, on the section it was left on.
func (m *Model) openSettings() {
	m.overlay = overlaySettings
	m.setTallW = 0
	m.setEditing, m.setErr, m.setSaid = false, "", ""
	m.setIn.SetValue(m.prefs.StartPath)
	m.setIn.Blur()
	secs := settingsSections()
	m.setSec = clamp(m.setSec, 0, len(secs)-1)
	m.setSel = clamp(m.setSel, 0, len(secs[m.setSec].items)-1)
}

func (m *Model) settingsSection() setSection {
	secs := settingsSections()
	return secs[clamp(m.setSec, 0, len(secs)-1)]
}

func (m *Model) settingsItem() (setItem, bool) {
	sec := m.settingsSection()
	if m.setSel < 0 || m.setSel >= len(sec.items) {
		return setItem{}, false
	}
	return sec.items[m.setSel], true
}

func (m *Model) gotoSection(i int) {
	n := len(settingsSections())
	m.setSec = (i%n + n) % n
	m.setSel = 0
	m.setEditing = false
	m.setErr = ""
	m.setIn.Blur()
}

func (m *Model) settingsKey(k tea.KeyPressMsg) tea.Cmd {
	if m.setEditing {
		switch k.String() {
		case "enter":
			m.chooseStartFolder()
			return nil
		case "esc":
			m.setEditing, m.setErr = false, ""
			m.setIn.Blur()
			return nil
		}
		var cmd tea.Cmd
		m.setIn, cmd = m.setIn.Update(k)
		m.setErr = ""
		return cmd
	}

	key := k.String()
	switch key {
	case "esc", "q", "f2":
		m.closeOverlay()
		return nil
	case "tab":
		m.gotoSection(m.setSec + 1)
	case "shift+tab":
		m.gotoSection(m.setSec - 1)
	case "up", "k", "ctrl+p":
		m.setSel = max(0, m.setSel-1)
	case "down", "j", "ctrl+n":
		m.setSel = min(len(m.settingsSection().items)-1, m.setSel+1)
	case "left", "h":
		return m.stepSetting(-1)
	case "right", "l", "space":
		return m.stepSetting(1)
	case "enter":
		return m.activateSetting()
	default:
		if n, err := strconv.Atoi(key); err == nil && n >= 1 && n <= len(settingsSections()) {
			m.gotoSection(n - 1)
		}
	}
	return nil
}

// stepSetting moves the selected choice by one, past those that cannot be
// chosen here, and applies it.
func (m *Model) stepSetting(by int) tea.Cmd {
	it, ok := m.settingsItem()
	if !ok || it.kind != kindChoice {
		return nil
	}
	cs := it.choices(m)
	if len(cs) == 0 {
		return nil
	}
	at := choiceIndex(cs, it.value(m))
	for range cs {
		at = (at + by + len(cs)) % len(cs)
		if cs[at].off == "" {
			break
		}
	}
	if cs[at].off != "" || cs[at].id == it.value(m) {
		return nil
	}
	m.applySetting(it, cs[at].id)
	return nil
}

// activateSetting is enter: an action runs, a folder is edited, a choice
// moves on to the next.
func (m *Model) activateSetting() tea.Cmd {
	it, ok := m.settingsItem()
	if !ok {
		return nil
	}
	if it.kind == kindAction {
		m.setErr = ""
		m.setSaid = it.run(m)
		if m.setCopy != "" {
			p := m.setCopy
			m.setCopy = ""
			return m.copyText(p)
		}
		return nil
	}
	if it.label == startFolderItem().label && m.prefs.StartMode() == config.StartFixed {
		m.editStartFolder()
		return nil
	}
	return m.stepSetting(1)
}

func (m *Model) applySetting(it setItem, id string) {
	m.setErr = ""
	m.setTallW = 0
	said := it.set(m, id)
	if m.setErr == "" && said != "" {
		m.setSaid = said
	}
}

func choiceIndex(cs []setChoice, id string) int {
	for i, c := range cs {
		if c.id == id {
			return i
		}
	}
	return 0
}

// ---- drawing ---------------------------------------------------------------

// setHit is something on the page a click can land on.
type setHit struct {
	y, x0, x1 int // overlay content line, and columns within it
	kind      int // hitSection, hitRow, hitPrev, hitNext
	idx       int
}

const (
	hitSection = iota
	hitRow
	hitPrev
	hitNext
)

const settingsNavW = 16

func (m *Model) settingsSize() (w, h int) {
	return clamp(m.w*4/5, 64, 116), clamp(m.h-4, 16, 34)
}

func (m *Model) settingsView() string {
	w, h := m.settingsSize()
	inner := w - 2
	secs := settingsSections()
	sec := secs[clamp(m.setSec, 0, len(secs)-1)]
	m.setHits = m.setHits[:0]

	bodyW := inner - settingsNavW - 3

	// The box is as tall as the tallest section, so it keeps its size as you
	// move between them — a page that jumps is a page you lose your place on.
	// Measured once per width and after a change, not on every frame: some
	// of the choices read the disk.
	if m.setTallW != w {
		m.setTallW, m.setTall = w, 0
		for _, s := range secs {
			b, _ := m.sectionBody(s, bodyW, 0)
			m.setTall = max(m.setTall, len(b))
		}
	}
	m.setHits = m.setHits[:0]
	m.setInputY = -1
	body, starts := m.sectionBody(sec, bodyW, m.setSel)

	// Rows available for it, between the title and the footer.
	rows := clamp(max(m.setTall, len(body), len(secs)), len(secs), h-6)
	top := 0
	if m.setSel < len(starts) {
		if end := starts[m.setSel] + 3; end > rows {
			top = end - rows
		}
	}

	var b strings.Builder
	title := m.st.Accent.Render("  ⚙ Settings")
	right := m.st.Faint.Render("changes save as you go  ")
	b.WriteString(title + strings.Repeat(" ", max(1, inner-lipgloss.Width(title)-lipgloss.Width(right))) + right + "\n\n")

	for r := 0; r < rows; r++ {
		line := r + 2 // content line within the overlay
		nav := strings.Repeat(" ", settingsNavW)
		if r < len(secs) {
			label := " " + strconv.Itoa(r+1) + " " + secs[r].title
			if r == m.setSec {
				nav = m.st.SelRow.Render(padRight(label, settingsNavW))
			} else {
				nav = m.st.Dim.Render(padRight(label, settingsNavW))
			}
			m.setHits = append(m.setHits, setHit{y: line, x0: 0, x1: settingsNavW, kind: hitSection, idx: r})
		}
		right := ""
		if i := top + r; i < len(body) {
			right = body[i]
			for j := range m.setHits {
				if m.setHits[j].kind != hitSection && m.setHits[j].y == -1-i {
					m.setHits[j].y = line
				}
			}
			if m.setInputY == -1-i {
				m.setInputY = line
			}
		}
		b.WriteString(nav + m.st.Faint.Render(" │ ") + right + "\n")
	}

	// Footer: what just happened, then how to move.
	status := ""
	switch {
	case m.setErr != "":
		status = m.st.Bad.Render("  " + truncate(m.setErr, inner-4))
	case m.setSaid != "":
		status = m.st.Good.Render("  ✓ " + truncate(m.setSaid, inner-6))
	}
	b.WriteString("\n" + status + "\n")
	hint := "↑↓ move · ←→ change · enter choose · tab section · esc close"
	if m.setEditing {
		hint = "type a folder · ~ is your home · enter save · esc back"
	}
	b.WriteString(m.st.Faint.Render("  " + truncate(hint, inner-2)))
	return m.st.Overlay.Width(w).Render(b.String())
}

// sectionBody is the right-hand side of the page for one section, as lines,
// with the line each setting starts on.
func (m *Model) sectionBody(sec setSection, w, sel int) ([]string, []int) {
	var body []string
	for _, l := range strings.Split(wrapPlain(sec.blurb, w), "\n") {
		body = append(body, m.st.Faint.Render(fitLine(l, w)))
	}
	body = append(body, "")
	starts := make([]int, len(sec.items))
	for i, it := range sec.items {
		starts[i] = len(body)
		body = append(body, m.settingRows(it, i == sel, w, i, len(body))...)
		body = append(body, "")
	}
	return body, starts
}

// fitLine keeps a line inside its column. A path has no spaces to wrap at, so
// one that is too long gives up its start: the end of a path is the part that
// says which folder it is.
func fitLine(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	return truncateLeft(s, w)
}

// settingRows draws one setting: its name and value on one line, what it means
// under that, and the folder input when it is open. Click targets are recorded
// against body lines (as -1-line) and moved to screen lines once the page is
// laid out and scrolled.
func (m *Model) settingRows(it setItem, sel bool, w, idx, at int) []string {
	bar := "  "
	label := m.st.Body.Render(it.label)
	if sel {
		bar = m.st.Accent.Render("▌ ")
		label = m.st.Bold.Render(it.label)
	}

	var value, note string
	switch it.kind {
	case kindChoice:
		cs := it.choices(m)
		cur := it.value(m)
		c := cs[choiceIndex(cs, cur)]
		note = orDefault(c.note, it.help)
		if c.off != "" {
			note = "not available here: " + c.off
		}
		value = c.label
		if sel {
			value = m.st.Faint.Render("‹ ") + m.st.Accent.Render(c.label) + m.st.Faint.Render(" ›")
		} else {
			value = m.st.Dim.Render(c.label)
		}
	case kindAction:
		note = it.help
		if it.value != nil {
			note = it.value(m)
		}
		btn := "[ " + it.button + " ]"
		if sel {
			value = m.st.SelRow.Render(btn)
		} else {
			value = m.st.Dim.Render(btn)
		}
	}

	room := w - lipgloss.Width(bar) - lipgloss.Width(label) - 2
	value = truncateLeft(value, max(8, room))
	gap := max(1, w-lipgloss.Width(bar)-lipgloss.Width(label)-lipgloss.Width(value))
	head := bar + label + strings.Repeat(" ", gap) + value

	vx := w - lipgloss.Width(value) + settingsNavW + 3
	m.setHits = append(m.setHits,
		setHit{y: -1 - at, x0: settingsNavW + 3, x1: settingsNavW + 3 + w, kind: hitRow, idx: idx},
		setHit{y: -1 - at, x0: vx, x1: vx + 2, kind: hitPrev, idx: idx},
		setHit{y: -1 - at, x0: vx + 2, x1: vx + lipgloss.Width(value), kind: hitNext, idx: idx},
	)

	out := []string{head}
	for _, l := range strings.Split(wrapPlain(note, w-4), "\n") {
		out = append(out, "    "+m.st.Faint.Render(fitLine(l, w-4)))
	}
	if sel && m.setEditing && it.label == startFolderItem().label {
		m.setInputY = -1 - (at + len(out))
		m.setIn.SetWidth(max(10, w-8))
		out = append(out, "    "+m.st.Accent.Render("› ")+m.setIn.View())
	}
	return out
}

// wrapPlain wraps plain text to a width, on spaces.
func wrapPlain(s string, w int) string {
	if w < 8 || lipgloss.Width(s) <= w {
		return s
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case lipgloss.Width(line)+1+lipgloss.Width(word) <= w:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// settingsClick does what the keys do, for whatever was drawn under the
// pointer.
func (m *Model) settingsClick(x, y int) tea.Cmd {
	cx, cy := x-m.overlayX-1, y-m.overlayY-1
	for _, h := range m.setHits {
		if h.y != cy || cx < h.x0 || cx >= h.x1 {
			continue
		}
		switch h.kind {
		case hitSection:
			m.gotoSection(h.idx)
			return nil
		case hitPrev:
			m.setSel = h.idx
			return m.stepSetting(-1)
		case hitNext:
			m.setSel = h.idx
			return m.activateSetting()
		case hitRow:
			// Prev and next are checked first because they are inside the row.
			if m.setSel != h.idx {
				m.setSel = h.idx
				m.setEditing = false
				return nil
			}
		}
	}
	// A click on the row that is already selected, outside its value, does
	// nothing; it was a click to look, not to change.
	return nil
}

// settingsCursor places the real cursor on the folder input while it is being
// typed into, so an input method has somewhere to compose.
func (m *Model) settingsCursor() *tea.Cursor {
	if !m.setEditing || m.setInputY < 0 {
		return nil
	}
	return offsetCursor(m.setIn.Cursor(),
		m.overlayX+1+settingsNavW+3+6, m.overlayY+1+m.setInputY)
}
