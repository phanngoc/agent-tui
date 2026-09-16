package ui

import (
	"context"
	"errors"
	"path"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/search"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/task"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// Update drains any work left by a path that had no way to return it. The
// approval and choice queues pull a waiting session to the front from inside
// the event pump, several frames deep in functions that return nothing; the
// reindex that move needs has to reach the runtime somehow.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.update(msg)
	if len(m.deferred) == 0 {
		return m, cmd
	}
	batch := append([]tea.Cmd{cmd}, m.deferred...)
	m.deferred = nil
	return m, tea.Batch(batch...)
}

// defer_ queues a command for the next Update to return.
func (m *Model) defer_(cmd tea.Cmd) {
	if cmd != nil {
		m.deferred = append(m.deferred, cmd)
	}
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		m.invalidateChat()
		return m, nil

	case indexReadyMsg:
		m.status = ""
		m.notice = plural(msg.n, "file") + " indexed in " + msg.took.Round(1e6).String()
		if m.overlay == overlayFinder {
			m.refreshFinder()
		}
		return m, nil

	case fileMsg:
		return m, m.applyFile(msg)

	case grepMsg:
		if msg.seq == m.grepSeq {
			m.setGrepResult(msg.res)
			m.grepBusy = false
		}
		return m, nil

	case agentMsg:
		return m, m.applyAgentEvent(msg)

	case agentGoneMsg:
		msg.sess.Busy, msg.sess.Status = false, ""
		delete(m.runs, msg.sess.ID)
		m.mgr.Save(msg.sess)
		m.invalidateChat()
		return m, nil

	case bangDoneMsg:
		m.applyBangDone(msg)
		return m, nil

	case wslReadyMsg:
		return m, m.applyWSLReady(msg)

	case gitLogMsg:
		return m, m.applyGitLog(msg)

	case gitDiffMsg:
		m.applyGitDiff(msg)
		return m, nil

	case treeMsg:
		// Keep the selection on the same path when rows shift underneath it.
		if n := len(m.tree.Rows()); m.treeSel >= n {
			m.treeSel = max(0, n-1)
		}
		return m, m.watchTree()

	case completionMsg:
		return m, m.applyCompletionResult(msg)

	case taskMsg:
		return m, m.watchTasks()

	case tickMsg:
		if m.mgr.Active().Busy {
			return m, tick()
		}
		return m, nil

	case tea.KeyPressMsg:
		return m, m.onKey(msg)

	case tea.MouseMsg:
		return m, m.onMouse(msg)
	}

	// Everything else (focus changes, spinner ticks) goes to the components.
	// Mouse events are handled above instead, so a scroll moves the pane the
	// pointer is over rather than every pane at once.
	var cmds []tea.Cmd
	var cmd tea.Cmd
	m.spin, cmd = m.spin.Update(msg)
	cmds = append(cmds, cmd)
	m.chat, cmd = m.chat.Update(msg)
	cmds = append(cmds, cmd)
	m.prev, cmd = m.prev.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

