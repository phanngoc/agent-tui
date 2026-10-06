package telegram

import (
	"encoding/json"
	"fmt"
	"net/url"
	pathpkg "path"
	"sort"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/gateway"
	"github.com/phanngoc/agent-tui/internal/session"
)

// How a turn shows in Telegram while it runs, after OpenClaw's channel
// (docs.openclaw.ai/channels/telegram/messaging):
//
//   - progress (the default): one message, edited as the turn goes — a
//     headline with the time and the step count, what the agent is thinking
//     or saying, and a line per tool call with what it touches, ⏳ while it
//     runs, ✅ or ❌ after, and how long it took. The answer comes after as
//     a message of its own; the progress message folds into a one-line
//     summary of what was done, rather than vanishing.
//   - partial: the answer itself streams into the message, under the
//     headline; a short answer ends there, a long one carries on in more.
//   - block: like progress, and what the agent says between its tool calls
//     comes as messages of their own, as it says them.
//   - off: the typing indicator, then the answer.
//
// Edits come at most once a second; the first message waits a moment, so a
// quick answer arrives alone, without a progress message flashing past.

// Streaming modes.
const (
	StreamProgress = "progress"
	StreamPartial  = "partial"
	StreamBlock    = "block"
	StreamOff      = "off"
)

// ResolveStreaming spells out the default.
func ResolveStreaming(m string) string {
	switch m {
	case StreamProgress, StreamPartial, StreamBlock, StreamOff:
		return m
	}
	return StreamProgress
}

const (
	shownSteps   = 8
	editEvery    = time.Second
	firstPreview = 1500 * time.Millisecond
)

type stepState int

const (
	stepRunning stepState = iota
	stepOK
	stepFailed
)

type step struct {
	id    string
	name  string
	icon  string
	label string
	state stepState
	start time.Time
	took  time.Duration
}

// progress is a turn as Telegram shows it: built from the gateway's events,
// rendered as Telegram HTML. It holds no Telegram state of its own.
type progress struct {
	mode     string
	started  time.Time
	status   string
	steps    []step
	byID     map[string]int
	thinking string    // the latest of the reasoning
	thinkAt  time.Time // when it last grew
	partial  string    // the text being written now
	said     string    // what the agent last said between tool calls
	answer   string    // its last whole message
	failed   string
}

func newProgress(mode string, now time.Time) *progress {
	return &progress{mode: ResolveStreaming(mode), started: now, byID: map[string]int{}}
}

// apply takes one event of the turn; it says whether the view changed, and
// returns an intermediate message to send on its own (block mode).
func (p *progress) apply(e gateway.Event, now time.Time) (changed bool, block string) {
	switch e.Type {
	case gateway.EvStatus:
		var d gateway.TextData
		_ = json.Unmarshal(e.Data, &d)
		if d.Text != "" && d.Text != p.status {
			p.status = d.Text
			return true, ""
		}
	case gateway.EvThinkingDelta:
		var d gateway.TextData
		_ = json.Unmarshal(e.Data, &d)
		if d.Text != "" {
			p.thinking = tailOf(p.thinking+d.Text, 600)
			p.thinkAt = now
			return true, ""
		}
	case gateway.EvTextDelta:
		var d gateway.TextData
		_ = json.Unmarshal(e.Data, &d)
		if d.Text != "" {
			p.partial += d.Text
			return true, ""
		}
	case gateway.EvToolStart:
		var d gateway.ToolData
		_ = json.Unmarshal(e.Data, &d)
		if i, ok := p.byID[d.Call.ID]; ok && p.steps[i].state == stepRunning {
			// Named when the agent wrote the call; timed from when it runs.
			p.steps[i].start = now
		}
		p.startStep(d.Call, now)
		return true, ""
	case gateway.EvToolDone:
		var d gateway.ToolData
		_ = json.Unmarshal(e.Data, &d)
		i, ok := p.byID[d.Call.ID]
		if !ok {
			p.startStep(d.Call, now)
			i = p.byID[d.Call.ID]
		}
		s := &p.steps[i]
		s.took = now.Sub(s.start)
		s.state = stepOK
		if d.Call.IsError {
			s.state = stepFailed
		}
		return true, ""
	case gateway.EvMessage:
		var d gateway.MessageData
		_ = json.Unmarshal(e.Data, &d)
		m := d.Message
		if m.Role != session.RoleAssistant {
			return false, ""
		}
		text := strings.TrimSpace(m.Text)
		p.partial = ""
		for _, c := range m.Tools {
			if _, seen := p.byID[c.ID]; !seen && !c.Done {
				p.startStep(c, now)
			}
		}
		if text == "" {
			return true, ""
		}
		p.answer = text
		if len(m.Tools) > 0 {
			// Said on the way, before more tool calls: commentary.
			p.said = text
			if p.mode == StreamBlock {
				return true, text
			}
		}
		return true, ""
	}
	return false, ""
}

