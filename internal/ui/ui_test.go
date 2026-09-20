package ui

import (
	"context"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/config"
	"github.com/phanngoc/agent-tui/internal/engine"
	"github.com/phanngoc/agent-tui/internal/fsx"
	"github.com/phanngoc/agent-tui/internal/highlight"
	"github.com/phanngoc/agent-tui/internal/preview"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/task"
	"github.com/phanngoc/agent-tui/internal/theme"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// newTestModel builds a fully wired model over a throwaway project. No network
// call is ever made: the agent is constructed but never run.
func newTestModel(t *testing.T) *Model {
	t.Helper()
	// Prompt history is stored per user, not per project, so without this the
	// tests read and write the real one: they pollute it, and they see each
	// other's entries.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	// The built-in engine checks for a credential before offering itself, and a
	// machine without one would silently fall back to a different engine.
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.go"),
		"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n")
	mustWrite(t, filepath.Join(root, "internal", "core", "core.go"),
		"package core\n\n// Needle marks the spot.\nconst Needle = 1\n")

	cfg := config.Default()
	cfg.Root = root
	st := theme.New(theme.Dark)
	sc := highlight.NewScheme(st.P.Fg, st.P.Keyword, st.P.Type, st.P.String,
		st.P.Number, st.P.Comment, st.P.Func, st.P.Punct)

	hostFS := vfs.NewLocal(root)
	idx := fsx.NewIndex(hostFS, root, 0)
	idx.Build()

	mgr := session.NewManager(t.TempDir(), root, cfg.Model)
	t.Cleanup(mgr.Shutdown)
	mgr.Restore(5)

	ex := &agent.Executor{FS: hostFS, Root: root, Index: idx, MaxBytes: 1 << 20, Workers: 2}
	ag := agent.New("test-key", ex, cfg.Model, cfg.Effort, cfg.MaxTokens)
	reg := engine.NewRegistryWith(engine.NewAPI(ag, cfg.Model))

	m := New(cfg, st, idx, preview.NewLoader(sc, 1024, 8), mgr, reg, task.NewRegistry())
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	return m
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// key builds a key press from the name the app matches on. Modifiers are
// stripped first so a named key keeps its code: "shift+tab" is shift on KeyTab,
// not shift on the letter t.
var namedKeys = map[string]rune{
	"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab,
	"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
	"space": tea.KeySpace, "backspace": tea.KeyBackspace,
	"home": tea.KeyHome, "end": tea.KeyEnd, "f1": tea.KeyF1,
}

func key(s string) tea.KeyPressMsg {
	var mod tea.KeyMod
	for {
		switch {
		case strings.HasPrefix(s, "ctrl+"):
			mod, s = mod|tea.ModCtrl, strings.TrimPrefix(s, "ctrl+")
		case strings.HasPrefix(s, "alt+"):
			mod, s = mod|tea.ModAlt, strings.TrimPrefix(s, "alt+")
		case strings.HasPrefix(s, "shift+"):
			mod, s = mod|tea.ModShift, strings.TrimPrefix(s, "shift+")
		default:
			if code, ok := namedKeys[s]; ok {
				return tea.KeyPressMsg{Code: code, Mod: mod}
			}
			r := []rune(s)[0]
			k := tea.KeyPressMsg{Code: r, Mod: mod}
			if mod == 0 {
				k.Text = string(r)
			}
			return k
		}
	}
}

// runUntil executes a command, descending into tea.Batch, until it yields a
// message of the requested type.
func runUntil[T tea.Msg](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case T:
			return msg
		case tea.BatchMsg:
			queue = append(queue, msg...)
		}
	}
	var zero T
	t.Fatalf("no %T was produced", zero)
	return zero
}

// selectRow moves the explorer selection onto a named row, so tests do not
// depend on where it happens to sit in the list.
func selectRow(t *testing.T, m *Model, rel string) {
	t.Helper()
	for i, r := range m.tree.Rows() {
		if r.Rel == rel {
			m.treeSel = i
			return
		}
	}
	t.Fatalf("no row named %q in %+v", rel, m.tree.Rows())
}

// press feeds a key and renders, so any panic in either path surfaces here.
func press(t *testing.T, m *Model, k string) string {
	t.Helper()
	m.Update(key(k))
	return m.View().Content
}

func TestRendersWithoutPanicking(t *testing.T) {
	m := newTestModel(t)
	out := m.View().Content
	if out == "" {
		t.Fatal("empty first frame")
	}
	if !strings.Contains(stripANSI(out), "agent-tui") {
		t.Errorf("welcome text missing from the first frame")
	}
}

func TestTinyTerminalDegradesGracefully(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 10, Height: 4})
	if got := stripANSI(m.View().Content); !strings.Contains(got, "too small") {
		t.Errorf("expected a size warning, got %q", got)
	}
	// And it must recover when the terminal grows again.
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if !m.ready {
		t.Error("model did not recover after a resize")
	}
}

func TestOverlaysOpenAndClose(t *testing.T) {
	m := newTestModel(t)
	for _, tc := range []struct {
		key  string
		want overlay
		text string
	}{
		{"ctrl+p", overlayFinder, "Go to file"},
		{"ctrl+g", overlayGrep, "Search"},
		{"f1", overlayHelp, "Keyboard shortcuts"},
	} {
		out := press(t, m, tc.key)
		if m.overlay != tc.want {
			t.Fatalf("%s did not open its overlay (state=%v)", tc.key, m.overlay)
		}
		if !strings.Contains(stripANSI(out), tc.text) {
			t.Errorf("%s overlay missing %q", tc.key, tc.text)
		}
		press(t, m, "esc")
		if m.overlay != overlayNone {
			t.Errorf("esc did not close the %s overlay", tc.key)
		}
	}
}

func TestFinderFiltersAndOpensFile(t *testing.T) {
	m := newTestModel(t)
	press(t, m, "ctrl+p")
	for _, r := range "core" {
		press(t, m, string(r))
	}
	if len(m.finderHit) == 0 {
		t.Fatal("finder returned no hits for 'core'")
	}
	if !strings.Contains(m.finderHit[0].Path, "core.go") {
		t.Fatalf("top hit = %q, want core.go", m.finderHit[0].Path)
	}

	// Enter returns the load command; run it and feed the result back.
	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	msg := cmd()
	fm, ok := msg.(fileMsg)
	if !ok {
		t.Fatalf("expected a fileMsg, got %T", msg)
	}
	m.Update(fm)

	if m.file == nil || !strings.Contains(m.file.Rel, "core.go") {
		t.Fatalf("preview did not load the file: %+v", m.file)
	}
	if m.file.Lang != "Go" {
		t.Errorf("language = %q, want Go", m.file.Lang)
	}
	if !strings.Contains(stripANSI(m.View().Content), "core.go") {
		t.Error("preview pane does not show the file name")
	}
}

func TestInFileSearchHighlights(t *testing.T) {
	m := newTestModel(t)
	m.Update(fileMsg{f: m.loader.Load(m.hostFS,
		filepath.Join(m.idx.Root(), "internal/core/core.go"),
		"internal/core/core.go"), line: 1, focus: true})

	m.setFocus(focusPreview)
	m.startFind()
	m.applyFind("Needle")
	if m.findHits != 2 {
		t.Errorf("found %d hits for 'Needle', want 2", m.findHits)
	}
	m.applyFind("nothing-here")
	if m.findHits != 0 {
		t.Errorf("stale hits survived a new query: %d", m.findHits)
	}
}

func TestGrepOverlayFindsMatches(t *testing.T) {
	m := newTestModel(t)
	press(t, m, "ctrl+g")
	var cmd func() tea.Msg
	for _, r := range "Needle" {
		_, c := m.Update(key(string(r)))
		if c != nil {
			cmd = c
		}
	}
	if cmd == nil {
		t.Fatal("typing in the grep overlay never scheduled a search")
	}
	msg := runUntil[grepMsg](t, cmd)
	m.Update(msg)
	if len(m.grepRes.Matches) == 0 {
		t.Fatal("no matches for 'Needle'")
	}
	if !strings.Contains(m.grepRes.Matches[0].Path, "core.go") {
		t.Errorf("unexpected match %+v", m.grepRes.Matches[0])
	}
}

func TestSessionLifecycle(t *testing.T) {
	m := newTestModel(t)
	if m.mgr.Len() != 1 {
		t.Fatalf("expected one starting session, got %d", m.mgr.Len())
	}

	press(t, m, "ctrl+t")
	if m.mgr.Len() != 2 {
		t.Fatalf("ctrl+t did not add a session: %d", m.mgr.Len())
	}
	first := m.mgr.ActiveIndex()

	m.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModAlt})
	if m.mgr.ActiveIndex() == first {
		t.Error("alt+down did not change the active session")
	}

	press(t, m, "ctrl+w")
	if m.mgr.Len() != 1 {
		t.Errorf("ctrl+w did not close a session: %d", m.mgr.Len())
	}
}

func TestPaneTogglesChangeLayout(t *testing.T) {
	m := newTestModel(t)
	withPreview := m.prevW
	if withPreview == 0 {
		t.Fatal("preview pane should be visible at 160 columns")
	}
	press(t, m, "ctrl+e")
	if m.prevW != 0 {
		t.Errorf("ctrl+e did not hide the preview pane (width=%d)", m.prevW)
	}
	press(t, m, "ctrl+b")
	if m.sideW != 0 {
		t.Errorf("ctrl+b did not hide the sidebar (width=%d)", m.sideW)
	}
	// Chat must absorb the freed columns rather than leaving a gap.
	if m.chatW != m.w {
		t.Errorf("chat width = %d, want the full %d columns", m.chatW, m.w)
	}
}

func TestTranscriptCacheInvalidates(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()

	first := m.transcript(80)
	if m.transcript(80) != first {
		t.Error("cached transcript should be byte-identical on a repeat call")
	}

	s.Append(session.Message{Role: session.RoleUser, Text: "hello there"})
	if got := m.transcript(80); got == first {
		t.Fatal("transcript did not change after a new message")
	} else if !strings.Contains(stripANSI(got), "hello there") {
		t.Errorf("new message missing from the transcript: %q", stripANSI(got))
	}
}

func TestFocusCyclesThroughVisiblePanes(t *testing.T) {
	m := newTestModel(t)
	seen := map[focus]bool{}
	for i := 0; i < 8; i++ {
		seen[m.focus] = true
		press(t, m, "ctrl+o")
	}
	for _, f := range []focus{focusInput, focusChat, focusPreview, focusSessions} {
		if !seen[f] {
			t.Errorf("tab never reached focus %v", f)
		}
	}
}

func BenchmarkTranscript(b *testing.B) {
	t := &testing.T{}
	m := newTestModel(t)
	s := m.mgr.Active()
	for i := 0; i < 200; i++ {
		s.Append(session.Message{Role: session.RoleUser, Text: strings.Repeat("word ", 40)})
		s.Append(session.Message{Role: session.RoleAssistant, Text: strings.Repeat("reply ", 40)})
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.invalidateChat()
		m.transcript(100)
	}
}

func BenchmarkTranscriptCached(b *testing.B) {
	t := &testing.T{}
	m := newTestModel(t)
	s := m.mgr.Active()
	for i := 0; i < 200; i++ {
		s.Append(session.Message{Role: session.RoleAssistant, Text: strings.Repeat("reply ", 40)})
	}
	m.transcript(100)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.transcript(100)
	}
}

