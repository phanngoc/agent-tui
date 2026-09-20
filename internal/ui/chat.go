package ui

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/phanngoc/agent-tui/internal/highlight"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/theme"
)

// transcript renders the whole conversation as Warp-style blocks: a coloured
// rail down the left, a header, then wrapped body text.
//
// Committed messages and the streaming tail are cached separately. A streaming
// turn emits a text delta every few milliseconds, and re-wrapping a long
// session on each one would dominate the update loop; splitting the cache makes
// a delta cost only what the unfinished turn costs.
func (m *Model) transcript(width int) string {
	s := m.mgr.Active()

	key := s.ID + "|" + strconv.Itoa(len(s.Messages)) + "|" +
		strconv.Itoa(width) + "|" + toolStateKey(s) + "|" +
		strconv.FormatBool(m.showAllCalls) + "|" + clockKey(s)
	if key != m.chatKey {
		h := m.renderHead(width)
		m.chatCache, m.chatStarts, m.chatTurn = h.text, h.starts, h.turn
		m.chatKey = key
	}
	if s.Partial == "" && s.LastErr == "" && len(s.Calls) == 0 && s.Output == "" {
		return m.chatCache
	}

	var b strings.Builder
	b.Grow(len(m.chatCache) + len(s.Partial) + len(s.Output) + 256)
	b.WriteString(m.chatCache)
	if s.Partial != "" {
		if len(s.Messages) > 0 {
			b.WriteByte('\n')
		}
		m.blockMD(&b, m.st.AgentBar, m.st.AgentTag.Render("agent"), "", s.Partial, width)
	}
	// Calls the model is still writing. Each carries half a JSON object, which
	// is what makes them worth showing at all: the path appears as it is typed
	// instead of once the whole turn is finished.
	for _, c := range s.Calls {
		m.renderTool(&b, c, width)
	}
	// What the running tool has printed. It goes under the committed half
	// rather than under its own line, so that a build printing every second
	// does not rebuild the cached transcript above it every second.
	for _, l := range lastLines(s.Output, liveOutputRows) {
		b.WriteString(m.st.AgentBar.Render("▎") + "     " +
			m.st.Dim.Render(truncate(highlight.ExpandTabs(l), max(10, width-8))) + "\n")
	}
	if s.LastErr != "" {
		b.WriteByte('\n')
		m.block(&b, m.st.Bad, m.st.ErrTag.Render("error"), "", s.LastErr, width)
	}
	return b.String()
}

// showLatestTurn scrolls the transcript to the top of the newest exchange.
//
// Landing at the very bottom instead drops the reader into the last line of an
// answer they have not read, which for a long reply is the least useful line in
// it; landing at the question puts the answer to it on screen from its first
// line. If the exchange is shorter than the pane the offset clamps and the
// bottom is shown anyway, which is the same thing.
func (m *Model) showLatestTurn() {
	width := max(10, m.chatW-2)
	// The transcript is normally handed to the viewport while drawing, one
	// frame later than this. Setting it here too means the offset is clamped
	// against this session's transcript rather than against whichever one was
	// on screen a moment ago.
	m.chat.SetContent(m.transcript(width))
	m.chat.SetYOffset(m.chatTurn)
}

// showMessage scrolls the transcript so message i starts at the top of the
// pane — the same argument showLatestTurn makes, at message granularity: what
// you want on screen is the whole of the thing you came for, from its first
// line.
//
// The order here is load-bearing. The offset is read from chatStarts, which
// belongs to whichever conversation the cache was last built for, so the
// content has to be rebuilt for this one first. Setting an offset from the
// previous session's map scrolls the right conversation to the wrong place,
// which looks plausible and is completely wrong.
func (m *Model) showMessage(i int) {
	width := max(10, m.chatW-2)
	m.chat.SetContent(m.transcript(width))
	if i >= 0 && i < len(m.chatStarts) {
		m.chat.SetYOffset(m.chatStarts[i])
		return
	}
	m.chat.SetYOffset(m.chatTurn)
}

// renderHead renders every committed message. Its result only changes when a
// turn completes or a tool call finishes.
//
// It also reports the line the newest exchange starts on — the last thing the
// user said, question or `!` command alike. The count is kept as the lines are
// written rather than measured afterwards, so a long session costs one pass
// rather than one per message.
func (m *Model) renderHead(width int) head {
	return m.renderHeadOf(m.mgr.Active(), width)
}