// onKey routes a key press: modal overlays first, then global bindings, then
// whichever pane has focus.
func (m *Model) onKey(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()

	// A prompt the agent is blocked on is modal: nothing else may run.
	switch m.overlay {
	case overlayApproval:
		return m.approvalKey(key)
	case overlayChoice:
		return m.choiceKey(key)
	}

	// An open buffer owns the keyboard, because almost every key is text. Only
	// the handful that would otherwise be unreachable are let through, and
	// quitting is guarded while there is unsaved work.
	if m.edit != nil && m.focus == focusPreview && m.overlay == overlayNone {
		switch key {
		case "ctrl+c", "ctrl+q":
			if m.edit.Dirty() {
				m.notice = "unsaved changes in " + m.edit.rel + " — ctrl+s to save, esc esc to discard"
				return nil
			}
		case "ctrl+o", "f1":
			// fall through to the global bindings
		default:
			return m.editorKey(k)
		}
	}

	switch key {
	case "ctrl+c":
		if m.mgr.Active().Busy {
			m.cancelRun()
			return nil
		}
		return tea.Quit
	case "ctrl+q":
		return tea.Quit
	case "esc":
		// The task view has two levels, so esc steps out of a task's output
		// before it closes the list.
		if m.overlay == overlayTasks && m.taskOpen != "" {
			m.taskOpen = ""
			return nil
		}
		if m.overlay != overlayNone {
			m.closeOverlay()
			return nil
		}
		if m.finding {
			m.stopFind()
			return nil
		}
		if m.focus != focusInput {
			m.setFocus(focusInput)
			return nil
		}
		return nil
	case "ctrl+p":
		m.overlay = overlayFinder
		m.finderIn.SetValue("")
		m.finderIn.Focus()
		m.refreshFinder()
		return nil
	case "ctrl+f":
		if m.focus == focusPreview && m.file != nil {
			m.startFind()
			return nil
		}
		m.overlay = overlayGrep
		m.grepIn.Focus()
		return nil
	case "ctrl+g":
		m.overlay = overlayGrep
		m.grepIn.Focus()
		return nil
	case "f1":
		if m.overlay == overlayHelp {
			m.overlay = overlayNone
		} else {
			m.overlay = overlayHelp
		}
		return nil
	case "ctrl+d":
		m.overlay = overlayTarget
		m.refreshTargets()
		return nil
	case "ctrl+k":
		m.overlay = overlayTasks
		m.taskSel, m.taskOpen = 0, ""
		return nil
	case "ctrl+r":
		m.overlay = overlayEngine
		m.engineSel = m.engineIndex(m.mgr.Active().Engine)
		return nil
	case "ctrl+t":
		s := m.mgr.New()
		s.Engine = m.lastEngine
		cmd := m.onSessionSwitch()
		m.notice = "new session"
		return cmd
	case "alt+t":
		return m.forkSession(m.mgr.Active())
	case "ctrl+w":
		m.mgr.Close(m.mgr.ActiveIndex())
		return m.onSessionSwitch()
	case "ctrl+pgdown", "alt+down":
		m.mgr.Cycle(1)
		return m.onSessionSwitch()
	case "ctrl+pgup", "alt+up":
		m.mgr.Cycle(-1)
		return m.onSessionSwitch()
	case "ctrl+b":
		m.showSessions = !m.showSessions
		m.resize(m.w, m.h)
		return nil
	case "ctrl+e":
		m.showPreview = !m.showPreview
		m.resize(m.w, m.h)
		return nil
	case "ctrl+o":
		// Always available, because tab belongs to the prompt.
		if m.overlay == overlayNone {
			m.cycleFocus(1)
			return nil
		}
	case "tab":
		// In the prompt this completes, the way a terminal does; elsewhere
		// there is no text to complete, so it keeps cycling panes.
		if m.overlay == overlayNone && m.focus != focusInput {
			m.cycleFocus(1)
			return nil
		}
	case "shift+tab":
		// Cycles the mode, the way Claude Code does. The completion menu gets
		// it first when it is open, which is handled before we reach here.
		if m.overlay == overlayNone {
			m.setMode(sessionMode(m.mgr.Active()).Next())
			return nil
		}
	}

	// alt+1..9 jumps straight to a session.
	if strings.HasPrefix(key, "alt+") && len(key) == 5 && key[4] >= '1' && key[4] <= '9' {
		if n, err := strconv.Atoi(key[4:]); err == nil {
			m.mgr.Select(n - 1)
			return m.onSessionSwitch()
		}
	}

	switch m.overlay {
	case overlayFinder:
		return m.finderKey(k)
	case overlayGrep:
		return m.grepKey(k)
	case overlayEngine:
		return m.engineKey(k.String())
	case overlayModel:
		return m.modelKey(k.String())
	case overlayGit:
		return m.gitKey(k.String())
	case overlayTarget:
		return m.targetKey(k.String())
	case overlayTasks:
		return m.tasksKey(k.String())
	case overlayHelp:
		m.overlay = overlayNone
		return nil
	}

	switch m.focus {
	case focusInput:
		return m.inputKey(k)
	case focusPreview:
		return m.previewKey(k)
	case focusChat:
		var cmd tea.Cmd
		m.chat, cmd = m.chat.Update(k)
		return cmd
	case focusExplorer:
		return m.explorerKey(k.String())
	case focusSessions:
		return m.sessionsKey(key)
	}
	return nil
}

