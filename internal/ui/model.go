// Package ui is the Bubble Tea layer: one root model owning three panes, a set
// of overlays, and the plumbing that pumps agent events into the update loop.
package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/complete"
	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/engine"
	"github.com/phanngoc/agent-tui/internal/explorer"
	"github.com/phanngoc/agent-tui/internal/fsx"
	"github.com/phanngoc/agent-tui/internal/highlight"
	"github.com/phanngoc/agent-tui/internal/preview"
	"github.com/phanngoc/agent-tui/internal/search"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/task"
	"github.com/phanngoc/agent-tui/internal/theme"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

type focus int

const (
	focusInput focus = iota
	focusChat
	focusExplorer
	focusPreview
	focusSessions
	focusBtw
)

type overlay int

const (
	overlayNone overlay = iota
	overlayFinder
	overlayGrep
	overlayApproval
	overlayChoice
	overlayTasks
	overlayHelp
	overlayEngine
	overlayTarget
	overlayModel
	overlayGit
	overlayRename
)

// Model is the root Bubble Tea model.
type Model struct {
	cfg    config.Config
	st     *theme.Styles
	idx    *fsx.Index
	loader *preview.Loader
	mgr    *session.Manager
	reg    *engine.Registry
	// sc colours fenced code in the transcript, using the preview's scheme.
	sc *highlight.Scheme

	w, h  int
	ready bool

	// Pane geometry, recomputed only on resize.
	chatW, prevW, sideW, btwW, bodyH int
	// sideSet and prevSet are widths the user chose, in columns. Zero means
	// the pane has not been touched and keeps the width it is given.
	sideSet, prevSet int
	// drag is the divider the mouse is holding, if any.
	drag dragging
	// sel is the text the mouse has selected, in whichever pane it was made.
	sel selection
	// toggles are where the header drew its pane switches, so a click lands on
	// the one that was drawn rather than near it.
	toggles []toggleHit

	focus   focus
	overlay overlay
	// Where the active overlay was drawn, so the terminal cursor can follow its
	// input.
	overlayX, overlayY int

	showSessions bool
	showPreview  bool
	// showBtw opens the side chat's pane. The conversation in it outlives the
	// pane: closing this hides it rather than ending it.
	showBtw bool

	chat  viewport.Model
	prev  viewport.Model
	input textarea.Model
	spin  spinner.Model

	// showAllCalls unfolds the tool calls a turn folded away. It is a way of
	// reading the transcript rather than a property of any turn in it, so it
	// applies to all of them at once.
	showAllCalls bool

	// Transcript render cache: rebuilding the whole transcript on every frame
	// would dominate the update loop once a session gets long.
	chatCache string
	chatKey   string
	// chatTurn is the line the newest exchange starts on, counted while the
	// cache above is built, so opening a session can land there.
	chatTurn int
	// placedChat records that the transcript has been positioned once, so the
	// first layout lands on the newest exchange and later resizes do not yank
	// the reader away from what they were reading.
	placedChat bool

	// Renaming a session.
	renameIn textinput.Model

	// File picker overlay.
	finderIn  textinput.Model
	finderHit []fsx.Hit
	finderSel int

	// Project search overlay.
	grepIn    textinput.Model
	grepRes   search.Result
	grepFiles []grepFile
	grepRows  []grepRow
	grepSel   int
	grepTop   int
	grepCase  bool // exact case, the Aa toggle
	grepRegex bool // treat the query as a pattern, the .* toggle
	grepBusy  bool
	grepSeq   int
	grepStop  context.CancelFunc

	// Preview pane.
	file        *preview.File
	fileLine    int // 1-based caret line, drives the gutter highlight
	findIn      textinput.Model
	finding     bool
	findHits    int
	findMatches []findMatch
	findSel     int

	// Editing the previewed file. Nil unless the buffer is open.
	edit         *editor
	discardArmed bool // a second esc discards unsaved changes

	// Session list.
	sessSel int

	// Background commands.
	tasks    *task.Registry
	taskSel  int
	taskOpen string // id of the task whose output is being read

	// Project explorer.
	prevSeq int
	tree    *explorer.Tree
	treeSel int
	treeTop int

	// Tab completion for the prompt.
	compSeq  int
	comp     complete.Result
	compOpen bool
	compSel  int

	// Images pasted from the clipboard, waiting to go out with the next
	// prompt. They belong to the prompt being typed rather than to a session,
	// which is also true of the prompt itself.
	attach []session.Attachment

	// Prompt history, oldest first, shared across sessions like a shell's.
	history   []string
	histIdx   int    // len(history) means "not recalling"
	histDraft string // what was typed before recall started

	// Engine picker.
	engineSel  int
	modelSel   int
	lastEngine string // engine a new session inherits

	// History browser, nil until /git opens it.
	git *gitState

	// Filesystem target picker.
	hostFS    vfs.FS
	fsCache   map[string]vfs.FS
	targets   []target
	targetSel int

	// In-flight agent turns, one per session. Sessions run concurrently, so
	// every piece of run state is keyed by session rather than held globally.
	runs      map[string]context.CancelFunc
	approvals []pendingApproval
	choices   []pendingChoice
	choiceSel int

	// deferred holds commands produced where none could be returned.
	deferred []tea.Cmd

	status  string
	errText string
	notice  string
	sessMsg string
}