// head is a rendered transcript plus where each message begins in it.
type head struct {
	text string
	// starts[i] is the line message i begins on, recorded in the same pass
	// that writes the text. The count is already being kept; measuring it
	// afterwards would mean rendering the conversation twice.
	starts []int
	// turn is the line the newest exchange starts on — starts[the last user
	// message] — kept as a field so one place decides what "newest" means.
	turn int
}

// renderHeadOf is renderHead for a named conversation. The side chat is drawn
// in the same frame as the main one, so which session is being rendered has to
// be said rather than assumed.
func (m *Model) renderHeadOf(s *session.Session, width int) head {
	var b strings.Builder
	b.Grow(1024 + len(s.Messages)*160)

	if len(s.Messages) == 0 {
		b.WriteString(m.welcome(width))
	}
	// One turn is one block, however many round trips it took, and it keeps
	// only its last few calls. Both are decided over the whole run of steps at
	// once rather than message by message.
	plans := planCalls(s.Messages, callsShown, m.showAllCalls)

	h := head{starts: make([]int, len(s.Messages))}
	lines := 0
	for i := range s.Messages {
		if i > 0 && plans[i].head {
			b.WriteByte('\n')
			lines++
		}
		h.starts[i] = lines
		if s.Messages[i].Role == session.RoleUser {
			h.turn = lines
		}
		at := b.Len()
		m.renderMessage(&b, &s.Messages[i], width, plans[i])
		lines += strings.Count(b.String()[at:], "\n")
	}
	h.text = b.String()
	return h
}

// toolStateKey changes whenever a tool call flips from running to done, so the
// cache invalidates exactly when the transcript actually changes.
func toolStateKey(s *session.Session) string {
	var done, total int
	for i := range s.Messages {
		for _, t := range s.Messages[i].Tools {
			total++
			if t.Done {
				done++
			}
		}
	}
	return strconv.Itoa(done) + "/" + strconv.Itoa(total)
}

// isStep reports whether a message is one round trip of an agent's turn, as
// opposed to something the user did.
func isStep(msg session.Message) bool {
	return msg.Role == session.RoleAssistant && msg.Shell == nil
}

// renderMessage draws one message, following the plan made for its turn: where
// the heading goes, and which of its calls were folded away.
func (m *Model) renderMessage(b *strings.Builder, msg *session.Message, width int, plan callPlan) {
	stamp := m.st.Faint.Render(msg.At.Format("15:04"))

	if msg.Shell != nil {
		m.renderShell(b, msg.Shell, stamp, width)
		return
	}
	if msg.Role == session.RoleUser {
		m.block(b, m.st.UserBar, m.st.UserTag.Render("you"), stamp, msg.Text, width)
		m.renderFiles(b, msg.Files, m.st.UserBar, width)
		return
	}

	if plan.head {
		m.blockMD(b, m.st.AgentBar, m.st.AgentTag.Render("agent"), stamp, msg.Text, width)
	} else {
		m.blockBody(b, m.st.AgentBar, msg.Text, width)
	}

	if plan.note > 0 {
		b.WriteString(m.foldedLine(plan.note))
		b.WriteByte('\n')
	}
	for i, t := range msg.Tools {
		if i < plan.skip {
			continue
		}
		m.renderTool(b, t, width)
		// An edit says what it changed. "edited (1 replacement)" is the same
		// report whether the right line was changed or the wrong one.
		m.renderEdit(b, t, width, m.showAllCalls)
	}
	if msg.Err != "" {
		m.block(b, m.st.Bad, m.st.ErrTag.Render("error"), "", msg.Err, width)
	}
}

// renderShell draws a command the user ran with `!`.
//
// Output is left unwrapped and clipped instead: command output is columnar —
// a `ls -l`, a test summary, a table — and wrapping it the way prose is
// wrapped destroys the alignment that makes it readable at a glance.
func (m *Model) renderShell(b *strings.Builder, run *session.ShellRun, stamp string, width int) {
	bar := m.st.ShellBar.Render("▎")
	head := bar + " " + m.st.ShellTag.Render(run.Where+" $")
	if stamp != "" {
		head += "  " + stamp
	}
	if run.Done && run.Exit != 0 {
		head += "  " + m.st.Bad.Render("exit "+strconv.Itoa(run.Exit))
	}
	if run.Done && run.Elapsed > 0 {
		head += "  " + m.st.Faint.Render(shortDur(run.Elapsed))
	}
	b.WriteString(head + "\n")
	b.WriteString(bar + " " + m.st.Body.Render(truncate(run.Command, max(10, width-2))) + "\n")

	if !run.Done {
		note := "running…"
		if el := running(run.Started); el != "" {
			note += "  " + el
		}
		b.WriteString(bar + " " + m.st.Faint.Render(note) + "\n")
		return
	}
	body := strings.TrimRight(run.Output, "\n")
	if strings.TrimSpace(body) == "" {
		b.WriteString(bar + " " + m.st.Faint.Render("(no output)") + "\n")
		return
	}
	for _, l := range strings.Split(body, "\n") {
		// A tab is one character and four columns, and a line measured by the
		// first and drawn by the second runs past the rail beside it. Command
		// output is full of them: a test summary is a table made of tabs.
		b.WriteString(bar + " " + m.st.Dim.Render(truncate(highlight.ExpandTabs(l),
			max(10, width-2))) + "\n")
	}
}