func (m *Model) inputKey(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()

	// The completion menu takes the keys that drive it, and any other key
	// dismisses it, which is how a shell behaves.
	if m.compOpen {
		switch key {
		case "tab", "down", "ctrl+n":
			return m.cycleCompletion(1)
		case "shift+tab", "up", "ctrl+p":
			return m.cycleCompletion(-1)
		case "enter", "right":
			m.closeCompletion()
			return nil
		case "esc", "ctrl+g":
			m.closeCompletion()
			return nil
		default:
			m.closeCompletion()
		}
	}

	switch key {
	case "tab":
		return m.completeCmd()

	case "up", "ctrl+p":
		// Single-line prompts recall history, the way a shell does; a
		// multi-line prompt needs the arrows for moving around in it.
		if !strings.Contains(m.input.Value(), "\n") && m.recallHistory(-1) {
			return nil
		}
	case "down", "ctrl+n":
		if !strings.Contains(m.input.Value(), "\n") && m.recallHistory(1) {
			return nil
		}
	}

	switch key {
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return nil
		}
		// Commands are acted on rather than sent to the agent: they are
		// requests to move this session, not questions for it.
		if name, arg, ok := parseSlash(text); ok {
			m.pushHistory(text)
			m.input.Reset()
			return m.runSlash(name, arg)
		}
		// A `!` line is a command to run here, not a question to ask. It does
		// not wait for a turn to finish: the reason to reach for it mid-turn is
		// usually to find out what the agent is doing.
		if line, ok := parseBang(text); ok {
			m.pushHistory(text)
			m.input.Reset()
			m.chat.GotoBottom()
			return m.runBang(line)
		}
		if dir, ok := parseCD(text); ok {
			m.pushHistory(text)
			m.input.Reset()
			return m.changeDir(dir)
		}
		if m.mgr.Active().Busy {
			m.notice = "still working — ctrl+c to stop"
			return nil
		}
		m.pushHistory(text)
		m.input.Reset()
		m.chat.GotoBottom()
		return m.send(text)
	case "alt+enter", "ctrl+j":
		m.input.InsertString("\n")
		return nil
	case "ctrl+u":
		m.input.Reset()
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return cmd
}

// applyCompletionResult decides what a Tab actually does, following the same
// rules a shell does: finish the word when that is unambiguous, extend it as
// far as every candidate agrees, and otherwise offer a menu.
func (m *Model) applyCompletionResult(msg completionMsg) tea.Cmd {
	if msg.seq != m.compSeq || msg.line != m.input.Value() {
		return nil // the line moved on while we were listing a directory
	}
	// Nothing is selected when a menu first appears, so the next Tab lands on
	// the first candidate rather than skipping past it.
	m.comp, m.compSel = msg.res, -1

	switch {
	case len(msg.res.Candidates) == 0:
		m.compOpen = false
		m.notice = "no completion"
	case msg.res.Unambiguous():
		m.applyCompletion(msg.res.Candidates[0].Insert)
		m.compOpen = false
		m.notice = ""
	case msg.res.Extends():
		m.applyCompletion(msg.res.Common)
		m.compOpen = false
		m.notice = ""
	default:
		// Nothing more can be typed for free, so show what is on offer.
		m.compOpen = true
		m.notice = ""
	}
	return nil
}

// cycleCompletion steps through the menu, replacing the word as it goes so the
// prompt always shows what would be committed.
func (m *Model) cycleCompletion(delta int) tea.Cmd {
	n := len(m.comp.Candidates)
	if n == 0 {
		m.closeCompletion()
		return nil
	}
	switch {
	case m.compSel < 0 && delta < 0:
		m.compSel = n - 1
	default:
		m.compSel = ((m.compSel+delta)%n + n) % n
	}
	m.applyCompletion(m.comp.Candidates[m.compSel].Insert)
	return nil
}

