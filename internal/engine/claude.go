package engine

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/phanngoc/agent-tui/internal/agent"
	"github.com/phanngoc/agent-tui/internal/session"
	"github.com/phanngoc/agent-tui/internal/vfs"
)

// newClaude drives the Claude Code CLI. It is the only engine here that can ask
// before it acts: --permission-prompt-tool routes every prompt to an MCP tool,
// which the broker answers from this UI.
func newClaude(root string) *CLI {
	return &CLI{
		id: IDClaude, label: "Claude Code", bin: "claude",
		root: root, fs: vfs.NewLocal(root), approvals: true,
		streamIn: true,
		// A sub-agent's words between its calls, which say what it is doing
		// better than its last tool does. An environment variable rather than
		// --forward-subagent-text, which a CLI too old to know it refuses.
		env:    []string{"CLAUDE_CODE_FORWARD_SUBAGENT_TEXT=1"},
		argv:   claudeArgv,
		newDec: func() decoder { return &claudeDec{} },
	}
}

func claudeArgv(c *CLI, t agent.Turn, br *broker) []string {
	a := []string{
		"-p",
		// The prompt goes in on stdin, and so do messages sent while the
		// turn runs: Claude Code hands them to the model as soon as the
		// running tool calls finish, within the same turn, as its own UI
		// does with queued messages (measured against 2.1.290). It also
		// keeps the prompt off a Windows command line, which ends at 32767.
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
		// Under -p the thinking deltas arrive empty — a token estimate and no
		// words — unless this is on. Measured against 2.1.287: without it a
		// short question streamed 0 deltas with text in them, with it 21. The
		// words are what tell you where a long turn is.
		"--settings", `{"showThinkingSummaries":true}`,
		// agent-tui schedules work itself (internal/schedule). Claude Code's
		// own schedulers would compete with it, out of sight: its /schedule
		// skill and RemoteTrigger make cloud routines on the user's account,
		// and its cron tools, /loop and ScheduleWakeup die with this -p
		// process. Asked to run something hourly, the agent took the first,
		// so they are withheld and the schedule tool agent-tui serves is the
		// one there is. The flag takes a list, so it must not come last,
		// where it would swallow the prompt.
		"--disallowedTools", strings.Join(claudeWithheld, ","),
	}
	if sys := t.System; sys != "" {
		// A Windows command line holds 32767 characters in all; the
		// project's context is cut well short of that rather than losing the
		// whole turn to "the filename or extension is too long".
		if len(sys) > 16000 {
			sys = sys[:16000] + "\n…"
		}
		a = append(a, "--append-system-prompt", sys)
	}
	servers := map[string]any{}
	for k, v := range t.MCPServers {
		servers[k] = v
	}
	if br != nil && t.Mode != agent.ModePlan {
		servers[brokerServerName] = brokerFor(t.FS, br.server())
	}
	if len(servers) > 0 {
		cfg, _ := json.Marshal(map[string]any{"mcpServers": servers})
		// Followed by a flag of its own, since --mcp-config takes a list and
		// would swallow the prompt.
		a = append(a, "--mcp-config", string(cfg), "--verbose")
	}
	if dirs := addDirs(t); len(dirs) > 0 {
		// The folders besides the project that auto may change: Claude Code
		// then works in them as in the project, and its acceptEdits policy
		// settles an edit there without a prompt. It takes a list, so a flag
		// of its own follows.
		a = append(append(a, "--add-dir"), dirs...)
		a = append(a, "--verbose")
	}
	if t.Model != "" {
		a = append(a, "--model", t.Model)
	}
	if t.ExternalID != "" {
		a = append(a, "--resume", t.ExternalID)
		if t.Fork {
			// Branch into a new session id, leaving the original untouched.
			a = append(a, "--fork-session")
		}
	}

	// Claude Code has a permission mode of its own for each of ours.
	switch {
	case t.Mode == agent.ModePlan:
		a = append(a, "--permission-mode", "plan")
	case br != nil:
		// The broker answers whatever Claude Code decides to ask about. In ask
		// mode that is everything; in auto mode its own acceptEdits policy
		// settles work inside the project first, and only the rest arrives.
		mode := "manual"
		if !t.Mode.Confirms() {
			mode = "acceptEdits"
		}
		a = append(a,
			"--permission-mode", mode,
			"--permission-prompts", "host",
			"--permission-prompt-tool", br.ToolRef())
	case t.Mode == agent.ModeAsk:
		// Asking was wanted but no broker came up. Deny rather than silently
		// widening what the CLI may do.
		a = append(a, "--permission-mode", "manual", "--permission-prompts", "none")
	case t.Mode == agent.ModeFull:
		a = append(a, "--dangerously-skip-permissions")
	default:
		// Auto, with nobody to answer a prompt.
		//
		// Every other mode Claude Code offers still routes a shell command to a
		// permission prompt, and under -p there is no prompt to route it to:
		// measured against 2.1.272, acceptEdits, auto and dontAsk all refuse
		// `git --version` outright, and the turn comes back explaining that it
		// is waiting for an approval the session can never show. Auto says it
		// runs commands, so the only setting that keeps that promise is this
		// one.
		a = append(a, "--permission-mode", "bypassPermissions")
	}
	return a
}