// renderFiles lists what the user attached to a message. The image itself
// cannot be drawn here, so the line says what it was and where it went — the
// second half matters, because that path is what the agent was given.
func (m *Model) renderFiles(b *strings.Builder, files []session.Attachment, rail lipgloss.Style, width int) {
	bar := rail.Render("▎")
	for i, f := range files {
		line := bar + " " + m.st.MdMark.Render("[image #"+strconv.Itoa(i+1)+"]") + " " +
			m.st.Dim.Render(byteSize(f.Bytes))
		if where := f.Where(); where != "" {
			line += m.st.Faint.Render("  " + truncate(where, max(10, width-22)))
		}
		b.WriteString(line + "\n")
	}
}

// renderTool draws one tool call as a compact single line plus, for failures,
// the first few lines of output.
func (m *Model) renderTool(b *strings.Builder, t session.ToolCall, width int) {
	icon, style := "⋯", m.st.Dim
	switch {
	case t.Chosen != "":
		icon, style = "◆", m.st.Accent
	case t.Denied:
		icon, style = "✗", m.st.Bad
	case t.Done && t.IsError:
		icon, style = "!", m.st.Bad
	case t.Done:
		icon, style = "✓", m.st.Good
	}

	// The tail is built first, because it is the part worth keeping. A command
	// squeezed to fit still says what was run; an outcome cut in half says
	// nothing at all, and a line that does not fit wraps onto the next one and
	// turns a list of calls into twice as many rows of debris.
	tail := ""
	// What came back, in a few words. A transcript that only says "✓ read_file
	// chat.go" reports that the agent did something; the number of lines it got
	// is the part that tells you whether it read what you meant.
	if out := t.Outcome(); out != "" {
		tail += "  " + m.st.Good.Render(out)
	}
	switch {
	case t.Done && t.Elapsed > 0:
		tail += "  " + m.st.Faint.Render(shortDur(t.Elapsed))
	case !t.Done:
		// The finished calls say what they took; the one being waited on is
		// the one worth asking.
		if el := running(m.runStartOf(t)); el != "" {
			tail += "  " + m.st.Dim.Render(el)
		}
	}

	// The rail runs down the calls too. They are steps of the turn above them,
	// and a block whose middle is unrailed reads as three things rather than
	// one.
	bar := m.st.AgentBar.Render("▎")
	head := bar + "  " + style.Render(icon) + " " + m.st.ToolTag.Render(t.Name)
	line := head + tail
	if sum := t.Summary(); sum != "" {
		room := width - lipgloss.Width(head) - lipgloss.Width(tail) - 2
		line = head + "  " + m.st.Dim.Render(truncate(sum, max(8, room))) + tail
	}
	b.WriteString(clipLine(line, width))
	b.WriteByte('\n')

	if t.Done && (t.IsError || t.Denied) && t.Result != "" {
		for i, l := range strings.Split(t.Result, "\n") {
			if i >= 4 {
				b.WriteString(bar + "     " + m.st.Faint.Render("…") + "\n")
				break
			}
			if strings.TrimSpace(l) == "" {
				continue
			}
			b.WriteString(bar + "     " + m.st.Faint.Render(truncate(
				highlight.ExpandTabs(l), max(10, width-8))) + "\n")
		}
	}
}

// block writes a rail-prefixed paragraph of plain text. What the user typed,
// and what an engine failed with, is shown as written rather than interpreted.
func (m *Model) block(b *strings.Builder, rail lipgloss.Style, tag, stamp, body string, width int) {
	bar := m.blockHead(b, rail, tag, stamp)
	if strings.TrimSpace(body) == "" {
		return
	}
	wrapped := lipgloss.Wrap(body, max(10, width-2), " ")
	for _, l := range strings.Split(wrapped, "\n") {
		// Styled explicitly rather than left to the terminal default, which is
		// what made message text wash out against a dark background.
		b.WriteString(bar + " " + m.st.Body.Render(l) + "\n")
	}
}