// parseCD recognises a bare directory change typed into the prompt.
func parseCD(text string) (string, bool) {
	if strings.ContainsAny(text, "\n") {
		return "", false
	}
	rest, ok := strings.CutPrefix(text, "cd")
	if !ok {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	if rest != "" && !strings.HasPrefix(text[2:], " ") && !strings.HasPrefix(text[2:], "\t") {
		return "", false // "cdfoo" is not a directory change
	}
	if rest == "" {
		return "~", true
	}
	return strings.Trim(rest, `"'`), true
}

// changeDir resolves a cd target against the session's current directory.
func (m *Model) changeDir(dir string) tea.Cmd {
	root := m.tree.Root()
	switch {
	case dir == "~":
		return m.setSessionRoot(m.hostRoot())
	case dir == "-":
		return m.setSessionRoot(m.hostRoot())
	case vfs.IsAbs(dir):
		return m.setSessionRoot(vfs.CleanPath(dir))
	default:
		return m.setSessionRoot(vfs.CleanPath(vfs.Join(root, dir)))
	}
}

func (m *Model) previewKey(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()

	// The editor owns the pane while it is open.
	if m.edit != nil {
		return m.editorKey(k)
	}
	if m.finding {
		switch key {
		case "enter":
			m.stepFind(1)
			return nil
		case "esc":
			m.stopFind()
			return nil
		}
		var cmd tea.Cmd
		m.findIn, cmd = m.findIn.Update(k)
		m.applyFind(m.findIn.Value())
		return cmd
	}

	switch key {
	case "/":
		m.startFind()
		return nil
	case "n":
		m.stepFind(1)
		return nil
	case "N", "shift+n":
		m.stepFind(-1)
		return nil
	case "g", "home":
		m.prev.GotoTop()
		m.fileLine = 1
		return nil
	case "G", "shift+g", "end":
		m.prev.GotoBottom()
		m.fileLine = m.file.LineCount()
		return nil
	case "w":
		m.prev.SoftWrap = !m.prev.SoftWrap
		return nil
	case "e":
		return m.openEditor()
	}
	var cmd tea.Cmd
	before := m.prev.YOffset()
	m.prev, cmd = m.prev.Update(k)
	if m.prev.YOffset() != before {
		m.fileLine = m.prev.YOffset() + 1
	}
	return cmd
}

// editorKey routes a key inside the open buffer.
//
// Only the few commands that are not text go to us; everything else is typing,
// and the textarea already knows what to do with it.
func (m *Model) editorKey(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	switch key {
	case "ctrl+s":
		return m.saveEditor()
	case "esc":
		return m.closeEditor(m.discardArmed)
	case "ctrl+z":
		if !m.edit.Undo() {
			m.notice = "nothing to undo"
		}
		return nil
	case "ctrl+y", "ctrl+shift+z":
		if !m.edit.Redo() {
			m.notice = "nothing to redo"
		}
		return nil
	}

	// Anything that changes the buffer opens an undo step first.
	m.edit.snapshot(editKindOf(key))
	m.discardArmed = false

	var cmd tea.Cmd
	m.edit.ta, cmd = m.edit.ta.Update(k)
	return cmd
}

// editKindOf classifies a keystroke for undo grouping.
func editKindOf(key string) editKind {
	switch key {
	case "enter", "backspace", "delete", "ctrl+k", "ctrl+u", "ctrl+w", "ctrl+v", "alt+backspace":
		return editStructural
	}
	if len(key) == 1 || key == "space" || key == "tab" {
		return editType
	}
	return editNone // a cursor move: no snapshot, but it ends a typing run
}

// explorerKey drives the project tree.
func (m *Model) explorerKey(key string) tea.Cmd {
	rows := m.tree.Rows()
	switch key {
	case "up", "k":
		m.treeSel = max(0, m.treeSel-1)
		return m.previewSelected()
	case "down", "j":
		m.treeSel = min(len(rows)-1, m.treeSel+1)
		return m.previewSelected()
	case "g", "home":
		m.treeSel = 0
		return m.previewSelected()
	case "G", "shift+g", "end":
		m.treeSel = max(0, len(rows)-1)
		return m.previewSelected()
	}
	if m.treeSel >= len(rows) {
		return nil
	}
	node := rows[m.treeSel]

	switch key {
	case "enter", "right", "l":
		if node.IsParent() {
			return m.goUp()
		}
		if node.Dir {
			m.tree.Toggle(node.Rel)
			return nil
		}
		// enter is an explicit open, so it hands over to the preview.
		m.showPreview = true
		m.resize(m.w, m.h)
		return m.loadFileAt(vfs.Join(m.tree.Root(), node.Rel), node.Rel, 1, true)

	case "left", "h":
		if node.IsParent() {
			return m.goUp()
		}
		if node.Dir && m.tree.IsOpen(node.Rel) {
			m.tree.Toggle(node.Rel)
			return nil
		}
		// Otherwise jump to the parent row, which is the nearest row above
		// with a smaller depth.
		for i := m.treeSel - 1; i >= 0; i-- {
			if rows[i].Depth < node.Depth {
				m.treeSel = i
				break
			}
		}
		return nil

	case "r":
		// Point this session's agent at the selected directory.
		dir := node.Rel
		if !node.Dir {
			dir = path.Dir(dir)
			if dir == "." {
				dir = ""
			}
		}
		return m.setSessionRoot(vfs.Join(m.tree.Root(), dir))

	case "R":
		return m.setSessionRoot(m.hostRoot())

	case "-", "backspace":
		return m.goUp()
	}
	return nil
}

// goUp moves the session one directory out of its current root.
func (m *Model) goUp() tea.Cmd {
	parent := m.tree.Parent()
	if parent == "" {
		m.notice = "already at the top of this filesystem"
		return nil
	}
	return m.setSessionRoot(parent)
}

// setSessionRoot repoints a session at a directory. Everything that reads files
// follows: the tree, the fuzzy index, content search, and the -C / --dir the
// engine is given.
func (m *Model) setSessionRoot(abs string) tea.Cmd {
	if abs == "" {
		return nil
	}
	s := m.mgr.Active()
	fsys := m.sessionFS(s)

	if st, err := fsys.Stat(context.Background(), abs); err != nil || !st.Dir {
		m.notice = "not a directory: " + abs
		return nil
	}

	s.CWD = abs
	m.mgr.Save(s)

	m.tree.SetRoot(abs)
	m.treeSel, m.treeTop = 0, 0
	m.idx.Retarget(fsys, abs)
	m.setGrepResult(search.Result{})
	m.status = "indexing…"
	m.notice = "working in " + abs
	return m.buildIndex()
}

// hostRoot is the directory the app was opened on.
func (m *Model) hostRoot() string { return m.hostFS.DefaultDir() }

// forkSession branches a session and switches to the branch.
//
// The transcript is copied here, but the engine's own conversation is branched
// on the first turn, so an external agent keeps whatever context it had built
// up rather than re-reading it from the replayed messages.
func (m *Model) forkSession(src *session.Session) tea.Cmd {
	if src == nil {
		return nil
	}
	if len(src.Messages) == 0 {
		m.notice = "nothing to fork yet"
		return nil
	}

	f := m.mgr.Fork(src)
	cmd := m.onSessionSwitch()

	if f.ForkPending {
		m.notice = "forked " + src.Label() + "; the agent branches on your next message"
	} else {
		m.notice = "forked " + src.Label()
	}
	return cmd
}

func (m *Model) sessionsKey(key string) tea.Cmd {
	switch key {
	case "up", "k":
		m.sessSel = max(0, m.sessSel-1)
	case "down", "j":
		m.sessSel = min(m.mgr.Len()-1, m.sessSel+1)
	case "enter":
		m.mgr.Select(m.sessSel)
		cmd := m.onSessionSwitch()
		m.setFocus(focusInput)
		return cmd
	case "n":
		s := m.mgr.New()
		s.Engine = m.lastEngine
		cmd := m.onSessionSwitch()
		m.notice = "new session"
		return cmd
	case "f":
		all := m.mgr.All()
		if m.sessSel >= 0 && m.sessSel < len(all) {
			return m.forkSession(all[m.sessSel])
		}
	case "d", "x":
		m.mgr.Close(m.sessSel)
		m.sessSel = min(m.sessSel, m.mgr.Len()-1)
		return m.onSessionSwitch()
	}
	return nil
}

// applyAgentEvent folds one event into the session it came from and returns the
// command that pulls the next one.
func (m *Model) applyAgentEvent(msg agentMsg) tea.Cmd {
	s := msg.sess
	next := m.pump(s, msg.ch)
	// A background session's output must not scroll or repaint the foreground.
	foreground := s == m.mgr.Active()

	switch e := msg.ev.(type) {
	case agent.EvStatus:
		s.Status = e.Text

	case agent.EvTextDelta:
		// No cache invalidation here: only the streaming tail changed, and the
		// transcript renders that separately from the committed head.
		s.Partial += e.Text
		if foreground {
			m.chat.GotoBottom()
		}

	case agent.EvThinkingDelta:
		s.Status = "thinking"

	case agent.EvAssistant:
		s.Partial = ""
		s.Append(e.Message)
		if e.Message.Err != "" {
			s.LastErr = e.Message.Err
		}
		m.invalidateChat()
		if foreground {
			m.chat.GotoBottom()
		}
		m.mgr.Save(s)

	case agent.EvApproval:
		m.queueApproval(s, e)

	case agent.EvChoice:
		m.queueChoice(s, e)

	case agent.EvToolStart:
		s.Status = e.Call.Name
		m.invalidateChat()

	case agent.EvToolDone:
		markTool(s, e.Call)
		m.invalidateChat()
		if foreground {
			m.chat.GotoBottom()
		}
		// A write may have changed what the preview is showing.
		if e.Call.Name == "write_file" || e.Call.Name == "edit_file" {
			if m.file != nil {
				// Reloading after an edit must not pull focus away from
				// whatever the user was doing.
				return tea.Batch(next, m.loadFile(m.file.Rel, m.fileLine, false))
			}
		}

	case agent.EvUsage:
		s.InputTokens += e.In
		s.OutputTokens += e.Out
		s.CacheReads += e.CacheRead

	case agent.EvTask:
		// A background command an external agent started, shown beside ours.
		if e.ID != "" {
			if e.Label != "" || m.tasks.Get(e.ID) == nil {
				m.tasks.Adopt(e.ID, firstNonBlank(e.Label, "background command"), s.ID)
			}
			note := e.Note
			if note == "" && e.Output != "" {
				note = "output: " + e.Output
			}
			m.tasks.Update(e.ID, task.State(e.State), note)
		}

	case agent.EvSession:
		// Persist the external agent's own id as soon as it is known, so an
		// interrupted turn can still be resumed later. A fork reports the id of
		// the branch it created, which is what this session continues from now.
		if e.ExternalID != "" && (s.ExternalID != e.ExternalID || s.ForkPending) {
			s.ExternalID = e.ExternalID
			s.ForkPending = false
			m.mgr.Save(s)
		}

	case agent.EvDone:
		s.Busy, s.Status = false, ""
		if e.State != nil {
			s.Live = e.State
		}
		if e.Err != nil && !errors.Is(e.Err, context.Canceled) {
			s.LastErr = e.Err.Error()
			if foreground {
				m.errText = e.Err.Error()
			}
		}
		m.mgr.Save(s)
		m.invalidateChat()
	}
	return next
}

// queueApproval parks a tool-approval request. Only one prompt is on screen at
// a time; a request from a background session pulls that session to the front,
// since its agent is blocked until the user answers.
func (m *Model) queueApproval(s *session.Session, ev agent.EvApproval) {
	m.approvals = append(m.approvals, pendingApproval{sess: s, ev: ev})
	if m.overlay == overlayApproval {
		return
	}
	m.showNextApproval()
}

// queueChoice parks a question from the agent. Like an approval it is modal,
// and it pulls its session to the front because that agent is waiting.
func (m *Model) queueChoice(s *session.Session, ev agent.EvChoice) {
	m.choices = append(m.choices, pendingChoice{sess: s, ev: ev})
	if m.overlay == overlayChoice {
		return
	}
	m.showNextChoice()
}

func (m *Model) showNextChoice() {
	m.choiceSel = 0
	if len(m.choices) == 0 {
		if m.overlay == overlayChoice {
			m.overlay = overlayNone
		}
		return
	}
	head := m.choices[0]
	for i, s := range m.mgr.All() {
		if s == head.sess {
			m.mgr.Select(i)
			m.defer_(m.onSessionSwitch())
			break
		}
	}
	m.overlay = overlayChoice
}

// choiceKey drives the option box.
func (m *Model) choiceKey(key string) tea.Cmd {
	if len(m.choices) == 0 {
		m.overlay = overlayNone
		return nil
	}
	opts := m.choices[0].ev.Options

	answer := func(pick int) {
		head := m.choices[0]
		m.choices = m.choices[1:]
		replyChoice(head.ev.Reply, pick)
		m.showNextChoice()
	}

	switch key {
	case "up", "k", "shift+tab":
		m.choiceSel = max(0, m.choiceSel-1)
	case "down", "j", "tab":
		m.choiceSel = min(len(opts)-1, m.choiceSel+1)
	case "enter", "space":
		answer(m.choiceSel)
	case "esc":
		answer(-1)
	case "ctrl+c":
		answer(-1)
		m.cancelRun()
	default:
		// Number keys pick directly, which is faster than arrowing for a
		// short list.
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			if i := int(key[0] - '1'); i < len(opts) {
				answer(i)
			}
		}
	}
	return nil
}

