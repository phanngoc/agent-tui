package engine

import (
	"encoding/json"
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
		argv:   claudeArgv,
		newDec: func() decoder { return &claudeDec{} },
	}
}

func claudeArgv(c *CLI, t agent.Turn, br *broker) []string {
	a := []string{
		"-p",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
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
			"--mcp-config", br.MCPConfig(),
			"--permission-prompt-tool", br.ToolRef())
	case t.Mode == agent.ModeAsk:
		// Asking was wanted but no broker came up. Deny rather than silently
		// widening what the CLI may do.
		a = append(a, "--permission-mode", "manual", "--permission-prompts", "none")
	case t.Mode == agent.ModeFull:
		a = append(a, "--dangerously-skip-permissions")
	default:
		a = append(a, "--permission-mode", "acceptEdits")
	}
	return append(a, t.Prompt)
}

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
}

func (d *claudeDec) failure() string { return d.fail }

func (d *claudeDec) line(raw []byte, emit func(agent.Event)) {
	var ev struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id"`
		TaskID    string `json:"task_id"`
		TaskDesc  string `json:"description"`
		TaskSum   string `json:"summary"`
		Status    string `json:"status"`
		OutFile   string `json:"output_file"`
		Patch     struct {
			Status string `json:"status"`
		} `json:"patch"`
		Event   json.RawMessage `json:"event"`
		Message json.RawMessage `json:"message"`
		IsError bool            `json:"is_error"`
		Result  string          `json:"result"`
		Usage   claudeUsage     `json:"usage"`
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
		case "permission_denied":
			emit(agent.EvStatus{Text: "permission denied"})
		case "task_started":
			emit(agent.EvTask{
				ID:    ev.TaskID,
				Label: firstNonEmpty(ev.TaskDesc, ev.TaskSum, "background command"),
				State: "running",
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
		d.streamEvent(ev.Event, emit)

	case "assistant":
		d.assistant(ev.Message, emit)

	case "user":
		// A tool result closes out the assistant turn that requested it, so the
		// message must be committed before the result is reported.
		d.flush(emit)
		d.toolResults(ev.Message, emit)

	case "result":
		d.flush(emit)
		emit(agent.EvUsage{
			In:        ev.Usage.InputTokens,
			Out:       ev.Usage.OutputTokens,
			CacheRead: ev.Usage.CacheReadInputTokens,
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
	InputTokens          int64 `json:"input_tokens"`
	OutputTokens         int64 `json:"output_tokens"`
	CacheReadInputTokens int64 `json:"cache_read_input_tokens"`
}

func (d *claudeDec) streamEvent(raw json.RawMessage, emit func(agent.Event)) {
	var e struct {
		Type  string `json:"type"`
		Delta struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
	}
	if json.Unmarshal(raw, &e) != nil || e.Type != "content_block_delta" {
		return
	}
	switch e.Delta.Type {
	case "text_delta":
		if e.Delta.Text != "" {
			emit(agent.EvTextDelta{Text: e.Delta.Text})
		}
	case "thinking_delta":
		emit(agent.EvThinkingDelta{Text: e.Delta.Thinking})
	}
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
		emit(agent.EvToolDone{Call: session.ToolCall{
			ID:      b.ToolUseID,
			Result:  flattenContent(b.Content),
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
	msg.Tools = d.tools
	emit(agent.EvAssistant{Message: msg})
	for _, c := range d.tools {
		emit(agent.EvToolStart{Call: c})
	}
	d.msg = session.Message{}
	d.tools = nil
}