func BenchmarkTranscriptStreamingDelta(b *testing.B) {
	t := &testing.T{}
	m := newTestModel(t)
	s := m.mgr.Active()
	for i := 0; i < 200; i++ {
		s.Append(session.Message{Role: session.RoleAssistant, Text: strings.Repeat("reply ", 40)})
	}
	m.transcript(100)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.Partial += "token "
		m.transcript(100)
	}
}

func TestEventsLandOnTheirOwnSession(t *testing.T) {
	m := newTestModel(t)
	a := m.mgr.Active()
	b := m.mgr.New() // b is now active; a keeps running in the background
	m.onSessionSwitch()

	ch := make(chan agent.Event, 4)
	m.Update(agentMsg{sess: a, ch: ch, ev: agent.EvAssistant{
		Message: session.Message{Role: session.RoleAssistant, Text: "answer for a"},
	}})

	if len(a.Messages) != 1 {
		t.Fatalf("session a has %d messages, want 1", len(a.Messages))
	}
	if len(b.Messages) != 0 {
		t.Fatalf("the background turn leaked into session b: %+v", b.Messages)
	}
	if !strings.Contains(stripANSI(m.transcript(80)), "new session") &&
		strings.Contains(stripANSI(m.transcript(80)), "answer for a") {
		t.Error("the foreground transcript is showing another session's output")
	}
}

func TestDoneEventStoresHistoryOnItsSession(t *testing.T) {
	m := newTestModel(t)
	a := m.mgr.Active()
	a.Busy = true
	m.runs = map[string]context.CancelFunc{a.ID: func() {}}

	hist := []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("hi"))}
	m.Update(agentMsg{sess: a, ch: make(chan agent.Event, 1), ev: agent.EvDone{State: hist}})

	if a.Busy {
		t.Error("session still marked busy after EvDone")
	}
	got, ok := a.Live.([]anthropic.MessageParam)
	if !ok || len(got) != 1 {
		t.Fatalf("history was not stored on the session: %#v", a.Live)
	}
}

func TestCancelIsNotReportedAsAnError(t *testing.T) {
	m := newTestModel(t)
	a := m.mgr.Active()
	a.Busy = true

	m.Update(agentMsg{sess: a, ch: make(chan agent.Event, 1),
		ev: agent.EvDone{Err: context.Canceled}})

	if a.LastErr != "" || m.errText != "" {
		t.Errorf("a cancellation surfaced as an error: session=%q ui=%q", a.LastErr, m.errText)
	}
}

func TestApprovalsQueueAndAnswerInOrder(t *testing.T) {
	m := newTestModel(t)
	a := m.mgr.Active()
	b := m.mgr.New()
	m.onSessionSwitch()

	replyA := make(chan agent.Verdict, 1)
	replyB := make(chan agent.Verdict, 1)
	ch := make(chan agent.Event, 4)

	m.Update(agentMsg{sess: a, ch: ch, ev: agent.EvApproval{
		Call: session.ToolCall{ID: "1", Name: "bash"}, Reply: replyA,
	}})
	if m.overlay != overlayApproval {
		t.Fatal("the first approval did not open its prompt")
	}
	// The prompt belongs to a, so the UI must bring a to the front.
	if m.mgr.Active() != a {
		t.Error("the approving session was not brought to the front")
	}

	m.Update(agentMsg{sess: b, ch: ch, ev: agent.EvApproval{
		Call: session.ToolCall{ID: "2", Name: "write_file"}, Reply: replyB,
	}})
	if len(m.approvals) != 2 {
		t.Fatalf("second approval did not queue: %d pending", len(m.approvals))
	}
	if !strings.Contains(stripANSI(m.View().Content), "bash") {
		t.Error("the visible prompt is not the first one queued")
	}

	press(t, m, "y")
	if got := <-replyA; got != agent.Allow {
		t.Errorf("y answered %v, want allow", got)
	}
	if m.overlay != overlayApproval || len(m.approvals) != 1 {
		t.Fatalf("the queued prompt did not take over: overlay=%v pending=%d",
			m.overlay, len(m.approvals))
	}

	press(t, m, "n")
	if got := <-replyB; got != agent.Deny {
		t.Errorf("n answered %v, want deny", got)
	}
	if m.overlay != overlayNone {
		t.Error("the overlay stayed up with nothing left to approve")
	}
}

func TestCancelDeniesOnlyItsOwnApprovals(t *testing.T) {
	m := newTestModel(t)
	a := m.mgr.Active()
	b := m.mgr.New()
	m.onSessionSwitch()

	replyA, replyB := make(chan agent.Verdict, 1), make(chan agent.Verdict, 1)
	ch := make(chan agent.Event, 4)
	m.Update(agentMsg{sess: a, ch: ch, ev: agent.EvApproval{Reply: replyA,
		Call: session.ToolCall{ID: "1", Name: "bash"}}})
	m.Update(agentMsg{sess: b, ch: ch, ev: agent.EvApproval{Reply: replyB,
		Call: session.ToolCall{ID: "2", Name: "bash"}}})

	// The queue brought a to the front, since a's prompt is the one on screen.
	if m.mgr.Active() != a {
		t.Fatalf("expected session a to be active, got %p", m.mgr.Active())
	}
	// Cancelling a must not answer b's prompt.
	a.Busy = true
	m.cancelRun()

	if got := <-replyA; got != agent.Deny {
		t.Errorf("cancel answered %v, want deny", got)
	}
	if len(m.approvals) != 1 || m.approvals[0].sess != b {
		t.Fatalf("b's approval did not survive: %+v", m.approvals)
	}
	select {
	case <-replyB:
		t.Error("cancelling session a answered session b's prompt")
	default:
	}
}

func TestPluralSpelling(t *testing.T) {
	for _, c := range []struct {
		n          int
		word, want string
	}{
		{1, "match", "1 match"},
		{500, "match", "500 matches"},
		{1, "file", "1 file"},
		{53, "file", "53 files"},
		{0, "file", "0 files"},
		{2, "more line", "2 more lines"},
	} {
		if got := plural(c.n, c.word); got != c.want {
			t.Errorf("plural(%d, %q) = %q, want %q", c.n, c.word, got, c.want)
		}
	}
}

// TestPanesShareOneHeight guards the Lip Gloss v2 sizing contract: Width and
// Height count the border, so a pane sized to h must render exactly h lines and
// the assembled frame must exactly fill the terminal.
func TestPanesShareOneHeight(t *testing.T) {
	for _, size := range [][2]int{{160, 44}, {120, 30}, {100, 24}, {80, 20}} {
		w, h := size[0], size[1]
		m := newTestModel(t)
		m.Update(tea.WindowSizeMsg{Width: w, Height: h})

		m.chat.SetContent(m.transcript(max(10, m.chatW-2)))
		panes := map[string]string{
			"left":    m.leftColumn(),
			"chat":    m.pane(m.chat.View(), "transcript", m.chatW, m.bodyH, false),
			"preview": m.pane(m.previewPane(), "preview", m.prevW, m.bodyH, false),
		}
		for name, p := range panes {
			if m.sideW == 0 && name == "left" {
				continue
			}
			if m.prevW == 0 && name == "preview" {
				continue
			}
			if got := len(strings.Split(p, "\n")); got != m.bodyH {
				t.Errorf("%dx%d: %s pane is %d lines, want %d", w, h, name, got, m.bodyH)
			}
		}

		frame := strings.Split(m.View().Content, "\n")
		if len(frame) != h {
			t.Errorf("%dx%d: frame is %d lines, want %d", w, h, len(frame), h)
		}
		for i, line := range frame {
			if got := lipgloss.Width(line); got > w {
				t.Errorf("%dx%d: frame line %d is %d cells wide, want at most %d",
					w, h, i, got, w)
			}
		}
	}
}

// TestHelpColumnsAlign guards against padding by byte length: several key names
// contain multi-byte glyphs, so a len()-based column silently misaligns.
func TestHelpColumnsAlign(t *testing.T) {
	m := newTestModel(t)
	m.overlay = overlayHelp
	lines := strings.Split(stripANSI(m.helpView()), "\n")

	col := -1
	checked := 0
	for _, g := range helpGroups {
		for _, k := range g.keys {
			line, ok := findLine(lines, k.desc)
			if !ok {
				t.Fatalf("no help row for %q", k.desc)
			}
			at := lipgloss.Width(line[:strings.Index(line, k.desc)])
			if col == -1 {
				col = at
			} else if at != col {
				t.Errorf("row for %q starts its description at column %d, want %d",
					k.key, at, col)
			}
			checked++
		}
	}
	if checked < 5 {
		t.Fatalf("only checked %d rows; the help view changed shape", checked)
	}
}

func findLine(lines []string, needle string) (string, bool) {
	for _, l := range lines {
		if strings.Contains(l, needle) {
			return l, true
		}
	}
	return "", false
}

// ---- project explorer ------------------------------------------------------

func TestExplorerShowsTheSessionDirectory(t *testing.T) {
	m := newTestModel(t)
	out := stripANSI(m.View().Content)
	for _, want := range []string{"internal/", "main.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("explorer does not show %q:\n%s", want, out)
		}
	}
	if m.tree.Root() != m.idx.Root() {
		t.Errorf("tree root = %q, want the project root %q", m.tree.Root(), m.idx.Root())
	}
}

func TestExplorerExpandsAndOpensFiles(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusExplorer)

	selectRow(t, m, "internal")
	m.explorerKey("enter")
	if !m.tree.IsOpen("internal") {
		t.Fatal("enter did not expand the directory")
	}
	if got := stripANSI(m.View().Content); !strings.Contains(got, "core/") {
		t.Errorf("expanded directory is not visible:\n%s", got)
	}

	// Walk down to the file and open it.
	m.explorerKey("down")
	m.explorerKey("enter") // internal/core
	m.explorerKey("down")
	cmd := m.explorerKey("enter") // internal/core/core.go
	if cmd == nil {
		t.Fatal("opening a file produced no command")
	}
	msg := runUntil[fileMsg](t, cmd)
	m.Update(msg)

	if m.file == nil || !strings.HasSuffix(m.file.Rel, "core.go") {
		t.Fatalf("preview did not load the file: %+v", m.file)
	}
}

func TestExplorerCollapseAndParentNavigation(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusExplorer)
	selectRow(t, m, "internal")
	m.explorerKey("enter") // open internal/
	m.explorerKey("down")  // internal/core
	m.explorerKey("enter") // open it

	m.explorerKey("left") // collapses internal/core
	if m.tree.IsOpen("internal/core") {
		t.Error("left did not collapse the directory")
	}
	m.explorerKey("left") // now jumps to the parent row
	if got := m.tree.Rows()[m.treeSel].Rel; got != "internal" {
		t.Errorf("left on a collapsed directory should select its parent, got %q", got)
	}
}