func (p *progress) startStep(c session.ToolCall, now time.Time) {
	if _, ok := p.byID[c.ID]; ok && c.ID != "" {
		return
	}
	icon, label := describeTool(c)
	p.byID[c.ID] = len(p.steps)
	p.steps = append(p.steps, step{id: c.ID, name: toolName(c.Name), icon: icon, label: label, start: now})
}

// worth says the progress message is worth sending yet: something is
// happening that a quick answer would not show anyway.
func (p *progress) worth(now time.Time) bool {
	if p.mode == StreamOff {
		return false
	}
	if len(p.steps) > 0 {
		return true
	}
	if p.mode == StreamPartial {
		return len(p.partial) >= 40 && now.Sub(p.started) >= firstPreview/2
	}
	return now.Sub(p.started) >= firstPreview
}

// render is the message while the turn runs.
func (p *progress) render(now time.Time) string {
	var b strings.Builder
	head := "Working"
	if s := strings.TrimSpace(p.status); s != "" && !strings.EqualFold(s, "working") {
		head = strings.ToUpper(s[:1]) + s[1:]
	}
	fmt.Fprintf(&b, "⏳ <b>%s</b> · %s", esc(clip(head, 60)), since(now.Sub(p.started)))
	if n := len(p.steps); n > 0 {
		fmt.Fprintf(&b, " · %s", plural(n, "step"))
	}

	if p.mode == StreamPartial {
		// The answer itself, as it is written, under the last few steps.
		for _, s := range p.lastSteps(3) {
			b.WriteString("\n" + s.line(now))
		}
		if text := strings.TrimSpace(p.partial); text != "" {
			if len(text) > 3000 {
				text = "…" + text[len(text)-3000:]
			}
			b.WriteString("\n\n" + HTML(text) + " ▍")
		}
		return b.String()
	}

	// What it is thinking, while that is the newest thing it does.
	running := p.running()
	if p.thinking != "" && !running && (len(p.steps) == 0 || p.thinkAt.After(p.steps[len(p.steps)-1].start)) {
		b.WriteString("\n🧠 <i>" + esc(lastSentence(p.thinking, 160)) + "</i>")
	}
	// What it says between tool calls, or is saying now.
	if live := strings.TrimSpace(p.partial); live != "" && p.mode != StreamBlock {
		b.WriteString("\n💬 <i>" + esc(lastSentence(live, 220)) + "</i>")
	} else if p.said != "" && p.mode != StreamBlock {
		b.WriteString("\n💬 <i>" + esc(clip(oneLine(p.said), 220)) + "</i>")
	}
	if len(p.steps) > 0 {
		b.WriteString("\n")
		if hidden := len(p.steps) - shownSteps; hidden > 0 {
			b.WriteString("\n<i>… " + plural(hidden, "earlier step") + "</i>")
		}
		for _, s := range p.lastSteps(shownSteps) {
			b.WriteString("\n" + s.line(now))
		}
	}
	return b.String()
}

func (p *progress) running() bool {
	for _, s := range p.steps {
		if s.state == stepRunning {
			return true
		}
	}
	return false
}

func (p *progress) lastSteps(n int) []step {
	if len(p.steps) <= n {
		return p.steps
	}
	return p.steps[len(p.steps)-n:]
}

func (s step) line(now time.Time) string {
	mark := "⏳"
	took := now.Sub(s.start)
	switch s.state {
	case stepOK:
		mark, took = "✅", s.took
	case stepFailed:
		mark, took = "❌", s.took
	}
	line := mark + " " + s.icon + " " + esc(s.name)
	if s.label != "" {
		line += " <code>" + esc(s.label) + "</code>"
	}
	if took >= 2*time.Second || s.state == stepRunning && took >= time.Second {
		line += " <i>" + since(took) + "</i>"
	}
	return line
}