// claudeWithheld are Claude Code's tools and skills that schedule work, which
// agent-tui does itself.
var claudeWithheld = []string{"Skill(schedule)", "Skill(loop)", "RemoteTrigger", "CronCreate", "CronDelete", "CronList", "ScheduleWakeup"}

// claudeDec parses Claude Code's stream-json output.
//
// Text and thinking arrive twice: once as partial stream_event deltas (which
// drive the live transcript) and once inside the assistant message (which is
// what gets committed). Assistant content also arrives in several events per
// message, so blocks are accumulated by message id and flushed when the id
// changes or a tool result lands.
type claudeDec struct {
	msgID string
	msg   session.Message
	tools []session.ToolCall
	fail  string
	sent  bool
	// cwd is where Claude Code says it is running, from its init event. Its
	// command output files are filed under it.
	cwd string
	// pending are the tool calls of the message being streamed, with their
	// input as far as it has arrived; pendingAt maps a content block's index
	// to its place in pending.
	pending   []session.ToolCall
	pendingAt map[int]int
	// agents are the sub-agents started in this turn, by their Agent call;
	// taskAgent finds that call from the engine's task id.
	agents    map[string]*subAgentState
	taskAgent map[string]string
	// limits is the account's allowance as last reported, handed on with
	// the turn's usage.
	limits []agent.Limit
}

func (d *claudeDec) failure() string { return d.fail }