func (m *Model) showNextApproval() {
	if len(m.approvals) == 0 {
		if m.overlay == overlayApproval {
			m.overlay = overlayNone
		}
		return
	}
	head := m.approvals[0]
	for i, s := range m.mgr.All() {
		if s == head.sess {
			m.mgr.Select(i)
			m.defer_(m.onSessionSwitch())
			break
		}
	}
	m.overlay = overlayApproval
}

// markTool folds a finished tool call into the placeholder recorded when the
// assistant turn was committed.
//
// It merges rather than replaces: an engine's completion event often carries
// only the id and the result, because the name and arguments were already sent
// with the call. Overwriting would blank them out of the transcript.
func markTool(s *session.Session, call session.ToolCall) {
	for i := len(s.Messages) - 1; i >= 0; i-- {
		for j := range s.Messages[i].Tools {
			cur := &s.Messages[i].Tools[j]
			if cur.ID != call.ID {
				continue
			}
			if call.Name != "" {
				cur.Name = call.Name
			}
			if len(call.Input) > 0 {
				cur.Input = call.Input
			}
			cur.Result, cur.IsError, cur.Done = call.Result, call.IsError, call.Done
			if call.Denied {
				cur.Denied = true
			}
			if call.Elapsed > 0 {
				cur.Elapsed = call.Elapsed
			}
			return
		}
	}
}