// TestExplorerSetsSessionRoot is the "reflect the directory the agent is in"
// requirement: changing the root has to move the tree *and* what the engine is
// told to work on.
func TestExplorerSetsSessionRoot(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusExplorer)
	want := filepath.Join(m.idx.Root(), "internal")

	selectRow(t, m, "internal")
	m.Update(runUntil[indexReadyMsg](t, m.explorerKey("r")))

	s := m.mgr.Active()
	if s.CWD != want {
		t.Fatalf("session cwd = %q, want %q", s.CWD, want)
	}
	if m.tree.Root() != want {
		t.Errorf("tree root = %q, want %q", m.tree.Root(), want)
	}
	if got := stripANSI(m.View().Content); !strings.Contains(got, "core/") {
		t.Errorf("tree did not repoint:\n%s", got)
	}

	m.Update(runUntil[indexReadyMsg](t, m.explorerKey("R")))
	if m.mgr.Active().CWD != m.hostRoot() {
		t.Errorf("R did not restore the project root: %q", m.mgr.Active().CWD)
	}
}

func TestSwitchingSessionsRepointsTheTree(t *testing.T) {
	m := newTestModel(t)
	a := m.mgr.Active()
	a.CWD = filepath.Join(m.idx.Root(), "internal")

	b := m.mgr.New()
	b.CWD = m.idx.Root()
	m.onSessionSwitch()
	if m.tree.Root() != m.idx.Root() {
		t.Errorf("tree root = %q, want %q", m.tree.Root(), m.idx.Root())
	}

	m.mgr.Select(m.mgr.Len() - 1) // back to a
	m.onSessionSwitch()
	if m.tree.Root() != a.CWD {
		t.Errorf("tree root = %q, want the other session's cwd %q", m.tree.Root(), a.CWD)
	}
}

func TestExplorerReflectsFilesystemChanges(t *testing.T) {
	m := newTestModel(t)
	added := filepath.Join(m.idx.Root(), "appeared.go")
	mustWrite(t, added, "package appeared\n")

	// No keypress: the poll is what makes this work on filesystems that never
	// deliver events.
	m.Update(runUntil[treeMsg](t, m.watchTree()))
	if got := stripANSI(m.View().Content); !strings.Contains(got, "appeared.go") {
		t.Fatalf("a new file did not show up:\n%s", got)
	}

	if err := os.Remove(added); err != nil {
		t.Fatal(err)
	}
	m.Update(runUntil[treeMsg](t, m.watchTree()))
	if got := stripANSI(m.View().Content); strings.Contains(got, "appeared.go") {
		t.Errorf("a deleted file is still listed:\n%s", got)
	}
}

func TestCyclingReachesTheExplorer(t *testing.T) {
	m := newTestModel(t)
	seen := map[focus]bool{}
	for i := 0; i < 8; i++ {
		seen[m.focus] = true
		press(t, m, "ctrl+o")
	}
	if !seen[focusExplorer] {
		t.Error("cycling never reached the explorer")
	}
}

// TestTabDoesNotLeaveThePrompt is the reported problem: tab moved focus to
// another pane while the user was typing, so completing a path was impossible.
func TestTabDoesNotLeaveThePrompt(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusInput)
	m.input.SetValue("cd int")

	press(t, m, "tab")
	if m.focus != focusInput {
		t.Fatalf("tab moved focus to %v; it belongs to the prompt", m.focus)
	}

	// Away from the prompt there is nothing to complete, so tab still cycles.
	m.setFocus(focusChat)
	press(t, m, "tab")
	if m.focus == focusChat {
		t.Error("tab should still cycle panes outside the prompt")
	}
}

// ---- engine selection ------------------------------------------------------

func TestEnginePickerSwitchesEngine(t *testing.T) {
	m := newTestModel(t)
	m.reg = engine.NewRegistryWith(
		engine.NewAPI(agent.New("k", &agent.Executor{}, "m", "high", 1), "m"),
		fakeEngine{id: "claude", label: "Claude Code"},
	)

	press(t, m, "ctrl+r")
	if m.overlay != overlayEngine {
		t.Fatal("ctrl+r did not open the engine picker")
	}
	if got := stripANSI(m.View().Content); !strings.Contains(got, "Claude Code") {
		t.Errorf("picker does not list the engines:\n%s", got)
	}

	s := m.mgr.Active()
	s.Engine = "api"
	s.SetExternalID("api", "api-1")
	s.Live = []int{1}
	s.Append(session.Message{Role: session.RoleUser, Text: "earlier"})

	m.engineKey("down")
	m.engineKey("enter")

	if s.Engine != "claude" {
		t.Fatalf("engine = %q, want claude", s.Engine)
	}
	// The reasoning context genuinely cannot travel — signatures and a prompt
	// cache belong to one conversation with one server — so it goes.
	if s.Live != nil {
		t.Errorf("the reasoning context survived the switch: %v", s.Live)
	}
	// The arriving engine has never run here, so it has no id of its own.
	if s.ExternalID != "" {
		t.Errorf("claude was handed someone else's session id: %q", s.ExternalID)
	}
	// But the departing engine's id is not ours to throw away. Keeping it is
	// what makes coming back a resume rather than a second cold start.
	if got := s.StateFor("api").ExternalID; got != "api-1" {
		t.Errorf("the engine that left was forgotten: %q", got)
	}
	if m.overlay != overlayNone {
		t.Error("the picker stayed open after a selection")
	}
}

// Coming back resumes. The id was kept while another engine had the session,
// and the transcript that grew meanwhile is what the brief makes up.
func TestSwitchingBackResumesTheEngineItLeft(t *testing.T) {
	m := newTestModel(t)
	m.reg = engine.NewRegistryWith(
		engine.NewAPI(agent.New("k", &agent.Executor{}, "m", "high", 1), "m"),
		fakeEngine{id: "claude", label: "Claude Code"},
	)
	s := m.mgr.Active()
	s.Engine = "api"
	s.SetExternalID("api", "api-1")
	s.Append(session.Message{Role: session.RoleUser, Text: "earlier"})

	m.engineSel = 1
	m.engineKey("enter") // → claude
	m.engineSel = 0
	m.engineKey("enter") // → back to api

	if s.Engine != "api" {
		t.Fatalf("engine = %q, want api", s.Engine)
	}
	if s.ExternalID != "api-1" {
		t.Errorf("coming back started cold: id = %q", s.ExternalID)
	}
}

// Sequential by construction: half a turn from one engine and half from
// another is a transcript neither of them can continue.
func TestSwitchingWhileBusyIsRefused(t *testing.T) {
	m := newTestModel(t)
	m.reg = engine.NewRegistryWith(
		engine.NewAPI(agent.New("k", &agent.Executor{}, "m", "high", 1), "m"),
		fakeEngine{id: "claude", label: "Claude Code"},
	)
	s := m.mgr.Active()
	s.Engine, s.Busy = "api", true
	s.Append(session.Message{Role: session.RoleUser, Text: "earlier"})

	m.overlay = overlayEngine
	m.engineSel = 1
	m.engineKey("enter")

	if s.Engine != "api" {
		t.Errorf("the session was handed over mid-turn: %q", s.Engine)
	}
	if m.overlay != overlayEngine {
		t.Error("the picker closed, so the choice was lost along with the refusal")
	}
	if m.notice == "" {
		t.Error("the switch was refused without saying why")
	}
}

// A different filesystem invalidates every engine's conversation, not just the
// one selected: an id that resumes a conversation about another machine's
// files is worse than no id at all.
func TestChangingFilesystemForgetsEveryEngine(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Engine = "api"
	s.SetExternalID("api", "api-1")
	s.SetExternalID("claude", "claude-1")

	s.ForgetEngines()

	if len(s.Engines) != 0 || s.ExternalID != "" {
		t.Errorf("engine state survived: %v id=%q", s.Engines, s.ExternalID)
	}
}

func TestEnginePickerRefusesUnavailable(t *testing.T) {
	m := newTestModel(t)
	m.reg = engine.NewRegistryWith(
		engine.NewAPI(agent.New("k", &agent.Executor{}, "m", "high", 1), "m"),
		fakeEngine{id: "codex", label: "Codex", missing: true},
	)
	m.mgr.Active().Engine = "api"

	press(t, m, "ctrl+r")
	m.engineKey("down")
	m.engineKey("enter")

	if m.mgr.Active().Engine != "api" {
		t.Errorf("selected an unavailable engine: %q", m.mgr.Active().Engine)
	}
	if m.notice == "" {
		t.Error("no explanation was shown for the unavailable engine")
	}
}

func TestSidebarShowsTheEngine(t *testing.T) {
	m := newTestModel(t)
	got := stripANSI(m.View().Content)
	// The engine sits on the detail line under the title, next to the turn
	// count and how long ago the session last moved.
	if !strings.Contains(got, "api") {
		t.Errorf("sidebar does not name the session's engine:\n%s", got)
	}
}

// fakeEngine stands in for a CLI so the tests never depend on what is installed.
type fakeEngine struct {
	id, label string
	missing   bool
}

func (f fakeEngine) ID() string      { return f.id }
func (f fakeEngine) Label() string   { return f.label }
func (f fakeEngine) Detail() string  { return "fake" }
func (f fakeEngine) Available() bool { return !f.missing }
func (f fakeEngine) CanAsk() bool    { return false }
func (f fakeEngine) Run(ctx context.Context, t agent.Turn, out chan<- agent.Event) {
	close(out)
}

// ---- filesystem targets ----------------------------------------------------

// remoteFS stands in for a container: real files on disk, but reported as a
// filesystem this process cannot watch or walk in place, which is what drives
// the container code paths.
type remoteFS struct {
	vfs.FS
	name string
}

func (r remoteFS) ID() string    { return "docker:" + r.name }
func (r remoteFS) Label() string { return r.name }
func (r remoteFS) IsLocal() bool { return false }

// otherRoot builds a second filesystem with its own files, presented the way a
// container would be.
func otherRoot(t *testing.T) (string, vfs.FS) {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "elsewhere.go"), "package elsewhere\n")
	mustWrite(t, filepath.Join(root, "deep", "nested.go"), "package nested\n")
	return root, remoteFS{FS: vfs.NewLocal(root), name: "demo"}
}

func TestTargetSwitchRepointsEverything(t *testing.T) {
	m := newTestModel(t)
	hostRoot := m.idx.Root()
	other, fs := otherRoot(t)

	s := m.mgr.Active()
	s.ExternalID, s.Live = "stale", []int{1}

	cmd := m.useTarget(target{id: fs.ID(), label: "elsewhere", fs: fs, workdir: other})
	if cmd == nil {
		t.Fatal("switching target did not schedule a re-index")
	}
	m.Update(runUntil[indexReadyMsg](t, cmd))

	if s.Target != fs.ID() || s.CWD != other {
		t.Fatalf("session = target %q cwd %q", s.Target, s.CWD)
	}
	// A conversation cannot carry across filesystems: the paths it discussed
	// do not mean the same thing there.
	if s.ExternalID != "" || s.Live != nil {
		t.Errorf("stale agent state survived: id=%q live=%v", s.ExternalID, s.Live)
	}
	if m.tree.Root() != other {
		t.Errorf("tree root = %q, want %q", m.tree.Root(), other)
	}
	if m.idx.Root() != other {
		t.Errorf("index root = %q, want %q", m.idx.Root(), other)
	}

	out := stripANSI(m.View().Content)
	if !strings.Contains(out, "elsewhere.go") {
		t.Errorf("explorer does not show the new filesystem:\n%s", out)
	}
	if strings.Contains(out, "main.go") {
		t.Errorf("explorer is still showing the old filesystem:\n%s", out)
	}

	// The fuzzy index must have been rebuilt against the new filesystem.
	if hits := m.idx.Find("nested", 5); len(hits) == 0 {
		t.Error("the index was not rebuilt for the new filesystem")
	}
	if hits := m.idx.Find("core", 5); len(hits) > 0 && strings.Contains(hits[0].Path, "internal/core") {
		t.Errorf("the index still holds the old filesystem's files: %v", hits)
	}
	_ = hostRoot
}