// New builds the root model. Nothing blocking happens here; the file index is
// kicked off from Init so the first frame paints immediately.
func New(cfg config.Config, st *theme.Styles, idx *fsx.Index, ld *preview.Loader,
	mgr *session.Manager, reg *engine.Registry, tasks *task.Registry) *Model {

	ta := textarea.New()
	ta.Placeholder = "Ask anything, or press ctrl+p to open a file"
	ta.ShowLineNumbers = false
	ta.Prompt = "❯ "
	ta.CharLimit = 0
	ta.MaxHeight = 12
	ta.SetHeight(3)
	ta.Focus()

	ta.SetStyles(textareaStyles(st))
	// Use the terminal's own cursor rather than a drawn one. An input method —
	// Vietnamese Telex, Pinyin, Kana — composes at the real cursor, and with it
	// hidden the terminal has no insertion point, so keystrokes arrive raw and
	// uncomposed.
	ta.SetVirtualCursor(false)

	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(st.Accent))

	inStyles := textinputStyles(st)
	mk := func(ph string) textinput.Model {
		ti := textinput.New()
		ti.Placeholder = ph
		ti.Prompt = ""
		ti.SetStyles(inStyles)
		ti.SetVirtualCursor(false)
		return ti
	}

	m := &Model{
		cfg: cfg, st: st, idx: idx, loader: ld, mgr: mgr, reg: reg, tasks: tasks,
		sc:           ld.Scheme(),
		showSessions: true, showPreview: true,
		chat:     viewport.New(),
		prev:     viewport.New(),
		input:    ta,
		spin:     sp,
		renameIn: mk("name this session…"),
		finderIn: mk("fuzzy file name…"),
		grepIn:   mk("search file contents…"),
		findIn:   mk("find in file…"),
		status:   "indexing…",
		history:  loadHistory(),
	}
	// The layout is the one left behind last time. A pane you closed stays
	// closed, and a width you set stays set: both are decisions, and asking
	// for them again every morning is not a default, it is an interruption.
	lay := loadLayout()
	m.sideSet, m.prevSet = lay.Side, lay.Preview
	m.showSessions, m.showPreview = !lay.Hide, !lay.HidePrv
	m.histIdx = len(m.history)
	m.chat.SoftWrap = true
	m.prev.SoftWrap = false
	m.prev.LeftGutterFunc = m.gutter
	m.prev.HighlightStyle = st.Match
	m.prev.SelectedHighlightStyle = st.MatchOn
	m.hostFS = vfs.NewLocal(idx.Root())
	m.fsCache = map[string]vfs.FS{m.hostFS.ID(): m.hostFS}
	m.tree = explorer.New(m.hostFS, mgr.Active().CWD)
	if d := reg.Default(); d != nil {
		m.lastEngine = d.ID()
	}
	return m
}