// blockMD writes a rail-prefixed answer, rendering its markdown for the
// terminal instead of printing the syntax the model wrote.
func (m *Model) blockMD(b *strings.Builder, rail lipgloss.Style, tag, stamp, body string, width int) {
	m.blockHead(b, rail, tag, stamp)
	m.blockBody(b, rail, body, width)
}

// blockBody writes an answer with no heading above it, for a step that
// continues the block it is in.
func (m *Model) blockBody(b *strings.Builder, rail lipgloss.Style, body string, width int) {
	if strings.TrimSpace(body) == "" {
		return
	}
	bar := rail.Render("▎")
	for _, l := range renderMarkdown(m.st, m.sc, body, max(10, width-2)) {
		if l == "" {
			b.WriteString(bar + "\n")
			continue
		}
		b.WriteString(bar + " " + l + "\n")
	}
}

// blockHead writes the tag line and returns the rail every body line hangs
// off, so the plain and markdown bodies below it start from the same place.
func (m *Model) blockHead(b *strings.Builder, rail lipgloss.Style, tag, stamp string) string {
	bar := rail.Render("▎")
	head := bar + " " + tag
	if stamp != "" {
		head += "  " + stamp
	}
	b.WriteString(head)
	b.WriteByte('\n')
	return bar
}

func (m *Model) welcome(width int) string {
	st := m.st
	rows := []string{
		st.Accent.Render("agent-tui") + st.Dim.Render("  ·  "+m.projectName()),
		"",
		st.Dim.Render("Ask a question, or start with a shortcut:"),
		"",
		kv(st, "!<cmd>", "run it here instead of asking"),
		kv(st, "ctrl+p", "fuzzy-find a file"),
		kv(st, "ctrl+f", "search file contents"),
		kv(st, "/new", "start a session  ·  /fork to branch this one"),
		kv(st, "ctrl+t", "the same, as a shortcut"),
		kv(st, "tab", "move between panes"),
		kv(st, "f1", "all shortcuts"),
	}
	_ = width
	return strings.Join(rows, "\n") + "\n"
}

func kv(st *theme.Styles, k, v string) string {
	return "  " + st.StatusKey.Render(" "+k+" ") + " " + st.Dim.Render(v)
}

// truncate cuts s to w display columns, ending it with an ellipsis.
//
// Both halves of that have to be measured the same way. This used to decide
// whether to cut by display width and then cut by counting runes, which agree
// only for text that is entirely half-width: a line of Japanese came back
// nearly twice the width it was asked for, ran past its column and wrapped
// into the one beside it, so a diff spilled into the commit list next to it.
func truncate(s string, w int) string {
	if w <= 1 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	// ansi.Truncate counts columns and will not split a wide rune or an escape
	// sequence, so the result is at most w-1 columns and the ellipsis fits.
	return ansi.Truncate(s, w-1, "") + "…"
}

func shortDur(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return "<1ms"
	case d < time.Second:
		return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
	default:
		return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + "s"
	}
}

// runStartOf is when a call started, for the one that is still running.
//
// The session tracks a single one because a single one runs at a time — the
// same reason it keeps one buffer of live output, and the same field says
// which call both belong to.
func (m *Model) runStartOf(t session.ToolCall) time.Time {
	s := m.mgr.Active()
	if t.ID != "" && t.ID == s.OutputID {
		return s.RunAt
	}
	return time.Time{}
}

// ticking reports whether anything on screen has a clock on it.
//
// A `!` command does not make its session busy — you can go on asking while
// one runs — so "is the agent working" is not the question. The question is
// whether a number would change if the screen were drawn again.
func (m *Model) ticking() bool {
	for _, s := range m.mgr.All() {
		if s.Busy || s.Running > 0 {
			return true
		}
	}
	return false
}

// clockKey changes once a second while this session has a clock running, and
// is empty the rest of the time.
//
// The transcript's committed half is cached, and a running call's line lives
// in it — so without this its clock is drawn once, at zero, and stays there.
// With it, a session that is doing something redraws once a second and one
// that is not never redraws at all.
func clockKey(s *session.Session) string {
	if s.RunAt.IsZero() && s.Running == 0 {
		return ""
	}
	return strconv.FormatInt(time.Now().Unix(), 10)
}