// summary is the progress message once the turn is over: one line of what
// was done.
func (p *progress) summary(now time.Time, errText string) string {
	var b strings.Builder
	switch {
	case errText == "":
		b.WriteString("✅ <b>Done</b>")
	case strings.Contains(errText, "context canceled"):
		b.WriteString("⏹ <b>Stopped</b>")
	default:
		b.WriteString("⚠️ <b>Failed</b>")
	}
	fmt.Fprintf(&b, " · %s", since(now.Sub(p.started)))
	if n := len(p.steps); n > 0 {
		fmt.Fprintf(&b, " · %s", plural(n, "step"))
		count := map[string]int{}
		failed := 0
		for _, s := range p.steps {
			count[s.icon+" "+s.name]++
			if s.state == stepFailed {
				failed++
			}
		}
		kinds := make([]string, 0, len(count))
		for k := range count {
			kinds = append(kinds, k)
		}
		sort.Slice(kinds, func(i, j int) bool {
			if count[kinds[i]] != count[kinds[j]] {
				return count[kinds[i]] > count[kinds[j]]
			}
			return kinds[i] < kinds[j]
		})
		var parts []string
		for i, k := range kinds {
			if i == 5 {
				parts = append(parts, "…")
				break
			}
			if count[k] > 1 {
				parts = append(parts, fmt.Sprintf("%s ×%d", esc(k), count[k]))
			} else {
				parts = append(parts, esc(k))
			}
		}
		b.WriteString("\n" + strings.Join(parts, " · "))
		if failed > 0 {
			fmt.Fprintf(&b, "\n<i>%s failed</i>", plural(failed, "step"))
		}
	}
	return b.String()
}

// describeTool names a tool call's kind with an emoji, and what it touches
// in a few words.
func describeTool(c session.ToolCall) (icon, label string) {
	var in map[string]any
	_ = json.Unmarshal(c.Input, &in)
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := in[k].(string); ok && strings.TrimSpace(v) != "" {
				return v
			}
		}
		return ""
	}
	n := strings.ToLower(c.Name)
	switch {
	case strings.HasPrefix(n, "mcp__"):
		return "🔌", ""
	case n == "bash" || n == "shell" || n == "powershell" || strings.Contains(n, "exec") || n == "run_command":
		return "💻", clip(oneLine(str("command", "cmd")), 70)
	case strings.Contains(n, "edit") || strings.Contains(n, "write") || n == "apply_patch":
		return "✏️", shortPath(str("file_path", "path", "notebook_path"))
	case strings.Contains(n, "read") || n == "view" || n == "cat":
		return "📖", shortPath(str("file_path", "path"))
	case strings.Contains(n, "grep") || strings.Contains(n, "search") && !strings.Contains(n, "web"):
		label = str("pattern", "query")
		if p := str("path", "glob"); p != "" {
			label += " · " + shortPath(p)
		}
		return "🔎", clip(label, 60)
	case strings.Contains(n, "glob") || n == "ls" || n == "list" || strings.Contains(n, "find"):
		return "🔎", clip(str("pattern", "path"), 60)
	case strings.Contains(n, "web") || strings.Contains(n, "fetch") || strings.Contains(n, "browse"):
		if u := str("url"); u != "" {
			if pu, err := url.Parse(u); err == nil && pu.Host != "" {
				return "🌐", clip(pu.Host+pu.Path, 60)
			}
		}
		return "🌐", clip(str("query", "url"), 60)
	case n == "agent" || n == "task":
		return "🤖", clip(str("description", "prompt"), 60)
	case strings.Contains(n, "todo"):
		return "📝", ""
	case strings.Contains(n, "skill"):
		return "🧩", clip(str("skill", "name", "command"), 40)
	}
	return "🔧", clip(oneLine(str("command", "file_path", "path", "pattern", "query", "url", "description")), 60)
}

// toolName is how a step names its tool: an MCP tool by server and tool.
func toolName(n string) string {
	if rest, ok := strings.CutPrefix(n, "mcp__"); ok {
		if server, tool, ok := strings.Cut(rest, "__"); ok {
			return server + " · " + tool
		}
		return rest
	}
	return n
}

func shortPath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	if p == "" {
		return ""
	}
	dir, file := pathpkg.Split(strings.TrimRight(p, "/"))
	parent := pathpkg.Base(strings.TrimRight(dir, "/"))
	if parent == "." || parent == "/" || parent == "" {
		return file
	}
	return parent + "/" + file
}

func since(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// lastSentence is the end of a running text, from a sentence's start where
// one is near enough, so the line reads as words rather than a cut.
func lastSentence(s string, n int) string {
	s = oneLine(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	r = r[len(r)-n:]
	cut := string(r)
	for _, sep := range []string{". ", "? ", "! ", "\n"} {
		if i := strings.Index(cut, sep); i >= 0 && i < len(cut)/2 {
			return strings.TrimSpace(cut[i+len(sep):])
		}
	}
	return "…" + strings.TrimSpace(cut)
}