func (m *Model) approvalKey(key string) tea.Cmd {
	if len(m.approvals) == 0 {
		m.overlay = overlayNone
		return nil
	}
	reply := func(v agent.Verdict) {
		head := m.approvals[0]
		m.approvals = m.approvals[1:]
		replyOnce(head.ev.Reply, v)
		m.showNextApproval()
	}
	switch key {
	case "y", "Y", "enter":
		reply(agent.Allow)
	case "a", "A":
		// Trust lasts for this run and stays with the session that granted it.
		m.notice = "allowing the rest of this run"
		reply(agent.AllowAll)
	case "n", "N", "esc":
		reply(agent.Deny)
	case "ctrl+c":
		reply(agent.Deny)
		m.cancelRun()
	}
	return nil
}

func (m *Model) closeOverlay() {
	m.overlay = overlayNone
	m.taskOpen = ""
	m.finderIn.Blur()
	m.grepIn.Blur()
}

func (m *Model) setFocus(f focus) {
	m.focus = f
	if f == focusInput {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
}

func (m *Model) cycleFocus(d int) {
	order := []focus{focusInput, focusChat}
	if m.showPreview {
		order = append(order, focusPreview)
	}
	if m.sideW > 0 {
		order = append(order, focusExplorer, focusSessions)
	}
	at := 0
	for i, f := range order {
		if f == m.focus {
			at = i
		}
	}
	m.setFocus(order[((at+d)%len(order)+len(order))%len(order)])
}

func (m *Model) onSessionSwitch() tea.Cmd {
	m.sessSel = m.mgr.ActiveIndex()
	cmd := m.showActiveSession()
	m.invalidateChat()
	m.chat.GotoBottom()
	m.errText = m.mgr.Active().LastErr
	return cmd
}

// showActiveSession points everything that reads files at the filesystem and
// directory the active session's agent runs in.
//
// The tree and the index have to move together. They did not: switching
// session repointed the tree alone, and starting up built the tree on the host
// whatever the session said — so a session restored inside a distribution came
// back with the host filesystem aimed at a POSIX path, which reads as an empty
// directory. An explorer showing nothing, and a finder still listing the
// previous session's project, is the same bug seen from two panes.
func (m *Model) showActiveSession() tea.Cmd {
	s := m.mgr.Active()
	fsys, dir := m.sessionFS(s), m.sessionCWD(s)

	if m.tree.FS() == fsys && m.tree.Root() == dir && m.idx.Root() == dir {
		return nil
	}
	m.tree.SetFS(fsys, dir)
	m.treeSel, m.treeTop = 0, 0
	m.file = nil
	m.prev.SetContent("")
	m.idx.Retarget(fsys, dir)
	m.setGrepResult(search.Result{})
	m.status = "indexing…"
	return m.buildIndex()
}

// applyFile installs a freshly loaded file into the preview pane.
func (m *Model) applyFile(msg fileMsg) tea.Cmd {
	if msg.seq != 0 && msg.seq != m.prevSeq {
		return nil // the selection moved on while this was loading
	}
	m.file = msg.f
	if msg.f.Err != nil {
		m.errText = msg.f.Rel + ": " + msg.f.Err.Error()
		m.prev.SetContent("")
		return nil
	}
	m.errText = ""
	m.prev.SetContentLines(msg.f.Styled)
	m.findMatches, m.findSel, m.findHits = nil, 0, 0

	line := msg.line
	if line < 1 {
		line = 1
	}
	m.fileLine = min(line, max(1, msg.f.LineCount()))
	m.centreOn(m.fileLine)
	if msg.focus && m.showPreview {
		m.setFocus(focusPreview)
	}
	return nil
}

// centreOn scrolls so that line (1-based) sits in the middle of the pane.
func (m *Model) centreOn(line int) {
	half := m.prev.Height() / 2
	m.prev.SetYOffset(max(0, line-1-half))
}

func (m *Model) startFind() {
	m.finding = true
	m.findIn.SetValue("")
	m.findIn.Focus()
	m.setFocus(focusPreview)
}

func (m *Model) stopFind() {
	m.finding = false
	m.findIn.Blur()
	m.findMatches, m.findSel, m.findHits = nil, 0, 0
	m.restorePreview()
}

// resize recomputes every pane's geometry.
func (m *Model) resize(w, h int) {
	m.w, m.h = w, h
	if w < 20 || h < 10 {
		m.ready = false
		return
	}
	m.ready = true

	const (
		headerH  = 1
		statusH  = 1
		inputIn  = 3 // textarea rows
		inputBox = inputIn + 2
	)
	bodyH := h - headerH - statusH - inputBox
	bodyH = max(bodyH, 5)

	sidebarW := 0
	if m.showSessions && w >= 90 {
		sidebarW = clamp(w/6, 22, 34)
	}
	previewW := 0
	if m.showPreview && w-sidebarW >= 80 {
		previewW = clamp((w-sidebarW)*45/100, 38, 90)
	}
	chatW := w - sidebarW - previewW
	if chatW < 32 {
		chatW = w - sidebarW
		previewW = 0
	}

	m.chatW, m.prevW, m.sideW, m.bodyH = chatW, previewW, sidebarW, bodyH

	m.chat.SetWidth(max(1, chatW-2))
	m.chat.SetHeight(max(1, bodyH-2))
	m.prev.SetWidth(max(1, previewW-2))
	m.prev.SetHeight(max(1, bodyH-2))
	if m.edit != nil {
		m.edit.ta.SetWidth(max(10, previewW-2))
		m.edit.ta.SetHeight(max(3, bodyH-2))
	}
	m.input.SetWidth(max(10, w-2))
	m.input.SetHeight(inputIn)

	m.finderIn.SetWidth(max(10, m.overlayWidth()-4))
	m.grepIn.SetWidth(max(10, m.overlayWidth()-4))
	m.findIn.SetWidth(max(10, previewW-12))
}

func (m *Model) overlayWidth() int { return clamp(m.w*7/10, 40, 110) }

func clamp(v, lo, hi int) int { return max(lo, min(hi, v)) }

// plural renders a count with its noun. It handles the sibilant endings that
// actually occur in this UI ("match" -> "matches"); anything else takes a bare
// "s".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	suffix := "s"
	switch {
	case strings.HasSuffix(word, "h"), strings.HasSuffix(word, "s"),
		strings.HasSuffix(word, "x"), strings.HasSuffix(word, "ch"):
		suffix = "es"
	}
	return strconv.Itoa(n) + " " + word + suffix
}

// autoApprove stops asking for the rest of this run. It only reaches the
// built-in engine; an external CLI is governed by the flags it was started
// with, and the approval prompt says which engine it came from.
func (m *Model) autoApprove() {
	if a, ok := m.reg.Get(m.mgr.Active().Engine).(interface{ SetAutoApprove(bool) }); ok {
		a.SetAutoApprove(true)
	}
}