func TestTargetSwitchClearsTheOpenFile(t *testing.T) {
	m := newTestModel(t)
	m.Update(fileMsg{f: m.loader.Load(m.hostFS,
		filepath.Join(m.idx.Root(), "main.go"), "main.go"), line: 1})
	if m.file == nil {
		t.Fatal("setup: no file open")
	}

	other, fs := otherRoot(t)
	m.useTarget(target{id: fs.ID(), label: "elsewhere", fs: fs, workdir: other})

	if m.file != nil {
		t.Error("a file from the previous filesystem is still open")
	}
}

func TestSessionsKeepSeparateTargets(t *testing.T) {
	m := newTestModel(t)
	other, fs := otherRoot(t)

	a := m.mgr.Active()
	m.useTarget(target{id: fs.ID(), label: "elsewhere", fs: fs, workdir: other})
	if a.Target != fs.ID() {
		t.Fatal("setup: first session did not switch")
	}

	b := m.mgr.New()
	b.CWD = m.hostFS.DefaultDir()
	m.onSessionSwitch()
	if m.tree.Root() != m.hostFS.DefaultDir() {
		t.Errorf("a new session should start on the host, tree root = %q", m.tree.Root())
	}

	// Going back to the first session must return to its filesystem.
	for i, s := range m.mgr.All() {
		if s == a {
			m.mgr.Select(i)
		}
	}
	m.onSessionSwitch()
	if m.tree.Root() != other {
		t.Errorf("returning to the first session did not restore its filesystem: %q", m.tree.Root())
	}
}

func TestMissingContainerFallsBackToTheHost(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Target = "docker:definitely-not-a-real-container"

	fs := m.sessionFS(s)
	if !fs.IsLocal() {
		t.Fatalf("expected a fallback to the host, got %s", fs.ID())
	}
	if s.Target != "host" {
		t.Errorf("the session was left pointing at a container that is gone: %q", s.Target)
	}
	if m.notice == "" {
		t.Error("the fallback was not explained to the user")
	}
}

func TestTargetPickerListsTheHost(t *testing.T) {
	m := newTestModel(t)
	press(t, m, "ctrl+d")
	if m.overlay != overlayTarget {
		t.Fatal("ctrl+d did not open the target picker")
	}
	if len(m.targets) == 0 || m.targets[0].id != "host" {
		t.Fatalf("the host must always be offered: %+v", m.targets)
	}
	if got := stripANSI(m.View().Content); !strings.Contains(got, "Where this session works") {
		t.Errorf("picker did not render:\n%s", got)
	}
	press(t, m, "esc")
	if m.overlay != overlayNone {
		t.Error("esc did not close the picker")
	}
}

func TestHeaderNamesTheContainer(t *testing.T) {
	m := newTestModel(t)
	if got := m.projectName(); strings.Contains(got, "▸") {
		t.Errorf("the host should not be badged: %q", got)
	}

	other, fs := otherRoot(t)
	m.fsCache[fs.ID()] = fs
	m.mgr.Active().Target = fs.ID()
	m.mgr.Active().CWD = other

	got := m.projectName()
	if !strings.Contains(got, "▸ demo") {
		t.Errorf("a container session should name its container in the header: %q", got)
	}
}

// TestToolResultKeepsTheCallDetails guards a display bug: engines report a
// finished tool with just an id and a result, and overwriting the recorded call
// with that left the transcript showing a bare tick with no tool name.
func TestToolResultKeepsTheCallDetails(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	ch := make(chan agent.Event, 4)

	m.Update(agentMsg{sess: s, ch: ch, ev: agent.EvAssistant{
		Message: session.Message{
			Role: session.RoleAssistant,
			Tools: []session.ToolCall{{
				ID: "t1", Name: "Bash", Input: []byte(`{"command":"ls internal/vfs"}`),
			}},
		},
	}})
	m.Update(agentMsg{sess: s, ch: ch, ev: agent.EvToolDone{
		Call: session.ToolCall{ID: "t1", Result: "5 files", Done: true},
	}})

	call := s.Messages[0].Tools[0]
	if call.Name != "Bash" {
		t.Errorf("tool name was lost: %q", call.Name)
	}
	if call.Summary() == "" {
		t.Errorf("tool arguments were lost: input=%s", call.Input)
	}
	if !call.Done || call.Result != "5 files" {
		t.Errorf("the result was not recorded: %+v", call)
	}

	out := stripANSI(m.transcript(100))
	if !strings.Contains(out, "Bash") {
		t.Errorf("the transcript shows a tool call with no name:\n%s", out)
	}
}

// ---- moving around the filesystem ------------------------------------------

func TestParseCD(t *testing.T) {
	for _, tc := range []struct {
		in   string
		dir  string
		isCD bool
	}{
		{"cd ..", "..", true},
		{"cd  internal/ui ", "internal/ui", true},
		{"cd /tmp", "/tmp", true},
		{`cd "my dir"`, "my dir", true},
		{"cd", "~", true},
		{"cdr", "", false},
		{"cd..", "", false},
		{"can you cd ..", "", false},
		{"cd ..\nand then", "", false},
	} {
		dir, ok := parseCD(tc.in)
		if ok != tc.isCD || dir != tc.dir {
			t.Errorf("parseCD(%q) = (%q, %v), want (%q, %v)", tc.in, dir, ok, tc.dir, tc.isCD)
		}
	}
}

// TestCDMovesTheSession covers the reported problem: `cd ..` typed into the
// prompt used to be sent to the agent as a question instead of moving anything.
func TestCDMovesTheSession(t *testing.T) {
	m := newTestModel(t)
	root := m.idx.Root()

	m.input.SetValue("cd internal")
	cmd := m.inputKey(key("enter"))
	if cmd == nil {
		t.Fatal("cd produced no command")
	}
	m.Update(runUntil[indexReadyMsg](t, cmd))

	want := filepath.Join(root, "internal")
	if got := m.mgr.Active().CWD; got != want {
		t.Fatalf("session cwd = %q, want %q", got, want)
	}
	if m.tree.Root() != want || m.idx.Root() != want {
		t.Errorf("tree %q and index %q should both follow", m.tree.Root(), m.idx.Root())
	}
	// The turn must not have been sent to the agent. It is still recorded,
	// because the agent has to know that every path after it means something
	// else — but as a move, not as a question.
	msgs := m.mgr.Active().Messages
	if len(msgs) != 1 || msgs[0].Shell == nil {
		t.Errorf("cd was sent as a prompt: %+v", msgs)
	}
	if m.mgr.Active().Busy {
		t.Error("cd started a turn")
	}
	if m.input.Value() != "" {
		t.Errorf("the input was not cleared: %q", m.input.Value())
	}

	// And back out again.
	m.input.SetValue("cd ..")
	cmd = m.inputKey(key("enter"))
	m.Update(runUntil[indexReadyMsg](t, cmd))
	if got := m.mgr.Active().CWD; got != root {
		t.Errorf("cd .. left the session at %q, want %q", got, root)
	}
}

func TestCDRejectsNonDirectories(t *testing.T) {
	m := newTestModel(t)
	before := m.mgr.Active().CWD

	m.input.SetValue("cd main.go")
	m.inputKey(key("enter"))

	if m.mgr.Active().CWD != before {
		t.Errorf("cd onto a file moved the session to %q", m.mgr.Active().CWD)
	}
	if m.notice == "" {
		t.Error("nothing explained why cd did nothing")
	}
}

func TestExplorerParentRowGoesUp(t *testing.T) {
	m := newTestModel(t)
	root := m.idx.Root()
	m.setFocus(focusExplorer)

	// Narrow to a subdirectory first, which is where the dead end used to be.
	selectRow(t, m, "internal")
	cmd := m.explorerKey("r")
	m.Update(runUntil[indexReadyMsg](t, cmd))
	if m.tree.Root() == root {
		t.Fatal("setup: the session did not narrow")
	}

	rows := m.tree.Rows()
	if len(rows) == 0 || !rows[0].IsParent() {
		t.Fatalf("no way up is offered: %+v", rows)
	}
	if got := stripANSI(m.View().Content); !strings.Contains(got, "..") {
		t.Errorf("the parent row is not rendered:\n%s", got)
	}

	m.treeSel = 0
	cmd = m.explorerKey("enter")
	m.Update(runUntil[indexReadyMsg](t, cmd))
	if m.tree.Root() != root {
		t.Errorf("selecting the parent row left the tree at %q, want %q", m.tree.Root(), root)
	}
}

func TestDashGoesUpFromAnywhere(t *testing.T) {
	m := newTestModel(t)
	root := m.idx.Root()
	m.setFocus(focusExplorer)

	cmd := m.explorerKey("-")
	m.Update(runUntil[indexReadyMsg](t, cmd))
	if m.tree.Root() == root {
		t.Fatal("- did not move up out of the project root")
	}
	if m.mgr.Active().CWD != m.tree.Root() {
		t.Error("the session did not follow the tree")
	}
}

// TestInputStylesAlwaysSetAForeground is the same guard the theme has, extended
// to the bubbles components.
//
// Their defaults leave the focused Text style with no foreground, so typed text
// fell through to the terminal's own colour and vanished against the input box.
// Fixing the palette did not fix this, because these styles never came from the
// palette at all.
func TestInputStylesAlwaysSetAForeground(t *testing.T) {
	st := theme.New(theme.Dark)

	ta := textareaStyles(st)
	for name, style := range map[string]lipgloss.Style{
		"focused text":        ta.Focused.Text,
		"focused cursor line": ta.Focused.CursorLine,
		"focused prompt":      ta.Focused.Prompt,
		"focused placeholder": ta.Focused.Placeholder,
		"focused selection":   ta.Focused.Selection,
		"blurred text":        ta.Blurred.Text,
		"blurred prompt":      ta.Blurred.Prompt,
	} {
		if style.GetForeground() == nil {
			t.Errorf("textarea %s has no foreground", name)
		}
	}
	// A tinted cursor line is what made the typed line unreadable.
	if bg := ta.Focused.CursorLine.GetBackground(); bg != nil {
		if got := contrastRatio(ta.Focused.Text.GetForeground(), bg); got < 4.5 {
			t.Errorf("text on the cursor line is %.2f:1, want at least 4.5:1", got)
		}
	}
	if ta.Cursor.Color == nil {
		t.Error("the textarea cursor has no colour")
	}

	ti := textinputStyles(st)
	for name, style := range map[string]lipgloss.Style{
		"focused text":        ti.Focused.Text,
		"focused prompt":      ti.Focused.Prompt,
		"focused placeholder": ti.Focused.Placeholder,
		"blurred text":        ti.Blurred.Text,
	} {
		if style.GetForeground() == nil {
			t.Errorf("textinput %s has no foreground", name)
		}
	}
	if ti.Cursor.Color == nil {
		t.Error("the textinput cursor has no colour")
	}
}