// engineFor resolves the engine a session should run on, remembering the choice
// so the next new session inherits it.
func (m *Model) engineFor(s *session.Session) agent.Engine {
	e := m.reg.Get(s.Engine)
	if e != nil {
		s.Engine = e.ID()
	}
	return e
}

// engineIndex locates an engine in the picker list.
func (m *Model) engineIndex(id string) int {
	for i, e := range m.reg.All() {
		if e.ID() == id {
			return i
		}
	}
	return 0
}

// indexReadyMsg is emitted once the background walk finishes.
type indexReadyMsg struct {
	n    int
	took time.Duration
}

// pendingApproval is one tool call waiting on the user, tagged with the session
// that asked. Approvals queue: a second session's prompt waits its turn.
type pendingApproval struct {
	sess *session.Session
	ev   agent.EvApproval
}

// pendingChoice is a question the agent put to the user. It queues the same way
// an approval does, and for the same reason: the agent is blocked until it is
// answered, so the session it came from is brought to the front.
type pendingChoice struct {
	sess *session.Session
	ev   agent.EvChoice
}

// agentMsg carries one event, tagged with the session and channel it came from
// so a turn is never applied to whichever session happens to be active.
type agentMsg struct {
	sess *session.Session
	ch   chan agent.Event
	ev   agent.Event
	// eng is the engine that produced this event, which is not always the
	// session's engine any more: a turn can finish after the session has been
	// handed to another one, and an id or a reasoning context belongs to
	// whoever made it rather than to whoever holds the session now.
	eng string
}

// agentGoneMsg means one session's agent channel closed.
type agentGoneMsg struct{ sess *session.Session }

// grepMsg carries a finished project search. seq guards against a stale scan
// landing after the user has typed something newer.
type grepMsg struct {
	seq int
	res search.Result
}

// fileMsg carries a loaded preview file, optionally scrolled to a line.
//
// seq discards results that arrive after the selection has already moved on,
// which is what keeps arrow-key browsing from flickering between files.
type fileMsg struct {
	seq  int
	f    *preview.File
	line int
	// focus says whether loading this file should move focus into the preview.
	// Opening a file deliberately does; browsing past one in the tree must not,
	// or the next arrow key scrolls the file instead of moving the selection.
	focus bool
}

// tickMsg drives the "still working" spinner without a busy loop.
type tickMsg time.Time

// completionMsg carries the result of a Tab. It arrives as a message because
// listing a directory inside a container is a round trip.
type completionMsg struct {
	seq  int
	line string
	res  complete.Result
	// auto marks a lookup nobody asked for: the menu that opens while a file
	// reference is being typed. It may offer, but it may not type for you.
	auto bool
}

// promptCursor is the rune index of the caret within the prompt's current line.
func (m *Model) promptCursor() int {
	li := m.input.LineInfo()
	return li.StartColumn + li.ColumnOffset
}

// refToken returns the file reference the caret sits in, if it is in one.
//
// A reference is completed as it is typed rather than only on Tab: @ is a
// gesture borrowed from chat clients, where the list appears as soon as you
// press the key, and a marker that did nothing until you also pressed Tab
// would be a worse version of Tab.
func (m *Model) refToken() (complete.Token, bool) {
	tok := complete.TokenAt(m.input.Value(), m.promptCursor())
	return tok, complete.IsRef(tok.Text)
}

// completeCmd looks up completions for whatever the caret is sitting on.
func (m *Model) completeCmd(auto bool) tea.Cmd {
	line := m.input.Value()
	cursor := m.promptCursor()
	tok := complete.TokenAt(line, cursor)

	// A leading slash is a command, not a path.
	if tok.Start == 0 && strings.HasPrefix(tok.Text, "/") {
		m.compSeq++
		seq := m.compSeq
		res := complete.Names(slashNames(), tok)
		return func() tea.Msg { return completionMsg{seq: seq, line: line, res: res, auto: auto} }
	}

	kind := complete.KindFor(line)
	s := m.mgr.Active()
	fsys := m.sessionFS(s)
	root := m.sessionCWD(s)
	home := ""
	if fsys.IsLocal() {
		home, _ = os.UserHomeDir()
	}

	m.compSeq++
	seq := m.compSeq

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return completionMsg{seq: seq, line: line, auto: auto,
			res: complete.Paths(ctx, fsys, root, home, tok, kind)}
	}
}