func (d *claudeDec) line(raw []byte, emit func(agent.Event)) {
	var ev struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id"`
		CWD       string `json:"cwd"`
		TaskID    string `json:"task_id"`
		ToolUseID string `json:"tool_use_id"`
		ThinkTok  int    `json:"estimated_tokens_delta"`
		TaskDesc  string `json:"description"`
		TaskSum   string `json:"summary"`
		Status    string `json:"status"`
		OutFile   string `json:"output_file"`
		Patch     struct {
			Status string `json:"status"`
		} `json:"patch"`
		Event   json.RawMessage `json:"event"`
		Message json.RawMessage `json:"message"`
		// Parent is the Agent call of the sub-agent a message belongs to.
		Parent  string      `json:"parent_tool_use_id"`
		IsError bool        `json:"is_error"`
		Result  string      `json:"result"`
		Usage   claudeUsage `json:"usage"`
		// RateLimit arrives with each request: how much of the account's
		// five-hour and weekly allowance is spent.
		RateLimit *claudeRateLimit `json:"rate_limit_info"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return
	}

	switch ev.Type {
	case "system":
		if ev.SessionID != "" && !d.sent {
			d.sent = true
			emit(agent.EvSession{ExternalID: ev.SessionID})
		}
		switch ev.Subtype {
		case "init":
			if ev.CWD != "" {
				d.cwd = ev.CWD
			}
		case "permission_denied":
			emit(agent.EvStatus{Text: "permission denied"})
		case "status":
			// Sent as each request to the model goes out. Between a tool
			// finishing and the first token of the answer to it is the
			// stretch that otherwise looked like a stall on the tool.
			switch ev.Status {
			case "requesting":
				emit(agent.EvStatus{Text: "waiting for the model"})
			case "compacting":
				emit(agent.EvStatus{Text: "compacting the conversation"})
			}
		case "thinking_tokens":
			// The estimate arrives whether or not the words do.
			if ev.ThinkTok > 0 {
				emit(agent.EvThinkingDelta{Tokens: ev.ThinkTok})
			}
		case "task_started", "task_progress", "task_updated", "task_notification":
			if d.agentTask(raw, emit) {
				return // a sub-agent, not a command
			}
		}
		switch ev.Subtype {
		case "task_started":
			// Foreground commands arrive here too, not only background ones,
			// and both are written to a file while they run.
			emit(agent.EvTask{
				ID:      ev.TaskID,
				Label:   firstNonEmpty(ev.TaskDesc, ev.TaskSum, "background command"),
				State:   "running",
				Live:    claudeTaskFiles(d.cwd, ev.SessionID, ev.TaskID),
				ToolUse: ev.ToolUseID,
			})
		case "task_updated":
			emit(agent.EvTask{ID: ev.TaskID, State: claudeTaskState(ev.Patch.Status)})
		case "task_notification":
			emit(agent.EvTask{
				ID:     ev.TaskID,
				Label:  ev.TaskSum,
				State:  claudeTaskState(ev.Status),
				Output: ev.OutFile,
			})
		}

	case "stream_event":
		if ev.Parent != "" {
			return // a sub-agent's: its calls arrive whole, below
		}
		d.streamEvent(ev.Event, emit)

	case "assistant":
		if ev.Parent != "" {
			d.subMessage("assistant", ev.Parent, ev.Message, emit)
			return
		}
		d.assistant(ev.Message, emit)

	case "user":
		if ev.Parent != "" {
			d.subMessage("user", ev.Parent, ev.Message, emit)
			return
		}
		// A tool result closes out the assistant turn that requested it, so the
		// message must be committed before the result is reported.
		d.flush(emit)
		d.toolResults(ev.Message, emit)

	case "rate_limit_event":
		if ev.RateLimit != nil {
			if l := ev.RateLimit.limits(); len(l) > 0 {
				d.limits = l
			}
		}

	case "result":
		d.flush(emit)
		emit(agent.EvUsage{
			In:         ev.Usage.InputTokens,
			Out:        ev.Usage.OutputTokens,
			CacheRead:  ev.Usage.CacheReadInputTokens,
			CacheWrite: ev.Usage.CacheCreationInputTokens,
			Limits:     d.limits,
		})
		if ev.IsError {
			d.fail = firstNonEmpty(ev.Result, ev.Subtype, "the CLI reported an error")
		}
	}
}

func (d *claudeDec) finish(emit func(agent.Event)) { d.flush(emit) }

// claudeTaskState maps Claude Code's task vocabulary onto ours. Its background
// commands do not survive the process, so anything that is not plainly running
// has ended one way or another.
func claudeTaskState(s string) string {
	switch s {
	case "", "running", "in_progress":
		return "running"
	case "completed", "success", "done":
		return "done"
	case "failed", "error":
		return "failed"
	default:
		// killed, stopped, cancelled
		return "stopped"
	}
}

type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

// claudeRateLimit is a rate_limit_event's report. Every window is in
// unifiedWindows; the one it is about is also given on its own, which is
// all an older CLI sends.
type claudeRateLimit struct {
	Type    string  `json:"rateLimitType"`
	Used    float64 `json:"utilization"`
	Resets  int64   `json:"resetsAt"`
	Windows map[string]struct {
		Used   float64 `json:"utilization"`
		Resets int64   `json:"resetsAt"`
	} `json:"unifiedWindows"`
}

func (r claudeRateLimit) limits() []agent.Limit {
	var out []agent.Limit
	for name, w := range r.Windows {
		out = append(out, agent.Limit{Window: name, Used: w.Used, Resets: unixTime(w.Resets)})
	}
	if len(out) == 0 && r.Type != "" {
		out = append(out, agent.Limit{Window: r.Type, Used: r.Used, Resets: unixTime(r.Resets)})
	}
	slices.SortFunc(out, func(a, b agent.Limit) int { return cmp.Compare(a.Window, b.Window) })
	return out
}

func unixTime(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// streamEvent follows the message as the model writes it.
//
// Three moments matter to someone watching, and the stream has all three:
//
//   - a tool call being written (content_block_start, then its input arriving
//     as input_json_delta) — shown at once, half-typed, so a long command is
//     read as it is written rather than after it has run;
//   - the message ending (message_stop) — the moment Claude Code starts
//     running its calls, so it is when they are committed and their clocks
//     start. Waiting for the results instead put every call on screen already
//     finished, and a two-minute search looked like nothing at all;
//   - thinking, which carries words when showThinkingSummaries is set and a
//     token estimate either way.
func (d *claudeDec) streamEvent(raw json.RawMessage, emit func(agent.Event)) {
	var e struct {
		Type         string `json:"type"`
		Index        int    `json:"index"`
		ContentBlock struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return
	}
	switch e.Type {
	case "message_start":
		d.pending, d.pendingAt = nil, nil
	case "content_block_start":
		if e.ContentBlock.Type == "tool_use" {
			if d.pendingAt == nil {
				d.pendingAt = map[int]int{}
			}
			d.pendingAt[e.Index] = len(d.pending)
			d.pending = append(d.pending, session.ToolCall{ID: e.ContentBlock.ID, Name: e.ContentBlock.Name})
			emit(agent.EvToolPending{Calls: d.pendingCalls()})
		}
	case "content_block_delta":
		switch e.Delta.Type {
		case "text_delta":
			if e.Delta.Text != "" {
				emit(agent.EvTextDelta{Text: e.Delta.Text})
			}
		case "thinking_delta":
			if e.Delta.Thinking != "" {
				emit(agent.EvThinkingDelta{Text: e.Delta.Thinking})
			}
		case "input_json_delta":
			if i, ok := d.pendingAt[e.Index]; ok && e.Delta.PartialJSON != "" {
				d.pending[i].Input = append(d.pending[i].Input, e.Delta.PartialJSON...)
				emit(agent.EvToolPending{Calls: d.pendingCalls()})
			}
		}
	case "message_stop":
		// The calls run now, so they are on screen now, with a clock.
		if len(d.tools) > 0 {
			d.flush(emit)
		}
		d.pending, d.pendingAt = nil, nil
	}
}

// pendingCalls is a copy of the calls being written, for the UI to hold: the
// decoder goes on appending to its own.
func (d *claudeDec) pendingCalls() []session.ToolCall {
	out := make([]session.ToolCall, len(d.pending))
	for i, c := range d.pending {
		c.Input = append(json.RawMessage(nil), c.Input...)
		out[i] = c
	}
	return out
}

func (d *claudeDec) assistant(raw json.RawMessage, emit func(agent.Event)) {
	var m struct {
		ID      string `json:"id"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	if m.ID != d.msgID {
		d.flush(emit)
		d.msgID = m.ID
	}
	for _, b := range m.Content {
		switch b.Type {
		case "text":
			d.msg.Text += b.Text
		case "tool_use":
			d.tools = append(d.tools, session.ToolCall{
				ID: b.ID, Name: b.Name, Input: b.Input,
			})
			if isAgentTool(b.Name) {
				d.noteAgentCall(b.ID, "", b.Input, emit)
			}
		}
	}
}