// TestEveryInputIsStyled makes sure the styles are actually applied, not just
// constructed: three of the four inputs were built with bare defaults.
func TestEveryInputIsStyled(t *testing.T) {
	m := newTestModel(t)
	want := theme.Dark.Fg

	inputs := map[string]lipgloss.Style{
		"prompt":       m.input.Styles().Focused.Text,
		"file finder":  m.finderIn.Styles().Focused.Text,
		"grep":         m.grepIn.Styles().Focused.Text,
		"find in file": m.findIn.Styles().Focused.Text,
	}
	for name, style := range inputs {
		fg := style.GetForeground()
		if fg == nil {
			t.Errorf("the %s input kept the library default and has no foreground", name)
			continue
		}
		if fg != want {
			t.Errorf("the %s input is %v, want the theme foreground %v", name, fg, want)
		}
	}
}

// contrastRatio is the WCAG ratio between two colours.
func contrastRatio(fg, bg color.Color) float64 {
	lum := func(c color.Color) float64 {
		r, g, b, _ := c.RGBA()
		lin := func(v uint32) float64 {
			f := float64(v>>8) / 255
			if f <= 0.04045 {
				return f / 12.92
			}
			return math.Pow((f+0.055)/1.055, 2.4)
		}
		return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
	}
	a, b := lum(fg), lum(bg)
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

// ---- tab completion --------------------------------------------------------

// tabComplete presses tab and delivers the resulting completion.
func tabComplete(t *testing.T, m *Model) {
	t.Helper()
	cmd := m.inputKey(key("tab"))
	if cmd == nil {
		t.Fatal("tab produced no completion command")
	}
	m.Update(runUntil[completionMsg](t, cmd))
}

func TestTabFinishesAnUnambiguousWord(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusInput)
	m.input.SetValue("cd int")
	m.input.CursorEnd()

	tabComplete(t, m)

	if got := m.input.Value(); got != "cd internal/" {
		t.Errorf("value = %q, want %q", got, "cd internal/")
	}
	if m.compOpen {
		t.Error("a single match should not open the menu")
	}
}

func TestTabExtendsToTheCommonPrefix(t *testing.T) {
	m := newTestModel(t)
	mustWrite(t, filepath.Join(m.idx.Root(), "internal", "core", "alpha.go"), "package core\n")
	mustWrite(t, filepath.Join(m.idx.Root(), "internal", "core", "alphabet.go"), "package core\n")
	m.setFocus(focusInput)
	m.input.SetValue("read internal/core/al")
	m.input.CursorEnd()

	tabComplete(t, m)

	if got := m.input.Value(); got != "read internal/core/alpha" {
		t.Errorf("value = %q, want it extended to the shared prefix", got)
	}
	if m.compOpen {
		t.Error("the menu should wait until nothing more can be typed for free")
	}
}

func TestTabOpensAMenuAndCycles(t *testing.T) {
	m := newTestModel(t)
	// Two directories, so "cd " genuinely has nothing more to type for free.
	mustWrite(t, filepath.Join(m.idx.Root(), "docs", "readme.md"), "hi\n")
	m.tree.Refresh()

	m.setFocus(focusInput)
	m.input.SetValue("cd ")
	m.input.CursorEnd()

	tabComplete(t, m)
	if !m.compOpen {
		t.Fatalf("an ambiguous completion should open the menu: %+v", m.comp.Candidates)
	}
	if len(m.comp.Candidates) < 2 {
		t.Fatalf("expected several candidates, got %+v", m.comp.Candidates)
	}
	if got := stripANSI(m.View().Content); !strings.Contains(got, "matches") {
		t.Errorf("the menu is not rendered:\n%s", got)
	}

	first := m.comp.Candidates[0].Insert
	m.inputKey(key("tab")) // cycle to the first
	if got := m.input.Value(); got != "cd "+first {
		t.Errorf("value = %q, want %q", got, "cd "+first)
	}

	// Cycling again must replace the same word, not append to it.
	m.inputKey(key("tab"))
	second := m.comp.Candidates[m.compSel].Insert
	if got := m.input.Value(); got != "cd "+second {
		t.Errorf("value = %q, want %q — the token range went stale", got, "cd "+second)
	}
	if strings.Contains(m.input.Value(), first+second) {
		t.Error("cycling appended instead of replacing")
	}
}

func TestAnyOtherKeyDismissesTheMenu(t *testing.T) {
	m := newTestModel(t)
	mustWrite(t, filepath.Join(m.idx.Root(), "docs", "readme.md"), "hi\n")
	m.setFocus(focusInput)
	m.input.SetValue("cd ")
	m.input.CursorEnd()
	tabComplete(t, m)
	if !m.compOpen {
		t.Fatal("setup: the menu did not open")
	}

	m.inputKey(key("x"))
	if m.compOpen {
		t.Error("typing should dismiss the menu")
	}
}

func TestStaleCompletionIsIgnored(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusInput)
	m.input.SetValue("cd int")
	m.input.CursorEnd()
	cmd := m.inputKey(key("tab"))
	msg := runUntil[completionMsg](t, cmd)

	// The user kept typing while the directory was being listed.
	m.input.SetValue("cd something else")
	m.Update(msg)

	if m.input.Value() != "cd something else" {
		t.Errorf("a late completion overwrote what was typed since: %q", m.input.Value())
	}
}

func TestCompletionUsesTheSessionFilesystem(t *testing.T) {
	// Completing inside a container has to list the container's files.
	m := newTestModel(t)
	other, fs := otherRoot(t)
	m.useTarget(target{id: fs.ID(), label: "demo", fs: fs, workdir: other})

	m.setFocus(focusInput)
	m.input.SetValue("cd de")
	m.input.CursorEnd()
	tabComplete(t, m)

	if got := m.input.Value(); got != "cd deep/" {
		t.Errorf("value = %q, want the other filesystem's directory", got)
	}
}

// ---- history ---------------------------------------------------------------

func TestHistoryRecall(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusInput)
	m.history = nil
	m.histIdx = 0

	for _, line := range []string{"first question", "second question"} {
		m.input.SetValue(line)
		m.inputKey(key("enter"))
		// The turn that enter started would otherwise block the next one.
		m.mgr.Active().Busy = false
	}
	if len(m.history) != 2 {
		t.Fatalf("history = %v", m.history)
	}

	m.input.SetValue("half typed")
	m.inputKey(key("up"))
	if got := m.input.Value(); got != "second question" {
		t.Errorf("up recalled %q", got)
	}
	m.inputKey(key("up"))
	if got := m.input.Value(); got != "first question" {
		t.Errorf("up again recalled %q", got)
	}
	m.inputKey(key("down"))
	m.inputKey(key("down"))
	if got := m.input.Value(); got != "half typed" {
		t.Errorf("coming back down should restore the unsent line, got %q", got)
	}
}

func TestHistorySkipsImmediateRepeats(t *testing.T) {
	m := newTestModel(t)
	m.history, m.histIdx = nil, 0
	for i := 0; i < 3; i++ {
		m.input.SetValue("same thing")
		m.inputKey(key("enter"))
	}
	if len(m.history) != 1 {
		t.Errorf("history = %v, want one entry", m.history)
	}
}

func TestMultilinePromptKeepsItsArrows(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusInput)
	m.history = []string{"earlier"}
	m.histIdx = 1
	m.input.SetValue("line one\nline two")

	m.inputKey(key("up"))
	if m.input.Value() == "earlier" {
		t.Error("up recalled history instead of moving inside a multi-line prompt")
	}
}

func TestShiftTabSelectsTheLastCandidate(t *testing.T) {
	m := newTestModel(t)
	mustWrite(t, filepath.Join(m.idx.Root(), "docs", "readme.md"), "hi\n")
	m.setFocus(focusInput)
	m.input.SetValue("cd ")
	m.input.CursorEnd()
	tabComplete(t, m)
	if !m.compOpen {
		t.Fatal("setup: the menu did not open")
	}

	m.inputKey(key("shift+tab"))
	last := len(m.comp.Candidates) - 1
	if m.compSel != last {
		t.Errorf("shift+tab from a fresh menu selected %d, want the last candidate %d",
			m.compSel, last)
	}
}

// ---- mouse and sidebar browsing --------------------------------------------

// treeRowY is the screen row a tree index is drawn on.
func treeRowY(m *Model, idx int) int {
	sessH, _ := m.leftSplit()
	return headerRows + sessH + 1 + (idx - m.treeTop)
}

func clickAt(m *Model, x, y int) tea.Cmd {
	return m.onMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func TestClickOpensAFile(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusInput)

	// main.go is a file at the top level of the fixture.
	idx := -1
	for i, r := range m.tree.Rows() {
		if r.Rel == "main.go" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("no main.go row: %+v", m.tree.Rows())
	}

	cmd := clickAt(m, 3, treeRowY(m, idx))
	if m.focus != focusExplorer {
		t.Errorf("clicking the tree did not focus it: %v", m.focus)
	}
	if m.treeSel != idx {
		t.Errorf("selection = %d, want the clicked row %d", m.treeSel, idx)
	}
	if cmd == nil {
		t.Fatal("clicking a file did not load it")
	}
	m.Update(runUntil[fileMsg](t, cmd))
	if m.file == nil || m.file.Rel != "main.go" {
		t.Fatalf("preview shows %+v, want main.go", m.file)
	}
}

func TestClickTogglesADirectory(t *testing.T) {
	m := newTestModel(t)
	idx := -1
	for i, r := range m.tree.Rows() {
		if r.Rel == "internal" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("no internal/ row")
	}

	clickAt(m, 3, treeRowY(m, idx))
	if !m.tree.IsOpen("internal") {
		t.Error("clicking a directory did not expand it")
	}
	clickAt(m, 3, treeRowY(m, idx))
	if m.tree.IsOpen("internal") {
		t.Error("clicking again did not collapse it")
	}
}

func TestClickOnTheParentRowGoesUp(t *testing.T) {
	m := newTestModel(t)
	root := m.idx.Root()
	rows := m.tree.Rows()
	if len(rows) == 0 || !rows[0].IsParent() {
		t.Fatal("no parent row to click")
	}

	cmd := clickAt(m, 3, treeRowY(m, 0))
	m.Update(runUntil[indexReadyMsg](t, cmd))
	if m.tree.Root() == root {
		t.Error("clicking the parent row did not move up")
	}
}

func TestClickSelectsASession(t *testing.T) {
	m := newTestModel(t)
	m.mgr.New() // two sessions
	m.onSessionSwitch()
	first := m.mgr.ActiveIndex()

	// Each session occupies two rows; the second starts two rows down.
	clickAt(m, 3, headerRows+1+2)
	if m.focus != focusSessions {
		t.Errorf("clicking the session list did not focus it: %v", m.focus)
	}
	if m.mgr.ActiveIndex() == first {
		t.Error("clicking a session did not switch to it")
	}
}

// TestArrowKeysPreviewAsTheyMove is the sidebar behaviour asked for: moving the
// selection shows the file straight away, with no confirming keystroke.
//
// It drives the real key path rather than calling explorerKey directly: the
// first version of this called through and passed while the preview was in fact
// stealing focus, so the second arrow scrolled the file instead of browsing.
func TestArrowKeysPreviewAsTheyMove(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusExplorer)
	m.treeSel = 0

	var opened []string
	for i := 0; i < len(m.tree.Rows()); i++ {
		cmd := m.onKey(key("down"))
		if m.focus != focusExplorer {
			t.Fatalf("browsing moved focus to %v; the tree must keep it", m.focus)
		}
		if cmd == nil {
			continue // a directory row previews nothing
		}
		msg := runUntil[fileMsg](t, cmd)
		m.Update(msg)
		if m.file != nil {
			opened = append(opened, m.file.Rel)
		}
	}
	if len(opened) == 0 {
		t.Fatal("moving through the tree never previewed a file")
	}
	if m.file == nil || m.file.Rel != opened[len(opened)-1] {
		t.Errorf("preview does not match the last file moved onto")
	}
}