// applyCompletion puts text in place of the token and moves the caret after it.
//
// The token's extent is updated to cover what was just inserted, so cycling
// through the menu keeps replacing the same word instead of the stale range the
// first Tab was computed against.
func (m *Model) applyCompletion(text string) {
	line, cursor := complete.Apply(m.input.Value(), m.comp.Token, text)
	m.comp.Token.Text = text
	m.comp.Token.End = m.comp.Token.Start + len([]rune(text))
	m.input.SetValue(line)
	m.input.SetCursorColumn(cursor)
}

// closeCompletion dismisses the menu.
func (m *Model) closeCompletion() { m.compOpen = false; m.comp = complete.Result{} }

// pushHistory records a submitted prompt, skipping an immediate repeat.
func (m *Model) pushHistory(line string) {
	if n := len(m.history); n == 0 || m.history[n-1] != line {
		m.history = append(m.history, line)
		m.saveHistory()
	}
	m.histIdx = len(m.history)
	m.histDraft = ""
}

// recallHistory walks the history, keeping the in-progress line so stepping
// back down returns to it.
func (m *Model) recallHistory(delta int) bool {
	if len(m.history) == 0 {
		return false
	}
	if m.histIdx == len(m.history) {
		m.histDraft = m.input.Value()
	}
	idx := m.histIdx + delta
	switch {
	case idx < 0:
		idx = 0
	case idx >= len(m.history):
		m.histIdx = len(m.history)
		m.input.SetValue(m.histDraft)
		m.input.CursorEnd()
		return true
	}
	m.histIdx = idx
	m.input.SetValue(m.history[idx])
	m.input.CursorEnd()
	return true
}

func (m *Model) Init() tea.Cmd {
	m.tree.Watch()
	// The active session may have been restored inside a container or a
	// distribution, which the tree was built on the host not knowing.
	return tea.Batch(
		m.showActiveSession(),
		m.spin.Tick,
		m.watchTree(),
		m.watchTasks(),
	)
}

// treeMsg says the file tree changed on disk.
type treeMsg struct{}

// taskMsg says a background command produced output or changed state.
type taskMsg struct{}

// watchTasks repaints when background work moves. It blocks on the registry
// rather than polling, so an idle session costs nothing.
func (m *Model) watchTasks() tea.Cmd {
	tasks := m.tasks
	return func() tea.Msg {
		<-tasks.Changed()
		return taskMsg{}
	}
}

// watchTree keeps the explorer current. Filesystem events shorten the wait
// where they work; the timeout is what makes it work everywhere else, which is
// the whole story on Docker bind mounts and WSL drives.
func (m *Model) watchTree() tea.Cmd {
	tree := m.tree
	return func() tea.Msg {
		select {
		case <-tree.Changes():
		case <-time.After(tree.PollInterval()):
		}
		tree.Refresh()
		return treeMsg{}
	}
}

func (m *Model) buildIndex() tea.Cmd {
	return func() tea.Msg {
		m.idx.Build()
		return indexReadyMsg{n: m.idx.Len(), took: m.idx.Took()}
	}
}

// loadFile reads and highlights a file off the update loop.
func (m *Model) loadFile(rel string, line int, takeFocus bool) tea.Cmd {
	return m.loadFileAt(m.idx.Abs(rel), rel, line, takeFocus)
}

// pump delivers the next event from one session's agent, re-arming itself each
// time. The session travels with the message, so concurrent runs stay separate.
func (m *Model) pump(s *session.Session, eng string, ch chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return agentGoneMsg{sess: s}
		}
		return agentMsg{sess: s, ch: ch, ev: ev, eng: eng}
	}
}