func (d *claudeDec) toolResults(raw json.RawMessage, emit func(agent.Event)) {
	var m struct {
		Content []struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			IsError   bool            `json:"is_error"`
			Content   json.RawMessage `json:"content"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	for _, b := range m.Content {
		if b.Type != "tool_result" {
			continue
		}
		res := flattenContent(b.Content)
		if b.IsError {
			res = toolError(res)
		}
		emit(agent.EvToolDone{Call: session.ToolCall{
			ID:      b.ToolUseID,
			Result:  res,
			IsError: b.IsError,
			Done:    true,
		}})
	}
}

// flush commits the accumulated assistant turn.
func (d *claudeDec) flush(emit func(agent.Event)) {
	if d.msg.Text == "" && len(d.tools) == 0 {
		return
	}
	msg := d.msg
	msg.Role = session.RoleAssistant
	msg.At = time.Now()
	for i, c := range d.tools {
		if d.agents[c.ID] != nil {
			a := d.snapshot(c.ID, 0)
			d.tools[i].Agent = &a
		}
	}
	msg.Tools = d.tools
	emit(agent.EvAssistant{Message: msg})
	for _, c := range d.tools {
		emit(agent.EvToolStart{Call: c})
	}
	d.msg = session.Message{}
	d.tools = nil
}