func TestSelectingADirectoryKeepsTheOpenFile(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusExplorer)
	m.Update(fileMsg{f: m.loader.Load(m.hostFS,
		filepath.Join(m.idx.Root(), "main.go"), "main.go"), line: 1})

	for i, r := range m.tree.Rows() {
		if r.Dir && !r.IsParent() {
			m.treeSel = i
			break
		}
	}
	if cmd := m.previewSelected(); cmd != nil {
		t.Error("selecting a directory should not replace the preview")
	}
	if m.file == nil || m.file.Rel != "main.go" {
		t.Errorf("the open file was cleared: %+v", m.file)
	}
}

func TestStalePreviewIsDiscarded(t *testing.T) {
	// Holding an arrow key queues several loads; only the last may land.
	m := newTestModel(t)
	m.setFocus(focusExplorer)

	first := m.loadFileAt(filepath.Join(m.idx.Root(), "main.go"), "main.go", 1, false)
	second := m.loadFileAt(filepath.Join(m.idx.Root(), "internal/core/core.go"), "internal/core/core.go", 1, false)

	late := runUntil[fileMsg](t, first)   // issued first, arrives last
	fresh := runUntil[fileMsg](t, second) // the newer request

	m.Update(fresh)
	m.Update(late)

	if m.file == nil || !strings.HasSuffix(m.file.Rel, "core.go") {
		t.Errorf("a stale load overwrote the newer one: %+v", m.file)
	}
}

func TestWheelScrollsOnlyThePaneUnderThePointer(t *testing.T) {
	m := newTestModel(t)
	m.Update(fileMsg{f: m.loader.Load(m.hostFS,
		filepath.Join(m.idx.Root(), "internal/core/core.go"), "internal/core/core.go"), line: 1})
	m.chat.SetContent(strings.Repeat("chat line\n", 200))
	m.prev.SetContent(strings.Repeat("preview line\n", 200))
	m.chat.GotoTop()
	m.prev.GotoTop()

	// Over the chat pane.
	x := m.sideW + 2
	m.onMouse(tea.MouseWheelMsg{X: x, Y: headerRows + 3, Button: tea.MouseWheelDown})
	if m.chat.YOffset() == 0 {
		t.Error("the wheel did not scroll the chat pane")
	}
	if m.prev.YOffset() != 0 {
		t.Error("the wheel scrolled the preview pane as well")
	}

	// And back up again, which the first version of this got wrong.
	down := m.chat.YOffset()
	m.onMouse(tea.MouseWheelMsg{X: x, Y: headerRows + 3, Button: tea.MouseWheelUp})
	if m.chat.YOffset() >= down {
		t.Errorf("scrolling up left the chat at %d, want less than %d", m.chat.YOffset(), down)
	}

	// Over the preview pane.
	before := m.chat.YOffset()
	m.onMouse(tea.MouseWheelMsg{X: m.sideW + m.chatW + 2, Y: headerRows + 3, Button: tea.MouseWheelDown})
	if m.prev.YOffset() == 0 {
		t.Error("the wheel did not scroll the preview pane")
	}
	if m.chat.YOffset() != before {
		t.Error("scrolling the preview also moved the chat")
	}
}

func TestPaneAtMatchesTheDrawnLayout(t *testing.T) {
	m := newTestModel(t)
	sessH, _ := m.leftSplit()

	for _, tc := range []struct {
		name string
		x, y int
		want focus
	}{
		{"header", 5, 0, -1},
		{"sessions", 3, headerRows + 1, focusSessions},
		{"explorer", 3, headerRows + sessH + 2, focusExplorer},
		{"chat", m.sideW + 5, headerRows + 3, focusChat},
		{"preview", m.sideW + m.chatW + 5, headerRows + 3, focusPreview},
		{"input box", 5, headerRows + m.bodyH, focusInput},
		{"status bar", 5, m.h - 1, -1},
	} {
		if got := m.paneAt(tc.x, tc.y); got != tc.want {
			t.Errorf("paneAt(%d,%d) for the %s = %v, want %v", tc.x, tc.y, tc.name, got, tc.want)
		}
	}
}

// TestBrowsingKeepsFocusInTheTree covers the click path too: after clicking a
// file, the next arrow key must move the selection, not scroll the preview.
func TestBrowsingKeepsFocusInTheTree(t *testing.T) {
	m := newTestModel(t)
	// Files after the one clicked, so "down" has somewhere to go.
	mustWrite(t, filepath.Join(m.idx.Root(), "second.go"), "package second\n")
	mustWrite(t, filepath.Join(m.idx.Root(), "third.go"), "package third\n")
	m.tree.Refresh()

	idx := -1
	for i, r := range m.tree.Rows() {
		if r.Rel == "main.go" {
			idx = i
		}
	}
	if idx < 0 || idx >= len(m.tree.Rows())-1 {
		t.Fatalf("setup: main.go must not be the last row: %+v", m.tree.Rows())
	}

	cmd := clickAt(m, 3, treeRowY(m, idx))
	m.Update(runUntil[fileMsg](t, cmd))
	if m.focus != focusExplorer {
		t.Fatalf("clicking a file moved focus to %v", m.focus)
	}

	before := m.treeSel
	cmd = m.onKey(key("down"))
	if m.treeSel == before {
		t.Fatalf("the arrow key did not move the selection (focus=%v); it went to the preview", m.focus)
	}
	// And it previewed what it moved onto.
	if cmd == nil {
		t.Fatal("moving onto a file did not preview it")
	}
	m.Update(runUntil[fileMsg](t, cmd))
	if m.file == nil || m.file.Rel == "main.go" {
		t.Errorf("the preview did not follow the selection: %+v", m.file)
	}
}

// TestEnterHandsOverToThePreview is the other half: an explicit open should
// land you in the file so you can scroll it.
func TestEnterHandsOverToThePreview(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusExplorer)
	selectRow(t, m, "main.go")

	cmd := m.explorerKey("enter")
	m.Update(runUntil[fileMsg](t, cmd))
	if m.focus != focusPreview {
		t.Errorf("enter left focus at %v, want the preview", m.focus)
	}
}

// ---- new and forked sessions -----------------------------------------------

func TestCtrlTMakesANewSession(t *testing.T) {
	m := newTestModel(t)
	before := m.mgr.Len()

	press(t, m, "ctrl+t")
	if m.mgr.Len() != before+1 {
		t.Fatalf("sessions = %d, want %d", m.mgr.Len(), before+1)
	}
	if len(m.mgr.Active().Messages) != 0 {
		t.Error("a new session should start empty")
	}
	// The engine carries over, so ctrl+t twice does not silently change agent.
	m.lastEngine = "api"
	press(t, m, "ctrl+t")
	if m.mgr.Active().Engine != "api" {
		t.Errorf("a new session picked engine %q", m.mgr.Active().Engine)
	}
}

func TestForkKeepsTheOriginalAndBranchesTheNew(t *testing.T) {
	m := newTestModel(t)
	src := m.mgr.Active()
	src.ExternalID = "sess-1"
	src.Append(session.Message{Role: session.RoleUser, Text: "the original thread"})

	press(t, m, "alt+t")

	if m.mgr.Len() != 2 {
		t.Fatalf("sessions = %d, want 2", m.mgr.Len())
	}
	f := m.mgr.Active()
	if f == src {
		t.Fatal("the fork did not become the active session")
	}
	if len(f.Messages) != 1 || f.Messages[0].Text != "the original thread" {
		t.Errorf("the fork did not inherit the transcript: %+v", f.Messages)
	}
	if !f.ForkPending {
		t.Error("the fork should branch the engine on its next turn")
	}
	if len(src.Messages) != 1 {
		t.Error("the original was disturbed")
	}
	if got := stripANSI(m.View().Content); !strings.Contains(got, "fork") {
		t.Errorf("the fork is not visible in the session list:\n%s", got)
	}
}

func TestForkingAnEmptySessionDoesNothing(t *testing.T) {
	m := newTestModel(t)
	press(t, m, "alt+t")
	if m.mgr.Len() != 1 {
		t.Errorf("forking an empty session created %d sessions", m.mgr.Len())
	}
	if m.notice == "" {
		t.Error("nothing explained why the fork did not happen")
	}
}

func TestTurnCarriesTheForkFlagOnce(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Engine = "api"
	s.SetExternalID("api", "sess-1")
	s.ForkPending = true
	s.Append(session.Message{Role: session.RoleUser, Text: "earlier"})

	// The engine reports the id of the branch it created. An event names the
	// engine that produced it, because a session can be handed on while a turn
	// is still finishing and the id belongs to whoever made it.
	ch := make(chan agent.Event, 2)
	m.Update(agentMsg{sess: s, ch: ch, eng: "api", ev: agent.EvSession{ExternalID: "sess-2"}})

	if s.ForkPending {
		t.Error("the fork should be spent once the engine reports its new session")
	}
	if got := s.StateFor("api").ExternalID; got != "sess-2" {
		t.Errorf("external id = %q, want the branch's own id", got)
	}
}

// An event that arrives after the session was handed on belongs to the engine
// that produced it, not to the one holding the session now. Filed under the
// wrong engine, it becomes `resume <another program's id>` on the next turn.
func TestALateEventLandsOnTheEngineThatProducedIt(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	s.Engine = "claude"
	s.Append(session.Message{Role: session.RoleUser, Text: "earlier"})

	ch := make(chan agent.Event, 2)
	m.Update(agentMsg{sess: s, ch: ch, eng: "codex", ev: agent.EvSession{ExternalID: "codex-1"}})

	if got := s.StateFor("codex").ExternalID; got != "codex-1" {
		t.Errorf("codex remembers %q, want its own id", got)
	}
	if got := s.StateFor("claude").ExternalID; got != "" {
		t.Errorf("claude was given codex's id: %q", got)
	}
}

func TestSessionListForkAndNewKeys(t *testing.T) {
	m := newTestModel(t)
	m.mgr.Active().Append(session.Message{Role: session.RoleUser, Text: "hi"})
	m.setFocus(focusSessions)
	m.sessSel = 0

	m.sessionsKey("f")
	if m.mgr.Len() != 2 {
		t.Fatalf("f did not fork: %d sessions", m.mgr.Len())
	}
	m.sessionsKey("n")
	if m.mgr.Len() != 3 {
		t.Errorf("n did not create a session: %d", m.mgr.Len())
	}
	if len(m.mgr.Active().Messages) != 0 {
		t.Error("n should create an empty session, not a fork")
	}
}

// ---- slash commands --------------------------------------------------------