func (m *Model) runGrep(q string) tea.Cmd {
	if m.grepStop != nil {
		m.grepStop()
	}
	m.grepSeq++
	seq := m.grepSeq
	ctx, cancel := context.WithCancel(context.Background())
	m.grepStop = cancel
	files := m.idx.Files()
	root := m.idx.Root()
	workers := m.cfg.Workers
	fsys := m.sessionFS(m.mgr.Active())
	regex, exact := m.grepRegex, m.grepCase

	return func() tea.Msg {
		defer cancel()
		hits, truncated, err := fsys.Grep(ctx, root, vfs.GrepOptions{
			Query: q, Regex: regex, CaseSensitive: exact,
			Limit: 2000, Workers: workers,
			MaxFileBytes: 4 << 20, Files: files,
		})
		res := search.Result{Truncated: truncated, Err: err}
		seen := map[string]bool{}
		for _, h := range hits {
			seen[h.Path] = true
			res.Matches = append(res.Matches, search.Match{
				Path: h.Path, Line: h.Line, Text: h.Text,
				Start: h.Start, End: h.End,
			})
		}
		res.Files = len(seen)
		return grepMsg{seq: seq, res: res}
	}
}

// send starts an agent turn for the active session. Other sessions keep running
// their own turns unaffected.
// send starts a turn in whichever conversation the prompt is pointed at: the
// active session, or the side chat beside it when the caret is in that pane.
func (m *Model) send(text string) tea.Cmd {
	return m.sendTo(m.promptTarget(), text)
}

// sendTo starts a turn in one session. Sessions run concurrently, so which one
// this is matters and "the active one" is not always the answer.
func (m *Model) sendTo(s *session.Session, text string) tea.Cmd {
	if s == nil {
		return nil
	}
	if s.Busy {
		return nil
	}
	files := m.takeAttachments(fsID(m.sessionFS(s)))
	s.Append(session.Message{Role: session.RoleUser, Text: text, Files: files})
	s.Busy = true
	s.Status = "thinking"
	s.Started = time.Now()
	s.LastErr = ""
	s.Partial = ""
	m.errText = ""
	m.mgr.Save(s)

	eng := m.engineFor(s)
	if eng == nil {
		s.Busy = false
		m.errText = "no engine available"
		return nil
	}
	m.lastEngine = eng.ID()
	fsys := m.sessionFS(s)
	if setter, ok := eng.(interface{ SetFS(vfs.FS) }); ok {
		setter.SetFS(fsys)
	}
	st := s.StateFor(eng.ID())
	turn := agent.Turn{
		Prompt: text,
		// Brief is the gap between what this engine has seen and where the
		// conversation now is. The built-in engine never reads it — it is
		// handed the transcript itself — so only a CLI pays for one.
		Brief:      m.handoffBrief(s, eng.ID()),
		History:    append([]session.Message(nil), s.Messages...),
		State:      s.Live,
		ExternalID: st.ExternalID,
		Fork:       s.ForkPending,
		Root:       m.sessionCWD(s),
		Mode:       sessionMode(s),
		Model:      m.sessionModel(s),
		FS:         fsys,
		Files:      files,
	}

	ctx, cancel := context.WithCancel(context.Background())
	if m.runs == nil {
		m.runs = make(map[string]context.CancelFunc, 4)
	}
	m.runs[s.ID] = cancel

	ch := make(chan agent.Event, 64)
	go eng.Run(ctx, turn, ch)

	m.invalidateChat()
	return tea.Batch(m.pump(s, eng.ID(), ch), m.spin.Tick, tick())
}

func tick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) invalidateChat() { m.chatKey = "" }

// cancelRun stops the active session's turn and drops any approval it was
// waiting on. Runs in other sessions are left alone.
func (m *Model) cancelRun() {
	s := m.mgr.Active()
	if cancel, ok := m.runs[s.ID]; ok {
		cancel()
		delete(m.runs, s.ID)
	}
	m.dropApprovals(s)
	m.dropChoices(s)
	s.Busy = false
	s.Status = ""
	m.notice = "cancelled"
}

// dropApprovals denies and removes every queued approval for one session.
func (m *Model) dropApprovals(s *session.Session) {
	kept := m.approvals[:0]
	for _, p := range m.approvals {
		if p.sess == s {
			replyOnce(p.ev.Reply, agent.Deny)
			continue
		}
		kept = append(kept, p)
	}
	m.approvals = kept
	if len(m.approvals) == 0 && m.overlay == overlayApproval {
		m.overlay = overlayNone
	}
}

// dropChoices dismisses every queued question for one session.
func (m *Model) dropChoices(s *session.Session) {
	kept := m.choices[:0]
	for _, p := range m.choices {
		if p.sess == s {
			replyChoice(p.ev.Reply, -1)
			continue
		}
		kept = append(kept, p)
	}
	m.choices = kept
	if len(m.choices) == 0 && m.overlay == overlayChoice {
		m.overlay = overlayNone
	}
}

// replyOnce answers an approval without ever blocking; the channel is buffered
// and the agent reads it exactly once.
func replyOnce(ch chan agent.Verdict, v agent.Verdict) {
	select {
	case ch <- v:
	default:
	}
}

// replyChoice answers a question the same way.
func replyChoice(ch chan int, pick int) {
	select {
	case ch <- pick:
	default:
	}
}

// gutter renders the preview's line-number column. It is called per visible
// line, so it stays allocation-light and fixed-width.
func (m *Model) gutter(info viewport.GutterContext) string {
	width := m.gutterWidth()
	if info.Soft {
		return m.st.Gutter.Render(strings.Repeat(" ", width) + "│ ")
	}
	if info.Index >= info.TotalLines {
		return m.st.Gutter.Render(pad("~", width) + "│ ")
	}
	n := info.Index + 1
	style := m.st.Gutter
	if n == m.fileLine {
		style = m.st.GutterOn
	}
	return style.Render(pad(itoa(n), width) + "│ ")
}

func (m *Model) gutterWidth() int {
	n := m.file.LineCount()
	w := 1
	for n >= 10 {
		n /= 10
		w++
	}
	return max(w, 3) + 1
}