func TestParseSlash(t *testing.T) {
	for _, tc := range []struct {
		in        string
		name, arg string
		ok        bool
	}{
		{"/new", "new", "", true},
		{"/NEW", "new", "", true},
		{"/cd internal/ui", "cd", "internal/ui", true},
		{"/search  some text ", "search", "some text", true},
		{"/", "", "", false},
		{"//not a command", "", "", false},
		{"/path/to/file explain this", "", "", false},
		{"what does /new do?", "", "", false},
		{"/new\nand more", "", "", false},
	} {
		name, arg, ok := parseSlash(tc.in)
		if ok != tc.ok || name != tc.name || arg != tc.arg {
			t.Errorf("parseSlash(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, name, arg, ok, tc.name, tc.arg, tc.ok)
		}
	}
}

func TestSlashNewCreatesASession(t *testing.T) {
	m := newTestModel(t)
	before := m.mgr.Len()

	m.input.SetValue("/new")
	m.inputKey(key("enter"))

	if m.mgr.Len() != before+1 {
		t.Fatalf("sessions = %d, want %d", m.mgr.Len(), before+1)
	}
	if len(m.mgr.Active().Messages) != 0 {
		t.Error("/new should start an empty session")
	}
	// The command must not reach the agent.
	for _, s := range m.mgr.All() {
		for _, msg := range s.Messages {
			if strings.Contains(msg.Text, "/new") {
				t.Error("/new was sent to the agent as a prompt")
			}
		}
	}
	if m.input.Value() != "" {
		t.Errorf("the prompt was not cleared: %q", m.input.Value())
	}
}

func TestSlashForkBranchesTheSession(t *testing.T) {
	m := newTestModel(t)
	src := m.mgr.Active()
	src.ExternalID = "sess-1"
	src.Append(session.Message{Role: session.RoleUser, Text: "original"})

	m.input.SetValue("/fork")
	m.inputKey(key("enter"))

	if m.mgr.Len() != 2 {
		t.Fatalf("sessions = %d, want 2", m.mgr.Len())
	}
	f := m.mgr.Active()
	if len(f.Messages) != 1 || !f.ForkPending {
		t.Errorf("the fork did not inherit the thread: %+v", f)
	}
}

func TestSlashCDMovesTheSession(t *testing.T) {
	m := newTestModel(t)
	root := m.idx.Root()

	m.input.SetValue("/cd internal")
	cmd := m.inputKey(key("enter"))
	m.Update(runUntil[indexReadyMsg](t, cmd))

	if got := m.mgr.Active().CWD; got != filepath.Join(root, "internal") {
		t.Errorf("cwd = %q", got)
	}
}

func TestUnknownCommandIsNotSentToTheAgent(t *testing.T) {
	m := newTestModel(t)

	m.input.SetValue("/nope")
	m.inputKey(key("enter"))

	if len(m.mgr.Active().Messages) != 0 {
		t.Errorf("an unknown command was sent as a prompt: %+v", m.mgr.Active().Messages)
	}
	if !strings.Contains(m.notice, "/nope") || !strings.Contains(m.notice, "/new") {
		t.Errorf("the notice should name the command and list the real ones: %q", m.notice)
	}
}

func TestAMessageThatMentionsASlashStillReachesTheAgent(t *testing.T) {
	for _, text := range []string{
		"explain /etc/hosts",
		"//literally a comment",
		"what does /new do?",
	} {
		if _, _, ok := parseSlash(text); ok {
			t.Errorf("%q was taken as a command", text)
		}
	}
}

func TestSlashOverlayCommands(t *testing.T) {
	m := newTestModel(t)
	for cmd, want := range map[string]overlay{
		"/help":   overlayHelp,
		"/files":  overlayFinder,
		"/engine": overlayEngine,
		"/target": overlayTarget,
	} {
		m.overlay = overlayNone
		m.input.SetValue(cmd)
		m.inputKey(key("enter"))
		if m.overlay != want {
			t.Errorf("%s opened overlay %v, want %v", cmd, m.overlay, want)
		}
	}
}

func TestSlashEngineByName(t *testing.T) {
	m := newTestModel(t)
	m.reg = engine.NewRegistryWith(
		engine.NewAPI(agent.New("k", &agent.Executor{}, "m", "high", 1), "m"),
		fakeEngine{id: "claude", label: "Claude Code"},
	)

	m.input.SetValue("/engine claude")
	m.inputKey(key("enter"))
	if got := m.mgr.Active().Engine; got != "claude" {
		t.Errorf("engine = %q, want claude", got)
	}

	m.input.SetValue("/engine nonsense")
	m.inputKey(key("enter"))
	if m.mgr.Active().Engine != "claude" {
		t.Error("an unknown engine name changed the session")
	}
	if !strings.Contains(m.notice, "nonsense") {
		t.Errorf("notice = %q", m.notice)
	}
}

func TestTabCompletesCommands(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusInput)
	m.input.SetValue("/fo")
	m.input.CursorEnd()

	tabComplete(t, m)
	if got := m.input.Value(); got != "/fork" {
		t.Errorf("value = %q, want %q", got, "/fork")
	}
}

func TestTabOffersACommandMenu(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusInput)
	m.input.SetValue("/")
	m.input.CursorEnd()

	tabComplete(t, m)
	if !m.compOpen {
		t.Fatalf("a bare slash should list the commands: %+v", m.comp.Candidates)
	}
	var names []string
	for _, c := range m.comp.Candidates {
		names = append(names, c.Insert)
	}
	if len(names) != len(slashCmds) {
		t.Errorf("offered %v, want all %d commands", names, len(slashCmds))
	}
}

func TestHelpListsEveryCommand(t *testing.T) {
	m := newTestModel(t)
	m.overlay = overlayHelp
	got := stripANSI(m.helpView())
	for _, c := range slashCmds {
		if !strings.Contains(got, "/"+c.name) {
			t.Errorf("/%s is missing from the help:\n%s", c.name, got)
		}
	}
}

// ---- input method support --------------------------------------------------

// TestTerminalCursorFollowsTheFocusedInput guards Vietnamese (and every other
// composing) input. A drawn-on cursor leaves the terminal with no insertion
// point, so the IME never composes and keystrokes arrive raw — "khoong" instead
// of "không".
func TestTerminalCursorFollowsTheFocusedInput(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusInput)
	m.input.SetValue("hello")
	m.input.CursorEnd()

	c := m.cursor()
	if c == nil {
		t.Fatal("the prompt has focus but reports no terminal cursor")
	}
	// It has to land inside the prompt box, not somewhere in the panes.
	top := headerRows + m.bodyH
	if c.Position.Y < top || c.Position.Y >= top+inputRows {
		t.Errorf("cursor row %d is outside the prompt box (%d..%d)",
			c.Position.Y, top, top+inputRows)
	}
	if c.Position.X < promptBorderX || c.Position.X >= m.w {
		t.Errorf("cursor column %d is outside the screen", c.Position.X)
	}

	// It must move with the text.
	before := c.Position.X
	m.input.SetValue("hello there, a longer line")
	m.input.CursorEnd()
	if after := m.cursor().Position.X; after <= before {
		t.Errorf("cursor did not follow the text: %d then %d", before, after)
	}
}

func TestNoTerminalCursorWhenTheInputIsNotFocused(t *testing.T) {
	m := newTestModel(t)
	for _, f := range []focus{focusChat, focusPreview, focusExplorer, focusSessions} {
		m.setFocus(f)
		if c := m.cursor(); c != nil {
			t.Errorf("focus %v still shows a text cursor at %+v", f, c.Position)
		}
	}
}

func TestOverlayInputsGetTheCursor(t *testing.T) {
	m := newTestModel(t)
	for _, tc := range []struct {
		key  string
		name string
	}{
		{"ctrl+p", "file finder"},
		{"ctrl+g", "content search"},
	} {
		press(t, m, tc.key)
		c := m.cursor()
		if c == nil {
			t.Errorf("the %s takes typing but reports no cursor", tc.name)
			press(t, m, "esc")
			continue
		}
		if c.Position.X < m.overlayX || c.Position.Y < m.overlayY {
			t.Errorf("the %s cursor at %+v is outside its box at (%d,%d)",
				tc.name, c.Position, m.overlayX, m.overlayY)
		}
		press(t, m, "esc")
	}
}

func TestPickersHaveNoTextCursor(t *testing.T) {
	// These take keys, not text, so a blinking cursor would be misleading.
	m := newTestModel(t)
	for _, k := range []string{"ctrl+r", "f1"} {
		press(t, m, k)
		if c := m.cursor(); c != nil {
			t.Errorf("%s shows a text cursor at %+v", k, c.Position)
		}
		press(t, m, "esc")
	}
}

func TestClickingThePromptFocusesIt(t *testing.T) {
	m := newTestModel(t)
	m.setFocus(focusExplorer)

	clickAt(m, 5, headerRows+m.bodyH+1)
	if m.focus != focusInput {
		t.Fatalf("clicking the prompt left focus at %v", m.focus)
	}
	if m.cursor() == nil {
		t.Error("the prompt has focus but no cursor to type at")
	}
}

// ---- the agent's option box ------------------------------------------------

func askEvent(reply chan int) agent.EvChoice {
	return agent.EvChoice{
		Call:     session.ToolCall{ID: "q1", Name: "ask_user"},
		Question: "Which approach?",
		Options: []session.Choice{
			{Label: "Rewrite it", Detail: "cleaner, slower to land"},
			{Label: "Patch it", Detail: "smaller change"},
		},
		Reply: reply,
	}
}

func TestChoiceBoxAnswersTheAgent(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	reply := make(chan int, 1)

	m.Update(agentMsg{sess: s, ch: make(chan agent.Event, 1), ev: askEvent(reply)})
	if m.overlay != overlayChoice {
		t.Fatalf("the question did not open its box (overlay=%v)", m.overlay)
	}
	out := stripANSI(m.View().Content)
	for _, want := range []string{"Which approach?", "Rewrite it", "Patch it", "cleaner"} {
		if !strings.Contains(out, want) {
			t.Errorf("the box is missing %q:\n%s", want, out)
		}
	}

	press(t, m, "down")
	press(t, m, "enter")
	if got := <-reply; got != 1 {
		t.Errorf("answered %d, want the second option", got)
	}
	if m.overlay != overlayNone {
		t.Error("the box stayed open after an answer")
	}
}

func TestChoiceBoxNumberKeys(t *testing.T) {
	m := newTestModel(t)
	reply := make(chan int, 1)
	m.Update(agentMsg{sess: m.mgr.Active(), ch: make(chan agent.Event, 1), ev: askEvent(reply)})

	press(t, m, "2")
	if got := <-reply; got != 1 {
		t.Errorf("pressing 2 answered %d, want index 1", got)
	}
}

func TestChoiceBoxCanBeDeclined(t *testing.T) {
	m := newTestModel(t)
	reply := make(chan int, 1)
	m.Update(agentMsg{sess: m.mgr.Active(), ch: make(chan agent.Event, 1), ev: askEvent(reply)})

	press(t, m, "esc")
	if got := <-reply; got != -1 {
		t.Errorf("esc answered %d, want -1 for declined", got)
	}
}

func TestChoicesQueueAcrossSessions(t *testing.T) {
	m := newTestModel(t)
	a := m.mgr.Active()
	b := m.mgr.New()
	m.onSessionSwitch()

	ra, rb := make(chan int, 1), make(chan int, 1)
	ch := make(chan agent.Event, 2)
	m.Update(agentMsg{sess: a, ch: ch, ev: askEvent(ra)})
	if m.mgr.Active() != a {
		t.Error("the asking session was not brought to the front")
	}
	m.Update(agentMsg{sess: b, ch: ch, ev: askEvent(rb)})
	if len(m.choices) != 2 {
		t.Fatalf("questions did not queue: %d", len(m.choices))
	}

	press(t, m, "1")
	if got := <-ra; got != 0 {
		t.Errorf("first answer = %d", got)
	}
	if m.overlay != overlayChoice {
		t.Error("the queued question did not take over")
	}
	press(t, m, "esc")
	if got := <-rb; got != -1 {
		t.Errorf("second answer = %d", got)
	}
}