func pad(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return strings.Repeat(" ", w-len(s)) + s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// target is one row of the filesystem picker.
//
// A WSL row carries distro instead of fs: resolving one means starting the
// distribution, which is too slow to do for every row the picker draws.
type target struct {
	id      string
	label   string
	detail  string
	workdir string
	fs      vfs.FS
	distro  string
}

// sessionFS resolves the filesystem a session works in, remembering resolved
// containers so switching between sessions does not re-shell out each time.
func (m *Model) sessionFS(s *session.Session) vfs.FS {
	id := s.Target
	if id == "" || id == "host" {
		return m.hostFS
	}
	if f, ok := m.fsCache[id]; ok {
		return f
	}
	f := vfs.Open(context.Background(), id, m.hostFS.DefaultDir())
	if f.ID() != id {
		// The container is gone; fall back to the host and say so rather than
		// leaving the session pointed at nothing.
		s.Target, s.CWD = "host", m.hostFS.DefaultDir()
		m.notice = "container for this session is gone; showing the host"
		return m.hostFS
	}
	m.fsCache[id] = f
	return f
}

// sessionMode is how much this session's agent may do. An unset mode is auto,
// which is what the zero value already means.
func sessionMode(s *session.Session) agent.Mode { return agent.ParseMode(s.Mode) }

// setMode records a mode and says what it now permits.
func (m *Model) setMode(mode agent.Mode) {
	s := m.mgr.Active()
	s.Mode = mode.String()
	m.mgr.Save(s)

	note := mode.Label() + " — " + mode.Detail()
	if mode.Confirms() {
		if e := m.reg.Get(s.Engine); e != nil && !e.CanAsk() {
			note = mode.Label() + " — but " + e.Label() + " cannot ask, so it stays confined instead"
		}
	}
	m.notice = note
}

// sessionCWD is the directory a session's agent runs in.
func (m *Model) sessionCWD(s *session.Session) string {
	if s.CWD != "" {
		return s.CWD
	}
	return m.idx.Root()
}

// loadFileAt previews an absolute path, labelled by rel. takeFocus moves the
// caret into the preview, which is what an explicit open wants and what
// browsing does not.
func (m *Model) loadFileAt(abs, rel string, line int, takeFocus bool) tea.Cmd {
	fsys := m.sessionFS(m.mgr.Active())
	m.prevSeq++
	seq := m.prevSeq
	return func() tea.Msg {
		return fileMsg{seq: seq, f: m.loader.Load(fsys, abs, rel), line: line, focus: takeFocus}
	}
}

// openEditor turns the preview into a writable buffer for the open file.
func (m *Model) openEditor() tea.Cmd {
	if m.file == nil || m.file.Err != nil {
		m.notice = "no file open"
		return nil
	}
	if m.file.Binary {
		m.notice = m.file.Rel + " is a binary file"
		return nil
	}
	if m.file.Truncated {
		m.notice = m.file.Rel + " was truncated for display; not safe to edit"
		return nil
	}

	body := strings.Join(m.file.Plain, "\n")
	if len(m.file.Plain) > 0 {
		body += "\n"
	}
	ed, why := newEditor(m.st, m.sessionFS(m.mgr.Active()),
		m.file.Abs, m.file.Rel, body, m.prevW-2, m.bodyH-2)
	if ed == nil {
		m.notice = why
		return nil
	}

	m.edit = ed
	m.showPreview = true
	m.setFocus(focusPreview)
	m.stopFind()
	m.resize(m.w, m.h)
	m.notice = "editing " + m.file.Rel + " — ctrl+s save · esc close"
	return nil
}

// closeEditor leaves the buffer, refusing to drop unsaved work silently.
func (m *Model) closeEditor(force bool) tea.Cmd {
	if m.edit == nil {
		return nil
	}
	if m.edit.Dirty() && !force {
		m.notice = "unsaved changes — ctrl+s to save, or esc again to discard"
		m.discardArmed = true
		return nil
	}
	rel, abs := m.edit.rel, m.edit.abs
	m.edit, m.discardArmed = nil, false
	m.setFocus(focusPreview)
	// Reload so the syntax highlighting comes back.
	return m.loadFileAt(abs, rel, m.fileLine, false)
}

// saveEditor writes the buffer and reloads the preview from what was written.
func (m *Model) saveEditor() tea.Cmd {
	if m.edit == nil {
		return nil
	}
	if err := m.edit.Save(); err != nil {
		m.errText = "could not save " + m.edit.rel + ": " + err.Error()
		return nil
	}
	m.errText = ""
	m.notice = "saved " + m.edit.rel
	m.discardArmed = false
	// The tree and the index may care that the file changed.
	return nil
}

// previewSelected shows whatever the explorer selection is pointing at.
//
// Selecting a file previews it straight away, the way a sidebar does: moving
// through a directory should show you what is in it, not make you confirm each
// step. Directories leave the current preview alone.
func (m *Model) previewSelected() tea.Cmd {
	rows := m.tree.Rows()
	if m.treeSel < 0 || m.treeSel >= len(rows) {
		return nil
	}
	node := rows[m.treeSel]
	if node.Dir || node.IsParent() {
		return nil
	}
	abs := vfs.Join(m.tree.Root(), node.Rel)
	if m.file != nil && m.file.Abs == abs {
		return nil // already showing
	}
	m.showPreview = true
	m.resize(m.w, m.h)
	return m.loadFileAt(abs, node.Rel, 1, false)
}

// projectName is the label shown in the header. It stays the project the app
// was opened on even when a session is working inside a container, so the
// header does not lose the thing it identifies.
func (m *Model) projectName() string {
	name := filepath.Base(m.hostFS.DefaultDir())
	if f := m.sessionFS(m.mgr.Active()); f != nil && !f.IsLocal() {
		name += "  ▸ " + f.Label()
	}
	return name
}

// promptTarget is the conversation the prompt is talking to.
//
// One prompt box serves both panes rather than two: a second text area would
// double the input handling — history, completion, attachments, paste — for a
// pane whose whole point is that it is a quick aside.
func (m *Model) promptTarget() *session.Session {
	if m.focus == focusBtw {
		if side := m.sideSession(); side != nil {
			return side
		}
	}
	return m.mgr.Active()
}