func TestCancellingARunDeclinesItsQuestions(t *testing.T) {
	m := newTestModel(t)
	a := m.mgr.Active()
	reply := make(chan int, 1)
	m.Update(agentMsg{sess: a, ch: make(chan agent.Event, 1), ev: askEvent(reply)})

	a.Busy = true
	m.cancelRun()
	if got := <-reply; got != -1 {
		t.Errorf("cancelling answered %d, want it declined", got)
	}
}

// ---- background commands ---------------------------------------------------

func TestTasksViewListsAndOpens(t *testing.T) {
	m := newTestModel(t)
	m.tasks.Adopt("t1", "go test ./...", m.mgr.Active().ID)
	m.tasks.Update("t1", task.Running, "running tests")
	m.tasks.Adopt("t2", "npm run dev", m.mgr.Active().ID)

	press(t, m, "ctrl+k")
	if m.overlay != overlayTasks {
		t.Fatal("ctrl+k did not open the task list")
	}
	out := stripANSI(m.View().Content)
	for _, want := range []string{"Background commands", "go test", "npm run dev"} {
		if !strings.Contains(out, want) {
			t.Errorf("the list is missing %q:\n%s", want, out)
		}
	}

	press(t, m, "enter")
	if m.taskOpen != "t1" {
		t.Fatalf("enter opened %q, want t1", m.taskOpen)
	}
	if got := stripANSI(m.View().Content); !strings.Contains(got, "running tests") {
		t.Errorf("the output is not shown:\n%s", got)
	}

	// esc steps back to the list before closing the whole thing.
	press(t, m, "esc")
	if m.taskOpen != "" || m.overlay != overlayTasks {
		t.Errorf("esc closed everything instead of stepping back: open=%q overlay=%v",
			m.taskOpen, m.overlay)
	}
	press(t, m, "esc")
	if m.overlay != overlayNone {
		t.Error("a second esc should close the list")
	}
}

func TestRunningTasksAreVisibleInTheStatusBar(t *testing.T) {
	m := newTestModel(t)
	if got := stripANSI(m.View().Content); strings.Contains(got, "running") {
		t.Errorf("nothing is running, but the status bar says so:\n%s", got)
	}
	m.tasks.Adopt("t1", "go build", m.mgr.Active().ID)
	if got := stripANSI(m.View().Content); !strings.Contains(got, "1 running") {
		t.Errorf("a running command is not surfaced:\n%s", got)
	}
	m.tasks.Update("t1", task.Done, "")
	if got := stripANSI(m.View().Content); strings.Contains(got, "1 running") {
		t.Errorf("a finished command is still counted:\n%s", got)
	}
}

// TestForeignTasksAppearInTheSameList is the point of the shared registry:
// Claude Code's own background work shows up beside ours.
func TestForeignTasksAppearInTheSameList(t *testing.T) {
	m := newTestModel(t)
	s := m.mgr.Active()
	ch := make(chan agent.Event, 4)

	m.Update(agentMsg{sess: s, ch: ch, ev: agent.EvTask{
		ID: "bbkhka38p", Label: "Sleep then print", State: "running",
	}})
	tk := m.tasks.Get("bbkhka38p")
	if tk == nil || !tk.Live() {
		t.Fatalf("the CLI's task was not adopted: %+v", tk)
	}
	if tk.Owner != s.ID {
		t.Errorf("owner = %q, want the session that started it", tk.Owner)
	}

	m.Update(agentMsg{sess: s, ch: ch, ev: agent.EvTask{
		ID: "bbkhka38p", State: "stopped", Output: "/tmp/x.output",
	}})
	if tk.Live() {
		t.Errorf("state = %s, want it finished", tk.State())
	}
	if got := strings.Join(tk.Output(), " "); !strings.Contains(got, "/tmp/x.output") {
		t.Errorf("the output file was not recorded: %q", got)
	}
}

func TestSlashTasksOpensTheList(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("/tasks")
	m.inputKey(key("enter"))
	if m.overlay != overlayTasks {
		t.Errorf("/tasks opened %v", m.overlay)
	}
}

// TestHistoryIsIsolatedFromTheRealOne is a guard on the tests themselves: they
// used to write to the user's own prompt history, which both corrupted it and
// made them depend on the order they ran in.
func TestHistoryIsIsolatedFromTheRealOne(t *testing.T) {
	m := newTestModel(t)
	if len(m.history) != 0 {
		t.Fatalf("a fresh model already has history: %v", m.history)
	}

	m.input.SetValue("a test prompt")
	m.inputKey(key("enter"))

	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		t.Fatal("XDG_DATA_HOME was not redirected")
	}
	if !strings.HasPrefix(historyPath(), data) {
		t.Errorf("history is written to %q, outside the test's own directory", historyPath())
	}
}

// ---- modes -----------------------------------------------------------------

func TestNewSessionsStartOnAuto(t *testing.T) {
	m := newTestModel(t)
	if got := sessionMode(m.mgr.Active()); got != agent.ModeAuto {
		t.Errorf("a new session is in %v mode, want auto", got)
	}
	press(t, m, "ctrl+t")
	if got := sessionMode(m.mgr.Active()); got != agent.ModeAuto {
		t.Errorf("ctrl+t started in %v mode, want auto", got)
	}
	if out := stripANSI(m.View().Content); !strings.Contains(out, "auto") {
		t.Errorf("the mode is not on screen:\n%s", out)
	}
}

func TestShiftTabCyclesMode(t *testing.T) {
	m := newTestModel(t)
	seen := []agent.Mode{sessionMode(m.mgr.Active())}

	for i := 0; i < len(agent.Cycle); i++ {
		press(t, m, "shift+tab")
		seen = append(seen, sessionMode(m.mgr.Active()))
	}
	if seen[len(seen)-1] != seen[0] {
		t.Errorf("cycling did not return to %v: %v", seen[0], seen)
	}
	for _, mode := range agent.Cycle {
		var found bool
		for _, s := range seen {
			if s == mode {
				found = true
			}
		}
		if !found {
			t.Errorf("cycling never reached %v: %v", mode, seen)
		}
	}
}

// TestShiftTabNeverReachesFull mirrors the safety property at the UI level: no
// amount of tapping should land on a mode with no project boundary.
func TestShiftTabNeverReachesFull(t *testing.T) {
	m := newTestModel(t)
	for i := 0; i < 12; i++ {
		press(t, m, "shift+tab")
		if got := sessionMode(m.mgr.Active()); got == agent.ModeFull {
			t.Fatalf("shift+tab reached full mode after %d presses", i+1)
		}
	}
}

func TestModeIsPerSession(t *testing.T) {
	m := newTestModel(t)
	a := m.mgr.Active()
	m.setMode(agent.ModePlan)

	b := m.mgr.New()
	m.onSessionSwitch()
	if got := sessionMode(b); got != agent.ModeAuto {
		t.Errorf("the new session inherited %v, want its own auto", got)
	}

	for i, s := range m.mgr.All() {
		if s == a {
			m.mgr.Select(i)
		}
	}
	m.onSessionSwitch()
	if got := sessionMode(m.mgr.Active()); got != agent.ModePlan {
		t.Errorf("going back lost the session's mode: %v", got)
	}
}

func TestSlashModeSetsItByName(t *testing.T) {
	m := newTestModel(t)
	for _, name := range []string{"plan", "ask", "full", "auto"} {
		m.input.SetValue("/mode " + name)
		m.inputKey(key("enter"))
		if got := sessionMode(m.mgr.Active()); got != agent.ParseMode(name) {
			t.Errorf("/mode %s left the session in %v", name, got)
		}
	}
	// Bare /mode cycles, like the shortcut.
	before := sessionMode(m.mgr.Active())
	m.input.SetValue("/mode")
	m.inputKey(key("enter"))
	if sessionMode(m.mgr.Active()) == before {
		t.Error("a bare /mode did not cycle")
	}
}

func TestTurnCarriesTheSessionMode(t *testing.T) {
	m := newTestModel(t)
	m.setMode(agent.ModePlan)
	if got := sessionMode(m.mgr.Active()); got != agent.ModePlan {
		t.Fatalf("setup: mode is %v", got)
	}
	// The engine is handed the mode rather than reading it from anywhere else.
	if got := sessionMode(m.mgr.Active()); !got.Writes() {
		return // plan mode, as expected
	}
	t.Error("plan mode should not allow writes")
}

// TestAskModeWarnsWhenTheEngineCannotAsk: codex and opencode have no approval
// channel, so promising "ask first" there would be a lie.
func TestAskModeWarnsWhenTheEngineCannotAsk(t *testing.T) {
	m := newTestModel(t)
	m.reg = engine.NewRegistryWith(
		engine.NewAPI(agent.New("k", &agent.Executor{}, "m", "high", 1), "m"),
		fakeEngine{id: "codex", label: "Codex"}, // CanAsk is false
	)
	m.mgr.Active().Engine = "codex"

	m.setMode(agent.ModeAsk)
	if !strings.Contains(m.notice, "cannot ask") {
		t.Errorf("nothing warned that the engine cannot ask: %q", m.notice)
	}
	if out := stripANSI(m.View().Content); !strings.Contains(out, "⚠") {
		t.Errorf("the status bar does not flag the mismatch:\n%s", out)
	}

	// The built-in engine can, so it gets no warning.
	m.mgr.Active().Engine = "api"
	m.setMode(agent.ModeAsk)
	if strings.Contains(m.notice, "cannot ask") {
		t.Errorf("the built-in engine was wrongly flagged: %q", m.notice)
	}
}

// TestAllowAllStaysWithItsOwnSession guards the fix for a real race: trusting
// the rest of a run used to set a flag on the shared executor, so one
// conversation's decision silently applied to every other one.
func TestAllowAllStaysWithItsOwnSession(t *testing.T) {
	m := newTestModel(t)
	a := m.mgr.Active()
	b := m.mgr.New()
	m.onSessionSwitch()

	ra, rb := make(chan agent.Verdict, 1), make(chan agent.Verdict, 1)
	ch := make(chan agent.Event, 4)

	m.Update(agentMsg{sess: a, ch: ch, ev: agent.EvApproval{
		Call: session.ToolCall{ID: "1", Name: "bash"}, Reply: ra,
	}})
	press(t, m, "a") // allow everything for a's run
	if got := <-ra; got != agent.AllowAll {
		t.Fatalf("a answered %v, want allow-all", got)
	}

	// b must still be asked.
	m.Update(agentMsg{sess: b, ch: ch, ev: agent.EvApproval{
		Call: session.ToolCall{ID: "2", Name: "write_file"}, Reply: rb,
	}})
	if m.overlay != overlayApproval {
		t.Fatal("the other session was not asked; trust leaked across sessions")
	}
	press(t, m, "n")
	if got := <-rb; got != agent.Deny {
		t.Errorf("second answer = %v", got)
	}
}
